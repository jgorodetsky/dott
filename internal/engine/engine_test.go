package engine_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/manifest"
	"github.com/jgorodetsky/dott/internal/testkit"
)

func TestRoll(t *testing.T) {
	parts := []engine.Status{
		{ID: "x/a", State: engine.OK}, {ID: "x/b", State: engine.Drift}, {ID: "x/c", State: engine.Conflict},
		{ID: "x/d", State: engine.Off}, {ID: "x/e", State: engine.Absent},
	}
	st, detail, fix := engine.Roll(parts)
	if st != engine.Conflict || detail != "1 conflict: c · 1 drift: b · 1 ok · 1 off · 1 absent" || fix == "" {
		t.Fatalf("%s %q %q", st, detail, fix)
	}
	st, _, _ = engine.Roll([]engine.Status{{ID: "x/a", State: engine.OK}, {ID: "x/b", State: engine.Absent}})
	if st != engine.OK {
		t.Fatalf("ok and absent roll up to ok, got %s", st)
	}
}

// A fix whose check doesn't come back ok is an error, and its backup puts things back.
func TestApplyVerifies(t *testing.T) {
	f := testkit.New(t)
	f.Use(t)
	c := f.Ctx(t, nil)
	f.Write(t, "home/.zshrc", "mine\n")
	it := &c.M.Items[1] // loader
	if it.ID != "loader" {
		t.Fatalf("fixture order changed: %s", it.ID)
	}
	p := f.Path("home/.zshrc")
	bad := []engine.Change{{Summary: "write the wrong thing", Paths: []string{p}, Do: func(*engine.Exec) error {
		return os.WriteFile(p, []byte("wrong\n"), 0o644)
	}}}
	res, err := engine.Apply(c, it, "", bad)
	if err == nil || !strings.Contains(err.Error(), "still drift") || res.Backup == "" {
		t.Fatalf("err %v, backup %q", err, res.Backup)
	}
	if err := fsops.Restore(res.Backup); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine\n" {
		t.Fatalf("after restore: %q", b)
	}
	failing := []engine.Change{{Summary: "fail", Paths: []string{p}, Do: func(*engine.Exec) error { return errors.New("boom") }}}
	if _, err := engine.Apply(c, it, "", failing); err == nil || !strings.Contains(err.Error(), "fail: boom") {
		t.Fatalf("err %v", err)
	}
}

func TestLockBlocksASecondFix(t *testing.T) {
	f := testkit.New(t)
	f.Use(t)
	c := f.Ctx(t, nil)
	unlock, err := c.St.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := engine.Apply(c, &c.M.Items[1], "", nil); err == nil || !strings.Contains(err.Error(), "another dott") {
		t.Fatalf("err %v", err)
	}
}

// TestApplySkipsNoteSteps: a script whose plan prints several lines gets one step per line,
// and only the last one runs something. The others are notes; Apply must not call them.
func TestApplySkipsNoteSteps(t *testing.T) {
	f := testkit.New(t)
	f.Use(t)
	c := f.Ctx(t, nil)
	var it *manifest.Item
	for i := range c.M.Items {
		if c.M.Items[i].ID == "tool" {
			it = &c.M.Items[i]
		}
	}
	done := f.Path("home/.fx-done")
	steps := []engine.Change{
		{Summary: "build it first"},
		{Summary: "create ~/.fx-done", Do: func(*engine.Exec) error { return os.WriteFile(done, nil, 0o644) }},
	}
	if _, err := engine.Apply(c, it, "", steps); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(done); err != nil {
		t.Fatal("the step that runs did not run")
	}
}
