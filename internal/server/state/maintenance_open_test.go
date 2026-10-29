package state

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestManagedStorePlatformValidationPrecedence(test *testing.T) {
	directory := test.TempDir()
	missing := filepath.Join(directory, "missing-parent", "server.db")
	canceled, cancel := context.WithCancel(test.Context())
	cancel()
	expired, stop := context.WithDeadline(test.Context(), time.Now().Add(-time.Second))
	defer stop()
	for _, scenario := range []struct {
		name    string
		ctx     context.Context
		path    string
		key     []byte
		want    error
		message string
	}{
		{name: "canceled-before-invalid-input", ctx: canceled, path: "relative.db", want: context.Canceled},
		{name: "expired-before-invalid-input", ctx: expired, path: "relative.db", want: context.DeadlineExceeded},
		{name: "key-before-filesystem", ctx: test.Context(), path: missing, message: "server credential encryption key must be 32 bytes"},
		{name: "key-before-path", ctx: test.Context(), path: "relative.db", message: "server credential encryption key must be 32 bytes"},
		{name: "relative-path", ctx: test.Context(), path: "relative.db", key: make([]byte, 32), message: "server database path must be absolute and clean"},
		{name: "unclean-path", ctx: test.Context(), path: directory + string(os.PathSeparator) + "missing-parent" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "server.db", key: make([]byte, 32), message: "server database path must be absolute and clean"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			store, err := OpenWithOptions(scenario.ctx, scenario.path, scenario.key, Options{MaintenanceManaged: true})
			if store != nil {
				_ = store.Close()
				test.Fatal("invalid startup inputs returned a Store")
			}
			if scenario.want != nil && !errors.Is(err, scenario.want) || scenario.message != "" && (err == nil || err.Error() != scenario.message) {
				test.Fatalf("managed startup changed validation precedence: %v", err)
			}
		})
	}
	for _, ctx := range []context.Context{canceled, expired} {
		if err := maintenance.CheckPlatform(ctx); !errors.Is(err, ctx.Err()) {
			test.Fatal("platform capability check lost context cancellation")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		test.Fatal("invalid managed startup created filesystem entries")
	}
}

func TestManagedStorePlatformCheckPrecedesFilesystem(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	capability := maintenance.CheckPlatform(test.Context())
	if capability != nil && !errors.Is(capability, maintenance.ErrUnsupported) {
		test.Fatal("unexpected platform capability failure")
	}
	for _, path := range []string{filepath.Join(directory, "missing-parent", "server.db"), filepath.Join(directory, "server.db")} {
		store, err := OpenWithOptions(test.Context(), path, make([]byte, 32), Options{MaintenanceManaged: true})
		if errors.Is(capability, maintenance.ErrUnsupported) {
			if store != nil {
				_ = store.Close()
			}
			if store != nil || !errors.Is(err, maintenance.ErrUnsupported) {
				test.Fatalf("unsupported managed startup reached filesystem validation: %v", err)
			}
		} else if filepath.Dir(path) != directory {
			if store != nil {
				_ = store.Close()
			}
			if store != nil || !errors.Is(err, os.ErrNotExist) {
				test.Fatal("supported platform lost its canonical-parent failure")
			}
		} else {
			if err != nil || store == nil {
				test.Fatalf("supported managed startup failed: %v", err)
			}
			if err := store.CreateUser(test.Context(), "supported-managed-writer"); err != nil {
				test.Fatal(err)
			}
			if err := store.Close(); err != nil {
				test.Fatal(err)
			}
		}
	}
	if errors.Is(capability, maintenance.ErrUnsupported) {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			test.Fatal("unsupported platform capability check created filesystem entries")
		}
	}
}

func TestManagedStoreStartupAdmissionAndCleanup(test *testing.T) {
	store, instance, directory := managedStoreFixture(test)
	exclusive, err := instance.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer exclusive.Release()
	path := filepath.Join(directory, "second.db")
	if opened, err := OpenWithOptions(test.Context(), path, make([]byte, 32), Options{MaintenanceManaged: true}); opened != nil || !errors.Is(err, maintenance.ErrQuiescing) {
		if opened != nil {
			opened.Close()
		}
		test.Fatalf("startup bypassed admission: %v", err)
	}
	for _, suffix := range []string{"", ".schema.lock"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("blocked startup touched schema/database")
		}
	}
	exclusive.Release()
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	if opened, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), bytes.Repeat([]byte{9}, 32), Options{MaintenanceManaged: true}); opened != nil || err == nil {
		if opened != nil {
			opened.Close()
		}
		test.Fatal("wrong-key startup succeeded")
	}
	managedProcessProbe(test, directory, "acquired")
}

