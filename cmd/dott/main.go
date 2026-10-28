// dott is one fuzzy picker over a dotfiles setup: what is fine, what needs you, and the fixes.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/jgorodetsky/dott/internal/picker"
	"github.com/jgorodetsky/dott/internal/rows"
	"github.com/jgorodetsky/dott/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=<v>".
var version = "dev"

// openTTY is swapped in tests, so a test never opens the real terminal.
var openTTY = ui.OpenTTY

const usage = `dott - one fuzzy picker for your dotfiles: what is fine, what needs you, and the fixes

usage:
  dott [words...]     open the picker, filtered by the words (dott aws, dott drift)

flags, for scripts:
  --json              print every item's state as JSON; changes nothing
  --init <repo>       set up this Mac from a dotfiles repo (owner/name or a path)
  --yes               answer yes to change prompts, never to dangerous ones
  --version           print the version
  --help              print this help

marks in the picker:
  ●  read-only     runs at once
  ◆  change        shows what it will do, then asks Run it? [Y/n/copy]
  ▲  dangerous     asks you to type yes
  ▸  guided        opens a follow-up picker
`

type options struct {
	help, version, json, yes bool
	init                     string
	words                    []string
}

// parse reads flags anywhere on the line; everything else is a search word.
// "--" ends the flags, so a word that starts with "-" can still be searched.
func parse(args []string) (options, error) {
	var o options
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			o.words = append(o.words, args[i+1:]...)
			return o, nil
		case a == "-h" || a == "--help":
			o.help = true
		case a == "-v" || a == "--version":
			o.version = true
		case a == "--json":
			o.json = true
		case a == "-y" || a == "--yes":
			o.yes = true
		case a == "--init":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--init needs a repo, for example --init owner/dotfiles")
			}
			i++
			o.init = args[i]
		case strings.HasPrefix(a, "--init="):
			o.init = strings.TrimPrefix(a, "--init=")
			if o.init == "" {
				return o, fmt.Errorf("--init needs a repo, for example --init owner/dotfiles")
			}
		case strings.HasPrefix(a, "-") && a != "-":
			return o, fmt.Errorf("unknown flag %s (dott --help lists the flags)", a)
		default:
			o.words = append(o.words, a)
		}
	}
	return o, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	o, err := parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "dott:", err)
		return 2
	}
	switch {
	case o.help:
		fmt.Fprint(stdout, usage)
		return 0
	case o.version:
		fmt.Fprintf(stdout, "dott %s (fzf %s)\n", version, fzfVersion())
		return 0
	case o.json:
		fmt.Fprintln(stderr, "dott: --json arrives with the scan (milestone 2)")
		return 1
	case o.init != "":
		fmt.Fprintln(stderr, "dott: --init arrives with the bootstrap (milestone 7)")
		return 1
	}
	return pick(strings.Join(o.words, " "), stdout, stderr)
}

// pick runs the picker until the user quits. Words given on the command line
// filter the first screen, and a single match is picked at once.
func pick(query string, stdout, stderr io.Writer) int {
	pk := picker.New()
	all := rows.Demo()
	term, closeTTY, err := openTTY()
	if err != nil {
		return printMatches(pk, all, query, stdout, stderr)
	}
	defer closeTTY()
	select1 := query != ""
	for {
		res, err := pk.Pick(picker.Request{Lines: rows.Lines(all, term.Cols), Query: query, Select1: select1})
		if err != nil {
			fmt.Fprintln(stderr, "dott:", err)
			return 2
		}
		r, ok := find(all, rows.ID(res.Line))
		if res.Code == picker.CodeInterrupt || !ok {
			return 0
		}
		act(pk, term, r)
		query, select1 = "", false // back to the whole picker
	}
}

// act does what the row's tier allows. Until the engine exists, nothing changes.
func act(pk picker.Backend, t *ui.Term, r rows.Row) {
	switch r.Tier {
	case rows.RO:
		t.Say("● %s: %s (demo, nothing to show yet)", r.Command, r.Desc)
	case rows.MUT:
		t.Plan(r.Command, r.Plan)
		switch t.Confirm() {
		case ui.Yes:
			t.Say("✓ %s (demo, nothing changed)", r.Command)
		case ui.Copy:
			if err := ui.CopyText(r.Shell); err != nil {
				t.Say("could not copy: %v", err)
			} else {
				t.Say("copied: %s", r.Shell)
			}
		default:
			t.Say("skipped %s", r.Command)
		}
	case rows.DGR:
		t.Plan(r.Command, r.Plan)
		if t.TypeYes() {
			t.Say("✓ %s (demo, nothing changed)", r.Command)
		} else {
			t.Say("cancelled %s", r.Command)
		}
	case rows.PICK:
		if len(r.Next) == 0 {
			t.Say("▸ %s arrives in a later milestone", r.Command)
			return
		}
		res, err := pk.Pick(picker.Request{Lines: rows.Lines(r.Next, t.Cols), Prompt: r.Command + " › "})
		if err != nil {
			t.Say("dott: %v", err)
			return
		}
		if n, ok := find(r.Next, rows.ID(res.Line)); ok {
			act(pk, t, n)
		} // esc goes back to the main picker
	}
}

// printMatches is the picker without a terminal: one plain line per matching row.
func printMatches(pk picker.Backend, all []rows.Row, query string, stdout, stderr io.Writer) int {
	got, err := pk.Filter(rows.Lines(all, 100), query)
	if err != nil {
		fmt.Fprintln(stderr, "dott:", err)
		return 2
	}
	if len(got) == 0 {
		fmt.Fprintf(stderr, "dott: no row matches %q\n", query)
		return 1
	}
	for _, line := range got {
		if r, ok := find(all, rows.ID(line)); ok {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.Tier, r.Command, r.Desc)
		}
	}
	return 0
}

func find(rs []rows.Row, id string) (rows.Row, bool) {
	for _, r := range rs {
		if r.ID == id && id != "" {
			return r, true
		}
	}
	return rows.Row{}, false
}

// fzfVersion is the fzf module version built into this binary.
func fzfVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/junegunn/fzf" {
				return strings.TrimPrefix(d.Version, "v")
			}
		}
	}
	return "unknown"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
