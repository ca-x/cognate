package install

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func openProjectionLink(root *os.Root, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("symlink name must be a single component")
	}
	link, err := root.OpenFile(name, unix.O_SYMLINK|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := link.Stat()
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		link.Close()
		return nil, fmt.Errorf("projection handle is not a symlink")
	}
	current, err := root.Lstat(name)
	if err != nil || !os.SameFile(info, current) {
		link.Close()
		return nil, fmt.Errorf("projection link changed while opening")
	}
	return link, nil
}
