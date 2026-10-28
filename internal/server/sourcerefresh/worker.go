package sourcerefresh

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

const DefaultInterval = 15 * time.Minute

var (
	ErrBusy    = errors.New("source refresh worker is already running")
	ErrClosed  = errors.New("source refresh worker is closed")
	ErrStarted = errors.New("source refresh worker is already started")
)

type Options struct {
	Interval, Timeout time.Duration
	MaxOwners         int
	DataDirectory     string
}
type Worker struct {
	store                    *state.Store
	library                  *library.Service
	options                  Options
	cursor                   state.SourceRefreshCursor
	mutex                    sync.Mutex
	closed, started, running bool
	lifetime                 context.Context
	cancel                   context.CancelFunc
	active                   sync.WaitGroup
	closeOnce                sync.Once
}

func New(store *state.Store, service *library.Service, options Options) (*Worker, error) {
	if store == nil || service == nil {
		return nil, errors.New("source refresh requires storage and library")
	}
	if options.Interval == 0 {
		options.Interval = DefaultInterval
	}
	if options.Timeout == 0 {
		options.Timeout = 2 * time.Minute
	}
	if options.MaxOwners == 0 {
		options.MaxOwners = 16
	}
	if options.Interval < time.Second || options.Interval > 24*time.Hour || options.Timeout < time.Millisecond || options.Timeout > 2*time.Minute || options.MaxOwners < 1 || options.MaxOwners > 16 {
		return nil, errors.New("invalid source refresh bounds")
	}
	if !filepath.IsAbs(options.DataDirectory) || filepath.Clean(options.DataDirectory) != options.DataDirectory {
		return nil, errors.New("source refresh requires a trusted data directory")
	}
	directory, err := state.OpenPrivateDirectory(options.DataDirectory)
	if err != nil {
		return nil, err
	}
	if err := directory.Close(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &Worker{store: store, library: service, options: options, lifetime: lifetime, cancel: cancel}, nil
}

func (worker *Worker) RunOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	worker.mutex.Lock()
	if worker.closed {
		worker.mutex.Unlock()
		return ErrClosed
	}
	if worker.running {
		worker.mutex.Unlock()
		return ErrBusy
	}
	worker.running = true
	worker.active.Add(1)
	worker.mutex.Unlock()
	defer func() {
		worker.mutex.Lock()
		worker.running = false
		worker.mutex.Unlock()
		worker.active.Done()
	}()
	ctx, cancel := context.WithTimeout(ctx, worker.options.Timeout)
	defer cancel()
	stop := context.AfterFunc(worker.lifetime, cancel)
	defer stop()
	lockContext, cancelLock := context.WithTimeout(ctx, 250*time.Millisecond)
	lock, err := filelock.Acquire(lockContext, filepath.Join(worker.options.DataDirectory, ".source-refresh.lock"))
	cancelLock()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrBusy
	}
	defer lock.Close()
	batch, err := worker.store.ListSourceRefreshBatch(ctx, worker.cursor, worker.options.MaxOwners)
	if err != nil {
		return err
	}
	if len(batch.Items) == 0 {
		worker.cursor = batch.Next
	}
	var failures []error
	for _, item := range batch.Items {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		worker.cursor = item.Next
		ticket, err := worker.store.BeginSourceRefresh(ctx, item.Target)
		if err == nil {
			_, err = worker.library.RefreshAutomatic(ctx, ticket)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(append(failures, ctx.Err())...)
}

func (worker *Worker) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	if worker.closed {
		return ErrClosed
	}
	if worker.started {
		return ErrStarted
	}
	worker.started = true
	worker.active.Go(func() {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(worker.lifetime, cancel)
		defer stop()
		timer := time.NewTimer(worker.options.Interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				_ = worker.RunOnce(ctx)
				timer.Reset(worker.options.Interval)
			}
		}
	})
	return nil
}

func (worker *Worker) Close() error {
	worker.closeOnce.Do(func() {
		worker.mutex.Lock()
		worker.closed = true
		worker.cancel()
		worker.mutex.Unlock()
		worker.active.Wait()
	})
	return nil
}
