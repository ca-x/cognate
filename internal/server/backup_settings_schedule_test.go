package server

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestBackupSettingsScheduleDisableReenableFirstDelay(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		wake, done := make(chan struct{}, 1), make(chan struct{})
		var interval, revision, calls, observed atomic.Int64
		interval.Store(int64(time.Minute))
		revision.Store(1)
		go func() {
			defer close(done)
			backupSettingsSchedule(ctx, wake, func() (time.Duration, int64) {
				return time.Duration(interval.Load()), revision.Load()
			}, func(_ context.Context, current int64) {
				observed.Store(current)
				calls.Add(1)
			})
		}()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		interval.Store(0)
		revision.Store(2)
		wake <- struct{}{}
		synctest.Wait()
		time.Sleep(2 * time.Hour)
		if calls.Load() != 0 {
			test.Fatal("disabled timer produced work")
		}
		interval.Store(int64(time.Minute))
		revision.Store(3)
		wake <- struct{}{}
		synctest.Wait()
		time.Sleep(time.Minute - time.Nanosecond)
		if calls.Load() != 0 {
			test.Fatal("re-enabled timer skipped first full delay")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 1 || observed.Load() != 3 {
			test.Fatal("re-enabled timer remained permanently stopped")
		}
		time.Sleep(30 * time.Second)
		interval.Store(int64(2 * time.Minute))
		revision.Store(4)
		wake <- struct{}{}
		synctest.Wait()
		time.Sleep(2*time.Minute - time.Nanosecond)
		if calls.Load() != 1 {
			test.Fatal("changed interval inherited old due slot")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 2 || observed.Load() != 4 {
			test.Fatal("changed interval not applied")
		}
		cancel()
		<-done
		time.Sleep(time.Hour)
		if calls.Load() != 2 {
			test.Fatal("stopped timer produced work")
		}
	})
}
