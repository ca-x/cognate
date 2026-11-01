//go:build linux

package runtimegen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkerLeaseScope(test *testing.T) {
	for _, exclusive := range []bool{false, true} {
		test.Run(map[bool]string{false: "SH", true: "EX"}[exclusive], func(test *testing.T) {
			root := test.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				test.Fatal(err)
			}
			var borrow func(context.Context, func(*os.File, *os.File) error) error
			var closeOwner func() error
			if exclusive {
				owner, err := LockOffline(test.Context(), root)
				if err != nil {
					test.Fatal(err)
				}
				borrow, closeOwner = owner.WithWorkerFiles, owner.Close
			} else {
				owner, err := OpenRuntime(test.Context(), root)
				if err != nil {
					test.Fatal(err)
				}
				borrow, closeOwner = owner.WithWorkerFiles, owner.Close
			}
			defer closeOwner()
			entered, release := make(chan struct{}), make(chan struct{})
			done, closed := make(chan error, 1), make(chan error, 1)
			var inherited *os.File
			go func() {
				done <- borrow(test.Context(), func(root, lock *os.File) error {
					descriptor, err := unix.Dup(int(lock.Fd()))
					if err != nil {
						return err
					}
					inherited = os.NewFile(uintptr(descriptor), "same OFD")
					close(entered)
					<-release
					return nil
				})
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				test.Fatal("scope not entered")
			}
			go func() { closed <- closeOwner() }()
			select {
			case err := <-closed:
				test.Fatal("Close bypassed scope", err)
			case <-time.After(40 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-done:
				if err != nil {
					test.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				test.Fatal("borrow not joined")
			}
			defer inherited.Close()
			select {
			case err := <-closed:
				if err != nil {
					test.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				test.Fatal("Close not joined")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 80*time.Millisecond)
			defer cancel()
			if peer, err := LockOffline(ctx, root); peer != nil || !errors.Is(err, context.DeadlineExceeded) {
				test.Fatal("owner Close unlocked inherited OFD", err)
			}
			if err := inherited.Close(); err != nil {
				test.Fatal(err)
			}
			peer, err := LockOffline(test.Context(), root)
			if err != nil {
				test.Fatal(err)
			}
			if err := peer.Close(); err != nil {
				test.Fatal(err)
			}
			if err := borrow(test.Context(), func(*os.File, *os.File) error { return nil }); !errors.Is(err, ErrInvalid) {
				test.Fatal("closed owner borrowed", err)
			}
		})
	}
}

func TestWorkerLeaseAdmission(test *testing.T) {
	root := test.TempDir()
	_ = os.Chmod(root, 0700)
	owner, err := OpenRuntime(test.Context(), root)
	if err != nil {
		test.Fatal(err)
	}
	defer owner.Close()
	called := false
	consume := func(*os.File, *os.File) error { called = true; return nil }
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if err := owner.WithWorkerFiles(ctx, consume); !errors.Is(err, context.Canceled) || called {
		test.Fatal("canceled borrow", err)
	}
	if err := owner.WithWorkerFiles(nil, consume); !errors.Is(err, ErrInvalid) {
		test.Fatal(err)
	}
	if err := owner.WithWorkerFiles(test.Context(), nil); !errors.Is(err, ErrInvalid) {
		test.Fatal(err)
	}
	if err := (*Runtime)(nil).WithWorkerFiles(test.Context(), consume); !errors.Is(err, ErrInvalid) {
		test.Fatal(err)
	}
	if err := new(OfflineRoot).WithWorkerFiles(test.Context(), consume); !errors.Is(err, ErrInvalid) {
		test.Fatal(err)
	}
	for range 2 {
		if err := owner.WithWorkerFiles(test.Context(), consume); err != nil {
			test.Fatal(err)
		}
	}
	path := filepath.Join(root, ".runtime-fences", "observed")
	if err := os.WriteFile(path, []byte("CGRLS001"), 0600); err != nil {
		test.Fatal(err)
	}
	called = false
	if err := owner.WithWorkerFiles(test.Context(), consume); !errors.Is(err, ErrNeedsAttention) || called {
		test.Fatal("fence EOF bypass", err)
	}
	if err := os.Remove(path); err != nil {
		test.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".runtime.lock"), filepath.Join(root, "saved")); err != nil {
		test.Fatal(err)
	}
	if err := owner.WithWorkerFiles(test.Context(), consume); !errors.Is(err, ErrInvalid) || called {
		test.Fatal("replaced name admitted", err)
	}
	if err := os.Rename(filepath.Join(root, "saved"), filepath.Join(root, ".runtime.lock")); err != nil {
		test.Fatal(err)
	}
}
