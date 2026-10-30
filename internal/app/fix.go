package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/rows"
)

// errShown is an error that was already printed.
var errShown = errors.New("failed (see above)")

func (a *App) item(id string) *manifest.Item {
	for i := range a.M.Items {
		if a.M.Items[i].ID == id {
			return &a.M.Items[i]
		}
	}
	return nil
}

func (a *App) status(id string) (engine.Status, bool) {
	for _, s := range a.All {
		if s.ID == id {
			return s, true
		}
	}
	return engine.Status{}, false
}

// fixPlan plans the fix of one item, or one part of it. replace lets it replace yours.
func (a *App) fixPlan(id, part string, replace bool) (*Plan, error) {
	it := a.item(id)
	s, ok := a.status(id)
	if it == nil || !ok {
		return nil, fmt.Errorf("no item %s", id)
	}
	changes, err := a.C.Kinds[it.Kind].Plan(a.C, it, s, part, replace)
	if err != nil {
		return nil, err
	}
	label := id
	if part != "" {
		label += "/" + part
	}
	p := &Plan{Title: "fix " + label}
	if replace {
		p.Title = "replace " + label + " with the repo's"
	}
	var shell []string
	for _, ch := range changes {
		p.Lines = append(p.Lines, ch.Summary)
		if ch.Shell != "" {
			shell = append(shell, ch.Shell)
		}
		p.Interactive = p.Interactive || ch.Interactive
	}
	p.Shell = strings.Join(shell, "\n")
	p.Run = func(a *App) error { return a.apply(it, part, changes) }
	return p, nil
}

// apply runs a fix and reports it. On a failure with a backup, it offers to put things back.
func (a *App) apply(it *manifest.Item, part string, changes []engine.Change) error {
	label := it.ID
	if part != "" {
		label += "/" + part
	}
	a.C.Out = a.w()
	res, err := engine.Apply(a.C, it, part, changes)
	if err == nil {
		got := res.After
		if part != "" {
			got, _ = res.After.Part(part)
		}
		a.say("%s %s: %s", green("✓"), label, got.Detail)
		if res.Backup != "" {
			a.say("  %s", dim("backup: "+a.Env.Short(res.Backup)+" (undo: dott restore)"))
		}
		return nil
	}
	a.say("%s %s: %v", red("✗"), label, err)
	if res.Backup == "" {
		return errShown
	}
	if a.T != nil && a.T.YesNo("Put it back as it was?", true) {
		if rerr := fsops.Restore(res.Backup); rerr != nil {
			a.say("%s could not put it back: %v (the backup is in %s)", red("✗"), rerr, a.Env.Short(res.Backup))
		} else {
			a.say("%s put back as it was", green("✓"))
		}
	} else {
		a.say("  %s", dim("the backup is in "+a.Env.Short(res.Backup)+"; dott restore puts it back"))
	}
	return errShown
}

type job struct {
	it      *manifest.Item
	changes []engine.Change
}

