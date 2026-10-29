package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"golang.org/x/sys/unix"
)

func TestClientCredentialKeyPrivatePersistentAndNotRecreated(test *testing.T) {
	home := test.TempDir()
	if err := clientkey.Initialize(test.Context(), home); err != nil {
		test.Fatal(err)
	}
	first, err := loadClientKey(test.Context(), home)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(first)
	second, err := loadClientKey(test.Context(), home)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(second)
	if len(first) != 32 || !bytes.Equal(first, second) {
		test.Fatal("client credential key changed")
	}
	path := filepath.Join(home, ".config", "skillhub", "client.key")
	if err := os.Chmod(path, 0o644); err != nil {
		test.Fatal(err)
	}
	if _, err := loadClientKey(test.Context(), home); err == nil {
		test.Fatal("readable key accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := loadClientKey(test.Context(), home); err == nil {
		test.Fatal("truncated key silently recreated")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, []byte("truncated")) {
		test.Fatalf("malformed existing key was changed: %v", err)
	}
}

func TestClientCredentialKeyRejectsLinks(test *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		test.Run(kind, func(test *testing.T) {
			home := test.TempDir()
			directory := filepath.Join(home, ".config", "skillhub")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			outside := filepath.Join(home, "unrelated.key")
			if err := os.WriteFile(outside, bytes.Repeat([]byte{21}, 32), 0o600); err != nil {
				test.Fatal(err)
			}
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			if err := link(outside, filepath.Join(directory, "client.key")); err != nil {
				test.Fatal(err)
			}
			if _, err := loadClientKey(test.Context(), home); err == nil {
				test.Fatal("linked key accepted")
			}
		})
	}
}

func TestClientCredentialKeyReplacementMustNotBlock(test *testing.T) {
	home := test.TempDir()
	directory := filepath.Join(home, ".config", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	keyPath := filepath.Join(directory, "client.key")
	replacementPath := filepath.Join(directory, "replacement.key")
	expected := bytes.Repeat([]byte{37}, 32)
	if err := os.WriteFile(keyPath, expected, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := syscall.Mkfifo(replacementPath, 0o600); err != nil {
		test.Fatal(err)
	}
	beforeKey, err := os.Lstat(keyPath)
	if err != nil {
		test.Fatal(err)
	}
	beforeReplacement, err := os.Lstat(replacementPath)
	if err != nil {
		test.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestClientCredentialKeyReplacementHelper$")
	child.Env = append(os.Environ(), "SKILLHUB_CLIENT_KEY_REPLACEMENT_HOME="+home, "GOMAXPROCS=2")
	output, err := child.CombinedOutput()
	if errors.Is(deadline.Err(), context.DeadlineExceeded) {
		test.Fatal("client key load blocked while a regular key was exchanged with a FIFO")
	}
	if err != nil {
		test.Fatalf("replacement subprocess: %v; %s", err, output)
	}
	for _, path := range []string{keyPath, replacementPath} {
		after, err := os.Lstat(path)
		if err != nil {
			test.Fatal(err)
		}
		if os.SameFile(beforeKey, after) {
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, expected) {
				test.Fatalf("existing key changed: %v", err)
			}
		} else if !os.SameFile(beforeReplacement, after) || after.Mode()&os.ModeNamedPipe == 0 {
			test.Fatal("malformed key entry was replaced")
		}
	}
}

func TestClientCredentialKeyReplacementHelper(test *testing.T) {
	home := os.Getenv("SKILLHUB_CLIENT_KEY_REPLACEMENT_HOME")
	if home == "" {
		test.Skip("isolated subprocess only")
	}
	directory := filepath.Join(home, ".config", "skillhub")
	keyPath := filepath.Join(directory, "client.key")
	replacementPath := filepath.Join(directory, "replacement.key")
	ctx, cancel := context.WithCancel(test.Context())
	var swapping sync.WaitGroup
	var replacementErr error
	swapping.Go(func() {
		for ctx.Err() == nil {
			if err := unix.Renameat2(unix.AT_FDCWD, keyPath, unix.AT_FDCWD, replacementPath, unix.RENAME_EXCHANGE); err != nil {
				replacementErr = err
				return
			}
		}
	})
	defer func() {
		cancel()
		swapping.Wait()
		if replacementErr != nil {
			test.Fatal(replacementErr)
		}
	}()
	for range 1000 {
		key, err := loadClientKey(test.Context(), home)
		if err == nil && !bytes.Equal(key, bytes.Repeat([]byte{37}, 32)) {
			test.Error("replacement returned a different client key")
		}
		clear(key)
	}
}
