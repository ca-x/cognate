//go:build linux

package runtimegen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRetainedWorkerNormalRelease(test *testing.T) {
	path := privateRoot(test)
	owner, err := LockOffline(test.Context(), path)
	if err != nil {
		test.Fatal("owner class")
	}
	for range 2 {
		if err := owner.WithRetainedWorkerFiles(test.Context(), func(_, lock *os.File) error {
			descriptor, err := unix.FcntlInt(lock.Fd(), unix.F_DUPFD_CLOEXEC, 0)
			if err != nil {
				return ErrUncertain
			}
			return os.NewFile(uintptr(descriptor), "owned guard").Close()
		}); err != nil {
			test.Fatal("normal retained scope class")
		}
	}
	if owner.Close() != nil || owner.Close() != nil {
		test.Fatal("normal release class")
	}
	processProbe(test, path, "offline", "acquired")
}

func TestRetainedWorkerQuarantine(test *testing.T) {
	for _, fault := range []string{"callback", "post-fence", "post-canceled", "panic"} {
		test.Run(fault, func(test *testing.T) {
			path := privateRoot(test)
			owner, err := LockOffline(test.Context(), path)
			if err != nil {
				test.Fatal("owner class")
			}
			physicalCloses := 0
			owner.lease.ops.close = func(file *os.File) error { physicalCloses++; return file.Close() }
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			privateError := errors.New("callback private class")
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				err = owner.WithRetainedWorkerFiles(ctx, func(_, lock *os.File) error {
					descriptor, err := unix.FcntlInt(lock.Fd(), unix.F_DUPFD_CLOEXEC, 0)
					if err != nil || unix.Close(descriptor) != nil {
						test.Fatal("auxiliary physical close class")
					}
					switch fault {
					case "callback":
						return privateError
					case "post-fence":
						return os.WriteFile(filepath.Join(path, fenceName, "foreign"), []byte("CGRLS001"), 0600)
					case "post-canceled":
						cancel()
					case "panic":
						panic("synthetic release panic")
					}
					return nil
				})
			}()
			if panicked != (fault == "panic") || !panicked && (!errors.Is(err, ErrUncertain) || errors.Is(err, privateError)) {
				test.Fatal("fixed uncertainty class")
			}
			for range 2 {
				if !errors.Is(owner.Close(), ErrUncertain) {
					test.Fatal("quarantine Close class")
				}
			}
			if physicalCloses != 0 {
				test.Fatal("quarantine physically closed owner")
			}
			for _, file := range []*os.File{owner.lease.root, owner.lease.lock, owner.lease.fences} {
				var identity unix.Stat_t
				if unix.Fstat(int(file.Fd()), &identity) != nil {
					test.Fatal("original descriptor not live")
				}
			}
			called := false
			consume := func(*os.File, *os.File) error { called = true; return nil }
			if !errors.Is(owner.WithRetainedWorkerFiles(test.Context(), consume), ErrUncertain) || called {
				test.Fatal("quarantine reborrowed")
			}
			if !errors.Is(owner.WithWorkerFiles(test.Context(), consume), ErrInvalid) || called {
				test.Fatal("old API reborrowed quarantined owner")
			}
			runtime.GC()
			processProbe(test, path, "offline", "deadline")
			if fault == "post-fence" {
				data, err := os.ReadFile(filepath.Join(path, fenceName, "foreign"))
				if err != nil || string(data) != "CGRLS001" {
					test.Fatal("foreign fence lost")
				}
			}
		})
	}
}

func TestRetainedWorkerCloseRace(test *testing.T) {
	path := privateRoot(test)
	owner, err := LockOffline(test.Context(), path)
	if err != nil {
		test.Fatal("owner class")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	borrowed, closed := make(chan error, 1), make(chan error, 1)
	go func() {
		borrowed <- owner.WithRetainedWorkerFiles(test.Context(), func(*os.File, *os.File) error {
			close(entered)
			<-release
			return ErrUncertain
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		test.Fatal("scope entry timeout")
	}
	go func() { closed <- owner.Close() }()
	select {
	case <-closed:
		test.Fatal("Close bypassed retained scope")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for _, result := range []<-chan error{borrowed, closed} {
		select {
		case err := <-result:
			if !errors.Is(err, ErrUncertain) {
				test.Fatal("atomic quarantine class")
			}
		case <-time.After(3 * time.Second):
			test.Fatal("scope join timeout")
		}
	}
	processProbe(test, path, "offline", "deadline")
}

func TestRetainedWorkerAdmissionAndOriginalAPI(test *testing.T) {
	path := privateRoot(test)
	owner, err := LockOffline(test.Context(), path)
	if err != nil {
		test.Fatal("owner class")
	}
	defer owner.Close()
	called := false
	consume := func(*os.File, *os.File) error { called = true; return nil }
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if !errors.Is(owner.WithRetainedWorkerFiles(ctx, consume), context.Canceled) || called {
		test.Fatal("canceled preadmission entered callback")
	}
	if !errors.Is((*OfflineRoot)(nil).WithRetainedWorkerFiles(test.Context(), consume), ErrInvalid) ||
		!errors.Is(new(OfflineRoot).WithRetainedWorkerFiles(test.Context(), consume), ErrInvalid) ||
		!errors.Is(owner.WithRetainedWorkerFiles(nil, consume), ErrInvalid) ||
		!errors.Is(owner.WithRetainedWorkerFiles(test.Context(), nil), ErrInvalid) {
		test.Fatal("invalid input class")
	}
	privateError := errors.New("ordinary callback class")
	if err := owner.WithWorkerFiles(test.Context(), func(*os.File, *os.File) error { return privateError }); !errors.Is(err, privateError) || errors.Is(err, ErrUncertain) {
		test.Fatal("original callback contract changed")
	}
	if owner.Close() != nil {
		test.Fatal("pre-admission or old callback quarantined owner")
	}
	processProbe(test, path, "offline", "acquired")
}
