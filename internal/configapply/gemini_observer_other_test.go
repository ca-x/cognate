//go:build !linux

package configapply

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGeminiUnsupportedPlatformBeforePath(test *testing.T) {
	home := filepath.Join(test.TempDir(), "not-created")
	if engine, err := OpenGemini(test.Context(), home, filepath.Join(home, "state"), bytes.Repeat([]byte{3}, 32)); err != ErrGeminiUnsupported || engine != nil {
		test.Fatal("unsupported platform did not hard-close")
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		test.Fatal("unsupported platform accessed target")
	}
}
