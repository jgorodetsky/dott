// dott is one fuzzy picker over a dotfiles setup: what is fine, what needs you, and the fixes.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=<v>".
var version = "dev"

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
		fmt.Fprintf(stdout, "dott %s\n", version)
		return 0
	}
	fmt.Fprintln(stderr, "dott: the picker is not built yet (milestone 1)")
	return 1
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
