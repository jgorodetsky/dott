package app

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/picker"
	"github.com/jgorodetsky/dott/internal/rows"
	"github.com/jgorodetsky/dott/internal/ui"
)

// Action is what a row does. Which field is used depends on the row's tier:
// read-only rows Show, change and dangerous rows build a Plan, guided rows open Next (a
// short follow-up picker that closes after its action), Open (a view that stays) or run
// Guide (questions, then their own confirm). Item rows Open their view.
type Action struct {
	Show    func(a *App) error
	Changes func(a *App) (*Plan, error)
	Next    func(a *App) ([]rows.Row, map[string]*Action)
	Open    func(a *App) View
	Guide   func(a *App) error
}

// View is one picker screen: the main list, an item, a folder, a folded list of tools.
// Its rows are built again after every action, from the latest scan.
type View struct {
	Prompt  string
	Header  func(a *App) string // lines above the legend; nil for none
	Rows    func(a *App) ([]rows.Row, map[string]*Action)
	Stay    bool   // after an action, show this view again; else go back
	Forward bool   // a query typed on the command line goes on into this view
	Ghost   string // grey hint in the empty query; defaults to esc goes back
}

// outcome is how a screen ended.
type outcome int

const (
	acted  outcome = iota // a row did something here; scan again
	looked                // a look, or a no: nothing changed, so stay as you were
	closed                // a deeper view did something and closed; its scan is fresh
	back                  // esc: show the screen below again, as it was
	quit                  // ctrl-c, or the picker failed: leave dott
)

// browse shows a view until esc, ctrl-c, or (for a view that doesn't stay) one action.
// It returns back, quit or closed.
// Each screen keeps its own query: esc from a deeper screen comes back to it as it was.
func (a *App) browse(v View, query string, select1 bool) outcome {
	for first := true; ; first = false {
		rs, acts := v.Rows(a)
		if len(rs) == 0 {
			a.say("%s: nothing to pick", strings.TrimSuffix(v.Prompt, " › "))
			return back
		}
		// words that went on into this view but match none of its rows: show all of them
		if first && select1 && query != "" {
			if got, err := a.Pk.Filter(rows.Lines(rs, a.T.Cols), query); err == nil && len(got) == 0 {
				query, select1 = "", false
			}
		}
		a.acts = acts
		if v.Ghost == "" {
			v.Ghost = "esc goes back"
		}
		header := rows.Legend
		if v.Header != nil {
			if h := v.Header(a); h != "" {
				header = h + "\n" + rows.Legend
			}
		}
		res, err := a.Pk.Pick(picker.Request{Lines: rows.Lines(rs, a.T.Cols), Query: query, Select1: select1,
			Prompt: v.Prompt, Header: header, History: a.history(), Ghost: v.Ghost, Height: a.pickerHeight()})
		a.T.Mark()
		if err != nil {
			a.say("dott: %v", err)
			a.failed = true
			return quit
		}
		if res.Code == picker.CodeInterrupt {
			return quit
		}
		if res.Back {
			return back
		}
		query = res.Query
		r, ok := findRow(rs, rows.ID(res.Line))
		if !ok || !a.stillMatches(res.Line, query) {
			// enter on no match, or a key that came before fzf's search for the query was
			// done (a paste): stay, with what was typed, and pick nothing
			select1 = false
			continue
		}
		_ = a.C.St.Pick(r.ID, time.Now())
		fwd := query != "" // what was typed goes on into a view that takes it: edit shell, add file
		select1 = false
		switch a.Act(r, acts[r.ID], query, fwd) {
		case quit:
			return quit
		case back:
			continue // came back from a deeper screen: keep this one's query
		case looked:
			// nothing changed: stay. After a no, what you typed stays, to try again; after a
			// look, the next pick is most likely another row of this list
			if r.Tier == rows.RO {
				query = ""
			}
			continue
		case acted:
			a.Scan()
		}
		if !v.Stay {
			return closed
		}
		query = ""
	}
}

// forwardQuery is the part of a query that goes on into the view a row opens: the words past
// the row's own name. A word that starts a word of the name is that name being typed:
// "optional parts" and "opt" on the optional parts row leave nothing, "edit rc" leaves "rc".
func forwardQuery(command, query string) string {
	name := strings.Fields(strings.ToLower(command))
	var rest []string
	for _, w := range strings.Fields(query) {
		if !slices.ContainsFunc(name, func(n string) bool { return strings.HasPrefix(n, strings.ToLower(w)) }) {
			rest = append(rest, w)
		}
	}
	return strings.Join(rest, " ")
}

