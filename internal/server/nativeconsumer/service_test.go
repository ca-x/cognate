package nativeconsumer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/state"
)

func newLifecycleFixture(test *testing.T) *Service {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	master := bytes.Repeat([]byte{73}, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "native.db"), master)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	owner, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	service, err := New(store, owner, master, "https://cognate.example")
	clear(master)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(service.Close)
	return service
}

func TestNativeLifecycleThirtySecondSerialPrune(test *testing.T) {
	service := newLifecycleFixture(test)
	synctest.Test(test, func(test *testing.T) {
		var calls atomic.Int32
		var active atomic.Int32
		service.prune = func(ctx context.Context) (state.NativePruneResult, error) {
			if active.Add(1) != 1 {
				test.Error("GC rounds overlapped")
			}
			defer active.Add(-1)
			calls.Add(1)
			return state.NativePruneResult{}, state.ErrNativeUnavailable
		}
		if err := service.Start(test.Context()); err != nil {
			test.Fatal(err)
		}
		synctest.Wait()
		if calls.Load() != 1 {
			test.Fatal("startup did not run exactly one bounded pass")
		}
		time.Sleep(29 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			test.Fatal("production interval was shortened")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			test.Fatal("30-second round missing")
		}
		service.Close()
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 2 || service.Start(test.Context()) == nil {
			test.Fatal("closed lifecycle restarted")
		}
	})
}

func TestNativeLifecycleAdmissionCancellationAndActualDrain(test *testing.T) {
	service := newLifecycleFixture(test)
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	defer finish()
	service.prune = func(ctx context.Context) (state.NativePruneResult, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return state.NativePruneResult{}, ctx.Err()
	}
	if err := service.Start(test.Context()); err != nil {
		test.Fatal(err)
	}
	<-entered
	var work sync.WaitGroup
	started := make(chan struct{}, 8)
	for range 8 {
		work.Go(func() {
			err := service.Call(test.Context(), func(ctx context.Context) error {
				deadline, exists := ctx.Deadline()
				if !exists || time.Until(deadline) > 20*time.Second {
					return errors.New("request has no 20-second bound")
				}
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
			if !errors.Is(err, context.Canceled) {
				test.Error("admitted work was not canceled")
			}
		})
	}
	for range 8 {
		<-started
	}
	if err := service.Call(test.Context(), func(context.Context) error { test.Error("ninth admitted"); return nil }); !errors.Is(err, state.ErrNativeQuota) {
		test.Fatal("eight slots did not bound whole requests")
	}
	closed := make(chan struct{})
	go func() { service.Close(); close(closed) }()
	<-canceled
	work.Wait()
	select {
	case <-closed:
		test.Fatal("Close returned before actual GC worker drained")
	default:
	}
	finish()
	<-closed
	if err := service.Call(test.Context(), func(context.Context) error { test.Error("closed service admitted"); return nil }); !errors.Is(err, state.ErrNativeUnavailable) {
		test.Fatal("shutdown admission stayed open")
	}
}

func TestNativeLifecycleCloseWaitsForActualCanceledWorker(test *testing.T) {
	service := newLifecycleFixture(test)
	synctest.Test(test, func(test *testing.T) {
		entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		finish := sync.OnceFunc(func() { close(release) })
		defer finish()
		service.prune = func(ctx context.Context) (state.NativePruneResult, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return state.NativePruneResult{}, ctx.Err()
		}
		if err := service.Start(test.Context()); err != nil {
			test.Fatal(err)
		}
		<-entered
		closed := make(chan struct{})
		go func() { service.Close(); close(closed) }()
		<-canceled
		synctest.Wait()
		select {
		case <-closed:
			test.Fatal("Close returned before the canceled worker finished")
		default:
		}
		finish()
		<-closed
	})
}
