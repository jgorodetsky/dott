package kinds

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Copy copies a repo file, or every file in a repo folder, to a place on this Mac. A copy
// that differs is the repo's newer version when it matches something the repo (or dott)
// put there before; otherwise it is yours, and dott asks before replacing it.
type Copy struct{}

type copyFile struct {
	name, src, dst, rel string
}

func copyFiles(c *engine.Ctx, it *manifest.Item) ([]copyFile, bool, error) {
	from, to := c.Path(it.From), c.Path(it.To)
	fi, err := os.Stat(from)
	if err != nil {
		return nil, false, fmt.Errorf("the repo has no %s", it.From)
	}
	if !fi.IsDir() {
		return []copyFile{{filepath.Base(from), from, to, it.From}}, false, nil
	}
	var out []copyFile
	err = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() == ".DS_Store" {
			return err
		}
		r, _ := filepath.Rel(from, p)
		out = append(out, copyFile{filepath.ToSlash(r), p, filepath.Join(to, r), filepath.Join(it.From, r)})
		return nil
	})
	return out, true, err
}

func (Copy) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	files, dir, err := copyFiles(c, it)
	if err != nil {
		return status(engine.Blocked, err.Error(), "check the repo")
	}
	var parts []engine.Status
	for _, f := range files {
		st, detail, fix := copyState(c, f)
		parts = append(parts, part(it.ID, f.name, st, detail, fix))
	}
	if !dir {
		p := parts[0]
		return status(p.State, p.Detail, p.Fix)
	}
	s := engine.Status{Parts: parts}
	s.State, s.Detail, s.Fix = engine.Roll(parts)
	if s.State == engine.OK {
		s.Detail = fmt.Sprintf("%d files in %s match the repo", len(files), c.Env.Short(c.Path(it.To)))
	}
	return s
}

func copyState(c *engine.Ctx, f copyFile) (engine.State, string, string) {
	short := c.Env.Short(f.dst)
	src, err := os.ReadFile(f.src)
	if err != nil {
		return engine.Blocked, err.Error(), ""
	}
	dst, err := os.ReadFile(f.dst)
	switch {
	case os.IsNotExist(err):
		return engine.Drift, "not copied yet: " + short, "copy it"
	case err != nil:
		return engine.Blocked, "can't read " + short + ": " + err.Error(), ""
	case bytes.Equal(src, dst):
		return engine.OK, short, ""
	}
	if c.St.Records()["copy:"+f.dst] == sha(dst) {
		return engine.Drift, "the repo has a newer version of " + short, "copy it"
	}
	if yes, known := wasRepo(c, f.dst, f.rel); yes {
		return engine.Drift, "the repo has a newer version of " + short, "copy it"
	} else if !known {
		return engine.Conflict, short + " differs from the repo's (dott can't tell which is newer)", "keep yours, or replace it"
	}
	return engine.Conflict, short + " differs from the repo's, and you changed it", "keep yours, or replace it"
}

func (Copy) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, name string, replace bool) ([]engine.Change, error) {
	files, _, err := copyFiles(c, it)
	if err != nil {
		return nil, err
	}
	mode := fs.FileMode(0o644)
	if it.FileMode != "" {
		m, err := strconv.ParseUint(it.FileMode, 8, 32)
		if err != nil {
			return nil, fmt.Errorf("file_mode %q: %w", it.FileMode, err)
		}
		mode = fs.FileMode(m)
	}
	var out []engine.Change
	for _, f := range files {
		if name != "" && f.name != name {
			continue
		}
		st, _, _ := copyState(c, f)
		if st == engine.Conflict && !replace && name == "" {
			continue // fix everything leaves your own versions alone
		}
		if st == engine.Conflict && !replace {
			return nil, fmt.Errorf("%s is yours; pick replace to swap it for the repo's (yours goes to the backup)", c.Env.Short(f.dst))
		}
		if st != engine.Drift && st != engine.Conflict {
			continue
		}
		f := f
		out = append(out, engine.Change{
			Summary: "copy " + c.Rel(f.src) + " → " + c.Env.Short(f.dst),
			Shell:   engine.Quote("mkdir", "-p", filepath.Dir(f.dst)) + " && " + engine.Quote("cp", f.src, f.dst),
			Paths:   []string{f.dst},
			Danger:  st == engine.Conflict,
			Do: func(x *engine.Exec) error {
				if err := fsops.CopyFile(f.src, f.dst, mode); err != nil {
					return err
				}
				return x.C.St.SetRecord("copy:"+f.dst, fileSHA(f.src))
			},
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing to copy")
	}
	return out, nil
}
