package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func plannedProjectionPath(path string) (string, string, error) {
	if err := validateProjectionPath(path); err != nil {
		return "", "", err
	}
	parent := filepath.Dir(path)
	for current := parent; ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			info, err := os.Stat(resolved)
			if err != nil || !info.IsDir() {
				return "", "", fmt.Errorf("operation target ancestor must be a directory")
			}
			remainder, err := filepath.Rel(current, parent)
			if err != nil {
				return "", "", fmt.Errorf("resolve operation target: %w", err)
			}
			canonical := filepath.Join(resolved, remainder, filepath.Base(path))
			return canonical, projectionPathKey(canonical), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("resolve operation target ancestor: %w", err)
		}
		if _, statErr := os.Lstat(current); statErr == nil {
			return "", "", fmt.Errorf("operation target ancestor cannot be resolved: %w", err)
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return "", "", fmt.Errorf("inspect operation target ancestor: %w", statErr)
		}
		if filepath.Dir(current) == current {
			return "", "", fmt.Errorf("operation target has no existing ancestor")
		}
	}
}