// fixAllPlan is fix everything: every item that drifted, in an order that puts what an item
// needs first. It leaves out your own things (conflicts), dangerous steps and items that ask
// their own questions, and says which.
func (a *App) fixAllPlan() (*Plan, error) {
	var jobs []job
	var skipped, later []string
	inJobs := map[string]bool{}
	for _, s := range a.All {
		it := a.item(s.ID)
		if it == nil {
			continue
		}
		for _, p := range s.Parts {
			if p.State == engine.Conflict {
				skipped = append(skipped, p.ID+" is yours (resolve it)")
			}
		}
		switch {
		case s.State == engine.Blocked:
			if a.unblockedBy(it, inJobs) {
				later = append(later, s.ID)
			} else {
				skipped = append(skipped, s.ID+" is blocked: "+s.Detail)
			}
			continue
		case s.State == engine.Conflict && len(s.Parts) == 0:
			skipped = append(skipped, s.ID+" is yours (resolve it)")
			continue
		case !hasDrift(s):
			continue
		case s.Interactive:
			skipped = append(skipped, s.ID+" asks its own questions (pick fix "+s.ID+")")
			continue
		}
		changes, err := a.C.Kinds[it.Kind].Plan(a.C, it, s, "", false)
		if err != nil {
			skipped = append(skipped, s.ID+": "+err.Error())
			continue
		}
		var keep []engine.Change
		for _, ch := range changes {
			if !ch.Danger && !ch.Interactive {
				keep = append(keep, ch)
			}
		}
		if len(keep) > 0 {
			jobs = append(jobs, job{it, keep})
			inJobs[it.ID] = true
		}
	}
	if len(jobs) == 0 {
		msg := "nothing to fix"
		if len(skipped) > 0 {
			msg += "; left for you: " + strings.Join(skipped, "; ")
		}
		return nil, errors.New(msg)
	}
	jobs = order(jobs)
	p := &Plan{Title: "fix everything"}
	var shell []string
	for _, j := range jobs {
		for _, ch := range j.changes {
			p.Lines = append(p.Lines, j.it.ID+": "+ch.Summary)
			if ch.Shell != "" {
				shell = append(shell, ch.Shell)
			}
		}
	}
	// an item that waits gets its steps planned now, so the plan shows what it will run; it is
	// planned again once what it needs is in place
	for _, id := range later {
		s, _ := a.status(id)
		after := strings.TrimSuffix(strings.TrimPrefix(s.Fix, "fix "), " first")
		changes, err := a.C.Kinds[a.item(id).Kind].Plan(a.C, a.item(id), s, "", false)
		if err != nil || len(changes) == 0 {
			p.Lines = append(p.Lines, id+": after "+after+", its own fix")
			continue
		}
		for _, ch := range changes {
			p.Lines = append(p.Lines, id+": after "+after+": "+ch.Summary)
			if ch.Shell != "" {
				shell = append(shell, ch.Shell)
			}
		}
	}
	p.Left = skipped
	p.Shell = strings.Join(shell, "\n")
	p.Run = func(a *App) error {
		failed := 0
		for _, j := range jobs {
			if a.apply(j.it, "", j.changes) != nil {
				failed++
			}
		}
		if len(later) > 0 {
			a.Scan()
			for _, id := range later {
				s, _ := a.status(id)
				if !hasDrift(s) || s.Interactive {
					a.say("%s %s: still %s: %s", amber("~"), id, s.State, s.Detail)
					continue
				}
				it := a.item(id)
				changes, err := a.C.Kinds[it.Kind].Plan(a.C, it, s, "", false)
				if err != nil {
					a.say("%s %s: %v", amber("~"), id, err)
					continue
				}
				if a.apply(it, "", changes) != nil {
					failed++
				}
			}
		}
		if failed > 0 {
			return fmt.Errorf("%s failed", plural(failed, "item", "items"))
		}
		return nil
	}
	return p, nil
}

func hasDrift(s engine.Status) bool {
	// a script's own state is apart from its parts': a stale build drifts with every part ok
	if s.State == engine.Drift && (len(s.Parts) == 0 || s.Kind == "script") {
		return true
	}
	for _, p := range s.Parts {
		if p.State == engine.Drift {
			return true
		}
	}
	return false
}

// unblockedBy says whether every need of a blocked item is fixed by the jobs before it.
func (a *App) unblockedBy(it *manifest.Item, inJobs map[string]bool) bool {
	if len(it.Needs) == 0 {
		return false
	}
	for _, n := range it.Needs {
		if pkg, ok := strings.CutPrefix(n, "brew:"); ok {
			if a.C.BrewHas(pkg) {
				continue
			}
			brew, _ := a.status(brewItem(a.M))
			if p, ok := brew.Part(pkg); !inJobs[brew.ID] || !ok || p.State != engine.Drift {
				return false
			}
			continue
		}
		if s, _ := a.status(n); s.State.NeedsYou() && !inJobs[n] {
			return false
		}
	}
	return true
}

