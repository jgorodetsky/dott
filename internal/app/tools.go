package app

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/gitx"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/perms"
	"github.com/jgorodetsky/dott/internal/rows"
)

// summary is the health screen: counts, then what needs you, then the fine ones by section.
func (a *App) summary() {
	counts := map[engine.State]int{}
	var needs []engine.Status
	bySection := map[string][]string{}
	var sections []string
	for _, s := range a.All {
		counts[s.State]++
		if s.State.NeedsYou() {
			needs = append(needs, s)
		} else if s.State == engine.OK {
			if _, ok := bySection[s.Section]; !ok {
				sections = append(sections, s.Section)
			}
			bySection[s.Section] = append(bySection[s.Section], s.ID)
		}
	}
	head := a.Env.Short(a.M.Dir)
	if a.Env.Has("git") {
		if b := (gitx.Repo{Dir: a.M.Dir}).Branch(); b != "" {
			head += " (" + b + ")"
		}
	}
	a.say("%s  %s", bold("dott"), dim(head+" · settings "+a.Env.Short(a.C.CfgAt)))
	a.say("%s ok · %s need you · %s off · %s not on this Mac",
		green(fmt.Sprint(counts[engine.OK])), amber(fmt.Sprint(counts[engine.Drift]+counts[engine.Conflict]+counts[engine.Blocked])),
		fmt.Sprint(counts[engine.Off]), fmt.Sprint(counts[engine.Absent]))
	if len(needs) == 0 {
		a.say("  %s", green("✓ nothing needs you"))
	}
	for _, s := range needs {
		a.say("  %s %-16s %s", amber("~"), s.ID, s.Detail)
	}
	for _, sec := range sections {
		a.say("  %s %s", dim(sec+":"), strings.Join(bySection[sec], " · "))
	}
	for _, l := range a.hints() {
		a.say("  %s %s", dim("·"), l)
	}
	if bs := fsops.Backups(a.C.St.Backups()); len(bs) > 0 {
		a.say("  %s", dim(plural(len(bs), "backup", "backups")+" in "+a.Env.Short(a.C.St.Backups())))
	}
}

// hints are optional clean-ups the old health screen showed: git settings you also set in
// the repo, and brew packages the Brewfile doesn't list. They only read.
func (a *App) hints() []string {
	var out []string
	for _, it := range a.M.Items {
		switch {
		case it.Kind == "include-line" && it.Format == "gitconfig" && a.Env.Has("git"):
			mine := gitKeys(a.C.Path(it.File))
			repo := gitKeys(a.C.Path(it.Line))
			var same, diff []string
			for k, v := range mine {
				if strings.HasPrefix(k, "include.") {
					continue
				}
				if rv, ok := repo[k]; ok {
					if rv == v {
						same = append(same, k[strings.LastIndex(k, ".")+1:])
					} else {
						diff = append(diff, k[strings.LastIndex(k, ".")+1:])
					}
				}
			}
			sort.Strings(same)
			sort.Strings(diff)
			short := a.Env.Short(a.C.Path(it.File))
			if len(same) > 0 {
				out = append(out, fmt.Sprintf("%s: %s also set in the repo (optional to remove): %s", short, plural(len(same), "setting", "settings"), list(same, 4)))
			}
			if len(diff) > 0 {
				out = append(out, fmt.Sprintf("%s: %s set differently; the repo's include comes later, so it wins: %s", short, plural(len(diff), "setting", "settings"), list(diff, 4)))
			}
		case it.Kind == "brew-bundle":
			b := kinds.BrewPath()
			if b == "" {
				continue
			}
			pkgs, _, err := kinds.ParseBrewfile(a.C.Path(it.Brewfile))
			if err != nil {
				continue
			}
			listed := map[string]bool{}
			for _, p := range pkgs {
				listed[p.Short] = true
			}
			leaves, _ := runEnv([]string{"HOMEBREW_NO_AUTO_UPDATE=1"}, b, "leaves", "--installed-on-request")
			casks, _ := runEnv([]string{"HOMEBREW_NO_AUTO_UPDATE=1"}, b, "list", "--cask", "-1")
			var extra []string
			seen := map[string]bool{}
			for _, n := range strings.Fields(string(leaves) + " " + string(casks)) {
				n = n[strings.LastIndex(n, "/")+1:]
				if !listed[n] && !seen[n] {
					seen[n] = true
					extra = append(extra, n)
				}
			}
			sort.Strings(extra)
			if len(extra) > 0 {
				out = append(out, fmt.Sprintf("brew: %s not in the Brewfile: %s", plural(len(extra), "package", "packages"), list(extra, 5)))
			}
		}
	}
	return out
}

