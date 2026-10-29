package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/gitx"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/rows"
	"github.com/jgorodetsky/dott/internal/ui"
)

// addedMark is the line above an [[item]] that dott wrote; such an item can be stopped.
const addedMark = "# added with dott"

// addRows are the things you can add, each on every machine (the repo) or this Mac only.
func (a *App) addRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	add := func(r rows.Row, act *Action) {
		rs = append(rs, r)
		acts[r.ID] = act
	}
	add(rows.Row{ID: "add-file", Tier: rows.PICK, Command: "add file <path>", Desc: "track a config file or folder: it moves to the repo (or this Mac's files) and a link takes its place"},
		&Action{Guide: func(a *App) error { return a.addFile() }})
	if brewItem(a.M) != "" {
		add(rows.Row{ID: "add-brew", Tier: rows.PICK, Command: "add brew <package>", Desc: "a package for brew to install, on every machine or this Mac only"},
			&Action{Guide: func(a *App) error { return a.addBrew("") }})
	}
	if a.M.Aliases != nil {
		add(rows.Row{ID: "alias-add", Tier: rows.PICK, Command: "add alias <name> <command>", Desc: "on every machine (the repo) or this Mac only"},
			&Action{Guide: func(a *App) error { return a.addAlias() }})
	}
	if a.skillsItem() != nil {
		add(rows.Row{ID: "add-skill", Tier: rows.PICK, Command: "add skill", Desc: "from npx skills, a GitHub URL, a folder here, or a new one"},
			&Action{Guide: func(a *App) error { return a.addSkill() }})
	}
	return rs, acts
}

func under(p, dir string) bool {
	return dir != "" && (p == dir || strings.HasPrefix(p, dir+"/"))
}

// suggestRel is where a file of yours goes in the repo: ~/.config/foo/x → foo/x, ~/.foorc →
// foo/.foorc, anything else keeps its path under HOME.
func suggestRel(home, p string) string {
	rel, _ := filepath.Rel(home, p)
	if r, ok := strings.CutPrefix(rel, ".config/"); ok {
		return r
	}
	if !strings.Contains(rel, "/") && strings.HasPrefix(rel, ".") {
		name := strings.TrimSuffix(strings.TrimPrefix(rel, "."), "rc")
		if name == "" {
			name = "home"
		}
		return name + "/" + rel
	}
	return rel
}

var notID = regexp.MustCompile(`[^a-z0-9]+`)

