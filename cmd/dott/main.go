// dott is one fuzzy picker over a dotfiles setup: what is fine, what needs you, and the fixes.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/jgorodetsky/dott/internal/app"
	"github.com/jgorodetsky/dott/internal/env"
	"github.com/jgorodetsky/dott/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=<v>".
var version = "dev"

// openTTY and newEnv are swapped in tests, so a test never opens the real terminal.
var (
	openTTY = ui.OpenTTY
	newEnv  = env.New
)

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
	}
	e := newEnv()
	if o.init != "" {
		if _, err := app.Init(e, o.init, stdout); err != nil {
			fmt.Fprintln(stderr, "dott:", err)
			return 1
		}
	}
	a, err := app.Load(e, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "dott:", err)
		return 2
	}
	a.Yes = o.yes
	if o.json {
		if err := a.JSON(); err != nil {
			fmt.Fprintln(stderr, "dott:", err)
			return 1
		}
		return 0
	}
	query := strings.Join(o.words, " ")
	term, closeTTY, err := openTTY()
	app.Color = err == nil && os.Getenv("NO_COLOR") == ""
	if err != nil {
		if o.init != "" && query == "" {
			if !o.yes {
				fmt.Fprintln(stdout, "next: run dott in a terminal to pick what to set up, or dott fix everything --yes")
				return 0
			}
			query = "fix everything"
		}
		if query == "" {
			fmt.Fprintln(stderr, "dott: no terminal; give words to list matching rows (dott drift), or --json")
			return 2
		}
		return a.Matches(query)
	}
	defer closeTTY()
	a.T = term
	return a.Session(query)
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
