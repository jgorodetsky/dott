package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jgorodetsky/dott/internal/journey"
	"github.com/jgorodetsky/dott/internal/testkit"
)

var update = flag.Bool("update", false, "rewrite the golden screens in testdata/screens")

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// dottBin builds dott once, without -race: fzf has a data race of its own
// (matcher.go:146 writes merger.final while terminal.go:2041 reads it) that
// fails any -race build as soon as a query is typed. Users run this kind of build.
func dottBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		binPath = filepath.Join(os.Getenv("HOME"), "dott-under-test")
		build := exec.Command("go", "build", "-o", binPath, ".")
		build.Env = append(os.Environ(), goEnv...)
		out, err := build.CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// screen is dott in a pseudo-terminal, failing the test when a wait runs out.
type screen struct {
	*journey.Screen
	t    *testing.T
	sent int // how much output there was when keys last went in
}

// machine is a fresh fake machine whose optional group is answered, with your own folder in
// the way of one link, and an editor that only logs what it was asked to open. The repo is
// reached as ~/dotfiles, so screens show the same paths on every run.
func machine(t *testing.T) (*testkit.Fixture, []string) {
	t.Helper()
	f := testkit.New(t)
	f.Write(t, "home/.config/dott/config.toml", "[groups]\nextra = false\n")
	f.Write(t, "home/.config/fx/mine/own.conf", "mine\n")
	f.Write(t, "bin/editor", "#!/bin/sh\necho \"editor $*\" >> \"$DOTT_STUB_LOG\"\n")
	if err := os.Chmod(f.Path("bin/editor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.Repo, f.Path("home/dotfiles")); err != nil {
		t.Fatal(err)
	}
	return f, f.Environ("DOTT_REPO="+f.Path("home/dotfiles"), "EDITOR="+f.Path("bin/editor"))
}

// runNoTTY runs dott without a terminal on that machine, the way a script does.
func runNoTTY(t *testing.T, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(dottBin(t), args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dott %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// open runs a program in a cols x rows pseudo-terminal.
func open(t *testing.T, env []string, cols, rows int, name string, args ...string) *screen {
	t.Helper()
	s, err := journey.Start(env, cols, rows, name, args...)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(s.Close)
	return &screen{Screen: s, t: t}
}

// start runs dott on a fresh machine in a 120x40 terminal.
func start(t *testing.T, args ...string) *screen {
	t.Helper()
	_, env := machine(t)
	return open(t, env, 120, 40, dottBin(t), args...)
}

func (s *screen) waitFor(want string, n int) {
	s.t.Helper()
	if err := s.WaitFor(want, n, 10*time.Second); err != nil {
		s.t.Fatalf("%v; screen:\n%s", err, s.Snapshot())
	}
}

// waitScreen waits until the screen as a person sees it shows want. Unlike waitFor, it can't
// be fooled by a prompt that was drawn earlier.
func (s *screen) waitScreen(want string) {
	s.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(s.Snapshot(), want); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			s.t.Fatalf("waited 10s for %q on the screen:\n%s", want, s.Snapshot())
		}
	}
}

// enter types a query, waits for fzf to finish filtering, then presses enter. Enter sent at
// once can pick a row of the list before the query filtered it.
func (s *screen) enter(query string) {
	s.t.Helper()
	s.send(query)
	if err := s.Still(250*time.Millisecond, 10*time.Second); err != nil {
		s.t.Fatal(err)
	}
	s.send("\r")
}

func (s *screen) send(keys string) {
	s.t.Helper()
	s.sent = len(s.Text())
	if err := s.Send(keys); err != nil {
		s.t.Fatal(err)
	}
}

// exit waits for dott to finish and returns its exit code.
func (s *screen) exit() int {
	s.t.Helper()
	code, err := s.Exit(10 * time.Second)
	if err != nil {
		s.t.Fatalf("%v; screen:\n%s", err, s.Snapshot())
	}
	return code
}

// golden compares the settled screen with testdata/screens/name.txt; -update rewrites it.
func (s *screen) golden(name string) string {
	s.t.Helper()
	if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
		s.t.Fatal(err)
	}
	got := s.Snapshot()
	file := filepath.Join("testdata", "screens", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			s.t.Fatal(err)
		}
		return got
	}
	want, err := os.ReadFile(file)
	if err != nil {
		s.t.Fatalf("%v (run go test -update to write it); screen:\n%s", err, got)
	}
	if rule.ReplaceAllString(got, "─") != rule.ReplaceAllString(string(want), "─") {
		s.t.Errorf("screen %s differs (go test -update rewrites it)\n--- want\n%s--- got\n%s", name, want, got)
	}
	return got
}

