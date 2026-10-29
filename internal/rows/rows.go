// Package rows turns picker entries into the tab-separated lines fzf shows and searches.
package rows

import (
	"fmt"
	"regexp"
	"strings"
)

// Tier says what picking a row does: the four tiers of ghh, and items.
type Tier int

const (
	RO   Tier = iota // read-only: runs at once
	MUT              // change: shows the plan, then asks Run it? [Y/n/copy]
	DGR              // dangerous: the user types yes
	PICK             // guided: opens a follow-up picker
	ITEM             // an item or a part: opens its view; the mark shows its state
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

var stateMarks = map[string]string{
	"ok": esc + "32m✓" + reset, "drift": esc + "33m~" + reset, "conflict": esc + "31m!" + reset,
	"blocked": esc + "31m✗" + reset, "off": esc + "2m·" + reset, "absent": esc + "2m·" + reset,
}

var notes = map[Tier]string{
	RO:   "read-only, runs as soon as you press enter",
	MUT:  "changes things, shows what it will do and asks first",
	DGR:  "dangerous, you type yes to run it",
	PICK: "opens a list, or asks a few questions first; esc comes back",
	ITEM: "opens it: where it lives, its files, its fixes; esc comes back",
}

var names = map[Tier]string{RO: "read-only", MUT: "change", DGR: "dangerous", PICK: "guided", ITEM: "item"}

func (t Tier) String() string { return names[t] }

// Mark is the colored tier symbol.
func (t Tier) Mark() string { return marks[t] }

// Note is the one-line explanation shown in the preview.
func (t Tier) Note() string { return notes[t] }

var stateColor = map[string]string{
	"ok": "32", "off": "2", "absent": "2", "drift": "33", "conflict": "31", "blocked": "31", "risky": "31", "missing": "33",
}

// Row is one entry in the picker.
type Row struct {
	ID      string // stable key, never shown
	Tier    Tier
	Command string // "fix aws"; <tokens> show in italics
	Desc    string
	State   string // ok, off, absent, drift, conflict, blocked, or empty
	Section string
	Detail  string // first preview line; defaults to the command
}

// Legend is the last header line: the state marks of items, then the tier marks.
const Legend = esc + "32m✓" + reset + " ok " + esc + "33m~" + reset + " drift " + esc + "31m!" + reset + " conflict " +
	esc + "31m✗" + reset + " blocked " + esc + "2m·" + reset + " off " + esc + "2m│" + reset + " " +
	esc + "32m●" + reset + " runs " + esc + "33m◆" + reset + " asks " +
	esc + "31m▲" + reset + " type yes " + esc + "36m▸" + reset + " opens"

// Fields of a line. fzf numbers them from 1: shown are 3..7, searched are 4..7
// (command, state, section, description), the preview reads 8 and 9.
const (
	WithNth = "3..7"
	Nth     = "2..5" // counted inside the shown fields
)

// Lines renders rows for a screen cols wide: mark, command, state, section, description.
// Widths are shared, so the columns line up. The description comes last and is never cut,
// so every word in it can be searched; fzf cuts it at the edge of the screen.
func Lines(rs []Row, cols int) []string {
	if cols <= 0 {
		cols = 100
	}
	wc, ws, wsec := 0, 0, 0
	for _, r := range rs {
		wc = max(wc, width(r.Command))
		ws = max(ws, width(r.State))
		wsec = max(wsec, width(r.Section))
	}
	wc = min(wc, cols*4/10)
	wsec = min(wsec, 12)
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.line(wc, ws, wsec))
	}
	return out
}

func (r Row) line(wc, ws, wsec int) string {
	detail := r.Detail
	if detail == "" {
		detail = r.Command
	}
	mark := r.Tier.Mark()
	if r.Tier == ITEM {
		mark = stateMarks[r.State]
		if mark == "" {
			mark = " "
		}
	}
	gap := func(s string, w int) string {
		if w == 0 {
			return ""
		}
		return pad(s, w) + "  "
	}
	state := gap(r.State, ws)
	if r.State != "" {
		state = esc + stateColor[r.State] + "m" + r.State + reset + state
	}
	fields := []string{
		clean(r.ID),
		fmt.Sprint(int(r.Tier)),
		mark + " ",
		paint(clean(r.Command)) + gap(r.Command, wc),
		state,
		esc + "2;3m" + clean(r.Section) + reset + gap(r.Section, wsec),
		esc + "2m" + clean(r.Desc) + reset,
		clean(detail),
		clean(r.Tier.Note()),
	}
	return strings.Join(fields, "\t")
}

func width(s string) int { return len([]rune(s)) }

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
