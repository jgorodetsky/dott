// Package testkit builds a fake machine for tests: a temp HOME, a small dotfiles repo that
// uses every kind, and stub brew, npx and uvx commands that log each call and fake their work.
// Nothing here touches the real HOME.
package testkit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/env"
	"github.com/jgorodetsky/dott/internal/kinds"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/state"
)

// Fixture is one fake machine.
type Fixture struct {
	Root, Home, Repo, Bin, Log string
}

// Manifest is the fixture repo's dott.toml: one or more items of every kind.
const Manifest = `schema = 1
name = "fixture"

[groups]
extra = "the extra package"

[paths.term]
first_existing = ["~/.term-a", "~/.term"]

[[item]]
id = "pkgs"
kind = "brew-bundle"
section = "packages"
desc = "the Brewfile"
brewfile = "Brewfile"
package_groups = { jq = "extra" }

[[item]]
id = "loader"
kind = "block"
section = "shell"
desc = "the loader line"
file = "~/.zshrc"
start = "# >>> fx >>>"
end = "# <<< fx <<<"
body = 'source "{{repo}}/rc"'

[[item]]
id = "theme"
kind = "block"
section = "term"
desc = "a theme, added once"
file = "@term"
mode = "seed"
skip_if = '^theme\s*='
start = "# >>> fx theme >>>"
end = "# <<< fx theme <<<"
body = "theme = fx"

[[item]]
id = "gitinc"
kind = "include-line"
section = "git"
desc = "include the repo's git config"
format = "gitconfig"
file = "~/.gitconfig"
line = "gitconfig"

[[item]]
id = "ignore"
kind = "include-line"
section = "git"
desc = "global ignore lines"
file = "~/.config/git/ignore"
lines_from = "ignore"

[[item]]
id = "themes"
kind = "copy"
section = "term"
desc = "theme files"
from = "themes"
to = "~/.config/fx/themes"

[[item]]
id = "cfg"
kind = "link"
section = "term"
desc = "a config folder"
link = "~/.config/fx/cfg"
to = "cfg"

[[item]]
id = "mine"
kind = "link"
section = "term"
desc = "a folder you may have your own of"
link = "~/.config/fx/mine"
to = "mine"

[[item]]
id = "tool"
kind = "script"
section = "tools"
desc = "a script item"
check = ["sh", "{{repo}}/check.sh"]
plan = ["sh", "{{repo}}/check.sh", "--plan"]
apply = ["sh", "-c", "touch \"$HOME/.fx-done\""]

[[item]]
id = "ask"
kind = "script"
section = "tools"
desc = "a script that asks its own questions"
interactive = true
check = ["sh", "-c", "echo 'not set up'; exit 1"]
apply = ["sh", "-c", "true"]

[[item]]
id = "skills"
kind = "skills"
section = "agents"
desc = "agent skills"
needs = ["brew:node"]
sources = ["skills"]
home = "~/.agents/skills"
claude_links = "~/.claude/skills"
installer = ["npx", "-y", "skills@1.7.4", "add"]
env = { DISABLE_TELEMETRY = "1" }
agents = ["codex"]
protect = ["~/.claude/skills/synced"]
validator = ["uvx", "validate"]

[[item]]
id = "rules"
kind = "rules"
section = "agents"
desc = "one rules file"
source = "AGENTS.md"
targets = [
  { tool = "claude", method = "import", file = "~/.claude/CLAUDE.md", start = "<!-- >>> fx >>> -->", end = "<!-- <<< fx <<< -->", when = { dir = "~/.claude" } },
  { tool = "codex", method = "link", file = "~/.codex/AGENTS.md", when = { dir = "~/.codex" } },
  { tool = "gone", method = "link", file = "~/.gone/AGENTS.md", when = { dir = "~/.gone" } },
]

[[item]]
id = "later"
kind = "script"
section = "tools"
desc = "an optional item"
group = "extra"
check = ["sh", "-c", "exit 1"]
apply = ["sh", "-c", "true"]

[aliases]
repo_file = "rc"
local_file = "~/.zshrc.local"
header = "# aliases added with dott"
before = "^# machine-specific"
notes = "notes.tsv"
search = ["rc", "~/.zshrc.local", "~/.zshrc"]

[[edit]]
name = "rc"
file = "rc"
desc = "the shell file"
`

