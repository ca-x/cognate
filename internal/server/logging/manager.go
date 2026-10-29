package logging

import (
	"context"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
)

const lockBudget = 200 * time.Millisecond

type Manager struct {
	files               fileOperations
	directory           string
	dataDirectory       string
	identity            os.FileInfo
	dataIdentity        os.FileInfo
	lockIdentity        os.FileInfo
	allowInitialization bool
	life                context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	mutex               sync.Mutex
	closing             bool
	active              sync.WaitGroup
	retained            []io.Closer
	closeOnce           sync.Once
	closeError          error
	writeFailures       atomic.Uint64
	cleanupFailures     atomic.Uint64
	droppedRecords      atomic.Uint64
	warningMutex        sync.Mutex
	warningTime         time.Time
	warningWriter       io.Writer
}

func Open(ctx context.Context, dataDirectory string) (*Manager, error) {
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if !filepath.IsAbs(dataDirectory) || filepath.Clean(dataDirectory) != dataDirectory {
		return nil, ErrInvalid
	}
	data, err := state.OpenPrivateDirectory(dataDirectory)
	if err != nil {
		return nil, ErrUnavailable
	}
	dataIdentity, inspectError := data.Stat(".")
	if closeError := data.Close(); inspectError != nil || closeError != nil {
		return nil, ErrUnavailable
	}
	directory := filepath.Join(dataDirectory, "logs")
	root, err := state.OpenPrivateDirectory(directory)
	if err != nil {
		return nil, ErrUnavailable
	}
	identity, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, ErrUnavailable
	}
	life, cancel := context.WithCancel(context.Background())
	manager := &Manager{files: rootFiles{root: root}, directory: directory, dataDirectory: dataDirectory, identity: identity, dataIdentity: dataIdentity, life: life, cancel: cancel, done: make(chan struct{}), warningWriter: os.Stderr}
	_, err = manager.withLock(ctx, func(ctx context.Context) (Settings, error) {
		settings, err := manager.readSettings(true)
		if err != nil {
			return Settings{}, err
		}
		err = manager.recoverTails(ctx, settings)
		return settings, err
	})
	if err != nil {
		cancel()
		_ = manager.files.Close()
		return nil, ErrUnavailable
	}
	go manager.retentionLoop()
	return manager, nil
}

func (manager *Manager) enter() error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.closing {
		return ErrClosed
	}
	manager.active.Add(1)
	return nil
}

func (manager *Manager) health() Health {
	value := Health{WriteFailures: manager.writeFailures.Load(), CleanupFailures: manager.cleanupFailures.Load(), DroppedRecords: manager.droppedRecords.Load()}
	value.Degraded = value.WriteFailures != 0 || value.CleanupFailures != 0 || value.DroppedRecords != 0
	return value
}

func (manager *Manager) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := manager.enter(); err != nil {
		return Snapshot{}, err
	}
	defer manager.active.Done()
	settings, err := manager.withLock(ctx, func(context.Context) (Settings, error) { return manager.readSettings(false) })
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	return Snapshot{Settings: settings, Health: manager.health()}, nil
}

func (manager *Manager) Update(ctx context.Context, expectedRevision int64, proposed Settings) (Snapshot, error) {
	if err := manager.enter(); err != nil {
		return Snapshot{}, err
	}
	defer manager.active.Done()
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 || !proposed.valid() || proposed.Revision != 0 && proposed.Revision != expectedRevision {
		return Snapshot{}, ErrInvalid
	}
	settings, err := manager.withLock(ctx, func(ctx context.Context) (Settings, error) {
		current, err := manager.readSettings(false)
		if err != nil {
			return Settings{}, err
		}
		if current.Revision != expectedRevision {
			return Settings{}, ErrConflict
		}
		proposed.Revision = current.Revision + 1
		if _, err := manager.enforcePolicy(ctx, proposed, 0, 4096); err != nil {
			return Settings{}, err
		}
		if ctx.Err() != nil {
			return Settings{}, ErrUnavailable
		}
		if err := manager.publishSettings(proposed); err != nil {
			return Settings{}, err
		}
		return proposed, nil
	})
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			manager.writeFailures.Add(1)
			manager.warn()
		}
		return Snapshot{}, err
	}
	return Snapshot{Settings: settings, Health: manager.health()}, nil
}

