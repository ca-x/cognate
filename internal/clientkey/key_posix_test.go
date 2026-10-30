//go:build linux || darwin

package clientkey

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
)

func posixKeyHome(test *testing.T) string {
	test.Helper()
	parent := os.Getenv("COGNATE_KEY_CORE_TEST_PARENT")
	if runtime.GOOS == "darwin" && os.Getenv("COGNATE_KEY_CORE_APFS_REQUIRED") == "1" && parent == "" {
		test.Fatal("qualification requires an explicit APFS fixture")
	}
	var home string
	if parent == "" {
		home = test.TempDir()
	} else {
		resolved, err := filepath.EvalSymlinks(parent)
		if err != nil || resolved != parent || !filepath.IsAbs(parent) {
			test.Fatal("qualification parent is not canonical")
		}
		home, err = os.MkdirTemp(parent, "key-core-")
		if err != nil {
			test.Fatal("qualification home unavailable")
		}
		test.Cleanup(func() {
			if os.RemoveAll(home) != nil {
				test.Error("qualification home cleanup failed")
			}
		})
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || os.Chmod(canonical, 0700) != nil {
		test.Fatal("canonical private home unavailable")
	}
	return canonical
}

func posixKeyFile(test *testing.T, path string, data []byte) {
	test.Helper()
	if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, data, 0600) != nil {
		test.Fatal("private fixture file unavailable")
	}
}

func posixKeyRefusal(test *testing.T, home string, expected error) {
	test.Helper()
	key, err := LoadExisting(test.Context(), home)
	defer clear(key)
	if key != nil || !errors.Is(err, expected) || !errors.Is(Initialize(test.Context(), home), expected) {
		test.Fatal("unsafe fixture did not refuse without returning a key")
	}
}

func TestClientKeyPOSIXLifecycle(test *testing.T) {
	home := posixKeyHome(test)
	keyPath := filepath.Join(home, ".config", "skillhub", "client.key")
	key, err := LoadExisting(test.Context(), home)
	clear(key)
	if key != nil || !errors.Is(err, ErrInitializationRequired) {
		test.Fatal("fresh key did not require explicit initialization")
	}
	if _, err := os.Lstat(keyPath); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("fresh load generated a key")
	}
	if Initialize(test.Context(), home) != nil {
		test.Fatal("explicit initialization failed")
	}
	first, err := LoadExisting(test.Context(), home)
	defer clear(first)
	if err != nil || len(first) != 32 || Initialize(test.Context(), home) != nil {
		test.Fatal("initialized key could not be reused")
	}
	second, err := LoadExisting(test.Context(), home)
	defer clear(second)
	if err != nil || !bytes.Equal(first, second) {
		test.Fatal("idempotent initialization changed the key")
	}
	if os.Remove(keyPath) != nil {
		test.Fatal("controlled key loss failed")
	}
	posixKeyRefusal(test, home, ErrRecoveryRequired)
	if _, err := os.Lstat(keyPath); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("lost key was regenerated")
	}
	legacy := posixKeyHome(test)
	original := bytes.Repeat([]byte{0x51}, 32)
	defer clear(original)
	posixKeyFile(test, filepath.Join(legacy, ".config", "skillhub", "client.key"), original)
	enrolled, err := LoadExisting(test.Context(), legacy)
	defer clear(enrolled)
	if err != nil || !bytes.Equal(original, enrolled) {
		test.Fatal("valid pre-marker key was not preserved")
	}
}

