// Package engine checks every item in a manifest and fixes the ones that need it.
//
// A scan only reads. A fix is always: plan, confirm (by the caller), backup, apply, verify.
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/env"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/state"
)

// State is what a check found.
type State string

const (
	OK       State = "ok"
	Off      State = "off"      // an optional item that is not on for this Mac
	Absent   State = "absent"   // the tool it is for is not on this Mac
	Drift    State = "drift"    // not set up, or the repo has something newer
	Conflict State = "conflict" // something of yours is in the way
	Blocked  State = "blocked"  // it can't be fixed until something else is
)

// NeedsYou says whether a state is one the user has to act on.
func (s State) NeedsYou() bool { return s == Drift || s == Conflict || s == Blocked }

// Status is the result of one check. Parts are the pieces of an item (each skill, package, tool).
type Status struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind,omitempty"`
	Section     string   `json:"section,omitempty"`
	Desc        string   `json:"desc,omitempty"`
	State       State    `json:"state"`
	Detail      string   `json:"detail,omitempty"`
	Fix         string   `json:"fix,omitempty"`
	Parts       []Status `json:"parts,omitempty"`
	Interactive bool     `json:"interactive,omitempty"`
}

// Part finds one part by its name (the text after the slash in its id).
func (s Status) Part(name string) (Status, bool) {
	for _, p := range s.Parts {
		if p.ID == s.ID+"/"+name {
			return p, true
		}
	}
	return Status{}, false
}

// Change is one step of a fix.
type Change struct {
	Summary     string              // one line for the plan
	Shell       string              // the same step as a shell command, for "copy"
	Paths       []string            // what it writes: each one is backed up first
	Danger      bool                // it replaces something of yours: the user types yes
	Interactive bool                // it needs the terminal and asks its own questions
	Do          func(x *Exec) error // nil for a note: a plan line that runs nothing itself
}

// Kind checks and plans one kind of item.
type Kind interface {
	Detect(c *Ctx, it *manifest.Item) Status
	// Plan returns the steps that fix the item, or only one part of it. replace lets a
	// fix replace something of yours (a conflict the user chose to override).
	Plan(c *Ctx, it *manifest.Item, s Status, part string, replace bool) ([]Change, error)
}

// Ctx is one run: the machine, the manifest, this Mac's config and what the kinds share.
type Ctx struct {
	Env   *env.Env
	M     *manifest.Manifest
	Cfg   *config.Config
	CfgAt string
	St    state.State
	Kinds map[string]Kind
	Out   io.Writer // fixes print here
	TTY   bool      // a terminal is attached, so interactive steps can run

	mu     sync.Mutex
	shared map[string]any
}

// Shared computes a fact once per run (one brew call for every package, say).
func (c *Ctx) Shared(key string, f func() any) any {
	c.mu.Lock()
	if c.shared == nil {
		c.shared = map[string]any{}
	}
	v, ok := c.shared[key]
	c.mu.Unlock()
	if ok {
		return v
	}
	v = f()
	c.mu.Lock()
	c.shared[key] = v
	c.mu.Unlock()
	return v
}

// Forget drops shared facts, so the next scan reads the machine again.
func (c *Ctx) Forget() {
	c.mu.Lock()
	c.shared = nil
	c.mu.Unlock()
}

// Path turns a manifest path into a real one: @name, {{repo}}, a repo-relative path, ~, ${VAR}.
func (c *Ctx) Path(p string) string {
	if strings.HasPrefix(p, "@") {
		return c.named(p[1:])
	}
	p = c.M.Template(p, c.RulesSource())
	return c.Env.Expand(c.M.Repo(p))
}

