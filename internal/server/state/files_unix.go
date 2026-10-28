//go:build unix

package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func validateServerDirectoryInfo(info os.FileInfo) error {
	attributes, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || attributes.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("server data directory must be private and owned by the current user")
	}
	return nil
}

func validateServerDirectory(path string) error {
	return validateServerPath(path, true)
}

func validateServerAncestor(path string) error {
	return validateServerPath(path, false)
}

func validateServerPath(path string, private bool) error {
	for current := path; ; current = filepath.Dir(current) {
		var info unix.Stat_t
		if err := unix.Lstat(current, &info); err != nil {
			return fmt.Errorf("inspect server data directory: %w", err)
		}
		if info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Uid != uint32(os.Geteuid()) && info.Uid != 0 {
			return fmt.Errorf("server data directory path is not trusted")
		}
		if current == path && private {
			if info.Uid != uint32(os.Geteuid()) || info.Mode&0o077 != 0 {
				return fmt.Errorf("server data directory must be private and owned by the current user")
			}
		} else if info.Mode&0o022 != 0 && info.Mode&unix.S_ISVTX == 0 {
			return fmt.Errorf("server data directory ancestor is writable by other users")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func inspectServerFile(path string) (bool, error) {
	var info unix.Stat_t
	if err := unix.Lstat(path, &info); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 || info.Uid != uint32(os.Geteuid()) || info.Mode&0o077 != 0 {
		return false, fmt.Errorf("server database files must be private, owned regular files with one hard link")
	}
	return true, nil
}
