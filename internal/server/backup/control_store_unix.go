//go:build unix

package backup

import (
	"os"

	"golang.org/x/sys/unix"
)

func controlPlatform() error { return nil }

func controlPrivate(info os.FileInfo, directory bool) bool {
	return info != nil && sealPrivate(info, directory)
}

func controlDuplicateLock(file *os.File) (*os.File, error) {
	descriptor, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	return os.NewFile(uintptr(descriptor), controlLockName), nil
}

func controlLock(root *os.Root) (*os.File, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, ErrInvalid
	}
	descriptor, openError := unix.Openat(int(directory.Fd()), controlLockName, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	closeError := directory.Close()
	if openError != nil {
		if closeError != nil {
			return nil, ErrControlUncertain
		}
		return nil, ErrInvalid
	}
	file := os.NewFile(uintptr(descriptor), controlLockName)
	if closeError != nil {
		return file, ErrControlUncertain
	}
	info, err := file.Stat()
	named, namedError := root.Lstat(controlLockName)
	if err != nil || namedError != nil || !controlPrivate(info, false) || !controlPrivate(named, false) || !os.SameFile(info, named) {
		return file, ErrInvalid
	}
	if err := unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return file, ErrBusy
	}
	return file, nil
}
