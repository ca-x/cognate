//go:build unix

package state

import (
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/sys/unix"
)

func checkDatabaseLinks(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("database must be a regular file")
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("database must have exactly one hard link; found %d", stat.Nlink)
	}
	return nil
}
