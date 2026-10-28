//go:build unix

package filelock

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockOpenFlags() int {
	return os.O_CREATE | os.O_RDWR | unix.O_NONBLOCK | unix.O_NOFOLLOW
}