func TestClientKeyPOSIXRefusals(test *testing.T) {
	for _, name := range []string{"client.key", "client-key-use.v1", "client-key.init.lock", "client.db.install.lock", "client.db"} {
		for _, kind := range []string{"symlink", "hardlink", "fifo", "public"} {
			test.Run(name+"/"+kind, func(test *testing.T) {
				home := posixKeyHome(test)
				path := filepath.Join(home, ".config", "skillhub", name)
				if name == "client.db" || name == "client.db.install.lock" {
					path = filepath.Join(home, ".local", "share", "skillhub", name)
				}
				if os.MkdirAll(filepath.Dir(path), 0700) != nil {
					test.Fatal("fixture parent unavailable")
				}
				target := filepath.Join(home, "preserved-target")
				original := bytes.Repeat([]byte{0x63}, 32)
				defer clear(original)
				posixKeyFile(test, target, original)
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(target, path)
				case "hardlink":
					err = os.Link(target, path)
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				case "public":
					posixKeyFile(test, path, original)
					err = os.Chmod(path, 0666)
				}
				if err != nil {
					test.Fatal("unsafe fixture unavailable")
				}
				posixKeyRefusal(test, home, ErrUnavailable)
				preserved, err := os.ReadFile(target)
				defer clear(preserved)
				if err != nil || !bytes.Equal(original, preserved) {
					test.Fatal("unsafe fixture target was modified")
				}
			})
		}
	}
	for _, residual := range []string{"client.db", "client.db-wal", "retained-state"} {
		home := posixKeyHome(test)
		posixKeyFile(test, filepath.Join(home, ".local", "share", "skillhub", residual), []byte("synthetic residue"))
		posixKeyRefusal(test, home, ErrRecoveryRequired)
	}
	for _, component := range []string{".config", ".local", ".local/share/skillhub"} {
		home := posixKeyHome(test)
		path := filepath.Join(home, filepath.FromSlash(component))
		target := filepath.Join(home, "preserved-directory")
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.Mkdir(target, 0700) != nil || os.Symlink(target, path) != nil {
			test.Fatal("linked component fixture unavailable")
		}
		posixKeyRefusal(test, home, ErrUnavailable)
		if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
			test.Fatal("linked component target was changed")
		}
		unsafeHome := posixKeyHome(test)
		unsafe := filepath.Join(unsafeHome, filepath.FromSlash(component))
		if os.MkdirAll(unsafe, 0700) != nil || os.Chmod(unsafe, 0777) != nil {
			test.Fatal("unsafe component fixture unavailable")
		}
		posixKeyRefusal(test, unsafeHome, ErrUnavailable)
	}
	for _, size := range []int{0, 31, 33} {
		home := posixKeyHome(test)
		path := filepath.Join(home, ".config", "skillhub", "client.key")
		original := bytes.Repeat([]byte{0x62}, size)
		defer clear(original)
		posixKeyFile(test, path, original)
		posixKeyRefusal(test, home, ErrRecoveryRequired)
		preserved, err := os.ReadFile(path)
		defer clear(preserved)
		if err != nil || !bytes.Equal(original, preserved) {
			test.Fatal("malformed key was repaired")
		}
	}
	for _, marker := range []string{"", "{", `{"version":1,"version":1,"root":"default-client-state"}`, `{"version":2,"root":"default-client-state"}`, `{"Version":1,"root":"default-client-state"}`, strings.Repeat("x", 257)} {
		home := posixKeyHome(test)
		path := filepath.Join(home, ".config", "skillhub", "client-key-use.v1")
		posixKeyFile(test, path, []byte(marker))
		posixKeyRefusal(test, home, ErrRecoveryRequired)
		preserved, err := os.ReadFile(path)
		defer clear(preserved)
		if err != nil || string(preserved) != marker {
			test.Fatal("malformed marker was repaired")
		}
	}
}

func TestClientKeyPOSIXLockAndCancellation(test *testing.T) {
	home := posixKeyHome(test)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	key, err := LoadExisting(ctx, home)
	clear(key)
	if key != nil || !errors.Is(err, context.Canceled) {
		test.Fatal("pre-canceled operation admitted a key")
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		test.Fatal("pre-canceled operation changed the home")
	}
	if Initialize(test.Context(), home) != nil {
		test.Fatal("lock fixture initialization failed")
	}
	for _, relative := range []string{".config/skillhub/client-key.init.lock", ".local/share/skillhub/client.db.install.lock"} {
		lock, err := filelock.Acquire(test.Context(), filepath.Join(home, filepath.FromSlash(relative)))
		if err != nil {
			test.Fatal("physical lock unavailable")
		}
		ctx, cancel := context.WithTimeout(test.Context(), 80*time.Millisecond)
		key, err := LoadExisting(ctx, home)
		cancel()
		clear(key)
		closeErr := lock.Close()
		if closeErr != nil || key != nil || !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("key load bypassed a physical lock or lost cancellation")
		}
	}
}

func TestClientKeyPOSIXConcurrent(test *testing.T) {
	home := posixKeyHome(test)
	results := make(chan []byte, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			if Initialize(test.Context(), home) != nil {
				test.Error("concurrent initialization failed")
				results <- nil
				return
			}
			key, err := LoadExisting(test.Context(), home)
			if err != nil {
				test.Error("concurrent key load failed")
			}
			results <- key
		})
	}
	workers.Wait()
	close(results)
	var first []byte
	defer func() { clear(first) }()
	for key := range results {
		if first == nil {
			first = bytes.Clone(key)
		}
		if len(key) != 32 || !bytes.Equal(first, key) {
			test.Error("concurrent initializations did not converge")
		}
		clear(key)
	}
}
