package clientkey

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/state"
	"github.com/gofrs/flock"
)

var (
	ErrInitializationRequired = errors.New("client key initialization required")
	ErrRecoveryRequired       = errors.New("client key recovery required")
	ErrUnavailable            = errors.New("client key unavailable")
	ErrUnsupported            = errors.New("client key unsupported")
)

const markerBody = "{\"version\":1,\"root\":\"default-client-state\"}\n"

type directory struct {
	root     *os.Root
	parent   *os.Root
	name     string
	identity os.FileInfo
	private  bool
}

type admission struct {
	directories []directory
	config      *os.Root
	state       *os.Root
	keyLock     *flock.Flock
	installLock *flock.Flock
	configPath  string
	statePath   string
}

func LoadExisting(ctx context.Context, home string) ([]byte, error) {
	return access(ctx, home, false)
}

func Initialize(ctx context.Context, home string) error {
	key, err := access(ctx, home, true)
	clear(key)
	return err
}

func access(ctx context.Context, home string, initialize bool) (result []byte, resultErr error) {
	resources := &admission{}
	defer func() {
		if err := resources.close(); err != nil {
			resultErr = errors.Join(resultErr, ErrUnavailable)
		}
		if err := ctx.Err(); err != nil {
			resultErr = errors.Join(resultErr, ErrUnavailable, err)
		}
		if resultErr != nil {
			clear(result)
			result = nil
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || filepath.Dir(home) == home {
		return nil, ErrUnavailable
	}
	if err := resources.open(home); err != nil {
		return nil, err
	}
	if err := resources.lock(ctx); err != nil {
		return nil, err
	}
	if err := resources.verify(); err != nil {
		return nil, err
	}
	marker, err := readPrivate(resources.config, "client-key-use.v1", 256)
	if err != nil {
		return nil, err
	}
	if marker != nil && !validMarker(marker) {
		return nil, ErrRecoveryRequired
	}
	key, err := readPrivate(resources.config, "client.key", 32)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			clear(key)
		}
	}()
	if key == nil {
		if marker != nil {
			return nil, ErrRecoveryRequired
		}
		pristine, err := resources.pristine()
		if err != nil {
			return nil, err
		}
		if !pristine {
			return nil, ErrRecoveryRequired
		}
		if !initialize {
			return nil, ErrInitializationRequired
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, ErrUnavailable
		}
		if err := createPrivate(resources.config, "client.key", key); err != nil {
			clear(key)
			if !errors.Is(err, os.ErrExist) {
				return nil, ErrUnavailable
			}
			key, err = readPrivate(resources.config, "client.key", 32)
			if err != nil || key == nil {
				return nil, ErrRecoveryRequired
			}
		}
	}
	if len(key) != 32 {
		return nil, ErrRecoveryRequired
	}
	if err := resources.durable("client.key", key); err != nil {
		return nil, err
	}
	if marker == nil {
		if err := createPrivate(resources.config, "client-key-use.v1", []byte(markerBody)); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrUnavailable
		}
		marker, err = readPrivate(resources.config, "client-key-use.v1", 256)
		if err != nil {
			return nil, err
		}
		if !validMarker(marker) {
			return nil, ErrRecoveryRequired
		}
	}
	if err := resources.durable("client-key-use.v1", marker); err != nil {
		return nil, err
	}
	if err := resources.verify(); err != nil {
		return nil, err
	}
	return key, nil
}

func validMarker(data []byte) bool {
	var marker struct {
		Version int    `json:"version"`
		Root    string `json:"root"`
	}
	return len(data) <= 256 && json.Unmarshal(data, &marker, json.RejectUnknownMembers(true)) == nil && marker.Version == 1 && marker.Root == "default-client-state"
}

func trustedFile(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}

func trustedDirectory(info os.FileInfo, private bool) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if private {
		return attributes.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0o077 == 0
	}
	return (attributes.Uid == 0 || attributes.Uid == uint32(os.Geteuid())) && (info.Mode().Perm()&0o022 == 0 || attributes.Uid == 0 && info.Mode()&os.ModeSticky != 0)
}

