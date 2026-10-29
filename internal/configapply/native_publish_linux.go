//go:build linux

package configapply

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type nativePin struct {
	parent   *os.File
	file     *os.File
	name     string
	identity nativeIdentity
}

type nativeDirectory struct {
	file     *os.File
	pins     []nativePin
	owned    []*os.File
	identity nativeIdentity
}

func nativePlatform() error { return nil }

func statIdentity(stat *unix.Stat_t) nativeIdentity {
	return nativeIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Mode: stat.Mode, Links: uint64(stat.Nlink), Owner: stat.Uid, Size: stat.Size}
}

func sameDirectory(first, second nativeIdentity) bool {
	return first.Device == second.Device && first.Inode == second.Inode && first.Mode == second.Mode && first.Owner == second.Owner
}

func nativeStat(file *os.File) (nativeIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nativeIdentity{}, ErrUnsafeTarget
	}
	return statIdentity(&stat), nil
}

func nativeACL(file *os.File) error {
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		_, err := unix.Fgetxattr(int(file.Fd()), name, nil)
		if !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
			return ErrUnsafeTarget
		}
	}
	return nil
}

func nativeTrustDirectory(file *os.File, final, private bool) (nativeIdentity, error) {
	identity, err := nativeStat(file)
	if err != nil || identity.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nativeIdentity{}, ErrUnsafeTarget
	}
	if final {
		if identity.Owner != uint32(os.Geteuid()) || identity.Mode&0o022 != 0 || private && identity.Mode&0o077 != 0 {
			return nativeIdentity{}, ErrUnsafeTarget
		}
	} else if identity.Owner != 0 && identity.Owner != uint32(os.Geteuid()) || identity.Mode&0o022 != 0 && (identity.Owner != 0 || identity.Mode&unix.S_ISVTX == 0) {
		return nativeIdentity{}, ErrUnsafeTarget
	}
	if err := nativeACL(file); err != nil {
		return nativeIdentity{}, err
	}
	var filesystem unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &filesystem) != nil {
		return nativeIdentity{}, ErrUnsafeTarget
	}
	switch uint64(filesystem.Type) {
	case 0x6969, 0xff534d42, 0xfe534d42, 0x65735546:
		return nativeIdentity{}, ErrUnsafeTarget
	}
	return identity, nil
}

func nativeOpenDirectory(path string, private bool) (*nativeDirectory, error) {
	if path == "/" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrUnsafeTarget
	}
	rootFD, err := unix.Open("/", unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnsafeTarget
	}
	root := os.NewFile(uintptr(rootFD), "configuration directory")
	directory := &nativeDirectory{file: root, owned: []*os.File{root}}
	identity, err := nativeTrustDirectory(root, false, false)
	if err != nil {
		_ = directory.close()
		return nil, err
	}
	directory.pins = append(directory.pins, nativePin{file: root, identity: identity})
	components := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, name := range components {
		parent := directory.file
		descriptor, err := unix.Openat(int(parent.Fd()), name, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			_ = directory.close()
			return nil, ErrUnsafeTarget
		}
		child := os.NewFile(uintptr(descriptor), "configuration directory")
		directory.owned = append(directory.owned, child)
		identity, err = nativeTrustDirectory(child, index == len(components)-1, private)
		if err != nil {
			_ = directory.close()
			return nil, err
		}
		directory.pins = append(directory.pins, nativePin{parent: parent, file: child, name: name, identity: identity})
		directory.file, directory.identity = child, identity
	}
	if err := directory.check(); err != nil {
		_ = directory.close()
		return nil, err
	}
	return directory, nil
}

