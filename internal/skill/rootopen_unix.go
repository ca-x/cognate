//go:build unix

package skill

import (
	"os"

	"golang.org/x/sys/unix"
)

func openRootRead(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
}
