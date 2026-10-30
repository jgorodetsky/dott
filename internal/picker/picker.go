// Package picker shows rows in fzf and returns what was picked.
//
// Two backends take the same arguments: the fzf library built into dott
// (the default) and an fzf binary named by DOTT_FZF (a fallback and a check
// that both behave the same).
package picker

import (
	"errors"
	"os"
	"strconv"

	"github.com/jgorodetsky/dott/internal/rows"
)

// Request is one picker screen.
type Request struct {
	Lines   []string // from rows.Lines
	Query   string
	Select1 bool   // a single match is picked without showing the picker
	Prompt  string // defaults to "dott › "
	Header  string // lines above the list; defaults to the legend
	History string // file for query history; empty for none
	Ghost   string // grey text in an empty query
	Height  int    // rows the picker takes; 0 for 32

	// dott --pick, for scripts: the lines as they are, with no columns, legend or history
	Plain   bool
	Multi   bool   // tab picks more than one line
	Input   bool   // no lines: a text box, whose text comes back as the query
	Preview string // a shell command whose output shows below; {} is the line, {q} the text
}

// Result is what came back from a picker screen.
type Result struct {
	Line  string   // the picked line, empty when nothing was picked
	Lines []string // every picked line, for Multi
	Query string   // what was typed, so a screen can come back with it
	Back  bool     // esc: go back one screen
	Code  int      // 0 picked or back, 1 no match, 130 the user quit (ctrl-c)
}

// result reads what fzf printed: the query, the key (esc or empty for enter), the line.
func result(got []string, code int) Result {
	r := Result{Code: code}
	if len(got) > 0 {
		r.Query = got[0]
	}
	if len(got) > 1 {
		r.Back = got[1] == "esc"
	}
	if len(got) > 2 {
		r.Line, r.Lines = got[2], got[2:]
	}
	return r
}

// Quit codes from fzf.
const (
	CodeNoMatch   = 1
	CodeError     = 2
	CodeInterrupt = 130
)

// errFailed is fzf's own error exit. It is what a picker on a terminal that is gone gets:
// without it, dott would take it as nothing picked and open the next picker at once, forever.
var errFailed = errors.New("the picker failed (fzf exit 2): its terminal is gone, or fzf hit an error")

// checked turns fzf's error exit into an error.
func checked(r Result, err error) (Result, error) {
	if err == nil && r.Code == CodeError {
		err = errFailed
	}
	return r, err
}

// Backend runs fzf.
type Backend interface {
	Pick(Request) (Result, error)
	// Filter returns the lines that match query, best first, without a screen.
	Filter(lines []string, query string) ([]string, error)
}

// New returns the fzf binary backend when DOTT_FZF names one, else the built-in fzf.
func New() Backend {
	if path := os.Getenv("DOTT_FZF"); path != "" {
		return Exec{Path: path}
	}
	return Lib{}
}

// match are the options that decide what is searched and how, shared by
// the picker and by Filter, so the ranking tests see what the user sees.
func match() []string {
	return []string{
		"--ansi", "--delimiter=\t", "--with-nth=" + rows.WithNth, "--nth=" + rows.Nth,
		"--tabstop=1", "--exact", "--scheme=history", "--tiebreak=chunk,index",
	}
}

func args(req Request) []string {
	if req.Plain {
		return plainArgs(req)
	}
	prompt, header := req.Prompt, req.Header
	if prompt == "" {
		prompt = "dott › "
	}
	if header == "" {
		header = rows.Legend
	}
	height := req.Height
	if height <= 0 {
		height = 32
	}
	a := append(match(),
		"--height="+strconv.Itoa(height), "--reverse", "--info=inline-right",
		"--query="+req.Query,
		"--print-query", "--expect=esc", // esc goes back a screen; ctrl-c quits
		"--prompt="+prompt,
		"--header="+header,
		"--ghost="+req.Ghost,
		"--color=hl:yellow:bold:underline,hl+:yellow:bold:underline",
		"--with-shell=/bin/sh -c",
		`--preview=printf '\033[1m%s\033[0m\n\033[2m%s\033[0m\n' {8} {9}`,
		"--preview-window=down,2,border-top,wrap",
	)
	if req.Select1 {
		a = append(a, "--select-1")
	}
	if req.History != "" {
		a = append(a, "--history="+req.History)
	}
	return a
}

// plainArgs are for dott --pick: a short list sized to fit, or a text box. Enter on no match
// does nothing; in a text box it gives back the text.
func plainArgs(req Request) []string {
	prompt := req.Prompt
	if prompt == "" {
		prompt = "dott pick › "
	}
	a := []string{"--exact", "--height=~20", "--reverse", "--info=inline-right",
		"--query=" + req.Query, "--print-query", "--expect=esc", "--prompt=" + prompt,
		"--color=hl:yellow:bold:underline,hl+:yellow:bold:underline", "--with-shell=/bin/sh -c"}
	if req.Header != "" {
		a = append(a, "--header="+req.Header)
	}
	if req.Multi {
		a = append(a, "--multi")
	}
	if req.Input {
		a = append(a, "--disabled", "--info=hidden", "--no-separator", "--bind=enter:accept-or-print-query")
	} else {
		a = append(a, "--bind=enter:accept-non-empty")
	}
	if req.Preview != "" {
		win := "--preview-window=down,3,border-top,wrap"
		if req.Input {
			win += ",noinfo" // no scroll count under a text box
		}
		a = append(a, "--preview="+req.Preview, win)
	}
	return a
}
