// Package kinds holds the item kinds a manifest can use. Each one checks its item without
// changing anything and plans the steps that fix it.
package kinds

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
)

// All is every kind, by the name the manifest uses.
func All() map[string]engine.Kind {
	return map[string]engine.Kind{
		"link":         Link{},
		"block":        Block{},
		"include-line": IncludeLine{},
		"copy":         Copy{},
		"script":       Script{},
		"brew-bundle":  Brew{},
		"skills":       Skills{},
		"rules":        Rules{},
	}
}

func status(st engine.State, detail, fix string) engine.Status {
	return engine.Status{State: st, Detail: detail, Fix: fix}
}

func part(id, name string, st engine.State, detail, fix string) engine.Status {
	return engine.Status{ID: id + "/" + name, State: st, Detail: detail, Fix: fix}
}

// partName is the text after the item id in a part id.
func partName(s engine.Status) string { return s.ID[strings.LastIndex(s.ID, "/")+1:] }

// otherCheckout says which other copy of the same repo p points into: p ends in rel, and
// what comes before rel is a folder with a dott.toml (or the old installer) that isn't this repo.
func otherCheckout(c *engine.Ctx, p, rel string) string {
	p = filepath.Clean(c.Env.Expand(p))
	rel = filepath.Clean(rel)
	root, ok := strings.CutSuffix(p, string(filepath.Separator)+rel)
	if !ok || root == filepath.Clean(c.M.Dir) || root == "" {
		return ""
	}
	for _, f := range []string{"dott.toml", "install.sh"} {
		if exists(filepath.Join(root, f)) {
			return root
		}
	}
	return ""
}

// readLines reads a file as lines. A missing file is no lines and not an error.
func readLines(p string) ([]string, bool, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	s := string(b)
	if s == "" {
		return nil, true, nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), true, nil
}

// codeLines are the lines that are not blank and not # comments.
func codeLines(p string) ([]string, error) {
	ls, ok, err := readLines(p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no %s", p)
	}
	var out []string
	for _, l := range ls {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			out = append(out, l)
		}
	}
	return out, nil
}

// appendText adds text to the end of a file, after a newline if the file doesn't end in one.
func appendText(p, text string) error {
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return writeFile(p, append(b, text...))
}

func writeFile(p string, data []byte) error {
	return fsops.WriteFile(p, data, 0o644)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func fileSHA(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return sha(b)
}

// blobID is the id git gives a file's contents, so dott can ask whether the repo ever had them.
func blobID(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// pastBlobs lists every version of every file under rel that the repo ever committed, as
// path -> blob ids. It needs git; without it the answer is "don't know" (nil, false).
func pastBlobs(c *engine.Ctx, rel string) (map[string]map[string]bool, bool) {
	if !c.Env.Has("git") {
		return nil, false
	}
	v := c.Shared("blobs:"+rel, func() any {
		out, code, err := c.Run(nil, []string{"git", "--no-optional-locks", "-C", c.M.Dir, "log", "--format=", "--raw", "--no-abbrev", "--no-renames", "--", rel}, 10*time.Second)
		if err != nil || code != 0 {
			return map[string]map[string]bool(nil)
		}
		m := map[string]map[string]bool{}
		for _, l := range strings.Split(out, "\n") {
			meta, path, ok := strings.Cut(l, "\t")
			f := strings.Fields(meta)
			if !ok || len(f) < 5 {
				continue
			}
			if m[path] == nil {
				m[path] = map[string]bool{}
			}
			m[path][f[3]] = true
		}
		return m
	}).(map[string]map[string]bool)
	return v, v != nil
}

// wasRepo says whether the file at p holds a version the repo once committed as rel.
func wasRepo(c *engine.Ctx, p, rel string) (yes, known bool) {
	blobs, ok := pastBlobs(c, rel)
	if !ok {
		return false, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false, true
	}
	return blobs[filepath.ToSlash(rel)][blobID(b)], true
}

// treeHash is the old installer's hash of a skill folder: for each regular file (not
// .dotfiles, .DS_Store or __pycache__), sorted by path, "<sha256>  <path>\n", hashed again,
// first 16 hex characters. "-" when the folder doesn't exist.
func treeHash(dir string) string {
	files, err := treeFiles(dir)
	if err != nil {
		return "-"
	}
	var b bytes.Buffer
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return "-"
		}
		fmt.Fprintf(&b, "%s  %s\n", sha(data), f)
	}
	return sha(b.Bytes())[:16]
}

// treeFiles lists the regular files in dir the way the old installer's find did, following
// dir itself when it is a link and sorted byte by byte (LC_ALL=C).
func treeFiles(dir string) ([]string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	if !isDir(real) {
		return nil, fmt.Errorf("%s is not a folder", dir)
	}
	var out []string
	err = filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "__pycache__" {
			return fs.SkipDir
		}
		if !d.Type().IsRegular() || d.Name() == ".dotfiles" || d.Name() == ".DS_Store" {
			return nil
		}
		rel, _ := filepath.Rel(real, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out) // byte order, the same as LC_ALL=C sort
	return out, err
}
