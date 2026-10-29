//go:build !linux

package clientkey

import (
	"errors"
	"os"
	"testing"
)

func TestClientKeyUnsupported(test *testing.T) {
	home := test.TempDir()
	key, err := LoadExisting(test.Context(), home)
	if key != nil || !errors.Is(err, ErrUnsupported) || !errors.Is(Initialize(test.Context(), home), ErrUnsupported) {
		test.Fatal("unsupported platform admitted client key")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		test.Fatal("unsupported call mutated home")
	}
}