// gitKeys reads a git config file's own settings (not what it includes), key -> values.
func gitKeys(p string) map[string]string {
	out, err := exec.Command("git", "config", "--file", p, "--list").Output()
	m := map[string]string{}
	if err != nil {
		return m
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = strings.TrimSpace(m[k] + "\n" + v)
		}
	}
	return m
}

// edit opens a file in your editor.
func (a *App) edit(p string) error {
	ed := a.Env.Getenv("EDITOR")
	if ed == "" || ed == "vim" {
		if a.Env.Has("nvim") {
			ed = "nvim"
		} else {
			ed = "vim"
		}
	}
	argv := append(strings.Fields(ed), p)
	return engine.TTY(a.M.Dir, argv)
}

func (a *App) showPerms(tool string) {
	r := perms.Read(tool, a.Env.Home, a.Env.Short)
	a.say("%s  %s", bold("permissions "+tool), dim("read-only: dott never changes these"))
	if r.Note != "" {
		a.say("  %s", dim(r.Note))
	}
	if r.Err != "" {
		a.say("  %s %s", red("✗"), r.Err)
	}
	risky := 0
	where := ""
	for _, rule := range r.Rules {
		if rule.Where != where {
			where = rule.Where
			a.say("  %s", dim(where))
		}
		mark := map[string]string{"allow": green("allow"), "deny": red("deny "), "ask": amber("ask  ")}[rule.Action]
		if mark == "" {
			mark = fmt.Sprintf("%-5s", rule.Action)
		}
		line := fmt.Sprintf("    %s  %s", mark, rule.What)
		if rule.Risky != "" {
			risky++
			line += "  " + red("▲ risky: "+rule.Risky)
		}
		a.say("%s", line)
	}
	if len(r.Rules) == 0 {
		a.say("  %s", dim("no rules found"))
	}
	a.say("  %s", dim(plural(risky, "risky rule", "risky rules")))
}

func (a *App) restoreRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	for _, dir := range fsops.Backups(a.C.St.Backups()) {
		dir := dir
		entries := fsops.ReadEntries(dir)
		var names []string
		for _, e := range entries {
			names = append(names, a.Env.Short(e.Path))
		}
		id := filepath.Base(dir)
		rs = append(rs, rows.Row{ID: id, Tier: rows.MUT, Command: id, Desc: list(names, 3)})
		acts[id] = &Action{Changes: func(a *App) (*Plan, error) {
			p := &Plan{Title: "restore " + id}
			for _, e := range entries {
				switch {
				case !e.Existed:
					p.Lines = append(p.Lines, "remove "+a.Env.Short(e.Path)+" (it didn't exist before)")
				case e.Link != "":
					p.Lines = append(p.Lines, "put back "+a.Env.Short(e.Path)+" as a link to "+a.Env.Short(e.Link))
				default:
					p.Lines = append(p.Lines, "put back "+a.Env.Short(e.Path)+" as it was")
				}
			}
			p.Run = func(a *App) error {
				if err := fsops.Restore(dir); err != nil {
					return err
				}
				a.say("%s restored %s", green("✓"), id)
				return nil
			}
			return p, nil
		}}
	}
	return rs, acts
}

func (a *App) skillsItem() *manifest.Item {
	for i := range a.M.Items {
		if a.M.Items[i].Kind == "skills" {
			return &a.M.Items[i]
		}
	}
	return nil
}

// itemFiles are the repo paths an item reads, so refresh can say which items a pull touches.
func itemFiles(m *manifest.Manifest, it manifest.Item) []string {
	var out []string
	rel := func(p string) {
		p = strings.TrimPrefix(p, "{{repo}}/")
		if p != "" && !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") && !strings.HasPrefix(p, "@") && !strings.HasPrefix(p, "$") && !strings.HasPrefix(p, "-") {
			out = append(out, filepath.Clean(p))
		}
	}
	for _, p := range []string{it.To, it.From, it.LinesFrom, it.Brewfile, it.Source} {
		rel(p)
	}
	if it.Kind == "include-line" && it.Format == "gitconfig" {
		rel(it.Line)
	}
	for _, s := range it.Sources {
		rel(s)
	}
	for _, argv := range [][]string{it.Check, it.Plan, it.Apply} {
		for _, a := range argv {
			if strings.Contains(a, "/") && exists(filepath.Join(m.Dir, strings.TrimPrefix(a, "{{repo}}/"))) {
				rel(a)
			}
		}
	}
	for _, l := range strings.Split(it.Body, "\n") {
		for _, f := range strings.Fields(l) {
			f = strings.Trim(f, `"'`)
			if r, ok := strings.CutPrefix(f, "{{repo}}/"); ok {
				rel(r)
			}
		}
	}
	seen := map[string]bool{}
	uniq := out[:0]
	for _, p := range out {
		if !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	return uniq
}

