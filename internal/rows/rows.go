// Package rows turns picker entries into the tab-separated lines fzf shows and searches.
package rows

import (
	"fmt"
	"regexp"
	"strings"
)

// Tier says what picking a row does, the same four tiers as ghh.
type Tier int

const (
	RO   Tier = iota // read-only: runs at once
	MUT              // change: shows the plan, then asks Run it? [Y/n/copy]
	DGR              // dangerous: the user types yes
	PICK             // guided: opens a follow-up picker
)

const (
	esc   = "\033["
	reset = esc + "0m"
)

var marks = map[Tier]string{
	RO:   esc + "32m●" + reset,
	MUT:  esc + "33m◆" + reset,
	DGR:  esc + "31m▲" + reset,
	PICK: esc + "36m▸" + reset,
}

var notes = map[Tier]string{
	RO:   "read-only, runs as soon as you press enter",
	MUT:  "changes things, shows what it will do and asks first",
	DGR:  "dangerous, you type yes to run it",
	PICK: "guided, asks for the details, then runs it",
}

var names = map[Tier]string{RO: "read-only", MUT: "change", DGR: "dangerous", PICK: "guided"}

func (t Tier) String() string { return names[t] }

// Mark is the colored tier symbol.
func (t Tier) Mark() string { return marks[t] }

// Note is the one-line explanation shown in the preview.
func (t Tier) Note() string { return notes[t] }

var stateColor = map[string]string{
	"ok": "32", "off": "2", "absent": "2", "drift": "33", "conflict": "31", "blocked": "31",
}

// Row is one entry in the picker.
type Row struct {
	ID      string // stable key, never shown
	Tier    Tier
	Command string // "fix aws"; <tokens> show in italics
	Desc    string
	State   string // ok, off, absent, drift, conflict, blocked, or empty
	Section string
	Detail  string   // first preview line; defaults to the command
	Plan    []string // what a change does, shown before Run it?
	Shell   string   // the same change as a shell command, for copy
	Next    []Row    // follow-up rows of a guided row
}

// Legend is the picker header.
const Legend = esc + "32m●" + reset + " runs now   " + esc + "33m◆" + reset + " asks first   " +
	esc + "31m▲" + reset + " type yes   " + esc + "36m▸" + reset + " guided"

// Fields of a line. fzf numbers them from 1: shown are 3..7, searched are 4..7
// (command, description, state, section), the preview reads 8 and 9.
const (
	WithNth = "3..7"
	Nth     = "2..5" // counted inside the shown fields
)

// Lines renders rows for a screen cols wide. Widths are shared, so the columns line up.
func Lines(rs []Row, cols int) []string {
	if cols <= 0 {
		cols = 100
	}
	wc, wd := 0, 0
	for _, r := range rs {
		wc = max(wc, len([]rune(r.Command)))
		wd = max(wd, len([]rune(r.Desc)))
	}
	wc = min(wc, cols*4/10)
	wd = max(0, min(wd, cols-wc-24))
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.line(wc, wd))
	}
	return out
}

func (r Row) line(wc, wd int) string {
	detail := r.Detail
	if detail == "" {
		detail = r.Command
	}
	state := ""
	if r.State != "" {
		state = esc + stateColor[r.State] + "m" + r.State + reset
	}
	fields := []string{
		clean(r.ID),
		fmt.Sprint(int(r.Tier)),
		r.Tier.Mark() + " ",
		paint(clean(r.Command)) + pad(r.Command, wc) + "  ",
		esc + "2m" + clean(r.Desc) + pad(r.Desc, wd) + reset + "  ",
		state,
		esc + "2;3m" + clean(r.Section) + reset,
		clean(detail),
		clean(r.Tier.Note()),
	}
	return strings.Join(fields, "\t")
}

// ID reads the row ID back from a line that fzf printed.
func ID(line string) string {
	id, _, _ := strings.Cut(line, "\t")
	return id
}

var token = regexp.MustCompile(`<[^>]*>`)

// paint makes the command bold with <tokens> in plain italics.
func paint(s string) string {
	return esc + "1m" + token.ReplaceAllStringFunc(s, func(t string) string {
		return esc + "22;3m" + t + esc + "23;1m"
	}) + reset
}

func pad(s string, w int) string {
	n := w - len([]rune(s))
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// clean keeps a field on one line and inside its own column.
func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
