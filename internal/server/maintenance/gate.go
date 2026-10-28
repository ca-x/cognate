package maintenance

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrClosed      = errors.New("maintenance handle is closed")
	ErrQuiescing   = errors.New("maintenance admission is closed")
	ErrLease       = errors.New("maintenance lease is not live for this instance")
	ErrDirectory   = errors.New("maintenance directory is not private and stable")
	ErrLock        = errors.New("maintenance instance lock is unavailable")
	ErrUnsupported = errors.New("maintenance directory security is unsupported on this platform")
)

type Instance struct {
	shared *sharedGate
	handle *instanceHandle
}

type instanceHandle struct {
	closing    bool
	finalizing bool
	active     int
	exclusive  int
	done       chan struct{}
	err        error
}

type sharedGate struct {
	mutex     sync.Mutex
	changed   chan struct{}
	release   func() error
	writers   int
	exclusive *Exclusive
}

type writerLease struct {
	shared *sharedGate
	live   bool
}

type leaseContextKey struct{}

type Exclusive struct {
	shared *sharedGate
	owner  *instanceHandle
	live   bool
	ready  bool
}

func (instance *Instance) Enter(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if instance == nil || instance.shared == nil || instance.handle == nil {
		return nil, nil, ErrClosed
	}
	shared, handle := instance.shared, instance.handle
	parent, _ := ctx.Value(leaseContextKey{}).(*writerLease)
	shared.mutex.Lock()
	defer shared.mutex.Unlock()
	if handle.finalizing {
		return nil, nil, ErrClosed
	}
	if parent != nil && (parent.shared != shared || !parent.live) {
		return nil, nil, ErrLease
	}
	if handle.closing && parent == nil {
		return nil, nil, ErrClosed
	}
	if shared.exclusive != nil && parent == nil {
		return nil, nil, ErrQuiescing
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	lease := &writerLease{shared: shared, live: true}
	shared.writers++
	handle.active++
	release := func() {
		shared.mutex.Lock()
		defer shared.mutex.Unlock()
		if lease.live {
			lease.live = false
			shared.writers--
			handle.active--
			shared.notify()
		}
	}
	return context.WithValue(ctx, leaseContextKey{}, lease), release, nil
}

func (instance *Instance) Quiesce(ctx context.Context) (*Exclusive, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if instance == nil || instance.shared == nil || instance.handle == nil {
		return nil, ErrClosed
	}
	if ctx.Value(leaseContextKey{}) != nil {
		return nil, ErrLease
	}
	shared, handle := instance.shared, instance.handle
	shared.mutex.Lock()
	defer shared.mutex.Unlock()
	if handle.closing {
		return nil, ErrClosed
	}
	if shared.exclusive != nil {
		return nil, ErrQuiescing
	}
	token := &Exclusive{shared: shared, owner: handle, live: true}
	shared.exclusive = token
	handle.exclusive++
	shared.notify()
	for {
		if err := ctx.Err(); err != nil {
			token.releaseLocked()
			return nil, err
		}
		if handle.closing {
			token.releaseLocked()
			return nil, ErrClosed
		}
		if shared.writers == 0 {
			token.ready = true
			return token, nil
		}
		changed := shared.changed
		shared.mutex.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		shared.mutex.Lock()
	}
}

func (token *Exclusive) ValidFor(instance *Instance) bool {
	if token == nil || token.shared == nil || instance == nil || instance.handle == nil || token.shared != instance.shared {
		return false
	}
	token.shared.mutex.Lock()
	defer token.shared.mutex.Unlock()
	return token.live && token.ready && token.shared.exclusive == token && !instance.handle.finalizing
}

func (token *Exclusive) Release() {
	if token == nil || token.shared == nil {
		return
	}
	token.shared.mutex.Lock()
	defer token.shared.mutex.Unlock()
	token.releaseLocked()
}

func (token *Exclusive) releaseLocked() {
	if token.live && token.shared.exclusive == token {
		token.live = false
		token.shared.exclusive = nil
		token.owner.exclusive--
		token.shared.notify()
	}
}

func (instance *Instance) Close() error {
	if instance == nil || instance.shared == nil || instance.handle == nil {
		return nil
	}
	shared, handle := instance.shared, instance.handle
	shared.mutex.Lock()
	if handle.closing {
		shared.mutex.Unlock()
		<-handle.done
		return handle.err
	}
	handle.closing = true
	shared.notify()
	for handle.active != 0 || handle.exclusive != 0 {
		changed := shared.changed
		shared.mutex.Unlock()
		<-changed
		shared.mutex.Lock()
	}
	handle.finalizing = true
	shared.mutex.Unlock()
	err := shared.release()
	shared.mutex.Lock()
	handle.err = err
	close(handle.done)
	shared.mutex.Unlock()
	return err
}

func (shared *sharedGate) notify() {
	close(shared.changed)
	shared.changed = make(chan struct{})
}
