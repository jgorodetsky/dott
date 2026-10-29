package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/rows"
)

// Outdated is one package with a newer version.
type Outdated struct {
	Kind, Name, Short, From, To string
	Note                        string // why it is left for you to pick on its own
	Careful                     bool
	InBrewfile                  bool
}

// careful are tools where even a minor version bump can bite.
var careful = map[string]string{
	"terraform":      "state written by a newer terraform can't be read by an older one; repos often pin a version",
	"kubernetes-cli": "kubectl should stay within one minor version of your clusters",
}

func vpart(v string, i int) int {
	f := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '_' || r == ',' || r == '-' })
	if i >= len(f) {
		return 0
	}
	n, _ := strconv.Atoi(f[i])
	return n
}

// ParseOutdated reads brew outdated --json=v2 the way the old upgrade did: pinned formulae
// are left out; a new major version or a careful tool's minor bump is marked.
func ParseOutdated(data []byte, brewfile map[string]bool, mine map[string]bool) []Outdated {
	var v struct {
		Formulae []struct {
			Name      string   `json:"name"`
			Installed []string `json:"installed_versions"`
			Current   string   `json:"current_version"`
			Pinned    bool     `json:"pinned"`
		} `json:"formulae"`
		Casks []struct {
			Name      string   `json:"name"`
			Installed []string `json:"installed_versions"`
			Current   string   `json:"current_version"`
		} `json:"casks"`
	}
	if json.Unmarshal(data, &v) != nil {
		return nil
	}
	var out []Outdated
	add := func(kind, name string, inst []string, cur string) {
		short := name[strings.LastIndex(name, "/")+1:]
		in := brewfile[short]
		if !in && !mine[short] {
			return // a dependency: it upgrades with whatever needs it
		}
		from := ""
		if len(inst) > 0 {
			from = inst[len(inst)-1]
		}
		o := Outdated{Kind: kind, Name: name, Short: short, From: from, To: cur, InBrewfile: in}
		if a, b := vpart(from, 0), vpart(cur, 0); a != b && a >= 1 {
			o.Note, o.Careful = "major version: can break things", true
		} else if why := careful[short]; why != "" && vpart(from, 1) != vpart(cur, 1) {
			o.Note, o.Careful = why, true
		}
		if !in {
			o.Note = strings.TrimPrefix(o.Note+" · not in the Brewfile", " · ")
		}
		out = append(out, o)
	}
	for _, f := range v.Formulae {
		if !f.Pinned {
			add("formula", f.Name, f.Installed, f.Current)
		}
	}
	for _, c := range v.Casks {
		add("cask", c.Name, c.Installed, c.Current)
	}
	// the Brewfile's first, then yours
	var bf, rest []Outdated
	for _, o := range out {
		if o.InBrewfile {
			bf = append(bf, o)
		} else {
			rest = append(rest, o)
		}
	}
	return append(bf, rest...)
}

