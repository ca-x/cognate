//go:build unix

package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"

	"github.com/ca-x/cognate/internal/server/maintenance"
	"golang.org/x/sys/unix"
)

func sealPrivate(info os.FileInfo, directory bool) bool {
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && attributes.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && ((directory && info.IsDir()) || (!directory && info.Mode().IsRegular() && attributes.Nlink == 1))
}

func newSealDirectory(parent *os.File) (*sealDirectory, error) {
	if err := maintenance.CheckPlatform(context.Background()); err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, ErrInvalid
	}
	name := parent.Name()
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(name)
	if err != nil || canonical != name {
		return nil, ErrInvalid
	}
	info, err := parent.Stat()
	if err != nil || !sealPrivate(info, true) {
		return nil, ErrInvalid
	}
	named, err := os.Lstat(name)
	if err != nil || !os.SameFile(info, named) {
		return nil, ErrInvalid
	}
	descriptor, err := unix.Openat(int(parent.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrInvalid
	}
	ownedParent := os.NewFile(uintptr(descriptor), name)
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		_ = ownedParent.Close()
		return nil, ErrUnavailable
	}
	child := "crypto-" + hex.EncodeToString(random[:])
	if unix.Mkdirat(descriptor, child, 0700) != nil {
		_ = ownedParent.Close()
		return nil, ErrInvalid
	}
	rootFD, err := unix.Openat(descriptor, child, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		_ = unix.Unlinkat(descriptor, child, unix.AT_REMOVEDIR)
		_ = ownedParent.Close()
		return nil, ErrInvalid
	}
	root := os.NewFile(uintptr(rootFD), filepath.Join(name, child))
	directory := &sealDirectory{parent: ownedParent, root: root, name: child, refs: 1}
	if parent.Sync() != nil || root.Sync() != nil {
		if cleanupErr := discardSealDirectory(directory); cleanupErr != nil {
			return nil, cleanupErr
		}
		return nil, ErrSealCleanup
	}
	return directory, nil
}

func discardSealDirectory(directory *sealDirectory) error {
	err := sealCloseDirectory(directory)
	if err != nil {
		directory.failed = true
		retainSealDirectory(directory)
	}
	return err
}

func sealOpen(directory *sealDirectory, name string, write bool) (*os.File, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return nil, ErrInvalid
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
	if write {
		flags = unix.O_RDWR | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
	}
	descriptor, err := unix.Openat(int(directory.root.Fd()), name, flags, 0600)
	if err != nil {
		return nil, ErrInvalid
	}
	file := os.NewFile(uintptr(descriptor), filepath.Join(directory.root.Name(), name))
	info, err := file.Stat()
	if err != nil || !sealPrivate(info, false) {
		_ = file.Close()
		return nil, ErrInvalid
	}
	return file, nil
}

func sealBorrow(file *os.File) (os.FileInfo, error) {
	if file == nil {
		return nil, ErrInvalid
	}
	info, err := file.Stat()
	if err != nil || !sealPrivate(info, false) {
		return nil, ErrInvalid
	}
	named, err := os.Lstat(file.Name())
	if err != nil || !os.SameFile(info, named) || !sealPrivate(named, false) {
		return nil, ErrInvalid
	}
	return info, nil
}

func sealRepin(file *os.File, before os.FileInfo) error {
	current, err := sealBorrow(file)
	if err != nil || !os.SameFile(before, current) || before.Size() != current.Size() || before.Mode() != current.Mode() || !before.ModTime().Equal(current.ModTime()) {
		return ErrInvalid
	}
	return nil
}

func sealUnlink(directory *sealDirectory, name string) error {
	if unix.Unlinkat(int(directory.root.Fd()), name, 0) != nil {
		return ErrSealCleanup
	}
	return nil
}

func sealCloseDirectory(directory *sealDirectory) error {
	if directory.root.Sync() != nil {
		return ErrSealCleanup
	}
	if directory.root.Close() != nil {
		return ErrSealUncertain
	}
	if unix.Unlinkat(int(directory.parent.Fd()), directory.name, unix.AT_REMOVEDIR) != nil {
		return ErrSealCleanup
	}
	if directory.parent.Sync() != nil {
		return ErrSealCleanup
	}
	if directory.parent.Close() != nil {
		return ErrSealUncertain
	}
	return nil
}
