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

type adder func(*[]rows.Row, rows.Row, *Action)

// mainView is the first screen: what needs you, every item, then the tools.
func (a *App) mainView() View {
	return View{Prompt: "dott › ", Header: (*App).mainHeader, Rows: (*App).Build, Stay: true, Ghost: "type any words, like: " + a.example() + "; esc quits"}
}

// example is a query for the grey hint that finds a row of this repo: a fix that is waiting,
// else the first item.
func (a *App) example() string {
	for _, s := range a.All {
		if s.State == engine.Drift {
			return "fix " + s.ID
		}
	}
	if len(a.All) > 0 {
		return a.All[0].ID
	}
	return "fix everything"
}

// mainHeader is the status line: the repo, its branch and how many items are in each state.
func (a *App) mainHeader() string {
	counts := map[engine.State]int{}
	for _, s := range a.All {
		counts[s.State]++
	}
	head := a.Env.Short(a.M.Dir)
	if a.branch != "" {
		head += " (" + a.branch + ")"
	}
	need := counts[engine.Drift] + counts[engine.Conflict] + counts[engine.Blocked]
	needs := plural(need, "needs you", "need you")
	if need > 0 {
		needs = amber(needs)
	}
	line := fmt.Sprintf("%s %s %s ok · %s · %d off · %d not here", bold("dott"), dim("· "+head+" ·"),
		green(fmt.Sprint(counts[engine.OK])), needs, counts[engine.Off], counts[engine.Absent])
	if n := a.notice(0); n != "" {
		line += "\n" + amber("↑ "+n)
	}
	return line
}

// Build turns the last scan into the main screen's rows and what each one does.
//
// Order: the last pick, up to 7 rows picked often lately, fix everything, the rows that
// need you, every item (each opens its view), then the tools.
func (a *App) Build() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var needs, items []rows.Row
	add := func(dst *[]rows.Row, r rows.Row, act *Action) {
		*dst = append(*dst, r)
		acts[r.ID] = act
	}
	var fixNames []string
	for _, s := range a.All {
		if a.needRows(s, &needs, add) {
			fixNames = append(fixNames, s.ID)
		}
		add(&items, a.itemRow(s), a.openItem(s.ID))
	}
	var top, tools []rows.Row
	if n := len(fixNames); n > 0 {
		add(&top, rows.Row{ID: "fix-all", Tier: rows.MUT, Command: "fix everything",
			Desc: plural(n, "item", "items") + " in drift · your own things and questions are left to you", Detail: list(fixNames, 6)},
			&Action{Changes: func(a *App) (*Plan, error) { return a.fixAllPlan() }})
	}
	a.toolRows(&tools, add)
	all := append(append(append(top, needs...), items...), tools...)
	return a.float(all), acts
}

func (a *App) itemRow(s engine.Status) rows.Row {
	r := rows.Row{ID: "item:" + s.ID, Tier: rows.ITEM, Command: s.ID, Desc: s.Desc, State: string(s.State), Section: s.Section, Detail: s.Detail}
	if it := a.item(s.ID); it != nil && it.Origin == "local" {
		r.Desc += " · this Mac only"
	}
	// a script's parts (clipfmt's link rules) go last, past the screen's edge mostly, so a
	// part's name finds its item. Other kinds' parts have their own fix rows when they need you.
	var names []string
	for _, p := range s.Parts {
		if s.Kind == "script" {
			names = append(names, p.ID[len(s.ID)+1:])
		}
	}
	if len(names) > 0 {
		r.Desc += " · " + strings.Join(names, ", ")
	}
	return r
}

func (a *App) openItem(id string) *Action {
	return &Action{Open: func(a *App) View { return a.itemView(id) }}
}

// needRows adds the rows for what needs you in one item: a fix, a resolve or why it is
// blocked, for the item and for each of its parts. It says whether fix everything takes it.
func (a *App) needRows(s engine.Status, dst *[]rows.Row, add adder) bool {
	id := s.ID
	base := rows.Row{Desc: s.Desc, State: string(s.State), Section: s.Section, Detail: s.Detail}
	switch s.State {
	case engine.OK, engine.Absent, engine.Off:
		return false
	case engine.Blocked:
		r := base
		r.ID, r.Tier, r.Command, r.Detail = "why:"+id, rows.RO, "fix "+id, "blocked: "+s.Detail
		add(dst, r, &Action{Show: func(a *App) error { a.why(id, s.Detail, s.Fix); return nil }})
		return false
	}
	if len(s.Parts) == 0 {
		r := base
		if s.State == engine.Conflict {
			r.ID, r.Tier, r.Command = "resolve:"+id, rows.PICK, "resolve "+id
			add(dst, r, &Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.resolveRows(id, "") }})
			return false
		}
		r.ID, r.Tier, r.Command = "fix:"+id, rows.MUT, "fix "+id
		add(dst, r, &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, "", false) }})
		return !s.Interactive
	}
	fix := hasDrift(s)
	if fix {
		r := base
		r.ID, r.Tier, r.Command = "fix:"+id, rows.MUT, "fix "+id
		add(dst, r, &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, "", false) }})
	}
	for _, p := range s.Parts {
		if r, act, ok := a.partNeed(s, p); ok {
			add(dst, r, act)
		}
	}
	return fix
}

// partNeed is the row for a part that needs you: a fix, a resolve or why it is blocked.
func (a *App) partNeed(s, p engine.Status) (rows.Row, *Action, bool) {
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
		return r, &Action{Changes: func(a *App) (*Plan, error) { return a.fixPlan(id, name, false) }}, true
	case engine.Conflict:
		r.ID, r.Tier, r.Command = "resolve:"+p.ID, rows.PICK, "resolve "+p.ID
		return r, &Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.resolveRows(id, name) }}, true
	case engine.Blocked:
		r.ID, r.Tier, r.Command, r.Detail = "why:"+p.ID, rows.RO, "fix "+p.ID, "blocked: "+p.Detail
		return r, &Action{Show: func(a *App) error { a.why(p.ID, p.Detail, p.Fix); return nil }}, true
	}
	return r, nil, false
}

