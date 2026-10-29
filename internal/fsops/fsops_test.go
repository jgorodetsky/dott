package fsops

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupRestore(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	link := filepath.Join(dir, "l")
	folder := filepath.Join(dir, "d")
	fresh := filepath.Join(dir, "new")
	os.WriteFile(file, []byte("old"), 0o600)
	os.Symlink("/somewhere", link)
	os.MkdirAll(filepath.Join(folder, "sub"), 0o755)
	os.WriteFile(filepath.Join(folder, "sub", "x"), []byte("x"), 0o644)

	b := NewBackup(filepath.Join(dir, "backups"), "t")
	for _, p := range []string{file, link, folder, fresh, file} {
		if err := b.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.Entries) != 4 {
		t.Fatalf("a path saved twice counts once: %d entries", len(b.Entries))
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	WriteFile(file, []byte("new"), 0o644)
	os.Remove(link)
	os.Symlink("/elsewhere", link)
	os.RemoveAll(folder)
	os.WriteFile(fresh, []byte("made by the fix"), 0o644)

	if got := Backups(filepath.Join(dir, "backups")); len(got) != 1 || got[0] != b.Dir {
		t.Fatalf("Backups = %v", got)
	}
	if err := Restore(b.Dir); err != nil {
		t.Fatal(err)
	}
	if s, _ := os.ReadFile(file); string(s) != "old" {
		t.Fatalf("file = %q", s)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	if l, _ := os.Readlink(link); l != "/somewhere" {
		t.Fatalf("link = %q", l)
	}
	if s, _ := os.ReadFile(filepath.Join(folder, "sub", "x")); string(s) != "x" {
		t.Fatal("folder not restored")
	}
	if _, err := os.Lstat(fresh); !os.IsNotExist(err) {
		t.Fatal("a path the fix created is removed on restore")
	}
}

func TestEmptyBackupLeavesNoFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "b")
	b := NewBackup(root, "x")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("an empty backup made a folder")
	}
}

func TestWriteFileFollowsLinksAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	os.WriteFile(real, []byte("a"), 0o640)
	link := filepath.Join(dir, "link")
	os.Symlink(real, link)
	if err := WriteFile(link, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if s, _ := os.ReadFile(real); string(s) != "b" {
		t.Fatalf("real = %q", s)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode())
	}
}

func TestSymlinkRefusesRealFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	os.WriteFile(p, []byte("mine"), 0o644)
	if err := Symlink("/x", p); err == nil {
		t.Fatal("Symlink replaced a real file")
	}
}

// TestBackupsNewestFirst: two backups made in the same second sort by when they were made,
// not by name.
func TestBackupsNewestFirst(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	for i, name := range []string{"20261028-231504-skills", "20261028-231504-themes"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, "backup.json")
		if err := os.WriteFile(f, []byte("[]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// skills was made last
		at := now.Add(-time.Duration(i) * 300 * time.Millisecond)
		if err := os.Chtimes(f, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := filepath.Base(Backups(root)[0]); got != "20261028-231504-skills" {
		t.Fatalf("newest is %s", got)
	}
}
