package picker

import (
	"os"
	"path/filepath"
	"testing"
)

// The ranking cases (tests/search.tsv) run in internal/app, on rows from a real scan.

func TestExecBadPath(t *testing.T) {
	if _, err := (Exec{Path: "/nonexistent/fzf"}).Filter([]string{"a"}, "a"); err == nil {
		t.Fatal("want an error for a missing fzf binary")
	}
}

// An fzf that exits 2 (its error exit, as on a terminal that is gone) is an error, not an
// empty pick: a screen would otherwise open the next picker at once, forever.
func TestPickErrorExit(t *testing.T) {
	fzf := filepath.Join(t.TempDir(), "fzf")
	if err := os.WriteFile(fzf, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := (Exec{Path: fzf}).Pick(Request{Lines: []string{"a"}}); err == nil {
		t.Fatal("want an error for fzf's exit 2")
	}
}
