package httpapi

import (
	"context"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func (handler *configurationHandler) GetAssetOrder(ctx context.Context, request *connect.Request[configurationv1.GetAssetOrderRequest]) (*connect.Response[configurationv1.GetAssetOrderResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.GetAssetOrderResponse, error) {
		kind, err := configurationOrderKind(request.Msg.Kind)
		if err != nil {
			return nil, err
		}
		snapshot, err := handler.service.GetAssetOrder(ctx, owner, request.Msg.AgentId, kind)
		if err != nil {
			return nil, err
		}
		view, err := configurationOrderView(snapshot, request.Msg.Kind)
		if err != nil {
			return nil, err
		}
		return &configurationv1.GetAssetOrderResponse{Snapshot: view}, nil
	})
}

func (handler *configurationHandler) ReplaceAssetOrder(ctx context.Context, request *connect.Request[configurationv1.ReplaceAssetOrderRequest]) (*connect.Response[configurationv1.ReplaceAssetOrderResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.ReplaceAssetOrderResponse, error) {
		kind, err := configurationOrderKind(request.Msg.Kind)
		if err != nil {
			return nil, err
		}
		snapshot, err := handler.service.ReplaceAssetOrder(ctx, owner, state.ConfigurationOrderRequest{AgentID: request.Msg.AgentId, Kind: kind, ExpectedRevision: request.Msg.ExpectedRevision, IDs: request.Msg.AssetIds})
		if err != nil {
			return nil, err
		}
		view, err := configurationOrderView(snapshot, request.Msg.Kind)
		if err != nil {
			return nil, err
		}
		return &configurationv1.ReplaceAssetOrderResponse{Snapshot: view}, nil
	})
}

func configurationOrderKind(kind configurationv1.AssetKind) (state.ConfigurationKind, error) {
	switch kind {
	case configurationv1.AssetKind_ASSET_KIND_PROVIDER:
		return state.ConfigurationKindProvider, nil
	case configurationv1.AssetKind_ASSET_KIND_MCP:
		return state.ConfigurationKindMCP, nil
	case configurationv1.AssetKind_ASSET_KIND_PROMPT:
		return state.ConfigurationKindPrompt, nil
	case configurationv1.AssetKind_ASSET_KIND_AGENT_SETTINGS:
		return state.ConfigurationKindAgentSettings, nil
	case configurationv1.AssetKind_ASSET_KIND_WORKSPACE:
		return state.ConfigurationKindWorkspace, nil
	default:
		return "", configuration.ErrInvalid
	}
}

func configurationOrderView(snapshot state.ConfigurationOrderSnapshot, kind configurationv1.AssetKind) (*configurationv1.AssetOrderSnapshot, error) {
	view := &configurationv1.AssetOrderSnapshot{AgentId: snapshot.AgentID, Kind: kind, Revision: snapshot.Revision, Assets: make([]*configurationv1.Asset, 0, len(snapshot.Assets))}
	for _, entry := range snapshot.Assets {
		asset, err := configurationAsset(entry)
		if err != nil {
			return nil, err
		}
		view.Assets = append(view.Assets, asset)
	}
	return view, nil
}
