package app

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/gitx"
	"github.com/jgorodetsky/dott/internal/picker"
	"github.com/jgorodetsky/dott/internal/rows"
	"github.com/jgorodetsky/dott/internal/testkit"
)

// fixtureApp is the app on a fresh fake machine, with your own ~/.config/fx/mine in the way.
func fixtureApp(t *testing.T) (*testkit.Fixture, *App) {
	t.Helper()
	f := testkit.New(t)
	f.Use(t)
	f.Write(t, "home/.config/fx/mine/own.conf", "mine\n")
	c := f.Ctx(t, &config.Config{Groups: map[string]bool{}})
	a := &App{Env: c.Env, Cfg: c.Cfg, M: c.M, C: c, Pk: picker.Lib{}, Out: io.Discard, Err: io.Discard}
	a.Scan()
	return f, a
}

type searchCase struct {
	query, want string
	rank, line  int
}

func loadCases(t *testing.T) []searchCase {
	f, err := os.Open("../../tests/search.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cs []searchCase
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 3 {
			t.Fatalf("search.tsv:%d: want 3 tab-separated fields, got %d", n, len(p))
		}
		rank, err := strconv.Atoi(p[2])
		if err != nil {
			t.Fatalf("search.tsv:%d: %v", n, err)
		}
		cs = append(cs, searchCase{p[0], p[1], rank, n})
	}
	return cs
}

func backends(t *testing.T) map[string]picker.Backend {
	b := map[string]picker.Backend{"library": picker.Lib{}}
	if path := os.Getenv("DOTT_TEST_FZF"); path != "" {
		b["binary"] = picker.Exec{Path: path}
	} else {
		t.Log("DOTT_TEST_FZF not set: the fzf binary is not checked (scripts/check.sh sets it)")
	}
	return b
}

func ids(lines []string) []string {
	var out []string
	for _, l := range lines {
		out = append(out, rows.ID(l))
	}
	return out
}

// TestSearchRanking runs every case through fzf's filter mode, the same matching as the picker.
// A case first>second also opens the view behind first, which takes the query, and finds
// second there: that is what dott edit rc does.
func TestSearchRanking(t *testing.T) {
	_, a := fixtureApp(t)
	rs, acts := a.Build()
	lines := rows.Lines(rs, 100)
	rankOf := func(b picker.Backend, lines []string, query, want string) (int, []string) {
		got, err := b.Filter(lines, query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		for i, l := range got {
			if rows.ID(l) == want {
				return i + 1, got
			}
		}
		return 0, got
	}
	for name, b := range backends(t) {
		t.Run(name, func(t *testing.T) {
			for _, c := range loadCases(t) {
				first, second, deep := strings.Cut(c.want, ">")
				if rank, got := rankOf(b, lines, c.query, first); rank == 0 || rank > c.rank {
					t.Errorf("search.tsv:%d %q: %s at rank %d, want %d or better; got %v", c.line, c.query, first, rank, c.rank, ids(got))
					continue
				}
				if !deep {
					continue
				}
				act := acts[first]
				if act == nil || act.Open == nil || !act.Open(a).Forward {
					t.Errorf("search.tsv:%d: %s doesn't open a view that takes the query", c.line, first)
					continue
				}
				vrs, _ := act.Open(a).Rows(a)
				r, _ := findRow(rs, first)
				if rank, got := rankOf(b, rows.Lines(vrs, 100), forwardQuery(r.Command, c.query), second); rank != 1 {
					t.Errorf("search.tsv:%d %q: in %s, %s at rank %d, want 1; got %v", c.line, c.query, first, second, rank, ids(got))
				}
			}
		})
	}
}

// TestBackendsAgree checks that the built-in fzf and the binary rank every query the same.
func TestBackendsAgree(t *testing.T) {
	path := os.Getenv("DOTT_TEST_FZF")
	if path == "" {
		t.Skip("DOTT_TEST_FZF not set")
	}
	_, a := fixtureApp(t)
	rs, _ := a.Build()
	lines := rows.Lines(rs, 100)
	for _, c := range loadCases(t) {
		x, err1 := picker.Lib{}.Filter(lines, c.query)
		y, err2 := picker.Exec{Path: path}.Filter(lines, c.query)
		if err1 != nil || err2 != nil {
			t.Fatalf("%q: %v / %v", c.query, err1, err2)
		}
		if strings.Join(x, "\n") != strings.Join(y, "\n") {
			t.Errorf("%q: library and binary differ\nlibrary: %v\nbinary:  %v", c.query, ids(x), ids(y))
		}
	}
}

func TestEveryRowHasAnAction(t *testing.T) {
	_, a := fixtureApp(t)
	rs, acts := a.Build()
	seen := map[string]bool{}
	for _, r := range rs {
		if seen[r.ID] {
			t.Errorf("row id %s twice", r.ID)
		}
		seen[r.ID] = true
		if acts[r.ID] == nil {
			t.Errorf("row %s has no action", r.ID)
		}
	}
}

func TestFixAllPlanLeavesYoursAndQuestionsAlone(t *testing.T) {
	_, a := fixtureApp(t)
	p, err := a.fixAllPlan()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(p.Lines, "\n")
	for _, want := range []string{"pkgs: brew install --formula node", "loader: add a marked block", "skills: after pkgs/node: "} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q:\n%s", want, text)
		}
	}
	// what it leaves alone is not a step, so it has its own list and mark, and no copy text
	left := strings.Join(p.Left, "\n")
	for _, want := range []string{"mine is yours", "ask asks its own questions"} {
		if !strings.Contains(left, want) || strings.Contains(text, want) {
			t.Errorf("%q is not only in Left:\nlines:\n%s\nleft:\n%s", want, text, left)
		}
	}
	if strings.Contains(p.Shell, "mine") || !strings.Contains(p.Shell, "npx") {
		t.Errorf("copy text:\n%s", p.Shell)
	}
	if strings.Contains(text, "jq") || strings.Contains(text, "later:") {
		t.Errorf("the plan touches an optional group that is off:\n%s", text)
	}
	// pkgs comes before skills, which needs brew:node
	if strings.Index(text, "pkgs:") > strings.Index(text, "skills:") {
		t.Errorf("order:\n%s", text)
	}
}

