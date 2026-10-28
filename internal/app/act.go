package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jgorodetsky/dott/internal/picker"
	"github.com/jgorodetsky/dott/internal/rows"
	"github.com/jgorodetsky/dott/internal/ui"
)

// Action is what a row does. Which field is used depends on the row's tier:
// read-only rows Show, change and dangerous rows build a Plan, guided rows open Next (a
// follow-up picker) or run Guide (questions, then their own confirm).
type Action struct {
	Show    func(a *App) error
	Changes func(a *App) (*Plan, error)
	Next    func(a *App) ([]rows.Row, map[string]*Action)
	Guide   func(a *App) error
}

// Plan is a change shown before it runs.
type Plan struct {
	Title       string
	Lines       []string
	Shell       string // the same change as shell commands, for copy
	Interactive bool   // it asks its own questions in the terminal
	Run         func(a *App) error
}

func printPlan(w io.Writer, p *Plan) {
	fmt.Fprintln(w, bold(p.Title))
	for _, l := range p.Lines {
		fmt.Fprintf(w, "  %s %s\n", amber("◆"), l)
	}
}

// Act does what the row's tier allows.
func (a *App) Act(r rows.Row, act *Action) {
	if act == nil {
		a.say("%s: nothing to do", r.Command)
		return
	}
	switch r.Tier {
	case rows.RO:
		if act.Show != nil {
			if err := act.Show(a); err != nil {
				a.say("%s %s: %v", red("✗"), r.Command, err)
			}
		}
	case rows.MUT, rows.DGR:
		a.confirmRun(r, act, r.Tier == rows.DGR)
	case rows.PICK:
		if act.Guide != nil {
			if err := act.Guide(a); err != nil {
				a.say("%s %s: %v", red("✗"), r.Command, err)
			}
			return
		}
		if act.Next == nil {
			return
		}
		next, acts := act.Next(a)
		if len(next) == 0 {
			a.say("%s: nothing to pick", r.Command)
			return
		}
		res, err := a.Pk.Pick(picker.Request{Lines: rows.Lines(next, a.T.Cols), Prompt: r.Command + " › "})
		if err != nil {
			a.say("dott: %v", err)
			return
		}
		if n, ok := findRow(next, rows.ID(res.Line)); ok {
			a.Act(n, acts[n.ID])
		} // esc goes back to the main picker
	}
}

// confirmRun shows the plan and runs it after Run it? [Y/n/copy], or after "yes" typed
// for a dangerous change.
func (a *App) confirmRun(r rows.Row, act *Action, danger bool) {
	if act.Changes == nil {
		return
	}
	p, err := act.Changes(a)
	if err != nil {
		a.say("%s %s: %v", amber("~"), r.Command, err)
		return
	}
	printPlan(a.T.Out, p)
	if danger {
		if !a.T.TypeYes() {
			a.say("cancelled %s", r.Command)
			return
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
			return
		case ui.No:
			a.say("skipped %s", r.Command)
			return
		}
	}
	if err := p.Run(a); err != nil && !errors.Is(err, errShown) {
		a.say("%s %s: %v", red("✗"), r.Command, err)
	}
}
