package setup

import "golang.org/x/sys/unix"

// flushInput discards what's been typed at the terminal but not read.
func flushInput(fd int) {
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