// TestBlockedHintNamesARow: the next: hint of an item that waits for a brew package names the
// package's own row, which is under the brew item's id.
func TestBlockedHintNamesARow(t *testing.T) {
	_, a := fixtureApp(t)
	s, _ := a.status("skills")
	if s.Fix != "fix pkgs/node first" {
		t.Fatalf("hint %q", s.Fix)
	}
	rs, _ := a.Build()
	for _, r := range rs {
		if r.Command == "fix pkgs/node" {
			return
		}
	}
	t.Fatal("no fix pkgs/node row for the hint")
}

func TestFloatPutsPicksFirst(t *testing.T) {
	_, a := fixtureApp(t)
	a.C.St.Pick("refresh", timeNow())
	a.C.St.Pick("upgrade", timeNow())
	a.C.St.Pick("refresh", timeNow())
	rs, _ := a.Build()
	if rs[0].ID != "refresh" || rs[1].ID != "upgrade" {
		t.Fatalf("top rows %s %s", rs[0].ID, rs[1].ID)
	}
}

func TestInsertAlias(t *testing.T) {
	before := regexp.MustCompile("^# machine-specific")
	rc := "a\n# aliases added with dott\nalias cl='clear'\n\n# machine-specific config\nz\n"
	got := string(InsertAlias([]byte(rc), "gl", AliasLine("gl", "git log --oneline"), "# aliases added with dott", before, true, ""))
	if got != "a\n# aliases added with dott\nalias cl='clear'\nalias gl='git log --oneline'\n\n# machine-specific config\nz\n" {
		t.Fatalf("into the block: %q", got)
	}
	got = string(InsertAlias([]byte("a\n# machine-specific config\n"), "gl", "alias gl='x'", "# aliases added with dott", before, true, ""))
	if got != "a\n# aliases added with dott\nalias gl='x'\n\n# machine-specific config\n" {
		t.Fatalf("a new block: %q", got)
	}
	got = string(InsertAlias([]byte("alias gl='old'\n"), "gl", "alias gl='new'", "h", before, true, ""))
	if got != "alias gl='new'\n" {
		t.Fatalf("replace: %q", got)
	}
	got = string(InsertAlias(nil, "x", "alias x='y'", "h", before, false, "does y"))
	if got != "# x - does y\nalias x='y'\n" {
		t.Fatalf("local: %q", got)
	}
	if got := AliasLine("q", "echo 'hi'"); got != `alias q='echo '\''hi'\'''` {
		t.Fatalf("quoting: %s", got)
	}
	if got := string(RemoveAlias([]byte("a\n# x - does y\nalias x='y'\nb\n"), "x")); got != "a\nb\n" {
		t.Fatalf("remove: %q", got)
	}
}