// rule is a separator line that fzf draws. Its width is fzf's, and on a slow machine fzf can
// draw it a column short before its window has its full width, so goldens don't compare it.
var rule = regexp.MustCompile(`(?m)^─+$`)

const (
	down   = "\x1b[B"
	esc    = "\x1b"
	ctrlC  = "\x03"
	prompt = "dott › "
)

// TestPTYCtrlCRestoresTerminal runs dott from a shell, as a user does, opens an item and
// presses ctrl-c there. The shell prints the terminal modes (stty -g) before and after:
// they must match, and dott exits 0.
func TestPTYCtrlCRestoresTerminal(t *testing.T) {
	_, env := machine(t)
	s := open(t, env, 120, 40, "/bin/sh", "-c", `echo "BEFORE:$(stty -g)"; "$0" loader; echo "EXIT:$? AFTER:$(stty -g)"`, dottBin(t))
	s.waitFor(prompt+"loader", 1)
	s.send(down + "\r")
	s.waitFor("loader › ", 1)
	s.send(ctrlC)
	s.waitFor("AFTER:", 1)
	text := s.Text()
	field := func(key string) string {
		_, rest, _ := strings.Cut(text, key)
		v, _, _ := strings.Cut(rest, "\n")
		v, _, _ = strings.Cut(v, " ")
		return strings.TrimSpace(v)
	}
	if code := field("EXIT:"); code != "0" {
		t.Fatalf("exit code %s after ctrl-c, want 0", code)
	}
	if before, after := field("BEFORE:"), field("AFTER:"); before == "" || before != after {
		t.Fatalf("terminal modes not restored:\nbefore %s\nafter  %s", before, after)
	}
}

func TestPTYBackToPickerAfterAction(t *testing.T) {
	s := start(t)
	s.waitFor(prompt, 1)
	s.send("fix skills")
	s.waitFor("read-only, runs as soon as you press enter", 1) // the preview of the row under the cursor
	s.send("\r")
	s.waitFor("✗ skills:", 1)
	s.waitFor(prompt, 2) // the picker came back in the same process
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.Snapshot())
	}
}

func TestPTYSingleMatchAsksFirst(t *testing.T) {
	s := start(t, "fix", "loader")
	s.waitFor("Run it? [Y/n/copy]", 1) // picked at once, but a change still asks
	if strings.Contains(s.Text(), "✓ loader") {
		t.Fatal("ran before the answer")
	}
	s.send("n\r")
	s.waitFor("skipped fix loader", 1)
	s.waitFor(prompt, 1)
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.Snapshot())
	}
}

func TestPTYFixThenBack(t *testing.T) {
	s := start(t, "fix", "loader")
	s.waitFor("Run it? [Y/n/copy]", 1)
	s.send("\r")
	s.waitFor("✓ loader: block in ~/.zshrc", 1)
	s.waitFor(prompt, 1)
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.Snapshot())
	}
}

func TestPTYDangerousNeedsYes(t *testing.T) {
	s := start(t, "resolve", "mine")
	s.waitFor("resolve mine › ", 1) // the guided row opened its follow-up picker
	s.send("replace")
	s.waitFor("dangerous, you type yes to run it", 1) // the cursor is on the dangerous row
	s.send("\r")
	s.answer("anything else cancels:", "y")
	s.waitFor("cancelled replace with the repo's", 1)
	s.waitScreen("resolve mine › replace") // nothing changed: still in the list, with the query
	s.send(esc)
	s.waitScreen(prompt)
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.Snapshot())
	}
}

func TestPTYAsksGroupsOnce(t *testing.T) {
	f := testkit.New(t)
	s := open(t, f.Environ(), 120, 40, dottBin(t))
	s.waitFor("extra: the extra package (holds later, pkgs/jq) [y/N]", 1)
	s.send("n\r")
	s.waitFor(prompt, 1)
	s.send(esc)
	s.exit()
	b, _ := os.ReadFile(f.Path("home/.config/dott/config.toml"))
	if !strings.Contains(string(b), "extra = false") {
		t.Fatalf("the answer was not saved: %s", b)
	}
}

