//go:build linux

package runtimegen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLegacyGate(test *testing.T) {
	for _, name := range []string{"runtime.current", "runtime.future", ".runtime.future", ".runtime-future", "generations", "restore", "restore.future", ".restore", ".restore-future", "layout.intent", "switch.intent", "rollback.intent"} {
		for _, kind := range []string{"empty", "partial", "directory", "symlink", "fifo"} {
			test.Run(name+"/"+kind, func(test *testing.T) {
				root := privateRoot(test)
				lease, err := OpenRuntime(test.Context(), root)
				if err != nil {
					test.Fatal(err)
				}
				defer func() {
					if err := lease.Close(); err != nil {
						test.Error(err)
					}
				}()
				path := filepath.Join(root, name)
				switch kind {
				case "empty":
					err = os.WriteFile(path, nil, 0600)
				case "partial":
					err = os.WriteFile(path, []byte("unknown version PRIVATE"), 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "symlink":
					err = os.Symlink(filepath.Join(root, "missing"), path)
				case "fifo":
					err = unix.Mkfifo(path, 0600)
				}
				if err != nil {
					test.Fatal(err)
				}
				before, err := os.Lstat(path)
				if err != nil {
					test.Fatal(err)
				}
				for _, check := range []func(context.Context, string) error{CheckLegacyLayout, lease.CheckLegacy} {
					if err := check(test.Context(), root); !errors.Is(err, ErrNeedsAttention) {
						test.Fatal("unknown layout admitted", err)
					}
				}
				after, err := os.Lstat(path)
				if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() {
					test.Fatal("gate changed unknown entry")
				}
			})
		}
	}
	test.Run("legacy", func(test *testing.T) {
		root := privateRoot(test)
		lease, err := OpenRuntime(test.Context(), root)
		if err != nil {
			test.Fatal(err)
		}
		defer lease.Close()
		for _, name := range []string{"server.db", "server.db-wal", ".snapshot-old", ".source-refresh.lock", "configuration.json"} {
			if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
				test.Fatal(err)
			}
		}
		if err := lease.CheckLegacy(test.Context(), root); err != nil {
			test.Fatal("valid legacy rejected", err)
		}
		if err := CheckLegacyLayout(test.Context(), root); err != nil {
			test.Fatal(err)
		}
	})
}

func TestFreshRootProof(test *testing.T) {
	for _, variant := range []string{"fresh", "ordinary", "hidden", "fence-after-eof", "lock-replaced", "fence-replaced", "wrong-root", "closed", "cancel", "nil-callback", "callback-error"} {
		test.Run(variant, func(test *testing.T) {
			root := privateRoot(test)
			lease, err := OpenRuntime(test.Context(), root)
			if err != nil {
				test.Fatal(err)
			}
			defer func() {
				if err := lease.Close(); err != nil {
					test.Error(err)
				}
			}()
			ctx := test.Context()
			wanted := error(nil)
			path := root
			switch variant {
			case "ordinary", "hidden":
				name := "history"
				if variant == "hidden" {
					name = ".history"
				}
				if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
					test.Fatal(err)
				}
				wanted = ErrNeedsAttention
			case "fence-after-eof":
				if err := lease.CheckLegacy(ctx, root); err != nil {
					test.Fatal(err)
				}
				name := filepath.Join(root, fenceName, "foreign")
				if err := os.WriteFile(name, nil, 0600); err != nil {
					test.Fatal(err)
				}
				defer os.Remove(name)
				wanted = ErrNeedsAttention
			case "lock-replaced", "fence-replaced":
				name := lockName
				if variant == "fence-replaced" {
					name = fenceName
				}
				original := filepath.Join(root, name)
				moved := filepath.Join(root, "saved")
				if err := os.Rename(original, moved); err != nil {
					test.Fatal(err)
				}
				if name == lockName {
					err = os.WriteFile(original, nil, 0600)
				} else {
					err = os.Mkdir(original, 0700)
				}
				if err != nil {
					test.Fatal(err)
				}
				defer func() {
					_ = os.Remove(original)
					if err := os.Rename(moved, original); err != nil {
						test.Error(err)
					}
				}()
				wanted = ErrInvalid
			case "wrong-root":
				path = privateRoot(test)
				wanted = ErrInvalid
			case "closed":
				if err := lease.Close(); err != nil {
					test.Fatal(err)
				}
				wanted = ErrInvalid
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wanted = context.Canceled
			case "nil-callback":
				wanted = ErrInvalid
			case "callback-error":
				wanted = context.DeadlineExceeded
			}
			called := 0
			initialize := func() error {
				called++
				if variant == "callback-error" {
					return wanted
				}
				return nil
			}
			if variant == "nil-callback" {
				initialize = nil
			}
			err = lease.WithFreshRoot(ctx, path, initialize)
			if !errors.Is(err, wanted) || (wanted != nil && variant != "callback-error" && called != 0) || (wanted == nil && called != 1) {
				test.Fatal("fresh proof mismatch", variant, err, called)
			}
		})
	}
	test.Run("scope-blocks-close", func(test *testing.T) {
		root := privateRoot(test)
		lease, err := OpenRuntime(test.Context(), root)
		if err != nil {
			test.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		done := make(chan error, 1)
		go func() {
			done <- lease.WithFreshRoot(test.Context(), root, func() error { close(entered); <-release; return nil })
		}()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			test.Fatal("fresh scope not entered")
		}
		closed := make(chan error, 1)
		go func() { closed <- lease.Close() }()
		ctx, cancel := context.WithTimeout(test.Context(), 100*time.Millisecond)
		defer cancel()
		if offline, err := LockOffline(ctx, root); offline != nil || !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("scope let Close release physical authority", err)
		}
		unblock()
		if err := <-done; err != nil {
			test.Fatal(err)
		}
		if err := <-closed; err != nil {
			test.Fatal(err)
		}
	})
	test.Run("invalid", func(test *testing.T) {
		for _, lease := range []*Runtime{nil, {}} {
			if err := lease.WithFreshRoot(test.Context(), privateRoot(test), func() error { test.Fatal("invalid callback"); return nil }); !errors.Is(err, ErrInvalid) {
				test.Fatal(err)
			}
		}
	})
}
