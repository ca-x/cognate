package runtimegen

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (offline *OfflineRoot) WithLegacyWorkerFiles(ctx context.Context, consume func(root, lock *os.File) error) error {
	if consume == nil {
		return ErrInvalid
	}
	return offline.WithWorkerFiles(ctx, func(root, lock *os.File) error {
		if err := legacyEntries(ctx, root, false); err != nil {
			return err
		}
		return consume(root, lock)
	})
}

func CheckLegacyLayout(ctx context.Context, root string) error {
	if ctx == nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := os.Lstat(root)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	file, err := os.Open(root)
	if err != nil {
		return ErrInvalid
	}
	opened, statError := file.Stat()
	readError := legacyEntries(ctx, file, false)
	closeError := file.Close()
	if closeError != nil {
		return ErrUncertain
	}
	after, err := os.Lstat(root)
	if statError != nil || err != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return ErrInvalid
	}
	return readError
}

func reservedLegacyName(name string) bool {
	if name == ".runtime.lock" || name == ".runtime-fences" {
		return false
	}
	switch name {
	case "runtime.current", "generations", "restore", "layout.intent", "switch.intent", "rollback.intent":
		return true
	}
	return strings.HasPrefix(name, "runtime.") || strings.HasPrefix(name, ".runtime.") || strings.HasPrefix(name, ".runtime-") || strings.HasPrefix(name, "restore.") || strings.HasPrefix(name, ".restore")
}

func legacyEntries(ctx context.Context, root *os.File, fresh bool) error {
	if _, err := root.Seek(0, io.SeekStart); err != nil {
		return ErrInvalid
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, err := root.Readdirnames(32)
		for _, name := range names {
			if reservedLegacyName(name) || fresh && name != ".runtime.lock" && name != ".runtime-fences" {
				return ErrNeedsAttention
			}
		}
		if errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		if err != nil {
			return ErrInvalid
		}
	}
}
