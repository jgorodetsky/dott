package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/rows"
)

// itemView is one item: what needs you in it, where it lives, its parts, its manifest
// entry, and turning it off or on.
func (a *App) itemView(id string) View {
	return View{Prompt: id + " › ", Stay: true,
		Header: func(a *App) string { return a.itemHeader(id, "") },
		Rows:   func(a *App) ([]rows.Row, map[string]*Action) { return a.itemRows(id) }}
}

// partView is one part of an item: a package, a skill, a rules target.
func (a *App) partView(id, name string) View {
	return View{Prompt: id + "/" + name + " › ", Stay: true,
		Header: func(a *App) string { return a.itemHeader(id, name) },
		Rows:   func(a *App) ([]rows.Row, map[string]*Action) { return a.partRows(id, name) }}
}

var stateMark = map[engine.State]func(string) string{
	engine.OK: green, engine.Drift: amber, engine.Conflict: red, engine.Blocked: red, engine.Off: dim, engine.Absent: dim,
}

// itemHeader names the item or part, its state and what it is.
func (a *App) itemHeader(id, part string) string {
	s, ok := a.status(id)
	if !ok {
		return bold(id) + " · " + dim("not in the manifest any more")
	}
	desc := s.Desc
	if part != "" {
		p, ok := s.Part(part)
		if !ok {
			return bold(id+"/"+part) + " · " + dim("not there any more")
		}
		if p.Desc != "" {
			desc = p.Desc
		}
		s = p
	}
	paint := stateMark[s.State]
	if paint == nil {
		paint = dim
	}
	line := bold(s.ID) + " · " + paint(string(s.State))
	if s.Detail != "" {
		line += " · " + s.Detail
	}
	var more []string
	if desc != "" {
		more = append(more, desc)
	}
	if it := a.item(id); it != nil {
		more = append(more, it.Kind)
		if it.Group != "" {
			more = append(more, "optional group "+it.Group)
		}
	}
	if s.Fix != "" && s.State != engine.OK {
		more = append(more, "next: "+s.Fix)
	}
	return line + "\n" + dim(strings.Join(more, " · "))
}

func (a *App) itemRows(id string) ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	add := func(dst *[]rows.Row, r rows.Row, act *Action) {
		*dst = append(*dst, r)
		acts[r.ID] = act
	}
	s, ok := a.status(id)
	it := a.item(id)
	if !ok || it == nil {
		return nil, acts
	}
	a.needRows(s, &rs, add)
	for _, p := range a.places(it) {
		a.placeRow(&rs, p, "", add)
	}
	if it.Kind == "brew-bundle" {
		if extra := a.notInBrewfile(); len(extra) > 0 {
			add(&rs, rows.Row{ID: "unlisted", Tier: rows.PICK, Command: plural(len(extra), "package", "packages") + " not in a Brewfile",
				Desc: "you installed them yourself: " + list(extra, 5), Detail: "pick one to list it in the Brewfile, or this Mac's own"},
				&Action{Open: func(a *App) View { return View{Rows: (*App).notInBrewfileRows, Forward: true} }})
		}
	}
	for i, h := range a.hintsFor(it) {
		h := h
		add(&rs, rows.Row{ID: fmt.Sprint("note:", i), Tier: rows.RO, Command: "note", Desc: h, Detail: h},
			&Action{Show: func(a *App) error { a.say("%s %s", dim("·"), h); return nil }})
	}
	for _, p := range s.Parts {
		name := p.ID[len(id)+1:]
		desc := p.Desc
		if desc == "" || desc == name {
			desc = p.Detail
		}
		add(&rs, rows.Row{ID: "part:" + p.ID, Tier: rows.ITEM, Command: name, Desc: desc, State: string(p.State), Detail: p.Detail},
			&Action{Open: func(a *App) View { return a.partView(id, name) }})
	}
	if file, line := a.manifestLine(id); line > 0 {
		add(&rs, rows.Row{ID: "entry", Tier: rows.RO, Command: "manifest entry", Desc: fmt.Sprintf("%s:%d", filepath.Base(file), line), Detail: "its [[item]] in " + a.Env.Short(file)},
			&Action{Show: func(a *App) error { return a.showEntry(id) }})
	}
	if a.added(it) {
		add(&rs, rows.Row{ID: "stop:" + id, Tier: rows.MUT, Command: "stop tracking " + id, Desc: "a real copy takes the link's place; the item goes"},
			&Action{Changes: func(a *App) (*Plan, error) { return a.stopPlan(id) }})
	}
	a.onOffRow(&rs, s, it, add)
	return rs, acts
}

