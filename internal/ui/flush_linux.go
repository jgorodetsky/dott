//go:build linux

package ui

import "golang.org/x/sys/unix"

// flushInput drops what was typed on the terminal and not read yet.
func flushInput(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