// TestPTYTypedAheadIsNotAnAnswer: keys typed before a question shows (the impatient y and
// enter) are dropped, so they can't answer a question asked only once.
func TestPTYTypedAheadIsNotAnAnswer(t *testing.T) {
	f := testkit.New(t)
	s := open(t, f.Environ(), 120, 40, dottBin(t))
	s.send("y\r")
	s.waitFor("extra: the extra package (holds later, pkgs/jq) [y/N]", 1)
	if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f.Path("home/.config/dott/config.toml")); strings.Contains(string(b), "extra") {
		t.Fatalf("the early y answered the question: %s", b)
	}
	s.send("\r")
	s.waitFor(prompt, 1)
	s.send(esc)
	s.exit()
	if b, _ := os.ReadFile(f.Path("home/.config/dott/config.toml")); !strings.Contains(string(b), "extra = false") {
		t.Fatalf("the answer was not saved: %s", b)
	}
}

// TestJourneyMainScreen: the first screen at two sizes. The header has the counts, the items
// are on the first screen, and there is no summary row.
func TestJourneyMainScreen(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			_, env := machine(t)
			s := open(t, env, size[0], size[1], dottBin(t))
			s.waitFor(prompt, 1)
			got := s.golden(fmt.Sprintf("main-%dx%d", size[0], size[1]))
			for _, want := range []string{"dott · ~/dotfiles", "need you", "✓ ok ~ drift"} {
				if !strings.Contains(got, want) {
					t.Errorf("no %q on the screen", want)
				}
			}
			if strings.Contains(got, "summary") {
				t.Error("the summary row is back")
			}
			s.send(esc)
			if code := s.exit(); code != 0 {
				t.Fatalf("exit code %d", code)
			}
		})
	}
}

// TestJourneySmallScreenKeepsResult: on a screen shorter than the picker, the last lines dott
// printed (an error, a fix's result and its backup) stay in view above the picker.
func TestJourneySmallScreenKeepsResult(t *testing.T) {
	_, env := machine(t)
	s := open(t, env, 80, 24, dottBin(t), "add", "file")
	s.answer("file or folder to track", "~/nope.conf")
	s.waitScreen(prompt)
	if got := s.Snapshot(); !strings.Contains(got, "~/nope.conf isn't there") {
		t.Fatalf("the error is not in view:\n%s", got)
	}
	s.send(esc)
	s.exit()
	s = open(t, env, 80, 24, dottBin(t), "fix", "loader")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitScreen(prompt)
	if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); !strings.Contains(got, "✓ loader: block in ~/.zshrc") || !strings.Contains(got, "undo: dott restore") {
		t.Fatalf("the result is not in view:\n%s", got)
	}
	s.send(esc)
	s.exit()
}

