package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/ui"
)

var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// addSkill is the four ways to add a skill.
func (a *App) addSkill() error {
	n, ok := a.T.Choose("Add a skill from where?", []string{
		"npx skills: a package from the skills registry (owner/repo)",
		"a GitHub URL: dott shows its SKILL.md and scripts before anything is kept",
		"a folder on this Mac: dott adds it as a local source",
		"a new skill: dott writes a SKILL.md to start from",
	})
	if !ok {
		return nil
	}
	switch n {
	case 0:
		return a.skillFromNpx()
	case 1:
		return a.skillFromGitHub()
	case 2:
		return a.skillFromFolder()
	}
	return a.newSkill()
}

func (a *App) agentsArgs() ([]string, []string) {
	it := a.skillsItem()
	agents := append([]string{}, it.Agents...)
	if isDir(filepath.Join(a.Env.Home, ".claude")) || a.Env.Has("claude") {
		agents = append([]string{"claude-code"}, agents...)
	}
	var env []string
	for k, v := range it.Env {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return agents, env
}

func (a *App) skillFromNpx() error {
	it := a.skillsItem()
	pkg, ok := a.T.Ask("package (owner/repo, as skills.sh lists it)", "")
	if !ok {
		return nil
	}
	agents, env := a.agentsArgs()
	argv := append(append(append([]string{}, it.Installer...), pkg, "-g", "-a"), agents...)
	p := &Plan{Title: "add skill " + pkg, Lines: []string{
		"run " + strings.Join(env, " ") + " " + engine.Quote(argv...) + " (it asks which skills)",
		dim("it installs outside dott's sources, so dott won't update it; add its folder as a local source to track it"),
	}}
	printPlan(a.T.Out, p)
	if a.T.Confirm() != ui.Yes {
		return nil
	}
	if len(argv) == 0 || !a.Env.Has(argv[0]) {
		return fmt.Errorf("%s isn't installed (brew install node)", it.Installer[0])
	}
	return engine.TTY(a.M.Dir, argv, env...)
}

// localSkillsDir is where this Mac keeps skills of its own: the first local source that is
// a folder of skills, else ~/.local/share/dott/skills.
func (a *App) localSkillsDir() string {
	for _, s := range a.Cfg.Local.Skills {
		p := a.Env.Expand(s)
		if isDir(p) && !exists(filepath.Join(p, "SKILL.md")) {
			return p
		}
	}
	data := a.Env.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(a.Env.Home, ".local", "share")
	}
	return filepath.Join(data, "dott", "skills")
}

// addLocalSource remembers a folder of this Mac's own skills in the config.
func (a *App) addLocalSource(dir string) error {
	short := a.Env.Short(dir)
	for _, s := range a.Cfg.Local.Skills {
		if a.Env.Expand(s) == dir {
			return nil
		}
	}
	a.Cfg.Local.Skills = append(a.Cfg.Local.Skills, short)
	return a.SaveConfig()
}

// whereSkill asks where a new skill should live: the repo (public) or this Mac only.
func (a *App) whereSkill() (string, bool, bool) {
	it := a.skillsItem()
	repoDir := ""
	if len(it.Sources) > 0 {
		repoDir = a.C.Path(it.Sources[0])
	}
	local := a.localSkillsDir()
	var opts []string
	if repoDir != "" {
		opts = append(opts, "the repo: "+a.C.Rel(repoDir)+" (public: every machine gets it)")
	}
	opts = append(opts, "this Mac only: "+a.Env.Short(local)+" (never in the repo)")
	n, ok := a.T.Choose("Where should it live?", opts)
	if !ok {
		return "", false, false
	}
	if repoDir != "" && n == 0 {
		return repoDir, false, true
	}
	return local, true, true
}

