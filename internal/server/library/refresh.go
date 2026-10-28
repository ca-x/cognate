package library

import (
	"context"

	"github.com/ca-x/cognate/internal/repository"
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
	base, err := service.store.ReadRepositoryRefresh(ctx, owner, id)
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	if base.Version != expected {
		return state.RepositoryRefreshResult{}, state.ErrRepositoryRefreshConflict
	}
	snapshot, err := service.fetcher.Fetch(ctx, repository.Reference{NameWithOwner: base.Repository.NameWithOwner, Branch: base.Repository.Branch})
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	if snapshot == nil || len(snapshot.Skills) == 0 {
		return state.RepositoryRefreshResult{}, repository.ErrArchiveInvalid
	}
	if base.Published && snapshot.Hash != base.Version.ArchiveHash {
		return state.RepositoryRefreshResult{}, state.ErrRepositoryRefreshPublished
	}
	if err := service.cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	return service.store.RefreshCollectedRepository(ctx, owner, id, expected, snapshot.Hash, snapshot.Skills)
}