// why says what a blocked item or part is waiting for.
func (a *App) why(id, detail, fix string) {
	a.say("%s %s: %s", red("✗"), id, detail)
	if fix != "" {
		a.say("  %s", dim("next: "+fix))
	}
}

// toolRows are the rows that are always there. Lists of more than one thing fold into a
// row that opens them, so the items stay on the first screen.
func (a *App) toolRows(dst *[]rows.Row, add adder) {
	if gs := sortedKeys(a.M.Groups); len(gs) > 0 {
		off := 0
		for _, g := range gs {
			if !a.Cfg.Groups[g] {
				off++
			}
		}
		add(dst, rows.Row{ID: "parts", Tier: rows.PICK, Command: "optional parts", Desc: "turn on or off: " + strings.Join(gs, ", "),
			Detail: fmt.Sprintf("%d on · %d off on this Mac", len(gs)-off, off)},
			&Action{Open: func(a *App) View { return View{Rows: (*App).groupRows, Stay: true, Forward: true} }})
	}
	add(dst, rows.Row{ID: "refresh", Tier: rows.PICK, Command: "refresh", Desc: "update: fetch, see what changed, then pull and fix after a yes each"},
		&Action{Guide: func(a *App) error { return a.refresh() }})
	add(dst, rows.Row{ID: "upgrade", Tier: rows.PICK, Command: "upgrade", Desc: "newer versions of what brew installed; you pick which"},
		&Action{Next: func(a *App) ([]rows.Row, map[string]*Action) { return a.upgradeRows() }})
	if adds, _ := a.addRows(); len(adds) > 0 {
		names := map[string]string{"file": "a file", "brew": "a brew package", "alias": "an alias", "skill": "a skill"}
		var what []string
		for _, r := range adds {
			what = append(what, names[strings.Fields(r.Command)[1]])
		}
		if len(what) > 1 {
			what = append(what[:len(what)-2], what[len(what)-2]+" or "+what[len(what)-1])
		}
		add(dst, rows.Row{ID: "add", Tier: rows.PICK, Command: "add", Desc: "add " + strings.Join(what, ", ") + ": on every machine or this Mac only"},
			&Action{Open: func(a *App) View { return View{Rows: (*App).addRows, Forward: true} }})
	}
	if a.M.Aliases != nil {
		add(dst, rows.Row{ID: "alias-rm", Tier: rows.PICK, Command: "remove alias <name>", Desc: "asks where: the repo, this Mac, or both"},
			&Action{Guide: func(a *App) error { return a.removeAlias() }})
	}
	if len(a.M.Edits) > 0 {
		var names []string
		for _, e := range a.M.Edits {
			names = append(names, e.Name)
		}
		add(dst, rows.Row{ID: "edits", Tier: rows.PICK, Command: "edit a file", Desc: strings.Join(names, ", "), Detail: plural(len(names), "file", "files") + " you edit often"},
			&Action{Open: func(a *App) View { return View{Rows: (*App).editRows, Stay: true, Forward: true} }})
	}
	if tools := a.permTools(); len(tools) > 0 {
		add(dst, rows.Row{ID: "perms", Tier: rows.PICK, Command: "permissions", Desc: strings.Join(tools, ", ") + ": what each may do without asking (read-only)"},
			&Action{Open: func(a *App) View { return View{Rows: (*App).permRows, Stay: true, Forward: true} }})
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

// groupRows turn each optional group on or off.
func (a *App) groupRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	for _, g := range sortedKeys(a.M.Groups) {
		g := g
		on := a.Cfg.Groups[g]
		r := rows.Row{ID: "group:" + g, Tier: rows.MUT, Command: "turn on " + g, Desc: a.M.Groups[g], State: "off", Detail: a.groupDetail(g)}
		if on {
			r.Command, r.State = "turn off "+g, "on"
		}
		rs = append(rs, r)
		acts[r.ID] = &Action{Changes: func(a *App) (*Plan, error) { return a.groupPlan(g, !on), nil }}
	}
	return rs, acts
}

// groupDetail names what a group holds.
func (a *App) groupDetail(g string) string {
	var in []string
	for _, it := range a.M.Items {
		if it.Group == g {
			in = append(in, it.ID)
		}
		for pkg, pg := range it.PackageGroups {
			if pg == g {
				in = append(in, it.ID+"/"+pkg)
			}
		}
	}
	sort.Strings(in)
	return "holds " + list(in, 6)
}

func (a *App) editRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	for _, e := range a.M.Edits {
		p := a.C.Path(e.File)
		rs = append(rs, rows.Row{ID: "edit:" + e.Name, Tier: rows.RO, Command: "edit " + e.Name, Desc: e.Desc, Detail: a.Env.Short(p)})
		acts["edit:"+e.Name] = &Action{Show: func(a *App) error { return a.edit(p) }}
	}
	return rs, acts
}

func (a *App) permTools() []string {
	var out []string
	for _, tool := range perms.Tools {
		if a.toolHere(tool) {
			out = append(out, tool)
		}
	}
	return out
}

func (a *App) permRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	for _, tool := range a.permTools() {
		tool := tool
		rs = append(rs, rows.Row{ID: "perms:" + tool, Tier: rows.RO, Command: "permissions " + tool, Desc: "what " + tool + " may do without asking (read-only)"})
		acts["perms:"+tool] = &Action{Show: func(a *App) error { a.showPerms(tool); return nil }}
	}
	return rs, acts
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
