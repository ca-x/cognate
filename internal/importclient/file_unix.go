//go:build unix

package importclient

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func openExport(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errFile
	}
	directory, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errFile
	}
	defer func() { _ = unix.Close(directory) }()
	components := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	for index, component := range components {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if index < len(components)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(directory, component, flags, 0)
		if err != nil {
			return nil, errFile
		}
		if index == len(components)-1 {
			return os.NewFile(uintptr(next), "import export"), nil
		}
		_ = unix.Close(directory)
		directory = next
	}
	return nil, errFile
}
