package desktop

import (
	"context"
	"errors"
	"math"
	"slices"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
)

var errConfigurationOrderUnknown = errors.New("排序结果 Unknown；请显式重新读取并重新审阅，禁止重放")

type configurationOrderPartition struct {
	AgentID, Kind string
}

func (partition configurationOrderPartition) label() string {
	return partition.AgentID + " · " + partition.Kind
}

type configurationOrderSnapshot struct {
	configurationOrderPartition
	Revision int64
	Assets   []configurationAsset
}

func configurationOrderKind(kind string) configurationv1.AssetKind {
	switch kind {
	case "provider":
		return configurationv1.AssetKind_ASSET_KIND_PROVIDER
	case "mcp":
		return configurationv1.AssetKind_ASSET_KIND_MCP
	case "prompt":
		return configurationv1.AssetKind_ASSET_KIND_PROMPT
	case "agent-settings":
		return configurationv1.AssetKind_ASSET_KIND_AGENT_SETTINGS
	case "workspace":
		return configurationv1.AssetKind_ASSET_KIND_WORKSPACE
	default:
		return configurationv1.AssetKind_ASSET_KIND_UNSPECIFIED
	}
}

func configurationOrderView(view *configurationv1.AssetOrderSnapshot, partition configurationOrderPartition) (configurationOrderSnapshot, error) {
	if view == nil || !configurationKnown(view) || view.AgentId != partition.AgentID || view.Kind != configurationOrderKind(partition.Kind) || view.Revision < 0 || len(view.Assets) > 250 {
		return configurationOrderSnapshot{}, errConfigurationInvalid
	}
	snapshot := configurationOrderSnapshot{configurationOrderPartition: partition, Revision: view.Revision, Assets: make([]configurationAsset, 0, len(view.Assets))}
	seen := make(map[string]bool, len(view.Assets))
	for _, entry := range view.Assets {
		asset, err := configurationMetadata(entry)
		if err != nil || asset.AgentID != partition.AgentID || asset.Kind != partition.Kind || seen[asset.ID] {
			return configurationOrderSnapshot{}, errConfigurationInvalid
		}
		seen[asset.ID] = true
		snapshot.Assets = append(snapshot.Assets, asset)
	}
	return snapshot, nil
}

func configurationOrderIDs(assets []configurationAsset) []string {
	ids := make([]string, len(assets))
	for index, asset := range assets {
		ids[index] = asset.ID
	}
	return ids
}

func (service *Service) ConfigurationOrder(ctx context.Context, identity configurationIdentity, partition configurationOrderPartition) (configurationOrderSnapshot, error) {
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return configurationOrderSnapshot{}, err
	}
	kind := configurationOrderKind(partition.Kind)
	if kind == configurationv1.AssetKind_ASSET_KIND_UNSPECIFIED || !configurationText(partition.AgentID, 64) {
		return configurationOrderSnapshot{}, errConfigurationInvalid
	}
	request := connect.NewRequest(&configurationv1.GetAssetOrderRequest{AgentId: partition.AgentID, Kind: kind})
	if err := identity.authorize(request.Header()); err != nil {
		return configurationOrderSnapshot{}, err
	}
	response, err := client.GetAssetOrder(ctx, request)
	if err := configurationFailure(ctx, err); err != nil {
		return configurationOrderSnapshot{}, err
	}
	if !configurationKnown(response.Msg) {
		return configurationOrderSnapshot{}, errConfigurationInvalid
	}
	return configurationOrderView(response.Msg.Snapshot, partition)
}

func (service *Service) ReplaceConfigurationOrder(ctx context.Context, identity configurationIdentity, before configurationOrderSnapshot, ids []string) (configurationOrderSnapshot, error) {
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return configurationOrderSnapshot{}, err
	}
	kind := configurationOrderKind(before.Kind)
	if before.Revision < 0 || len(before.Assets) > 250 || kind == configurationv1.AssetKind_ASSET_KIND_UNSPECIFIED || len(ids) != len(before.Assets) {
		return configurationOrderSnapshot{}, errConfigurationInvalid
	}
	expected := configurationOrderIDs(before.Assets)
	permutation := slices.Clone(ids)
	slices.Sort(expected)
	slices.Sort(permutation)
	if !slices.Equal(expected, permutation) {
		return configurationOrderSnapshot{}, errConfigurationInvalid
	}
	for index, id := range permutation {
		if !configurationID(id) || index > 0 && id == permutation[index-1] {
			return configurationOrderSnapshot{}, errConfigurationInvalid
		}
	}
	revision := before.Revision
	if !slices.Equal(configurationOrderIDs(before.Assets), ids) {
		if revision == math.MaxInt64 {
			return configurationOrderSnapshot{}, errConfigurationLimit
		}
		revision++
	}
	request := connect.NewRequest(&configurationv1.ReplaceAssetOrderRequest{AgentId: before.AgentID, Kind: kind, ExpectedRevision: before.Revision, AssetIds: slices.Clone(ids)})
	if err := identity.authorize(request.Header()); err != nil {
		return configurationOrderSnapshot{}, err
	}
	response, err := client.ReplaceAssetOrder(ctx, request)
	if err != nil {
		switch connect.CodeOf(err) {
		case connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeAborted, connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeResourceExhausted:
			return configurationOrderSnapshot{}, configurationFailure(context.Background(), err)
		default:
			return configurationOrderSnapshot{}, errConfigurationOrderUnknown
		}
	}
	if ctx.Err() != nil || !configurationKnown(response.Msg) {
		return configurationOrderSnapshot{}, errConfigurationOrderUnknown
	}
	after, err := configurationOrderView(response.Msg.Snapshot, before.configurationOrderPartition)
	if err != nil || after.Revision != revision || !slices.Equal(configurationOrderIDs(after.Assets), ids) {
		return configurationOrderSnapshot{}, errConfigurationOrderUnknown
	}
	return after, nil
}
