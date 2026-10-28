package kinds_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/testkit"
)

func setup(t *testing.T) (*testkit.Fixture, *engine.Ctx) {
	t.Helper()
	f := testkit.New(t)
	f.Use(t)
	return f, f.Ctx(t, nil)
}

func check(t *testing.T, c *engine.Ctx, id string) engine.Status {
	t.Helper()
	c.Forget()
	return engine.Check(c, id)
}

func want(t *testing.T, s engine.Status, st engine.State, detail string) {
	t.Helper()
	if s.State != st || !strings.Contains(s.Detail, detail) {
		t.Fatalf("%s: got %s %q, want %s with %q", s.ID, s.State, s.Detail, st, detail)
	}
}

func item(t *testing.T, c *engine.Ctx, id string) *manifest.Item {
	t.Helper()
	for i := range c.M.Items {
		if c.M.Items[i].ID == id {
			return &c.M.Items[i]
		}
	}
	t.Fatalf("no item %s", id)
	return nil
}

// fix plans and applies the fix of an item (or a part), and fails the test on an error.
func fix(t *testing.T, c *engine.Ctx, id, part string, replace bool) engine.Result {
	t.Helper()
	it := item(t, c, id)
	s := check(t, c, id)
	ch, err := c.Kinds[it.Kind].Plan(c, it, s, part, replace)
	if err != nil {
		t.Fatalf("plan %s: %v", id, err)
	}
	res, err := engine.Apply(c, it, part, ch)
	if err != nil {
		t.Fatalf("apply %s: %v", id, err)
	}
	return res
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLink(t *testing.T) {
	f, c := setup(t)
	want(t, check(t, c, "cfg"), engine.Drift, "not set up")
	fix(t, c, "cfg", "", false)
	want(t, check(t, c, "cfg"), engine.OK, "→ cfg")
	if l, _ := os.Readlink(f.Path("home/.config/fx/cfg")); l != f.Path("repo/cfg") {
		t.Fatalf("link points at %s", l)
	}

	// another checkout of the same repo is drift, and the fix points it here
	other := f.Path("old-checkout")
	f.Write(t, "old-checkout/dott.toml", "")
	f.Write(t, "old-checkout/cfg/init.conf", "")
	os.Remove(f.Path("home/.config/fx/cfg"))
	os.Symlink(filepath.Join(other, "cfg"), f.Path("home/.config/fx/cfg"))
	want(t, check(t, c, "cfg"), engine.Drift, "another checkout")
	fix(t, c, "cfg", "", false)
	want(t, check(t, c, "cfg"), engine.OK, "")
}

func TestLinkConflictKeepsYours(t *testing.T) {
	f, c := setup(t)
	f.Write(t, "home/.config/fx/mine/own.conf", "mine\n")
	s := check(t, c, "mine")
	want(t, s, engine.Conflict, "you have your own")
	it := item(t, c, "mine")
	if _, err := c.Kinds["link"].Plan(c, it, s, "", false); err == nil {
		t.Fatal("a plan without replace must refuse to touch yours")
	}
	ch, err := c.Kinds["link"].Plan(c, it, s, "", true)
	if err != nil || len(ch) != 1 || !ch[0].Danger {
		t.Fatalf("replace plan: %v %+v", err, ch)
	}
	res, err := engine.Apply(c, it, "", ch)
	if err != nil || res.Backup == "" {
		t.Fatalf("apply: %v (backup %q)", err, res.Backup)
	}
	want(t, check(t, c, "mine"), engine.OK, "")
	if err := fsops.Restore(res.Backup); err != nil {
		t.Fatal(err)
	}
	if got := read(t, f.Path("home/.config/fx/mine/own.conf")); got != "mine\n" {
		t.Fatalf("restore gave %q", got)
	}
	want(t, check(t, c, "mine"), engine.Conflict, "")
}

func TestBlock(t *testing.T) {
	f, c := setup(t)
	want(t, check(t, c, "loader"), engine.Drift, "no ~/.zshrc yet")
	f.Write(t, "home/.zshrc", "export A=1") // no newline at the end
	want(t, check(t, c, "loader"), engine.Drift, "not set up")
	fix(t, c, "loader", "", false)
	block := "# >>> fx >>>\nsource \"" + f.Repo + "/rc\"\n# <<< fx <<<\n"
	if got := read(t, f.Path("home/.zshrc")); got != "export A=1\n"+block {
		t.Fatalf("zshrc = %q", got)
	}
	want(t, check(t, c, "loader"), engine.OK, "block in ~/.zshrc")

	// a block from somewhere else is rewritten; the lines around it stay
	f.Write(t, "home/.zshrc", "before\n# >>> fx >>>\nsource /old/rc\n# <<< fx <<<\nafter\n")
	want(t, check(t, c, "loader"), engine.Drift, "differs")
	fix(t, c, "loader", "", false)
	if got := read(t, f.Path("home/.zshrc")); got != "before\n"+block+"after\n" {
		t.Fatalf("zshrc = %q", got)
	}

	f.Write(t, "home/.zshrc", "# >>> fx >>>\nsource /old/rc\n")
	want(t, check(t, c, "loader"), engine.Conflict, "no end line")
}

func TestSeedBlockRespectsYourTheme(t *testing.T) {
	f, c := setup(t)
	f.Write(t, "home/.term", "font = mono\ntheme = mine\n")
	want(t, check(t, c, "theme"), engine.OK, "you set your own: theme = mine")
	f.Write(t, "home/.term", "font = mono\n")
	// @term is the first of its paths that exists: ~/.term (not ~/.term-a)
	want(t, check(t, c, "theme"), engine.Drift, "~/.term")
	fix(t, c, "theme", "", false)
	f.Write(t, "home/.term", strings.Replace(read(t, f.Path("home/.term")), "theme = fx", "theme = changed", 1))
	want(t, check(t, c, "theme"), engine.OK, "added once")
}

func TestIncludeLine(t *testing.T) {
	f, c := setup(t)
	f.Write(t, "home/.gitconfig", "[user]\n\tname = me\n")
	want(t, check(t, c, "gitinc"), engine.Drift, "no include line")
	fix(t, c, "gitinc", "", false)
	if got := read(t, f.Path("home/.gitconfig")); !strings.HasSuffix(got, "[include]\n\tpath = "+f.Repo+"/gitconfig\n") || !strings.HasPrefix(got, "[user]") {
		t.Fatalf("gitconfig = %q", got)
	}
	want(t, check(t, c, "gitinc"), engine.OK, "includes gitconfig")

	f.Write(t, "old/dott.toml", "")
	f.Write(t, "home/.gitconfig", "[include]\n\tpath = "+f.Path("old/gitconfig")+"\n")
	want(t, check(t, c, "gitinc"), engine.Drift, "another checkout")
	fix(t, c, "gitinc", "", false)
	want(t, check(t, c, "gitinc"), engine.OK, "")

	f.Write(t, "home/.config/git/ignore", "*.swp")
	want(t, check(t, c, "ignore"), engine.Drift, "lacks: .DS_Store")
	fix(t, c, "ignore", "", false)
	if got := read(t, f.Path("home/.config/git/ignore")); got != "*.swp\n.DS_Store\n" {
		t.Fatalf("ignore = %q", got)
	}
}

func TestCopy(t *testing.T) {
	f, c := setup(t)
	want(t, check(t, c, "themes"), engine.Drift, "2 drift")
	fix(t, c, "themes", "", false)
	want(t, check(t, c, "themes"), engine.OK, "2 files")

	// the repo changed a file dott copied: that's an update, not yours
	f.Write(t, "repo/themes/one", "palette = 1b\n")
	s := check(t, c, "themes")
	if p, _ := s.Part("one"); p.State != engine.Drift || !strings.Contains(p.Detail, "newer") {
		t.Fatalf("part one: %+v", p)
	}
	fix(t, c, "themes", "one", false)

	// you changed a copy: yours, kept by fix everything
	f.Write(t, "home/.config/fx/themes/two", "palette = mine\n")
	s = check(t, c, "themes")
	if p, _ := s.Part("two"); p.State != engine.Conflict {
		t.Fatalf("part two: %+v", p)
	}
	it := item(t, c, "themes")
	if _, err := c.Kinds["copy"].Plan(c, it, s, "", false); err == nil {
		t.Fatal("fix everything has nothing to copy, so the plan errors")
	}
	if _, err := c.Kinds["copy"].Plan(c, it, s, "two", false); err == nil {
		t.Fatal("a plan for your own copy needs replace")
	}
}

func TestScript(t *testing.T) {
	_, c := setup(t)
	s := check(t, c, "tool")
	want(t, s, engine.Drift, "not done")
	if s.Fix != "run it" {
		t.Fatalf("fix = %q", s.Fix)
	}
	ch, err := c.Kinds["script"].Plan(c, item(t, c, "tool"), s, "", false)
	if err != nil || ch[len(ch)-1].Summary != "create ~/.fx-done" {
		t.Fatalf("plan: %v %+v", err, ch)
	}
	fix(t, c, "tool", "", false)
	want(t, check(t, c, "tool"), engine.OK, "done")
}

func TestBrewAndNeeds(t *testing.T) {
	f, c := setup(t)
	s := check(t, c, "pkgs")
	if p, _ := s.Part("jq"); p.State != engine.Off {
		t.Fatalf("jq is in an optional group that is off: %+v", p)
	}
	if p, _ := s.Part("node"); p.State != engine.Drift {
		t.Fatalf("node: %+v", p)
	}
	want(t, check(t, c, "skills"), engine.Blocked, "needs node (brew)")
	fix(t, c, "pkgs", "", false)
	want(t, check(t, c, "pkgs"), engine.OK, "")
	if got := strings.Join(f.Calls(t), " "); !strings.Contains(got, "brew_install_--formula_node") || strings.Contains(got, "_jq") || !strings.Contains(got, "brew_install_--cask_--adopt_fxapp") {
		t.Fatalf("brew calls: %s", got)
	}
	want(t, check(t, c, "skills"), engine.Drift, "2 drift")
}

func TestSkills(t *testing.T) {
	f, c := setup(t)
	f.Write(t, "brew-installed", "node\n")
	want(t, check(t, c, "skills"), engine.Drift, "2 drift: bye, hello")
	fix(t, c, "skills", "", false)
	want(t, check(t, c, "skills"), engine.OK, "2 skills in ~/.agents/skills, linked for claude")
	calls := strings.Join(f.Calls(t), " ")
	if !strings.Contains(calls, "npx_-y_skills@1.7.4_add_"+f.Repo+"/skills_-g_-a_claude-code_codex_-s_bye_hello_-y") || !strings.Contains(calls, "uvx_validate") {
		t.Fatalf("calls: %s", calls)
	}

	// the source changed, the installed copy didn't: an update
	f.Write(t, "repo/skills/hello/SKILL.md", "---\nname: hello\ndescription: says hello twice\n---\n")
	p, _ := check(t, c, "skills").Part("hello")
	if p.State != engine.Drift || !strings.Contains(p.Detail, "newer") {
		t.Fatalf("hello: %+v", p)
	}
	fix(t, c, "skills", "hello", false)

	// you changed the installed copy: yours
	f.Write(t, "home/.agents/skills/bye/notes.md", "mine\n")
	p, _ = check(t, c, "skills").Part("bye")
	if p.State != engine.Conflict || !strings.Contains(p.Detail, "you changed the installed copy") {
		t.Fatalf("bye: %+v", p)
	}

	// a local source from this Mac's config shows as local
	f.Write(t, "own/skills/solo/SKILL.md", "---\nname: solo\ndescription: mine only\n---\n")
	c.Cfg.Local.Skills = []string{f.Path("own/skills")}
	p, _ = check(t, c, "skills").Part("solo")
	if p.State != engine.Drift || !strings.HasPrefix(p.Desc, "local") {
		t.Fatalf("solo: %+v", p)
	}

	// a skill that fails the spec check is blocked
	f.Write(t, "repo/skills/bad/SKILL.md", "---\nname: other\ndescription: x\n---\n")
	p, _ = check(t, c, "skills").Part("bad")
	if p.State != engine.Blocked || !strings.Contains(p.Detail, "not the folder name") {
		t.Fatalf("bad: %+v", p)
	}
}

func TestSkillsNeverTouchProtected(t *testing.T) {
	f, c := setup(t)
	f.Write(t, "brew-installed", "node\n")
	f.Write(t, "repo/skills/synced/SKILL.md", "---\nname: synced\ndescription: x\n---\n")
	it := item(t, c, "skills")
	s := check(t, c, "skills")
	ch, err := c.Kinds["skills"].Plan(c, it, s, "synced", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Apply(c, it, "synced", ch); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("want a refusal for ~/.claude/skills/synced, got %v", err)
	}
	if _, err := os.Lstat(f.Path("home/.claude/skills/synced")); err == nil {
		t.Fatal("~/.claude/skills/synced was created")
	}
}

func TestRules(t *testing.T) {
	f, c := setup(t)
	f.Write(t, "home/.claude/CLAUDE.md", "my own line\n")
	s := check(t, c, "rules")
	if p, _ := s.Part("gone"); p.State != engine.Absent {
		t.Fatalf("gone: %+v", p)
	}
	fix(t, c, "rules", "", false)
	want(t, check(t, c, "rules"), engine.OK, "read by claude, codex")
	if got := read(t, f.Path("home/.claude/CLAUDE.md")); got != "my own line\n\n<!-- >>> fx >>> -->\n@"+f.Repo+"/AGENTS.md\n<!-- <<< fx <<< -->\n" {
		t.Fatalf("CLAUDE.md = %q", got)
	}
	os.Remove(f.Path("home/.codex/AGENTS.md"))
	f.Write(t, "home/.codex/AGENTS.md", "codex rules of my own\n")
	p, _ := check(t, c, "rules").Part("codex")
	if p.State != engine.Conflict || !strings.Contains(p.Detail, "rules of its own (1 lines)") {
		t.Fatalf("codex: %+v", p)
	}
}

func TestGroupsAndOff(t *testing.T) {
	f, _ := setup(t)
	c := f.Ctx(t, &config.Config{Groups: map[string]bool{}})
	want(t, check(t, c, "later"), engine.Off, "not asked yet")
	c.Cfg.Groups["extra"] = true
	want(t, check(t, c, "later"), engine.Drift, "")
	c.Cfg.SetOff("later", true)
	want(t, check(t, c, "later"), engine.Off, "turned off")
}

func TestLint(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"ok":      "---\nname: ok\ndescription: fine\nlicense: MIT\n---\nbody",
		"nostart": "name: nostart\n",
		"open":    "---\nname: open\ndescription: x\n",
		"extra":   "---\nname: extra\ndescription: x\nversion: 2\n---\n",
		"Upper":   "---\nname: Upper\ndescription: x\n---\n",
		"nodesc":  "---\nname: nodesc\n---\n",
	}
	wantWhy := map[string]string{"ok": "", "nostart": "has to start with ---", "open": "never ends", "extra": "fields the spec does not allow", "Upper": "lowercase", "nodesc": "no description"}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(body), 0o644)
		got := kinds.Lint(p)
		if (wantWhy[name] == "") != (got == "") || !strings.Contains(got, wantWhy[name]) {
			t.Errorf("%s: Lint = %q, want %q", name, got, wantWhy[name])
		}
	}
}

func TestParseBrewfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Brewfile")
	os.WriteFile(p, []byte("tap \"a/b\"\n# core\nbrew \"gh\"   # GitHub CLI\nbrew \"a/b/tf\", trusted: true  # Terraform\n\n# apps\ncask \"code\", args: { adopt: true }  # editor\nbrew \"bare\"\n"), 0o644)
	pkgs, taps, err := kinds.ParseBrewfile(p)
	if err != nil || len(taps) != 1 || len(pkgs) != 4 {
		t.Fatalf("%v %v %+v", err, taps, pkgs)
	}
	if pkgs[1].Short != "tf" || !pkgs[1].Trusted || pkgs[1].Desc != "Terraform" || pkgs[2].Kind != "cask" || pkgs[2].Section != "apps" || pkgs[3].Desc != "bare" {
		t.Fatalf("%+v", pkgs)
	}
}