func brewItem(m *manifest.Manifest) string {
	for _, it := range m.Items {
		if it.Kind == "brew-bundle" {
			return it.ID
		}
	}
	return ""
}

// order keeps the manifest order but moves an item after the items it needs.
func order(jobs []job) []job {
	var out []job
	placed := map[string]bool{}
	in := map[string]bool{}
	for _, j := range jobs {
		in[j.it.ID] = true
	}
	for len(out) < len(jobs) {
		moved := false
		for _, j := range jobs {
			if placed[j.it.ID] {
				continue
			}
			ready := true
			for _, n := range j.it.Needs {
				if in[n] && !placed[n] {
					ready = false
				}
			}
			if ready {
				out = append(out, j)
				placed[j.it.ID] = true
				moved = true
			}
		}
		if !moved { // a loop in needs: keep the rest as they are
			for _, j := range jobs {
				if !placed[j.it.ID] {
					out = append(out, j)
					placed[j.it.ID] = true
				}
			}
		}
	}
	return out
}

// resolveRows are the choices for something of yours in the way.
func (a *App) resolveRows(id, part string) ([]rows.Row, map[string]*Action) {
	label := id
	if part != "" {
		label += "/" + part
	}
	acts := map[string]*Action{}
	rs := []rows.Row{
		{ID: "keep", Tier: rows.MUT, Command: "keep mine", Desc: "turn " + label + " off on this Mac; yours stays as it is"},
		{ID: "replace", Tier: rows.DGR, Command: "replace with the repo's", Desc: "yours goes to the backup first; dott restore puts it back"},
	}
	acts["keep"] = &Action{Changes: func(a *App) (*Plan, error) { return a.switchPlan(label, true, false), nil }}
	acts["replace"] = &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, part, true) }}
	if mine, theirs, ok := a.comparePaths(id, part); ok {
		rs = append(rs, rows.Row{ID: "compare", Tier: rows.RO, Command: "compare", Desc: "yours (" + a.Env.Short(mine) + ") next to the repo's"})
		acts["compare"] = &Action{Show: func(a *App) error { return a.compare(mine, theirs) }}
	}
	return rs, acts
}

// comparePaths are yours and the repo's version of a conflicting item or part.
func (a *App) comparePaths(id, part string) (string, string, bool) {
	it := a.item(id)
	if it == nil {
		return "", "", false
	}
	c := a.C
	resolve := func(p string) string {
		if t, err := os.Readlink(p); err == nil {
			if !strings.HasPrefix(t, "/") {
				return p
			}
			return t
		}
		return p
	}
	switch it.Kind {
	case "link":
		return resolve(c.Path(it.Link)), c.Path(it.To), true
	case "copy":
		from, to := c.Path(it.From), c.Path(it.To)
		if part != "" {
			return to + "/" + part, from + "/" + part, true
		}
		return to, from, true
	case "rules":
		f, ok := kinds.RuleFile(c, it, part)
		return resolve(f), c.Path(it.Source), ok
	case "skills":
		return kinds.SkillCompare(c, it, part)
	}
	return "", "", false
}

// compare shows yours next to the repo's: nvim -d for two files, else diff.
func (a *App) compare(mine, theirs string) error {
	fm, err1 := os.Stat(mine)
	ft, err2 := os.Stat(theirs)
	if err1 == nil && err2 == nil && !fm.IsDir() && !ft.IsDir() && a.Env.Has("nvim") && a.T != nil {
		a.say(dim("left yours, right the repo's; read-only. :qa closes it"))
		return engine.TTY(a.M.Dir, []string{"nvim", "-d", "-R", mine, theirs})
	}
	out, _ := exec.Command("diff", "-ru", mine, theirs).CombinedOutput()
	ls := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(ls) > 300 {
		ls = append(ls[:300], fmt.Sprintf("… and %d more lines", len(ls)-300))
	}
	a.say("%s", bold("diff -ru "+a.Env.Short(mine)+" "+a.Env.Short(theirs)))
	a.say("%s", strings.Join(ls, "\n"))
	return nil
}