func (c *Ctx) named(name string) string {
	spec, ok := c.M.Paths[name]
	if !ok {
		return ""
	}
	return c.Shared("path:"+name, func() any {
		for _, p := range spec.FirstExisting {
			if p = c.Env.Expand(p); exists(p) {
				return p
			}
		}
		if spec.Gitconfig != "" && c.Env.Has("git") {
			if out, _, err := c.Run(nil, []string{"git", "config", "--global", "--get", spec.Gitconfig}, 2*time.Second); err == nil {
				if v := strings.TrimSpace(out); v != "" {
					return c.Env.Expand(v)
				}
			}
		}
		if spec.Default != "" {
			return c.Env.Expand(spec.Default)
		}
		if len(spec.FirstExisting) > 0 {
			return c.Env.Expand(spec.FirstExisting[0])
		}
		return ""
	}).(string)
}

// RulesSource is the rules file of the first rules item, for {{source}}.
func (c *Ctx) RulesSource() string {
	for _, it := range c.M.Items {
		if it.Kind == "rules" {
			return c.Env.Expand(c.M.Repo(it.Source))
		}
	}
	return ""
}

// When says whether a condition holds on this Mac. No condition always holds.
func (c *Ctx) When(w *manifest.Cond) bool {
	if w == nil {
		return true
	}
	if w.Cmd != "" && !c.Env.Has(w.Cmd) {
		return false
	}
	if w.App != "" && !c.Env.App(w.App) {
		return false
	}
	if w.Dir != "" && !isDir(c.Env.Expand(w.Dir)) {
		return false
	}
	if w.File != "" && !exists(c.Env.Expand(w.File)) {
		return false
	}
	if w.OS != "" && w.OS != c.Env.OS {
		return false
	}
	if len(w.Any) > 0 {
		return slices.ContainsFunc(w.Any, func(a manifest.Cond) bool { return c.When(&a) })
	}
	return true
}

// Run runs argv in the repo folder and returns its output and exit code. A missing
// command or a timeout is an error. stdin is empty unless in is given.
func (c *Ctx) Run(in io.Reader, argv []string, timeout time.Duration) (string, int, error) {
	if len(argv) == 0 {
		return "", 0, errors.New("empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = c.M.Dir
	cmd.Stdin = in
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), -1, fmt.Errorf("%s took longer than %s", argv[0], timeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode(), nil
	}
	if err != nil {
		return out.String(), -1, err
	}
	return out.String(), 0, nil
}

// Argv fills the paths in a manifest command.
func (c *Ctx) Argv(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = c.Env.Expand(c.M.Template(a, c.RulesSource()))
	}
	return out
}

// Scan checks every item, at most 8 at a time. It only reads.
func Scan(c *Ctx) []Status {
	out := make([]Status, len(c.M.Items))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range c.M.Items {
		it := &c.M.Items[i]
		if s, done := c.gate(it); done {
			out[i] = s
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = c.detect(it)
		}()
	}
	wg.Wait()
	for i := range out {
		c.needs(&c.M.Items[i], &out[i], out)
	}
	return out
}

// Check checks one item, the same way Scan does.
func Check(c *Ctx, id string) Status {
	all := Scan(c)
	for _, s := range all {
		if s.ID == id {
			return s
		}
	}
	return Status{ID: id, State: Blocked, Detail: "no such item"}
}

// gate decides off and absent before an item is checked.
func (c *Ctx) gate(it *manifest.Item) (Status, bool) {
	s := c.base(it)
	switch {
	case c.Cfg.IsOff(it.ID):
		s.State, s.Detail, s.Fix = Off, "turned off on this Mac", "turn it back on"
	case it.Group != "" && !c.Cfg.Groups[it.Group]:
		s.State, s.Fix = Off, "turn on "+it.Group
		if _, asked := c.Cfg.Groups[it.Group]; asked {
			s.Detail = "optional (" + it.Group + "): off on this Mac"
		} else {
			s.Detail = "optional (" + it.Group + "): not asked yet"
		}
	case !c.When(it.When):
		s.State, s.Detail = Absent, "not for this Mac: "+describe(it.When)
	default:
		return s, false
	}
	return s, true
}

func (c *Ctx) base(it *manifest.Item) Status {
	return Status{ID: it.ID, Kind: it.Kind, Section: it.Section, Desc: it.Desc, Interactive: it.Interactive}
}

