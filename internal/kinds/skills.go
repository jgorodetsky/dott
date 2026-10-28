package kinds

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Skills installs agent skills with the installer the manifest names (npx skills): one real
// copy in a shared folder every agent reads, and a link for claude. The installer replaces
// without asking, so dott keeps a hash of each copy it installed to tell your edits from
// updates in the source.
type Skills struct{}

// Skill is one skill folder in a source.
type Skill struct {
	Name, Src string
	From      string // "the repo", or the local folder it came from
	Local     bool
}

// SkillSources lists every skill to install: the repo's sources first, then this Mac's own
// (config [local] skills). A later skill with a name already taken is left out.
func SkillSources(c *engine.Ctx, it *manifest.Item) []Skill {
	var out []Skill
	seen := map[string]bool{}
	add := func(dir, from string, local bool) {
		dir = filepath.Clean(dir)
		cands := []string{dir}
		if !exists(filepath.Join(dir, "SKILL.md")) {
			cands = nil
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				if p := filepath.Join(dir, e.Name()); isDir(p) && exists(filepath.Join(p, "SKILL.md")) {
					cands = append(cands, p)
				}
			}
		}
		for _, p := range cands {
			n := filepath.Base(p)
			if !seen[n] {
				seen[n] = true
				out = append(out, Skill{n, p, from, local})
			}
		}
	}
	for _, s := range it.Sources {
		add(c.Path(s), "the repo", false)
	}
	for _, s := range c.Cfg.Local.Skills {
		p := c.Env.Expand(s)
		add(p, c.Env.Short(p), true)
	}
	return out
}

type skillDirs struct {
	home, claude string
	claudeOn     bool
}

func dirsOf(c *engine.Ctx, it *manifest.Item) skillDirs {
	d := skillDirs{home: c.Path(it.Home)}
	if it.ClaudeLinks != "" {
		d.claude = c.Path(it.ClaudeLinks)
		d.claudeOn = isDir(filepath.Dir(d.claude)) || c.Env.Has("claude")
	}
	return d
}

// linked says whether claude's entry for name is a link to the shared copy (or there's no claude).
func (d skillDirs) linked(name string) bool {
	if !d.claudeOn {
		return true
	}
	l := filepath.Join(d.claude, name)
	fi, err := os.Lstat(l)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 || !isDir(filepath.Join(d.home, name)) {
		return false
	}
	a, err1 := filepath.EvalSymlinks(l)
	b, err2 := filepath.EvalSymlinks(filepath.Join(d.home, name))
	return err1 == nil && err2 == nil && a == b
}

// installed is the copy the agents load: a folder of its own in claude's skills wins.
func (d skillDirs) installed(name string) string {
	if d.claudeOn {
		if p := filepath.Join(d.claude, name); exists(p) && !d.linked(name) {
			return p
		}
	}
	if p := filepath.Join(d.home, name); isDir(p) {
		return p
	}
	return ""
}

var lintName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Lint is the quick spec check (agentskills.io) on a skill folder: "" when it passes.
func Lint(dir string) string {
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "no SKILL.md"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	allowed := map[string]bool{"name": true, "description": true, "license": true, "allowed-tools": true, "metadata": true, "compatibility": true}
	key := regexp.MustCompile(`^([A-Za-z_-]+):`)
	closed, desc := false, false
	name := ""
	var extra []string
	for n := 1; sc.Scan(); n++ {
		l := sc.Text()
		if n == 1 {
			if l != "---" {
				return "SKILL.md has to start with ---"
			}
			continue
		}
		if strings.TrimRight(l, " \t") == "---" {
			closed = true
			break
		}
		if v, ok := strings.CutPrefix(l, "name:"); ok {
			name = strings.Map(func(r rune) rune {
				if r == '"' || r == '\'' || r == ' ' || r == '\t' {
					return -1
				}
				return r
			}, v)
		}
		if v, ok := strings.CutPrefix(l, "description:"); ok {
			desc = strings.TrimLeft(v, " \t") != ""
		}
		if m := key.FindStringSubmatch(l); m != nil && !allowed[m[1]] {
			extra = append(extra, m[1])
		}
	}
	base := filepath.Base(dir)
	switch {
	case !closed:
		return "the frontmatter never ends (---)"
	case name == "":
		return "no name"
	case name != base:
		return "name " + name + " is not the folder name"
	case !lintName.MatchString(name) || len(name) > 64:
		return "the name has to be lowercase letters, digits and single hyphens, 64 at most"
	case !desc:
		return "no description"
	case len(extra) > 0:
		return "fields the spec does not allow at the top: " + strings.Join(extra, " ") + " (put them under metadata)"
	}
	return ""
}

