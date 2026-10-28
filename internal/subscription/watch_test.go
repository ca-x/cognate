package subscription_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/subscription"
)

func TestWatchImmediateSerialDelayAndRetryReset(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		stop := errors.New("report stopped")
		start := time.Now()
		var starts []time.Duration
		var reports []subscription.WatchReport
		active := false
		err := subscription.Watch(test.Context(), subscription.WatchOptions{Interval: time.Minute}, func(context.Context) (subscription.SyncResult, error) {
			if active {
				test.Fatal("sync cycles overlapped")
			}
			active = true
			starts = append(starts, time.Since(start))
			time.Sleep(5 * time.Second)
			active = false
			if len(starts) == 4 {
				return subscription.SyncResult{Updated: 2}, nil
			}
			return subscription.SyncResult{Installed: 99, Backups: []string{"partial"}}, errors.New("synthetic private upstream failure")
		}, func(_ context.Context, report subscription.WatchReport) error {
			if active {
				test.Fatal("report ran before cycle completion")
			}
			reports = append(reports, report)
			if len(reports) == 5 {
				return stop
			}
			return nil
		})
		if !errors.Is(err, stop) {
			test.Fatal("report failure did not stop watch")
		}
		if !reflect.DeepEqual(starts, []time.Duration{0, 35 * time.Second, 100 * time.Second, 165 * time.Second, 230 * time.Second}) {
			test.Fatalf("unexpected serial cycle timing: %v", starts)
		}
		for index, report := range reports {
			if index == 3 {
				if !report.Succeeded || report.Result.Updated != 2 || report.ConsecutiveFailures != 0 || report.NextAttemptIn != time.Minute {
					test.Fatal("success did not reset retry state")
				}
				continue
			}
			expectedFailures := uint64(index + 1)
			expectedDelay := time.Minute
			if index == 0 || index == 4 {
				expectedDelay = 30 * time.Second
				expectedFailures = 1
			}
			if report.Succeeded || !reflect.DeepEqual(report.Result, subscription.SyncResult{}) || report.ConsecutiveFailures != expectedFailures || report.NextAttemptIn != expectedDelay {
				test.Fatal("failed cycle leaked partial success or retry state was incorrect")
			}
		}
	})
}

func TestWatchCancellationStopsWaitAndInFlightAttempt(test *testing.T) {
	for _, inFlight := range []bool{false, true} {
		test.Run(map[bool]string{false: "wait", true: "attempt"}[inFlight], func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				reports := 0
				completed := make(chan error, 1)
				go func() {
					completed <- subscription.Watch(ctx, subscription.WatchOptions{}, func(attempt context.Context) (subscription.SyncResult, error) {
						if inFlight {
							<-attempt.Done()
						}
						return subscription.SyncResult{Installed: 1}, nil
					}, func(context.Context, subscription.WatchReport) error {
						reports++
						return nil
					})
				}()
				synctest.Wait()
				cancel()
				if !errors.Is(<-completed, context.Canceled) {
					test.Fatal("watch did not propagate cancellation")
				}
				if inFlight && reports != 0 || !inFlight && reports != 1 {
					test.Fatal("canceled attempt produced a completion report")
				}
			})
		})
	}
}

func TestWatchAttemptDeadlineRejectsLateSuccess(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		stop := errors.New("finished")
		start := time.Now()
		err := subscription.Watch(test.Context(), subscription.WatchOptions{Interval: time.Second, AttemptTimeout: 2 * time.Second}, func(attempt context.Context) (subscription.SyncResult, error) {
			<-attempt.Done()
			return subscription.SyncResult{Updated: 8}, nil
		}, func(_ context.Context, report subscription.WatchReport) error {
			if report.Succeeded || !reflect.DeepEqual(report.Result, subscription.SyncResult{}) || report.NextAttemptIn != time.Second || time.Since(start) != 2*time.Second {
				test.Fatal("deadline was ignored or partial result escaped")
			}
			return stop
		})
		if !errors.Is(err, stop) {
			test.Fatal("deadline report did not terminate as requested")
		}
	})
}

func TestWatchRejectsInvalidOptionsBeforeCycle(test *testing.T) {
	options := []subscription.WatchOptions{
		{Interval: -time.Second}, {Interval: time.Nanosecond}, {Interval: 25 * time.Hour},
		{AttemptTimeout: -time.Second}, {AttemptTimeout: time.Nanosecond}, {AttemptTimeout: 11 * time.Minute},
	}
	cycle := func(context.Context) (subscription.SyncResult, error) {
		test.Fatal("invalid watch invoked sync")
		return subscription.SyncResult{}, nil
	}
	report := func(context.Context, subscription.WatchReport) error {
		test.Fatal("invalid watch emitted a report")
		return nil
	}
	for _, option := range options {
		if !errors.Is(subscription.Watch(test.Context(), option, cycle, report), subscription.ErrInvalidWatchOptions) {
			test.Fatal("invalid options accepted")
		}
	}
	if !errors.Is(subscription.Watch(test.Context(), subscription.WatchOptions{}, nil, report), subscription.ErrInvalidWatchOptions) || !errors.Is(subscription.Watch(test.Context(), subscription.WatchOptions{}, cycle, nil), subscription.ErrInvalidWatchOptions) {
		test.Fatal("nil callback accepted")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if !errors.Is(subscription.Watch(ctx, subscription.WatchOptions{}, cycle, report), context.Canceled) {
		test.Fatal("already canceled watch performed work")
	}
}