func (a *App) skillFromGitHub() error {
	url, ok := a.T.Ask("GitHub URL (https://github.com/owner/repo)", "")
	if !ok {
		return nil
	}
	if !strings.HasPrefix(url, "https://github.com/") {
		return fmt.Errorf("give a https://github.com/owner/repo URL")
	}
	if !a.Env.Has("git") {
		return fmt.Errorf("git isn't here yet (xcode-select --install)")
	}
	tmp, err := os.MkdirTemp("", "dott-skill-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, "src")
	x := &engine.Exec{C: a.C, Out: a.w()}
	if err := x.Stream([]string{"git", "clone", "--quiet", "--depth", "1", url, src}); err != nil {
		return err
	}
	head, _, _ := a.C.Run(nil, []string{"git", "-C", src, "rev-parse", "HEAD"}, 5*time.Second)
	var found []string
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || strings.Count(strings.TrimPrefix(p, src), "/") > 3) {
			return fs.SkipDir
		}
		if d.Name() == "SKILL.md" {
			found = append(found, filepath.Dir(p))
		}
		return nil
	})
	if len(found) == 0 {
		return fmt.Errorf("no SKILL.md in %s", url)
	}
	for _, dir := range found {
		a.say("%s", bold("── "+filepath.Base(dir)+"/SKILL.md"))
		b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		ls := strings.Split(string(b), "\n")
		if len(ls) > 40 {
			ls = append(ls[:40], dim(fmt.Sprintf("… %d more lines", len(ls)-40)))
		}
		a.say("%s", strings.Join(ls, "\n"))
		var scripts []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			fi, _ := d.Info()
			if fi != nil && (fi.Mode()&0o111 != 0 || isScript(p, false)) {
				r, _ := filepath.Rel(dir, p)
				scripts = append(scripts, r)
			}
			return nil
		})
		if len(scripts) > 0 {
			a.say("  %s %s", amber("▲ it has code that agents can run:"), strings.Join(scripts, ", "))
		}
		if why := kinds.Lint(dir); why != "" {
			a.say("  %s %s", red("✗ fails the spec check:"), why)
		}
	}
	if !a.T.YesNo(fmt.Sprintf("Keep %s?", plural(len(found), "skill", "skills")), false) {
		return nil
	}
	target, local, ok := a.whereSkill()
	if !ok {
		return nil
	}
	var names []string
	for _, dir := range found {
		name := filepath.Base(dir)
		if found[0] == src {
			name = filepath.Base(strings.TrimSuffix(url, ".git"))
		}
		dst := filepath.Join(target, name)
		if exists(dst) {
			a.say("%s %s already exists; skipped", amber("~"), a.Env.Short(dst))
			continue
		}
		if err := fsops.CopyTree(dir, dst); err != nil {
			return err
		}
		_ = os.RemoveAll(filepath.Join(dst, ".git"))
		note := fmt.Sprintf("copied from %s @ %s (%s) with dott add skill.\n", url, strings.TrimSpace(head), time.Now().Format("2006-01-02"))
		_ = os.WriteFile(filepath.Join(dst, "UPSTREAM"), []byte(note), 0o644)
		names = append(names, name)
		a.say("%s kept %s", green("✓"), a.Env.Short(dst))
	}
	if local {
		if err := a.addLocalSource(target); err != nil {
			return err
		}
	}
	return a.installNew(names)
}

// installNew offers to install skills that were just added to a source.
func (a *App) installNew(names []string) error {
	if len(names) == 0 {
		return nil
	}
	a.Scan()
	for _, n := range names {
		p, err := a.fixPlan(a.skillsItem().ID, n, false)
		if err != nil {
			a.say("%s %s: %v", amber("~"), n, err)
			continue
		}
		printPlan(a.T.Out, p)
		if a.T.Confirm() == ui.Yes {
			_ = p.Run(a)
		}
	}
	return nil
}

func (a *App) skillFromFolder() error {
	in, ok := a.T.Ask("folder (one skill, or a folder of skills)", "")
	if !ok {
		return nil
	}
	dir, err := filepath.Abs(a.Env.Expand(in))
	if err != nil || !isDir(dir) {
		return fmt.Errorf("no folder %s", in)
	}
	if strings.HasPrefix(dir, a.M.Dir+"/") {
		return fmt.Errorf("%s is in the repo; skills in the repo's sources are found already", a.Env.Short(dir))
	}
	var dirs []string
	if exists(filepath.Join(dir, "SKILL.md")) {
		dirs = []string{dir}
	} else {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if p := filepath.Join(dir, e.Name()); exists(filepath.Join(p, "SKILL.md")) {
				dirs = append(dirs, p)
			}
		}
	}
	if len(dirs) == 0 {
		return fmt.Errorf("no SKILL.md in %s or the folders in it", a.Env.Short(dir))
	}
	var names []string
	for _, d := range dirs {
		if why := kinds.Lint(d); why != "" {
			a.say("  %s %s: %s", red("✗"), filepath.Base(d), why)
		} else {
			a.say("  %s %s", green("✓"), filepath.Base(d))
			names = append(names, filepath.Base(d))
		}
	}
	p := &Plan{Title: "add " + a.Env.Short(dir) + " as a source of this Mac's own skills",
		Lines: []string{"remember it in " + a.Env.Short(a.C.CfgAt) + " (never in the repo)", "then install " + strings.Join(names, ", ")}}
	printPlan(a.T.Out, p)
	if a.T.Confirm() != ui.Yes {
		return nil
	}
	if err := a.addLocalSource(dir); err != nil {
		return err
	}
	return a.installNew(names)
}

func (a *App) newSkill() error {
	name, ok := a.T.Ask("skill name (lowercase-with-hyphens)", "")
	if !ok {
		return nil
	}
	if !skillName.MatchString(name) || len(name) > 64 {
		return fmt.Errorf("a skill name is lowercase letters, digits and single hyphens, 64 at most")
	}
	desc, ok := a.T.Ask("one line: what it does and when an agent should use it", "")
	if !ok {
		return nil
	}
	target, local, ok := a.whereSkill()
	if !ok {
		return nil
	}
	dst := filepath.Join(target, name)
	if exists(dst) {
		return fmt.Errorf("%s already exists", a.Env.Short(dst))
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n\n" +
		"Write what the agent should do here. Ask your agent to use the skill-creator skill to flesh it out and test it.\n"
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte(body), 0o644); err != nil {
		return err
	}
	if why := kinds.Lint(dst); why != "" {
		return fmt.Errorf("the new SKILL.md fails the spec check: %s", why)
	}
	a.say("%s wrote %s", green("✓"), a.Env.Short(filepath.Join(dst, "SKILL.md")))
	if local {
		if err := a.addLocalSource(target); err != nil {
			return err
		}
	}
	return a.installNew([]string{name})
}
