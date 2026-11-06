package backup

import (
	"errors"
	"testing"
)

func TestControllerReconfigurationRejectsBusyAndUncertain(test *testing.T) {
	for _, phase := range []string{"idle", "active", "pending", "capturing", "uploading", "committing", "interrupted", "commit_unknown", "uncertain"} {
		test.Run(phase, func(test *testing.T) {
			controller, _, _ := controllerFixture(test)
			state := controller.get()
			state.store.mutex.Lock()
			jobs := state.store.data.Jobs
			switch phase {
			case "idle":
			case "active":
				state.active = &controlActive{}
			case "uncertain":
				state.store.uncertain = true
			default:
				state.store.data.Jobs = append(state.store.data.Jobs, controlJob{Job: Job{Phase: phase}})
			}
			state.store.mutex.Unlock()
			err := controller.CheckReconfiguration(test.Context())
			state.store.mutex.Lock()
			state.active, state.store.uncertain, state.store.data.Jobs = nil, false, jobs
			state.store.mutex.Unlock()
			switch phase {
			case "idle":
				if err != nil {
					test.Fatal("idle controller cannot be reconfigured", err)
				}
			case "uncertain":
				if !errors.Is(err, ErrControlUncertain) {
					test.Fatal("uncertain controller permitted reconfiguration", err)
				}
			default:
				if !errors.Is(err, ErrBusy) {
					test.Fatal("busy or unresolved controller permitted reconfiguration", err)
				}
			}
		})
	}
}
