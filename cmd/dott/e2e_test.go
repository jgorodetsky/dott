package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jgorodetsky/dott/internal/testkit"
)

// dott runs the built binary on the fake machine with no terminal at all (its own session,
// so /dev/tty can't open), the way a script or CI runs it.
func dott(t *testing.T, f *testkit.Fixture, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(dottBin(t), args...)
	cmd.Env = f.Environ()
	cmd.Dir = f.Root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

type report struct {
	Items []struct {
		ID, State, Detail string
		Parts             []struct{ ID, State string }
	}
}

func states(t *testing.T, f *testkit.Fixture) map[string]string {
	t.Helper()
	out, errs, code := dott(t, f, "--json")
	if code != 0 {
		t.Fatalf("--json exit %d: %s", code, errs)
	}
	var r report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m := map[string]string{}
	for _, it := range r.Items {
		m[it.ID] = it.State
		for _, p := range it.Parts {
			m[p.ID] = p.State
		}
	}
	return m
}

// TestLookIsReadOnly: a look (--json, or listing rows without a terminal) leaves the fake HOME
// byte for byte the same and never runs npx, uvx or anything but brew list.
func TestLookIsReadOnly(t *testing.T) {
	f := testkit.New(t)
	f.Write(t, "home/.zshrc", "export A=1\n")
	f.Write(t, "home/.config/fx/mine/own.conf", "mine\n")
	f.Write(t, "home/.claude/skills/synced/x/SKILL.md", "not dott's\n")
	before := testkit.HashTree(t, f.Home)
	states(t, f)
	for _, words := range [][]string{{"drift"}, {"fix", "loader"}, {"summary"}} {
		dott(t, f, words...)
	}
	if after := testkit.HashTree(t, f.Home); after != before {
		t.Fatal("a look changed the fake HOME")
	}
	for _, c := range f.Calls(t) {
		if !strings.HasPrefix(c, "brew_list_") {
			t.Fatalf("a look ran %s", c)
		}
	}
}

// TestFreshFixEverything: on a fresh machine, fix everything --yes fixes all it may; a
// second look finds only what it must leave to you, and the protected folder is untouched.
func TestFreshFixEverything(t *testing.T) {
	f := testkit.New(t)
	f.Write(t, "home/.config/dott/config.toml", "[groups]\nextra = false\n")
	f.Write(t, "home/.config/fx/mine/own.conf", "mine\n")
	f.Write(t, "home/.claude/skills/synced/x/SKILL.md", "not dott's\n")
	synced := testkit.HashTree(t, f.Path("home/.claude/skills/synced"))
	out, errs, code := dott(t, f, "fix", "everything", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	got := states(t, f)
	want := map[string]string{
		"pkgs": "ok", "pkgs/node": "ok", "pkgs/fxapp": "ok", "pkgs/jq": "off",
		"loader": "ok", "theme": "ok", "gitinc": "ok", "ignore": "ok", "themes": "ok", "cfg": "ok", "tool": "ok",
		"skills": "ok", "skills/hello": "ok", "rules": "ok", "rules/gone": "absent", "later": "off",
		"mine": "conflict", "ask": "drift",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %s, want %s", id, got[id], w)
		}
	}
	if b, _ := os.ReadFile(f.Path("home/.config/fx/mine/own.conf")); string(b) != "mine\n" {
		t.Error("your own folder was replaced")
	}
	if testkit.HashTree(t, f.Path("home/.claude/skills/synced")) != synced {
		t.Error("~/.claude/skills/synced changed")
	}
	// every fix that replaced something left a backup
	if ents, _ := os.ReadDir(f.Path("home/.local/state/dott/backups")); len(ents) == 0 {
		t.Error("no backups")
	}
	// a second run has nothing to fix
	before := testkit.HashTree(t, f.Home)
	t.Logf("first run:\n%s", out)
	out, errs, code = dott(t, f, "fix", "everything", "--yes")
	if code != 0 || out != "nothing to fix\n" {
		t.Fatalf("second run: exit %d %q %s", code, out, errs)
	}
	if testkit.HashTree(t, f.Home) != before {
		t.Error("a second fix everything changed something")
	}
	if !strings.Contains(strings.Join(f.Calls(t), " "), "npx_-y_skills@1.7.4_add_") {
		t.Error("skills were not installed with npx skills")
	}
}

func TestInitFromAFolder(t *testing.T) {
	f := testkit.New(t)
	env := f.Environ()
	for i, kv := range env {
		if strings.HasPrefix(kv, "DOTT_REPO=") {
			env[i] = "DOTT_REPO="
		}
	}
	cmd := exec.Command(dottBin(t), "--init", f.Repo)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(f.Home, ".config", "dott", "config.toml"))
	if err != nil || !strings.Contains(string(b), "repo = \""+f.Repo+"\"") {
		t.Fatalf("config: %v\n%s\n%s", err, b, out)
	}
	if !strings.Contains(string(out), "next: run dott in a terminal") {
		t.Fatalf("out: %s", out)
	}
}
