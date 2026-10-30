package kinds

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Script hands an item to a script in the repo. The check command only reads and answers
// with its exit code (0 ok, 1 drift, 2 conflict, 3 blocked, 4 absent, 5 off: you haven't
// opted in), the first line it prints is the detail and the second the fix. Each line after
// that is a part: name<TAB>state<TAB>desc, with state ok, off or absent; the script's own
// actions change them. plan prints the changes, one per line; apply makes them.
type Script struct{}

var exitState = map[int]engine.State{0: engine.OK, 1: engine.Drift, 2: engine.Conflict, 3: engine.Blocked, 4: engine.Absent, 5: engine.Off}

// partStates are the states a script's part can have. Its fix is the script's business (its
// actions), so a part never gets dott's own fix rows.
var partStates = map[string]engine.State{"ok": engine.OK, "off": engine.Off, "absent": engine.Absent}

func (Script) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	if it.Present != "" {
		if p, err := exec.LookPath(it.Present); err == nil && c.Env.Has(it.Present) {
			return status(engine.OK, "installed: "+c.Env.Short(p), "")
		}
		return status(engine.Drift, it.Present+" is not installed", "install it")
	}
	argv := c.Argv(it.Check)
	out, code, err := c.Run(nil, argv, 15*time.Second)
	if err != nil {
		return status(engine.Blocked, "the check didn't run: "+err.Error(), "")
	}
	st, ok := exitState[code]
	if !ok {
		return status(engine.Blocked, fmt.Sprintf("the check exited %d: %s", code, firstLine(out)), "")
	}
	ls := strings.Split(strings.TrimRight(out, "\n"), "\n")
	detail, fix := strings.TrimSpace(ls[0]), ""
	if len(ls) > 1 {
		fix = strings.TrimSpace(ls[1])
	}
	s := status(st, detail, fix)
	for _, l := range ls[min(2, len(ls)):] {
		f := strings.Split(l, "\t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(f) != 3 || f[0] == "" {
			return status(engine.Blocked, fmt.Sprintf("the check printed a part line that isn't name<TAB>state<TAB>desc: %q", l), "")
		}
		pst, ok := partStates[f[1]]
		if !ok {
			return status(engine.Blocked, fmt.Sprintf("the check gave part %s the state %q (ok, off or absent)", f[0], f[1]), "")
		}
		p := part(it.ID, f[0], pst, f[2], "")
		p.Desc = f[2]
		s.Parts = append(s.Parts, p)
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (Script) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, part string, replace bool) ([]engine.Change, error) {
	if s.State != engine.Drift && !(s.State == engine.Conflict && replace) {
		return nil, fmt.Errorf("nothing to fix (%s)", s.State)
	}
	lines := it.PlanText
	if len(it.Plan) > 0 {
		out, code, err := c.Run(nil, c.Argv(it.Plan), 15*time.Second)
		if err != nil || code != 0 {
			return nil, fmt.Errorf("the plan command failed: %s", firstLine(out))
		}
		lines = nil
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
	}
	argv := c.Argv(it.Apply)
	if len(lines) == 0 {
		lines = []string{"run " + engine.Quote(argv...)}
	}
	var out []engine.Change
	for _, l := range lines[:len(lines)-1] {
		out = append(out, engine.Change{Summary: l})
	}
	out = append(out, engine.Change{
		Summary:     lines[len(lines)-1],
		Shell:       engine.Quote(argv...),
		Interactive: it.Interactive,
		Do: func(x *engine.Exec) error {
			if it.Interactive {
				return engine.TTY(c.M.Dir, argv)
			}
			return x.Stream(argv)
		},
	})
	return out, nil
}