// onOffRow turns an item, its group or a part off or on.
func (a *App) onOffRow(dst *[]rows.Row, s engine.Status, it *manifest.Item, add adder) {
	id := s.ID
	switch {
	case a.Cfg.IsOff(id):
		add(dst, rows.Row{ID: "on:" + id, Tier: rows.MUT, Command: "turn on " + id, Desc: "check it on this Mac again", State: "off"},
			&Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(id, false), nil }})
	case it != nil && it.Group != "" && !a.Cfg.Groups[it.Group]:
		g := it.Group
		add(dst, rows.Row{ID: "on-group:" + g, Tier: rows.MUT, Command: "turn on " + g, Desc: a.M.Groups[g], State: "off", Detail: a.groupDetail(g)},
			&Action{Changes: func(a *App) (*Plan, error) { return a.groupPlan(g, true), nil }})
	case s.State != engine.Off && s.State != engine.Absent:
		add(dst, rows.Row{ID: "off:" + id, Tier: rows.MUT, Command: "turn off " + id, Desc: "stop checking it on this Mac"},
			&Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(id, true), nil }})
	}
}

func (a *App) partRows(id, name string) ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	add := func(dst *[]rows.Row, r rows.Row, act *Action) {
		*dst = append(*dst, r)
		acts[r.ID] = act
	}
	s, ok := a.status(id)
	it := a.item(id)
	if !ok || it == nil {
		return nil, acts
	}
	p, ok := s.Part(name)
	if !ok {
		return nil, acts
	}
	if r, act, ok := a.partNeed(s, p); ok {
		add(&rs, r, act)
	}
	for _, pl := range a.partPlaces(it, name) {
		a.placeRow(&rs, pl, "", add)
	}
	if it.Kind == "brew-bundle" {
		if b := kinds.BrewPath(); b != "" {
			add(&rs, rows.Row{ID: "brew-info", Tier: rows.RO, Command: "brew info " + name, Desc: "what brew knows about it (read-only)"},
				&Action{Show: func(a *App) error {
					x := &engine.Exec{C: a.C, Out: a.w()}
					return x.Stream([]string{b, "info", name})
				}})
		}
	}
	if p.State != engine.Absent {
		pid := p.ID
		if a.Cfg.IsOff(pid) {
			add(&rs, rows.Row{ID: "on:" + pid, Tier: rows.MUT, Command: "turn on " + pid, Desc: "check it on this Mac again", State: "off"},
				&Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(pid, false), nil }})
		} else if p.State != engine.Off {
			add(&rs, rows.Row{ID: "off:" + pid, Tier: rows.MUT, Command: "turn off " + pid, Desc: "stop checking it on this Mac"},
				&Action{Changes: func(a *App) (*Plan, error) { return a.offPlan(pid, true), nil }})
		}
	}
	return rs, acts
}

// place is a file or folder an item touches.
type place struct {
	Label string
	Path  string
	Repo  bool // in the repo; else on this Mac
}

// places are the files and folders an item touches: on this Mac first, then the repo's.
func (a *App) places(it *manifest.Item) []place {
	c := a.C
	var out []place
	seen := map[string]bool{}
	put := func(label, p string, repo bool) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, place{label, p, repo})
	}
	switch it.Kind {
	case "link":
		put("the link on this Mac", c.Path(it.Link), false)
		put("what it links to, in the repo", c.Path(it.To), true)
	case "block":
		put("the file with the block", c.Path(it.File), false)
	case "include-line":
		put("the file with the line", c.Path(it.File), false)
	case "copy":
		put("the copy on this Mac", c.Path(it.To), false)
		put("the repo's, copied from", c.Path(it.From), true)
	case "brew-bundle":
		put("the Brewfile", c.Path(it.Brewfile), true)
	case "skills":
		put("the shared copy every agent reads", c.Path(it.Home), false)
		put("claude's links", c.Path(it.ClaudeLinks), false)
		for _, s := range it.Sources {
			put("skills in the repo", c.Path(s), true)
		}
		for _, s := range a.Cfg.Local.Skills {
			put("skills only on this Mac", a.Env.Expand(s), false)
		}
	case "rules":
		put("the rules file, in the repo", c.Path(it.Source), true)
		for _, t := range it.Targets {
			if c.When(t.When) {
				put(t.Tool+" reads it here", c.Path(t.File), false)
			}
		}
	}
	for _, f := range itemFiles(a.M, *it) {
		put("in the repo", filepath.Join(a.M.Dir, f), true)
	}
	return out
}

