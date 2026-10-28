package library

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/state"
)

var (
	ErrClosed         = errors.New("repository library is closed")
	ErrCapacity       = errors.New("repository collection capacity exhausted")
	ErrArchiveMissing = errors.New("saved repository archive is missing or corrupt")
)

type ArchiveFetcher interface {
	Fetch(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)
}

type Service struct {
	store      *state.Store
	cache      *ArchiveCache
	fetcher    ArchiveFetcher
	lifetime   context.Context
	cancel     context.CancelFunc
	mutex      sync.Mutex
	closed     bool
	active     sync.WaitGroup
	slots      chan struct{}
	closeOnce  sync.Once
	closeError error
}

func NewService(store *state.Store, cache *ArchiveCache, fetcher ArchiveFetcher) (*Service, error) {
	if store == nil || cache == nil || fetcher == nil {
		return nil, errors.New("repository library requires storage, cache, and an archive fetcher")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: store, cache: cache, fetcher: fetcher, lifetime: ctx, cancel: cancel, slots: make(chan struct{}, 2)}, nil
}

func (service *Service) Collect(ctx context.Context, user, input, branch string) (state.CollectedRepository, error) {
	reference, err := repository.ParseReference(input, branch)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	defer done()
	if err := service.reserveSlot(); err != nil {
		return state.CollectedRepository{}, err
	}
	defer func() { <-service.slots }()
	existing, err := service.store.LookupCollectedRepository(ctx, user, reference)
	if err == nil {
		return service.verifiedRepository(ctx, existing)
	}
	if !errors.Is(err, state.ErrRepositoryNotFound) {
		return state.CollectedRepository{}, err
	}
	if err := service.store.CheckRepositoryCapacity(ctx, user); err != nil {
		return state.CollectedRepository{}, err
	}
	snapshot, err := service.fetcher.Fetch(ctx, reference)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	if snapshot == nil || len(snapshot.Skills) == 0 {
		return state.CollectedRepository{}, repository.ErrArchiveInvalid
	}
	if err := service.cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
		return state.CollectedRepository{}, err
	}
	entry, err := service.store.CollectRepository(ctx, user, reference, snapshot.Hash, snapshot.Skills)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	return service.verifiedRepository(ctx, entry)
}

func (service *Service) List(ctx context.Context, user string) ([]state.CollectedRepository, error) {
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return service.store.ListCollectedRepositories(ctx, user)
}

func (service *Service) Get(ctx context.Context, user, id string) (state.CollectedRepository, error) {
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	defer done()
	entry, err := service.store.CollectedRepository(ctx, user, id)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	if err := service.reserveSlot(); err != nil {
		return state.CollectedRepository{}, err
	}
	defer func() { <-service.slots }()
	return service.verifiedRepository(ctx, entry)
}

func (service *Service) verifiedRepository(ctx context.Context, entry state.CollectedRepository) (state.CollectedRepository, error) {
	if err := service.cache.Verify(ctx, entry.ArchiveHash); err != nil {
		if ctx.Err() != nil {
			return state.CollectedRepository{}, ctx.Err()
		}
		return state.CollectedRepository{}, ErrArchiveMissing
	}
	return entry, nil
}

func (service *Service) reserveSlot() error {
	select {
	case service.slots <- struct{}{}:
		return nil
	default:
		return ErrCapacity
	}
}

func (service *Service) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	service.mutex.Lock()
	if service.closed {
		service.mutex.Unlock()
		return nil, nil, ErrClosed
	}
	service.active.Add(1)
	service.mutex.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	stop := context.AfterFunc(service.lifetime, cancel)
	return ctx, func() { stop(); cancel(); service.active.Done() }, nil
}

func (service *Service) Close() error {
	service.closeOnce.Do(func() {
		service.mutex.Lock()
		service.closed = true
		service.cancel()
		service.mutex.Unlock()
		service.active.Wait()
		service.closeError = service.cache.Close()
	})
	return service.closeError
}
