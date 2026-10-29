package kinds

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Brew installs the packages a Brewfile lists. Each package is a part; a package can belong
// to an optional group (awscli to aws, say), so it is only installed when that group is on.
type Brew struct{}

// Package is one brew or cask line of a Brewfile.
type Package struct {
	Kind    string // brew, cask
	Name    string // as written: hashicorp/tap/terraform
	Short   string // terraform
	Section string // the "# section" heading above it
	Desc    string // the comment after it
	Trusted bool   // trusted: true, for a formula from a tap
}

var (
	brewLine = regexp.MustCompile(`^(brew|cask)\s+"([^"]+)"(.*?)(\s+#\s*(.*))?$`)
	tapLine  = regexp.MustCompile(`^tap\s+"([^"]+)"`)
	heading  = regexp.MustCompile(`^#\s+([a-z][a-z-]*)\s*$`)
)

// ParseBrewfile reads the packages and taps of a Brewfile, the same lines the old installer read.
func ParseBrewfile(p string) ([]Package, []string, error) {
	ls, ok, err := readLines(p)
	if err != nil || !ok {
		return nil, nil, fmt.Errorf("no Brewfile at %s", p)
	}
	var pkgs []Package
	var taps []string
	sec := "misc"
	for _, l := range ls {
		if m := heading.FindStringSubmatch(l); m != nil {
			sec = m[1]
		} else if m := tapLine.FindStringSubmatch(l); m != nil {
			taps = append(taps, m[1])
		} else if m := brewLine.FindStringSubmatch(l); m != nil {
			short := m[2][strings.LastIndex(m[2], "/")+1:]
			desc := strings.TrimSpace(m[5])
			if desc == "" {
				desc = short
			}
			pkgs = append(pkgs, Package{m[1], m[2], short, sec, desc, strings.Contains(m[3], "trusted: true")})
		}
	}
	return pkgs, taps, nil
}

// BrewPath finds brew even when PATH doesn't have it yet (a fresh Mac, a launchd shell).
func BrewPath() string {
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

type installed struct {
	ok       bool
	formulae map[string]string // name -> version
	casks    map[string]bool
}

// list asks brew once per run what is installed. brew list only reads.
func list(c *engine.Ctx) installed {
	return c.Shared("brew:installed", func() any {
		b := BrewPath()
		if b == "" {
			return installed{}
		}
		in := installed{ok: true, formulae: map[string]string{}, casks: map[string]bool{}}
		out, code, err := c.Run(nil, []string{b, "list", "--formula", "--versions"}, 15*time.Second)
		if err != nil || code != 0 {
			return installed{}
		}
		for _, l := range strings.Split(out, "\n") {
			if f := strings.Fields(l); len(f) >= 2 {
				in.formulae[f[0]] = f[len(f)-1]
			}
		}
		out, _, _ = c.Run(nil, []string{b, "list", "--cask", "-1"}, 15*time.Second)
		for _, l := range strings.Fields(out) {
			in.casks[l] = true
		}
		return in
	}).(installed)
}

// Has says whether a formula or cask is installed (for needs = ["brew:NAME"]).
func (Brew) Has(c *engine.Ctx, name string) bool {
	in := list(c)
	short := name[strings.LastIndex(name, "/")+1:]
	_, f := in.formulae[short]
	return f || in.casks[short]
}

func pkgState(c *engine.Ctx, it *manifest.Item, p Package, in installed) (engine.State, string, string) {
	if g := it.PackageGroups[p.Short]; g != "" && !c.Cfg.Groups[g] {
		if v, ok := in.formulae[p.Short]; ok {
			return engine.Off, "optional (" + g + "), installed anyway (" + v + ")", "turn on " + g
		}
		return engine.Off, "optional (" + g + "): off on this Mac", "turn on " + g
	}
	if p.Kind == "cask" {
		if in.casks[p.Short] {
			return engine.OK, "brew cask " + p.Short, ""
		}
		if app := it.OutsideBrew[p.Short]; app != "" && exists(c.Env.Expand(app)) {
			return engine.OK, c.Env.Short(c.Env.Expand(app)) + " (installed outside brew)", ""
		}
		return engine.Drift, "not installed", "brew install --cask " + p.Short
	}
	if v, ok := in.formulae[p.Short]; ok {
		return engine.OK, p.Desc + " (" + v + ")", ""
	}
	return engine.Drift, "not installed", "brew install " + p.Name
}

func (Brew) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	pkgs, _, err := ParseBrewfile(c.Path(it.Brewfile))
	if err != nil {
		return status(engine.Blocked, err.Error(), "check the repo")
	}
	in := list(c)
	if !in.ok {
		return status(engine.Blocked, "Homebrew is not installed", "install Homebrew first: see brew.sh")
	}
	var parts []engine.Status
	for _, p := range pkgs {
		st, detail, fix := pkgState(c, it, p, in)
		s := part(it.ID, p.Short, st, detail, fix)
		s.Section, s.Desc = p.Section, p.Desc
		parts = append(parts, s)
	}
	s := engine.Status{Parts: parts}
	s.State, s.Detail, s.Fix = engine.Roll(parts)
	return s
}

func (Brew) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, name string, replace bool) ([]engine.Change, error) {
	pkgs, _, err := ParseBrewfile(c.Path(it.Brewfile))
	if err != nil {
		return nil, err
	}
	b := BrewPath()
	if b == "" {
		return nil, fmt.Errorf("brew is not installed (see brew.sh)")
	}
	in := list(c)
	var formulae, casks, trust []string
	for _, p := range pkgs {
		if name != "" && p.Short != name {
			continue
		}
		if st, _, _ := pkgState(c, it, p, in); st != engine.Drift {
			continue
		}
		if p.Kind == "cask" {
			casks = append(casks, p.Name)
		} else {
			formulae = append(formulae, p.Name)
			if p.Trusted {
				trust = append(trust, p.Name)
			}
		}
	}
	var out []engine.Change
	if len(trust) > 0 {
		argv := append([]string{b, "trust", "--formula"}, trust...)
		out = append(out, engine.Change{
			Summary: "trust " + strings.Join(trust, ", ") + " (Homebrew 6+ loads a tap's formula only once you trust it)",
			Shell:   engine.Quote(argv...),
			Do:      func(x *engine.Exec) error { return x.Stream(argv) },
		})
	}
	if len(formulae) > 0 {
		argv := append([]string{b, "install", "--formula"}, formulae...)
		out = append(out, engine.Change{
			Summary: "brew install --formula " + strings.Join(formulae, " "),
			Shell:   engine.Quote(argv...),
			Do:      func(x *engine.Exec) error { return x.Stream(argv) },
		})
	}
	if len(casks) > 0 {
		argv := append([]string{b, "install", "--cask", "--adopt"}, casks...)
		out = append(out, engine.Change{
			Summary: "brew install --cask --adopt " + strings.Join(casks, " ") + " (--adopt: an app you already have is taken over, not copied again)",
			Shell:   engine.Quote(argv...),
			Do:      func(x *engine.Exec) error { return x.Stream(argv) },
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing to install")
	}
	return out, nil
}
