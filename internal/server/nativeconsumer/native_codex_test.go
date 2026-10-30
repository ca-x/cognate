package nativeconsumer

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
)

func TestCodexNativeServiceLifetime(test *testing.T) {
	service := newLifecycleFixture(test)
	service.prune = func(context.Context) (state.NativePruneResult, error) { return state.NativePruneResult{}, nil }
	synctest.Test(test, func(test *testing.T) {
		if service.Start(test.Context()) != nil {
			test.Fatal("cannot start shared lifecycle")
		}
		entered, finished := make(chan struct{}), make(chan error, 1)
		go func() {
			finished <- service.Call(test.Context(), func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) != 20*time.Second {
					return state.ErrNativeInvalid
				}
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			})
		}()
		<-entered
		synctest.Wait()
		time.Sleep(19 * time.Second)
		select {
		case <-finished:
			test.Fatal("full-request budget shortened")
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if !errors.Is(<-finished, context.DeadlineExceeded) {
			test.Fatal("real callback did not receive exact shared 20-second cancellation")
		}
		service.Close()
		if err := service.Call(test.Context(), func(context.Context) error { test.Error("closed callback ran"); return nil }); !errors.Is(err, state.ErrNativeUnavailable) {
			test.Fatal("closed Codex caller bypassed common admission")
		}
	})
}
