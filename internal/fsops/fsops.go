// Package fsops writes files safely: a backup of each path before it changes, atomic
// writes, and a restore that puts every backed-up path back as it was.
package fsops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry is one backed-up path.
type Entry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Link    string `json:"link,omitempty"` // it was a symlink to this
	Saved   string `json:"saved,omitempty"`
}

// Backup collects the paths one fix is about to change.
type Backup struct {
	Dir     string
	Entries []Entry
	seen    map[string]bool
}

// NewBackup returns a backup folder root/<time>-<id>. Nothing is created until a path is saved.
func NewBackup(root, id string) *Backup {
	return &Backup{Dir: filepath.Join(root, time.Now().Format("20060102-150405")+"-"+id), seen: map[string]bool{}}
}

// Save copies path into the backup before it changes. A path that doesn't exist is
// remembered too, so a restore removes what the fix created.
func (b *Backup) Save(path string) error {
	if b.seen[path] {
		return nil
	}
	b.seen[path] = true
	e := Entry{Path: path}
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		e.Existed = true
		if e.Link, err = os.Readlink(path); err != nil {
			return err
		}
	default:
		e.Existed = true
		e.Saved = filepath.Join("files", fmt.Sprintf("%d", len(b.Entries)), filepath.Base(path))
		if err := CopyTree(path, filepath.Join(b.Dir, e.Saved)); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	}
	b.Entries = append(b.Entries, e)
	return nil
}

// Close writes backup.json. A backup with nothing in it leaves no folder.
func (b *Backup) Close() error {
	if len(b.Entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b.Entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.Dir, "backup.json"), append(data, '\n'), 0o644)
}

// Restore puts every path in the backup folder dir back as it was before the fix.
func Restore(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "backup.json"))
	if err != nil {
		return err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if err := os.RemoveAll(e.Path); err != nil {
			return err
		}
		switch {
		case !e.Existed:
		case e.Link != "":
			if err := os.Symlink(e.Link, e.Path); err != nil {
				return err
			}
		default:
			if err := CopyTree(filepath.Join(dir, e.Saved), e.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// Backups lists backup folders under root, newest first.
func Backups(root string) []string {
	ents, _ := os.ReadDir(root)
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "backup.json")); err == nil {
				out = append(out, filepath.Join(root, e.Name()))
			}
		}
	}
	// by when each was made: names have whole seconds, and a fix makes several in one second
	made := func(dir string) time.Time {
		fi, err := os.Stat(filepath.Join(dir, "backup.json"))
		if err != nil {
			return time.Time{}
		}
		return fi.ModTime()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ti, tj := made(out[i]), made(out[j]); !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i] > out[j]
	})
	return out
}

// ReadEntries reads what a backup folder holds.
func ReadEntries(dir string) []Entry {
	var entries []Entry
	data, err := os.ReadFile(filepath.Join(dir, "backup.json"))
	if err == nil {
		_ = json.Unmarshal(data, &entries)
	}
	return entries
}

// WriteFile writes data with a temp file and a rename, so a crash never leaves half a file.
// An existing file keeps its permissions; a symlinked file is written where the link points.
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dott-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Symlink points link at target, replacing a link that is already there.
func Symlink(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("%s is a real file or folder, not a link", link)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.Symlink(target, link)
}

// CopyTree copies a file or a folder (recursively), keeping permissions and symlinks.
func CopyTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Symlink(t, dst)
	case fi.IsDir():
		if err := os.MkdirAll(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := CopyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return CopyFile(src, dst, fi.Mode().Perm())
	}
}

// CopyFile copies one file.
func CopyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
