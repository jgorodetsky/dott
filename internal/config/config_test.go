package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dott", "config.toml")
	c, err := Load(p)
	if err != nil || c.Groups == nil {
		t.Fatalf("a missing file is an empty config: %v", err)
	}
	c.Repo = "~/dotfiles"
	c.Groups["aws"] = false
	c.SetOff("nvim", true)
	c.SetOff("rules/codex", true)
	c.SetOff("nvim", false)
	c.Local.Skills = []string{"~/own"}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "~/dotfiles" || got.Groups["aws"] || len(got.Groups) != 1 || !got.IsOff("rules/codex") || got.IsOff("nvim") || got.Local.Skills[0] != "~/own" {
		t.Fatalf("%+v", got)
	}
}

func TestUnknownKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("repo = \"x\"\nrepoo = \"y\"\n"), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "repoo") {
		t.Fatalf("want an unknown key error, got %v", err)
	}
}
