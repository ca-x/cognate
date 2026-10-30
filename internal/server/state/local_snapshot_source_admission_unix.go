//go:build unix

package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type snapshotSourceFile struct {
	file *os.File
	info os.FileInfo
}

func snapshotSourcePrivateFile(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && info.Mode().Perm()&0o077 == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}

func snapshotSourceSame(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Size() == after.Size() && before.Mode() == after.Mode() && before.ModTime().Equal(after.ModTime())
}

func snapshotSourceOpen(parent *os.File, name string) (snapshotSourceFile, error) {
	descriptor, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return snapshotSourceFile{}, err
	}
	file := os.NewFile(uintptr(descriptor), "local snapshot inspection")
	return snapshotSourceFile{file: file}, nil
}

func snapshotSourceJournalAbsent(parent *os.File, name string) bool {
	var info unix.Stat_t
	return errors.Is(unix.Fstatat(int(parent.Fd()), name+"-journal", &info, unix.AT_SYMLINK_NOFOLLOW), unix.ENOENT)
}

func snapshotSourceParent(parent *os.File) (os.FileInfo, error) {
	path := parent.Name()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || validateServerDirectory(path) != nil {
		return nil, errSnapshotSourceInvalid
	}
	opened, err := parent.Stat()
	if err != nil || validateServerDirectoryInfo(opened) != nil {
		return nil, errSnapshotSourceInvalid
	}
	named, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, named) {
		return nil, errSnapshotSourceInvalid
	}
	return opened, nil
}

func snapshotSourceRead(ctx context.Context, file snapshotSourceFile, buffer []byte, remaining *uint64, operations localSnapshotSourceIO) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if file.file == nil || file.info.Size() == 0 {
		return nil
	}
	if *remaining < uint64(len(buffer)) {
		return errSnapshotSourceBudget
	}
	*remaining -= uint64(len(buffer))
	count, err := operations.readAt(file.file, buffer, 0)
	if err != nil || count != len(buffer) {
		return errSnapshotSourceInvalid
	}
	return ctx.Err()
}

func snapshotSourceRecheck(parent *os.File, name string, before snapshotSourceFile, operations localSnapshotSourceIO) error {
	current, err := snapshotSourceOpen(parent, name)
	if before.file == nil {
		if current.file != nil {
			if operations.closeFile(current.file) != nil {
				return errSnapshotSourceCleanup
			}
		}
		if !errors.Is(err, unix.ENOENT) {
			return errSnapshotSourceInvalid
		}
		return nil
	}
	if err != nil {
		return errSnapshotSourceInvalid
	}
	named, statError := current.file.Stat()
	closeError := operations.closeFile(current.file)
	if closeError != nil {
		return errSnapshotSourceCleanup
	}
	opened, err := before.file.Stat()
	if statError != nil || err != nil || !snapshotSourcePrivateFile(named) || !snapshotSourcePrivateFile(opened) || !snapshotSourceSame(before.info, named) || !snapshotSourceSame(before.info, opened) {
		return errSnapshotSourceInvalid
	}
	return nil
}

func inspectLocalSnapshotSourcePlatform(ctx context.Context, parent, database *os.File, readBudget uint64, operations localSnapshotSourceIO) (result localSnapshotSourceObservation, returnedError error) {
	var files [3]snapshotSourceFile
	defer func() {
		for index := len(files) - 1; index >= 0; index-- {
			if files[index].file != nil && operations.closeFile(files[index].file) != nil {
				returnedError = errors.Join(returnedError, errSnapshotSourceCleanup)
			}
		}
		if err := ctx.Err(); err != nil {
			returnedError = errors.Join(returnedError, err)
		}
		if returnedError != nil {
			result = localSnapshotSourceObservation{}
		}
	}()
	parentInfo, err := snapshotSourceParent(parent)
	if err != nil {
		return result, err
	}
	if filepath.Dir(database.Name()) != parent.Name() || filepath.Base(database.Name()) == "." {
		return result, errSnapshotSourceInvalid
	}
	name := filepath.Base(database.Name())
	borrowed, err := database.Stat()
	if err != nil || !snapshotSourcePrivateFile(borrowed) || !snapshotSourceJournalAbsent(parent, name) {
		return result, errSnapshotSourceInvalid
	}
	suffixes := [3]string{"", "-wal", "-shm"}
	for index, suffix := range suffixes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		files[index], err = snapshotSourceOpen(parent, name+suffix)
		if index > 0 && errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return result, errSnapshotSourceInvalid
		}
		files[index].info, err = files[index].file.Stat()
		if err != nil || !snapshotSourcePrivateFile(files[index].info) {
			return result, errSnapshotSourceInvalid
		}
	}
	if !snapshotSourceSame(borrowed, files[0].info) {
		return result, errSnapshotSourceInvalid
	}
	var main [100]byte
	var wal [32]byte
	var shm [136]byte
	if err := snapshotSourceRead(ctx, files[0], main[:], &readBudget, operations); err != nil {
		return result, err
	}
	result, err = snapshotSourceMain(&main, files[0].info.Size())
	if err != nil {
		return result, err
	}
	var walSize, shmSize int64
	if files[1].file != nil {
		walSize = files[1].info.Size()
	}
	if files[2].file != nil {
		shmSize = files[2].info.Size()
	}
	if err := snapshotSourceRead(ctx, files[1], wal[:], &readBudget, operations); err != nil {
		return result, err
	}
	if err := snapshotSourceWAL(&result, &wal, walSize); err != nil {
		return result, err
	}
	if err := snapshotSourceRead(ctx, files[2], shm[:], &readBudget, operations); err != nil {
		return result, err
	}
	if err := snapshotSourceSHM(&result, &shm, shmSize, &wal, walSize); err != nil {
		return result, err
	}
	if err := snapshotSourceNecessaryRefusal(&result); err != nil {
		return result, err
	}
	for index, suffix := range suffixes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := snapshotSourceRecheck(parent, name+suffix, files[index], operations); err != nil {
			return result, err
		}
	}
	finalParent, err := snapshotSourceParent(parent)
	finalBorrowed, borrowedError := database.Stat()
	if err != nil || borrowedError != nil || !os.SameFile(parentInfo, finalParent) || !snapshotSourcePrivateFile(finalBorrowed) || !snapshotSourceSame(borrowed, finalBorrowed) || !snapshotSourceJournalAbsent(parent, name) {
		return result, errSnapshotSourceInvalid
	}
	result.decision = snapshotSourceNeedsCertificate
	return result, nil
}