// upgradeRows lists newer versions; brew's own data, read without going online.
func (a *App) upgradeRows() ([]rows.Row, map[string]*Action) {
	acts := map[string]*Action{}
	var rs []rows.Row
	b := kinds.BrewPath()
	if b == "" {
		a.say("Homebrew is not installed")
		return nil, nil
	}
	if pid := otherBrew(); pid != "" {
		a.say("%s another brew is running (pid %s); wait for it, or stop it: kill %s", red("✗"), pid, pid)
		return nil, nil
	}
	a.say("%s", dim("reading brew's version info…"))
	env := []string{"HOMEBREW_NO_AUTO_UPDATE=1"}
	data, _ := runEnv(env, b, "outdated", "--json=v2")
	brewfile := map[string]bool{}
	if it := a.item(brewItem(a.M)); it != nil {
		pkgs, _, _ := kinds.ParseBrewfile(a.C.Path(it.Brewfile))
		for _, p := range pkgs {
			brewfile[p.Short] = true
		}
	}
	mine := map[string]bool{}
	leaves, err := runEnv(env, b, "list", "--formula", "--installed-on-request")
	if err != nil {
		leaves, _ = runEnv(env, b, "leaves", "--installed-on-request")
	}
	casks, _ := runEnv(env, b, "list", "--cask", "-1")
	for _, n := range strings.Fields(string(leaves) + " " + string(casks)) {
		mine[n[strings.LastIndex(n, "/")+1:]] = true
	}
	ups := ParseOutdated(data, brewfile, mine)
	age := brewAge(b)
	if clt := oldCLT(); clt != "" {
		rs = append(rs, rows.Row{ID: "clt", Tier: rows.RO, Command: "command line tools are too old (" + clt + ")", Desc: "brew can't build anything until they're updated"})
		acts["clt"] = &Action{Show: func(a *App) error {
			a.say("Xcode's command line tools are %s, older than this macOS. dott doesn't run sudo, so update them yourself:", clt)
			a.say("  %s", bold("softwareupdate --list"))
			a.say("  %s", bold("sudo softwareupdate --install '<the Command Line Tools label it lists>'"))
			a.say("  %s", dim("or open System Settings > General > Software Update"))
			return nil
		}}
	}
	var safe []Outdated
	for _, o := range ups {
		if !o.Careful {
			safe = append(safe, o)
		}
	}
	if len(safe) > 0 {
		var names []string
		for _, o := range safe {
			names = append(names, o.Short)
		}
		rs = append(rs, rows.Row{ID: "safe", Tier: rows.MUT, Command: "upgrade the usual ones (" + strconv.Itoa(len(safe)) + ")", Desc: list(names, 5)})
		acts["safe"] = &Action{Changes: func(a *App) (*Plan, error) { return a.upgradePlan(safe, ups) }}
	}
	for _, o := range ups {
		o := o
		tier := rows.MUT
		if o.Careful {
			tier = rows.DGR
		}
		desc := o.From + " → " + o.To
		if o.Note != "" {
			desc += "   " + o.Note
		}
		rs = append(rs, rows.Row{ID: "one:" + o.Name, Tier: tier, Command: "upgrade " + o.Short, Desc: desc})
		acts["one:"+o.Name] = &Action{Changes: func(a *App) (*Plan, error) { return a.upgradePlan([]Outdated{o}, ups) }}
	}
	rs = append(rs, rows.Row{ID: "update", Tier: rows.MUT, Command: "refresh brew's version info", Desc: "brew update; the info is " + age + " old"})
	acts["update"] = &Action{Changes: func(a *App) (*Plan, error) {
		return &Plan{Title: "brew update", Lines: []string{"brew update (about 10 seconds), then the list again"}, Shell: "brew update",
			Run: func(a *App) error {
				x := &engine.Exec{C: a.C, Out: a.w()}
				if err := x.Stream([]string{b, "update"}); err != nil {
					return err
				}
				next, nacts := a.upgradeRows()
				if len(next) == 0 {
					return nil
				}
				a.Act(rows.Row{ID: "upgrade", Tier: rows.PICK, Command: "upgrade"}, &Action{Next: func(*App) ([]rows.Row, map[string]*Action) { return next, nacts }}, "", false)
				return nil
			}}, nil
	}}
	if len(ups) == 0 {
		a.say("%s everything brew installed for you is up to date (its info is %s old)", green("✓"), age)
	}
	return rs, acts
}