// suggestID makes an item id from a repo path that no item uses yet: foo/config.toml → foo.
func suggestID(m *manifest.Manifest, rel string) string {
	first, _, _ := strings.Cut(rel, "/")
	base := strings.Trim(notID.ReplaceAllString(strings.ToLower(strings.TrimPrefix(first, ".")), "-"), "-")
	if base == "" {
		base = "file"
	}
	id := base
	for n := 2; ; n++ {
		if _, taken := m.Item(id); !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

var idOK = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func tomlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}

// itemBlock is the [[item]] text dott appends for an added file.
func itemBlock(id, link, to, desc string) []byte {
	return []byte(fmt.Sprintf("\n%s\n[[item]]\nid = %s\nkind = \"link\"\nsection = \"files\"\ndesc = %s\nlink = %s\nto = %s\n",
		addedMark, tomlQuote(id), tomlQuote(desc), tomlQuote(link), tomlQuote(to)))
}

func appendBlock(cur, block []byte) []byte {
	out := append([]byte{}, cur...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, block...)
}

// removeBlock takes out the [[item]] dott added for id, with its mark. It reports false when
// the item isn't one dott added.
func removeBlock(text []byte, id string) ([]byte, bool) {
	ls := splitLines(text)
	idLine := regexp.MustCompile(`^\s*id\s*=\s*"` + regexp.QuoteMeta(id) + `"\s*$`)
	at := -1
	for i, l := range ls {
		if idLine.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		return text, false
	}
	start := at
	for start > 0 && strings.TrimSpace(ls[start]) != "[[item]]" {
		start--
	}
	if start == 0 || strings.TrimSpace(ls[start-1]) != addedMark {
		return text, false
	}
	start--
	if start > 0 && strings.TrimSpace(ls[start-1]) == "" {
		start--
	}
	end := at + 1
	for end < len(ls) && !strings.HasPrefix(strings.TrimSpace(ls[end]), "[") && strings.TrimSpace(ls[end]) != addedMark {
		end++
	}
	for end > at+1 && strings.TrimSpace(ls[end-1]) == "" {
		end--
	}
	out := append(append([]string{}, ls[:start]...), ls[end:]...)
	return joinLines(out), true
}

// added says whether dott added an item (it can stop tracking it).
func (a *App) added(it *manifest.Item) bool {
	if it == nil || it.Kind != "link" {
		return false
	}
	b, err := os.ReadFile(a.M.File(*it))
	if err != nil {
		return false
	}
	_, ok := removeBlock(b, it.ID)
	return ok
}

// owned refuses a path that an item already looks after: tracking it would move that item's
// work into the repo, or replace a repo file with a link to itself.
func (a *App) owned(src string) error {
	short := a.Env.Short(src)
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	var holds []string
	for i := range a.M.Items {
		it := &a.M.Items[i]
		for _, pl := range a.places(it) {
			if pl.Repo {
				continue
			}
			switch {
			case pl.Path == src:
				return fmt.Errorf("%s is where item %s puts its part; pick %s to see it", short, it.ID, it.ID)
			case under(src, pl.Path) && it.Kind == "link":
				return fmt.Errorf("%s is in the repo already, through the link %s (item %s)", short, a.Env.Short(pl.Path), it.ID)
			case under(src, pl.Path):
				return fmt.Errorf("%s is inside %s, which item %s looks after", short, a.Env.Short(pl.Path), it.ID)
			case under(pl.Path, src) && !slices.Contains(holds, it.ID):
				holds = append(holds, it.ID)
			}
		}
	}
	if len(holds) > 0 {
		sort.Strings(holds)
		return fmt.Errorf("%s holds what items %s set up; track a file in it instead", short, list(holds, 6))
	}
	// a link of yours, not dott's, can lead into the repo too
	if r := real(src); under(r, real(a.M.Dir)) || under(r, real(a.M.LocalDir)) {
		return fmt.Errorf("%s is in the repo or this Mac's files already, through a link in its path", short)
	}
	return nil
}

func (a *App) origin() string {
	if !a.Env.Has("git") {
		return ""
	}
	u, _ := (gitx.Repo{Dir: a.M.Dir}).Git(nil, nil, "remote", "get-url", "origin")
	return strings.TrimSpace(u)
}

// addFile tracks a file or folder of yours: it moves into the repo (every machine) or this
// Mac's own files, and a link takes its place. A file that holds secrets can only stay on
// this Mac, and one with lines that look like secrets needs a typed yes for the repo.
func (a *App) addFile() error {
	in, ok := a.T.Ask("file or folder to track (like ~/.config/foo/config.toml)", "")
	if !ok {
		return nil
	}
	home := a.Env.Home
	src := filepath.Clean(a.Env.Expand(in))
	if !filepath.IsAbs(src) {
		src = filepath.Join(home, src)
	}
	short := a.Env.Short(src)
	fi, err := os.Lstat(src)
	switch {
	case err != nil:
		return fmt.Errorf("%s isn't there", short)
	case fi.Mode()&os.ModeSymlink != 0:
		t, _ := os.Readlink(src)
		return fmt.Errorf("%s is a link (to %s); dott tracks real files and folders", short, a.Env.Short(t))
	case !under(src, home) || src == home:
		return fmt.Errorf("dott tracks files in your home folder; %s isn't in it", short)
	case under(src, a.M.Dir) || under(src, a.M.LocalDir):
		return fmt.Errorf("%s is already in the repo or in this Mac's files", short)
	}
	if err := a.owned(src); err != nil {
		return err
	}
	rel := suggestRel(home, src)
	local := false
	if why := secretPath(home, src); why != "" {
		a.say("%s %s, so dott tracks it only for this Mac, never in the repo", amber("~"), why)
		if !a.T.YesNo("Track it for this Mac only?", true) {
			return nil
		}
		local = true
	} else {
		n, ok := a.T.Choose("Where should it live?", []string{
			"the repo, at " + rel + " (every machine gets it; the repo may be public)",
			"this Mac only, in " + a.Env.Short(a.M.LocalDir) + " (never in the repo)",
		})
		if !ok {
			return nil
		}
		local = n == 1
	}
	base := a.M.Dir
	if local {
		base = a.M.LocalDir
	}
	if rel, ok = a.T.Ask("its place in "+a.Env.Short(base), rel); !ok {
		return nil
	}
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) || rel == "." || strings.HasPrefix(rel, "..") || rel == manifest.FileName || strings.HasPrefix(rel, ".git/") {
		return fmt.Errorf("%s isn't a place inside %s", rel, a.Env.Short(base))
	}
	dst := filepath.Join(base, rel)
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s is already there", a.Env.Short(dst))
	}
	id, ok := a.T.Ask("its id in dott", suggestID(a.M, rel))
	if !ok {
		return nil
	}
	if _, taken := a.M.Item(id); taken || !idOK.MatchString(id) {
		return fmt.Errorf("%q is taken, or isn't lowercase letters, digits and single hyphens", id)
	}
	mf, to := filepath.Join(a.M.Dir, manifest.FileName), rel
	if local {
		mf, to = a.M.LocalPath, "{{local}}/"+rel
	}
	link := "~/" + strings.TrimPrefix(src, home+"/")
	block := itemBlock(id, link, to, link)
	p := &Plan{Title: "track " + short + " as " + id, Lines: []string{
		"back up " + short,
		"move it to " + a.Env.Short(dst),
		"link " + short + " → " + a.Env.Short(dst),
		"add its [[item]] (a link) to " + a.Env.Short(mf),
		dim("then check it; if a step fails, everything is put back"),
	}}
	if !local {
		if o := a.origin(); o != "" {
			p.Lines = append(p.Lines, dim("the repo pushes to "+o))
		}
	}
	printPlan(a.T.Out, p)
	typed := false
	if finds := scanSecrets(src); !local && len(finds) > 0 {
		a.say("%s %s that look like secrets; anything in the repo can end up public:", red("▲"), plural(len(finds), "line", "lines"))
		for i, f := range finds {
			if i == 10 {
				a.say("    … and %d more", len(finds)-10)
				break
			}
			a.say("    %s %s", a.Env.Short(f.File), f)
		}
		if typed = a.T.TypeYes(); !typed {
			a.say("cancelled; track it for this Mac only instead, or take those lines out first")
			return nil
		}
	}
	if !typed && a.T.Confirm() != ui.Yes {
		a.say("skipped %s", p.Title)
		return nil
	}
	if err := a.track(id, src, dst, mf, block); err != nil {
		return err
	}
	if !local {
		edits := []gitx.Edit{{Path: manifest.FileName, Fn: func(b []byte) ([]byte, error) { return appendBlock(b, block), nil }}}
		_ = filepath.WalkDir(dst, func(f string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				data, rerr := os.ReadFile(f)
				if rerr != nil {
					return rerr
				}
				r, _ := filepath.Rel(a.M.Dir, f)
				edits = append(edits, gitx.Edit{Path: r, Fn: func([]byte) ([]byte, error) { return data, nil }})
			}
			return nil
		})
		a.offerCommit("dott: track "+rel, "Commit it, so other machines get it on their next pull?", edits)
	}
	return nil
}

