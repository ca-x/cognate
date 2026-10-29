//go:build darwin

package clientkey

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinExecutionCoreExplicitInitialization(test *testing.T) {
	home, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(home, 0700) != nil {
		test.Fatal("DARWIN_INFRA canonical private home")
	}
	key, err := LoadExisting(test.Context(), home)
	clear(key)
	if !errors.Is(err, ErrInitializationRequired) {
		if errors.Is(err, ErrUnsupported) {
			test.Log("DARWIN_BASELINE_REJECTION code=core-unsupported")
		}
		test.Fatal("Darwin core must require explicit initialization")
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "skillhub", "client.key")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("implicit key creation")
	}
	if err := Initialize(test.Context(), home); err != nil {
		test.Fatal("explicit initialization failed")
	}
	first, err := LoadExisting(test.Context(), home)
	defer clear(first)
	if err != nil || len(first) != 32 {
		test.Fatal("initialized key unavailable")
	}
	if err := Initialize(test.Context(), home); err != nil {
		test.Fatal("repeat initialization failed")
	}
	second, err := LoadExisting(test.Context(), home)
	defer clear(second)
	if err != nil || !bytes.Equal(first, second) {
		test.Fatal("reopen changed key")
	}
	path := filepath.Join(home, ".config", "skillhub", "client.key")
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		test.Fatal("private key metadata invalid")
	}
	if err := os.Remove(path); err != nil {
		test.Fatal("synthetic key removal failed")
	}
	lost, err := LoadExisting(test.Context(), home)
	clear(lost)
	if lost != nil || !errors.Is(err, ErrRecoveryRequired) || !errors.Is(Initialize(test.Context(), home), ErrRecoveryRequired) {
		test.Fatal("lost key did not require recovery")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("lost key regenerated")
	}
}