// upgradePlan works out what else comes along (brew's dry run), then upgrades and checks
// that each package really has the new version.
func (a *App) upgradePlan(pick, all []Outdated) (*Plan, error) {
	b := kinds.BrewPath()
	var formulae, casks []string
	p := &Plan{Title: "upgrade " + plural(len(pick), "package", "packages")}
	for _, o := range pick {
		p.Lines = append(p.Lines, o.Short+" "+o.From+" → "+o.To)
		if o.Kind == "cask" {
			casks = append(casks, o.Name)
		} else {
			formulae = append(formulae, o.Name)
		}
	}
	if len(formulae) > 0 {
		out, _ := runEnv([]string{"HOMEBREW_NO_AUTO_UPDATE=1"}, b, append([]string{"upgrade", "--dry-run", "--formula"}, formulae...)...)
		deps, fresh := parseDryRun(string(out))
		if len(deps) > 0 {
			p.Lines = append(p.Lines, "+ "+plural(len(deps), "dependency upgrade", "dependency upgrades")+": "+list(deps, 6))
			picked := map[string]bool{}
			for _, o := range pick {
				picked[o.Short] = true
			}
			for _, d := range deps {
				for _, o := range all {
					if o.Short == d && !picked[d] {
						p.Lines = append(p.Lines, amber("⚠ also upgrades "+d+" "+o.From+" → "+o.To+", which you didn't pick: a picked package needs it"))
					}
				}
			}
		}
		if len(fresh) > 0 {
			p.Lines = append(p.Lines, "+ "+plural(len(fresh), "new dependency", "new dependencies")+": "+list(fresh, 6))
		}
	}
	var shell []string
	if len(formulae) > 0 {
		shell = append(shell, engine.Quote(append([]string{"brew", "upgrade", "--formula"}, formulae...)...))
	}
	if len(casks) > 0 {
		shell = append(shell, engine.Quote(append([]string{"brew", "upgrade", "--cask"}, casks...)...))
	}
	p.Shell = strings.Join(shell, "\n")
	p.Run = func(a *App) error {
		x := &engine.Exec{C: a.C, Out: a.w()}
		env := "HOMEBREW_NO_AUTO_UPDATE=1"
		if len(formulae) > 0 {
			_ = x.Stream(append([]string{b, "upgrade", "--formula"}, formulae...), env)
		}
		if len(casks) > 0 {
			_ = x.Stream(append([]string{b, "upgrade", "--cask"}, casks...), env)
		}
		bad := 0
		for _, o := range pick {
			flag := "--formula"
			if o.Kind == "cask" {
				flag = "--cask"
			}
			out, _ := exec.Command(b, "list", flag, "--versions", o.Short).Output()
			f := strings.Fields(string(out))
			now := ""
			if len(f) > 0 {
				now = f[len(f)-1]
			}
			if now == o.To {
				a.say("%s %s %s → %s", green("✓"), o.Short, o.From, o.To)
			} else {
				bad++
				a.say("%s %s is still %s", red("✗"), o.Short, now)
			}
		}
		if bad > 0 {
			return fmt.Errorf("%s not upgraded", plural(bad, "package", "packages"))
		}
		return nil
	}
	return p, nil
}

var dryHead = regexp.MustCompile(`^==> Would (upgrade|install) \d+ dependenc`)

func parseDryRun(out string) (up, fresh []string) {
	sec := ""
	for _, l := range strings.Split(out, "\n") {
		if m := dryHead.FindStringSubmatch(l); m != nil {
			sec = m[1]
			continue
		}
		if strings.HasPrefix(l, "==>") {
			sec = ""
			continue
		}
		f := strings.Fields(l)
		if sec == "" || len(f) == 0 {
			continue
		}
		n := f[0][strings.LastIndex(f[0], "/")+1:]
		if sec == "upgrade" {
			up = append(up, n)
		} else {
			fresh = append(fresh, n)
		}
	}
	return up, fresh
}

func runEnv(env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd.Output()
}

// otherBrew is the pid of a brew that is installing or upgrading right now.
func otherBrew() string {
	out, _ := exec.Command("pgrep", "-f", `brew\.rb (upgrade|install|reinstall|uninstall|bundle)`).Output()
	if f := strings.Fields(string(out)); len(f) > 0 {
		return f[0]
	}
	return ""
}

// brewAge is how old brew's version info is.
func brewAge(b string) string {
	repo, _ := exec.Command(b, "--repository").Output()
	cache, _ := exec.Command(b, "--cache").Output()
	var t time.Time
	for _, p := range []string{filepath.Join(strings.TrimSpace(string(repo)), ".git", "FETCH_HEAD"), filepath.Join(strings.TrimSpace(string(cache)), "api")} {
		if !filepath.IsAbs(p) {
			continue // brew printed no folder: a relative path would be read where dott runs
		}
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	if t.IsZero() {
		return "an unknown time"
	}
	d := time.Since(t)
	switch {
	case d < 2*time.Minute:
		return "a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// oldCLT is the command line tools' version when it is older than macOS, else "".
func oldCLT() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("pkgutil", "--pkg-info=com.apple.pkg.CLTools_Executables").Output()
	if err != nil {
		return ""
	}
	v := ""
	for _, l := range strings.Split(string(out), "\n") {
		if s, ok := strings.CutPrefix(l, "version: "); ok {
			v = strings.TrimSpace(s)
		}
	}
	mac, _ := exec.Command("sw_vers", "-productVersion").Output()
	if v != "" && vpart(v, 0) < vpart(strings.TrimSpace(string(mac)), 0) {
		return fmt.Sprintf("%d.%d", vpart(v, 0), vpart(v, 1))
	}
	return ""
}
