package kinds

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The skill hash has to match the old installer's sk_hash byte for byte, so the records it
// left (~/.cache/dotfiles/skills.sync) still tell your edits from updates.
func TestTreeHashMatchesOldInstaller(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"SKILL.md":              "---\nname: x\n---\n",
		"b/a.md":                "a\n",
		"B.md":                  "upper sorts first\n",
		"refs/z z.md":           "a space\n",
		".dotfiles":             "left out\n",
		".DS_Store":             "left out\n",
		"kit/__pycache__/m.pyc": "left out\n",
		"kit/run.py":            "print(1)\n",
	}
	for p, s := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	os.Symlink("SKILL.md", filepath.Join(dir, "link.md")) // find -type f leaves links out
	old := `sk_files(){ ( cd "$1" && find . -type f ! -name .dotfiles ! -name .DS_Store ! -path '*/__pycache__/*' | sed 's|^\./||' | LC_ALL=C sort ); }
sk_hash(){ ( cd "$1" && sk_files . | while IFS= read -r f; do printf '%s  %s\n' "$(shasum -a 256 < "$f" | cut -c1-64)" "$f"; done ) | shasum -a 256 | cut -c1-16; }
sk_hash "$1"`
	if _, err := exec.LookPath("shasum"); err != nil {
		t.Skip("no shasum")
	}
	out, err := exec.Command("bash", "-c", old, "bash", dir).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := treeHash(dir), strings.TrimSpace(string(out)); got != want {
		t.Fatalf("treeHash = %s, the old sk_hash = %s", got, want)
	}
	if treeHash(filepath.Join(dir, "missing")) != "-" {
		t.Fatal("a missing folder hashes to -")
	}
}

func TestBlobIDMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("hello\n"), 0o644)
	out, err := exec.Command("git", "hash-object", p).Output()
	if err != nil {
		t.Skip("git hash-object: ", err)
	}
	if got := blobID([]byte("hello\n")); got != strings.TrimSpace(string(out)) {
		t.Fatalf("blobID = %s, git says %s", got, out)
	}
}
