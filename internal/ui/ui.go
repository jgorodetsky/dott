// Package ui asks the questions that guard each tier: Run it? [Y/n/copy] and type yes.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// Term is where questions are asked: the terminal, or anything in tests.
type Term struct {
	In   *bufio.Reader
	Out  io.Writer
	Cols int // screen width, 0 when unknown
}

// OpenTTY opens the controlling terminal, so questions work even when stdin is a pipe.
func OpenTTY() (*Term, func(), error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, func() {}, err
	}
	cols, _, _ := term.GetSize(int(f.Fd()))
	return &Term{In: bufio.NewReader(f), Out: f, Cols: cols}, func() { f.Close() }, nil
}

// Answer to Run it? [Y/n/copy].
type Answer int

const (
	No Answer = iota
	Yes
	Copy
)

func (t *Term) line() (string, error) {
	s, err := t.In.ReadString('\n')
	if err != nil && s == "" {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(s)), nil
}

// Plan prints what a change will do, one line per change.
func (t *Term) Plan(title string, plan []string) {
	fmt.Fprintf(t.Out, "\033[1m%s\033[0m\n", title)
	for _, p := range plan {
		fmt.Fprintf(t.Out, "  \033[33m◆\033[0m %s\n", p)
	}
}

// Confirm asks Run it? [Y/n/copy]. Enter means yes. Anything it doesn't know means no.
func (t *Term) Confirm() Answer {
	fmt.Fprint(t.Out, "Run it? [Y/n/copy] ")
	s, err := t.line()
	switch {
	case err != nil:
		return No
	case s == "" || s == "y" || s == "yes":
		return Yes
	case s == "c" || s == "copy":
		return Copy
	}
	return No
}

// TypeYes runs a dangerous change only after the word yes. Anything else cancels.
func (t *Term) TypeYes() bool {
	fmt.Fprint(t.Out, "\033[31m▲\033[0m type yes to run it, anything else cancels: ")
	s, err := t.line()
	return err == nil && s == "yes"
}

// Say prints one line.
func (t *Term) Say(format string, a ...any) {
	fmt.Fprintf(t.Out, format+"\n", a...)
}

// CopyText puts s on the clipboard with pbcopy.
func CopyText(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
