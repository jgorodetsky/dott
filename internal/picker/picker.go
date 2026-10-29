// Package picker shows rows in fzf and returns what was picked.
//
// Two backends take the same arguments: the fzf library built into dott
// (the default) and an fzf binary named by DOTT_FZF (a fallback and a check
// that both behave the same).
package picker

import (
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
}

// Result is what came back from a picker screen.
type Result struct {
	Line  string // the picked line, empty when nothing was picked
	Query string // what was typed, so a screen can come back with it
	Back  bool   // esc: go back one screen
	Code  int    // 0 picked or back, 1 no match, 130 the user quit (ctrl-c)
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
		r.Line = got[2]
	}
	return r
}

// Quit codes from fzf.
const (
	CodeNoMatch   = 1
	CodeInterrupt = 130
)

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
