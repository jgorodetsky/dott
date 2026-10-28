package picker

import "testing"

// The ranking cases (tests/search.tsv) run in internal/app, on rows from a real scan.

func TestExecBadPath(t *testing.T) {
	if _, err := (Exec{Path: "/nonexistent/fzf"}).Filter([]string{"a"}, "a"); err == nil {
		t.Fatal("want an error for a missing fzf binary")
	}
}
