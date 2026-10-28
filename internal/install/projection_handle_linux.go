package install

import (
	"os"

	"golang.org/x/sys/unix"
)

func openProjectionLink(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, unix.O_PATH|unix.O_NOFOLLOW, 0)
}
