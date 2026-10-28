package subscription

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidWatchOptions = errors.New("invalid subscription watch options")

const (
	DefaultWatchInterval       = 5 * time.Minute
	DefaultWatchAttemptTimeout = 2 * time.Minute
)

type WatchOptions struct {
	Interval       time.Duration
	AttemptTimeout time.Duration
}

func (options WatchOptions) Validate() error {
	if options.Interval != 0 && (options.Interval < time.Second || options.Interval > 24*time.Hour) || options.AttemptTimeout != 0 && (options.AttemptTimeout < time.Second || options.AttemptTimeout > 10*time.Minute) {
		return ErrInvalidWatchOptions
	}
	return nil
}

type WatchReport struct {
	Result              SyncResult
	Succeeded           bool
	ConsecutiveFailures uint64
	NextAttemptIn       time.Duration
}

func Watch(ctx context.Context, options WatchOptions, cycle func(context.Context) (SyncResult, error), report func(context.Context, WatchReport) error) error {
	if options.Interval == 0 {
		options.Interval = DefaultWatchInterval
	}
	if options.AttemptTimeout == 0 {
		options.AttemptTimeout = DefaultWatchAttemptTimeout
	}
	if options.Validate() != nil || cycle == nil || report == nil {
		return ErrInvalidWatchOptions
	}
	retry := min(30*time.Second, options.Interval)
	var failures uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, options.AttemptTimeout)
		result, err := cycle(attempt)
		succeeded := err == nil && attempt.Err() == nil
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		next := options.Interval
		if succeeded {
			failures = 0
			retry = min(30*time.Second, options.Interval)
		} else {
			result = SyncResult{}
			if failures != ^uint64(0) {
				failures++
			}
			next = retry
			retry = min(2*retry, options.Interval)
		}
		if err := report(ctx, WatchReport{Result: result, Succeeded: succeeded, ConsecutiveFailures: failures, NextAttemptIn: next}); err != nil {
			return err
		}
		if err := waitForSync(ctx, next); err != nil {
			return err
		}
	}
}

func waitForSync(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
