package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

func publishDirectory(parent, source, destination string) error {
	return publishDirectoryChecked(parent, source, destination, nil)
}

func publishDirectoryChecked(parent, source, destination string, expected fs.FileInfo) error {
	for _, name := range []string{source, destination} {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("directory name must be a single component")
		}
	}
	if source == destination {
		return fmt.Errorf("source and destination must differ")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("open installation directory: %w", err)
	}
	defer root.Close()
	info, err := root.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect stage: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("stage must be a directory")
	}
	if expected != nil && !os.SameFile(info, expected) {
		return fmt.Errorf("stage identity changed before publication")
	}
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open installation directory handle: %w", err)
	}
	defer directory.Close()
	if err := renameNoReplace(directory, parent, source, destination); err != nil {
		return fmt.Errorf("publish installation: %w", err)
	}
	if expected != nil {
		published, err := root.Lstat(destination)
		if err != nil || !published.IsDir() || !os.SameFile(expected, published) {
			return fmt.Errorf("published directory identity does not match stage")
		}
		if _, err := root.Lstat(source); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stage path still exists after publication")
		}
	}
	return nil
}
