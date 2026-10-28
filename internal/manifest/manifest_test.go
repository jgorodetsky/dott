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
		ok + "colour = 1\n":                         "unknown keys",
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
