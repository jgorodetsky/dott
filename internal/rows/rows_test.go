package rows

import (
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestLineFields(t *testing.T) {
	r := Row{ID: "fix-aws", Tier: MUT, Command: "fix <name>", Desc: "run\tawssetup\nnow", State: "drift", Section: "cloud"}
	line := Lines([]Row{r}, 80)[0]
	f := strings.Split(line, "\t")
	if len(f) != 9 {
		t.Fatalf("want 9 fields, got %d: %q", len(f), line)
	}
	want := []string{"fix-aws", "1", "◆", "fix <name>", "run awssetup now", "drift", "cloud", "fix <name>", MUT.Note()}
	for i, w := range want {
		if got := strings.TrimSpace(ansi.ReplaceAllString(f[i], "")); got != w {
			t.Errorf("field %d = %q, want %q", i+1, got, w)
		}
	}
	if !strings.Contains(f[3], "\x1b[22;3m<name>\x1b[23;1m") {
		t.Errorf("token not in italics: %q", f[3])
	}
	if ID(line) != "fix-aws" {
		t.Errorf("ID = %q", ID(line))
	}
}

func TestDetailOverridesCommand(t *testing.T) {
	f := strings.Split(Lines([]Row{{ID: "x", Command: "c", Detail: "d"}}, 80)[0], "\t")
	if f[7] != "d" {
		t.Fatalf("preview line 1 = %q, want d", f[7])
	}
}

func TestColumnsLineUp(t *testing.T) {
	ls := Lines([]Row{{ID: "a", Command: "short", Desc: "x"}, {ID: "b", Command: "a longer command", Desc: "y"}}, 100)
	w := func(l string) int { return len([]rune(ansi.ReplaceAllString(strings.Split(l, "\t")[3], ""))) }
	if w(ls[0]) != w(ls[1]) {
		t.Fatalf("command columns differ: %d vs %d", w(ls[0]), w(ls[1]))
	}
}

func TestDemoIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	var walk func([]Row)
	walk = func(rs []Row) {
		for _, r := range rs {
			if r.ID == "" || seen[r.ID] {
				t.Errorf("empty or repeated ID %q", r.ID)
			}
			seen[r.ID] = true
			walk(r.Next)
		}
	}
	walk(Demo())
}