// TestJourneyPasteThenEnter: a query and enter that come at once (a paste) can reach fzf before
// its search for the query is done, so enter would pick a row of the full list. dott shows the
// filtered list again instead of running the wrong row.
func TestJourneyPasteThenEnter(t *testing.T) {
	_, env := machine(t)
	for i := 0; i < 3; i++ {
		s := open(t, env, 120, 40, dottBin(t))
		s.waitScreen(prompt)
		if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		s.send("cfg\r")
		if err := s.Still(600*time.Millisecond, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if got := s.Snapshot(); strings.Contains(got, "Run it?") && !strings.HasPrefix(got, "fix cfg\n") {
			t.Fatalf("enter ran a row the query doesn't match:\n%s", got)
		}
		s.send(ctrlC)
		s.Exit(10 * time.Second)
	}
}

// TestJourneyRowNameOpensItsList: typing a folded list's own name and enter opens that list
// with all of its rows. Only the words past the name go on into it.
func TestJourneyRowNameOpensItsList(t *testing.T) {
	_, env := machine(t)
	for _, c := range []struct{ query, want string }{
		{"optional parts", "turn on extra"},
		{"add", "add brew"},
	} {
		s := open(t, env, 120, 40, dottBin(t))
		s.waitScreen(prompt)
		s.enter(c.query)
		s.waitScreen(c.want)
		s.send(ctrlC)
		s.Exit(10 * time.Second)
	}
}

// TestJourneyNoChangeStays: a look (compare) or a no at Run it? changes nothing, so you stay
// where you were, with what you typed: you can compare, then choose.
func TestJourneyNoChangeStays(t *testing.T) {
	s := start(t)
	s.waitScreen(prompt)
	s.enter("resolve mine")
	s.waitScreen("resolve mine › ")
	s.enter("compare")
	s.waitFor("diff -ru", 1)
	s.waitScreen("resolve mine › ")
	s.enter("keep mine")
	s.answer("Run it? [Y/n/copy]", "n")
	s.waitFor("skipped", 1)
	s.waitScreen("resolve mine › keep mine")
	s.send(esc)
	s.waitScreen("dott › resolve mine")
	s.send(ctrlC)
	s.exit()
}

// TestJourneyItemThenBack is the bug from v0.1.0: dott <item> showed the item, then dropped
// to the main list. Now a single match opens the item's view, esc goes back to the main list
// with the words still typed, and esc again quits.
func TestJourneyItemThenBack(t *testing.T) {
	_, env := machine(t)
	runNoTTY(t, env, "fix", "everything", "--yes")
	s := open(t, env, 120, 40, dottBin(t), "cfg")
	s.waitFor("cfg › ", 1)
	got := s.golden("item-cfg")
	for _, want := range []string{"cfg · ok", "~/.config/fx/cfg", "~/dotfiles/cfg", "manifest entry", "turn off cfg"} {
		if !strings.Contains(got, want) {
			t.Errorf("the item view lacks %q", want)
		}
	}
	s.send(esc)
	s.waitScreen(prompt + "cfg")
	s.golden("main-after-back")
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
}

// TestJourneyOpenAFile: item, then a folder, then a file in your editor, then back out one
// level at a time.
func TestJourneyOpenAFile(t *testing.T) {
	f, env := machine(t)
	runNoTTY(t, env, "fix", "everything", "--yes")
	s := open(t, env, 120, 40, dottBin(t), "cfg")
	s.waitFor("cfg › ", 1)
	s.enter("~/dotfiles/cfg")
	s.waitFor("~/dotfiles/cfg › ", 1)
	s.enter("init.conf")
	want := "editor_" + f.Path("home/dotfiles/cfg/init.conf")
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(strings.Join(f.Calls(t), " "), want); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the editor was not asked to open init.conf: %v; screen:\n%s", f.Calls(t), s.Snapshot())
		}
	}
	s.waitFor("~/dotfiles/cfg › init.conf", 1)
	if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); !strings.Contains(got, "~/dotfiles/cfg › ") || strings.Contains(got, "~/dotfiles/cfg › init") {
		t.Fatalf("not back in the folder's list, with an empty query:\n%s", got)
	}
	s.send(esc)
	s.waitScreen("cfg › ~/dotfiles/cfg") // the item's view, with its query as it was
	s.send(esc)
	s.waitScreen(prompt + "cfg")
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
}

// TestJourneyFixInItemView: a fix from an item's view stays in that view, which shows the
// new state.
func TestJourneyFixInItemView(t *testing.T) {
	s := start(t, "loader")
	s.waitFor(prompt+"loader", 1)
	s.send(down + "\r") // the second row: the item, under its fix
	s.waitFor("loader › ", 1)
	s.waitFor("loader · drift", 1)
	s.enter("fix loader")
	s.waitFor("Run it? [Y/n/copy]", 1)
	s.send("\r")
	s.waitFor("✓ loader: block in ~/.zshrc", 1)
	s.waitFor("loader › ", 2)
	s.waitFor("loader · ok", 1)
	if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); !strings.Contains(got, "loader › ") || !strings.Contains(got, "turn off loader") {
		t.Fatalf("not back in the item's view:\n%s", got)
	}
	s.send(ctrlC)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
}

// gitCheckout makes the fake repo a git checkout with one commit, so add can offer one.
func gitCheckout(t *testing.T, f *testkit.Fixture) {
	t.Helper()
	testkit.Git(t, f.Repo, "init", "-q", "-b", "main")
	testkit.Git(t, f.Repo, "add", ".")
	testkit.Git(t, f.Repo, "commit", "-q", "-m", "first")
}

