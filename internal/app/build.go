package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/perms"
	"github.com/jgorodetsky/dott/internal/rows"
)

// Build turns the last scan into picker rows and what each one does.
//
// Order: the last pick, up to 7 rows picked often lately, fix everything, the rows that
// need you, turn on rows, the tools (summary, refresh, upgrade, aliases, edit,
// permissions, skills, restore), then every item that is fine.
func (a *App) Build() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var needs, on, fine []rows.Row
	add := func(dst *[]rows.Row, r rows.Row, act *Action) {
		*dst = append(*dst, r)
		acts[r.ID] = act
	}
	groupsOff := map[string][]string{}
	var groupOrder []string
	nFix := 0
	var fixNames []string
	for _, s := range a.All {
		s := s
		it := a.item(s.ID)
		show := &Action{Show: func(a *App) error { a.showItem(s); return nil }}
		base := rows.Row{Desc: s.Desc, State: string(s.State), Section: s.Section, Detail: s.Detail}
		switch s.State {
		case engine.OK, engine.Absent:
			r := base
			r.ID, r.Tier, r.Command = "show:"+s.ID, rows.RO, s.ID
			add(&fine, r, show)
			if s.State == engine.OK {
				a.partRows(s, &needs, add)
			}
			continue
		case engine.Off:
			if a.Cfg.IsOff(s.ID) || it == nil || it.Group == "" {
				r := base
				r.ID, r.Tier, r.Command = "on:"+s.ID, rows.MUT, "turn on "+s.ID
				id := s.ID
				add(&on, r, &Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(id, false), nil }})
				continue
			}
			if _, seen := groupsOff[it.Group]; !seen {
				groupOrder = append(groupOrder, it.Group)
			}
			groupsOff[it.Group] = append(groupsOff[it.Group], s.ID)
			continue
		case engine.Blocked:
			r := base
			r.ID, r.Tier, r.Command = "why:"+s.ID, rows.RO, "fix "+s.ID
			r.Detail = "blocked: " + s.Detail
			add(&needs, r, show)
			continue
		}
		if len(s.Parts) == 0 {
			id := s.ID
			if s.State == engine.Conflict {
				r := base
				r.ID, r.Tier, r.Command = "resolve:"+id, rows.PICK, "resolve "+id
				add(&needs, r, &Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.resolveRows(id, "") }})
				continue
			}
			r := base
			r.ID, r.Tier, r.Command = "fix:"+id, rows.MUT, "fix "+id
			add(&needs, r, &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, "", false) }})
			off := rows.Row{ID: "off:" + id, Tier: rows.MUT, Command: "turn off " + id, Desc: "stop checking it on this Mac", State: string(s.State), Section: s.Section}
			add(&needs, off, &Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(id, true), nil }})
			if !s.Interactive {
				nFix++
				fixNames = append(fixNames, id)
			}
			continue
		}
		if hasDrift(s) {
			id := s.ID
			r := base
			r.ID, r.Tier, r.Command = "fix:"+id, rows.MUT, "fix "+id
			add(&needs, r, &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, "", false) }})
			nFix++
			fixNames = append(fixNames, id)
		} else {
			r := base
			r.ID, r.Tier, r.Command = "show:"+s.ID, rows.RO, s.ID
			add(&needs, r, show)
		}
		a.partRows(s, &needs, add)
	}
	for _, g := range groupOrder {
		g := g
		r := rows.Row{ID: "on-group:" + g, Tier: rows.MUT, Command: "turn on " + g, Desc: a.M.Groups[g], State: "off",
			Detail: "optional: " + strings.Join(groupsOff[g], ", ")}
		add(&on, r, &Action{Changes: func(a *App) (*Plan, error) { return a.groupPlan(g, true), nil }})
	}
	for _, g := range a.groupsWithParts() {
		if _, done := groupsOff[g]; done || a.Cfg.Groups[g] {
			continue
		}
		g := g
		r := rows.Row{ID: "on-group:" + g, Tier: rows.MUT, Command: "turn on " + g, Desc: a.M.Groups[g], State: "off", Detail: "optional packages"}
		add(&on, r, &Action{Changes: func(a *App) (*Plan, error) { return a.groupPlan(g, true), nil }})
	}

	var top, tools []rows.Row
	if nFix > 0 {
		add(&top, rows.Row{ID: "fix-all", Tier: rows.MUT, Command: "fix everything",
			Desc: plural(nFix, "item drifted", "items drifted") + "; your own things and questions are left to you", Detail: list(fixNames, 6)},
			&Action{Changes: func(a *App) (*Plan, error) { return a.fixAllPlan() }})
	}
	a.toolRows(&tools, add)

	all := append(append(append(append(top, needs...), on...), tools...), fine...)
	return a.float(all), acts
}

