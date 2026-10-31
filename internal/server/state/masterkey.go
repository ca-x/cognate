package state

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/server/runtimegen"
)

func ReadMasterKey(path string) ([]byte, error) {
	key, err := ReadPrivateFile(path, 32)
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if len(key) != 32 {
		clear(key)
		return nil, fmt.Errorf("master key must be a private file containing exactly 32 raw bytes")
	}
	return key, nil
}

func InitializeMasterKey(ctx context.Context, path, dataDirectory string) ([]byte, error) {
	return initializeMasterKey(ctx, path, dataDirectory, nil)
}

func initializeMasterKey(ctx context.Context, path, dataDirectory string, runtime *runtimegen.Runtime) (key []byte, resultErr error) {
	if ctx == nil {
		return nil, runtimegen.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime != nil {
		if err := runtime.CheckLegacy(ctx, dataDirectory); err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("master key path must be absolute and clean")
	}
	if err := PrepareDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("prepare master key directory: %w", err)
	}
	lockPath := path + ".init.lock"
	if _, err := inspectServerFile(lockPath); err != nil {
		return nil, fmt.Errorf("unsafe master key initialization lock")
	}
	lock, err := filelock.Acquire(ctx, lockPath)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire master key initialization lock")
	}
	defer func() {
		if err := lock.Close(); err != nil {
			clear(key)
			key = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("cannot release master key initialization lock"))
		}
	}()
	if exists, err := inspectServerFile(path); err != nil {
		return nil, fmt.Errorf("unsafe existing master key")
	} else if exists {
		return ReadMasterKey(path)
	}
	initialize := func() error {
		if err := publishMasterKey(path); err != nil {
			return err
		}
		var err error
		key, err = ReadMasterKey(path)
		return err
	}
	if runtime != nil {
		if err := runtime.WithFreshRoot(ctx, dataDirectory, initialize); err != nil {
			clear(key)
			return nil, err
		}
		return key, nil
	}
	if err := PrepareDirectory(dataDirectory); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dataDirectory)
	if err != nil || len(entries) != 0 {
		return nil, fmt.Errorf("master key missing with existing or unreadable server state; restore the original key, do not regenerate it")
	}
	if err := initialize(); err != nil {
		return nil, err
	}
	return key, nil
}

func InitializeMasterKeyWithRuntime(ctx context.Context, path, dataDirectory string, runtime *runtimegen.Runtime) ([]byte, error) {
	if runtime == nil {
		return nil, runtimegen.ErrInvalid
	}
	return initializeMasterKey(ctx, path, dataDirectory, runtime)
}

func publishMasterKey(path string) error {
	candidate, err := os.CreateTemp(filepath.Dir(path), ".master-key-*")
	if err != nil {
		return fmt.Errorf("cannot create master key candidate")
	}
	defer func() { _ = os.Remove(candidate.Name()) }()
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		_ = candidate.Close()
		return fmt.Errorf("cannot generate master key")
	}
	_, writeErr := candidate.Write(key)
	syncErr := candidate.Sync()
	closeErr := candidate.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		return fmt.Errorf("cannot persist master key candidate")
	}
	if err := os.Link(candidate.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("cannot publish master key without overwriting")
	}
	if err := os.Remove(candidate.Name()); err != nil {
		return fmt.Errorf("master key published but candidate cleanup failed")
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("cannot open master key directory for sync")
	}
	if errors.Join(directory.Sync(), directory.Close()) != nil {
		return fmt.Errorf("master key published but directory sync failed")
	}
	return nil
}

func ReadPrivateFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > 1<<20 {
		return nil, fmt.Errorf("invalid private file size limit")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("private file path must be absolute and clean")
	}
	if err := validateServerDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("private file directory must be private and trusted: %w", err)
	}
	if exists, err := inspectServerFile(path); err != nil || !exists {
		return nil, fmt.Errorf("private file must be an owned regular file with private permissions")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect private file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open private file")
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		_ = file.Close()
		return nil, fmt.Errorf("private file changed while opening")
	}
	key, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil || len(key) == 0 || int64(len(key)) > maxBytes {
		clear(key)
		return nil, fmt.Errorf("private file is unreadable, empty or exceeds its size limit")
	}
	return key, nil
}
