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
	parent, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	descriptor, err := unix.Openat(int(parent.Fd()), name, unix.O_SYMLINK|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	link := os.NewFile(uintptr(descriptor), name)
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
