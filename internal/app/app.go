// Package app is dott's session: it loads the repo and this Mac's settings, scans, turns
// the states into picker rows, and runs what a row does under its tier's rules.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/env"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/picker"
	"github.com/jgorodetsky/dott/internal/rows"
	"github.com/jgorodetsky/dott/internal/state"
	"github.com/jgorodetsky/dott/internal/ui"
)

// App is one run of dott.
type App struct {
	Env *env.Env
	Cfg *config.Config
	M   *manifest.Manifest
	C   *engine.Ctx
	Pk  picker.Backend
	T   *ui.Term  // nil without a terminal
	Out io.Writer // stdout
	Err io.Writer
	Yes bool // --yes: answer yes to change prompts (never to dangerous ones)

	All  []engine.Status
	acts map[string]*Action
}

// ErrNoRepo means this Mac has no dotfiles repo set up yet.
var ErrNoRepo = errors.New("no dotfiles repo set up on this Mac yet: run dott --init owner/dotfiles (or a path)")

// ConfigPath is this Mac's settings file: $DOTT_CONFIG, else ~/.config/dott/config.toml.
func ConfigPath(e *env.Env) string {
	if p := e.Getenv("DOTT_CONFIG"); p != "" {
		return e.Expand(p)
	}
	return filepath.Join(e.ConfigDir(), "config.toml")
}

// RepoDir finds the dotfiles repo: $DOTT_REPO, the config, the current folder, ~/dotfiles.
func RepoDir(e *env.Env, cfg *config.Config) (string, error) {
	if r := e.Getenv("DOTT_REPO"); r != "" {
		return filepath.Abs(e.Expand(r))
	}
	if cfg.Repo != "" {
		return e.Expand(cfg.Repo), nil
	}
	if wd, err := os.Getwd(); err == nil && exists(filepath.Join(wd, manifest.FileName)) {
		return wd, nil
	}
	if d := filepath.Join(e.Home, "dotfiles"); exists(filepath.Join(d, manifest.FileName)) {
		return d, nil
	}
	return "", ErrNoRepo
}

// Load reads everything a run needs. It changes nothing.
func Load(e *env.Env, out, errw io.Writer) (*App, error) {
	cfg, err := config.Load(ConfigPath(e))
	if err != nil {
		return nil, err
	}
	dir, err := RepoDir(e, cfg)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return nil, err
	}
	a := &App{Env: e, Cfg: cfg, M: m, Pk: picker.New(), Out: out, Err: errw}
	a.C = &engine.Ctx{Env: e, M: m, Cfg: cfg, CfgAt: ConfigPath(e), St: state.State{Dir: e.StateDir()}, Kinds: kinds.All(), Out: out}
	return a, nil
}

// Scan checks every item again.
func (a *App) Scan() {
	a.C.Forget()
	a.All = engine.Scan(a.C)
}

// SaveConfig writes this Mac's settings.
func (a *App) SaveConfig() error { return a.Cfg.Save(a.C.CfgAt) }

// JSON prints every item's state. It changes nothing.
func (a *App) JSON() error {
	a.Scan()
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Repo  string          `json:"repo"`
		Items []engine.Status `json:"items"`
	}{a.M.Dir, a.All})
}

