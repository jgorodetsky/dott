package picker

import (
	fzf "github.com/junegunn/fzf/src"
)

// Lib is the fzf library built into dott. This is the only file that imports fzf.
type Lib struct{}

// run feeds lines to fzf and collects what it prints.
// useDefaults is false, so FZF_DEFAULT_OPTS can't change how dott's picker behaves.
func (Lib) run(argv, lines []string) ([]string, int, error) {
	opts, err := fzf.ParseOptions(false, argv)
	if err != nil {
		return nil, 0, err
	}
	in := make(chan string)
	out := make(chan string)
	opts.Input, opts.Output = in, out
	go func() {
		for _, l := range lines {
			in <- l
		}
		close(in)
	}()
	var got []string
	done := make(chan struct{})
	go func() {
		for l := range out {
			got = append(got, l)
		}
		close(done)
	}()
	code, err := fzf.Run(opts)
	close(out)
	<-done
	return got, code, err
}

func (l Lib) Pick(req Request) (Result, error) {
	got, code, err := l.run(args(req), req.Lines)
	if err != nil {
		return Result{Code: code}, err
	}
	r := Result{Code: code}
	if len(got) > 0 {
		r.Line = got[0]
	}
	return r, nil
}

func (l Lib) Filter(lines []string, query string) ([]string, error) {
	got, code, err := l.run(append(match(), "--filter="+query), lines)
	if err != nil || code > CodeNoMatch {
		return nil, err
	}
	return got, nil
}