// partRows adds a row for each part that needs you.
func (a *App) partRows(s engine.Status, dst *[]rows.Row, add func(*[]rows.Row, rows.Row, *Action)) {
	for _, p := range s.Parts {
		p := p
		name := p.ID[len(s.ID)+1:]
		id := s.ID
		desc := p.Desc
		if desc == "" {
			desc = s.Desc
		}
		r := rows.Row{Desc: desc, State: string(p.State), Section: s.Section, Detail: p.Detail}
		switch p.State {
		case engine.Drift:
			r.ID, r.Tier, r.Command = "fix:"+p.ID, rows.MUT, "fix "+p.ID
			add(dst, r, &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, name, false) }})
		case engine.Conflict:
			r.ID, r.Tier, r.Command = "resolve:"+p.ID, rows.PICK, "resolve "+p.ID
			add(dst, r, &Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.resolveRows(id, name) }})
		case engine.Blocked:
			r.ID, r.Tier, r.Command = "why:"+p.ID, rows.RO, "fix "+p.ID
			r.Detail = "blocked: " + p.Detail
			add(dst, r, &Action{Show: func(a *App) error {
				a.say("%s %s: %s\n  %s", red("✗"), p.ID, p.Detail, dim("next: "+p.Fix))
				return nil
			}})
		case engine.Off:
			if a.Cfg.IsOff(p.ID) {
				r.ID, r.Tier, r.Command = "on:"+p.ID, rows.MUT, "turn on "+p.ID
				pid := p.ID
				add(dst, r, &Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(pid, false), nil }})
			}
		}
	}
}

func (a *App) groupsWithParts() []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range a.M.Items {
		for _, g := range it.PackageGroups {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	sort.Strings(out)
	return out
}

// toolRows are the rows that are always there.
func (a *App) toolRows(dst *[]rows.Row, add func(*[]rows.Row, rows.Row, *Action)) {
	counts := map[engine.State]int{}
	for _, s := range a.All {
		counts[s.State]++
	}
	need := counts[engine.Drift] + counts[engine.Conflict] + counts[engine.Blocked]
	add(dst, rows.Row{ID: "summary", Tier: rows.RO, Command: "summary",
		Desc: fmt.Sprintf("%d ok · %d need you · %d off · %d not here", counts[engine.OK], need, counts[engine.Off], counts[engine.Absent])},
		&Action{Show: func(a *App) error { a.summary(); return nil }})
	add(dst, rows.Row{ID: "refresh", Tier: rows.PICK, Command: "refresh", Desc: "update: pull the repo, see what changed, then fix"},
		&Action{Guide: func(a *App) error { return a.refresh() }})
	add(dst, rows.Row{ID: "upgrade", Tier: rows.PICK, Command: "upgrade", Desc: "newer versions of what brew installed; you pick which"},
		&Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.upgradeRows() }})
	if a.M.Aliases != nil {
		add(dst, rows.Row{ID: "alias-add", Tier: rows.PICK, Command: "add alias <name> <command>", Desc: "on every machine (the repo) or this Mac only"},
			&Action{Guide: func(a *App) error { return a.addAlias() }})
		add(dst, rows.Row{ID: "alias-rm", Tier: rows.PICK, Command: "remove alias <name>", Desc: "asks where: the repo, this Mac, or both"},
			&Action{Guide: func(a *App) error { return a.removeAlias() }})
	}
	for _, e := range a.M.Edits {
		e := e
		p := a.C.Path(e.File)
		add(dst, rows.Row{ID: "edit:" + e.Name, Tier: rows.RO, Command: "edit " + e.Name, Desc: e.Desc, Detail: a.Env.Short(p)},
			&Action{Show: func(a *App) error { return a.edit(p) }})
	}
	for _, tool := range perms.Tools {
		if !a.toolHere(tool) {
			continue
		}
		tool := tool
		add(dst, rows.Row{ID: "perms:" + tool, Tier: rows.RO, Command: "permissions " + tool, Desc: "what " + tool + " may do without asking (read-only)"},
			&Action{Show: func(a *App) error { a.showPerms(tool); return nil }})
	}
	if a.skillsItem() != nil {
		add(dst, rows.Row{ID: "add-skill", Tier: rows.PICK, Command: "add skill", Desc: "from npx skills, a GitHub URL, a folder here, or a new one"},
			&Action{Guide: func(a *App) error { return a.addSkill() }})
	}
	if bs := fsops.Backups(a.C.St.Backups()); len(bs) > 0 {
		add(dst, rows.Row{ID: "restore", Tier: rows.PICK, Command: "restore a backup", Desc: plural(len(bs), "backup", "backups") + ", newest " + filepath.Base(bs[0])},
			&Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.restoreRows() }})
	}
	add(dst, rows.Row{ID: "turn-off", Tier: rows.PICK, Command: "turn off <item>", Desc: "stop checking an item or a group on this Mac"},
		&Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.turnOffRows() }})
	for _, cr := range a.Cfg.Rows {
		cr := cr
		add(dst, rows.Row{ID: "row:" + cr.Name, Tier: rows.MUT, Command: cr.Name, Desc: cr.Desc, Section: "yours"},
			&Action{Changes: func(a *App) (*Plan, error) {
				return &Plan{Title: cr.Name, Lines: []string{"run " + engine.Quote(cr.Run...)}, Shell: engine.Quote(cr.Run...), Interactive: true,
					Run: func(a *App) error { return engine.TTY(a.M.Dir, cr.Run) }}, nil
			}})
	}
}