func TestCommitOnlyHoldsOnlyTheChange(t *testing.T) {
	dir := t.TempDir()
	testkit.Git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "rc"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "other"), []byte("x\n"), 0o644)
	testkit.Git(t, dir, "add", ".")
	testkit.Git(t, dir, "commit", "-q", "-m", "first")
	// unrelated edits: a line in rc and a whole other file
	os.WriteFile(filepath.Join(dir, "rc"), []byte("zero\none\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "other"), []byte("changed\n"), 0o644)
	add := func(b []byte) ([]byte, error) { return append(b, []byte("alias gl='x'\n")...), nil }
	cur, _ := os.ReadFile(filepath.Join(dir, "rc"))
	next, _ := add(cur)
	os.WriteFile(filepath.Join(dir, "rc"), next, 0o644)
	for _, kv := range []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"} {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	if _, err := (gitx.Repo{Dir: dir}).CommitOnly("shell: alias gl", []gitx.Edit{{Path: "rc", Fn: add}}); err != nil {
		t.Fatal(err)
	}
	if got := testkit.Git(t, dir, "show", "HEAD:rc"); got != "one\nalias gl='x'" {
		t.Fatalf("committed rc = %q", got)
	}
	if got := testkit.Git(t, dir, "show", "--stat", "--format=%s", "HEAD"); !strings.Contains(got, "shell: alias gl") || strings.Contains(got, "other") {
		t.Fatalf("commit = %s", got)
	}
	// the unrelated edits are still there, unstaged
	if got := testkit.Git(t, dir, "diff", "--", "rc"); !strings.Contains(got, "+zero") || strings.Contains(got, "+alias gl") {
		t.Fatalf("rc's unstaged diff = %s", got)
	}
	if got := testkit.Git(t, dir, "diff", "--name-only"); got != "other\nrc" {
		t.Fatalf("unstaged files = %q", got)
	}
	if got := testkit.Git(t, dir, "diff", "--cached"); got != "" {
		t.Fatalf("the index has staged leftovers: %s", got)
	}
}

func TestParseOutdated(t *testing.T) {
	data := []byte(`{"formulae":[
	 {"name":"gh","installed_versions":["2.1.0"],"current_version":"2.2.0","pinned":false},
	 {"name":"go","installed_versions":["1.26.4"],"current_version":"2.0.0","pinned":false},
	 {"name":"hashicorp/tap/terraform","installed_versions":["1.9.0"],"current_version":"1.10.0","pinned":false},
	 {"name":"libdep","installed_versions":["1"],"current_version":"2","pinned":false},
	 {"name":"pinned","installed_versions":["1"],"current_version":"2","pinned":true},
	 {"name":"mytool","installed_versions":["1.0"],"current_version":"1.1","pinned":false}],
	 "casks":[{"name":"ghostty","installed_versions":["1.1"],"current_version":"1.2"}]}`)
	bf := map[string]bool{"gh": true, "go": true, "terraform": true, "ghostty": true, "pinned": true}
	ups := ParseOutdated(data, bf, map[string]bool{"mytool": true})
	var got []string
	for _, o := range ups {
		got = append(got, o.Short+":"+strconv.FormatBool(o.Careful))
	}
	if strings.Join(got, " ") != "gh:false go:true terraform:true ghostty:false mytool:false" {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(ups[4].Note, "not in the Brewfile") {
		t.Fatalf("%+v", ups[4])
	}
}

func TestParseDryRun(t *testing.T) {
	out := "==> Would upgrade 2 outdated packages:\ngh 2.1 -> 2.2\n==> Would upgrade 2 dependencies for gh:\nopenssl@3 3.1 -> 3.2\nca-certificates\n==> Would install 1 dependency for gh:\nnew/tap/thing\n"
	up, fresh := parseDryRun(out)
	if strings.Join(up, ",") != "openssl@3,ca-certificates" || strings.Join(fresh, ",") != "thing" {
		t.Fatalf("%v %v", up, fresh)
	}
}

func TestItemFiles(t *testing.T) {
	_, a := fixtureApp(t)
	got := map[string]string{}
	for _, it := range a.M.Items {
		got[it.ID] = strings.Join(itemFiles(a.M, it), ",")
	}
	want := map[string]string{"loader": "rc", "themes": "themes", "cfg": "cfg", "tool": "check.sh", "skills": "skills", "rules": "AGENTS.md", "gitinc": "gitconfig", "ignore": "ignore", "pkgs": "Brewfile"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	if !touches([]string{"themes"}, "themes/one") || touches([]string{"themes"}, "themesx") {
		t.Error("touches")
	}
}