// record is the hash of the copy dott (or the old installer) last installed.
func record(c *engine.Ctx, it *manifest.Item, name string) string {
	if h := c.St.Records()["skill:"+name]; h != "" {
		return h
	}
	if it.LegacyRecord == "" {
		return ""
	}
	legacy := c.Shared("skills:legacy", func() any {
		m := map[string]string{}
		ls, _, _ := readLines(c.Env.Expand(it.LegacyRecord))
		for _, l := range ls {
			if n, h, ok := strings.Cut(l, "\t"); ok {
				m[n] = h
			}
		}
		return m
	}).(map[string]string)
	return legacy[name]
}

// wasRepoSkill says whether an installed copy is exactly a version the repo once committed,
// which makes the repo simply newer. It compares git blob ids with the skill at each commit.
func wasRepoSkill(c *engine.Ctx, s Skill, inst string) bool {
	rel, err := filepath.Rel(c.M.Dir, s.Src)
	if err != nil || strings.HasPrefix(rel, "..") || !c.Env.Has("git") {
		return false
	}
	files, err := treeFiles(inst)
	if err != nil {
		return false
	}
	var mine []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(inst, f))
		if err != nil {
			return false
		}
		mine = append(mine, blobID(b)+" "+f)
	}
	sort.Strings(mine)
	git := []string{"git", "--no-optional-locks", "-C", c.M.Dir}
	out, code, err := c.Run(nil, append(git, "log", "-n", "50", "--format=%H", "--", rel+"/"), 10*time.Second)
	if err != nil || code != 0 {
		return false
	}
	for _, commit := range strings.Fields(out) {
		tree, code, err := c.Run(nil, append(git, "ls-tree", "-r", commit, "--", rel+"/"), 5*time.Second)
		if err != nil || code != 0 {
			continue
		}
		var theirs []string
		for _, l := range strings.Split(strings.TrimSpace(tree), "\n") {
			meta, path, ok := strings.Cut(l, "\t")
			if f := strings.Fields(meta); ok && len(f) == 3 {
				theirs = append(theirs, f[2]+" "+strings.TrimPrefix(path, rel+"/"))
			}
		}
		sort.Strings(theirs)
		if strings.Join(theirs, "\n") == strings.Join(mine, "\n") {
			return true
		}
	}
	return false
}

// skillState is the old installer's skill states: same, new, update, link, mine, both, bad.
func skillState(c *engine.Ctx, it *manifest.Item, d skillDirs, s Skill) (engine.State, string, string) {
	if why := Lint(s.Src); why != "" {
		return engine.Blocked, "fails the spec check: " + why, "fix its SKILL.md in " + c.Env.Short(s.Src)
	}
	inst := d.installed(s.Name)
	if inst == "" {
		return engine.Drift, "not installed", "install it"
	}
	h, r, l := treeHash(inst), treeHash(s.Src), record(c, it, s.Name)
	switch {
	case h == r && d.linked(s.Name):
		return engine.OK, "same as " + s.From, ""
	case h == r:
		return engine.Drift, "claude reads a copy of its own; switch it to the shared one", "install it"
	case h == l:
		return engine.Drift, s.From + " has a newer version", "install it"
	case r == l:
		return engine.Conflict, "you changed the installed copy; " + s.From + " didn't", "keep yours, or replace it"
	case l == "" && !s.Local && wasRepoSkill(c, s, inst):
		return engine.Drift, s.From + " has a newer version", "install it"
	case l != "":
		return engine.Conflict, "you and " + s.From + " both changed it", "keep yours, or replace it"
	}
	return engine.Conflict, "differs from " + s.From + ", and there is no record of which is newer", "keep yours, or replace it"
}

func (Skills) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	d := dirsOf(c, it)
	all := SkillSources(c, it)
	if len(all) == 0 {
		return status(engine.OK, "no skills in the sources", "")
	}
	var parts []engine.Status
	for _, s := range all {
		st, detail, fix := skillState(c, it, d, s)
		p := part(it.ID, s.Name, st, detail, fix)
		p.Desc = "from " + s.From
		if s.Local {
			p.Desc = "local, from " + s.From
		}
		parts = append(parts, p)
	}
	s := engine.Status{Parts: parts}
	s.State, s.Detail, s.Fix = engine.Roll(parts)
	if s.State == engine.OK {
		s.Detail = fmt.Sprintf("%d skills in %s", len(all), c.Env.Short(d.home))
		if d.claudeOn {
			s.Detail += ", linked for claude"
		}
	}
	return s
}

