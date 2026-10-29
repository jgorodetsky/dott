// Command dott-drive runs dott on a fake machine in a pseudo-terminal, types the steps it is
// given and prints the screens. It is for exploring dott the way a person does (the
// persona-qa skill uses it), never for a real HOME: the fake machine is the test fixture,
// with stub brew, npx and uvx, built in a temp folder.
//
//	go run ./cmd/dott-drive -size 80x24 -step 'line fix loader' -step 'wait Run it?' -step snap
//	go run ./cmd/dott-drive -setup fixed -step 'key enter' -step snap -- cfg
//
// Steps, in order:
//
//	type TEXT     type TEXT, no enter
//	line TEXT     type TEXT, wait until dott has drawn it and the screen is still, then enter
//	key NAME      enter, esc, ctrl-c, ctrl-d, up, down, tab, backspace
//	wait TEXT     wait (10s at most) until TEXT is on the screen; stop if it never is
//	still         wait until dott has answered the last keys and the screen is still for 300ms
//	sleep MS      wait MS milliseconds
//	snap          print the screen as a person sees it now
//
// At the end it prints the last screen, dott's exit code (or that it still runs), every stub
// call (brew, npx, uvx, editor) and every path under the fake HOME and repo that changed.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/journey"
	"github.com/jgorodetsky/dott/internal/testkit"
)

type steps []string

func (s *steps) String() string     { return strings.Join(*s, "; ") }
func (s *steps) Set(v string) error { *s = append(*s, v); return nil }

var keys = map[string]string{
	"enter": "\r", "esc": "\x1b", "ctrl-c": "\x03", "ctrl-d": "\x04", "up": "\x1b[A", "down": "\x1b[B",
	"tab": "\t", "backspace": "\x7f",
}

func main() {
	var ss steps
	size := flag.String("size", "120x40", "terminal size, COLSxROWS")
	setup := flag.String("setup", "answered", "fresh (nothing answered), answered (the optional group answered no), fixed (answered, then fix everything --yes)")
	origin := flag.Bool("origin", false, "make the repo a git checkout whose origin has one commit it doesn't")
	noBrew := flag.Bool("no-brew", false, "a Mac without Homebrew: no brew stub")
	slow := flag.Bool("slow", false, "every stub call takes 2 seconds")
	keep := flag.String("keep", "", "build the fake machine in this empty folder and keep it (default: a temp folder, removed after)")
	bin := flag.String("bin", "", "the dott binary (default: go build ./cmd/dott)")
	flag.Var(&ss, "step", "a step (repeat it); see the package doc")
	flag.Parse()
	if *noBrew {
		// dott looks for brew at /opt/homebrew/bin/brew too, so without the stub it would find the
		// real one, and a fix would install for real. Until dott can be told brew is missing, no.
		fmt.Fprintln(os.Stderr, "dott-drive: -no-brew is off: dott would find the real /opt/homebrew/bin/brew")
		os.Exit(2)
	}
	if err := run(ss, *size, *setup, *origin, *noBrew, *slow, *keep, *bin, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "dott-drive:", err)
		os.Exit(1)
	}
}

