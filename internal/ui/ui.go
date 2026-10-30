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
	Rows int // screen height, 0 when unknown
	tty  int // the terminal's fd for flushInput; 0 when In is not a terminal
}

// OpenTTY opens the controlling terminal, so questions work even when stdin is a pipe.
func OpenTTY() (*Term, func(), error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, func() {}, err
	}
	cols, rows, _ := term.GetSize(int(f.Fd()))
	return &Term{In: bufio.NewReader(f), Out: &screen{w: f, cols: cols}, Cols: cols, Rows: rows, tty: int(f.Fd())}, func() { f.Close() }, nil
}

// Fresh drops keys typed before a question or a picker showed, so an early y or enter
// (typed ahead while dott was busy) can't answer it.
func (t *Term) Fresh() {
	if t.tty == 0 {
		return
	}
	_ = flushInput(t.tty)
	_, _ = t.In.Discard(t.In.Buffered())
}

// screen counts the screen rows written since the last Mark, so a picker can leave the last
// of them in view. Color codes take no room; a line wider than the screen takes two rows.
type screen struct {
	w         io.Writer
	cols      int
	rows, col int
	inEscape  bool
}

func (s *screen) Write(p []byte) (int, error) {
	for _, r := range string(p) {
		switch {
		case s.inEscape:
			s.inEscape = r < 0x40 || r > 0x7e || r == '['
		case r == 0x1b:
			s.inEscape = true
		case r == '\n':
			s.rows, s.col = s.rows+1, 0
		case r == '\r':
			s.col = 0
		default:
			if s.col++; s.cols > 0 && s.col > s.cols {
				s.rows, s.col = s.rows+1, 1
			}
		}
	}
	return s.w.Write(p)
}

// Printed is how many screen rows were written since the last Mark.
func (t *Term) Printed() int {
	s, ok := t.Out.(*screen)
	if !ok {
		return 0
	}
	if s.col > 0 {
		return s.rows + 1
	}
	return s.rows
}

// Mark starts the count of Printed again.
func (t *Term) Mark() {
	if s, ok := t.Out.(*screen); ok {
		s.rows, s.col = 0, 0
	}
}

// echoed counts the line the terminal echoed for an answer: its enter moved to a new row.
func (t *Term) echoed() {
	if s, ok := t.Out.(*screen); ok {
		s.rows, s.col = s.rows+1, 0
	}
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
	t.echoed()
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
	t.Fresh()
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
	t.Fresh()
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

// Ask reads one line of text. ok is false when the input ended (ctrl-d) or was empty
// and there is no default.
func (t *Term) Ask(prompt, def string) (string, bool) {
	t.Fresh()
	if def != "" {
		fmt.Fprintf(t.Out, "%s \033[2m[%s]\033[0m: ", prompt, def)
	} else {
		fmt.Fprintf(t.Out, "%s: ", prompt)
	}
	s, err := t.In.ReadString('\n')
	t.echoed()
	if err != nil && s == "" {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = def
	}
	return s, s != ""
}

// YesNo asks a question; enter gives def.
func (t *Term) YesNo(q string, def bool) bool {
	t.Fresh()
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(t.Out, "%s %s ", q, hint)
	s, err := t.line()
	switch {
	case err != nil:
		return false
	case s == "":
		return def
	}
	return s == "y" || s == "yes"
}

// Choose asks for one of the options by number; enter picks the first. ok is false
// when the user typed nothing usable or q.
func (t *Term) Choose(q string, options []string) (int, bool) {
	t.Fresh()
	fmt.Fprintf(t.Out, "\033[1m%s\033[0m\n", q)
	for i, o := range options {
		fmt.Fprintf(t.Out, "  %d  %s\n", i+1, o)
	}
	fmt.Fprintf(t.Out, "pick 1-%d (enter: 1, q: cancel): ", len(options))
	s, err := t.line()
	if err != nil || s == "q" {
		return 0, false
	}
	if s == "" {
		return 0, true
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 1 || n > len(options) {
		return 0, false
	}
	return n - 1, true
}