// withOrigin makes the fake repo a git checkout of a bare origin, then pushes one more
// commit to origin from another clone, so origin has news the checkout doesn't.
func withOrigin(t *testing.T, f *testkit.Fixture) {
	t.Helper()
	gitCheckout(t, f)
	testkit.Git(t, f.Root, "init", "-q", "--bare", "-b", "main", "origin.git")
	testkit.Git(t, f.Repo, "remote", "add", "origin", f.Path("origin.git"))
	testkit.Git(t, f.Repo, "push", "-q", "-u", "origin", "main")
	testkit.Git(t, f.Root, "clone", "-q", f.Path("origin.git"), "other")
	f.Write(t, "other/notes.md", "new\n")
	testkit.Git(t, f.Path("other"), "add", ".")
	testkit.Git(t, f.Path("other"), "commit", "-q", "-m", "notes")
	testkit.Git(t, f.Path("other"), "push", "-q")
}

// TestJourneyUpdateNotice: origin has a commit this checkout doesn't. The header says so
// without fetching, and refresh pulls it and clears the notice.
func TestJourneyUpdateNotice(t *testing.T) {
	f, env := machine(t)
	withOrigin(t, f)
	before := testkit.Git(t, f.Repo, "for-each-ref")
	s := open(t, env, 120, 40, dottBin(t))
	s.waitScreen("↑ origin/main has new commits: pick refresh")
	if after := testkit.Git(t, f.Repo, "for-each-ref"); after != before {
		t.Fatalf("the check changed refs:\n%s\nwant\n%s", after, before)
	}
	s.enter("refresh")
	s.answer("Pull?", "y")
	s.answer("Run it? [Y/n/copy]", "y")
	s.waitScreen(prompt)
	if err := s.Still(300*time.Millisecond, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); strings.Contains(got, "new commits") {
		t.Fatalf("the notice is still there after the pull:\n%s", got)
	}
	if !strings.Contains(testkit.Git(t, f.Repo, "log", "--oneline"), "notes") {
		t.Fatal("refresh didn't pull")
	}
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

// TestJourneyRefreshAsksBeforeFix: a yes to the pull is not a yes to the fixes. After the pull,
// refresh shows the fix plan and asks again; a no there changes nothing in home.
func TestJourneyRefreshAsksBeforeFix(t *testing.T) {
	f, env := machine(t)
	withOrigin(t, f)
	zshrc, _ := os.ReadFile(f.Path("home/.zshrc"))
	s := open(t, env, 120, 40, dottBin(t), "refresh")
	s.answer("Pull?", "y")
	s.waitFor("fix everything", 1)
	s.answer("Run it? [Y/n/copy]", "n")
	s.waitFor("skipped", 1)
	if !strings.Contains(testkit.Git(t, f.Repo, "log", "--oneline"), "notes") {
		t.Fatal("refresh didn't pull")
	}
	after, _ := os.ReadFile(f.Path("home/.zshrc"))
	_, err := os.Lstat(f.Path("home/.codex/AGENTS.md"))
	if string(after) != string(zshrc) || err == nil || strings.Contains(strings.Join(f.Calls(t), " "), "install") {
		t.Fatalf("a no at Run it? still fixed things; calls %v; screen:\n%s", f.Calls(t), s.Snapshot())
	}
	s.send(esc)
	s.exit()
}

// TestJourneyNoGitCheckout: a repo folder that is not a git checkout gets plain words from
// refresh, and add offers no commit.
func TestJourneyNoGitCheckout(t *testing.T) {
	f, env := machine(t)
	s := open(t, env, 120, 40, dottBin(t), "refresh")
	s.waitFor("~/dotfiles isn't a git checkout, so there is nothing to pull", 1)
	s.send(ctrlC)
	s.Exit(10 * time.Second)
	f.Write(t, "home/.config/foo/config.toml", "x = 1\n")
	s = open(t, env, 120, 40, dottBin(t), "add", "file")
	s.answer("file or folder to track", "~/.config/foo/config.toml")
	s.answer("Where should it live?", "1")
	s.answer("its place in ~/dotfiles", "")
	s.answer("its id in dott", "")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitFor("isn't a git checkout, so there is nothing to commit", 1)
	if got := s.Snapshot(); strings.Contains(got, "Commit it") {
		t.Fatalf("a commit offer in a folder with no git:\n%s", got)
	}
	s.send(ctrlC)
	s.Exit(10 * time.Second)
}

// TestJourneyRemoteHangs: a remote that never answers costs at most 1.5s more than the scan,
// and shows no notice.
func TestJourneyRemoteHangs(t *testing.T) {
	f, env := machine(t)
	withOrigin(t, f)
	testkit.Git(t, f.Repo, "config", "protocol.ext.allow", "always")
	testkit.Git(t, f.Repo, "remote", "set-url", "origin", "ext::sleep 21.5")
	began := time.Now()
	s := open(t, env, 120, 40, dottBin(t))
	s.waitScreen("dott · ~/dotfiles (main)")
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("the first screen took %s", took)
	}
	if err := s.Still(300*time.Millisecond, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); strings.Contains(got, "new commits") {
		t.Fatalf("a notice from a remote that never answered:\n%s", got)
	}
	s.send(esc)
	s.exit()
	// quitting stops the check: no git or sleep is left running
	time.Sleep(200 * time.Millisecond)
	if out, _ := exec.Command("pgrep", "-f", `^(/bin/)?sleep 21\.5$`).Output(); len(out) > 0 {
		t.Fatalf("the check's processes outlived dott: %s", out)
	}
}