func run(ss steps, size, setup string, origin, noBrew, slow bool, keep, bin string, args []string) error {
	var cols, rows int
	if _, err := fmt.Sscanf(size, "%dx%d", &cols, &rows); err != nil || cols < 20 || rows < 5 {
		return fmt.Errorf("size %q: want COLSxROWS, like 80x24", size)
	}
	root := keep
	if root == "" {
		d, err := os.MkdirTemp("", "dott-drive-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(d)
		root = d
	}
	f, err := testkit.Make(root)
	if err != nil {
		return err
	}
	if bin == "" {
		bin = filepath.Join(root, "dott-under-test")
		if out, err := exec.Command("go", "build", "-o", bin, "./cmd/dott").CombinedOutput(); err != nil {
			return fmt.Errorf("go build ./cmd/dott (run dott-drive from the dott repo): %v\n%s", err, out)
		}
	}
	env, err := machine(f, setup, origin, noBrew, slow)
	if err != nil {
		return err
	}
	if setup == "fixed" {
		cmd := exec.Command(bin, "fix", "everything", "--yes")
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("fix everything --yes: %v\n%s", err, out)
		}
		if err := os.WriteFile(f.Log, nil, 0o644); err != nil {
			return err
		}
	}
	before := listing(f.Root)
	s, err := journey.Start(env, cols, rows, bin, args...)
	if err != nil {
		return err
	}
	defer s.Close()
	snaps := 0
	var sent time.Time // when the last keys went in, so line and still wait for their answer
	send := func(k string) error {
		sent = time.Now()
		return s.Send(k)
	}
	for _, step := range ss {
		verb, arg, _ := strings.Cut(step, " ")
		switch verb {
		case "type":
			err = send(arg)
		case "line":
			if err = send(arg); err == nil {
				_ = s.Settle(sent, 250*time.Millisecond, 10*time.Second)
				err = send("\r")
			}
		case "key":
			k, ok := keys[arg]
			if !ok {
				return fmt.Errorf("step %q: no key %q", step, arg)
			}
			err = send(k)
		case "wait":
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(s.Snapshot(), arg) && !strings.Contains(s.Text(), arg) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if !strings.Contains(s.Snapshot(), arg) && !strings.Contains(s.Text(), arg) {
				fmt.Printf("=== wait timed out after 10s: %q; the screen:\n%s", arg, s.Snapshot())
				return report(s, f, before)
			}
		case "still":
			err = s.Settle(sent, 300*time.Millisecond, 10*time.Second)
		case "sleep":
			ms, cerr := strconv.Atoi(arg)
			if cerr != nil {
				return fmt.Errorf("step %q: %v", step, cerr)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "snap":
			_ = s.Still(300*time.Millisecond, 10*time.Second)
			snaps++
			fmt.Printf("=== snap %d (after: %s)\n%s", snaps, step, s.Snapshot())
		default:
			return fmt.Errorf("step %q: unknown; use type, line, key, wait, still, sleep or snap", step)
		}
		if err != nil {
			return fmt.Errorf("step %q: %v", step, err)
		}
	}
	return report(s, f, before)
}

// machine sets up the fake machine like the journey tests do: the repo reached as
// ~/dotfiles, an editor that only logs, and the optional group answered unless fresh.
func machine(f *testkit.Fixture, setup string, origin, noBrew, slow bool) ([]string, error) {
	switch setup {
	case "fresh":
	case "answered", "fixed":
		if err := f.Put("home/.config/dott/config.toml", "[groups]\nextra = false\n"); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("setup %q: use fresh, answered or fixed", setup)
	}
	if err := f.Put("home/.config/fx/mine/own.conf", "mine\n"); err != nil {
		return nil, err
	}
	if err := f.Put("bin/editor", "#!/bin/sh\necho \"editor $*\" >> \"$DOTT_STUB_LOG\"\n"); err != nil {
		return nil, err
	}
	if err := os.Chmod(f.Path("bin/editor"), 0o755); err != nil {
		return nil, err
	}
	if noBrew {
		if err := os.Remove(f.Path("bin/brew")); err != nil {
			return nil, err
		}
	}
	if slow {
		for _, n := range []string{"brew", "npx", "uvx"} {
			p := f.Path("bin/" + n)
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if err := os.WriteFile(p, []byte(strings.Replace(string(b), "\n", "\nsleep 2\n", 1)), 0o755); err != nil {
				return nil, err
			}
		}
	}
	if err := os.Symlink(f.Repo, f.Path("home/dotfiles")); err != nil {
		return nil, err
	}
	if origin {
		if err := withOrigin(f); err != nil {
			return nil, err
		}
	}
	env := f.Environ("DOTT_REPO="+f.Path("home/dotfiles"), "EDITOR="+f.Path("bin/editor"))
	if noBrew {
		env = append(env, "PATH="+f.Bin+":/usr/bin:/bin:/usr/sbin:/sbin")
	}
	return env, nil
}

func withOrigin(f *testkit.Fixture) error {
	git := func(dir string, args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v\n%s", args, err, out)
		}
		return nil
	}
	for _, c := range []struct {
		dir  string
		args []string
	}{
		{f.Repo, []string{"init", "-q", "-b", "main"}}, {f.Repo, []string{"add", "."}}, {f.Repo, []string{"commit", "-q", "-m", "first"}},
		{f.Root, []string{"init", "-q", "--bare", "-b", "main", "origin.git"}},
		{f.Repo, []string{"remote", "add", "origin", f.Path("origin.git")}}, {f.Repo, []string{"push", "-q", "-u", "origin", "main"}},
		{f.Root, []string{"clone", "-q", f.Path("origin.git"), "other"}},
	} {
		if err := git(c.dir, c.args...); err != nil {
			return err
		}
	}
	if err := f.Put("other/notes.md", "new\n"); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "notes"}, {"push", "-q"}} {
		if err := git(f.Path("other"), args...); err != nil {
			return err
		}
	}
	return nil
}

func report(s *journey.Screen, f *testkit.Fixture, before map[string]string) error {
	_ = s.Still(300*time.Millisecond, 5*time.Second)
	fmt.Printf("=== last screen\n%s", s.Snapshot())
	if code, err := s.Exit(500 * time.Millisecond); err == nil {
		fmt.Printf("=== dott exited %d\n", code)
	} else {
		fmt.Println("=== dott is still running (the driver stops it now)")
	}
	b, _ := os.ReadFile(f.Log)
	fmt.Printf("=== stub calls\n%s", b)
	after := listing(f.Root)
	var changed []string
	for p, v := range after {
		if before[p] != v {
			changed = append(changed, "changed or new: "+p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed = append(changed, "removed: "+p)
		}
	}
	sort.Strings(changed)
	fmt.Printf("=== paths that changed under the fake root (home/ is the fake HOME, repo/ the dotfiles repo)\n%s\n", strings.Join(changed, "\n"))
	return nil
}

// listing maps every path under root (except the stub log and the binary) to its type and
// content, so the end can say what changed.
func listing(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "stub.log" || rel == "brew-installed" || rel == "dott-under-test" || strings.HasPrefix(rel, "origin.git") || strings.HasPrefix(rel, "other") || strings.Contains(rel, "/.git/") || strings.HasSuffix(rel, "/.git") {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		v := fi.Mode().String()
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			t, _ := os.Readlink(p)
			v += " -> " + t
		case fi.Mode().IsRegular():
			b, _ := os.ReadFile(p)
			v += " " + strconv.Itoa(len(b)) + " " + string(b[:min(len(b), 64)])
		}
		out[rel] = v
		return nil
	})
	return out
}
