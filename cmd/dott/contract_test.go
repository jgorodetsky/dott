package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/jgorodetsky/dott/internal/testkit"
)

// TestContract runs a real dotfiles repo's manifest (DOTT_CONTRACT_REPO) on a fresh fake HOME:
// it has to load, every item has to get a state, no check may fail on its own, and the look
// must leave HOME as it was. CI runs it against github.com/jgorodetsky/dotfiles.
func TestContract(t *testing.T) {
	repo := os.Getenv("DOTT_CONTRACT_REPO")
	if repo == "" {
		t.Skip("DOTT_CONTRACT_REPO not set")
	}
	f := testkit.New(t)
	before := testkit.HashTree(t, f.Home)
	cmd := exec.Command(dottBin(t), "--json")
	cmd.Env = append(f.Environ(), "DOTT_REPO="+repo)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var r report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Items) == 0 {
		t.Fatal("no items")
	}
	for _, it := range r.Items {
		switch {
		case it.State == "":
			t.Errorf("%s has no state", it.ID)
		case strings.Contains(it.Detail, "the check failed") || strings.Contains(it.Detail, "took longer") || strings.Contains(it.Detail, "doesn't know kind"):
			t.Errorf("%s: %s", it.ID, it.Detail)
		}
		t.Logf("%-16s %-8s %s", it.ID, it.State, it.Detail)
	}
	if testkit.HashTree(t, f.Home) != before {
		t.Error("the look changed the fake HOME")
	}
	for _, c := range f.Calls(t) {
		if !strings.HasPrefix(c, "brew_list_") {
			t.Errorf("the look ran %s", c)
		}
	}
}
