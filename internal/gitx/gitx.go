// Package gitx runs git for dott: read-only questions about the repo, and a commit that
// holds only the change dott made, even when other files (or other lines) are edited too.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Repo is one git checkout.
type Repo struct{ Dir string }

// Git runs git in the repo without taking optional locks, and returns stdout.
func (r Repo) Git(stdin []byte, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-C", r.Dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (r Repo) one(args ...string) (string, error) {
	out, err := r.Git(nil, nil, args...)
	return strings.TrimSpace(out), err
}

// Edit changes one file's contents. It runs on the version at HEAD for the commit, and on
// the file on disk for the working tree, so the commit never picks up other edits.
type Edit struct {
	Path string // relative to the repo
	Fn   func([]byte) ([]byte, error)
}

// CommitOnly commits the edits on top of HEAD and nothing else. The commit is signed when
// commit.gpgsign is on (the signing prompt may pop up). It never pushes.
func (r Repo) CommitOnly(msg string, edits []Edit) (string, error) {
	head, err := r.one("rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "dott-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := r.Git(nil, env, "read-tree", head); err != nil {
		return "", err
	}
	type staged struct{ path, mode, oldBlob, newBlob string }
	var done []staged
	for _, e := range edits {
		mode, oldBlob := "100644", ""
		if ls, _ := r.one("ls-tree", head, "--", e.Path); ls != "" {
			f := strings.Fields(ls)
			mode, oldBlob = f[0], f[2]
		}
		var old []byte
		if oldBlob != "" {
			s, err := r.Git(nil, nil, "cat-file", "blob", oldBlob)
			if err != nil {
				return "", err
			}
			old = []byte(s)
		}
		data, err := e.Fn(old)
		if err != nil {
			return "", fmt.Errorf("%s at HEAD: %w", e.Path, err)
		}
		blob, err := r.Git(data, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		blob = strings.TrimSpace(blob)
		if _, err := r.Git(nil, env, "update-index", "--add", "--cacheinfo", mode+","+blob+","+e.Path); err != nil {
			return "", err
		}
		done = append(done, staged{e.Path, mode, oldBlob, blob})
	}
	tree, err := r.Git(nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", strings.TrimSpace(tree), "-p", head, "-m", msg}
	if s, _ := r.one("config", "--bool", "commit.gpgsign"); s == "true" {
		args = append(args, "-S")
	}
	commit, err := r.Git(nil, nil, args...)
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	if _, err := r.Git(nil, nil, "update-ref", "-m", "dott: "+msg, "HEAD", commit, head); err != nil {
		return "", err
	}
	// the real index still holds the old version: move it on where it was untouched
	for _, s := range done {
		cur, _ := r.one("ls-files", "-s", "--", s.path)
		if f := strings.Fields(cur); len(cur) == 0 || (len(f) >= 2 && f[1] == s.oldBlob) {
			if _, err := r.Git(nil, nil, "update-index", "--add", "--cacheinfo", s.mode+","+s.newBlob+","+s.path); err != nil {
				return commit, err
			}
		}
	}
	return commit, nil
}

// Checkout says whether the folder is a git checkout (a work tree).
func (r Repo) Checkout() bool {
	out, err := r.one("rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// Branch is the current branch, empty when detached.
func (r Repo) Branch() string {
	b, _ := r.one("branch", "--show-current")
	return b
}

// Upstream is the branch HEAD pulls from, like origin/main.
func (r Repo) Upstream() (string, error) {
	return r.one("rev-parse", "--abbrev-ref", "@{u}")
}

// RemoteNews asks the remote, without fetching, whether the branch it tracks has commits
// that HEAD doesn't. It writes nothing: no fetch, no refs. git runs in a session of its own
// with no terminal and GIT_TERMINAL_PROMPT=0, so neither git nor ssh can stop to ask for a
// password; they fail instead, and so does this. When ctx ends, git and everything it
// started (ssh) are killed. It returns the upstream's name, like origin/main, when there
// is news.
func (r Repo) RemoteNews(ctx context.Context) (string, error) {
	b := r.Branch()
	if b == "" {
		return "", fmt.Errorf("not on a branch")
	}
	remote, err := r.one("config", "branch."+b+".remote")
	if err != nil {
		return "", fmt.Errorf("branch %s tracks no remote", b)
	}
	ref, err := r.one("config", "branch."+b+".merge")
	if err != nil {
		return "", fmt.Errorf("branch %s tracks no remote branch", b)
	}
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", r.Dir, "ls-remote", "--", remote, ref)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) } // git and its ssh
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s: %v", remote, err)
	}
	sha, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	if sha == "" {
		return "", fmt.Errorf("%s has no %s", remote, ref)
	}
	if head, _ := r.one("rev-parse", "HEAD"); head == sha {
		return "", nil
	}
	if _, err := r.one("merge-base", "--is-ancestor", sha, "HEAD"); err == nil {
		return "", nil // HEAD has it already: you are ahead
	}
	return remote + "/" + strings.TrimPrefix(ref, "refs/heads/"), nil
}
