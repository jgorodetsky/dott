package app

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jgorodetsky/dott/internal/config"
	"github.com/jgorodetsky/dott/internal/env"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Init sets up this Mac's settings from a dotfiles repo: a path, or owner/name on GitHub,
// which is cloned to ~/dotfiles. It changes nothing else; the picker does the rest.
func Init(e *env.Env, repo string, out io.Writer) (string, error) {
	cfg, err := config.Load(ConfigPath(e))
	if err != nil {
		return "", err
	}
	dir := ""
	if p, err := filepath.Abs(e.Expand(repo)); err == nil && isDir(p) {
		dir = p
	} else {
		url := repo
		if !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
			if strings.Count(repo, "/") != 1 {
				return "", fmt.Errorf("--init takes owner/name, a git URL or a folder; got %q", repo)
			}
			url = "https://github.com/" + strings.TrimSuffix(repo, ".git") + ".git"
		}
		dest := filepath.Join(e.Home, "dotfiles")
		if exists(dest) {
			if !exists(filepath.Join(dest, manifest.FileName)) {
				return "", fmt.Errorf("%s already exists and has no %s; move it, or pass the folder to use: dott --init <path>", e.Short(dest), manifest.FileName)
			}
			fmt.Fprintf(out, "using %s (already there)\n", e.Short(dest))
		} else {
			if !e.Has("git") {
				return "", fmt.Errorf("git comes with Xcode's command line tools: run xcode-select --install, wait for it to finish, then dott --init %s again", repo)
			}
			fmt.Fprintf(out, "cloning %s into %s\n", url, e.Short(dest))
			cmd := exec.Command("git", "clone", url, dest)
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("git clone %s: %w", url, err)
			}
		}
		dir = dest
	}
	if !exists(filepath.Join(dir, manifest.FileName)) {
		return "", fmt.Errorf("%s has no %s, so dott doesn't know what to set up", e.Short(dir), manifest.FileName)
	}
	if _, err := manifest.Load(dir); err != nil {
		return "", err
	}
	cfg.Repo = e.Short(dir)
	if err := cfg.Save(ConfigPath(e)); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "✓ this Mac uses %s (saved in %s)\n", e.Short(dir), e.Short(ConfigPath(e)))
	return dir, nil
}
