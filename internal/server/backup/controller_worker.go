package backup

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"uuid"
)

func (state *controllerState) run(ctx context.Context, active *controlActive) {
	committed, failure := state.execute(ctx, active.id)
	state.store.mutex.Lock()
	defer state.store.mutex.Unlock()
	critical := errors.Is(controlError(failure), ErrControlUncertain)
	if !state.store.uncertain {
		_, err := state.updateLocked(active.id, func(record *controlJob) error {
			record.Job.Code = controlFailureCode(failure)
			switch {
			case committed && failure == nil:
				record.Job.Phase = "remote_committed"
			case record.CompleteAttempted:
				record.Job.Phase = "commit_unknown"
			case state.closing.Load():
				record.Job.Phase, record.Job.Code = "interrupted", "interrupted"
			case record.Job.CancelRequested:
				record.Job.Phase, record.Job.Code = "canceled", "canceled"
			case errors.Is(failure, errControlPutUnknown):
				record.Job.Phase = "interrupted"
			default:
				record.Job.Phase = "failed"
			}
			return nil
		})
		if err != nil && !errors.Is(err, ErrControlBudget) {
			state.store.poison()
		}
	}
	if critical {
		state.store.poison()
	}
	active.cancel()
	state.active = nil
	close(active.done)
}

func (state *controllerState) execute(ctx context.Context, id string) (committed bool, failure error) {
	if err := state.change(id, func(record *controlJob) error {
		if record.Job.CancelRequested {
			return context.Canceled
		}
		record.Job.Phase = "capturing"
		return nil
	}); err != nil {
		return false, err
	}
	backupID, _ := uuid.Parse(id)
	var instance [16]byte
	_, _ = hex.Decode(instance[:], []byte(state.instance))
	captured, err := state.engine.CaptureSnapshot(ctx, state.service, CaptureOptions{InstanceID: instance, BackupID: [16]byte(backupID), AppVersion: state.options.AppVersion, Limits: state.options.ContentLimits})
	if err != nil {
		return false, err
	}
	defer func() {
		if err := captured.Close(); err != nil {
			failure = errors.Join(failure, err)
		}
		if state.targetUncertain() {
			failure = errors.Join(failure, ErrControlUncertain)
		}
	}()
	var catalog []controlObject
	var total int64
	if err := captured.WithFiles(ctx, func(_ context.Context, ref ObjectRef, _ *os.File) error {
		data := ref.read()
		catalog = append(catalog, controlObject{Kind: data.kind, Ordinal: data.ordinal, Size: data.size, SHA256: hex.EncodeToString(data.digest[:]), Attempt: hex.EncodeToString(data.attempt.attempt[:])})
		total += data.size
		return nil
	}); err != nil {
		return false, err
	}
	if err := state.change(id, func(record *controlJob) error {
		if record.Job.CancelRequested || ctx.Err() != nil {
			return context.Canceled
		}
		record.Catalog, record.Job.Objects, record.Job.CiphertextBytes = catalog, uint32(len(catalog)), total
		record.Job.Phase = "uploading"
		return nil
	}); err != nil {
		return false, err
	}
	var operationError error
	err = captured.WithFiles(ctx, func(ctx context.Context, ref ObjectRef, input *os.File) error {
		if ref.read().kind == Complete {
			operationError = state.change(id, func(record *controlJob) error {
				if record.Job.CancelRequested || ctx.Err() != nil {
					return context.Canceled
				}
				record.CompleteAttempted, record.Job.Phase = true, "committing"
				return nil
			})
			if operationError != nil {
				return operationError
			}
		}
		result, err := state.target.PutSealed(ctx, ref, input)
		if state.targetUncertain() {
			operationError = ErrControlUncertain
		} else if result == PutConflict {
			operationError = ErrControlConflict
		} else if err != nil || result != VerifiedNew && result != VerifiedExisting {
			operationError = errControlPutUnknown
		}
		return operationError
	})
	if errors.Is(controlError(err), ErrControlUncertain) {
		return false, errors.Join(err, operationError)
	}
	if operationError != nil {
		return false, operationError
	}
	return err == nil, err
}

func (state *controllerState) targetUncertain() bool {
	state.target.mutex.Lock()
	defer state.target.mutex.Unlock()
	return state.target.closeFailed
}

func controlFailureCode(failure error) string {
	if failure == nil {
		return ""
	}
	if errors.Is(controlError(failure), ErrControlUncertain) {
		return "uncertain"
	}
	if errors.Is(failure, errControlPutUnknown) {
		return "put_unknown"
	}
	switch controlError(failure) {
	case ErrControlBudget:
		return "budget"
	case ErrControlConflict:
		return "conflict"
	case ErrInvalid:
		return "invalid"
	case context.Canceled:
		return "canceled"
	default:
		return "unavailable"
	}
}
