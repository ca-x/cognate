package state_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/server/state"
)

func TestInitializeMasterKeyCreatesAndPreserves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base := privateDirectory(t)
	path := filepath.Join(base, "keys", "master.key")
	data := filepath.Join(base, "data")
	first, err := state.InitializeMasterKey(t.Context(), path, data)
	if err != nil || len(first) != 32 {
		t.Fatalf("initialize: %v", err)
	}
	defer clear(first)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(data, "server.db"), []byte("existing database"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := state.InitializeMasterKey(t.Context(), path, data)
	defer clear(second)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("restart changed key: %v", err)
	}
}

func TestInitializeMasterKeyRefusesExistingStateWithoutKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base := privateDirectory(t)
	path := filepath.Join(base, "keys", "master.key")
	data := filepath.Join(base, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "server.db"), []byte("existing database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.InitializeMasterKey(t.Context(), path, data); err == nil {
		t.Fatal("recreated lost key for existing state")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("failed initialization published key")
	}
}

func TestInitializeMasterKeyConcurrent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base := privateDirectory(t)
	path := filepath.Join(base, "master.key")
	data := filepath.Join(base, "data")
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			key, err := state.InitializeMasterKey(t.Context(), path, data)
			defer clear(key)
			if err != nil {
				t.Error(err)
				return
			}
			stored, err := state.ReadMasterKey(path)
			defer clear(stored)
			if err != nil || !bytes.Equal(key, stored) {
				t.Errorf("concurrent key mismatch: %v", err)
			}
		})
	}
	workers.Wait()
}

func TestInitializeMasterKeyRejectsInvalidExistingKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	for _, scenario := range []struct {
		name string
		size int
		mode os.FileMode
	}{
		{name: "empty", size: 0, mode: 0o600},
		{name: "short", size: 31, mode: 0o600},
		{name: "long", size: 33, mode: 0o600},
		{name: "public", size: 32, mode: 0o644},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := privateDirectory(t)
			path := filepath.Join(base, "master.key")
			original := bytes.Repeat([]byte{23}, scenario.size)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, scenario.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := state.InitializeMasterKey(t.Context(), path, filepath.Join(base, "data")); err == nil {
				t.Fatal("accepted invalid existing key")
			}
			stored, err := os.ReadFile(path)
			defer clear(stored)
			if err != nil || !bytes.Equal(stored, original) {
				t.Fatal("replaced invalid existing key")
			}
		})
	}
}

func TestInitializeMasterKeyRejectsLinkedKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			base := privateDirectory(t)
			target := filepath.Join(base, "original.key")
			path := filepath.Join(base, "master.key")
			original := bytes.Repeat([]byte{41}, 32)
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			link := os.Symlink
			if kind == "hardlink" {
				link = os.Link
			}
			if err := link(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := state.InitializeMasterKey(t.Context(), path, filepath.Join(base, "data")); err == nil {
				t.Fatal("accepted linked master key")
			}
			stored, err := os.ReadFile(target)
			defer clear(stored)
			if err != nil || !bytes.Equal(stored, original) {
				t.Fatal("linked target changed")
			}
		})
	}
}

func TestInitializeMasterKeyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := state.InitializeMasterKey(ctx, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled initialization: %v", err)
	}
}

func TestMasterKeyRejectsLazyCatWritableAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base := privateDirectory(t)
	parent := filepath.Join(base, "var")
	keys := filepath.Join(parent, "cognate", "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(keys, "master.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{13}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadMasterKey(path); err == nil || !strings.Contains(err.Error(), "ancestor is writable") {
		t.Fatalf("missing actionable ancestor rejection: %v", err)
	}
	if _, err := state.InitializeMasterKey(t.Context(), path, filepath.Join(parent, "cognate", "data")); err == nil {
		t.Fatal("initialization bypassed unsafe ancestor")
	}
	stored, err := os.ReadFile(path)
	defer clear(stored)
	if err != nil || !bytes.Equal(stored, bytes.Repeat([]byte{13}, 32)) {
		t.Fatal("existing key changed")
	}
}
