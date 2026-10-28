package perms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, home, rel, s string) {
	t.Helper()
	p := filepath.Join(home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func risky(r Report) []string {
	var out []string
	for _, x := range r.Rules {
		if x.Risky != "" {
			out = append(out, x.What)
		}
	}
	return out
}

func short(home string) Short {
	return func(p string) string { return strings.Replace(p, home, "~", 1) }
}

func TestClaude(t *testing.T) {
	home := t.TempDir()
	write(t, home, ".claude/settings.json", `{"env":{"SECRET":"never read"},"permissions":{"allow":["Bash","Read","Bash(git status:*)","Bash(rm -rf:*)"],"deny":["Bash(curl:*)"],"defaultMode":"bypassPermissions"}}`)
	write(t, home, ".claude/settings.local.json", `{"permissions":{"ask":["WebFetch"]}}`)
	write(t, home, ".claude.json", `{"oauthAccount":{"token":"x"},"projects":{"/p":{"allowedTools":["Write"]}}}`)
	r := Read("claude", home, short(home))
	if r.Err != "" {
		t.Fatal(r.Err)
	}
	if got := strings.Join(risky(r), "|"); got != "Bash|Bash(rm -rf:*)|defaultMode = bypassPermissions|Write" {
		t.Fatalf("risky: %s", got)
	}
	for _, x := range r.Rules {
		if strings.Contains(x.What, "SECRET") || strings.Contains(x.What, "token") {
			t.Fatal("read a key that isn't a permission")
		}
	}
}

func TestCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	write(t, home, ".codex/config.toml", "model = \"m\"\napproval_policy = \"never\"\n[projects.\""+home+"\"]\ntrust_level = \"trusted\"\n[projects.\"/work\"]\ntrust_level = \"trusted\"\n[mcp_servers.x.env]\nTOKEN = \"secret\"\n")
	write(t, home, ".codex/rules/default.rules", "prefix_rule(pattern=[\"git\"], decision=\"allow\")\nprefix_rule(pattern=[\"npm\", \"test\"], decision=\"allow\")\n")
	r := Read("codex", home, short(home))
	if r.Err != "" {
		t.Fatal(r.Err)
	}
	if got := strings.Join(risky(r), "|"); got != "approval_policy = never|~|git" {
		t.Fatalf("risky: %s", got)
	}
}

func TestOpenCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	r := Read("opencode", home, short(home))
	if len(risky(r)) != 1 {
		t.Fatalf("no config means the allow defaults: %+v", r)
	}
	write(t, home, ".config/opencode/opencode.jsonc", "{\n  // a comment\n  \"permission\": {\"edit\": \"ask\", \"bash\": {\"*\": \"allow\", \"rm *\": \"deny\"},},\n}\n")
	r = Read("opencode", home, short(home))
	if r.Err != "" {
		t.Fatal(r.Err)
	}
	if got := strings.Join(risky(r), "|"); got != "bash *|webfetch" {
		t.Fatalf("risky: %s", got)
	}
}

func TestPi(t *testing.T) {
	r := Read("pi", t.TempDir(), func(s string) string { return s })
	if len(risky(r)) != 1 || !strings.Contains(r.Note, "no approval") {
		t.Fatalf("%+v", r)
	}
}