// partPlaces are the files of one part: a skill's folder and SKILL.md, a rules target.
func (a *App) partPlaces(it *manifest.Item, name string) []place {
	c := a.C
	switch it.Kind {
	case "skills":
		home := filepath.Join(c.Path(it.Home), name)
		out := []place{{"the shared copy", home, false}, {"what the skill says", filepath.Join(home, "SKILL.md"), false}}
		for _, s := range it.Sources {
			if src := filepath.Join(c.Path(s), name); exists(src) {
				out = append(out, place{"its source, in the repo", src, true})
			}
		}
		for _, s := range a.Cfg.Local.Skills {
			if src := filepath.Join(a.Env.Expand(s), name); exists(src) {
				out = append(out, place{"its source, only on this Mac", src, false})
			}
		}
		if it.ClaudeLinks != "" {
			out = append(out, place{"claude's link", filepath.Join(c.Path(it.ClaudeLinks), name), false})
		}
		return out
	case "rules":
		for _, t := range it.Targets {
			if t.Tool == name {
				return []place{{name + " reads it here", c.Path(t.File), false}}
			}
		}
	case "copy":
		return []place{{"the copy on this Mac", filepath.Join(c.Path(it.To), name), false}, {"the repo's", filepath.Join(c.Path(it.From), name), true}}
	}
	return nil
}

// placeRow is a row for a file (opens in your editor) or a folder (opens its list). In a
// folder's list, name is the entry's name, shown instead of the path.
func (a *App) placeRow(dst *[]rows.Row, p place, name string, add adder) {
	where := "this Mac"
	if p.Repo {
		where = "repo"
	}
	r := rows.Row{ID: "open:" + p.Path, Tier: rows.RO, Command: a.Env.Short(p.Path), Desc: p.Label, Section: where, Detail: a.Env.Short(p.Path)}
	if t, err := os.Readlink(p.Path); err == nil {
		r.Detail += " → " + a.Env.Short(t)
		if name != "" {
			r.Desc = "→ " + a.Env.Short(t)
		}
	}
	fi, err := os.Stat(p.Path)
	if name != "" {
		r.Command, r.Section = name, ""
		if err == nil && fi.IsDir() {
			r.Command += "/"
		}
	}
	switch {
	case err != nil:
		r.State = "missing"
		add(dst, r, &Action{Show: func(a *App) error { a.say("%s isn't there yet", a.Env.Short(p.Path)); return nil }})
	case fi.IsDir():
		r.Tier = rows.PICK
		add(dst, r, &Action{Open: func(a *App) View { return a.folderView(p.Path) }})
	default:
		add(dst, r, &Action{Show: func(a *App) error { return a.edit(p.Path) }})
	}
}

// folderView lists a folder: a folder opens its own list, a file opens in your editor.
func (a *App) folderView(dir string) View {
	return View{Prompt: a.Env.Short(dir) + " › ", Stay: true, Rows: func(a *App) ([]rows.Row, map[string]*Action) {
		acts := map[string]*Action{}
		var rs []rows.Row
		add := func(dst *[]rows.Row, r rows.Row, act *Action) {
			*dst = append(*dst, r)
			acts[r.ID] = act
		}
		es, err := os.ReadDir(dir)
		if err != nil {
			a.say("%s %v", red("✗"), err)
			return nil, acts
		}
		sort.Slice(es, func(i, j int) bool { return es[i].Name() < es[j].Name() })
		for i, e := range es {
			if i == 300 {
				a.say("%s", dim(fmt.Sprintf("showing 300 of %d entries", len(es))))
				break
			}
			a.placeRow(&rs, place{Path: filepath.Join(dir, e.Name())}, e.Name(), add)
		}
		return rs, acts
	}}
}

// manifestLine finds the line of an item's id in dott.toml.
func (a *App) manifestLine(id string) (string, int) {
	file := filepath.Join(a.M.Dir, manifest.FileName)
	if it := a.item(id); it != nil {
		file = a.M.File(*it)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return file, 0
	}
	re := regexp.MustCompile(`^\s*id\s*=\s*"` + regexp.QuoteMeta(id) + `"\s*$`)
	ls := strings.Split(string(b), "\n")
	for i, l := range ls {
		if re.MatchString(l) {
			// the entry starts at its [[item]] line, a few lines above the id
			for i > 0 && !strings.HasPrefix(strings.TrimSpace(ls[i]), "[[item]]") {
				i--
			}
			return file, i + 1
		}
	}
	return file, 0
}

// showEntry prints an item's [[item]] block from dott.toml, with its line number.
func (a *App) showEntry(id string) error {
	file, line := a.manifestLine(id)
	if line == 0 {
		return fmt.Errorf("no id = %q in %s", id, a.Env.Short(file))
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	ls := strings.Split(string(b), "\n")
	start, end := line-1, line
	for end < len(ls) && !strings.HasPrefix(strings.TrimSpace(ls[end]), "[") {
		end++
	}
	for end > line && strings.TrimSpace(ls[end-1]) == "" {
		end--
	}
	a.say("%s", bold(fmt.Sprintf("%s:%d", a.Env.Short(file), start+1)))
	for _, l := range ls[start:end] {
		a.say("  %s", l)
	}
	return nil
}
