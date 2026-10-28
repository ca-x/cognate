package library

import (
	"context"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) RefreshAutomatic(ctx context.Context, ticket state.SourceRefreshTicket) (state.RepositoryRefreshResult, error) {
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	defer done()
	if deadline := ticket.Deadline(); !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	if err := service.reserveSlot(); err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	defer func() { <-service.slots }()
	if err := service.store.CheckSourceRefresh(ctx, ticket); err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	base := ticket.Snapshot()
	snapshot, err := service.fetcher.Fetch(ctx, repository.Reference{NameWithOwner: base.Repository.NameWithOwner, Branch: base.Repository.Branch})
	if err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	if snapshot == nil || len(snapshot.Skills) == 0 {
		return state.RepositoryRefreshResult{}, repository.ErrArchiveInvalid
	}
	if err := service.store.CheckSourceRefresh(ctx, ticket); err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	if base.Published && snapshot.Hash != base.Version.ArchiveHash {
		return state.RepositoryRefreshResult{}, state.ErrRepositoryRefreshPublished
	}
	if err := service.cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
		return state.RepositoryRefreshResult{}, err
	}
	return service.store.ApplySourceRefresh(ctx, ticket, snapshot.Hash, snapshot.Skills)
}