var repoFiles = map[string]string{
	"Brewfile":              "# core\nbrew \"node\"   # runs npx\nbrew \"jq\"     # json\n\n# apps\ncask \"fxapp\"   # an app\n",
	"rc":                    "# the repo's rc\nalias ll='ls -l'\n\n# aliases added with dott\nalias cl='clear'\n\n# machine-specific config\n[ -f ~/.zshrc.local ] && source ~/.zshrc.local\n",
	"notes.tsv":             "# notes\n",
	"gitconfig":             "[core]\n\tpager = less\n",
	"ignore":                "# ignored everywhere\n.DS_Store\n*.swp\n",
	"themes/one":            "palette = 1\n",
	"themes/two":            "palette = 2\n",
	"cfg/init.conf":         "x = 1\n",
	"mine/init.conf":        "y = 1\n",
	"check.sh":              "if [ \"$1\" = --plan ]; then echo 'create ~/.fx-done'; exit 0; fi\nif [ -f \"$HOME/.fx-done\" ]; then echo done; exit 0; fi\necho 'not done'; echo 'run it'; exit 1\n",
	"AGENTS.md":             "# rules\n- be brief\n",
	"skills/hello/SKILL.md": "---\nname: hello\ndescription: says hello\n---\n\n# hello\n",
	"skills/bye/SKILL.md":   "---\nname: bye\ndescription: says bye\n---\n\n# bye\n",
}

// stubs log "name args..." to $DOTT_STUB_LOG, and fake brew's and npx's work in the fake HOME.
var stubs = map[string]string{
	"brew": `#!/bin/sh
echo "brew $*" >> "$DOTT_STUB_LOG"
db="$DOTT_STUB_DIR/brew-installed"
touch "$db"
case "$1 $2" in
  "list --formula") [ "$3" = --versions ] && awk '$1 != "cask" { print $1 " 1.0" }' "$db"; exit 0 ;;
  "list --cask") awk '$1 == "cask" { print $2 }' "$db"; exit 0 ;;
  "install --formula") shift 2; for p in "$@"; do echo "${p##*/}" >> "$db"; done; exit 0 ;;
  "install --cask") shift 3; for p in "$@"; do echo "cask ${p##*/}" >> "$db"; done; exit 0 ;;
  "trust "*) exit 0 ;;
esac
exit 0
`,
	"npx": `#!/bin/sh
echo "npx $*" >> "$DOTT_STUB_LOG"
[ "$DISABLE_TELEMETRY" = 1 ] || { echo "telemetry is on" >&2; exit 9; }
shift 2   # -y skills@x
[ "$1" = add ] || exit 2
src="$2"; shift 2
names=""; mode=""
for a in "$@"; do
  case "$a" in -s) mode=s ;; -a|-g|-y) mode="" ;; *) [ "$mode" = s ] && names="$names $a" ;; esac
done
mkdir -p "$HOME/.agents/skills" "$HOME/.claude/skills"
for n in $names; do
  rm -rf "$HOME/.agents/skills/$n" "$HOME/.claude/skills/$n"
  cp -R "$src/$n" "$HOME/.agents/skills/$n"
  ln -s "$HOME/.agents/skills/$n" "$HOME/.claude/skills/$n"
done
`,
	"uvx": `#!/bin/sh
echo "uvx $*" >> "$DOTT_STUB_LOG"
exit 0
`,
}

