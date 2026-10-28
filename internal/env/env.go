// Package env holds the facts about this machine that every check reads: home, OS,
// which commands and apps exist. Tests build an Env by hand to fake a machine.
package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Env is one machine, read once per run.
type Env struct {
	Home    string
	OS      string // runtime.GOOS
	CLT     bool   // git and compilers work: Xcode command line tools are installed (always true off macOS)
	AppDirs []string
	Getenv  func(string) string
}

// New reads the real machine. It runs nothing that can change it: xcode-select -p only prints a path.
func New() *Env {
	home, _ := os.UserHomeDir()
	e := &Env{
		Home:    home,
		OS:      runtime.GOOS,
		CLT:     true,
		AppDirs: []string{"/Applications", filepath.Join(home, "Applications")},
		Getenv:  os.Getenv,
	}
	if e.OS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/xcode-select", "-p").Output()
		dir := strings.TrimSpace(string(out))
		e.CLT = err == nil && dir != "" && exists(dir)
	}
	return e
}

// Has says whether cmd is on PATH. /usr/bin/git without the command line tools is a stub
// that opens an install dialog, so git counts only when they are there.
func (e *Env) Has(cmd string) bool {
	p, err := exec.LookPath(cmd)
	if err != nil {
		return false
	}
	if cmd == "git" && p == "/usr/bin/git" && !e.CLT {
		return false
	}
	return true
}

// App says whether /Applications (or ~/Applications) has NAME.app.
func (e *Env) App(name string) bool {
	for _, d := range e.AppDirs {
		if exists(filepath.Join(d, name+".app")) {
			return true
		}
	}
	return false
}

// envVar is one ${VAR} or ${VAR:-default} whose default has no braces, so nested defaults
// like ${XDG_CONFIG_HOME:-${HOME}/.config} expand from the inside out.
var envVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^{}]*))?\}`)

// Expand turns ~ and ${VAR:-default} into real paths.
func (e *Env) Expand(p string) string {
	for range 4 {
		next := envVar.ReplaceAllStringFunc(p, func(m string) string {
			g := envVar.FindStringSubmatch(m)
			if v := e.Getenv(g[1]); v != "" {
				return v
			}
			return g[3]
		})
		if next == p {
			break
		}
		p = next
	}
	if p == "~" {
		return e.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(e.Home, p[2:])
	}
	return p
}

// Short writes a path under home as ~/..., for screens and plans.
func (e *Env) Short(p string) string {
	if e.Home != "" && (p == e.Home || strings.HasPrefix(p, e.Home+"/")) {
		return "~" + p[len(e.Home):]
	}
	return p
}

// ConfigDir is $XDG_CONFIG_HOME/dott or ~/.config/dott.
func (e *Env) ConfigDir() string { return e.xdg("XDG_CONFIG_HOME", ".config") }

// StateDir is $XDG_STATE_HOME/dott or ~/.local/state/dott.
func (e *Env) StateDir() string { return e.xdg("XDG_STATE_HOME", ".local/state") }

func (e *Env) xdg(key, def string) string {
	if v := e.Getenv(key); v != "" {
		return filepath.Join(v, "dott")
	}
	return filepath.Join(e.Home, def, "dott")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
