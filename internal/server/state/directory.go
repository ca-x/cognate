package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

func PrepareDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("server data directory must be absolute and clean")
	}
	parent := path
	var missing []string
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(parent) == parent {
			return fmt.Errorf("inspect server data directory")
		}
		missing = append(missing, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
	if err := validateServerAncestor(parent); err != nil {
		return err
	}
	if len(missing) == 0 {
		return validateServerDirectory(path)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("open server data directory ancestor")
	}
	defer func() { _ = root.Close() }()
	for _, name := range slices.Backward(missing) {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create server data directory")
		}
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("server data directory is not a plain directory")
		}
		child, err := root.OpenRoot(name)
		if err != nil {
			return fmt.Errorf("open server data directory")
		}
		opened, err := child.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = child.Close()
			return fmt.Errorf("server data directory changed while opening")
		}
		if err := validateServerDirectoryInfo(opened); err != nil {
			_ = child.Close()
			return err
		}
		_ = root.Close()
		root = child
	}
	return validateServerDirectory(path)
}

func OpenPrivateDirectory(path string) (*os.Root, error) {
	if err := PrepareDirectory(path); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect server private directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open server private directory")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("server private directory changed while opening")
	}
	if err := validateServerDirectoryInfo(opened); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}