// answer waits for a question that comes after the keys sent last, then types the answer
// and enter. An earlier question with the same words, or the same words in a preview, would
// send the answer before dott asks, and dott drops keys typed before a question.
func (s *screen) answer(question, text string) {
	s.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(s.Text()[s.sent:], question); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			s.t.Fatalf("waited 10s for %q after the last keys; screen:\n%s", question, s.Snapshot())
		}
	}
	s.send(text + "\r")
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestJourneyTrackAFile: add file moves a config file into the repo, links it back and adds
// its item; stop tracking puts a real copy back and takes the item out again.
func TestJourneyTrackAFile(t *testing.T) {
	f, env := machine(t)
	gitCheckout(t, f)
	f.Write(t, "home/.config/foo/config.toml", "color = \"blue\"\n")
	manifestBefore := read(t, f.Path("repo/dott.toml"))
	s := open(t, env, 120, 40, dottBin(t), "add", "file")
	s.answer("file or folder to track", "~/.config/foo/config.toml")
	s.answer("Where should it live?", "1")
	s.answer("its place in ~/dotfiles", "")
	s.answer("its id in dott", "")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitFor("✓ foo: ~/.config/foo/config.toml → foo/config.toml", 1)
	s.answer("Commit it", "n")
	s.waitScreen(prompt)
	if got, _ := os.Readlink(f.Path("home/.config/foo/config.toml")); got != f.Path("home/dotfiles/foo/config.toml") {
		t.Fatalf("the link points at %q", got)
	}
	if got := read(t, f.Path("repo/foo/config.toml")); got != "color = \"blue\"\n" {
		t.Fatalf("the repo's copy is %q", got)
	}
	if got := read(t, f.Path("repo/dott.toml")); !strings.Contains(got, "# added with dott\n[[item]]\nid = \"foo\"") {
		t.Fatalf("no item in dott.toml:\n%s", got)
	}
	s.enter("foo")
	s.waitScreen("foo › ")
	s.enter("stop tracking")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitFor("is a file of yours again", 1)
	s.waitScreen(prompt)
	if fi, err := os.Lstat(f.Path("home/.config/foo/config.toml")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("not a real file again: %v", err)
	}
	if got := read(t, f.Path("home/.config/foo/config.toml")); got != "color = \"blue\"\n" {
		t.Fatalf("the file is %q", got)
	}
	if got := read(t, f.Path("repo/dott.toml")); got != manifestBefore {
		t.Fatalf("dott.toml is not as it was:\n%s", got)
	}
	s.send(esc)
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

// TestJourneyTrackRefusesOwned: add file refuses what an item already owns. A file inside a
// link that dott made is in the repo already; a folder that holds items, or a file that an
// item writes to, would move that item's work into the repo.
func TestJourneyTrackRefusesOwned(t *testing.T) {
	f, env := machine(t)
	runNoTTY(t, env, "fix", "everything", "--yes")
	repo := testkit.HashTree(t, f.Repo)
	for _, c := range []struct{ path, want string }{
		{"~/.config/fx/cfg/init.conf", "is in the repo already, through the link ~/.config/fx/cfg (item cfg)"},
		{"~/.config/fx", "holds what items cfg, mine, themes set up"},
		{"~/.zshrc", "is where item loader puts its part"},
	} {
		s := open(t, env, 120, 40, dottBin(t), "add", "file")
		s.answer("file or folder to track", c.path)
		s.waitFor(c.want, 1)
		s.waitScreen(prompt)
		s.send(esc)
		s.exit()
	}
	if got := testkit.HashTree(t, f.Repo); got != repo {
		t.Fatal("add file changed the repo")
	}
}

// TestJourneyRestoreKeepsYourEdits: restore replaces what is there now, which may hold your
// own edits made after the fix. So it asks for a typed yes, backs up what is there first, and
// the item it changed shows its new state at once.
func TestJourneyRestoreKeepsYourEdits(t *testing.T) {
	f, env := machine(t)
	runNoTTY(t, env, "fix", "loader", "--yes")
	edited := read(t, f.Path("home/.zshrc")) + "export MINE=1\n"
	f.Write(t, "home/.zshrc", edited)
	s := open(t, env, 120, 40, dottBin(t), "restore")
	s.waitScreen("-loader")
	s.enter("")
	s.waitFor("remove ~/.zshrc", 1)
	s.answer("type yes", "yes")
	s.waitFor("backup:", 1)
	s.waitScreen(prompt)
	if _, err := os.Lstat(f.Path("home/.zshrc")); err == nil {
		t.Fatal("~/.zshrc is still there")
	}
	saved := false
	_ = filepath.WalkDir(f.Path("home/.local/state/dott/backups"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && read(t, p) == edited {
			saved = true
		}
		return nil
	})
	if !saved {
		t.Fatal("no backup holds the edited ~/.zshrc")
	}
	if got := s.Snapshot(); !strings.Contains(got, "fix loader") {
		t.Fatalf("the loader item doesn't show it needs a fix again:\n%s", got)
	}
	s.send(esc)
	s.exit()
}

// TestJourneyTrackForThisMac: this Mac only puts the file in ~/.config/dott/files and its item
// in local.toml; the repo doesn't change, and the item says this Mac only.
func TestJourneyTrackForThisMac(t *testing.T) {
	f, env := machine(t)
	f.Write(t, "home/.config/foo/config.toml", "color = \"blue\"\n")
	repoBefore := testkit.HashTree(t, f.Repo)
	s := open(t, env, 120, 40, dottBin(t), "add", "file")
	s.answer("file or folder to track", "~/.config/foo/config.toml")
	s.answer("Where should it live?", "2")
	s.answer("its place in ~/.config/dott/files", "")
	s.answer("its id in dott", "")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitFor("✓ foo: ~/.config/foo/config.toml → ~/.config/dott/files/foo/config.toml", 1)
	s.waitScreen(prompt)
	if got, _ := os.Readlink(f.Path("home/.config/foo/config.toml")); got != f.Path("home/.config/dott/files/foo/config.toml") {
		t.Fatalf("the link points at %q", got)
	}
	if got := read(t, f.Path("home/.config/dott/local.toml")); !strings.Contains(got, `to = "{{local}}/foo/config.toml"`) {
		t.Fatalf("local.toml:\n%s", got)
	}
	if testkit.HashTree(t, f.Repo) != repoBefore {
		t.Fatal("the repo changed")
	}
	s.send("foo")
	s.waitScreen("this Mac only")
	s.send(esc)
	s.exit()
}

// TestJourneySecretsStayOff: a file of secrets can only be tracked for this Mac, and a file
// with a line that looks like a password needs a typed yes before it goes in the repo.
func TestJourneySecretsStayOff(t *testing.T) {
	f, env := machine(t)
	f.Write(t, "home/.ssh/config", "Host x\n")
	f.Write(t, "home/.config/bar/bar.conf", "user = me\npassword = hunter22\n")
	hash := func() string {
		return testkit.HashTree(t, f.Repo) + testkit.HashTree(t, f.Path("home/.ssh")) + testkit.HashTree(t, f.Path("home/.config"))
	}
	before := hash()
	s := open(t, env, 120, 40, dottBin(t), "add", "file")
	s.answer("file or folder to track", "~/.ssh/config")
	s.waitFor("holds secrets, so dott tracks it only for this Mac", 1)
	s.answer("Track it for this Mac only?", "n")
	s.waitScreen(prompt)
	s.enter("add file") // typed words go on into the add list, where they match one row
	s.waitFor("file or folder to track", 2)
	s.send("~/.config/bar/bar.conf\r")
	s.answer("Where should it live?", "1")
	s.answer("its place in ~/dotfiles", "")
	s.answer("its id in dott", "")
	s.waitFor("1 line that look like secrets", 1)
	s.waitFor("line 2: a password, token or key with a value", 1)
	if strings.Contains(s.Text(), "hunter22") {
		t.Fatal("the secret itself is on the screen")
	}
	s.answer("type yes to run it", "y")
	s.waitFor("cancelled", 1)
	s.waitScreen(prompt)
	s.send(esc)
	s.exit()
	if hash() != before {
		t.Fatal("the repo, ~/.ssh or ~/.config changed")
	}
}

// TestJourneyAddBrew: add brew lists a package under a section of the repo's Brewfile, then
// installs it with the brew item's fix.
func TestJourneyAddBrew(t *testing.T) {
	f, env := machine(t)
	gitCheckout(t, f)
	s := open(t, env, 120, 40, dottBin(t), "add", "brew")
	s.answer("brew package", "ripgrep")
	s.answer("Where should ripgrep be listed?", "1")
	s.answer("Which section?", "1")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitFor("listed ripgrep in ~/dotfiles/Brewfile", 1)
	s.answer("Commit it", "n")
	s.waitScreen(prompt)
	if got := read(t, f.Path("repo/Brewfile")); !strings.Contains(got, "brew \"jq\"     # json\nbrew \"ripgrep\"  # stub ripgrep\n\n# apps") {
		t.Fatalf("Brewfile:\n%s", got)
	}
	if calls := strings.Join(f.Calls(t), " "); !strings.Contains(calls, "brew_install_--formula_ripgrep") {
		t.Fatalf("not installed: %s", calls)
	}
	s.send(esc)
	s.exit()
}

// TestJourneyAddBrewForThisMac: this Mac only writes ~/.config/dott/files/Brewfile and a
// brew-local item in local.toml, then installs from it; the repo doesn't change.
func TestJourneyAddBrewForThisMac(t *testing.T) {
	f, env := machine(t)
	repoBefore := testkit.HashTree(t, f.Repo)
	s := open(t, env, 120, 40, dottBin(t), "add", "brew")
	s.answer("brew package", "hashicorp/tap/terraform")
	s.answer("Where should terraform be listed?", "2")
	s.answer("Trust hashicorp/tap/terraform?", "y")
	s.answer("Run it? [Y/n/copy]", "")
	s.waitFor("listed terraform in ~/.config/dott/files/Brewfile", 1)
	s.waitScreen(prompt)
	bf := read(t, f.Path("home/.config/dott/files/Brewfile"))
	if !strings.Contains(bf, "tap \"hashicorp/tap\"") || !strings.Contains(bf, "# local\nbrew \"hashicorp/tap/terraform\", trusted: true  # stub hashicorp/tap/terraform\n") {
		t.Fatalf("Brewfile:\n%s", bf)
	}
	if got := read(t, f.Path("home/.config/dott/local.toml")); !strings.Contains(got, `id = "brew-local"`) {
		t.Fatalf("local.toml:\n%s", got)
	}
	calls := strings.Join(f.Calls(t), " ")
	if !strings.Contains(calls, "brew_trust_--formula_hashicorp/tap/terraform") || !strings.Contains(calls, "brew_install_--formula_hashicorp/tap/terraform") {
		t.Fatalf("not trusted and installed: %s", calls)
	}
	if testkit.HashTree(t, f.Repo) != repoBefore {
		t.Fatal("the repo changed")
	}
	s.send(esc)
	s.exit()
}
