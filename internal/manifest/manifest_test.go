package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, s string) error {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte(s), 0o644)
	_, err := Load(dir)
	return err
}

func TestLoadErrors(t *testing.T) {
	ok := "schema = 1\n[[item]]\nid = \"a\"\nkind = \"link\"\nlink = \"~/a\"\nto = \"a\"\n"
	if err := load(t, ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"schema = 2\n":                              "schema = 2",
		ok + "colour = 1\n":                         "may need a newer dott",
		ok + "undo = [\"x\"]\n":                     "undo is only for script items",
		strings.Replace(ok, `"a"`, `"A b"`, 1):      "id has to be",
		ok + strings.TrimPrefix(ok, "schema = 1\n"): "id used twice",
		strings.Replace(ok, `"link"`, `"lnk"`, 1):   "known kinds",
		strings.Replace(ok, "to = \"a\"\n", "", 1):  "needs to",
		ok + "group = \"g\"\n":                      "not in [groups]",
		ok + "needs = [\"zz\"]\n":                   "not an item",
		"schema = 1\n[[item]]\nid = \"s\"\nkind = \"script\"\ncheck = [\"true\"]\n":                                                     "needs apply",
		"schema = 1\n[[item]]\nid = \"b\"\nkind = \"block\"\nfile = \"f\"\nstart = \"s\"\nend = \"e\"\nbody = \"b\"\nmode = \"once\"\n": "use seed",
	}
	for in, want := range cases {
		if err := load(t, in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v\n%s", want, err, in)
		}
	}
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no dott.toml") {
		t.Errorf("missing manifest: %v", err)
	}
}

func TestRepoAndTemplate(t *testing.T) {
	m := &Manifest{Dir: "/r"}
	for in, want := range map[string]string{"a/b": "/r/a/b", "/abs": "/abs", "~/x": "~/x", "@name": "@name", "${X}": "${X}"} {
		if got := m.Repo(in); got != want {
			t.Errorf("Repo(%q) = %q", in, got)
		}
	}
	if got := m.Template("{{repo}}/x {{source}}", "/s"); got != "/r/x /s" {
		t.Errorf("Template = %q", got)
	}
}

func TestMergeLocal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte("schema = 1\n[[item]]\nid = \"a\"\nkind = \"link\"\nlink = \"~/a\"\nto = \"a\"\n"), 0o644)
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	lp := filepath.Join(t.TempDir(), LocalName)
	if err := m.MergeLocal(lp, "/files"); err != nil || len(m.Items) != 1 {
		t.Fatalf("a missing local.toml: %v, %d items", err, len(m.Items))
	}
	os.WriteFile(lp, []byte("[[item]]\nid = \"b\"\nkind = \"link\"\nlink = \"~/b\"\nto = \"{{local}}/b\"\n"), 0o644)
	if err := m.MergeLocal(lp, "/files"); err != nil {
		t.Fatal(err)
	}
	b, _ := m.Item("b")
	if b.Origin != "local" || m.File(b) != lp || m.Template(b.To, "") != "/files/b" {
		t.Fatalf("b = %+v, file %s", b, m.File(b))
	}
	os.WriteFile(lp, []byte("[[item]]\nid = \"a\"\nkind = \"link\"\nlink = \"~/x\"\nto = \"x\"\n"), 0o644)
	m2, _ := Load(dir)
	if err := m2.MergeLocal(lp, "/files"); err == nil || !strings.Contains(err.Error(), "also in the repo") {
		t.Fatalf("an id in both: %v", err)
	}
}

// TestActions: a script item's own rows. Each needs a name, a run and a known tier; {{part}}
// only makes sense in an action for each part; other kinds have no actions.
func TestActions(t *testing.T) {
	script := "schema = 1\n[[item]]\nid = \"s\"\nkind = \"script\"\ncheck = [\"true\"]\napply = [\"true\"]\n"
	good := script + "[[item.action]]\nname = \"turn on {{part}}\"\npart = true\nstates = [\"off\"]\ntier = \"change\"\nrun = [\"x\", \"{{part}}\"]\n"
	if err := load(t, good); err != nil {
		t.Fatal(err)
	}
	link := "schema = 1\n[[item]]\nid = \"a\"\nkind = \"link\"\nlink = \"~/a\"\nto = \"a\"\n"
	cases := map[string]string{
		link + "[[item.action]]\nname = \"n\"\ntier = \"look\"\nrun = [\"x\"]\n":                                   "actions are only for script items",
		script + "[[item.action]]\ntier = \"look\"\nrun = [\"x\"]\n":                                               "needs a name",
		script + "[[item.action]]\nname = \"n\"\ntier = \"look\"\n":                                                "needs run",
		script + "[[item.action]]\nname = \"n\"\ntier = \"go\"\nrun = [\"x\"]\n":                                   "tier",
		script + "[[item.action]]\nname = \"n\"\ntier = \"look\"\nrun = [\"{{part}}\"]\n":                          "{{part}} needs part = true",
		script + "[[item.action]]\nname = \"n\"\npart = true\nstates = [\"on\"]\ntier = \"look\"\nrun = [\"x\"]\n": "states",
		script + "[[item.action]]\nname = \"n\"\ntier = \"look\"\nrun = [\"x\"]\ncolour = 1\n":                     "may need a newer dott",
	}
	for in, want := range cases {
		if err := load(t, in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v\n%s", want, err, in)
		}
	}
}
