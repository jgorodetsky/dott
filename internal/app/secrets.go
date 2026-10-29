package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The guard for files that go in the repo, which may be public. It is a guard, not a
// scanner: it stops the files that hold secrets by their nature, and asks for a typed yes
// when a file has a line that looks like one.

// secretPaths are files that hold secrets by their nature, relative to HOME. They can only be
// tracked for this Mac.
var secretPaths = []string{
	".ssh", ".gnupg", ".aws", ".kube/config", ".docker/config.json", ".netrc", ".npmrc", ".pypirc",
	".git-credentials", ".config/gh/hosts.yml", ".config/op", ".terraform.d/credentials.tfrc.json",
	"Library/Keychains",
}

var secretNames = regexp.MustCompile(`(?i)(\.pem|\.key|\.p12|\.pfx|\.keychain(-db)?|^id_[a-z0-9]+(\.pub)?|^\.env(\..*)?|credentials?(\.json)?)$`)

// secretPath says why a path may only be tracked for this Mac, or "" when it may go in the repo.
func secretPath(home, p string) string {
	rel, err := filepath.Rel(home, p)
	if err == nil && !strings.HasPrefix(rel, "..") {
		for _, s := range secretPaths {
			if rel == s || strings.HasPrefix(rel, s+"/") {
				return "~/" + s + " holds secrets"
			}
		}
	}
	if secretNames.MatchString(filepath.Base(p)) {
		return filepath.Base(p) + " is a kind of file that holds secrets"
	}
	return ""
}

// finding is one line that looks like a secret. The value itself is never shown.
type finding struct {
	File string
	Line int
	What string
}

var secretLines = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"an AWS access key id", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"a GitHub token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"a password, token or key with a value", regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)\b["']?\s*[:=]\s*["']?[^\s"'$]{6,}`)},
}

var longToken = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)

// scanSecrets looks at every text file under p (a file or a folder) for lines that look like
// secrets. Binary files and files over 1 MB are skipped.
func scanSecrets(p string) []finding {
	var out []finding
	_ = filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if fi, err := d.Info(); err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
			return nil
		}
		data, err := os.ReadFile(f)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for n := 1; sc.Scan(); n++ {
			if w := secretLine(sc.Text()); w != "" {
				out = append(out, finding{f, n, w})
			}
		}
		return nil
	})
	return out
}

func secretLine(l string) string {
	for _, s := range secretLines {
		if s.re.MatchString(l) {
			return s.what
		}
	}
	for _, t := range longToken.FindAllString(l, -1) {
		if entropy(t) > 4.2 {
			return "a long random-looking string"
		}
	}
	return ""
}

// entropy is the Shannon entropy of s in bits per character; random keys are above 4.
func entropy(s string) float64 {
	n := map[rune]float64{}
	for _, r := range s {
		n[r]++
	}
	var e float64
	l := float64(len([]rune(s)))
	for _, c := range n {
		p := c / l
		e -= p * math.Log2(p)
	}
	return e
}

func (f finding) String() string { return fmt.Sprintf("line %d: %s", f.Line, f.What) }
