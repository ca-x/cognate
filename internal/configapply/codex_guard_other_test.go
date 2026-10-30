//go:build !linux

package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexUnsupportedPlatformBeforeFilesystem(test *testing.T) {
	root := filepath.Join(test.TempDir(), "does-not-exist")
	if _, err := OpenCodex(test.Context(), filepath.Join(root, "home"), filepath.Join(root, "state"), bytes.Repeat([]byte{0x67}, 32)); !errors.Is(err, ErrCodexUnsupported) {
		test.Fatal("platform did not fail before filesystem")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("unsupported platform created files")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := OpenCodex(ctx, filepath.Join(root, "home"), filepath.Join(root, "state"), bytes.Repeat([]byte{0x67}, 32)); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation precedence")
	}
	if _, err := OpenCodex(test.Context(), "relative", filepath.Join(root, "state"), nil); !errors.Is(err, ErrCodexInvalid) {
		test.Fatal("base validation precedence")
	}
}