// AskGroups asks once about each optional group this Mac hasn't answered, and remembers.
func (a *App) AskGroups() {
	if a.T == nil {
		return
	}
	var names []string
	for g := range a.M.Groups {
		if _, asked := a.Cfg.Groups[g]; !asked {
			names = append(names, g)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	a.T.Say("\033[1mOptional parts\033[0m \033[2m(asked once; change it later with turn on / turn off)\033[0m")
	for _, g := range names {
		a.Cfg.Groups[g] = a.T.YesNo(fmt.Sprintf("  %s: %s", g, a.M.Groups[g]), false)
	}
	if err := a.SaveConfig(); err != nil {
		a.T.Say("could not save %s: %v", a.Env.Short(a.C.CfgAt), err)
	}
}

// Session runs the picker until the user quits. Words filter the first screen, and a single
// match is picked at once (its tier still asks).
func (a *App) Session(query string) int {
	a.AskGroups()
	a.Scan()
	select1 := query != ""
	for {
		rs, acts := a.Build()
		a.acts = acts
		res, err := a.Pk.Pick(picker.Request{Lines: rows.Lines(rs, a.T.Cols), Query: query, Select1: select1, History: a.history()})
		if err != nil {
			fmt.Fprintln(a.Err, "dott:", err)
			return 2
		}
		id := rows.ID(res.Line)
		r, ok := findRow(rs, id)
		if res.Code == picker.CodeInterrupt || !ok {
			return 0
		}
		_ = a.C.St.Pick(id, time.Now())
		a.Act(r, acts[id])
		a.Scan()
		query, select1 = "", false
	}
}

func (a *App) history() string {
	if err := os.MkdirAll(a.C.St.Dir, 0o755); err != nil {
		return ""
	}
	return a.C.St.History()
}

// Matches prints the rows that match, one per line, for a run without a terminal.
func (a *App) Matches(query string) int {
	a.Scan()
	rs, acts := a.Build()
	if a.Yes && query == "fix everything" {
		if _, ok := acts["fix-all"]; !ok {
			fmt.Fprintln(a.Out, "nothing to fix")
			return 0
		}
	}
	got, err := a.Pk.Filter(rows.Lines(rs, 100), query)
	if err != nil {
		fmt.Fprintln(a.Err, "dott:", err)
		return 2
	}
	if len(got) == 0 {
		fmt.Fprintf(a.Err, "dott: no row matches %q\n", query)
		return 1
	}
	if a.Yes {
		return a.yesRun(rs, acts, got)
	}
	for _, line := range got {
		if r, ok := findRow(rs, rows.ID(line)); ok {
			fmt.Fprintf(a.Out, "%s\t%s\t%s\n", r.Tier, r.Command, r.Desc)
		}
	}
	return 0
}

// yesRun runs the best match without a terminal: only a change row, never a dangerous,
// guided or interactive one.
func (a *App) yesRun(rs []rows.Row, acts map[string]*Action, got []string) int {
	r, _ := findRow(rs, rows.ID(got[0]))
	act := acts[r.ID]
	if r.Tier != rows.MUT || act == nil || act.Changes == nil {
		fmt.Fprintf(a.Err, "dott: --yes runs only a change row (◆) without a terminal; %q is %s\n", r.Command, r.Tier)
		return 2
	}
	p, err := act.Changes(a)
	if err != nil {
		fmt.Fprintln(a.Err, "dott:", err)
		return 1
	}
	if p.Interactive {
		fmt.Fprintf(a.Err, "dott: %s asks its own questions; run it in a terminal\n", r.Command)
		return 2
	}
	printPlan(a.Out, p)
	if err := p.Run(a); err != nil {
		if !errors.Is(err, errShown) {
			fmt.Fprintln(a.Err, "dott:", err)
		}
		return 1
	}
	return 0
}

func findRow(rs []rows.Row, id string) (rows.Row, bool) {
	for _, r := range rs {
		if r.ID == id && id != "" {
			return r, true
		}
	}
	return rows.Row{}, false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// say prints to the terminal when there is one, else to stdout.
func (a *App) say(format string, args ...any) {
	if a.T != nil {
		a.T.Say(format, args...)
		return
	}
	fmt.Fprintf(a.Out, format+"\n", args...)
}

func (a *App) w() io.Writer {
	if a.T != nil {
		return a.T.Out
	}
	return a.Out
}

// Color is off when dott writes to a pipe or NO_COLOR is set.
var Color = true

func paint(code, s string) string {
	if !Color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func dim(s string) string   { return paint("2", s) }
func bold(s string) string  { return paint("1", s) }
func green(s string) string { return paint("32", s) }
func red(s string) string   { return paint("31", s) }
func amber(s string) string { return paint("33", s) }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func list(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + fmt.Sprintf(" and %d more", len(names)-max)
}
