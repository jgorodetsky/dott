package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/jgorodetsky/dott/internal/testkit"
	"github.com/jgorodetsky/dott/internal/ui"
)

// goEnv keeps go's own cache folders for the builds tests run. They follow HOME
// by default, so without it every test build downloads the modules and toolchain again.
var goEnv []string

// TestMain points HOME and the XDG folders at a temp dir, so no test can touch the real ones.
func TestMain(m *testing.M) {
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE", "GOPATH").Output()
	if err != nil {
		panic(err)
	}
	for i, k := range []string{"GOMODCACHE", "GOCACHE", "GOPATH"} {
		goEnv = append(goEnv, k+"="+strings.Split(strings.TrimSpace(string(out)), "\n")[i])
	}
	home, err := os.MkdirTemp("", "dott-test-home-")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		os.Setenv(k, home)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    options
		wantErr string
	}{
		{"no args", nil, options{}, ""},
		{"words", []string{"fix", "aws"}, options{words: []string{"fix", "aws"}}, ""},
		{"flag after words", []string{"fix", "aws", "--yes"}, options{yes: true, words: []string{"fix", "aws"}}, ""},
		{"short flags", []string{"-h", "-v", "-y"}, options{help: true, version: true, yes: true}, ""},
		{"json", []string{"--json"}, options{json: true}, ""},
		{"init", []string{"--init", "owner/dotfiles"}, options{init: "owner/dotfiles"}, ""},
		{"init equals", []string{"--init=owner/dotfiles"}, options{init: "owner/dotfiles"}, ""},
		{"init without repo", []string{"--init"}, options{}, "--init needs a repo"},
		{"init equals empty", []string{"--init="}, options{}, "--init needs a repo"},
		{"double dash keeps dashes", []string{"--", "--json", "x"}, options{words: []string{"--json", "x"}}, ""},
		{"lone dash is a word", []string{"-"}, options{words: []string{"-"}}, ""},
		{"unknown flag", []string{"--frobnicate"}, options{}, "unknown flag --frobnicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	f := testkit.New(t)
	f.Use(t)
	f.Write(t, "home/.config/dott/config.toml", "[groups]\nextra = false\n")
	tests := []struct {
		name       string
		args       []string
		code       int
		stdout     string
		stderrPart string
	}{
		{"help", []string{"--help"}, 0, usage, ""},
		{"version", []string{"--version"}, 0, "dott dev (fzf 0.74.5)\n", ""},
		{"bad flag", []string{"--nope"}, 2, "", "unknown flag --nope"},
		{"no terminal prints matches", []string{"fix", "loader"}, 0, "change\tfix loader\tthe loader line\n", ""},
		{"no terminal, no match", []string{"zzzz"}, 1, "", `no row matches "zzzz"`},
		{"no terminal, no words", nil, 2, "", "no terminal"},
		{"yes refuses a guided row", []string{"upgrade", "--yes"}, 2, "", "only a change row"},
		{"yes refuses a script that asks", []string{"fix", "ask", "--yes"}, 2, "", "asks its own questions"},
	}
	oldTTY, oldEnv := openTTY, newEnv
	t.Cleanup(func() { openTTY, newEnv = oldTTY, oldEnv })
	openTTY = func() (*ui.Term, func(), error) { return nil, func() {}, errors.New("no terminal in tests") }
	newEnv = f.Env
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tt.args, &out, &errb); code != tt.code {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tt.code, errb.String())
			}
			if out.String() != tt.stdout {
				t.Fatalf("stdout %q, want %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errb.String(), tt.stderrPart) {
				t.Fatalf("stderr %q, want it to contain %q", errb.String(), tt.stderrPart)
			}
		})
	}
}
