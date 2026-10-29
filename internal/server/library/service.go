package library

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
)

var (
	ErrClosed         = errors.New("repository library is closed")
	ErrCapacity       = errors.New("repository collection capacity exhausted")
	ErrArchiveMissing = errors.New("saved repository archive is missing or corrupt")
)

type ArchiveFetcher interface {
	Fetch(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)
}

type AuthenticatedArchiveFetcher interface {
	FetchMetadata(context.Context, repository.Reference, github.Token, func(context.Context) error) (repository.GitHubMetadata, error)
	FetchPrivate(context.Context, repository.Reference, github.Token, func(context.Context) error) (*repository.ArchiveSnapshot, error)
}

func AuthorizeSource(ctx context.Context, store *state.Store, fetcher ArchiveFetcher, ticket state.SourceAccessTicket) (state.SourceAccessTicket, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	check := func(ctx context.Context) error { return store.CheckSourceAccess(ctx, ticket) }
	if err := check(ctx); err != nil {
		return ticket, err
	}
	if !ticket.Authenticated() {
		return ticket, nil
	}
	private, valid := fetcher.(AuthenticatedArchiveFetcher)
	if !valid {
		return ticket, state.ErrSourceAccessUnavailable
	}
	metadata, err := private.FetchMetadata(ctx, ticket.Reference(), ticket.Token(), check)
	if err != nil {
		return ticket, err
	}
	return store.BindSourceMetadata(ctx, ticket, metadata)
}

func FetchSource(ctx context.Context, store *state.Store, fetcher ArchiveFetcher, ticket state.SourceAccessTicket) (*repository.ArchiveSnapshot, state.SourceAccessTicket, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	deadline := ticket.Deadline()
	if current, exists := ctx.Deadline(); !deadline.IsZero() && (!exists || deadline.Before(current)) {
		var expire context.CancelFunc
		ctx, expire = context.WithDeadline(ctx, deadline)
		defer expire()
	}
	ticket, err := AuthorizeSource(ctx, store, fetcher, ticket)
	if err != nil {
		return nil, ticket, err
	}
	check := func(ctx context.Context) error { return store.CheckSourceAccess(ctx, ticket) }
	var snapshot *repository.ArchiveSnapshot
	if ticket.Authenticated() {
		private := fetcher.(AuthenticatedArchiveFetcher)
		snapshot, err = private.FetchPrivate(ctx, ticket.Reference(), ticket.Token(), check)
	} else {
		snapshot, err = fetcher.Fetch(ctx, ticket.Reference())
	}
	if err != nil {
		return nil, ticket, err
	}
	if err := check(ctx); err != nil {
		return nil, ticket, err
	}
	if snapshot == nil || len(snapshot.Skills) == 0 {
		return nil, ticket, repository.ErrArchiveInvalid
	}
	return snapshot, ticket, nil
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
	if err := ValidateMaintenancePair(context.Background(), store, cache); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: store, cache: cache, fetcher: fetcher, lifetime: ctx, cancel: cancel, slots: make(chan struct{}, 2)}, nil
}

func ValidateMaintenancePair(ctx context.Context, store *state.Store, cache *ArchiveCache) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || cache == nil {
		return errors.New("repository library requires storage and cache")
	}
	if store.MaintenanceManaged() != (cache.maintenance != nil) {
		return errors.New("repository library maintenance modes do not match")
	}
	if store.MaintenanceManaged() {
		_, err := store.WithMaintenanceWriter(ctx, func(ctx context.Context) (struct{}, error) {
			_, release, err := cache.enterWriter(ctx)
			if err != nil {
				return struct{}{}, err
			}
			defer release()
			return struct{}{}, cache.verifyDirectory()
		})
		return err
	}
	return nil
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
	ticket, err := service.store.BeginSourceAccess(ctx, user, reference)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	existing, err := service.store.LookupCollectedRepository(ctx, user, reference)
	if err == nil {
		ticket, err = AuthorizeSource(ctx, service.store, service.fetcher, ticket)
		if err != nil {
			return state.CollectedRepository{}, err
		}
		entry, err := service.verifiedRepository(ctx, existing)
		if err != nil {
			return state.CollectedRepository{}, err
		}
		if err := service.store.CheckSourceAccess(service.store.WithSourceAccess(ctx, ticket), ticket); err != nil {
			return state.CollectedRepository{}, err
		}
		return entry, nil
	}
	if !errors.Is(err, state.ErrRepositoryNotFound) {
		return state.CollectedRepository{}, err
	}
	if err := service.store.CheckRepositoryCapacity(ctx, user); err != nil {
		return state.CollectedRepository{}, err
	}
	snapshot, ticket, err := FetchSource(ctx, service.store, service.fetcher, ticket)
	if err != nil {
		return state.CollectedRepository{}, err
	}
	if snapshot == nil || len(snapshot.Skills) == 0 {
		return state.CollectedRepository{}, repository.ErrArchiveInvalid
	}
	entry, err := service.store.WithMaintenanceWriter(ctx, func(ctx context.Context) (state.CollectedRepository, error) {
		if err := service.cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
			return state.CollectedRepository{}, err
		}
		return service.store.CollectRepository(service.store.WithSourceAccess(ctx, ticket), user, reference, snapshot.Hash, snapshot.Skills)
	})
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
