package kinds

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// IncludeLine makes sure a file of yours has some lines, and adds the missing ones. With
// format "gitconfig" the line is an include.path in a git config file; otherwise it is a
// plain line, or every code line of a repo file (lines_from).
type IncludeLine struct{}

var (
	section = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9.-]+)\s*("[^"]*")?\s*\]`)
	pathKey = regexp.MustCompile(`(?i)^\s*path\s*=\s*(.*)$`)
)

// gitIncludes reads the include.path values of a git config file, in order.
func gitIncludes(ls []string) []string {
	var out []string
	in := false
	for _, l := range ls {
		if m := section.FindStringSubmatch(l); m != nil {
			in = strings.EqualFold(m[1], "include") && m[2] == ""
			continue
		}
		if m := pathKey.FindStringSubmatch(l); in && m != nil {
			v := strings.TrimSpace(m[1])
			if strings.HasPrefix(v, `"`) {
				v = strings.Trim(v, `"`)
			} else if i := strings.IndexAny(v, "#;"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			out = append(out, v)
		}
	}
	return out
}

func (IncludeLine) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	file := c.Path(it.File)
	short := c.Env.Short(file)
	ls, _, err := readLines(file)
	if err != nil {
		return status(engine.Blocked, "can't read "+short+": "+err.Error(), "")
	}
	if it.Format == "gitconfig" {
		want := c.Path(it.Line)
		inc := gitIncludes(ls)
		for _, p := range inc {
			if c.Env.Expand(p) == want {
				return status(engine.OK, short+" includes "+c.Rel(want), "")
			}
		}
		for _, p := range inc {
			if o := otherCheckout(c, p, it.Line); o != "" {
				return status(engine.Drift, short+" includes another checkout: "+c.Env.Short(o), "point it here")
			}
		}
		return status(engine.Drift, "no include line in "+short, "add the include")
	}
	want, err := wanted(c, it)
	if err != nil {
		return status(engine.Blocked, err.Error(), "check the repo")
	}
	miss := missing(ls, want)
	if len(miss) == 0 {
		return status(engine.OK, fmt.Sprintf("%s has all %d lines", short, len(want)), "")
	}
	return status(engine.Drift, short+" lacks: "+strings.Join(miss, " "), "add them")
}

func wanted(c *engine.Ctx, it *manifest.Item) ([]string, error) {
	if it.Line != "" {
		return []string{c.Env.Expand(c.M.Template(it.Line, ""))}, nil
	}
	return codeLines(c.Path(it.LinesFrom))
}

func missing(have, want []string) []string {
	set := map[string]bool{}
	for _, l := range have {
		set[l] = true
	}
	var out []string
	for _, w := range want {
		if !set[w] {
			out = append(out, w)
		}
	}
	return out
}

func (IncludeLine) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, part string, replace bool) ([]engine.Change, error) {
	if s.State != engine.Drift {
		return nil, fmt.Errorf("nothing to fix (%s)", s.State)
	}
	file := c.Path(it.File)
	short := c.Env.Short(file)
	ls, _, err := readLines(file)
	if err != nil {
		return nil, err
	}
	if it.Format == "gitconfig" {
		want := c.Path(it.Line)
		for _, p := range gitIncludes(ls) {
			if o := otherCheckout(c, p, it.Line); o != "" {
				old := p
				return []engine.Change{{
					Summary: "point the include in " + short + " here: " + c.Env.Short(old) + " → " + c.Rel(want),
					Shell:   engine.Quote("git", "config", "--file", file, "--replace-all", "include.path", want, "^"+regexp.QuoteMeta(old)+"$"),
					Paths:   []string{file},
					Do: func(*engine.Exec) error {
						ls, _, err := readLines(file)
						if err != nil {
							return err
						}
						for i, l := range ls {
							if m := pathKey.FindStringSubmatch(l); m != nil && strings.Contains(l, old) {
								ls[i] = strings.Replace(l, old, want, 1)
							}
						}
						return writeFile(file, []byte(strings.Join(ls, "\n")+"\n"))
					},
				}}, nil
			}
		}
		return []engine.Change{{
			Summary: "add an include of " + c.Rel(want) + " to " + short + " (your settings stay)",
			Shell:   engine.Quote("git", "config", "--file", file, "--add", "include.path", want),
			Paths:   []string{file},
			Do: func(*engine.Exec) error {
				return appendText(file, "[include]\n\tpath = "+want+"\n")
			},
		}}, nil
	}
	want, err := wanted(c, it)
	if err != nil {
		return nil, err
	}
	miss := missing(ls, want)
	return []engine.Change{{
		Summary: "add to " + short + ": " + strings.Join(miss, " "),
		Shell:   engine.Quote("printf", `%s\n`) + " " + engine.Quote(miss...) + " >> " + engine.Quote(file),
		Paths:   []string{file},
		Do: func(*engine.Exec) error {
			ls, _, err := readLines(file)
			if err != nil {
				return err
			}
			if m := missing(ls, want); len(m) > 0 {
				return appendText(file, strings.Join(m, "\n")+"\n")
			}
			return nil
		},
	}}, nil
}