// offPlan turns an item or part off (or back on) on this Mac.
func (a *App) offPlan(id string, off bool) *Plan { return a.switchPlan(id, off, true) }

// switchPlan turns an item or part off (or back on) on this Mac. Turning off an item with
// undo also runs it, when undo is true; keep mine passes false, since yours stays.
func (a *App) switchPlan(id string, off, undo bool) *Plan {
	verb, line := "turn on ", "count "+id+" on this Mac again"
	if off {
		verb, line = "turn off ", "stop checking "+id+" on this Mac; nothing of yours changes"
	}
	var argv []string
	if it := a.item(id); off && undo && it != nil && len(it.Undo) > 0 && a.undoable(id) {
		argv = a.C.Argv(it.Undo)
		line = "stop checking " + id + " on this Mac"
	}
	p := &Plan{Title: verb + id, Lines: []string{line}}
	if argv != nil {
		p.Lines = append(p.Lines, "and undo it here: "+engine.Quote(argv...))
		p.Shell = engine.Quote(argv...)
	}
	p.Lines = append(p.Lines, dim("saved in "+a.Env.Short(a.C.CfgAt)))
	p.Run = func(a *App) error {
		if argv != nil {
			if err := a.runUndo(id, argv); err != nil {
				return err
			}
		}
		a.Cfg.SetOff(id, off)
		if err := a.SaveConfig(); err != nil {
			return err
		}
		a.say("%s %s%s", green("✓"), verb, id)
		return nil
	}
	return p
}

// undoable says whether an item is set up here at all, so undo has something to take away.
func (a *App) undoable(id string) bool {
	s, ok := a.status(id)
	return ok && s.State != engine.Off && s.State != engine.Absent
}

// runUndo runs an item's undo command. When it fails, the item stays on.
func (a *App) runUndo(id string, argv []string) error {
	x := &engine.Exec{C: a.C, Out: a.w()}
	if err := x.Stream(argv); err != nil {
		return fmt.Errorf("undo of %s failed, so it stays on: %v", id, err)
	}
	return nil
}

// groupPlan turns an optional group on or off on this Mac. Turning it off runs the undo of
// each of its items that has one, and stops at the first that fails.
func (a *App) groupPlan(g string, on bool) *Plan {
	var items []string
	type undo struct {
		id   string
		argv []string
	}
	var undos []undo
	for _, it := range a.M.Items {
		if it.Group == g {
			items = append(items, it.ID)
			if !on && len(it.Undo) > 0 && a.Cfg.Groups[g] && a.undoable(it.ID) {
				undos = append(undos, undo{it.ID, a.C.Argv(it.Undo)})
			}
		}
		for pkg, pg := range it.PackageGroups {
			if pg == g {
				items = append(items, it.ID+"/"+pkg)
			}
		}
	}
	verb := "turn off "
	line := "stop checking " + strings.Join(items, ", ") + " on this Mac; nothing installed is removed"
	if len(undos) > 0 {
		line = "stop checking " + strings.Join(items, ", ") + " on this Mac"
	}
	if on {
		verb = "turn on "
		line = "check " + strings.Join(items, ", ") + " on this Mac; then fix them from the list"
	}
	p := &Plan{Title: verb + g + ": " + a.M.Groups[g], Lines: []string{line}}
	var shell []string
	for _, u := range undos {
		p.Lines = append(p.Lines, "undo "+u.id+" here: "+engine.Quote(u.argv...))
		shell = append(shell, engine.Quote(u.argv...))
	}
	p.Shell = strings.Join(shell, "\n")
	p.Lines = append(p.Lines, dim("saved in "+a.Env.Short(a.C.CfgAt)))
	p.Run = func(a *App) error {
		for _, u := range undos {
			if err := a.runUndo(u.id, u.argv); err != nil {
				return err
			}
		}
		a.Cfg.Groups[g] = on
		if err := a.SaveConfig(); err != nil {
			return err
		}
		a.say("%s %s%s", green("✓"), verb, g)
		return nil
	}
	return p
}