// track moves src to dst, links src to it and adds the item, with one backup for all of it.
// When a step fails, or the item isn't ok after, everything is put back.
func (a *App) track(id, src, dst, mf string, block []byte) error {
	b := fsops.NewBackup(a.C.St.Backups(), "track-"+id)
	for _, p := range []string{src, dst, mf} {
		if err := b.Save(p); err != nil {
			return err
		}
	}
	if err := b.Close(); err != nil {
		return err
	}
	undo := func(err error) error {
		if rerr := fsops.Restore(b.Dir); rerr != nil {
			return fmt.Errorf("%v; putting it back failed too: %v (the backup is in %s)", err, rerr, a.Env.Short(b.Dir))
		}
		_ = a.reload()
		return fmt.Errorf("%v; everything is put back as it was", err)
	}
	if err := fsops.CopyTree(src, dst); err != nil {
		return undo(err)
	}
	cur, err := os.ReadFile(mf)
	if err != nil && !os.IsNotExist(err) {
		return undo(err)
	}
	if err := fsops.WriteFile(mf, appendBlock(cur, block), 0o644); err != nil {
		return undo(err)
	}
	if err := a.reload(); err != nil {
		return undo(err)
	}
	if err := os.RemoveAll(src); err != nil {
		return undo(err)
	}
	if err := fsops.Symlink(dst, src); err != nil {
		return undo(err)
	}
	a.Scan()
	s, _ := a.status(id)
	if s.State != engine.OK {
		return undo(fmt.Errorf("%s is %s after: %s", id, s.State, s.Detail))
	}
	a.say("%s %s: %s", green("✓"), id, s.Detail)
	a.say("  %s", dim("backup: "+a.Env.Short(b.Dir)+" (undo: dott restore)"))
	return nil
}