func (directory *nativeDirectory) check() error {
	for _, pin := range directory.pins {
		identity, err := nativeStat(pin.file)
		if err != nil || !sameDirectory(identity, pin.identity) || nativeACL(pin.file) != nil {
			return ErrUnsafeTarget
		}
		if pin.parent != nil {
			var stat unix.Stat_t
			if unix.Fstatat(int(pin.parent.Fd()), pin.name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameDirectory(statIdentity(&stat), identity) {
				return ErrUnsafeTarget
			}
		}
	}
	return nil
}

func (directory *nativeDirectory) close() error {
	var result error
	for index := len(directory.owned) - 1; index >= 0; index-- {
		result = errors.Join(result, directory.owned[index].Close())
	}
	directory.owned = nil
	return result
}

func nativeName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

func (directory *nativeDirectory) child(name string, create bool) (*nativeDirectory, error) {
	if !nativeName(name) || directory.check() != nil {
		return nil, ErrUnsafeTarget
	}
	if create {
		if err := unix.Mkdirat(int(directory.file.Fd()), name, 0o700); err != nil {
			return nil, ErrConflict
		}
		if err := directory.sync(); err != nil {
			return nil, err
		}
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, ErrTargetAbsent
	}
	if err != nil {
		return nil, ErrUnsafeTarget
	}
	file := os.NewFile(uintptr(descriptor), "configuration private directory")
	identity, err := nativeTrustDirectory(file, true, create)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	pins := append(append([]nativePin(nil), directory.pins...), nativePin{parent: directory.file, file: file, name: name, identity: identity})
	child := &nativeDirectory{file: file, pins: pins, owned: []*os.File{file}, identity: identity}
	if err := child.check(); err != nil {
		_ = child.close()
		return nil, err
	}
	return child, nil
}

func (directory *nativeDirectory) read(name string, limit int64) (nativeImage, []byte, error) {
	if !nativeName(name) || directory.check() != nil {
		return nativeImage{}, nil, ErrUnsafeTarget
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nativeImage{}, nil, nil
	}
	if err != nil {
		return nativeImage{}, nil, ErrUnsafeTarget
	}
	file := os.NewFile(uintptr(descriptor), "private configuration file")
	defer file.Close()
	var before unix.Stat_t
	err = unix.Fstat(int(file.Fd()), &before)
	identity := statIdentity(&before)
	if err != nil || identity.Mode&unix.S_IFMT != unix.S_IFREG || identity.Owner != uint32(os.Geteuid()) || identity.Links != 1 || identity.Mode&0o022 != 0 || identity.Size < 0 || identity.Size > limit || nativeACL(file) != nil {
		return nativeImage{}, nil, ErrUnsafeTarget
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nativeImage{}, nil, ErrCapacity
	}
	var after unix.Stat_t
	err = unix.Fstat(int(file.Fd()), &after)
	final := statIdentity(&after)
	var entry unix.Stat_t
	if err != nil || final != identity || before.Mtim != after.Mtim || before.Ctim != after.Ctim || unix.Fstatat(int(directory.file.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || statIdentity(&entry) != identity || directory.check() != nil {
		clear(data)
		return nativeImage{}, nil, ErrConflict
	}
	return nativeImage{Present: true, Identity: identity, SHA256: sha256.Sum256(data)}, data, nil
}

func (directory *nativeDirectory) validateSessionFiles() error {
	if directory.check() != nil {
		return ErrUnsafeTarget
	}
	for _, name := range []string{"client.db", "client.db-wal", "client.db-shm", "client.db-journal", "client.db.install.lock"} {
		descriptor, err := unix.Openat(int(directory.file.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return ErrUnsafeTarget
		}
		file := os.NewFile(uintptr(descriptor), "configuration session state")
		identity, err := nativeStat(file)
		unsafe := err != nil || identity.Mode&unix.S_IFMT != unix.S_IFREG || identity.Owner != uint32(os.Geteuid()) || identity.Links != 1 || identity.Mode&0o022 != 0 || nativeACL(file) != nil
		closeErr := file.Close()
		if unsafe || closeErr != nil {
			return ErrUnsafeTarget
		}
	}
	return nil
}

func (directory *nativeDirectory) syncImage(name string, expected nativeImage) error {
	actual, data, err := directory.read(name, 256<<10)
	clear(data)
	if err != nil || actual != expected || !actual.Present {
		return ErrConflict
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrUnsafeTarget
	}
	file := os.NewFile(uintptr(descriptor), "configuration publication")
	defer file.Close()
	identity, err := nativeStat(file)
	if err != nil || identity != expected.Identity {
		return ErrConflict
	}
	if err := file.Sync(); err != nil {
		return ErrOutcomeUnknown
	}
	return directory.check()
}

func (directory *nativeDirectory) write(name string, data []byte) (nativeImage, error) {
	if !nativeName(name) || directory.check() != nil {
		return nativeImage{}, ErrUnsafeTarget
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nativeImage{}, ErrCapacity
	}
	file := os.NewFile(uintptr(descriptor), "private configuration stage")
	defer file.Close()
	if unix.Fchmod(descriptor, 0o600) != nil {
		return nativeImage{}, ErrUnsafeTarget
	}
	if _, err := file.Write(data); err != nil {
		return nativeImage{}, ErrCapacity
	}
	if err := file.Sync(); err != nil {
		return nativeImage{}, ErrOutcomeUnknown
	}
	identity, err := nativeStat(file)
	if err != nil || directory.sync() != nil {
		return nativeImage{}, ErrOutcomeUnknown
	}
	return nativeImage{Present: true, Identity: identity, SHA256: sha256.Sum256(data)}, nil
}

func (directory *nativeDirectory) entries() ([]string, error) {
	if directory.check() != nil {
		return nil, ErrUnsafeTarget
	}
	descriptor, err := unix.Openat(int(directory.file.Fd()), ".", unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnsafeTarget
	}
	file := os.NewFile(uintptr(descriptor), "private configuration records")
	defer file.Close()
	names, err := file.Readdirnames(8193)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > 8192 || directory.check() != nil {
		return nil, ErrStateInvalid
	}
	return names, nil
}

func (directory *nativeDirectory) sync() error {
	if directory.check() != nil {
		return ErrUnsafeTarget
	}
	if directory.file.Sync() != nil {
		return ErrOutcomeUnknown
	}
	return nil
}

func (directory *nativeDirectory) rename(name string, target *nativeDirectory, destination string) error {
	if !nativeName(name) || !nativeName(destination) || directory.check() != nil || target.check() != nil || directory.identity.Device != target.identity.Device {
		return ErrUnsafeTarget
	}
	if err := unix.Renameat2(int(directory.file.Fd()), name, int(target.file.Fd()), destination, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOENT) {
			return ErrConflict
		}
		return ErrOutcomeUnknown
	}
	return nil
}

func (directory *nativeDirectory) syncWith(other *nativeDirectory) error {
	return errors.Join(directory.sync(), other.sync())
}

func (directory *nativeDirectory) exchange(first, second string) error {
	if !nativeName(first) || !nativeName(second) || directory.check() != nil {
		return ErrUnsafeTarget
	}
	if unix.Renameat2(int(directory.file.Fd()), first, int(directory.file.Fd()), second, unix.RENAME_EXCHANGE) != nil {
		return ErrOutcomeUnknown
	}
	return nil
}

func (directory *nativeDirectory) removeOwn(name string, expected nativeImage) error {
	actual, data, err := directory.read(name, 4<<20)
	clear(data)
	if err != nil || actual != expected || !expected.Present {
		return ErrConflict
	}
	disposal := "remove-" + rand.Text()
	if err := directory.rename(name, directory, disposal); err != nil {
		return err
	}
	actual, data, err = directory.read(disposal, 4<<20)
	clear(data)
	if err != nil || actual != expected {
		_ = directory.rename(disposal, directory, name)
		return ErrConflict
	}
	if unix.Unlinkat(int(directory.file.Fd()), disposal, 0) != nil {
		return ErrOutcomeUnknown
	}
	return directory.sync()
}

func (directory *nativeDirectory) removeDirectory(name string, expected nativeIdentity) error {
	child, err := directory.child(name, false)
	if err != nil {
		return err
	}
	defer child.close()
	if !sameDirectory(child.identity, expected) {
		return ErrConflict
	}
	entries, err := child.entries()
	if err != nil || len(entries) != 0 {
		return ErrConflict
	}
	if unix.Unlinkat(int(directory.file.Fd()), name, unix.AT_REMOVEDIR) != nil {
		return ErrConflict
	}
	return directory.sync()
}

func (directory *nativeDirectory) reserve(size int) (nativeImage, error) {
	return directory.reserveAt("capacity", size)
}

func (directory *nativeDirectory) reserveAt(name string, size int) (nativeImage, error) {
	var filesystem unix.Statfs_t
	if unix.Fstatfs(int(directory.file.Fd()), &filesystem) != nil || uint64(filesystem.Bavail)*uint64(filesystem.Bsize) < uint64(size*2) {
		return nativeImage{}, ErrCapacity
	}
	data := bytes.Repeat([]byte{0}, size)
	return directory.write(name, data)
}
