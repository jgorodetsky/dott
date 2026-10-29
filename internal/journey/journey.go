// Package journey runs a program in a pseudo-terminal the way a user does: it types keys,
// waits for text, and reads the screen back through a terminal emulator, so tests and the
// QA driver see the same grid of characters a person sees.
package journey

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// Screen is one program running in a pseudo-terminal.
type Screen struct {
	ptmx, tty *os.File
	cmd       *exec.Cmd
	vt        vt10x.Terminal
	mu        sync.Mutex
	buf       bytes.Buffer
	last      time.Time // when the program last wrote
	done      chan struct{}
	code      int
}

var ansiSeq = regexp.MustCompile(`\x1b(\[[0-9;?<=>]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[()][A-Z0-9]|[=>78])`)

// Start runs name with args in a new cols x rows pseudo-terminal. The emulator answers the
// program's questions about the cursor, as a real terminal does.
func Start(env []string, cols, rows int, name string, args ...string) (*Screen, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		ptmx.Close()
		tty.Close()
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		ptmx.Close()
		tty.Close()
		return nil, err
	}
	s := &Screen{ptmx: ptmx, tty: tty, cmd: cmd, vt: vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(ptmx)),
		last: time.Now(), done: make(chan struct{})}
	go func() {
		b := make([]byte, 4096)
		var held []byte // the start of a character that the last read cut in two
		for {
			n, err := ptmx.Read(b)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(b[:n])
				s.last = time.Now()
				s.mu.Unlock()
				var ready []byte
				ready, held = wholeRunes(held, b[:n])
				s.vt.Write(ready)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		s.code = cmd.ProcessState.ExitCode()
		close(s.done)
	}()
	return s, nil
}

// Close stops the program and frees the terminal.
func (s *Screen) Close() {
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.ptmx.Close()
	s.tty.Close()
}

// Text is everything the program wrote so far, without escape codes.
func (s *Screen) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansiSeq.ReplaceAllString(s.buf.String(), "")
}

// Snapshot is the screen as a person sees it now: each line without trailing spaces, and
// no blank lines at the end.
func (s *Screen) Snapshot() string {
	ls := strings.Split(s.vt.String(), "\n")
	for i := range ls {
		ls[i] = strings.TrimRight(ls[i], " ")
	}
	return strings.TrimRight(strings.Join(ls, "\n"), "\n") + "\n"
}

// Send types keys: text, "\r" for enter, "\x1b" for esc, "\x03" for ctrl-c.
func (s *Screen) Send(keys string) error {
	_, err := s.ptmx.Write([]byte(keys))
	return err
}

// WaitFor waits until want has been written n times in total.
func (s *Screen) WaitFor(want string, n int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(s.Text(), want) >= n {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("waited %s for %q x%d", timeout, want, n)
}

// Still waits until the program has written nothing for d, so a snapshot shows a screen
// that has settled.
func (s *Screen) Still(d, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		quiet := time.Since(s.last)
		s.mu.Unlock()
		if quiet >= d {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the screen kept changing for %s", timeout)
}

// Settle waits for the program to answer keys sent at sent (output after that time, for at
// most 2s), then for the screen to be still for d. Still alone returns at once on a screen
// that was quiet before the keys, before the program drew what they did.
func (s *Screen) Settle(sent time.Time, d, timeout time.Duration) error {
	for answered := time.Now().Add(2 * time.Second); time.Now().Before(answered); time.Sleep(10 * time.Millisecond) {
		s.mu.Lock()
		after := s.last.After(sent)
		s.mu.Unlock()
		if after {
			break
		}
	}
	return s.Still(d, timeout)
}

// Exit waits for the program to finish and returns its exit code.
func (s *Screen) Exit(timeout time.Duration) (int, error) {
	select {
	case <-s.done:
		return s.code, nil
	case <-time.After(timeout):
		return -1, fmt.Errorf("still running after %s", timeout)
	}
}

// wholeRunes joins what was held back with a new read, and splits off a character at its end
// that the read cut in two. vt10x drops such a part instead of waiting for the rest.
func wholeRunes(held, read []byte) (ready, rest []byte) {
	data := append(held, read...)
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				return data[:i], append([]byte(nil), data[i:]...)
			}
			break
		}
	}
	return data, nil
}
