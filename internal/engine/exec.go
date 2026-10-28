package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Stream runs argv in the repo folder with its output going to the screen. stdin is empty,
// so a command that would ask a question gets an end of input instead of hanging.
func (x *Exec) Stream(argv []string, env ...string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = x.C.M.Dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = x.Out, x.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

// TTY runs argv with the terminal attached, for commands that ask their own questions.
func TTY(dir string, argv []string, env ...string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("%s needs a terminal", argv[0])
	}
	defer f.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = f, f, f
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

// Quote writes argv as one shell line, quoting only what needs it.
func Quote(argv ...string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@,+%^") == "" {
			out[i] = a
		} else {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(out, " ")
}
