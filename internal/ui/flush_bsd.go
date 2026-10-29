//go:build darwin || freebsd || netbsd || openbsd

package ui

import "golang.org/x/sys/unix"

// flushInput drops what was typed on the terminal and not read yet.
func flushInput(fd int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, 1) // FREAD: the input queue only
}
