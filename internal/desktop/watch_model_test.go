package desktop

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/egoist/mygo/ui"
)

type watchBackend struct {
	Backend
	cycle  func(context.Context) (subscription.SyncResult, []state.Subscription, error)
	writes atomic.Int32
}

func (backend *watchBackend) Install(context.Context, string, *catalogv1.PublicSkill) error {
	backend.writes.Add(1)
	return nil
}

func (backend *watchBackend) Packs(context.Context, string, string, string, string) ([]state.Subscription, error) {
	backend.writes.Add(1)
	return nil, nil
}

func (backend *watchBackend) Search(context.Context, string, string, string) (*catalogv1.ListSkillsResponse, error) {
	return nil, errors.New("synthetic-private-error-and-sharing-token")
}

func (backend *watchBackend) WatchCycle(ctx context.Context) (subscription.SyncResult, []state.Subscription, error) {
	return backend.cycle(ctx)
}

func nextWatchUpdate(test *testing.T, callbacks <-chan func()) {
	test.Helper()
	select {
	case update := <-callbacks:
		update()
	case <-time.After(5 * time.Second):
		test.Fatal("watch worker did not deliver bounded UI update")
	}
}

func TestNativeWatchExplicitControlsAndActualDrain(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("native watch kernel currently Linux only")
	}
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	backend := &watchBackend{cycle: func(ctx context.Context) (subscription.SyncResult, []state.Subscription, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return subscription.SyncResult{Installed: 1}, []state.Subscription{{Name: "stale owner"}}, nil
	}}
	callbacks := make(chan func(), 16)
	model := NewModel(backend, func(update func()) { callbacks <- update })
	test.Cleanup(func() { releaseOnce.Do(func() { close(release) }); model.Close(); model.Wait() })
	model.SetOrigin("https://example.org")
	model.Page = 1
	tester := ui.NewTester(model.View, 920, 760)
	if model.WatchActive || model.WatchStopping {
		test.Fatal("watch silently enabled at startup")
	}
	click(test, tester, "启用后台同步")
	<-started
	if !model.WatchActive {
		test.Fatal("native start control did not start immediate cycle")
	}
	click(test, tester, "停止后台同步")
	<-canceled
	if !model.WatchStopping || model.WatchActive {
		test.Fatal("stop did not retain actual worker drain state")
	}
	model.StartWatch()
	if !model.WatchStopping || model.WatchActive {
		test.Fatal("restart bypassed draining worker")
	}
	releaseOnce.Do(func() { close(release) })
	nextWatchUpdate(test, callbacks)
	tester.Frame()
	if model.WatchStopping || model.WatchActive || len(model.Subscriptions) != 0 {
		test.Fatal("stopped worker repopulated UI or did not drain")
	}
}

func TestNativeWatchQueuedOldReportsCannotRepopulate(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("native watch kernel currently Linux only")
	}
	for _, action := range []string{"stop", "cancel", "origin", "logout", "close"} {
		test.Run(action, func(test *testing.T) {
			backend := &watchBackend{cycle: func(context.Context) (subscription.SyncResult, []state.Subscription, error) {
				return subscription.SyncResult{Installed: 1}, []state.Subscription{{Name: "old-owner-result"}}, nil
			}}
			callbacks := make(chan func(), 16)
			model := NewModel(backend, func(update func()) { callbacks <- update })
			test.Cleanup(func() { model.Close(); model.Wait() })
			model.SetOrigin("https://old.example")
			model.StartWatch()
			var queued func()
			select {
			case queued = <-callbacks:
			case <-time.After(5 * time.Second):
				test.Fatal("watch did not immediately run")
			}
			switch action {
			case "stop":
				model.StopWatch()
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://new.example")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			}
			queued()
			if len(model.Subscriptions) != 0 || strings.Contains(model.WatchStatus, "后台同步完成") {
				test.Fatal("queued old report repopulated a canceled origin/owner")
			}
			if action == "close" {
				model.Wait()
				if len(callbacks) != 0 {
					test.Fatal("closed worker posted new UI callbacks")
				}
			} else {
				nextWatchUpdate(test, callbacks)
				if model.watchLocksLocal() {
					test.Fatal("stopped worker did not release UI admission")
				}
			}
		})
	}
}

func TestNativeWatchHeldFinalizerDoesNotBlockDrainOrClearRestart(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("native watch kernel currently Linux only")
	}
	var cycles atomic.Int32
	backend := &watchBackend{cycle: func(context.Context) (subscription.SyncResult, []state.Subscription, error) {
		cycle := cycles.Add(1)
		return subscription.SyncResult{Reused: 1}, []state.Subscription{{Name: fmt.Sprintf("owner-cycle-%d", cycle)}}, nil
	}}
	callbacks := make(chan func(), 16)
	model := NewModel(backend, func(update func()) { callbacks <- update })
	test.Cleanup(func() { model.Close(); model.Wait() })
	model.SetOrigin("https://example.org")
	model.StartWatch()
	var report func()
	select {
	case report = <-callbacks:
	case <-time.After(5 * time.Second):
		test.Fatal("initial report was not queued")
	}
	model.StopWatch()
	model.Wait()
	var finalizer func()
	select {
	case finalizer = <-callbacks:
	case <-time.After(5 * time.Second):
		test.Fatal("worker drain depended on unexecuted UI report")
	}
	if !model.WatchStopping {
		test.Fatal("UI admitted writes before acknowledging drained run")
	}
	model.StartWatch()
	if model.WatchActive || cycles.Load() != 1 {
		test.Fatal("restart bypassed pending drained-run acknowledgement")
	}
	report()
	finalizer()
	model.StartWatch()
	finalizer()
	report()
	if !model.WatchActive || model.watch == nil {
		test.Fatal("old finalizer cleared restarted run")
	}
	nextWatchUpdate(test, callbacks)
	if len(model.Subscriptions) != 1 || model.Subscriptions[0].Name != "owner-cycle-2" {
		test.Fatal("restart accepted old report or lost new report")
	}
}