func (resources *admission) open(home string) error {
	root, err := os.OpenRoot("/")
	if err != nil {
		return ErrUnavailable
	}
	info, err := root.Stat(".")
	resources.directories = append(resources.directories, directory{root: root, identity: info})
	if err != nil || !trustedDirectory(info, false) {
		return ErrUnavailable
	}
	for component := range strings.SplitSeq(strings.TrimPrefix(home, "/"), "/") {
		root, err = resources.child(root, component, false, false)
		if err != nil {
			return err
		}
	}
	homeRoot := root
	root, err = resources.child(homeRoot, ".config", false, true)
	if err != nil {
		return err
	}
	resources.config, err = resources.child(root, "skillhub", true, true)
	if err != nil {
		return err
	}
	root, err = resources.child(homeRoot, ".local", false, true)
	if err != nil {
		return err
	}
	root, err = resources.child(root, "share", false, true)
	if err != nil {
		return err
	}
	resources.state, err = resources.child(root, "skillhub", true, true)
	resources.configPath = filepath.Join(home, ".config", "skillhub")
	resources.statePath = filepath.Join(home, ".local", "share", "skillhub")
	return err
}

func (resources *admission) child(parent *os.Root, name string, private, create bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrUnavailable
		}
		info, err = parent.Lstat(name)
	}
	if err != nil || !trustedDirectory(info, private) {
		return nil, ErrUnavailable
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, ErrUnavailable
	}
	resources.directories = append(resources.directories, directory{root: child, parent: parent, name: name, identity: info, private: private})
	current, err := child.Stat(".")
	if err != nil || !os.SameFile(info, current) {
		return nil, ErrUnavailable
	}
	return child, nil
}

func (resources *admission) verify() error {
	for _, entry := range resources.directories {
		current, err := entry.root.Stat(".")
		if err != nil || !trustedDirectory(current, entry.private) || !os.SameFile(entry.identity, current) {
			return ErrUnavailable
		}
		if entry.parent != nil {
			linked, err := entry.parent.Lstat(entry.name)
			if err != nil || !trustedDirectory(linked, entry.private) || !os.SameFile(current, linked) {
				return ErrUnavailable
			}
		}
	}
	for _, entry := range []struct {
		root *os.Root
		name string
		lock *flock.Flock
	}{
		{resources.config, "client-key.init.lock", resources.keyLock}, {resources.state, "client.db.install.lock", resources.installLock},
	} {
		if entry.lock == nil {
			continue
		}
		info, err := entry.root.Lstat(entry.name)
		opened, statErr := entry.lock.Stat()
		if err != nil || statErr != nil || !trustedFile(info) || !trustedFile(opened) || !os.SameFile(info, opened) {
			return ErrUnavailable
		}
	}
	return nil
}

func (resources *admission) lock(ctx context.Context) error {
	var err error
	resources.keyLock, err = resources.acquire(ctx, resources.config, resources.configPath, "client-key.init.lock")
	if err != nil {
		return err
	}
	if err := inspectDatabase(resources.state); err != nil {
		return err
	}
	database := filepath.Join(resources.statePath, "client.db")
	resolved, err := state.ResolveDatabasePath(database)
	if err != nil || resolved != database {
		return ErrUnavailable
	}
	resources.installLock, err = resources.acquire(ctx, resources.state, resources.statePath, "client.db.install.lock")
	if err != nil {
		return err
	}
	if err := inspectDatabase(resources.state); err != nil {
		return err
	}
	resolved, err = state.ResolveDatabasePath(database)
	if err != nil || resolved != database {
		return ErrUnavailable
	}
	return nil
}

func inspectDatabase(root *os.Root) error {
	info, err := root.Lstat("client.db")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !safeDatabase(info) {
		return ErrUnavailable
	}
	return nil
}

func safeDatabase(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}

