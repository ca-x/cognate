//go:build darwin && arm64

package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/state"
)

func assertClientKeyHomeEmpty(test *testing.T, home string) {
	test.Helper()
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		test.Fatal("unsupported/invalid operation changed home")
	}
}

func darwinClientKeyHome(test *testing.T) string {
	test.Helper()
	home, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(home, 0o700) != nil {
		test.Fatal("private canonical caller fixture unavailable")
	}
	test.Setenv("HOME", home)
	return home
}

func darwinClientKeyRun(test *testing.T, output *bytes.Buffer, arguments ...string) error {
	test.Helper()
	command := New()
	command.SetArgs(arguments)
	command.SetOut(output)
	command.SetErr(output)
	return command.ExecuteContext(test.Context())
}

func darwinClientKeyNoBusiness(test *testing.T, home string) {
	test.Helper()
	for _, relative := range []string{".agents", ".local/share/skillhub/client.db", ".local/share/skillhub/client.db-wal", ".local/share/skillhub/client.db-shm"} {
		if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("caller admission reached business state")
		}
	}
}

func TestDarwinClientKeyExplicitInitialization(test *testing.T) {
	home := darwinClientKeyHome(test)
	var first []byte
	defer func() { clear(first) }()
	for range 2 {
		output := new(bytes.Buffer)
		if err := darwinClientKeyRun(test, output, "client-key", "init"); err != nil {
			test.Fatal("explicit caller initialization failed")
		}
		if output.String() != "本机客户端密钥已就绪（未初始化服务端会话）\n" {
			test.Fatal("initialization completion is not fixed")
		}
		key, err := clientkey.LoadExisting(test.Context(), home)
		if err != nil || len(key) != 32 || first != nil && !bytes.Equal(first, key) {
			clear(key)
			test.Fatal("caller changed initialized key")
		}
		clear(first)
		first = key
		darwinClientKeyNoBusiness(test, home)
	}
}

func TestDarwinClientKeyInstallationAdmission(test *testing.T) {
	for _, kind := range []string{"fresh", "initialized", "lost", "malformed", "public", "symlink", "hardlink", "residue", "legacy"} {
		test.Run(kind, func(test *testing.T) {
			home := darwinClientKeyHome(test)
			keyPath := filepath.Join(home, ".config", "skillhub", "client.key")
			expected := clientkey.ErrRecoveryRequired
			if kind == "fresh" {
				expected = clientkey.ErrInitializationRequired
			} else if kind == "residue" {
				if os.MkdirAll(filepath.Join(home, ".local/share/skillhub"), 0o700) != nil || os.WriteFile(filepath.Join(home, ".local/share/skillhub/unknown"), []byte("synthetic-residue"), 0o600) != nil {
					test.Fatal("residue fixture unavailable")
				}
			} else {
				if err := clientkey.Initialize(test.Context(), home); err != nil {
					test.Fatal("key fixture unavailable")
				}
				switch kind {
				case "initialized", "legacy":
					expected = clientkey.ErrUnsupported
					if kind == "legacy" {
						if os.Remove(filepath.Join(home, ".config/skillhub/client-key-use.v1")) != nil {
							test.Fatal("legacy fixture unavailable")
						}
					}
				case "lost":
					if os.Remove(keyPath) != nil {
						test.Fatal("lost fixture unavailable")
					}
				case "malformed":
					if os.WriteFile(keyPath, []byte("short-synthetic-key"), 0o600) != nil {
						test.Fatal("malformed fixture unavailable")
					}
				default:
					expected = clientkey.ErrUnavailable
					if kind == "public" {
						if os.Chmod(keyPath, 0o644) != nil {
							test.Fatal("public fixture unavailable")
						}
					} else {
						outside := filepath.Join(home, "unrelated-private-key")
						if os.Rename(keyPath, outside) != nil {
							test.Fatal("linked fixture unavailable")
						}
						link := os.Link
						if kind == "symlink" {
							link = os.Symlink
						}
						if link(outside, keyPath) != nil {
							test.Fatal("linked fixture unavailable")
						}
					}
				}
			}
			before, beforeErr := os.ReadFile(keyPath)
			defer clear(before)
			if kind == "initialized" && !errors.Is(admitDarwinInstallation(test.Context()), clientkey.ErrUnsupported) {
				test.Fatal("Darwin admission helper returned permission to execute")
			}
			var requests atomic.Int64
			endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.WriteHeader(http.StatusNotFound)
			}))
			defer endpoint.Close()
			for _, arguments := range [][]string{{"install-local", filepath.Join(home, "unread-source"), "--slug=demo"}, {"install-public", "--server", endpoint.URL, "--skill=synthetic-private-id"}} {
				output := new(bytes.Buffer)
				if err := darwinClientKeyRun(test, output, arguments...); !errors.Is(err, expected) {
					test.Fatal("installation admission classification mismatch")
				}
				if requests.Load() != 0 || output.Len() != 0 {
					test.Fatal("refused admission performed network or reported success")
				}
				after, afterErr := os.ReadFile(keyPath)
				changed := !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist)
				clear(after)
				if changed {
					test.Fatal("admission replaced or regenerated key")
				}
				darwinClientKeyNoBusiness(test, home)
			}
		})
	}
}

