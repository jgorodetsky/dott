package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jgorodetsky/dott/internal/picker"
)

const pickUsage = `usage: dott --pick [--prompt TEXT] [--header TEXT] [--query TEXT] [--multi] [--preview CMD] < lines
       dott --pick --input [--prompt TEXT] [--header TEXT] [--query TEXT] [--preview CMD]

a picker for scripts that dott runs: it prints the picked line (with --multi, tab picks more
than one and each prints on its own line). --input is a text box instead, and prints what
was typed; {q} in the --preview command is that text, {} the line under the cursor.
exit 0 picked, 1 esc, 2 a usage error, 130 ctrl-c
`

// newPicker is swapped in tests.
var newPicker = picker.New

// pick is dott --pick. It runs before the manifest loads, so a script can call it anywhere.
func pick(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	req := picker.Request{Plain: true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, inline := strings.Cut(a, "=")
		dst := map[string]*string{"--prompt": &req.Prompt, "--header": &req.Header, "--query": &req.Query, "--preview": &req.Preview}[name]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, pickUsage)
			return 0
		case a == "--multi":
			req.Multi = true
		case a == "--input":
			req.Input = true
		case dst != nil && inline:
			*dst = val
		case dst != nil && i+1 < len(args):
			i++
			*dst = args[i]
		case dst != nil:
			fmt.Fprintf(stderr, "dott --pick: %s needs a value\n", a)
			return 2
		default:
			fmt.Fprintf(stderr, "dott --pick: unknown argument %s\n%s", a, pickUsage)
			return 2
		}
	}
	if req.Prompt != "" && !strings.HasSuffix(req.Prompt, " ") {
		req.Prompt += " › "
	}
	if req.Input && req.Multi {
		fmt.Fprintln(stderr, "dott --pick: --input is a text box, so it can't take --multi")
		return 2
	}
	if !req.Input {
		if f, ok := stdin.(*os.File); ok {
			if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
				fmt.Fprintln(stderr, "dott --pick: give it the lines to pick on stdin, or --input for a text box")
				return 2
			}
		}
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if l := sc.Text(); l != "" {
				req.Lines = append(req.Lines, l)
			}
		}
		if len(req.Lines) == 0 {
			fmt.Fprintln(stderr, "dott --pick: no lines to pick from")
			return 2
		}
	}
	res, err := newPicker().Pick(req)
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "dott --pick:", err)
		return 2
	case res.Code == picker.CodeInterrupt:
		return 130
	case res.Back:
		return 1
	case req.Input:
		fmt.Fprintln(stdout, res.Query)
		return 0
	case len(res.Lines) == 0:
		return 1
	}
	for _, l := range res.Lines {
		fmt.Fprintln(stdout, l)
	}
	return 0
}
