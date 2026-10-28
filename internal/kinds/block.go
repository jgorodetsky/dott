package kinds

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Block is a marked block of lines in a file of yours: dott owns the lines between the start
// and end markers and never touches the rest. In seed mode it adds the block once and then
// leaves it to you.
type Block struct{}

func (Block) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	st, detail, fix := blockState(c, c.Path(it.File), it.Start, it.End, body(c, it.Body), it.Mode == "seed", it.SkipIf)
	return status(st, detail, fix)
}

// body fills {{repo}}, {{home}}, ~ and ${VAR} in a block body, line by line.
func body(c *engine.Ctx, b string) string {
	b = strings.ReplaceAll(c.M.Template(b, c.RulesSource()), "{{home}}", c.Env.Home)
	ls := strings.Split(strings.TrimRight(b, "\n"), "\n")
	for i, l := range ls {
		ls[i] = c.Env.Expand(l)
	}
	return strings.Join(ls, "\n")
}

// find returns the start and end line of the block, -1 when a marker is missing.
func find(ls []string, start, end string) (int, int) {
	s, e := -1, -1
	for i, l := range ls {
		if s < 0 && l == start {
			s = i
		} else if s >= 0 && l == end {
			e = i
			break
		}
	}
	return s, e
}

func blockState(c *engine.Ctx, file, start, end, want string, seed bool, skipIf string) (engine.State, string, string) {
	short := c.Env.Short(file)
	ls, ok, err := readLines(file)
	if err != nil {
		return engine.Blocked, "can't read " + short + ": " + err.Error(), ""
	}
	s, e := find(ls, start, end)
	switch {
	case s >= 0 && e < 0:
		return engine.Conflict, short + " has the start line of the block but no end line", "fix the file by hand: add " + end
	case s >= 0 && seed:
		return engine.OK, "added once in " + short + "; yours to change", ""
	case s >= 0:
		if strings.Join(ls[s+1:e], "\n") == want {
			return engine.OK, "block in " + short, ""
		}
		return engine.Drift, "the block in " + short + " differs from the repo's: " + first(ls[s+1:e]), "rewrite the block"
	}
	if seed && skipIf != "" {
		re := regexp.MustCompile(skipIf) // checked when the manifest loaded
		for _, l := range ls {
			if re.MatchString(l) {
				return engine.OK, "you set your own: " + strings.TrimSpace(l), ""
			}
		}
	}
	if !ok {
		return engine.Drift, "no " + short + " yet", "add the block"
	}
	return engine.Drift, "not set up in " + short, "add the block"
}

func first(ls []string) string {
	for _, l := range ls {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return "(empty)"
}

func (Block) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, part string, replace bool) ([]engine.Change, error) {
	if s.State != engine.Drift {
		return nil, fmt.Errorf("nothing to fix (%s)", s.State)
	}
	return blockChanges(c, c.Path(it.File), it.Start, it.End, body(c, it.Body))
}

func blockChanges(c *engine.Ctx, file, start, end, want string) ([]engine.Change, error) {
	ls, _, err := readLines(file)
	if err != nil {
		return nil, err
	}
	short := c.Env.Short(file)
	if s, e := find(ls, start, end); s >= 0 && e > s {
		return []engine.Change{{
			Summary: "rewrite the block in " + short + " (" + fmt.Sprint(e-s-1) + " lines → " + first(strings.Split(want, "\n")) + ")",
			Shell:   "# edit " + short + ": the lines between " + start + " and " + end,
			Paths:   []string{file},
			Do: func(*engine.Exec) error {
				ls, _, err := readLines(file)
				if err != nil {
					return err
				}
				s, e := find(ls, start, end)
				if s < 0 || e < 0 {
					return fmt.Errorf("the block in %s changed since the plan", short)
				}
				out := append(append(append([]string{}, ls[:s+1]...), strings.Split(want, "\n")...), ls[e:]...)
				return writeFile(file, []byte(strings.Join(out, "\n")+"\n"))
			},
		}}, nil
	}
	text := "\n" + start + "\n" + want + "\n" + end + "\n"
	return []engine.Change{{
		Summary: "add a marked block to the end of " + short + ": " + first(strings.Split(want, "\n")),
		Shell:   engine.Quote("printf", `\n%s\n%s\n%s\n`, start, want, end) + " >> " + engine.Quote(file),
		Paths:   []string{file},
		Do: func(*engine.Exec) error {
			b, _, err := readLines(file)
			if err != nil {
				return err
			}
			if s, _ := find(b, start, end); s >= 0 {
				return fmt.Errorf("%s got a block since the plan", short)
			}
			return appendRaw(file, text)
		},
	}}, nil
}

// appendRaw adds text at the very end of a file, the way >> does.
func appendRaw(p, text string) error {
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return writeFile(p, append(b, text...))
}