func TestClientKeyEntrypointsUnsupported(test *testing.T) {
	home := darwinClientKeyHome(test)
	if err := clientkey.Initialize(test.Context(), home); err != nil {
		test.Fatal("retained boundary fixture unavailable")
	}
	key, err := os.ReadFile(filepath.Join(home, ".config/skillhub/client.key"))
	if err != nil {
		test.Fatal("retained key unavailable")
	}
	defer clear(key)
	for _, arguments := range [][]string{{"pack", "sync"}, {"install-local", "unread", "--slug=demo"}, {"install-public", "--server", "http://127.0.0.1:1", "--skill=demo"}} {
		if !errors.Is(darwinClientKeyRun(test, new(bytes.Buffer), arguments...), clientkey.ErrUnsupported) {
			test.Fatal("unqualified product boundary opened")
		}
	}
	after, err := os.ReadFile(filepath.Join(home, ".config/skillhub/client.key"))
	defer clear(after)
	if err != nil || !bytes.Equal(key, after) {
		test.Fatal("closed product changed key")
	}
	darwinClientKeyNoBusiness(test, home)
}

func TestDarwinClientKeyExistingDatabaseUnchanged(test *testing.T) {
	home := darwinClientKeyHome(test)
	if clientkey.Initialize(test.Context(), home) != nil {
		test.Fatal("database fixture key unavailable")
	}
	path := filepath.Join(home, ".local/share/skillhub/client.db")
	store, err := state.Open(test.Context(), path)
	if err != nil {
		test.Fatal("actual SQLite unavailable")
	}
	if store.Close() != nil {
		test.Fatal("actual SQLite close failed")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		test.Fatal("SQLite bytes unavailable")
	}
	if !errors.Is(darwinClientKeyRun(test, new(bytes.Buffer), "install-local", "unread", "--slug=demo"), clientkey.ErrUnsupported) {
		test.Fatal("initialized installation became executable")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		test.Fatal("admission opened or mutated SQLite")
	}
	if _, err := loadClientKey(test.Context(), home); !errors.Is(err, clientkey.ErrUnsupported) {
		test.Fatal("shared loader enabled unrelated consumers")
	}
}

func TestDarwinClientKeyLockCancellation(test *testing.T) {
	home := darwinClientKeyHome(test)
	if clientkey.Initialize(test.Context(), home) != nil {
		test.Fatal("lock fixture key unavailable")
	}
	lock, err := filelock.Acquire(test.Context(), filepath.Join(home, ".local/share/skillhub/client.db.install.lock"))
	if err != nil {
		test.Fatal("physical lock unavailable")
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(test.Context(), 80*time.Millisecond)
	defer cancel()
	command := New()
	command.SetArgs([]string{"client-key", "init", "--timeout=100ms"})
	output := new(bytes.Buffer)
	command.SetOut(output)
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.DeadlineExceeded) || output.Len() != 0 {
		test.Fatal("physical lock did not cancel without success")
	}
	if strings.Contains(clientKeyErrorMessage(context.DeadlineExceeded), home) {
		test.Fatal("cancellation diagnostic exposed private identity")
	}
}