// stillMatches says whether a picked line matches the query that came back with it. fzf takes
// enter on the list it shows, which for a query typed a moment ago can still be the full list.
func (a *App) stillMatches(line, query string) bool {
	if query == "" {
		return true
	}
	got, err := a.Pk.Filter([]string{line}, query)
	return err != nil || len(got) == 1
}

// pickerHeight leaves the last few rows dott printed (an error, a result, a backup's path) in
// view above the picker, on a screen too short for both. 0 keeps the picker's own height.
func (a *App) pickerHeight() int {
	const full, least, keep = 32, 10, 4
	if a.T.Rows == 0 || a.T.Rows-min(a.T.Printed(), keep) >= full {
		return 0
	}
	return max(least, a.T.Rows-min(a.T.Printed(), keep))
}

// Plan is a change shown before it runs.
type Plan struct {
	Title       string
	Lines       []string
	Left        []string // what it leaves alone, and why: not steps, so not in the copy text
	Shell       string   // the same change as shell commands, for copy
	Interactive bool     // it asks its own questions in the terminal
	Run         func(a *App) error
}

func printPlan(w io.Writer, p *Plan) {
	fmt.Fprintln(w, bold(p.Title))
	for _, l := range p.Lines {
		fmt.Fprintf(w, "  %s %s\n", amber("◆"), l)
	}
	for _, l := range p.Left {
		fmt.Fprintf(w, "  %s\n", dim("· left alone: "+l))
	}
}

// Act does what the row's tier allows. A row that opens a screen returns how that screen
// ended; with fwd, query goes on into a view that takes it, and a single match there is
// picked at once.
func (a *App) Act(r rows.Row, act *Action, query string, fwd bool) outcome {
	// messages and prompts name the row without its placeholder: add brew, not add brew <package>
	if i := strings.Index(r.Command, " <"); i > 0 {
		r.Command = r.Command[:i]
	}
	if act == nil {
		a.say("%s: nothing to do", r.Command)
		return acted
	}
	switch {
	case r.Tier == rows.RO && act.Show != nil:
		if err := act.Show(a); err != nil {
			a.say("%s %s: %v", red("✗"), r.Command, err)
		}
		return looked
	case r.Tier == rows.MUT || r.Tier == rows.DGR:
		if !a.confirmRun(r, act, r.Tier == rows.DGR) {
			return looked
		}
	case act.Guide != nil:
		if err := act.Guide(a); err != nil {
			a.say("%s %s: %v", red("✗"), r.Command, err)
		}
	case act.Open != nil:
		v := act.Open(a)
		if v.Prompt == "" {
			v.Prompt = r.Command + " › "
		}
		if query = forwardQuery(r.Command, query); !v.Forward || !fwd || query == "" {
			query, fwd = "", false
		}
		return a.browse(v, query, fwd)
	case act.Next != nil:
		return a.browse(View{Prompt: r.Command + " › ", Rows: func(*App) ([]rows.Row, map[string]*Action) { return act.Next(a) }}, "", false)
	}
	return acted
}

// confirmRun shows the plan and runs it after Run it? [Y/n/copy], or after "yes" typed
// for a dangerous change. It says whether the change ran.
func (a *App) confirmRun(r rows.Row, act *Action, danger bool) bool {
	if act.Changes == nil {
		return false
	}
	p, err := act.Changes(a)
	if err != nil {
		a.say("%s %s: %v", amber("~"), r.Command, err)
		return false
	}
	printPlan(a.T.Out, p)
	if danger {
		if !a.T.TypeYes() {
			a.say("cancelled %s", r.Command)
			return false
		}
	} else {
		ans := ui.Yes
		if a.Yes {
			a.say("Run it? yes (--yes)")
		} else {
			ans = a.T.Confirm()
		}
		switch ans {
		case ui.Copy:
			shell := p.Shell
			if shell == "" {
				shell = "# " + strings.Join(p.Lines, "\n# ")
			}
			if err := ui.CopyText(shell); err != nil {
				a.say("could not copy: %v", err)
			} else {
				a.say("copied:\n%s", dim(shell))
			}
			return false
		case ui.No:
			a.say("skipped %s", r.Command)
			return false
		}
	}
	if err := p.Run(a); err != nil && !errors.Is(err, errShown) {
		a.say("%s %s: %v", red("✗"), r.Command, err)
	}
	return true // a failed run can still have changed some of it
}