func (manager *Manager) Record(event Event) error {
	level, err := event.level()
	if err != nil {
		manager.droppedRecords.Add(1)
		return ErrInvalid
	}
	if err := manager.enter(); err != nil {
		manager.droppedRecords.Add(1)
		return err
	}
	defer manager.active.Done()
	_, err = manager.withLock(manager.life, func(ctx context.Context) (Settings, error) {
		settings, err := manager.readSettings(false)
		if err != nil {
			return Settings{}, err
		}
		if level < settings.level() {
			_, err := manager.enforcePolicy(ctx, settings, 0, 0)
			return settings, err
		}
		data, err := encodeEvent(event, level, time.Now())
		if err != nil {
			return Settings{}, err
		}
		return settings, manager.appendRecord(ctx, settings, data)
	})
	if err != nil {
		manager.writeFailures.Add(1)
		manager.droppedRecords.Add(1)
		manager.warn()
		return ErrUnavailable
	}
	return nil
}

func (manager *Manager) retentionLoop() {
	defer close(manager.done)
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-manager.life.Done():
			return
		case <-timer.C:
			manager.retentionPass()
		}
	}
}

func (manager *Manager) retentionPass() {
	if manager.enter() != nil {
		return
	}
	defer manager.active.Done()
	enforced := false
	_, err := manager.withLock(manager.life, func(ctx context.Context) (Settings, error) {
		settings, err := manager.readSettings(false)
		if err != nil {
			return Settings{}, err
		}
		enforced = true
		_, err = manager.enforcePolicy(ctx, settings, 0, 0)
		return settings, err
	})
	if err != nil {
		if !enforced {
			manager.cleanupFailures.Add(1)
		}
		manager.warn()
	}
}

func (manager *Manager) warn() {
	if !manager.warningMutex.TryLock() {
		return
	}
	defer manager.warningMutex.Unlock()
	now := time.Now()
	if !manager.warningTime.IsZero() && now.Sub(manager.warningTime) < time.Minute {
		return
	}
	manager.warningTime = now
	if file, ok := manager.warningWriter.(*os.File); ok {
		writeWarning(file)
	}
}

func (manager *Manager) retain(resource io.Closer) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.retained = append(manager.retained, resource)
	manager.closing = true
	manager.cancel()
}

func (manager *Manager) closeFile(file io.Closer) error {
	if err := file.Close(); err != nil {
		manager.retain(file)
		return ErrUnavailable
	}
	return nil
}

func (manager *Manager) Close() error {
	manager.closeOnce.Do(func() {
		manager.mutex.Lock()
		manager.closing = true
		manager.cancel()
		manager.mutex.Unlock()
		<-manager.done
		manager.active.Wait()
		if err := manager.files.Close(); err != nil {
			manager.closeError = ErrUnavailable
			manager.writeFailures.Add(1)
			manager.warn()
		}
		manager.mutex.Lock()
		if len(manager.retained) != 0 {
			manager.closeError = ErrUnavailable
		}
		manager.mutex.Unlock()
	})
	return manager.closeError
}

type transportWriter struct{ manager *Manager }

func (writer transportWriter) Write(data []byte) (int, error) {
	_ = writer.manager.Record(Event{Kind: EventTransportError})
	return len(data), nil
}

func (manager *Manager) HTTPErrorLog() *log.Logger {
	return log.New(transportWriter{manager: manager}, "", 0)
}

func publicError(err error) error {
	for _, known := range []error{ErrInvalid, ErrConflict, ErrClosed} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}
