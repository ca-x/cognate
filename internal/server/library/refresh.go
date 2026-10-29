package library

import (
	"context"

	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) RefreshState(ctx context.Context, owner, id string) (state.RepositoryRefreshSnapshot, error) {
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return state.RepositoryRefreshSnapshot{}, err
	}
	defer done()
	return service.store.ReadRepositoryRefresh(ctx, owner, id)
}

func (service *Service) Refresh(ctx context.Context, owner, id string, expected state.RepositoryVersion) (state.RepositoryRefreshResult, error) {
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	defer done()
	if err := service.reserveSlot(); err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	defer func() { <-service.slots }()
	ticket, err := service.store.BeginRepositorySourceRefresh(ctx, owner, id)
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	base := ticket.Snapshot()
	if base.Version != expected {
		return state.RepositoryRefreshResult{}, state.ErrRepositoryRefreshConflict
	}
	snapshot, access, err := FetchSource(ctx, service.store, service.fetcher, ticket.Access())
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	if base.Published && snapshot.Hash != base.Version.ArchiveHash {
		return state.RepositoryRefreshResult{}, state.ErrRepositoryRefreshPublished
	}
	return service.store.WithMaintenanceWriter(ctx, func(ctx context.Context) (state.RepositoryRefreshResult, error) {
		if err := service.cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
			return state.RepositoryRefreshResult{}, err
		}
		return service.store.ApplySourceRefresh(service.store.WithSourceAccess(ctx, access), ticket, snapshot.Hash, snapshot.Skills)
	})
}
