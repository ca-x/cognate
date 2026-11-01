//go:build linux

package runtimegen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	lockName  = ".runtime.lock"
	fenceName = ".runtime-fences"
)

var quarantined struct {
	sync.Mutex
	leases []*rootLease
}

type leaseOps struct {
	close        func(*os.File) error
	sync         func(*os.File) error
	write        func(*os.File, []byte) (int, error)
	remove       func(string) error
	createMarker func(*os.File, string) (*os.File, error)
}

type rootLease struct {
	mutex   sync.Mutex
	done    bool
	result  error
	path    string
	root    *os.File
	lock    *os.File
	fences  *os.File
	marker  *os.File
	rootID  unix.Stat_t
	lockID  unix.Stat_t
	fenceID unix.Stat_t
	ops     leaseOps
}

func openLease(ctx context.Context, path string, exclusive bool) (*rootLease, error) {
	return openLeaseUsing(ctx, path, exclusive, leaseOps{close: (*os.File).Close, sync: (*os.File).Sync, write: (*os.File).Write, remove: unix.Unlink, createMarker: createMarker})
}

func openLeaseUsing(ctx context.Context, path string, exclusive bool, ops leaseOps) (*rootLease, error) {
	if ctx == nil || path == "" {
		return nil, ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for component := range strings.SplitSeq(path, "/") {
		if component == ".." {
			return nil, ErrInvalid
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrInvalid
	}
	root, err := openPrivateRoot(absolute)
	if err != nil {
		return nil, err
	}
	lease := &rootLease{path: absolute, root: root, ops: ops}
	fail := func(cause error) (*rootLease, error) {
		for _, file := range []*os.File{lease.fences, lease.root, lease.lock} {
			if file != nil && lease.ops.close(file) != nil {
				cause = ErrUncertain
			}
		}
		return nil, cause
	}
	if unix.Fstat(int(root.Fd()), &lease.rootID) != nil {
		return fail(ErrInvalid)
	}
	lease.lock, err = openLock(root)
	if err != nil {
		return fail(err)
	}
	if unix.Fstat(int(lease.lock.Fd()), &lease.lockID) != nil {
		return fail(ErrInvalid)
	}
	if err := unix.Mkdirat(int(root.Fd()), fenceName, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return fail(ErrInvalid)
	}
	lease.fences, err = openAt(root, fenceName, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return fail(ErrInvalid)
	}
	if unix.Fstat(int(lease.fences.Fd()), &lease.fenceID) != nil || !privateDirectory(lease.fenceID) {
		return fail(ErrInvalid)
	}
	if lease.ops.sync(lease.lock) != nil || lease.ops.sync(lease.fences) != nil || lease.ops.sync(lease.root) != nil {
		return fail(ErrUncertain)
	}
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	for {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		if err := lease.check(); err != nil {
			return fail(err)
		}
		err = unix.Flock(int(lease.lock.Fd()), mode|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return fail(ErrInvalid)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail(ctx.Err())
		case <-timer.C:
		}
	}
	if err := lease.admit(ctx); err != nil {
		if lease.close() != nil {
			return nil, ErrUncertain
		}
		return nil, err
	}
	return lease, nil
}

func openAt(parent *os.File, name string, flags int, mode uint32) (*os.File, error) {
	descriptor, err := unix.Openat(int(parent.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, ErrInvalid
	}
	return os.NewFile(uintptr(descriptor), "runtime coordination"), nil
}

func openLock(root *os.File) (*os.File, error) {
	file, err := openAt(root, lockName, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		file, err = openAt(root, lockName, unix.O_RDWR, 0)
	}
	if err != nil {
		return nil, ErrInvalid
	}
	var stat unix.Stat_t
	if unix.Fstat(int(file.Fd()), &stat) != nil || !privateLock(stat) {
		if file.Close() != nil {
			return nil, ErrUncertain
		}
		return nil, ErrInvalid
	}
	return file, nil
}

func openPrivateRoot(path string) (*os.File, error) {
	descriptor, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, part := range parts {
		var stat unix.Stat_t
		if unix.Fstat(descriptor, &stat) != nil || !safeAncestor(stat) {
			if unix.Close(descriptor) != nil {
				return nil, ErrUncertain
			}
			return nil, ErrInvalid
		}
		next, openError := unix.Openat(descriptor, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		closeError := unix.Close(descriptor)
		if closeError != nil {
			if openError == nil {
				unix.Close(next)
			}
			return nil, ErrUncertain
		}
		if openError != nil {
			return nil, ErrInvalid
		}
		descriptor = next
		if index == len(parts)-1 {
			if unix.Fstat(descriptor, &stat) != nil || !privateDirectory(stat) {
				if unix.Close(descriptor) != nil {
					return nil, ErrUncertain
				}
				return nil, ErrInvalid
			}
		}
	}
	return os.NewFile(uintptr(descriptor), "runtime root"), nil
}

func safeAncestor(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFDIR && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid())) && (stat.Mode&0022 == 0 || stat.Mode&unix.S_ISVTX != 0)
}

func privateDirectory(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Mode&07777 == 0700 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink > 0
}

func privateLock(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&07777 == 0600 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1 && stat.Size == 0
}

func sameInode(first, second unix.Stat_t) bool {
	return first.Dev == second.Dev && first.Ino == second.Ino
}

func (lease *rootLease) checkNames() error {
	var stat unix.Stat_t
	parent := "/"
	for part := range strings.SplitSeq(strings.TrimPrefix(lease.path, "/"), "/") {
		parent = filepath.Join(parent, part)
		if unix.Lstat(parent, &stat) != nil || !safeAncestor(stat) {
			return ErrInvalid
		}
	}
	if !privateDirectory(stat) || !sameInode(stat, lease.rootID) {
		return ErrInvalid
	}
	if unix.Lstat(filepath.Join(lease.path, lockName), &stat) != nil || !privateLock(stat) || !sameInode(stat, lease.lockID) {
		return ErrInvalid
	}
	if unix.Lstat(filepath.Join(lease.path, fenceName), &stat) != nil || !privateDirectory(stat) || !sameInode(stat, lease.fenceID) {
		return ErrInvalid
	}
	return nil
}

func (lease *rootLease) check() error {
	if err := lease.checkNames(); err != nil {
		return err
	}
	for _, entry := range []struct {
		file *os.File
		id   unix.Stat_t
	}{{lease.root, lease.rootID}, {lease.lock, lease.lockID}, {lease.fences, lease.fenceID}} {
		var stat unix.Stat_t
		if unix.Fstat(int(entry.file.Fd()), &stat) != nil || !sameInode(stat, entry.id) {
			return ErrInvalid
		}
	}
	return nil
}

func (lease *rootLease) admit(ctx context.Context) error {
	if err := lease.check(); err != nil {
		return err
	}
	names, err := lease.fences.Readdirnames(1)
	if len(names) != 0 {
		return ErrNeedsAttention
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return ctx.Err()
}

func (lease *rootLease) quarantineLocked() {
	lease.done = true
	lease.result = ErrUncertain
	quarantined.Lock()
	quarantined.leases = append(quarantined.leases, lease)
	quarantined.Unlock()
}

func (lease *rootLease) close() error {
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if lease.done {
		return lease.result
	}
	lease.done = true
	lease.result = ErrUncertain
	defer func() {
		if lease.result != nil {
			quarantined.Lock()
			quarantined.leases = append(quarantined.leases, lease)
			quarantined.Unlock()
		}
	}()
	identityError := lease.check()
	name, marker, err := lease.arm()
	if err != nil || identityError != nil || lease.check() != nil {
		return lease.result
	}
	for _, file := range []*os.File{lease.fences, lease.root, lease.lock} {
		if file == lease.lock && lease.checkNames() != nil {
			return lease.result
		}
		if lease.ops.close(file) != nil {
			return lease.result
		}
	}
	if lease.checkNames() != nil {
		return lease.result
	}
	path := filepath.Join(lease.path, fenceName, name)
	var named unix.Stat_t
	if unix.Lstat(path, &named) != nil || !sameInode(named, marker) || !privateMarker(named) {
		return lease.result
	}
	if lease.ops.remove(path) != nil {
		return lease.result
	}
	lease.result = nil
	return nil
}

func privateMarker(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&07777 == 0600 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1 && stat.Size == 8
}

func createMarker(parent *os.File, name string) (*os.File, error) {
	return openAt(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
}

func (lease *rootLease) arm() (string, unix.Stat_t, error) {
	var marker unix.Stat_t
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", marker, ErrUncertain
	}
	name := "pending-" + hex.EncodeToString(nonce[:])
	file, err := lease.ops.createMarker(lease.fences, name)
	if err != nil {
		return "", marker, ErrUncertain
	}
	lease.marker = file
	written, writeError := lease.ops.write(file, []byte("CGRLS001"))
	syncError := lease.ops.sync(file)
	statError := unix.Fstat(int(file.Fd()), &marker)
	closeError := lease.ops.close(file)
	directoryError := lease.ops.sync(lease.fences)
	rootError := lease.ops.sync(lease.root)
	if written != 8 || writeError != nil || syncError != nil || statError != nil || !privateMarker(marker) || closeError != nil || directoryError != nil || rootError != nil {
		return name, marker, ErrUncertain
	}
	return name, marker, nil
}
