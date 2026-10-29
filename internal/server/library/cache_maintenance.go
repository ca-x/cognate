package library

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

type cacheMaintenance struct {
	instance     *maintenance.Instance
	parent       string
	identity     os.FileInfo
	physicalRoot io.Closer
	mutex        sync.Mutex
	closing      bool
	active       int
	changed      chan struct{}
	closeOnce    sync.Once
	closeError   error
}

type cacheWriter struct {
	owner *cacheMaintenance
	live  bool
}

type cacheWriterContextKey struct{}

func beginCacheMaintenance(ctx context.Context, directory string) (context.Context, *cacheMaintenance, func(), error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || filepath.Dir(directory) == directory {
		return nil, nil, nil, maintenance.ErrDirectory
	}
	if err := maintenance.CheckPlatform(ctx); err != nil {
		return nil, nil, nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(directory))
	if err != nil {
		return nil, nil, nil, err
	}
	identity, err := os.Stat(parent)
	if err != nil {
		return nil, nil, nil, err
	}
	instance, err := maintenance.Join(ctx, parent)
	if err != nil {
		return nil, nil, nil, err
	}
	managed := &cacheMaintenance{instance: instance, parent: parent, identity: identity, changed: make(chan struct{})}
	ctx, release, err := managed.enter(ctx)
	if err != nil {
		_ = instance.Close()
		return nil, nil, nil, err
	}
	if err := managed.verifyParent(directory); err != nil {
		release()
		_ = instance.Close()
		return nil, nil, nil, err
	}
	return ctx, managed, release, nil
}

func (managed *cacheMaintenance) verifyParent(directory string) error {
	parent, err := filepath.EvalSymlinks(filepath.Dir(directory))
	if err != nil || parent != managed.parent {
		return maintenance.ErrDirectory
	}
	identity, err := os.Stat(parent)
	if err != nil || !os.SameFile(identity, managed.identity) {
		return maintenance.ErrDirectory
	}
	return nil
}

func (cache *ArchiveCache) enterWriter(ctx context.Context) (context.Context, func(), error) {
	if cache.maintenance == nil {
		return ctx, func() {}, nil
	}
	return cache.maintenance.enter(ctx)
}

func (managed *cacheMaintenance) enter(ctx context.Context) (context.Context, func(), error) {
	managed.mutex.Lock()
	defer managed.mutex.Unlock()
	parent, _ := ctx.Value(cacheWriterContextKey{}).(*cacheWriter)
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
	lease := &cacheWriter{owner: managed, live: true}
	managed.active++
	return context.WithValue(ctx, cacheWriterContextKey{}, lease), func() {
		managed.mutex.Lock()
		defer managed.mutex.Unlock()
		if lease.live {
			lease.live = false
			releaseInstance()
			managed.active--
			close(managed.changed)
			managed.changed = make(chan struct{})
		}
	}, nil
}

func (managed *cacheMaintenance) close() error {
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
		managed.closeError = managed.physicalRoot.Close()
		if managed.closeError == nil {
			managed.closeError = managed.instance.Close()
		}
	})
	return managed.closeError
}
