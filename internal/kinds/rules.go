package kinds

import (
	"fmt"
	"os"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Rules points every agent tool at one rules file in the repo. A tool either gets a link to
// it, or an @path import line in a marked block of its own file (claude), so your own lines
// there stay. Each tool is a part; a tool that isn't on this Mac is absent.
type Rules struct{}

func (Rules) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	src := c.Path(it.Source)
	if !exists(src) {
		return status(engine.Blocked, "the repo has no "+it.Source, "check the repo")
	}
	var parts []engine.Status
	for _, t := range it.Targets {
		st, detail, fix := ruleState(c, it, t, src)
		parts = append(parts, part(it.ID, t.Tool, st, detail, fix))
	}
	s := engine.Status{Parts: parts}
	s.State, s.Detail, s.Fix = engine.Roll(parts)
	if s.State == engine.OK {
		var tools []string
		for _, p := range parts {
			if p.State == engine.OK {
				tools = append(tools, partName(p))
			}
		}
		s.Detail = it.Source + ", read by " + strings.Join(tools, ", ")
	}
	return s
}

func ruleState(c *engine.Ctx, it *manifest.Item, t manifest.RuleTarget, src string) (engine.State, string, string) {
	if !c.When(t.When) {
		return engine.Absent, t.Tool + " is not on this Mac", ""
	}
	file := c.Path(t.File)
	if t.Method == "import" {
		ls, _, err := readLines(file)
		if err != nil {
			return engine.Blocked, "can't read " + c.Env.Short(file) + ": " + err.Error(), ""
		}
		for _, l := range ls {
			if l == "@"+src {
				return engine.OK, c.Env.Short(file) + " imports " + it.Source, ""
			}
		}
		if s, _ := find(ls, t.Start, t.End); s >= 0 {
			return engine.Drift, "the block in " + c.Env.Short(file) + " imports another file", "point it here"
		}
		return engine.Drift, "not set up: " + c.Env.Short(file) + " doesn't import " + it.Source, "add the import"
	}
	mine := c.Env.Short(file) + " has rules of its own"
	if n := countLines(file); n > 0 {
		mine += fmt.Sprintf(" (%d lines)", n)
	}
	return linkState(c, file, src, it.Source, mine)
}

func countLines(p string) int {
	ls, _, _ := readLines(p)
	n := 0
	for _, l := range ls {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func (Rules) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, name string, replace bool) ([]engine.Change, error) {
	src := c.Path(it.Source)
	var out []engine.Change
	for _, t := range it.Targets {
		if name != "" && t.Tool != name {
			continue
		}
		st, _, _ := ruleState(c, it, t, src)
		if st == engine.Conflict && name == "" {
			continue // fix everything keeps a tool's own rules
		}
		if st != engine.Drift && st != engine.Conflict {
			continue
		}
		file := c.Path(t.File)
		var ch []engine.Change
		var err error
		if t.Method == "import" {
			ch, err = blockChanges(c, file, t.Start, t.End, "@"+src)
		} else {
			ch, err = linkChanges(c, file, src, st, replace)
		}
		if err != nil {
			return nil, err
		}
		for i := range ch {
			ch[i].Summary = t.Tool + ": " + ch[i].Summary
		}
		out = append(out, ch...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing to link")
	}
	return out, nil
}

// RuleFile is the file a rules part writes, for compare.
func RuleFile(c *engine.Ctx, it *manifest.Item, tool string) (string, bool) {
	for _, t := range it.Targets {
		if t.Tool == tool {
			p := c.Path(t.File)
			_, err := os.Lstat(p)
			return p, err == nil
		}
	}
	return "", false
}
