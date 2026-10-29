package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/egoist/mygo/ui"
)

const initializeClientKeyLabel = "初始化本机客户端密钥"

type initializationBackend struct {
	Backend
	initialize func(context.Context) error
	calls      atomic.Int32
	writes     atomic.Int32
}

func (backend *initializationBackend) InitializeClientKey(ctx context.Context) error {
	backend.calls.Add(1)
	return backend.initialize(ctx)
}

func (backend *initializationBackend) Install(context.Context, string, *catalogv1.PublicSkill) error {
	backend.writes.Add(1)
	return nil
}

func (backend *initializationBackend) Packs(context.Context, string, string, string, string) ([]state.Subscription, error) {
	backend.writes.Add(1)
	return nil, nil
}

func (backend *initializationBackend) WatchCycle(context.Context) (subscription.SyncResult, []state.Subscription, error) {
	backend.writes.Add(1)
	return subscription.SyncResult{}, nil, errors.New("synthetic watch error")
}

func TestClientKeyEntrypointsNativeCanceledDrain(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		backend := &initializationBackend{initialize: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		}}
		model, pump := modelFixture(test, backend)
		model.SetOrigin("https://example.org")
		model.InitializeClientKey()
		<-started
		if !model.Busy || !strings.Contains(model.Status, "正在初始化") {
			test.Fatal("initialization did not provide immediate asynchronous feedback")
		}
		model.Cancel()
		<-canceled
		if model.Busy || model.pendingJobs.Load() != 1 || !model.localWorkBlocked() {
			test.Error("cancel claimed the backend had actually drained")
		}
		model.InitializeClientKey()
		model.Detail = &catalogv1.PublicSkill{Id: "synthetic"}
		model.Confirm = true
		model.Install()
		model.Packs("list")
		model.StartWatch()
		synctest.Wait()
		if backend.calls.Load() != 1 || backend.writes.Load() != 0 || model.pendingJobs.Load() != 1 || model.watch != nil {
			test.Error("canceled worker permitted a replacement local operation")
		}
		model.Close()
		joined := make(chan struct{})
		go func() { model.Wait(); close(joined) }()
		synctest.Wait()
		select {
		case <-joined:
			test.Error("Close/Wait abandoned the actual backend return")
		default:
		}
		close(release)
		<-joined
		pump()
		if model.pendingJobs.Load() != 0 || len(model.slots) != 0 {
			test.Fatal("drained worker retained local admission")
		}
	})
}

func TestClientKeyEntrypointsNativeQueuedGeneration(test *testing.T) {
	for _, action := range []string{"cancel", "origin", "logout", "close"} {
		test.Run(action, func(test *testing.T) {
			callbacks := make(chan func(), 8)
			backend := &initializationBackend{initialize: func(context.Context) error { return nil }}
			model := NewModel(backend, func(callback func()) { callbacks <- callback })
			defer func() { model.Close(); model.Wait() }()
			model.InitializeClientKey()
			model.Wait()
			if len(callbacks) != 1 {
				test.Fatal("completion did not reach dispatch barrier")
			}
			switch action {
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://new.example.org")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			}
			status := model.Status
			(<-callbacks)()
			if model.Status != status || model.Busy {
				test.Fatal("queued stale initialization completion crossed generation boundary")
			}
		})
	}
}

func TestClientKeyEntrypointsNativeCanceledEvents(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("successful native initialization action requires Linux")
	}
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	var backend *initializationBackend
	backend = &initializationBackend{initialize: func(ctx context.Context) error {
		if backend.calls.Load() > 1 {
			return nil
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}}
	model, pump := modelFixture(test, backend)
	model.Page = 3
	tester := ui.NewTester(model.View, 640, 600)
	click(test, tester, initializeClientKeyLabel)
	<-started
	tester.Frame()
	if !tester.HasText("正在初始化…") {
		test.Error("worker startup has no visible status")
	}
	click(test, tester, "取消任务")
	<-canceled
	tester.Frame()
	tester.Click(initializeClientKeyLabel)
	if backend.calls.Load() != 1 || model.pendingJobs.Load() != 1 || model.Busy {
		test.Error("native canceled button bypassed actual backend drain")
	}
	close(release)
	released = true
	pump()
	tester.Frame()
	click(test, tester, initializeClientKeyLabel)
	pump()
	if backend.calls.Load() != 2 || !strings.Contains(model.Status, "已就绪") {
		test.Fatal("native action did not reenable after actual drain")
	}
}

func TestClientKeyEntrypointsNativeDeadline(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		backend := &initializationBackend{initialize: func(ctx context.Context) error {
			deadline, present := ctx.Deadline()
			if !present || time.Until(deadline) != 30*time.Second {
				test.Error("initialization does not have the fixed 30s total budget")
			}
			<-ctx.Done()
			return ctx.Err()
		}}
		model, pump := modelFixture(test, backend)
		model.InitializeClientKey()
		pump()
		if model.Busy || model.pendingJobs.Load() != 0 || !strings.Contains(model.Status, "取消或超时") {
			test.Fatal("deadline did not drain initialization with a safe uncertain result")
		}
	})
	test.Run("SlotQueue", func(test *testing.T) {
		synctest.Test(test, func(test *testing.T) {
			backend := &initializationBackend{initialize: func(context.Context) error { return nil }}
			model, pump := modelFixture(test, backend)
			model.slots <- struct{}{}
			start := time.Now()
			model.InitializeClientKey()
			pump()
			<-model.slots
			if time.Since(start) != 30*time.Second || backend.calls.Load() != 0 || model.pendingJobs.Load() != 0 {
				test.Fatal("slot queue escaped the total initialization budget")
			}
		})
	})
}

