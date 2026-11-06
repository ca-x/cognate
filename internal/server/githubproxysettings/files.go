package githubproxysettings

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/server/state"
)

const maxSettingsBytes = 16 << 10

type atomicFile interface {
	io.Writer
	Sync() error
	Close() error
}

type atomicFiles interface {
	Create(string) (atomicFile, error)
	Rename(string, string) error
	Remove(string) error
	Sync() error
}

type rootFiles struct {
	root *os.Root
}

func (files rootFiles) Create(name string) (atomicFile, error) {
	return files.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func (files rootFiles) Rename(previous, next string) error { return files.root.Rename(previous, next) }

func (files rootFiles) Remove(name string) error { return files.root.Remove(name) }

func (files rootFiles) Sync() error {
	directory, err := files.root.Open(".")
	if err != nil {
		return ErrUnavailable
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func (manager *Manager) acquireOwner(ctx context.Context) error {
	file, err := manager.root.OpenFile(lockName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if err := file.Close(); err != nil {
			return ErrUnavailable
		}
	} else if !errors.Is(err, os.ErrExist) {
		return ErrUnavailable
	}
	before, err := manager.root.Lstat(lockName)
	if err != nil || !privateFileInfo(before) {
		return ErrUnavailable
	}
	owner, err := filelock.Acquire(ctx, filepath.Join(manager.root.Name(), lockName))
	if err != nil {
		if err := contextError(ctx); err != nil {
			return err
		}
		return ErrOwned
	}
	manager.owner = owner
	opened, err := owner.Stat()
	after, statError := manager.root.Lstat(lockName)
	if err != nil || statError != nil || !privateFileInfo(opened) || !privateFileInfo(after) || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return ErrUnavailable
	}
	manager.lockInfo = opened
	return nil
}

func (manager *Manager) readPrivateSettings() ([]byte, bool, error) {
	if err := manager.checkDirectory(); err != nil {
		return nil, false, err
	}
	before, err := manager.root.Lstat(settingsName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !privateFileInfo(before) {
		return nil, false, ErrUnavailable
	}
	encoded, err := state.ReadPrivateFile(filepath.Join(manager.root.Name(), settingsName), maxSettingsBytes)
	if err != nil {
		return nil, false, ErrUnavailable
	}
	after, err := manager.root.Lstat(settingsName)
	if err != nil || !privateFileInfo(after) || !os.SameFile(before, after) || manager.checkDirectory() != nil {
		clear(encoded)
		return nil, false, ErrUnavailable
	}
	return encoded, true, nil
}

func (manager *Manager) checkDirectory() error {
	before, err := os.Lstat(manager.root.Name())
	if err != nil || !os.SameFile(manager.identity, before) {
		return ErrUnavailable
	}
	parent, err := state.OpenPrivateDirectory(filepath.Dir(manager.root.Name()))
	if err != nil {
		return ErrUnavailable
	}
	if err := parent.Close(); err != nil {
		return ErrUnavailable
	}
	current, err := state.OpenPrivateDirectory(manager.root.Name())
	if err != nil {
		return ErrUnavailable
	}
	identity, statError := current.Stat(".")
	closeError := current.Close()
	lockInfo, lockError := manager.root.Lstat(lockName)
	if statError != nil || closeError != nil || !os.SameFile(manager.identity, identity) || lockError != nil || !privateFileInfo(lockInfo) || !os.SameFile(manager.lockInfo, lockInfo) {
		return ErrUnavailable
	}
	return nil
}

func writePrivateAtomic(ctx context.Context, files atomicFiles, data []byte) (uncertain bool, resultError error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	name := ".github-proxy-" + rand.Text() + ".tmp"
	file, err := files.Create(name)
	if err != nil {
		return false, ErrUnavailable
	}
	commitAttempted := false
	defer func() {
		if err := files.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			if commitAttempted {
				uncertain, resultError = true, errors.Join(ErrUncertain, resultError)
			} else if resultError == nil {
				resultError = ErrUnavailable
			}
		}
	}()
	written, writeError := file.Write(data)
	if writeError == nil && written != len(data) {
		writeError = io.ErrShortWrite
	}
	if writeError == nil {
		writeError = file.Sync()
	}
	closeError := file.Close()
	if writeError != nil || closeError != nil {
		return false, ErrUnavailable
	}
	if err := contextError(ctx); err != nil {
		return false, err
	}
	commitAttempted = true
	if err := files.Rename(name, settingsName); err != nil {
		return true, ErrUncertain
	}
	if err := files.Sync(); err != nil {
		return true, ErrUncertain
	}
	if err := contextError(ctx); err != nil {
		return true, errors.Join(ErrUncertain, err)
	}
	return false, nil
}