func touches(files []string, changed string) bool {
	for _, f := range files {
		if changed == f || strings.HasPrefix(changed, f+"/") {
			return true
		}
	}
	return false
}

// refresh shows what a pull would bring, then pulls and fixes after one yes.
func (a *App) refresh() error {
	if !a.Env.Has("git") {
		return fmt.Errorf("git isn't here yet (it comes with Xcode's command line tools: xcode-select --install)")
	}
	g := gitx.Repo{Dir: a.M.Dir}
	up, err := g.Upstream()
	if err != nil {
		return fmt.Errorf("branch %s has no remote branch to pull from", g.Branch())
	}
	a.say("%s", dim("fetching "+up+"…"))
	if _, err := g.Git(nil, nil, "fetch", "--quiet"); err != nil {
		return err
	}
	log, _ := g.Git(nil, nil, "log", "--oneline", "--no-decorate", "HEAD..@{u}")
	if strings.TrimSpace(log) == "" {
		a.say("%s up to date with %s", green("✓"), up)
		return nil
	}
	a.say("%s", bold("new on "+up+":"))
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		a.say("  %s", l)
	}
	raw, _ := g.Git(nil, nil, "diff", "--raw", "--no-abbrev", "--no-renames", "HEAD...@{u}")
	var changed []string
	execBit := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
		meta, path, ok := strings.Cut(l, "\t")
		if f := strings.Fields(meta); ok && len(f) >= 2 {
			changed = append(changed, path)
			execBit[path] = f[1] == "100755"
		}
	}
	var touched []string
	for _, it := range a.M.Items {
		files := itemFiles(a.M, it)
		for _, f := range changed {
			if touches(files, f) {
				touched = append(touched, it.ID)
				break
			}
		}
	}
	if len(touched) > 0 {
		a.say("%s %s", bold("items it touches:"), strings.Join(touched, ", "))
	}
	for _, f := range changed {
		if !isScript(f, execBit[f]) {
			continue
		}
		diff, _ := g.Git(nil, nil, "diff", "--color=always", "HEAD...@{u}", "--", f)
		ls := strings.Split(strings.TrimRight(diff, "\n"), "\n")
		if len(ls) > 200 {
			ls = append(ls[:200], fmt.Sprintf("… %d more lines: git diff HEAD...@{u} -- %s", len(ls)-200, f))
		}
		a.say("%s %s", bold("script changed:"), f)
		a.say("%s", strings.Join(ls, "\n"))
	}
	if st, _ := g.Git(nil, nil, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		a.say("%s", amber("~ you have local changes; the pull keeps them unless they clash"))
	}
	if !a.T.YesNo("Pull and fix?", true) {
		a.say("nothing pulled")
		return nil
	}
	a.C.Out = a.w()
	x := &engine.Exec{C: a.C, Out: a.w()}
	if err := x.Stream([]string{"git", "-C", a.M.Dir, "pull", "--ff-only"}); err != nil {
		return fmt.Errorf("the pull didn't fast-forward; sort it out in %s, then refresh again", a.Env.Short(a.M.Dir))
	}
	m, err := manifest.Load(a.M.Dir)
	if err != nil {
		return err
	}
	a.M, a.C.M = m, m
	a.Scan()
	p, err := a.fixAllPlan()
	if err != nil {
		a.say("%s pulled; %v", green("✓"), err)
		return nil
	}
	printPlan(a.w(), p)
	return p.Run(a)
}

// isScript says whether a changed file runs code: executable on the remote, or a shell or
// Python file. Those diffs are shown before a pull.
func isScript(f string, executable bool) bool {
	switch filepath.Ext(f) {
	case ".sh", ".zsh", ".bash", ".py":
		return true
	}
	return executable
}