func (c *Ctx) detect(it *manifest.Item) (s Status) {
	k, ok := c.Kinds[it.Kind]
	if !ok {
		s = c.base(it)
		s.State, s.Detail = Blocked, "dott doesn't know kind "+it.Kind
		return s
	}
	timeout := 3 * time.Second
	switch it.Kind {
	case "brew-bundle", "skills", "script", "copy":
		timeout = 20 * time.Second
	}
	done := make(chan Status, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				st := c.base(it)
				st.State, st.Detail = Blocked, fmt.Sprintf("the check failed: %v", r)
				done <- st
			}
		}()
		done <- k.Detect(c, it)
	}()
	select {
	case s = <-done:
	case <-time.After(timeout):
		s.State, s.Detail = Blocked, fmt.Sprintf("the check took longer than %s", timeout)
	}
	b := c.base(it)
	s.ID, s.Kind, s.Section, s.Desc, s.Interactive = b.ID, b.Kind, b.Section, b.Desc, b.Interactive
	for i := range s.Parts {
		p := &s.Parts[i]
		if c.Cfg.IsOff(p.ID) && p.State != OK {
			p.State, p.Detail, p.Fix = Off, "turned off on this Mac", "turn it back on"
		}
	}
	if len(s.Parts) > 0 && s.State == "" {
		s.State, s.Detail, s.Fix = Roll(s.Parts)
	}
	return s
}

// Roll sums up parts: the worst state wins, and the detail counts each state.
func Roll(parts []Status) (State, string, string) {
	order := []State{Blocked, Conflict, Drift, OK, Off, Absent}
	worst := Absent
	count := map[State][]string{}
	for _, p := range parts {
		name := p.ID[strings.LastIndex(p.ID, "/")+1:]
		count[p.State] = append(count[p.State], name)
		if slices.Index(order, p.State) < slices.Index(order, worst) {
			worst = p.State
		}
	}
	var bits []string
	for _, st := range order {
		if n := len(count[st]); n > 0 {
			names := ""
			if st.NeedsYou() {
				names = ": " + list(count[st], 4)
			}
			bits = append(bits, fmt.Sprintf("%d %s%s", n, st, names))
		}
	}
	fix := ""
	if worst.NeedsYou() {
		fix = "fix the parts that need it"
	}
	if worst == Absent || worst == Off {
		worst = OK
	}
	return worst, strings.Join(bits, " · "), fix
}

func list(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + fmt.Sprintf(" and %d more", len(names)-max)
}

// needs marks an item blocked when it needs fixing but something it needs isn't there yet.
func (c *Ctx) needs(it *manifest.Item, s *Status, all []Status) {
	if !s.State.NeedsYou() || s.State == Blocked {
		return
	}
	for _, n := range it.Needs {
		if pkg, ok := strings.CutPrefix(n, "brew:"); ok {
			if !c.BrewHas(pkg) {
				s.State, s.Detail, s.Fix = Blocked, s.Detail+"; needs "+pkg+" (brew), which is not installed", brewFix(pkg, all)
				return
			}
			continue
		}
		for _, o := range all {
			if o.ID == n && o.State.NeedsYou() {
				s.State, s.Detail, s.Fix = Blocked, s.Detail+"; needs "+n+" first", "fix "+n+" first"
				return
			}
		}
	}
}

// Packages is a kind that can say whether one package is installed (brew-bundle does).
type Packages interface {
	Has(c *Ctx, name string) bool
}

// brewFix is the hint for an item that waits for a brew package: the package's own row when a
// Brewfile lists it, else the brew command.
func brewFix(pkg string, all []Status) string {
	short := pkg[strings.LastIndex(pkg, "/")+1:]
	for _, o := range all {
		if p, ok := o.Part(short); ok && o.Kind == "brew-bundle" {
			return "fix " + p.ID + " first"
		}
	}
	return "brew install " + pkg + " first"
}

// BrewHas says whether a formula or cask is installed, for needs = ["brew:NAME"].
func (c *Ctx) BrewHas(name string) bool {
	if p, ok := c.Kinds["brew-bundle"].(Packages); ok {
		return p.Has(c, name)
	}
	return false
}