func (a *App) toolHere(tool string) bool {
	h := a.Env.Home
	dirs := map[string]string{"claude": ".claude", "codex": ".codex", "opencode": ".config/opencode", "pi": ".pi"}
	return isDir(filepath.Join(h, dirs[tool])) || a.Env.Has(tool)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func (a *App) turnOffRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	for _, g := range sortedKeys(a.M.Groups) {
		if a.Cfg.Groups[g] {
			g := g
			rs = append(rs, rows.Row{ID: "group:" + g, Tier: rows.MUT, Command: g, Desc: "optional group: " + a.M.Groups[g]})
			acts["group:"+g] = &Action{Changes: func(a *App) (*Plan, error) { return a.groupPlan(g, false), nil }}
		}
	}
	for _, s := range a.All {
		if s.State == engine.Off {
			continue
		}
		id := s.ID
		rs = append(rs, rows.Row{ID: id, Tier: rows.MUT, Command: id, Desc: s.Desc, State: string(s.State), Section: s.Section})
		acts[id] = &Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(id, true), nil }}
		for _, p := range s.Parts {
			if p.State == engine.Off || p.State == engine.Absent {
				continue
			}
			pid := p.ID
			rs = append(rs, rows.Row{ID: pid, Tier: rows.MUT, Command: pid, Desc: p.Desc, State: string(p.State), Section: s.Section})
			acts[pid] = &Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(pid, true), nil }}
		}
	}
	return rs, acts
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// float moves the last pick and up to 7 rows picked often lately to the top.
func (a *App) float(all []rows.Row) []rows.Row {
	last, ranked := a.C.St.Frecent(time.Now())
	if last == "" {
		return all
	}
	idx := map[string]int{}
	for i, r := range all {
		idx[r.ID] = i
	}
	var top []rows.Row
	used := map[string]bool{}
	take := func(id string) {
		if i, ok := idx[id]; ok && !used[id] {
			top = append(top, all[i])
			used[id] = true
		}
	}
	take(last)
	for _, id := range ranked {
		if len(top) >= 8 {
			break
		}
		take(id)
	}
	for _, r := range all {
		if !used[r.ID] {
			top = append(top, r)
		}
	}
	return top
}
