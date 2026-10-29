package state

import (
	"context"
	"sync"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

type storeMaintenance struct {
	instance   *maintenance.Instance
	mutex      sync.Mutex
	closing    bool
	active     int
	changed    chan struct{}
	closeOnce  sync.Once
	closeError error
}

type storeWriter struct {
	owner *storeMaintenance
	live  bool
}

type storeWriterContextKey struct{}

func (store *Store) enterWriter(ctx context.Context) (context.Context, func(), error) {
	if store.state.maintenance == nil {
		return ctx, func() {}, nil
	}
	return store.state.maintenance.enter(ctx)
}

func (managed *storeMaintenance) enter(ctx context.Context) (context.Context, func(), error) {
	managed.mutex.Lock()
	defer managed.mutex.Unlock()
	parent, _ := ctx.Value(storeWriterContextKey{}).(*storeWriter)
	if parent != nil && parent.owner == managed && !parent.live {
		return nil, nil, maintenance.ErrLease
	}
	if managed.closing && (parent == nil || parent.owner != managed || !parent.live) {
		return nil, nil, maintenance.ErrClosed
	}
	ctx, releaseInstance, err := managed.instance.Enter(ctx)
	if err != nil {
		return nil, nil, err
	}
	lease := &storeWriter{owner: managed, live: true}
	managed.active++
	release := func() {
		managed.mutex.Lock()
		defer managed.mutex.Unlock()
		if lease.live {
			lease.live = false
			releaseInstance()
			managed.active--
			close(managed.changed)
			managed.changed = make(chan struct{})
		}
	}
	return context.WithValue(ctx, storeWriterContextKey{}, lease), release, nil
}

func (managed *storeMaintenance) close(closeClient func() error) error {
	managed.closeOnce.Do(func() {
		managed.mutex.Lock()
		managed.closing = true
		close(managed.changed)
		managed.changed = make(chan struct{})
		for managed.active != 0 {
			changed := managed.changed
			managed.mutex.Unlock()
			<-changed
			managed.mutex.Lock()
		}
		managed.mutex.Unlock()
		managed.closeError = closeClient()
		if managed.closeError != nil {
			return
		}
		managed.closeError = managed.instance.Close()
	})
	return managed.closeError
}