func describe(w *manifest.Cond) string {
	if w == nil {
		return ""
	}
	var bits []string
	if w.Cmd != "" {
		bits = append(bits, w.Cmd+" is not installed")
	}
	if w.App != "" {
		bits = append(bits, w.App+".app is not installed")
	}
	if w.Dir != "" {
		bits = append(bits, "no "+w.Dir)
	}
	if w.File != "" {
		bits = append(bits, "no "+w.File)
	}
	if w.OS != "" {
		bits = append(bits, "only on "+w.OS)
	}
	if len(w.Any) > 0 {
		var any []string
		for _, a := range w.Any {
			any = append(any, describe(&a))
		}
		bits = append(bits, strings.Join(any, " and "))
	}
	return strings.Join(bits, ", ")
}

// Exec runs the changes of one fix.
type Exec struct {
	C      *Ctx
	Out    io.Writer
	Backup *fsops.Backup
}

// Protected says whether a path is one dott must never change.
func (c *Ctx) Protected(p string) bool {
	for _, it := range c.M.Items {
		for _, pr := range it.Protect {
			pr = c.Env.Expand(pr)
			if p == pr || strings.HasPrefix(p, pr+"/") {
				return true
			}
		}
	}
	return false
}

// Result is what a fix did.
type Result struct {
	Backup string // the backup folder, empty when nothing was replaced
	After  Status
}

// Apply runs the changes, then checks the item (or the part) again. When the check
// doesn't come back ok, the error says so and the backup can put it back.
func Apply(c *Ctx, it *manifest.Item, part string, changes []Change) (Result, error) {
	for _, ch := range changes {
		for _, p := range ch.Paths {
			if c.Protected(p) {
				return Result{}, fmt.Errorf("%s is protected; dott never changes it", c.Env.Short(p))
			}
		}
	}
	unlock, err := c.St.Lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	name := it.ID
	if part != "" {
		name += "-" + strings.ReplaceAll(part, "/", "-")
	}
	b := fsops.NewBackup(c.St.Backups(), name)
	x := &Exec{C: c, Out: c.Out, Backup: b}
	var applyErr error
	for _, ch := range changes {
		for _, p := range ch.Paths {
			if err := b.Save(p); err != nil {
				applyErr = err
				break
			}
		}
		if applyErr != nil {
			break
		}
		if ch.Do == nil {
			continue // a note in the plan, like the first lines of a script's plan
		}
		if err := ch.Do(x); err != nil {
			applyErr = fmt.Errorf("%s: %w", ch.Summary, err)
			break
		}
	}
	if err := b.Close(); err != nil && applyErr == nil {
		applyErr = err
	}
	res := Result{}
	if len(b.Entries) > 0 {
		res.Backup = b.Dir
	}
	if applyErr != nil {
		return res, applyErr
	}
	if len(it.After) > 0 && c.Env.Has(it.After[0]) {
		if out, code, err := c.Run(nil, c.Argv(it.After), 5*time.Minute); err != nil || code != 0 {
			fmt.Fprintf(c.Out, "  note: %s exited %d: %s\n", it.After[0], code, firstLine(out))
		}
	}
	c.Forget()
	res.After = Check(c, it.ID)
	got := res.After
	if part != "" {
		got, _ = res.After.Part(part)
	}
	if got.State.NeedsYou() {
		return res, fmt.Errorf("checked again and it is still %s: %s", got.State, got.Detail)
	}
	if len(it.Verify) > 0 {
		out, code, err := c.Run(nil, c.Argv(it.Verify), time.Minute)
		if err != nil || code != 0 {
			return res, fmt.Errorf("verify (%s) failed: %s", strings.Join(it.Verify, " "), firstLine(out))
		}
	}
	return res, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Rel shows a path relative to the repo when it is inside it.
func (c *Ctx) Rel(p string) string {
	if r, err := filepath.Rel(c.M.Dir, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return c.Env.Short(p)
}
