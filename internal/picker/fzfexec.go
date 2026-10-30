package picker

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Exec runs an fzf binary with the same arguments as the built-in fzf.
type Exec struct{ Path string }

func (e Exec) run(argv, lines []string) ([]string, int, error) {
	cmd := exec.Command(e.Path, argv...)
	cmd.Env = append(os.Environ(), "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState == nil {
		return nil, 0, err // fzf didn't start: a wrong DOTT_FZF path
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	code := cmd.ProcessState.ExitCode()
	text := strings.TrimSuffix(out.String(), "\n")
	if text == "" {
		return nil, code, err
	}
	return strings.Split(text, "\n"), code, err
}

func (e Exec) Pick(req Request) (Result, error) {
	got, code, err := e.run(args(req), req.Lines)
	return checked(result(got, code), err)
}

func (e Exec) Filter(lines []string, query string) ([]string, error) {
	got, code, err := e.run(append(match(), "--filter="+query), lines)
	if err != nil || code > CodeNoMatch {
		return nil, err
	}
	return got, nil
}
