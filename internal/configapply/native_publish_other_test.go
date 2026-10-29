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

func TestNativeOtherPlatformFailsBeforeFilesystem(test *testing.T) {
	root := test.TempDir()
	home := filepath.Join(root, "missing-home")
	stateRoot := filepath.Join(root, "missing-state")
	if engine, err := Open(test.Context(), home, stateRoot, bytes.Repeat([]byte{1}, 32)); engine != nil || !errors.Is(err, ErrUnsupportedPlatform) {
		test.Fatalf("unsupported platform fallback: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		test.Fatal("unsupported engine touched filesystem")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := Open(ctx, home, stateRoot, bytes.Repeat([]byte{1}, 32)); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation precedence lost")
	}
}
