package picker

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/rows"
)

type searchCase struct {
	query, want string
	rank        int
	line        int
}

// tests/search.tsv: what the user types, the row it must find, and the worst rank allowed.
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
			t.Fatalf("search.tsv:%d: rank %q: %v", n, p[2], err)
		}
		cs = append(cs, searchCase{p[0], p[1], rank, n})
	}
	return cs
}

func backends(t *testing.T) map[string]Backend {
	b := map[string]Backend{"library": Lib{}}
	if path := os.Getenv("DOTT_TEST_FZF"); path != "" {
		b["binary"] = Exec{Path: path}
	} else {
		t.Log("DOTT_TEST_FZF not set: the fzf binary is not checked (scripts/check.sh sets it)")
	}
	return b
}

// TestSearchRanking runs every case through fzf's filter mode, the same matching as the picker.
// Each backend runs all cases in one process, which also checks that fzf can run many times.
func TestSearchRanking(t *testing.T) {
	lines := rows.Lines(rows.Demo(), 100)
	for name, b := range backends(t) {
		t.Run(name, func(t *testing.T) {
			for _, c := range loadCases(t) {
				got, err := b.Filter(lines, c.query)
				if err != nil {
					t.Fatalf("search.tsv:%d %q: %v", c.line, c.query, err)
				}
				rank := 0
				var ids []string
				for i, l := range got {
					ids = append(ids, rows.ID(l))
					if rows.ID(l) == c.want && rank == 0 {
						rank = i + 1
					}
				}
				if rank == 0 || rank > c.rank {
					t.Errorf("search.tsv:%d %q: %s at rank %d, want %d or better; got %v", c.line, c.query, c.want, rank, c.rank, ids)
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
	lines := rows.Lines(rows.Demo(), 100)
	for _, c := range loadCases(t) {
		a, err1 := Lib{}.Filter(lines, c.query)
		b, err2 := Exec{Path: path}.Filter(lines, c.query)
		if err1 != nil || err2 != nil {
			t.Fatalf("%q: %v / %v", c.query, err1, err2)
		}
		if strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Errorf("%q: library and binary differ\nlibrary: %v\nbinary:  %v", c.query, ids(a), ids(b))
		}
	}
}

func ids(lines []string) []string {
	var out []string
	for _, l := range lines {
		out = append(out, rows.ID(l))
	}
	return out
}

func TestExecBadPath(t *testing.T) {
	if _, err := (Exec{Path: "/nonexistent/fzf"}).Filter([]string{"a"}, "a"); err == nil {
		t.Fatal("want an error for a missing fzf binary")
	}
}
