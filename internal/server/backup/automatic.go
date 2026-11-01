package backup

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"
	"uuid"
)

type automaticSchedule struct {
	mutex   sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	stopped bool
}

func automaticIntervalValid(interval time.Duration) bool {
	return interval == 0 || interval >= time.Minute && interval <= 24*time.Hour
}

func (controller *Controller) AutomaticEnabled() bool {
	return controller != nil && controller.get != nil && controller.get().options.AutomaticInterval != 0
}

func (controller *Controller) StartAutomatic(ctx context.Context) error {
	state, err := controller.acquire(ctx)
	if err != nil {
		return err
	}
	defer state.store.mutex.Unlock()
	if !controller.AutomaticEnabled() {
		return nil
	}
	schedule := &state.automatic
	schedule.mutex.Lock()
	defer schedule.mutex.Unlock()
	if schedule.stopped || state.closing.Load() {
		return ErrClosed
	}
	if schedule.done != nil {
		return nil
	}
	lifetime, cancel := context.WithCancel(ctx)
	schedule.cancel, schedule.done = cancel, make(chan struct{})
	go func() {
		defer close(schedule.done)
		defer cancel()
		automaticLoop(lifetime, state.options.AutomaticInterval, controller.automaticStep)
	}()
	return nil
}

func (controller *Controller) StopAutomatic(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if controller == nil || controller.get == nil {
		return nil
	}
	schedule := &controller.get().automatic
	schedule.mutex.Lock()
	schedule.stopped = true
	if schedule.cancel != nil {
		schedule.cancel()
	}
	done := schedule.done
	schedule.mutex.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Join(ErrControlUncertain, ctx.Err())
	}
}

func automaticLoop(ctx context.Context, interval time.Duration, step func(context.Context) error) {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if ctx.Err() != nil {
				return
			}
			err := step(ctx)
			if err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrControlConflict) && !errors.Is(err, ErrControlBudget) {
				return
			}
			timer.Reset(interval)
		}
	}
}

func (controller *Controller) automaticStep(ctx context.Context) error {
	state, err := controller.acquire(ctx)
	if err != nil {
		return err
	}
	defer state.store.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.active != nil {
		return ErrBusy
	}
	for _, record := range state.store.data.Jobs {
		if record.Job.Phase == "interrupted" || record.Job.Phase == "commit_unknown" {
			return ErrControlConflict
		}
	}
	var instance [16]byte
	if _, err := hex.Decode(instance[:], []byte(state.instance)); err != nil {
		return ErrInvalid
	}
	_, err = state.startLocked(ctx, uuid.UUID(instance).String(), uuid.New().String())
	return err
}