// stopPlan stops tracking a file dott added: a real copy takes the link's place and the item
// goes. The repo's (or this Mac's) copy stays where it is.
func (a *App) stopPlan(id string) (*Plan, error) {
	it := a.item(id)
	if !a.added(it) {
		return nil, fmt.Errorf("%s wasn't added with dott", id)
	}
	src, dst, mf := a.C.Path(it.Link), a.C.Path(it.To), a.M.File(*it)
	return &Plan{Title: "stop tracking " + id, Lines: []string{
		"replace the link " + a.Env.Short(src) + " with a copy of " + a.Env.Short(dst),
		"take its [[item]] out of " + a.Env.Short(mf),
		dim(a.Env.Short(dst) + " stays; delete it when you want"),
	}, Run: func(a *App) error {
		b := fsops.NewBackup(a.C.St.Backups(), "stop-"+id)
		for _, p := range []string{src, mf} {
			if err := b.Save(p); err != nil {
				return err
			}
		}
		if err := b.Close(); err != nil {
			return err
		}
		undo := func(err error) error {
			if rerr := fsops.Restore(b.Dir); rerr != nil {
				return fmt.Errorf("%v; putting it back failed too: %v (the backup is in %s)", err, rerr, a.Env.Short(b.Dir))
			}
			_ = a.reload()
			return fmt.Errorf("%v; everything is put back as it was", err)
		}
		cur, err := os.ReadFile(mf)
		if err != nil {
			return err
		}
		next, _ := removeBlock(cur, id)
		if fi, err := os.Lstat(src); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s isn't the link any more; nothing changed", a.Env.Short(src))
		}
		if err := os.Remove(src); err != nil {
			return undo(err)
		}
		if err := fsops.CopyTree(dst, src); err != nil {
			return undo(err)
		}
		if err := fsops.WriteFile(mf, next, 0o644); err != nil {
			return undo(err)
		}
		if err := a.reload(); err != nil {
			return undo(err)
		}
		a.say("%s %s is a file of yours again; dott doesn't track it", green("✓"), a.Env.Short(src))
		a.say("  %s", dim("backup: "+a.Env.Short(b.Dir)+" (undo: dott restore)"))
		return nil
	}}, nil
}

// brewFiles are the Brewfiles of every brew-bundle item, the repo's and this Mac's.
func (a *App) brewFiles() map[string]string {
	out := map[string]string{}
	for _, it := range a.M.Items {
		if it.Kind == "brew-bundle" {
			out[it.ID] = a.C.Path(it.Brewfile)
		}
	}
	return out
}

type brewPkg struct {
	Kind, Name, Desc string // brew or cask; the full name; brew's description
}

// brewInfo asks brew what a package is. It only reads.
// brewName is a formula or cask name, maybe with its tap (owner/tap/name). It can't start
// with -, which brew would read as an option.
var brewName = regexp.MustCompile(`^[A-Za-z0-9@+_.][A-Za-z0-9@+_.-]*(/[A-Za-z0-9_.-]+){0,2}$`)

