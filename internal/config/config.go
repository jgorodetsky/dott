// Package config is this Mac's own settings: which dotfiles repo, which optional groups
// are on, what is turned off here, and things that exist only on this Mac. It lives in
// ~/.config/dott/config.toml and never goes in a repo.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
)

// Config is the file's contents.
type Config struct {
	Repo   string          `toml:"repo"`
	Groups map[string]bool `toml:"groups"` // optional groups: on, off; missing means not asked yet
	Off    []string        `toml:"off"`    // items (or item/part) turned off on this Mac
	Local  Local           `toml:"local"`
	Rows   []Row           `toml:"row"`
}

// Local lists things that exist only on this Mac.
type Local struct {
	Skills []string `toml:"skills"` // folders of skills, or one skill folder each
}

// Row is a custom picker row: a command of your own.
type Row struct {
	Name string   `toml:"name"`
	Desc string   `toml:"desc"`
	Run  []string `toml:"run"`
}

// Load reads the file. A missing file is an empty config, not an error.
func Load(path string) (*Config, error) {
	c := &Config{}
	md, err := toml.DecodeFile(path, c)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Groups: map[string]bool{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("%s: unknown key %s", path, u[0])
	}
	if c.Groups == nil {
		c.Groups = map[string]bool{}
	}
	return c, nil
}

// Save writes the file atomically: a temp file in the same folder, then a rename.
func (c *Config) Save(path string) error {
	var b bytes.Buffer
	b.WriteString("# dott's settings for this Mac only. Never commit this file.\n")
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// IsOff says whether an item, or one part of it ("rules/codex"), is turned off here.
func (c *Config) IsOff(id string) bool { return slices.Contains(c.Off, id) }

// SetOff turns an item or part off (or back on).
func (c *Config) SetOff(id string, off bool) {
	c.Off = slices.DeleteFunc(c.Off, func(s string) bool { return s == id })
	if off {
		c.Off = append(c.Off, id)
		slices.Sort(c.Off)
	}
}
