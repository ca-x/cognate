package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestManagedWriterScope(test *testing.T) {
	test.Run("admission", func(test *testing.T) {
		store, instance, _ := managedStoreFixture(test)
		if !store.MaintenanceManaged() {
			test.Fatal("explicit managed Store lost its mode")
		}
		exclusive, err := instance.Quiesce(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		defer exclusive.Release()
		called := false
		value, err := store.WithMaintenanceWriter(test.Context(), func(context.Context) (int, error) {
			called = true
			return 42, nil
		})
		if called || value != 0 || !errors.Is(err, maintenance.ErrQuiescing) {
			test.Fatal("writer scope executed callback while maintenance owned Exclusive")
		}
		exclusive.Release()
		value, err = store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (int, error) {
			return 42, store.CreateUser(ctx, "scoped-owner")
		})
		if err != nil || value != 42 {
			test.Fatal("writer scope failed to publish a real SQLite write")
		}
	})
	test.Run("unmanaged-context", func(test *testing.T) {
		directory := test.TempDir()
		if err := os.Chmod(directory, 0o700); err != nil {
			test.Fatal(err)
		}
		store, err := Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
		if err != nil {
			if errors.Is(maintenance.CheckPlatform(test.Context()), maintenance.ErrUnsupported) && err.Error() == "server data directory security is unsupported on this platform" {
				return
			}
			test.Fatal(err)
		}
		defer store.Close()
		if store.MaintenanceManaged() {
			test.Fatal("default Store became managed")
		}
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		failure := errors.New("synthetic callback error")
		value, err := store.WithMaintenanceWriter(ctx, func(received context.Context) (int, error) {
			if received != ctx || !errors.Is(received.Err(), context.Canceled) {
				test.Error("unmanaged scope replaced its context")
			}
			return 77, failure
		})
		if value != 77 || !errors.Is(err, failure) {
			test.Fatal("unmanaged scope changed callback result semantics")
		}
	})
	test.Run("nested-independent-drain", func(test *testing.T) {
		store, instance, _ := managedStoreFixture(test)
		foreign, _, _ := managedStoreFixture(test)
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		finished := make(chan error, 1)
		var child context.Context
		parent, err := store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (context.Context, error) {
			go func() {
				_, err := store.WithMaintenanceWriter(ctx, func(ctx context.Context) (int, error) {
					entered <- ctx
					<-release
					return 1, store.CreateUser(ctx, "child-after-parent-return")
				})
				finished <- err
			}()
			select {
			case child = <-entered:
			case <-time.After(time.Second):
				return nil, errors.New("child scope did not enter")
			}
			return ctx, nil
		})
		if err != nil {
			test.Fatal(err)
		}
		for _, denied := range []struct {
			store *Store
			ctx   context.Context
		}{{store, parent}, {foreign, child}} {
			called := false
			value, err := denied.store.WithMaintenanceWriter(denied.ctx, func(context.Context) (int, error) {
				called = true
				return 1, nil
			})
			if value != 0 || called || !errors.Is(err, maintenance.ErrLease) {
				test.Fatal("stale or foreign-root context lent writer admission")
			}
		}
		acquired := make(chan *maintenance.Exclusive, 1)
		go func() {
			exclusive, err := instance.Quiesce(test.Context())
			if err == nil {
				acquired <- exclusive
			}
		}()
		awaitManagedAdmissionClosed(test, store)
		select {
		case exclusive := <-acquired:
			exclusive.Release()
			test.Fatal("scope borrowed its parent's count and lost its live child")
		default:
		}
		_, err = store.WithMaintenanceWriter(child, func(ctx context.Context) (int, error) {
			return 1, store.CreateUser(ctx, "nested-during-quiesce")
		})
		if err != nil {
			test.Fatal("live scoped context was not propagated to nested SQL")
		}
		close(release)
		if err := <-finished; err != nil {
			test.Fatal(err)
		}
		select {
		case exclusive := <-acquired:
			exclusive.Release()
		case <-time.After(time.Second):
			test.Fatal("independent scoped child did not drain")
		}
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		called := false
		value, err := store.WithMaintenanceWriter(ctx, func(context.Context) (int, error) {
			called = true
			return 1, nil
		})
		if value != 0 || called || !errors.Is(err, context.Canceled) {
			test.Fatal("canceled managed admission executed its callback")
		}
	})
	test.Run("close-drain", func(test *testing.T) {
		store, instance, _ := managedStoreFixture(test)
		if err := instance.Close(); err != nil {
			test.Fatal(err)
		}
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		finished := make(chan error, 1)
		go func() {
			_, err := store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (int, error) {
				entered <- ctx
				<-release
				return 1, store.CreateUser(ctx, "callback-before-SQL-close")
			})
			finished <- err
		}()
		var callback context.Context
		select {
		case callback = <-entered:
		case <-time.After(time.Second):
			test.Fatal("scope callback did not enter")
		}
		managed := store.state.maintenance
		managed.mutex.Lock()
		changed := managed.changed
		managed.mutex.Unlock()
		closed := make(chan error, 1)
		go func() { closed <- store.Close() }()
		select {
		case <-changed:
		case <-time.After(time.Second):
			test.Fatal("Store.Close did not start")
		}
		if _, err := store.WithMaintenanceWriter(test.Context(), func(context.Context) (int, error) { return 1, nil }); !errors.Is(err, maintenance.ErrClosed) {
			test.Fatal("closing Store admitted a new scope")
		}
		if _, err := store.WithMaintenanceWriter(callback, func(ctx context.Context) (int, error) { return 1, store.CreateUser(ctx, "nested-while-closing") }); err != nil {
			test.Fatal("Store.Close prevented a live scoped child from finishing")
		}
		select {
		case <-closed:
			test.Fatal("Store.Close escaped an active scoped callback")
		default:
		}
		close(release)
		if err := <-finished; err != nil {
			test.Fatal(err)
		}
		if err := <-closed; err != nil {
			test.Fatal(err)
		}
	})
}