// New builds a fixture under t.TempDir(). The fake HOME has ~/.claude and ~/.codex, so
// those rules targets apply, and nothing else.
func New(t testing.TB) *Fixture {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	f := &Fixture{Root: root, Home: filepath.Join(root, "home"), Repo: filepath.Join(root, "repo"), Bin: filepath.Join(root, "bin"), Log: filepath.Join(root, "stub.log")}
	for _, d := range []string{f.Home, f.Repo, f.Bin, filepath.Join(f.Home, ".claude"), filepath.Join(f.Home, ".codex")} {
		must(t, os.MkdirAll(d, 0o755))
	}
	f.Write(t, "repo/dott.toml", Manifest)
	for p, s := range repoFiles {
		f.Write(t, "repo/"+p, s)
	}
	for name, s := range stubs {
		p := filepath.Join(f.Bin, name)
		must(t, os.WriteFile(p, []byte(s), 0o755))
	}
	must(t, os.WriteFile(f.Log, nil, 0o644))
	return f
}

// Write writes a file under the fixture root, making folders.
func (f *Fixture) Write(t testing.TB, rel, data string) {
	t.Helper()
	p := filepath.Join(f.Root, rel)
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(data), 0o644))
}

// Path is a path under the fixture root.
func (f *Fixture) Path(rel string) string { return filepath.Join(f.Root, rel) }

// Getenv is the fake machine's environment.
func (f *Fixture) Getenv(k string) string {
	switch k {
	case "HOME":
		return f.Home
	case "DOTT_STUB_LOG":
		return f.Log
	case "DOTT_STUB_DIR":
		return f.Root
	case "DOTT_REPO":
		return f.Repo
	case "PATH":
		return f.Bin + ":/usr/bin:/bin:/usr/sbin:/sbin"
	}
	return ""
}

// Environ is the environment for running dott (or anything) on the fake machine.
func (f *Fixture) Environ(extra ...string) []string {
	e := []string{
		"HOME=" + f.Home, "PATH=" + f.Getenv("PATH"), "DOTT_STUB_LOG=" + f.Log, "DOTT_STUB_DIR=" + f.Root,
		"DOTT_REPO=" + f.Repo, "TERM=xterm-256color", "LANG=en_US.UTF-8",
		"XDG_CONFIG_HOME=", "XDG_STATE_HOME=", "XDG_CACHE_HOME=", "XDG_DATA_HOME=",
	}
	for _, k := range []string{"GOMODCACHE", "GOCACHE", "GOPATH", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			e = append(e, k+"="+v)
		}
	}
	return append(e, extra...)
}

// Use points this test process at the fake machine: HOME, PATH and the stub log. It is for
// in-process tests of the kinds, which look commands up on PATH.
func (f *Fixture) Use(t *testing.T) {
	for _, kv := range f.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if k != "DOTT_REPO" {
			t.Setenv(k, v)
		}
	}
}

// Env is the fake machine as dott sees it.
func (f *Fixture) Env() *env.Env {
	return &env.Env{Home: f.Home, OS: "darwin", CLT: true, AppDirs: []string{filepath.Join(f.Root, "Applications")}, Getenv: f.Getenv}
}

// Ctx loads the fixture repo with this config.
func (f *Fixture) Ctx(t testing.TB, cfg *config.Config) *engine.Ctx {
	t.Helper()
	m, err := manifest.Load(f.Repo)
	must(t, err)
	if cfg == nil {
		cfg = &config.Config{Groups: map[string]bool{"extra": false}}
	}
	e := f.Env()
	return &engine.Ctx{Env: e, M: m, Cfg: cfg, CfgAt: filepath.Join(f.Home, ".config", "dott", "config.toml"),
		St: state.State{Dir: filepath.Join(f.Home, ".local", "state", "dott")}, Kinds: kinds.All(), Out: os.Stderr}
}

// Calls are the stub calls logged so far.
func (f *Fixture) Calls(t testing.TB) []string {
	t.Helper()
	b, err := os.ReadFile(f.Log)
	must(t, err)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), " ", "_"))
}

// HashTree is one hash of everything under dir: each path, its type, mode, link target and
// contents. Two equal hashes mean nothing under dir changed.
func HashTree(t testing.TB, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s %v", rel, fi.Mode())
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			line += " -> " + l
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			line += " " + hex.EncodeToString(h[:])
		}
		lines = append(lines, line)
		return nil
	})
	must(t, err)
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// Git runs git in dir for a test, with a fixed identity and no signing.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