func (resources *admission) acquire(ctx context.Context, root *os.Root, path, name string) (*flock.Flock, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := createPrivate(root, name, nil); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrUnavailable
		}
		info, err = root.Lstat(name)
	}
	if err != nil || !trustedFile(info) {
		return nil, ErrUnavailable
	}
	if err := resources.verify(); err != nil {
		return nil, err
	}
	lock, err := filelock.Acquire(ctx, filepath.Join(path, name))
	if err != nil {
		return nil, errors.Join(ErrUnavailable, ctx.Err())
	}
	current, err := root.Lstat(name)
	opened, statErr := lock.Stat()
	if err != nil || statErr != nil || !trustedFile(current) || !trustedFile(opened) || !os.SameFile(info, current) || !os.SameFile(current, opened) {
		_ = lock.Close()
		return nil, ErrUnavailable
	}
	return lock, nil
}

func readPrivate(root *os.Root, name string, limit int64) (data []byte, resultErr error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !trustedFile(info) {
		return nil, ErrUnavailable
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() {
		if file.Close() != nil {
			resultErr = ErrUnavailable
		}
		if resultErr != nil {
			clear(data)
			data = nil
		}
	}()
	opened, err := file.Stat()
	if err != nil || !trustedFile(opened) || !os.SameFile(info, opened) {
		return nil, ErrUnavailable
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return data, ErrUnavailable
	}
	if int64(len(data)) > limit {
		return data, ErrRecoveryRequired
	}
	current, err := root.Lstat(name)
	if err != nil || !trustedFile(current) || !os.SameFile(opened, current) {
		return data, ErrUnavailable
	}
	return data, nil
}

func createPrivate(root *os.Root, name string, data []byte) (resultErr error) {
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if file.Close() != nil {
			resultErr = ErrUnavailable
		}
	}()
	info, err := file.Stat()
	if err != nil || !trustedFile(info) {
		return ErrUnavailable
	}
	count, err := file.Write(data)
	if err != nil || count != len(data) {
		return ErrUnavailable
	}
	current, err := root.Lstat(name)
	if err != nil || !trustedFile(current) || !os.SameFile(info, current) {
		return ErrUnavailable
	}
	return nil
}

func (resources *admission) durable(name string, expected []byte) (resultErr error) {
	root := resources.config
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		if file.Close() != nil {
			resultErr = ErrUnavailable
		}
	}()
	info, err := file.Stat()
	current, statErr := root.Lstat(name)
	if err != nil || statErr != nil || !trustedFile(info) || !trustedFile(current) || !os.SameFile(info, current) {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(len(expected)+1)))
	defer clear(data)
	if err != nil || !bytes.Equal(data, expected) {
		return ErrRecoveryRequired
	}
	if err := file.Sync(); err != nil {
		return ErrUnavailable
	}
	for index := len(resources.directories) - 1; index >= 0; index-- {
		parent, err := resources.directories[index].root.Open(".")
		if err != nil {
			return ErrUnavailable
		}
		syncErr, closeErr := parent.Sync(), parent.Close()
		if syncErr != nil || closeErr != nil {
			return ErrUnavailable
		}
	}
	current, err = root.Lstat(name)
	if err != nil || !trustedFile(current) || !os.SameFile(info, current) {
		return ErrUnavailable
	}
	return nil
}

func (resources *admission) pristine() (bool, error) {
	file, err := resources.state.Open(".")
	if err != nil {
		return false, ErrUnavailable
	}
	entries, readErr := file.ReadDir(2)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
		return false, ErrUnavailable
	}
	for _, entry := range entries {
		if entry.Name() != "client.db.install.lock" {
			return false, nil
		}
	}
	return true, nil
}

func (resources *admission) close() error {
	var failures []error
	if resources.installLock != nil {
		failures = append(failures, resources.installLock.Close())
	}
	if resources.keyLock != nil {
		failures = append(failures, resources.keyLock.Close())
	}
	for index := len(resources.directories) - 1; index >= 0; index-- {
		failures = append(failures, resources.directories[index].root.Close())
	}
	return errors.Join(failures...)
}
