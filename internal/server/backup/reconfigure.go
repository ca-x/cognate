package backup

import "context"

func (controller *Controller) Enabled() bool { return controller != nil }

func (controller *Controller) AutomaticStep(ctx context.Context) error {
	return controller.automaticStep(ctx)
}

func (controller *Controller) CheckReconfiguration(ctx context.Context) error {
	state, err := controller.acquire(ctx)
	if err != nil {
		return err
	}
	defer state.store.mutex.Unlock()
	if state.active != nil {
		return ErrBusy
	}
	if err := state.store.check(); err != nil {
		return err
	}
	for _, record := range state.store.data.Jobs {
		if controlRunning(record.Job.Phase) || record.Job.Phase == "interrupted" || record.Job.Phase == "commit_unknown" {
			return ErrBusy
		}
	}
	return nil
}
