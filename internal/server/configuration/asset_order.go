package configuration

import (
	"context"
	"math"
	"slices"

	"github.com/ca-x/cognate/internal/server/state"
)

type assetOrderStore interface {
	ConfigurationAssetOrder(context.Context, string, string, state.ConfigurationKind) (state.ConfigurationOrderSnapshot, error)
	ReplaceConfigurationAssetOrder(context.Context, string, state.ConfigurationOrderRequest) (state.ConfigurationOrderSnapshot, error)
}

func (service *Service) GetAssetOrder(ctx context.Context, owner, agent string, kind state.ConfigurationKind) (state.ConfigurationOrderSnapshot, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationOrderSnapshot{}, err
	}
	store, ok := service.store.(assetOrderStore)
	if !ok {
		return state.ConfigurationOrderSnapshot{}, ErrUnavailable
	}
	snapshot, err := store.ConfigurationAssetOrder(ctx, owner, agent, kind)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationOrderSnapshot{}, err
	}
	return checkedAssetOrder(snapshot, agent, kind)
}

func (service *Service) ReplaceAssetOrder(ctx context.Context, owner string, request state.ConfigurationOrderRequest) (state.ConfigurationOrderSnapshot, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationOrderSnapshot{}, err
	}
	store, ok := service.store.(assetOrderStore)
	if !ok {
		return state.ConfigurationOrderSnapshot{}, ErrUnavailable
	}
	request.IDs = slices.Clone(request.IDs)
	snapshot, err := store.ReplaceConfigurationAssetOrder(ctx, owner, request)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationOrderSnapshot{}, err
	}
	snapshot, err = checkedAssetOrder(snapshot, request.AgentID, request.Kind)
	if err != nil {
		return state.ConfigurationOrderSnapshot{}, err
	}
	if snapshot.Revision != request.ExpectedRevision && (request.ExpectedRevision == math.MaxInt64 || snapshot.Revision != request.ExpectedRevision+1) {
		return state.ConfigurationOrderSnapshot{}, ErrUnavailable
	}
	if len(snapshot.Assets) != len(request.IDs) {
		return state.ConfigurationOrderSnapshot{}, ErrUnavailable
	}
	for index, asset := range snapshot.Assets {
		if asset.ID != request.IDs[index] {
			return state.ConfigurationOrderSnapshot{}, ErrUnavailable
		}
	}
	return snapshot, nil
}

func checkedAssetOrder(snapshot state.ConfigurationOrderSnapshot, agent string, kind state.ConfigurationKind) (state.ConfigurationOrderSnapshot, error) {
	if snapshot.AgentID != agent || snapshot.Kind != kind || snapshot.Revision < 0 || len(snapshot.Assets) > state.MaxConfigurationAssets {
		return state.ConfigurationOrderSnapshot{}, ErrUnavailable
	}
	seen := make(map[string]bool, len(snapshot.Assets))
	for _, asset := range snapshot.Assets {
		if asset.AgentID != agent || asset.Kind != kind || validateMetadata(asset) != nil || seen[asset.ID] {
			return state.ConfigurationOrderSnapshot{}, ErrUnavailable
		}
		seen[asset.ID] = true
	}
	snapshot.Assets = slices.Clone(snapshot.Assets)
	return snapshot, nil
}
