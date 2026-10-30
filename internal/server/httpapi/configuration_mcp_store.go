package httpapi

import (
	"context"
	"sync/atomic"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type mcpRequestStore struct {
	store     *state.Store
	owner     string
	authorize func(context.Context) error
	failure   atomic.Uint32
}

var _ configuration.Store = (*mcpRequestStore)(nil)

func (store *mcpRequestStore) check(ctx context.Context, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner != store.owner {
		store.failure.Store(1)
		return configuration.ErrUnavailable
	}
	if err := store.authorize(ctx); err != nil {
		return err
	}
	return nil
}

func (store *mcpRequestStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	if err := store.check(ctx, owner); err != nil {
		return state.ConfigurationAsset{}, err
	}
	return store.store.CreateConfigurationAsset(ctx, owner, draft)
}

func (store *mcpRequestStore) ListConfigurationAssets(ctx context.Context, owner string) ([]state.ConfigurationAsset, error) {
	if err := store.check(ctx, owner); err != nil {
		return nil, err
	}
	return store.store.ListConfigurationAssets(ctx, owner)
}

func (store *mcpRequestStore) ConfigurationAsset(ctx context.Context, owner, id string) (state.ConfigurationAsset, error) {
	if err := store.check(ctx, owner); err != nil {
		return state.ConfigurationAsset{}, err
	}
	return store.store.ConfigurationAsset(ctx, owner, id)
}

func (store *mcpRequestStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	if err := store.check(ctx, owner); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	return store.store.ReadConfigurationAssetVersion(ctx, owner, id)
}

func (store *mcpRequestStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	if err := store.check(ctx, owner); err != nil {
		return state.ConfigurationAsset{}, err
	}
	return store.store.UpdateConfigurationAsset(ctx, owner, id, revision, draft)
}

func (store *mcpRequestStore) DeleteConfigurationAsset(ctx context.Context, owner, id string, revision int64) error {
	if err := store.check(ctx, owner); err != nil {
		return err
	}
	return store.store.DeleteConfigurationAsset(ctx, owner, id, revision)
}