func TestNativeWatchLocalWritesBlockedAndErrorsSafe(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("native watch kernel currently Linux only")
	}
	backend := &watchBackend{cycle: func(context.Context) (subscription.SyncResult, []state.Subscription, error) {
		return subscription.SyncResult{Installed: 999}, []state.Subscription{{Name: "must-not-publish"}}, errors.New("synthetic-private-error-and-sharing-token")
	}}
	callbacks := make(chan func(), 16)
	model := NewModel(backend, func(update func()) { callbacks <- update })
	test.Cleanup(func() { model.Close(); model.Wait() })
	model.SetOrigin("https://example.org")
	model.StartWatch()
	nextWatchUpdate(test, callbacks)
	model.Detail, model.Confirm = &catalogv1.PublicSkill{Id: "chosen", Name: "Chosen"}, true
	model.Install()
	model.Packs("sync")
	if backend.writes.Load() != 0 || model.Busy || !model.WatchActive {
		test.Fatal("manual write bypassed active background admission")
	}
	model.Select(-1)
	if !model.WatchActive || model.watch.ctx.Err() != nil {
		test.Fatal("browsing silently stopped background synchronization")
	}
	model.Search(false)
	nextWatchUpdate(test, callbacks)
	if model.Busy || !model.WatchActive || len(model.Subscriptions) != 0 || strings.Contains(model.WatchStatus, "999") || strings.Contains(model.WatchStatus+model.Status, "synthetic-private") {
		test.Fatal("raw error, partial result or failed mutation reached UI")
	}
	model.StopWatch()
	model.Install()
	model.Packs("sync")
	if backend.writes.Load() != 0 || model.Busy {
		test.Fatal("manual write bypassed draining background admission")
	}
	nextWatchUpdate(test, callbacks)
}

func TestWatchDefaultBackoffIsSerialBoundedAndReset(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("native watch kernel currently Linux only")
	}
	synctest.Test(test, func(test *testing.T) {
		var calls atomic.Int32
		var fail atomic.Bool
		fail.Store(true)
		backend := &watchBackend{cycle: func(context.Context) (subscription.SyncResult, []state.Subscription, error) {
			calls.Add(1)
			if fail.Load() {
				return subscription.SyncResult{Installed: 999}, nil, errors.New("synthetic-private-cycle-error")
			}
			return subscription.SyncResult{Reused: 2}, nil, nil
		}}
		callbacks := make(chan func(), 16)
		model := NewModel(backend, func(update func()) { callbacks <- update })
		defer func() { model.Close(); model.Wait() }()
		model.SetOrigin("https://example.org")
		model.StartWatch()
		nextWatchUpdate(test, callbacks)
		if calls.Load() != 1 || !strings.Contains(model.WatchStatus, "30s 后重试") {
			test.Fatal("default watch did not immediately run or start conservative retry")
		}
		for index, delay := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute} {
			synctest.Wait()
			time.Sleep(delay)
			synctest.Wait()
			nextWatchUpdate(test, callbacks)
			if calls.Load() != int32(index+2) || strings.Contains(model.WatchStatus, "999") || strings.Contains(model.WatchStatus, "synthetic-private") {
				test.Fatal("retry overlapped, skipped or exposed a failed partial result")
			}
		}
		if !strings.Contains(model.WatchStatus, "5m0s 后重试") {
			test.Fatal("retry exceeded normal interval cap")
		}
		fail.Store(false)
		synctest.Wait()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		nextWatchUpdate(test, callbacks)
		if calls.Load() != 7 || !strings.Contains(model.WatchStatus, "复用 2") {
			test.Fatal("successful retry did not report bounded core result")
		}
		fail.Store(true)
		synctest.Wait()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		nextWatchUpdate(test, callbacks)
		if calls.Load() != 8 || !strings.Contains(model.WatchStatus, "连续 1 轮") || !strings.Contains(model.WatchStatus, "30s 后重试") {
			test.Fatal("success did not reset failure backoff")
		}
		model.StopWatch()
		synctest.Wait()
		nextWatchUpdate(test, callbacks)
		time.Sleep(time.Hour)
		if calls.Load() != 8 || model.WatchActive || model.WatchStopping {
			test.Fatal("stopped scheduler continued calling backend")
		}
	})
}

func TestWatchCloseWaitsActualWorkerWithoutUICallback(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("native watch kernel currently Linux only")
	}
	synctest.Test(test, func(test *testing.T) {
		started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var dispatches atomic.Int32
		backend := &watchBackend{cycle: func(ctx context.Context) (subscription.SyncResult, []state.Subscription, error) {
			close(started)
			<-ctx.Done()
			<-release
			return subscription.SyncResult{}, nil, nil
		}}
		model := NewModel(backend, func(func()) { dispatches.Add(1) })
		model.SetOrigin("https://example.org")
		model.StartWatch()
		<-started
		model.Close()
		go func() { model.Wait(); close(finished) }()
		synctest.Wait()
		select {
		case <-finished:
			test.Fatal("Wait completed before actual worker returned")
		default:
		}
		close(release)
		<-finished
		if dispatches.Load() != 0 {
			test.Fatal("close caused post-close UI dispatch or callback dependency")
		}
	})
}
