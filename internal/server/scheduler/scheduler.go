package scheduler

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type Options struct {
	Interval       time.Duration
	Timeout        time.Duration
	MaxUsers       int
	MaxCollections int
}

var (
	ErrClosed  = errors.New("GitHub list scheduler is closed")
	ErrBusy    = errors.New("GitHub list scheduler is already synchronizing")
	ErrStarted = errors.New("GitHub list scheduler is already started")
)

type Scheduler struct {
	store     *state.Store
	cache     *library.ArchiveCache
	fetcher   library.ArchiveFetcher
	client    *http.Client
	options   Options
	lifetime  context.Context
	cancel    context.CancelFunc
	mutex     sync.Mutex
	closed    bool
	started   bool
	running   bool
	lastUser  int
	active    sync.WaitGroup
	closeOnce sync.Once
}

func New(store *state.Store, cache *library.ArchiveCache, fetcher library.ArchiveFetcher, client *http.Client, options Options) (*Scheduler, error) {
	if store == nil || cache == nil || fetcher == nil {
		return nil, errors.New("GitHub list scheduler requires storage, cache, and an archive fetcher")
	}
	if options.Interval == 0 {
		options.Interval = 15 * time.Minute
	}
	if options.Timeout == 0 {
		options.Timeout = 2 * time.Minute
	}
	if options.MaxUsers == 0 {
		options.MaxUsers = 16
	}
	if options.MaxCollections == 0 {
		options.MaxCollections = 16
	}
	if options.Interval < 0 || options.Interval > 24*time.Hour || options.Timeout < 0 || options.Timeout > 10*time.Minute || options.MaxUsers < 1 || options.MaxUsers > 1000 || options.MaxCollections < 1 || options.MaxCollections > 1000 {
		return nil, errors.New("invalid GitHub list scheduler bounds")
	}
	if err := library.ValidateMaintenancePair(context.Background(), store, cache); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &Scheduler{store: store, cache: cache, fetcher: fetcher, client: client, options: options, lifetime: lifetime, cancel: cancel}, nil
}

func (scheduler *Scheduler) RunOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scheduler.mutex.Lock()
	if scheduler.closed {
		scheduler.mutex.Unlock()
		return ErrClosed
	}
	if scheduler.running {
		scheduler.mutex.Unlock()
		return ErrBusy
	}
	scheduler.running = true
	scheduler.active.Add(1)
	scheduler.mutex.Unlock()
	defer func() {
		scheduler.mutex.Lock()
		scheduler.running = false
		scheduler.mutex.Unlock()
		scheduler.active.Done()
	}()
	ctx, cancel := context.WithTimeout(ctx, scheduler.options.Timeout)
	stop := context.AfterFunc(scheduler.lifetime, cancel)
	defer func() { stop(); cancel() }()
	users, err := scheduler.store.ListSyncUsers(ctx, scheduler.lastUser, scheduler.options.MaxUsers)
	if err != nil {
		return err
	}
	if len(users) == 0 && scheduler.lastUser != 0 {
		scheduler.lastUser = 0
		users, err = scheduler.store.ListSyncUsers(ctx, 0, scheduler.options.MaxUsers)
		if err != nil {
			return err
		}
	}
	var failures []error
	for _, user := range users {
		if err := ctx.Err(); err != nil {
			return err
		}
		scheduler.lastUser = user.Cursor
		if err := scheduler.syncUser(ctx, user.Key); err != nil {
			failures = append(failures, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(failures...)
}

func (scheduler *Scheduler) syncUser(ctx context.Context, user string) error {
	credential, err := scheduler.store.Credential(ctx, user)
	if err != nil {
		return err
	}
	if expires, exists := credential.Token.ExpiresAt(); exists {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, expires)
		defer cancel()
	}
	client, err := github.NewListsClient(credential.Token, scheduler.client)
	if err != nil {
		return err
	}
	ticket, err := scheduler.store.BeginListSync(ctx, user, credential.Generation)
	if err != nil {
		return err
	}
	result, err := scheduler.store.ReadSelectedListSync(ctx, ticket, client)
	if err != nil {
		return err
	}
	if err := scheduler.store.ApplyListSync(ctx, result); err != nil {
		return err
	}
	candidates, err := scheduler.store.ListCollectionBatch(ctx, result, scheduler.options.MaxCollections)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scheduler.store.AdvanceListCollection(ctx, result, candidate.Cursor); err != nil {
			return err
		}
		collectionErr := scheduler.collect(ctx, user, result, candidate.Repository)
		if collectionErr != nil {
			failures = append(failures, collectionErr)
		}
	}
	return errors.Join(failures...)
}

func (scheduler *Scheduler) collect(ctx context.Context, user string, result state.ListSyncResult, entry state.GitHubRepository) error {
	reference, err := repository.ParseReference(entry.NameWithOwner, "")
	if err != nil {
		return err
	}
	access, err := scheduler.store.BeginListSourceAccess(ctx, result, entry.ID, reference)
	if err != nil {
		return err
	}
	existing, err := scheduler.store.LookupCollectedRepository(ctx, user, reference)
	if err == nil {
		access, err = library.AuthorizeSource(ctx, scheduler.store, scheduler.fetcher, access)
		if err != nil {
			return err
		}
		if err := scheduler.cache.Verify(ctx, existing.ArchiveHash); err != nil {
			return err
		}
		return scheduler.store.CheckSourceAccess(scheduler.store.WithSourceAccess(ctx, access), access)
	}
	if !errors.Is(err, state.ErrRepositoryNotFound) {
		return err
	}
	if err := scheduler.store.CheckRepositoryCapacity(ctx, user); err != nil {
		return err
	}
	snapshot, access, err := library.FetchSource(ctx, scheduler.store, scheduler.fetcher, access)
	if err != nil {
		return err
	}
	if snapshot == nil || len(snapshot.Skills) == 0 {
		return repository.ErrArchiveInvalid
	}
	saved, err := scheduler.store.WithMaintenanceWriter(ctx, func(ctx context.Context) (state.CollectedRepository, error) {
		if err := scheduler.cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
			return state.CollectedRepository{}, err
		}
		return scheduler.store.CollectListRepository(scheduler.store.WithSourceAccess(ctx, access), result, entry.ID, reference, snapshot.Hash, snapshot.Skills)
	})
	if err != nil {
		return err
	}
	return scheduler.cache.Verify(ctx, saved.ArchiveHash)
}

func (scheduler *Scheduler) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if scheduler.closed {
		return ErrClosed
	}
	if scheduler.started {
		return ErrStarted
	}
	scheduler.started = true
	scheduler.active.Go(func() {
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(scheduler.lifetime, cancel)
		defer func() { stop(); cancel() }()
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				_ = scheduler.RunOnce(ctx)
				timer.Reset(scheduler.options.Interval)
			}
		}
	})
	return nil
}

func (scheduler *Scheduler) Close() error {
	scheduler.closeOnce.Do(func() {
		scheduler.mutex.Lock()
		scheduler.closed = true
		scheduler.cancel()
		scheduler.mutex.Unlock()
		scheduler.active.Wait()
	})
	return nil
}