func TestManagedStoreSameDirectoryAndIndependentClose(test *testing.T) {
	first, instance, directory := managedStoreFixture(test)
	second, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer second.Close()
	exclusive, err := instance.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if err := second.CreateUser(test.Context(), "blocked"); !errors.Is(err, maintenance.ErrQuiescing) {
		test.Fatal("second Store got an independent gate")
	}
	other, _, _ := managedStoreFixture(test)
	if err := other.CreateUser(test.Context(), "independent-directory"); err != nil {
		test.Fatal("unrelated directory inherited maintenance")
	}
	exclusive.Release()
	if err := first.Close(); err != nil {
		test.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	managedProcessProbe(test, directory, "blocked")
	if err := second.CreateUser(test.Context(), "still-open"); err != nil {
		test.Fatal("closing first Store closed second")
	}
	if err := second.Close(); err != nil {
		test.Fatal(err)
	}
	managedProcessProbe(test, directory, "acquired")
}

func TestDefaultStoreRemainsOutsideManagedProtocol(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	first, err := Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		if err.Error() != "server data directory security is unsupported on this platform" {
			test.Fatal(err)
		}
		if managed, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true}); managed != nil || !errors.Is(err, maintenance.ErrUnsupported) {
			if managed != nil {
				managed.Close()
			}
			test.Fatal("managed non-Unix mode did not return fixed unsupported")
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			test.Fatal("unsupported startup changed filesystem")
		}
		return
	}
	defer first.Close()
	second, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{})
	if err != nil {
		test.Fatal(err)
	}
	defer second.Close()
	if err := second.CreateUser(test.Context(), "default-peer"); err != nil {
		test.Fatal(err)
	}
	managedProcessProbe(test, directory, "default")
	instance, err := maintenance.Join(test.Context(), directory)
	if errors.Is(err, maintenance.ErrUnsupported) {
		if managed, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true}); managed != nil || !errors.Is(err, maintenance.ErrUnsupported) {
			test.Fatal("unsupported managed mode fell back")
		}
		return
	}
	if err != nil {
		test.Fatal(err)
	}
	defer instance.Close()
	exclusive, err := instance.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer exclusive.Release()
	if err := first.CreateUser(test.Context(), "explicitly-unmanaged"); err != nil {
		test.Fatal("default mode silently joined gate")
	}
}

type managedReplacementContext struct {
	context.Context
	once    sync.Once
	replace func()
}

func (ctx *managedReplacementContext) Deadline() (time.Time, bool) {
	ctx.once.Do(ctx.replace)
	return ctx.Context.Deadline()
}

func TestManagedStoreRechecksCanonicalParentAfterJoin(test *testing.T) {
	_, _, _ = managedStoreFixture(test)
	parent := test.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		test.Fatal(err)
	}
	directory := filepath.Join(parent, "data")
	if err := os.Mkdir(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	var replacementError error
	ctx := &managedReplacementContext{Context: test.Context(), replace: func() {
		replacementError = os.Rename(directory, filepath.Join(parent, "moved"))
		if replacementError == nil {
			replacementError = os.Mkdir(directory, 0o700)
		}
	}}
	store, err := OpenWithOptions(ctx, filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
	if replacementError != nil {
		test.Fatal(replacementError)
	}
	if store != nil {
		store.Close()
	}
	if store != nil || !errors.Is(err, maintenance.ErrDirectory) {
		test.Fatalf("changed parent identity accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "server.db")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("identity failure created SQL database")
	}
	managedProcessProbe(test, directory, "acquired")
}

func TestManagedStoreFailedJoinDoesNotFallback(test *testing.T) {
	_, instance, _ := managedStoreFixture(test)
	instance.Close()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".instance.lock"), nil, 0o644); err != nil {
		test.Fatal(err)
	}
	store, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
	if store != nil || !errors.Is(err, maintenance.ErrLock) {
		if store != nil {
			store.Close()
		}
		test.Fatal("failed Join fell back")
	}
	if _, err := os.Stat(filepath.Join(directory, "server.db")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("failed Join created database")
	}
}

func managedProcessProbe(test *testing.T, directory, mode string) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagedStoreProcessProbe$")
	command.Env = append(os.Environ(), "COGNATE_MANAGED_PROBE_DIR="+directory, "COGNATE_MANAGED_PROBE_MODE="+mode)
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("process probe %s failed: %v\n%s", mode, err, output)
	}
}

func TestManagedStoreProcessProbe(test *testing.T) {
	directory := os.Getenv("COGNATE_MANAGED_PROBE_DIR")
	if directory == "" {
		test.Skip("subprocess helper")
	}
	mode := os.Getenv("COGNATE_MANAGED_PROBE_MODE")
	ctx, cancel := context.WithTimeout(test.Context(), 350*time.Millisecond)
	defer cancel()
	store, err := OpenWithOptions(ctx, filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: mode != "default"})
	if mode == "blocked" {
		if store != nil {
			store.Close()
		}
		if store != nil || err == nil || !errors.Is(err, maintenance.ErrLock) {
			test.Fatal("participating second process was not rejected")
		}
		return
	}
	if err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
}