func (Skills) Plan(c *engine.Ctx, it *manifest.Item, st engine.Status, name string, replace bool) ([]engine.Change, error) {
	d := dirsOf(c, it)
	groups := map[string][]Skill{}
	var order []string
	danger := map[string]bool{}
	for _, s := range SkillSources(c, it) {
		if name != "" && s.Name != name {
			continue
		}
		state, _, _ := skillState(c, it, d, s)
		ok := state == engine.Drift || (state == engine.Conflict && replace && name != "")
		if !ok {
			if name != "" && state == engine.Conflict {
				return nil, fmt.Errorf("%s is yours; pick replace to swap it for the source's (yours goes to the backup)", s.Name)
			}
			continue
		}
		dir := filepath.Dir(s.Src)
		if _, seen := groups[dir]; !seen {
			order = append(order, dir)
		}
		groups[dir] = append(groups[dir], s)
		if state == engine.Conflict {
			danger[dir] = true
		}
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("nothing to install")
	}
	agents := it.Agents
	if d.claudeOn {
		agents = append([]string{"claude-code"}, agents...)
	}
	var env []string
	for k, v := range it.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	var out []engine.Change
	for _, dir := range order {
		ss := groups[dir]
		var names, paths []string
		for _, s := range ss {
			names = append(names, s.Name)
			paths = append(paths, filepath.Join(d.home, s.Name))
			if d.claudeOn {
				paths = append(paths, filepath.Join(d.claude, s.Name))
			}
		}
		argv := append(append(append(append(append([]string{}, it.Installer...), dir, "-g", "-a"), agents...), "-s"), names...)
		argv = append(argv, "-y")
		from := ss[0].From
		summary := "install " + strings.Join(names, ", ") + " from " + from + " (one copy in " + c.Env.Short(d.home)
		if d.claudeOn {
			summary += ", a link for claude"
		}
		out = append(out, engine.Change{
			Summary: summary + ")",
			Shell:   strings.Join(env, " ") + " " + engine.Quote(argv...),
			Paths:   paths,
			Danger:  danger[dir],
			Do: func(x *engine.Exec) error {
				if len(argv) == 0 || !c.Env.Has(argv[0]) {
					return fmt.Errorf("%s isn't installed (it comes with node: brew install node)", it.Installer[0])
				}
				for _, s := range ss {
					if why := validate(c, it, s.Src); why != "" {
						return fmt.Errorf("%s fails the spec check: %s", s.Name, why)
					}
				}
				if err := x.Stream(argv, env...); err != nil {
					return err
				}
				for _, s := range ss {
					h := treeHash(s.Src)
					if treeHash(filepath.Join(d.home, s.Name)) != h || !d.linked(s.Name) {
						return fmt.Errorf("%s: the installed copy doesn't match %s after installing", s.Name, c.Env.Short(s.Src))
					}
					if err := c.St.SetRecord("skill:"+s.Name, h); err != nil {
						return err
					}
				}
				return nil
			},
		})
	}
	return out, nil
}

// validate runs the quick check, then the full one (the manifest's validator) when its
// command is here. A validator that can't run (offline) doesn't block the install.
func validate(c *engine.Ctx, it *manifest.Item, dir string) string {
	if why := Lint(dir); why != "" {
		return why
	}
	if len(it.Validator) == 0 || !c.Env.Has(it.Validator[0]) {
		return ""
	}
	out, code, err := c.Run(nil, append(append([]string{}, it.Validator...), dir), 2*time.Minute)
	if err != nil || code == 0 || !strings.Contains(out, "Validation failed") {
		return ""
	}
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "- "); ok {
			return v
		}
	}
	return "the full spec check failed"
}

// SkillCompare is what "compare" shows for a skill you changed: yours, then the source.
func SkillCompare(c *engine.Ctx, it *manifest.Item, name string) (string, string, bool) {
	d := dirsOf(c, it)
	for _, s := range SkillSources(c, it) {
		if s.Name == name {
			return d.installed(name), s.Src, true
		}
	}
	return "", "", false
}