func TestClientKeyEntrypointsErrorPrivacy(test *testing.T) {
	for _, sentinel := range []error{clientkey.ErrInitializationRequired, clientkey.ErrRecoveryRequired, clientkey.ErrUnavailable, clientkey.ErrUnsupported} {
		secret := "synthetic-private-key-path-token"
		err := errors.Join(sentinel, errors.New(secret))
		if hint := safeTaskError(err); hint == "" || strings.Contains(hint, secret) {
			test.Fatal("core error classification exposed raw private context")
		}
		if safeTaskError(errors.Join(err, context.Canceled)) != safeTaskError(context.Canceled) {
			test.Fatal("core classification masked uncertain cancellation")
		}
	}
}

func TestClientKeyEntrypointsNativeEvents(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("successful core initialization requires Linux runtime")
	}
	for _, scenario := range []struct {
		origin   string
		keyboard bool
		dark     bool
		width    int
	}{
		{"", false, false, 640},
		{"invalid origin", true, true, 640},
		{"https://offline.invalid", false, true, 920},
		{"", true, false, 920},
	} {
		test.Run(fmt.Sprintf("keyboard-%t-dark-%t-width-%d", scenario.keyboard, scenario.dark, scenario.width), func(test *testing.T) {
			home := test.TempDir()
			model, pump := modelFixture(test, &Service{Home: home})
			model.SetOrigin(scenario.origin)
			model.Page = 3
			tester := ui.NewTester(model.View, scenario.width, 600)
			tester.SetDark(scenario.dark)
			path := filepath.Join(home, ".config", "skillhub", "client.key")
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				test.Fatal("render or origin change initialized a key")
			}
			if scenario.keyboard {
				for range 24 {
					if tester.Focused(initializeClientKeyLabel) {
						break
					}
					tester.Key(0, ui.KeyTab)
				}
				if !tester.Focused(initializeClientKeyLabel) {
					test.Fatal("initialization is not reachable with a visible keyboard focus")
				}
				tester.Key(0, ui.KeyEnter)
			} else {
				click(test, tester, initializeClientKeyLabel)
			}
			pump()
			tester.Frame()
			key, err := clientkey.LoadExisting(test.Context(), home)
			defer clear(key)
			if err != nil || len(key) != 32 || !tester.HasText("本机客户端密钥已就绪（未初始化服务端会话）") || model.session != "" {
				test.Fatal("explicit native action did not initialize the shared core while offline")
			}
			if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
				test.Fatal("key initialization prepared installation targets")
			}
			click(test, tester, initializeClientKeyLabel)
			pump()
			tester.Frame()
			again, err := clientkey.LoadExisting(test.Context(), home)
			defer clear(again)
			if err != nil || !bytes.Equal(key, again) {
				test.Fatal("repeated initialization replaced the existing key")
			}
			if strings.Contains(strings.Join(tester.Texts(), " "), home) {
				test.Fatal("private home reached native status text")
			}
			if directory := os.Getenv("DESKTOP_EVIDENCE_DIR"); directory != "" {
				file, err := os.Create(filepath.Join(directory, fmt.Sprintf("entrypoints-%t-%d.png", scenario.dark, scenario.width)))
				if err != nil {
					test.Fatal("screenshot output unavailable")
				}
				encodeErr := png.Encode(file, tester.Image())
				closeErr := file.Close()
				if encodeErr != nil || closeErr != nil {
					test.Fatal("headless screenshot output failed")
				}
			}
		})
	}
}
