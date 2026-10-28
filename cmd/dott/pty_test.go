package main

import (
	"bytes"
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

	"github.com/creack/pty"
)

// screen runs dott in a pseudo-terminal, the way a user runs it.
type screen struct {
	t    *testing.T
	ptmx *os.File
	tty  *os.File
	cmd  *exec.Cmd
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan error
}

var ansiSeq = regexp.MustCompile(`\x1b(\[[0-9;?<=>]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[()][A-Z0-9]|[=>78])`)

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
		out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// start runs dott with args in a new pseudo-terminal.
func start(t *testing.T, args ...string) *screen {
	t.Helper()
	return startCmd(t, dottBin(t), args...)
}

// startCmd runs any program in a new pseudo-terminal.
func startCmd(t *testing.T, name string, args ...string) *screen {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &screen{t: t, ptmx: ptmx, tty: tty, cmd: cmd, done: make(chan error, 1)}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := ptmx.Read(b)
			s.mu.Lock()
			s.buf.Write(b[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() { s.done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		ptmx.Close()
		tty.Close()
	})
	return s
}

// text is everything on screen so far, without escape codes.
func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansiSeq.ReplaceAllString(s.buf.String(), "")
}

// waitFor waits until want shows up n times in total.
func (s *screen) waitFor(want string, n int) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(s.text(), want) >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("waited 10s for %q x%d; screen:\n%s", want, n, s.text())
}

func (s *screen) send(keys string) {
	s.t.Helper()
	if _, err := s.ptmx.Write([]byte(keys)); err != nil {
		s.t.Fatal(err)
	}
}

// exit waits for dott to finish and returns its exit code.
func (s *screen) exit() int {
	s.t.Helper()
	select {
	case <-s.done:
		return s.cmd.ProcessState.ExitCode()
	case <-time.After(10 * time.Second):
		s.t.Fatalf("dott did not exit; screen:\n%s", s.text())
		return -1
	}
}

const prompt = "dott › "

// TestPTYCtrlCRestoresTerminal runs dott from a shell, as a user does, and has that shell
// print the terminal modes (stty -g) before and after: they must match.
func TestPTYCtrlCRestoresTerminal(t *testing.T) {
	s := startCmd(t, "/bin/sh", "-c", `echo "BEFORE:$(stty -g)"; "$0"; echo "EXIT:$? AFTER:$(stty -g)"`, dottBin(t))
	s.waitFor(prompt, 1)
	s.waitFor("status shell", 1)
	s.send("\x03")
	s.waitFor("AFTER:", 1)
	text := s.text()
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
	s.send("status shell")
	s.waitFor("read-only, runs as soon as you press enter", 1) // the preview of the row under the cursor
	s.send("\r")
	s.waitFor("(demo, nothing to show yet)", 1)
	s.waitFor(prompt, 2) // the picker came back in the same process
	s.send("\x1b")
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.text())
	}
}

func TestPTYSingleMatchAsksFirst(t *testing.T) {
	s := start(t, "fix", "aws")
	s.waitFor("Run it? [Y/n/copy]", 1) // picked at once, but a change still asks
	if strings.Contains(s.text(), "nothing changed") {
		t.Fatal("ran before the answer")
	}
	s.send("n\r")
	s.waitFor("skipped fix aws", 1)
	s.waitFor(prompt, 1)
	s.send("\x1b")
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.text())
	}
}

func TestPTYDangerousNeedsYes(t *testing.T) {
	s := start(t, "resolve", "nvim")
	s.waitFor("resolve nvim › ", 1) // the guided row opened its follow-up picker
	s.send("replace")
	s.waitFor("dangerous, you type yes to run it", 1) // the cursor is on the dangerous row
	s.send("\r")
	s.waitFor("type yes to run it", 1)
	s.send("y\r")
	s.waitFor("cancelled replace with the repo's", 1)
	s.waitFor(prompt, 1)
	s.send("\x1b")
	if code := s.exit(); code != 0 {
		t.Fatalf("exit code %d, want 0; screen:\n%s", code, s.text())
	}
}
