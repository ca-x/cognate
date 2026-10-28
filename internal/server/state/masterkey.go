package state

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ReadMasterKey(path string) ([]byte, error) {
	key, err := ReadPrivateFile(path, 32)
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, fmt.Errorf("master key must be a private file containing exactly 32 raw bytes")
	}
	return key, nil
}

func ReadPrivateFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > 1<<20 {
		return nil, fmt.Errorf("invalid private file size limit")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("private file path must be absolute and clean")
	}
	if err := validateServerDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("private file directory must be private and trusted")
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