func brewInfo(b, name string) (brewPkg, error) {
	if !brewName.MatchString(name) {
		return brewPkg{}, fmt.Errorf("%q isn't a formula or cask name", name)
	}
	out, err := runEnv([]string{"HOMEBREW_NO_AUTO_UPDATE=1"}, b, "info", "--json=v2", name)
	if err != nil {
		return brewPkg{}, fmt.Errorf("brew doesn't know %s", name)
	}
	var info struct {
		Formulae []struct {
			FullName string `json:"full_name"`
			Desc     string `json:"desc"`
		} `json:"formulae"`
		Casks []struct {
			FullToken string `json:"full_token"`
			Desc      string `json:"desc"`
		} `json:"casks"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return brewPkg{}, fmt.Errorf("brew info %s: %v", name, err)
	}
	switch {
	case len(info.Formulae) > 0:
		return brewPkg{"brew", info.Formulae[0].FullName, info.Formulae[0].Desc}, nil
	case len(info.Casks) > 0:
		return brewPkg{"cask", info.Casks[0].FullToken, info.Casks[0].Desc}, nil
	}
	return brewPkg{}, fmt.Errorf("brew doesn't know %s", name)
}

var brewHeading = regexp.MustCompile(`^#\s+([a-z][a-z-]*)\s*$`)

// insertBrew puts line at the end of a Brewfile's # section, or a new section at the end.
func insertBrew(text []byte, section, line string) []byte {
	ls := splitLines(text)
	for i, l := range ls {
		if m := brewHeading.FindStringSubmatch(l); m != nil && m[1] == section {
			j := i + 1
			for j < len(ls) && strings.TrimSpace(ls[j]) != "" && !brewHeading.MatchString(ls[j]) {
				j++
			}
			out := append(append(append([]string{}, ls[:j]...), line), ls[j:]...)
			return joinLines(out)
		}
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	if len(ls) > 0 {
		ls = append(ls, "")
	}
	return joinLines(append(ls, "# "+section, line))
}

// insertTap adds tap "owner/name" after the last tap line, unless it is there.
func insertTap(text []byte, tap string) []byte {
	ls := splitLines(text)
	want := `tap "` + tap + `"`
	last := -1
	for i, l := range ls {
		if strings.HasPrefix(strings.TrimSpace(l), want) {
			return text
		}
		if strings.HasPrefix(l, "tap ") {
			last = i
		}
	}
	if last < 0 {
		return joinLines(append([]string{want, ""}, ls...))
	}
	return joinLines(append(append(append([]string{}, ls[:last+1]...), want), ls[last+1:]...))
}

const localBrewItem = "brew-local"

// addBrew lists a package in the repo's Brewfile (every machine) or this Mac's own, then
// installs it with the brew item's fix. name may be empty: it asks.
func (a *App) addBrew(name string) error {
	b := kinds.BrewPath()
	if b == "" {
		return fmt.Errorf("brew is not installed (see brew.sh)")
	}
	if name == "" {
		var ok bool
		if name, ok = a.T.Ask("brew package (a formula or a cask)", ""); !ok {
			return nil
		}
	}
	pkg, err := brewInfo(b, name)
	if err != nil {
		return err
	}
	short := pkg.Name[strings.LastIndex(pkg.Name, "/")+1:]
	for id, f := range a.brewFiles() {
		pkgs, _, _ := kinds.ParseBrewfile(f)
		for _, p := range pkgs {
			if p.Short == short {
				return fmt.Errorf("%s is already in %s (item %s)", short, a.Env.Short(f), id)
			}
		}
	}
	repoItem := brewItem(a.M)
	n, ok := a.T.Choose("Where should "+short+" be listed?", []string{
		"the repo's Brewfile (every machine installs it)",
		"this Mac only: " + a.Env.Short(filepath.Join(a.M.LocalDir, "Brewfile")) + " (never in the repo)",
	})
	if !ok {
		return nil
	}
	local := n == 1 || repoItem == ""
	itemID, file, section := repoItem, "", "local"
	if local {
		itemID, file = localBrewItem, filepath.Join(a.M.LocalDir, "Brewfile")
	} else {
		it := a.item(repoItem)
		file = a.C.Path(it.Brewfile)
		pkgs, _, _ := kinds.ParseBrewfile(file)
		var secs []string
		seen := map[string]bool{}
		for _, p := range pkgs {
			if !seen[p.Section] {
				seen[p.Section] = true
				secs = append(secs, p.Section)
			}
		}
		k, ok := a.T.Choose("Which section?", append(secs, "a new section"))
		if !ok {
			return nil
		}
		if k < len(secs) {
			section = secs[k]
		} else if section, ok = a.T.Ask("its name (lowercase, like cloud)", ""); !ok || !brewHeading.MatchString("# "+section) {
			return fmt.Errorf("a section name is lowercase letters and hyphens")
		}
	}
	trusted := ""
	if pkg.Kind == "brew" && strings.Count(pkg.Name, "/") == 2 {
		if a.T.YesNo("Trust "+pkg.Name+"? Homebrew 6+ loads a formula from another tap only once you trust it", false) {
			trusted = ", trusted: true"
		}
	}
	desc := pkg.Desc
	if desc == "" {
		desc = short
	}
	line := fmt.Sprintf(`%s "%s"%s  # %s`, pkg.Kind, pkg.Name, trusted, desc)
	edit := func(cur []byte) ([]byte, error) {
		if len(cur) == 0 && local {
			cur = []byte("# the packages only this Mac installs (dott's brew-local item)\n")
		}
		next := insertBrew(cur, section, line)
		if parts := strings.Split(pkg.Name, "/"); len(parts) == 3 {
			next = insertTap(next, parts[0]+"/"+parts[1])
		}
		return next, nil
	}
	p := &Plan{Title: "add " + short, Lines: []string{"add to " + a.Env.Short(file) + " under # " + section + ": " + line}}
	needItem := local && a.item(localBrewItem) == nil
	if needItem {
		p.Lines = append(p.Lines, "add the "+localBrewItem+" item to "+a.Env.Short(a.M.LocalPath)+", which installs that Brewfile")
	}
	p.Lines = append(p.Lines, "then install it: brew install "+map[string]string{"brew": "", "cask": "--cask "}[pkg.Kind]+pkg.Name)
	printPlan(a.T.Out, p)
	if a.T.Confirm() != ui.Yes {
		a.say("skipped %s", p.Title)
		return nil
	}
	cur, _ := os.ReadFile(file)
	next, _ := edit(cur)
	files := map[string][]byte{file: next}
	if needItem {
		lt, _ := os.ReadFile(a.M.LocalPath)
		files[a.M.LocalPath] = appendBlock(lt, []byte(fmt.Sprintf("\n%s\n[[item]]\nid = %q\nkind = \"brew-bundle\"\nsection = \"packages\"\ndesc = \"the packages only this Mac installs\"\nbrewfile = \"{{local}}/Brewfile\"\n", addedMark, localBrewItem)))
	}
	if err := a.writeFiles("brew-"+short, files, func() error {
		if err := a.reload(); err != nil {
			return err
		}
		if _, ok := a.status(itemID); !ok {
			return fmt.Errorf("no %s item after the change", itemID)
		}
		return nil
	}); err != nil {
		return err
	}
	a.say("%s listed %s in %s", green("✓"), short, a.Env.Short(file))
	if fp, err := a.fixPlan(itemID, short, false); err == nil {
		if err := fp.Run(a); err != nil && err != errShown {
			return err
		}
	}
	if !local {
		rel, _ := filepath.Rel(a.M.Dir, file)
		a.offerCommit("brew: add "+short, "Commit it, so other machines install it on their next pull?", []gitx.Edit{{Path: rel, Fn: edit}})
	}
	return nil
}

// writeFiles writes files with a backup first, runs check, and puts all of them back (and
// loads the manifests again) when it fails.
func (a *App) writeFiles(id string, files map[string][]byte, check func() error) error {
	b := fsops.NewBackup(a.C.St.Backups(), id)
	for p := range files {
		if err := b.Save(p); err != nil {
			return err
		}
	}
	if err := b.Close(); err != nil {
		return err
	}
	for p, data := range files {
		if err := fsops.WriteFile(p, data, 0o644); err != nil {
			_ = fsops.Restore(b.Dir)
			_ = a.reload()
			return err
		}
	}
	if err := check(); err != nil {
		_ = fsops.Restore(b.Dir)
		_ = a.reload()
		return fmt.Errorf("%v; put back as it was", err)
	}
	return nil
}

// notInBrewfileRows are the packages you installed yourself that no Brewfile lists, each one
// ready to add.
func (a *App) notInBrewfileRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	for _, n := range a.notInBrewfile() {
		n := n
		rs = append(rs, rows.Row{ID: "adopt:" + n, Tier: rows.PICK, Command: "add brew " + n, Desc: "list it in the Brewfile, or this Mac's own"})
		acts["adopt:"+n] = &Action{Guide: func(a *App) error { return a.addBrew(n) }}
	}
	return rs, acts
}
