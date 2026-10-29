package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/picker"
	"github.com/jgorodetsky/dott/internal/testkit"
)

func TestSuggestRel(t *testing.T) {
	for in, want := range map[string]string{
		"/h/.config/foo/config.toml": "foo/config.toml",
		"/h/.foorc":                  "foo/.foorc",
		"/h/.wgetrc":                 "wget/.wgetrc",
		"/h/.tmux.conf":              "tmux.conf/.tmux.conf",
		"/h/notes/todo.md":           "notes/todo.md",
	} {
		if got := suggestRel("/h", in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestSuggestIDSkipsTaken(t *testing.T) {
	m := &manifest.Manifest{Items: []manifest.Item{{ID: "foo"}, {ID: "foo-2"}}}
	if got := suggestID(m, "foo/config.toml"); got != "foo-3" {
		t.Fatalf("got %s", got)
	}
	if got := suggestID(m, "Tmux.Conf/.tmux.conf"); got != "tmux-conf" {
		t.Fatalf("got %s", got)
	}
}

// TestItemBlockRoundTrip: the block dott appends loads as an item, and removeBlock takes
// out exactly that block, leaving the file as it was.
func TestItemBlockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := "schema = 1\nname = \"x\"\n\n[[item]]\nid = \"a\"\nkind = \"link\"\nlink = \"~/a\"\nto = \"a\"\n\n[[edit]]\nname = \"rc\"\nfile = \"rc\"\n"
	next := appendBlock([]byte(orig), itemBlock("foo", "~/.config/foo/x \"q\"", "foo/x", "~/.config/foo/x"))
	if err := os.WriteFile(filepath.Join(dir, manifest.FileName), next, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("the block doesn't load: %v\n%s", err, next)
	}
	it, ok := m.Item("foo")
	if !ok || it.Link != `~/.config/foo/x "q"` || it.To != "foo/x" || it.Kind != "link" {
		t.Fatalf("item = %+v", it)
	}
	back, ok := removeBlock(next, "foo")
	if !ok || string(back) != orig {
		t.Fatalf("removeBlock = %v\n%q\nwant\n%q", ok, back, orig)
	}
	if _, ok := removeBlock([]byte(orig), "a"); ok {
		t.Fatal("removed an item dott didn't add")
	}
}

func TestInsertBrew(t *testing.T) {
	bf := "tap \"a/b\"\n\n# core\nbrew \"gh\"  # x\n\n# apps\ncask \"x\"\n"
	if got := string(insertBrew([]byte(bf), "core", `brew "jq"  # j`)); got != "tap \"a/b\"\n\n# core\nbrew \"gh\"  # x\nbrew \"jq\"  # j\n\n# apps\ncask \"x\"\n" {
		t.Fatalf("into a section: %q", got)
	}
	if got := string(insertBrew([]byte(bf), "apps", `cask "y"`)); !strings.HasSuffix(got, "cask \"x\"\ncask \"y\"\n") {
		t.Fatalf("into the last section: %q", got)
	}
	if got := string(insertBrew([]byte(bf), "new", `brew "z"`)); !strings.HasSuffix(got, "cask \"x\"\n\n# new\nbrew \"z\"\n") {
		t.Fatalf("a new section: %q", got)
	}
	if got := string(insertTap([]byte(bf), "c/d")); !strings.HasPrefix(got, "tap \"a/b\"\ntap \"c/d\"\n\n# core") {
		t.Fatalf("tap: %q", got)
	}
	if got := string(insertTap([]byte(bf), "a/b")); got != bf {
		t.Fatalf("a tap twice: %q", got)
	}
}

func TestSecretPaths(t *testing.T) {
	for p, secret := range map[string]bool{
		"/h/.ssh/config": true, "/h/.aws/config": true, "/h/.config/gh/hosts.yml": true, "/h/.kube/config": true,
		"/h/proj/.env": true, "/h/x/server.pem": true, "/h/keys/id_ed25519": true, "/h/.netrc": true,
		"/h/.config/foo/config.toml": false, "/h/.tmux.conf": false, "/h/.config/gh/config.yml": false,
	} {
		if got := secretPath("/h", p) != ""; got != secret {
			t.Errorf("%s: secret %v, want %v", p, got, secret)
		}
	}
}

func TestSecretLines(t *testing.T) {
	for l, want := range map[string]bool{
		"aws_access_key_id = AKIAABCDEFGHIJKLMNOP":               true,
		"-----BEGIN OPENSSH PRIVATE KEY-----":                    true,
		"token: ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":      true,
		"password = hunter22":                                    true,
		"export API_KEY=\"$(op read op://x/y)\"":                 false,
		"x = 9f8Kq2LmZp4Rt7Vw1Yb3Nc6Hd0Gj5Sx8Ue2Ao9Ii4Pl7Ow3Qr6": true,
		"theme = catppuccin-mocha-and-a-very-long-name-for-it":   false,
		"font-family = JetBrainsMono Nerd Font":                  false,
		"palette = 0=#1d1f21":                                    false,
	} {
		if got := secretLine(l) != ""; got != want {
			t.Errorf("%q: secret %v, want %v (%s)", l, got, want, secretLine(l))
		}
	}
}

// undoApp is the fixture with one more optional item, u, that has an undo: it is ok while
// ~/.u exists, and its undo removes ~/.u.
func undoApp(t *testing.T) (*testkit.Fixture, *App) {
	t.Helper()
	f := testkit.New(t)
	f.Use(t)
	f.Write(t, "repo/dott.toml", testkit.Manifest+`
[[item]]
id = "u"
kind = "script"
group = "extra"
check = ["sh", "-c", "test -e \"$HOME/.u\""]
apply = ["sh", "-c", "touch \"$HOME/.u\""]
undo = ["sh", "-c", "rm \"$HOME/.u\""]
`)
	f.Write(t, "home/.u", "")
	c := f.Ctx(t, &config.Config{Groups: map[string]bool{"extra": true}})
	a := &App{Env: c.Env, Cfg: c.Cfg, M: c.M, C: c, Pk: picker.Lib{}, Out: io.Discard, Err: io.Discard}
	a.Scan()
	return f, a
}

func TestTurnOffRunsUndo(t *testing.T) {
	f, a := undoApp(t)
	p := a.offPlan("u", true)
	if !strings.Contains(strings.Join(p.Lines, "\n"), "and undo it here: sh -c") {
		t.Fatalf("the plan doesn't say it undoes: %v", p.Lines)
	}
	if err := p.Run(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path("home/.u")); !os.IsNotExist(err) {
		t.Fatal("undo didn't run")
	}
	if !a.Cfg.IsOff("u") {
		t.Fatal("not turned off")
	}
}

func TestKeepMineDoesNotUndo(t *testing.T) {
	f, a := undoApp(t)
	if err := a.switchPlan("u", true, false).Run(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path("home/.u")); err != nil {
		t.Fatal("keep mine ran undo")
	}
}

func TestGroupOffRunsUndo(t *testing.T) {
	f, a := undoApp(t)
	p := a.groupPlan("extra", false)
	if !strings.Contains(strings.Join(p.Lines, "\n"), "undo u here") {
		t.Fatalf("the plan doesn't say it undoes: %v", p.Lines)
	}
	if err := p.Run(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path("home/.u")); !os.IsNotExist(err) {
		t.Fatal("undo didn't run")
	}
	if a.Cfg.Groups["extra"] {
		t.Fatal("the group is still on")
	}
}

func TestFailedUndoKeepsItOn(t *testing.T) {
	f, a := undoApp(t)
	os.Remove(f.Path("home/.u")) // rm fails now; a check that always passes keeps it there
	a.M.Items[len(a.M.Items)-1].Check = []string{"sh", "-c", "true"}
	a.Scan()
	if err := a.offPlan("u", true).Run(a); err == nil || !strings.Contains(err.Error(), "stays on") {
		t.Fatalf("err = %v", err)
	}
	if a.Cfg.IsOff("u") {
		t.Fatal("turned off although undo failed")
	}
}

// TestNamesThatAreFlags: a name that starts with - would reach zsh or brew as an option.
func TestNamesThatAreFlags(t *testing.T) {
	for _, n := range []string{"-x", "--installed"} {
		if aliasName.MatchString(n) {
			t.Errorf("alias name %q passes", n)
		}
		if brewName.MatchString(n) {
			t.Errorf("brew name %q passes", n)
		}
	}
	for _, n := range []string{"gs", "ll.2", "a-b"} {
		if !aliasName.MatchString(n) {
			t.Errorf("alias name %q fails", n)
		}
	}
	for _, n := range []string{"ripgrep", "node@22", "gcc", "jgorodetsky/dott/dott", "c++utils"} {
		if !brewName.MatchString(n) {
			t.Errorf("brew name %q fails", n)
		}
	}
}

// TestNoteIsOneField: a tab or newline in a note would add a column to notes.tsv.
func TestNoteIsOneField(t *testing.T) {
	if got := oneField("one\ttwo\nthree"); got != "one two three" {
		t.Fatalf("%q", got)
	}
}

// TestChangeLines: a plan names the lines a change removes and adds, and a file that doesn't
// change has no lines.
func TestChangeLines(t *testing.T) {
	got := changeLines("~/rc", []byte("a\nalias cl='clear'\nb\n"), []byte("a\nb\nunalias x\n"))
	want := []string{"~/rc: remove alias cl='clear'", "~/rc: add unalias x"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if got := changeLines("~/notes", []byte("x\n"), []byte("x\n")); len(got) != 0 {
		t.Fatalf("an unchanged file: %q", got)
	}
}

// TestBrewAgeWithoutPaths: a brew that prints no paths gives an unknown age, not the age of
// a .git folder where dott runs.
func TestBrewAgeWithoutPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "FETCH_HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if got := brewAge("true"); got != "an unknown time" {
		t.Fatalf("age %q", got)
	}
}

// TestEntryLineIsItsStart: the manifest entry row and its printout name the same line, the
// entry's [[item]].
func TestEntryLineIsItsStart(t *testing.T) {
	_, a := fixtureApp(t)
	var out strings.Builder
	a.Out = &out
	file, line := a.manifestLine("cfg")
	b, _ := os.ReadFile(file)
	if got := strings.Split(string(b), "\n")[line-1]; strings.TrimSpace(got) != "[[item]]" {
		t.Fatalf("line %d is %q", line, got)
	}
	if err := a.showEntry("cfg"); err != nil {
		t.Fatal(err)
	}
	if first := strings.SplitN(out.String(), "\n", 2)[0]; !strings.Contains(first, fmt.Sprintf("dott.toml:%d", line)) {
		t.Fatalf("printout starts %q, row says line %d", first, line)
	}
}
