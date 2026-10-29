// Package manifest reads dott.toml, the file in a dotfiles repo that lists every item:
// what it sets up, how to check it, and when it applies.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the manifest's name at the repo root.
const FileName = "dott.toml"

// Schema is the manifest format this dott reads.
const Schema = 1

// Manifest is one dott.toml.
type Manifest struct {
	Schema  int                 `toml:"schema"`
	Name    string              `toml:"name"`
	Groups  map[string]string   `toml:"groups"` // optional groups: id -> the question asked
	Paths   map[string]PathSpec `toml:"paths"`  // named paths, used as "@name"
	Items   []Item              `toml:"item"`
	Aliases *Aliases            `toml:"aliases"`
	Edits   []Edit              `toml:"edit"`

	Dir       string `toml:"-"` // the repo root
	LocalPath string `toml:"-"` // this Mac's local.toml, when it has one
	LocalDir  string `toml:"-"` // where this Mac's own files live: {{local}}
}

// LocalName is the file next to this Mac's settings that lists its own items.
const LocalName = "local.toml"

// local is local.toml: items and edit rows that exist only on this Mac.
type local struct {
	Items []Item `toml:"item"`
	Edits []Edit `toml:"edit"`
}

// MergeLocal adds this Mac's own items from path (local.toml) to the manifest, marked as
// local, with the same checks as the repo's. A missing file adds nothing. dir is {{local}}.
// Paths in local items should start with ~, / or {{local}}: a relative one is the repo's.
func (m *Manifest) MergeLocal(path, dir string) error {
	m.LocalPath, m.LocalDir = path, dir
	l := &local{}
	md, err := toml.DecodeFile(path, l)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	repo := map[string]bool{}
	for _, it := range m.Items {
		repo[it.ID] = true
	}
	for i := range l.Items {
		if repo[l.Items[i].ID] {
			return fmt.Errorf("%s: id %q is also in the repo's %s; pick another", path, l.Items[i].ID, FileName)
		}
		l.Items[i].Origin = "local"
	}
	m.Items = append(m.Items, l.Items...)
	m.Edits = append(m.Edits, l.Edits...)
	if err := m.check(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// File is the manifest file an item is written in: dott.toml, or this Mac's local.toml.
func (m *Manifest) File(it Item) string {
	if it.Origin == "local" {
		return m.LocalPath
	}
	return filepath.Join(m.Dir, FileName)
}

// PathSpec finds a path that differs between machines.
type PathSpec struct {
	FirstExisting []string `toml:"first_existing"` // the first of these that exists, else the first
	Gitconfig     string   `toml:"gitconfig"`      // a global git setting that holds the path
	Default       string   `toml:"default"`
}

// Cond decides whether an item applies here. Every field that is set must hold.
type Cond struct {
	Cmd  string `toml:"cmd"`  // a command on PATH
	App  string `toml:"app"`  // NAME.app in /Applications
	Dir  string `toml:"dir"`  // a folder that exists
	File string `toml:"file"` // a file that exists
	OS   string `toml:"os"`   // darwin, linux
	Any  []Cond `toml:"any"`  // at least one of these
}

// Item is one [[item]]. Which fields count depends on Kind.
type Item struct {
	ID      string   `toml:"id"`
	Kind    string   `toml:"kind"`
	Section string   `toml:"section"`
	Desc    string   `toml:"desc"`
	Group   string   `toml:"group"`
	When    *Cond    `toml:"when"`
	Needs   []string `toml:"needs"`  // item ids, or brew:NAME
	Verify  []string `toml:"verify"` // runs after a fix; it has to exit 0
	After   []string `toml:"after"`  // runs after a fix when its command exists

	// link
	Link string `toml:"link"`
	To   string `toml:"to"` // link and copy

	// block
	File   string `toml:"file"` // block and include-line
	Start  string `toml:"start"`
	End    string `toml:"end"`
	Body   string `toml:"body"`
	Mode   string `toml:"mode"`    // block: "" keeps it in sync, "seed" adds it once
	SkipIf string `toml:"skip_if"` // seed: a line matching this regexp outside the block means you set your own

	// include-line
	Format    string `toml:"format"` // "gitconfig" (include.path) or "" (plain lines)
	Line      string `toml:"line"`
	LinesFrom string `toml:"lines_from"` // a repo file; its lines that are not comments

	// copy
	From     string `toml:"from"`
	FileMode string `toml:"file_mode"` // e.g. "0755"

	// script
	Check       []string `toml:"check"` // exit 0 ok, 1 drift, 2 conflict, 3 blocked, 4 absent
	Plan        []string `toml:"plan"`
	Apply       []string `toml:"apply"`
	Present     string   `toml:"present"` // ok when this command exists (instead of check)
	PlanText    []string `toml:"plan_text"`
	Interactive bool     `toml:"interactive"`
	Undo        []string `toml:"undo"` // turning it off runs this, after a yes

	// brew-bundle
	Brewfile      string            `toml:"brewfile"`
	PackageGroups map[string]string `toml:"package_groups"` // package -> optional group
	OutsideBrew   map[string]string `toml:"outside_brew"`   // cask -> an app path that counts as installed

	// skills
	Sources      []string          `toml:"sources"` // folders of skills in the repo
	Home         string            `toml:"home"`    // the shared copy every agent reads
	ClaudeLinks  string            `toml:"claude_links"`
	Installer    []string          `toml:"installer"`
	Env          map[string]string `toml:"env"`
	Agents       []string          `toml:"agents"`
	Protect      []string          `toml:"protect"`
	Validator    []string          `toml:"validator"`
	LegacyRecord string            `toml:"legacy_record"` // name<TAB>hash lines from an older installer

	// rules
	Source  string       `toml:"source"`
	Targets []RuleTarget `toml:"targets"`

	Origin string `toml:"-"` // "local" for an item from this Mac's local.toml
}

// RuleTarget is one agent tool that reads the rules file.
type RuleTarget struct {
	Tool   string `toml:"tool"`
	Method string `toml:"method"` // "link", or "import" (an @path line in a marked block)
	File   string `toml:"file"`
	Start  string `toml:"start"`
	End    string `toml:"end"`
	When   *Cond  `toml:"when"`
}

// Aliases says where `add alias` writes.
type Aliases struct {
	RepoFile  string   `toml:"repo_file"`
	LocalFile string   `toml:"local_file"`
	Header    string   `toml:"header"`
	Before    string   `toml:"before"`
	Notes     string   `toml:"notes"`
	Search    []string `toml:"search"` // files remove alias looks in
}

// Edit is one "edit" row: a file you open to change something.
type Edit struct {
	Name string `toml:"name"`
	File string `toml:"file"`
	Desc string `toml:"desc"`
}

// Kinds are the item kinds this dott knows.
var Kinds = []string{"link", "block", "include-line", "copy", "brew-bundle", "script", "skills", "rules"}

// Load reads dir/dott.toml and checks it.
func Load(dir string) (*Manifest, error) {
	path := filepath.Join(dir, FileName)
	m := &Manifest{}
	md, err := toml.DecodeFile(path, m)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no %s in %s", FileName, dir)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s (this dott.toml may need a newer dott: brew upgrade dott)", path, strings.Join(keys, ", "))
	}
	m.Dir = dir
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (m *Manifest) check() error {
	if m.Schema != Schema {
		return fmt.Errorf("schema = %d; this dott reads schema %d (update dott)", m.Schema, Schema)
	}
	seen := map[string]bool{}
	for i, it := range m.Items {
		where := fmt.Sprintf("item %d (%s)", i+1, it.ID)
		if !idRe.MatchString(it.ID) {
			return fmt.Errorf("%s: id has to be lowercase letters, digits and single hyphens", where)
		}
		if seen[it.ID] {
			return fmt.Errorf("%s: id used twice", where)
		}
		seen[it.ID] = true
		if !slices.Contains(Kinds, it.Kind) {
			return fmt.Errorf("%s: kind %q; known kinds: %s", where, it.Kind, strings.Join(Kinds, ", "))
		}
		if it.Group != "" {
			if _, ok := m.Groups[it.Group]; !ok {
				return fmt.Errorf("%s: group %q is not in [groups]", where, it.Group)
			}
		}
		for g := range it.PackageGroups {
			if _, ok := m.Groups[it.PackageGroups[g]]; !ok {
				return fmt.Errorf("%s: package_groups.%s = %q is not in [groups]", where, g, it.PackageGroups[g])
			}
		}
		if err := it.need(); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}
	for _, it := range m.Items {
		for _, n := range it.Needs {
			if !strings.HasPrefix(n, "brew:") && !seen[n] {
				return fmt.Errorf("item %s: needs %q, which is not an item", it.ID, n)
			}
		}
	}
	return nil
}

// need checks that the fields a kind needs are there.
func (it Item) need() error {
	req := map[string][]string{
		"link":         {"link", it.Link, "to", it.To},
		"block":        {"file", it.File, "start", it.Start, "end", it.End, "body", it.Body},
		"include-line": {"file", it.File},
		"copy":         {"from", it.From, "to", it.To},
		"brew-bundle":  {"brewfile", it.Brewfile},
		"skills":       {"home", it.Home},
		"rules":        {"source", it.Source},
	}[it.Kind]
	for i := 0; i < len(req); i += 2 {
		if req[i+1] == "" {
			return fmt.Errorf("kind %s needs %s", it.Kind, req[i])
		}
	}
	if len(it.Undo) > 0 && it.Kind != "script" {
		return fmt.Errorf("undo is only for script items")
	}
	switch it.Kind {
	case "include-line":
		if (it.Line == "") == (it.LinesFrom == "") {
			return fmt.Errorf("include-line needs line or lines_from, not both")
		}
	case "script":
		if len(it.Check) == 0 && it.Present == "" {
			return fmt.Errorf("script needs check or present")
		}
		if len(it.Apply) == 0 {
			return fmt.Errorf("script needs apply")
		}
	case "block":
		if it.Mode != "" && it.Mode != "seed" {
			return fmt.Errorf("block mode %q; use seed or leave it out", it.Mode)
		}
		if it.SkipIf != "" {
			if _, err := regexp.Compile(it.SkipIf); err != nil {
				return fmt.Errorf("skip_if: %w", err)
			}
		}
	case "rules":
		for _, t := range it.Targets {
			if t.Tool == "" || t.File == "" || (t.Method != "link" && t.Method != "import") {
				return fmt.Errorf("each rules target needs tool, file and method link or import")
			}
			if t.Method == "import" && (t.Start == "" || t.End == "") {
				return fmt.Errorf("rules target %s: import needs start and end", t.Tool)
			}
		}
	}
	return nil
}

// Item returns the item with id.
func (m *Manifest) Item(id string) (Item, bool) {
	for _, it := range m.Items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// Repo turns a repo-relative path into an absolute one. Absolute paths and ~ stay as they are.
func (m *Manifest) Repo(p string) string {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "@") || strings.HasPrefix(p, "$") {
		return p
	}
	return filepath.Join(m.Dir, p)
}

// Template fills {{repo}}, {{source}} and {{local}}.
func (m *Manifest) Template(s, source string) string {
	return strings.NewReplacer("{{repo}}", m.Dir, "{{source}}", source, "{{local}}", m.LocalDir).Replace(s)
}
