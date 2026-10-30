//go:build linux

package configapply

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type codexGuard struct {
	file      *os.File
	directory *nativeDirectory
	identity  nativeIdentity
}

func codexPlatform() error {
	var identity unix.Utsname
	if unix.Uname(&identity) != nil || !codexLinuxKernelSupported(unix.ByteSliceToString(identity.Release[:]), unix.ByteSliceToString(identity.Version[:])) {
		return ErrCodexUnsupported
	}
	return nil
}

func codexLinuxKernelSupported(release, version string) bool {
	combined := strings.ToLower(release + " " + version)
	return release != "" && version != "" && !strings.Contains(combined, "microsoft") && !strings.Contains(combined, "wsl")
}

func acquireCodexGuard(ctx context.Context, directory *nativeDirectory) (*codexGuard, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if directory == nil || directory.check() != nil {
		return nil, ErrUnsafeTarget
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), "client.db.install.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, ErrUnsafeTarget
	}
	file := os.NewFile(uintptr(descriptor), "codex installation guard")
	identity, err := nativeStat(file)
	if err != nil || identity.Mode&unix.S_IFMT != unix.S_IFREG || identity.Owner != uint32(os.Geteuid()) || identity.Links != 1 || identity.Mode&0o022 != 0 || nativeACL(file) != nil {
		_ = file.Close()
		return nil, ErrUnsafeTarget
	}
	guard := &codexGuard{file: file, directory: directory, identity: identity}
	for {
		err = unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			_ = file.Close()
			return nil, ErrUnsafeTarget
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err := guard.check(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return guard, nil
}

func (guard *codexGuard) check() error {
	if guard == nil || guard.file == nil || guard.directory.check() != nil {
		return ErrUnsafeTarget
	}
	identity, err := nativeStat(guard.file)
	var entry unix.Stat_t
	if err != nil || identity != guard.identity || nativeACL(guard.file) != nil || unix.Fstatat(int(guard.directory.file.Fd()), "client.db.install.lock", &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || statIdentity(&entry) != identity {
		return ErrUnsafeTarget
	}
	return nil
}

func (guard *codexGuard) close() error {
	if guard == nil || guard.file == nil {
		return nil
	}
	err := guard.file.Close()
	guard.file = nil
	if err != nil {
		return ErrOutcomeUnknown
	}
	return nil
}

func codexDirectoryEntries(directory *nativeDirectory) ([]string, error) {
	if directory == nil || directory.check() != nil {
		return nil, ErrCodexAttention
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), ".", unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrCodexAttention
	}
	file := os.NewFile(uintptr(descriptor), "codex bounded inventory")
	defer file.Close()
	names, err := file.Readdirnames(codexEntryLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > codexEntryLimit || directory.check() != nil {
		return nil, ErrCodexCapacity
	}
	return names, nil
}
