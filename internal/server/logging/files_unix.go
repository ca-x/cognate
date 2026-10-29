//go:build unix

package logging

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func validPrivateFileInfo(info os.FileInfo) bool {
	metadata, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && metadata.Uid == uint32(os.Geteuid()) && metadata.Nlink == 1
}

func privateOpenFlags(flags int) int { return flags | unix.O_NOFOLLOW | unix.O_NONBLOCK }

func writeWarning(file *os.File) {
	raw, err := file.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(descriptor uintptr) {
		flags, err := unix.FcntlInt(descriptor, unix.F_GETFL, 0)
		if err != nil || flags&unix.O_NONBLOCK == 0 {
			return
		}
		var metadata unix.Stat_t
		if unix.Fstat(int(descriptor), &metadata) != nil {
			return
		}
		kind := metadata.Mode & unix.S_IFMT
		if kind != unix.S_IFIFO && kind != unix.S_IFSOCK {
			return
		}
		_, _ = unix.Write(int(descriptor), []byte("cognate: operational logging unavailable\n"))
	})
}

func trustedLoggingPath(path string) bool {
	for current := path; ; current = filepath.Dir(current) {
		var metadata unix.Stat_t
		if unix.Lstat(current, &metadata) != nil || metadata.Mode&unix.S_IFMT != unix.S_IFDIR || metadata.Uid != uint32(os.Geteuid()) && metadata.Uid != 0 {
			return false
		}
		if current == path {
			if metadata.Uid != uint32(os.Geteuid()) || metadata.Mode&0o777 != 0o700 {
				return false
			}
		} else if metadata.Mode&0o022 != 0 && metadata.Mode&unix.S_ISVTX == 0 {
			return false
		}
		if filepath.Dir(current) == current {
			return true
		}
	}
}
