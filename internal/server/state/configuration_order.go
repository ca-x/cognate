package state

import (
	"cmp"
	"context"
	"math"
	"slices"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/configurationasset"
	"github.com/ca-x/cognate/internal/server/ent/configurationorder"
	"github.com/ca-x/cognate/internal/server/ent/configurationorderentry"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

type ConfigurationOrderSnapshot struct {
	AgentID  string
	Kind     ConfigurationKind
	Revision int64
	Assets   []ConfigurationAsset
}

type ConfigurationOrderRequest struct {
	AgentID          string
	Kind             ConfigurationKind
	ExpectedRevision int64
	IDs              []string
}

func (store *Store) ConfigurationAssetOrder(ctx context.Context, owner, agent string, kind ConfigurationKind) (ConfigurationOrderSnapshot, error) {
	if !configurationOrderPartition(agent, kind) {
		return ConfigurationOrderSnapshot{}, ErrConfigurationInvalid
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return ConfigurationOrderSnapshot{}, configurationFailure(err)
	}
	defer tx.Rollback()
	_, result, err := configurationOrderData(ctx, tx.Client(), owner, agent, kind, "")
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return ConfigurationOrderSnapshot{}, configurationFailure(err)
	}
	return result, nil
}

func (store *Store) ReplaceConfigurationAssetOrder(ctx context.Context, owner string, request ConfigurationOrderRequest) (ConfigurationOrderSnapshot, error) {
	if !configurationOrderPartition(request.AgentID, request.Kind) || request.ExpectedRevision < 0 || len(request.IDs) > MaxConfigurationAssets {
		return ConfigurationOrderSnapshot{}, ErrConfigurationInvalid
	}
	request.IDs = slices.Clone(request.IDs)
	seen := make(map[string]bool, len(request.IDs))
	for _, id := range request.IDs {
		if !validPackKey(id) || seen[id] {
			return ConfigurationOrderSnapshot{}, ErrConfigurationInvalid
		}
		seen[id] = true
	}
	var result ConfigurationOrderSnapshot
	err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		account, err := findUser(ctx, tx.Client(), owner)
		if err != nil {
			return err
		}
		partition, err := configurationOrderRow(ctx, tx.Client(), account.ID, request.AgentID, request.Kind)
		if err != nil {
			return err
		}
		if partition == nil {
			if request.ExpectedRevision != 0 {
				return ErrConfigurationConflict
			}
		} else {
			if partition.Revision != request.ExpectedRevision {
				return ErrConfigurationConflict
			}
			count, err := tx.ConfigurationOrder.Update().Where(configurationorder.IDEQ(partition.ID), configurationorder.RevisionEQ(request.ExpectedRevision)).SetRevision(request.ExpectedRevision).Save(ctx)
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrConfigurationConflict
			}
		}
		_, snapshot, err := configurationOrderData(ctx, tx.Client(), owner, request.AgentID, request.Kind, "")
		if err != nil {
			return err
		}
		if len(snapshot.Assets) != len(request.IDs) {
			return ErrConfigurationConflict
		}
		byID := make(map[string]ConfigurationAsset, len(snapshot.Assets))
		currentIDs := make([]string, 0, len(snapshot.Assets))
		for _, asset := range snapshot.Assets {
			byID[asset.ID] = asset
			currentIDs = append(currentIDs, asset.ID)
		}
		ordered := make([]ConfigurationAsset, 0, len(request.IDs))
		for _, id := range request.IDs {
			asset, exists := byID[id]
			if !exists {
				return ErrConfigurationConflict
			}
			ordered = append(ordered, asset)
		}
		if slices.Equal(currentIDs, request.IDs) {
			result = snapshot
			return nil
		}
		revision, err := configurationOrderSave(ctx, tx.Client(), account.ID, request.AgentID, request.Kind, partition, ordered)
		if err != nil {
			return err
		}
		result = ConfigurationOrderSnapshot{AgentID: request.AgentID, Kind: request.Kind, Revision: revision, Assets: ordered}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return ConfigurationOrderSnapshot{}, configurationFailure(err)
	}
	return result, nil
}

func configurationOrderPartition(agent string, kind ConfigurationKind) bool {
	switch agent {
	case "claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp":
	default:
		return false
	}
	switch kind {
	case ConfigurationKindProvider, ConfigurationKindMCP, ConfigurationKindPrompt, ConfigurationKindAgentSettings, ConfigurationKindWorkspace:
		return true
	default:
		return false
	}
}

func configurationOrderRow(ctx context.Context, client *ent.Client, ownerID int, agent string, kind ConfigurationKind) (*ent.ConfigurationOrder, error) {
	entry, err := client.ConfigurationOrder.Query().Where(configurationorder.UserIDEQ(ownerID), configurationorder.AgentIDEQ(agent), configurationorder.KindEQ(configurationorder.Kind(kind))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return entry, err
}

func configurationOrderAssets(ctx context.Context, client *ent.Client, owner string) ([]*ent.ConfigurationAsset, error) {
	fields := append(slices.Clone(configurationMetadataFields), configurationasset.FieldID)
	entries, err := client.ConfigurationAsset.Query().Where(configurationasset.HasUserWith(user.UserKeyEQ(owner))).Select(fields...).Order(ent.Desc(configurationasset.FieldCreatedAt), ent.Asc(configurationasset.FieldPublicID)).Limit(MaxConfigurationAssets + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) > MaxConfigurationAssets {
		return nil, ErrConfigurationLimit
	}
	return entries, nil
}

func configurationOrderData(ctx context.Context, client *ent.Client, owner, agent string, kind ConfigurationKind, exclude string) (*ent.ConfigurationOrder, ConfigurationOrderSnapshot, error) {
	account, err := findUser(ctx, client, owner)
	if err != nil {
		return nil, ConfigurationOrderSnapshot{}, err
	}
	partition, err := configurationOrderRow(ctx, client, account.ID, agent, kind)
	if err != nil {
		return nil, ConfigurationOrderSnapshot{}, err
	}
	assets, err := configurationOrderAssets(ctx, client, owner)
	if err != nil {
		return nil, ConfigurationOrderSnapshot{}, err
	}
	result := ConfigurationOrderSnapshot{AgentID: agent, Kind: kind, Assets: make([]ConfigurationAsset, 0)}
	byID := make(map[int]ConfigurationAsset)
	for _, asset := range assets {
		if asset.AgentID == agent && ConfigurationKind(asset.Kind) == kind && asset.PublicID != exclude {
			metadata := configurationMetadata(asset)
			result.Assets = append(result.Assets, metadata)
			byID[asset.ID] = metadata
		}
	}
	if partition == nil {
		return nil, result, nil
	}
	entries, err := client.ConfigurationOrderEntry.Query().Where(configurationorderentry.OrderIDEQ(partition.ID)).Order(ent.Asc(configurationorderentry.FieldOrdinal)).Limit(MaxConfigurationAssets + 1).All(ctx)
	if err != nil {
		return nil, ConfigurationOrderSnapshot{}, err
	}
	if len(entries) != len(byID) {
		return nil, ConfigurationOrderSnapshot{}, ErrConfigurationUnavailable
	}
	result.Revision = partition.Revision
	result.Assets = make([]ConfigurationAsset, 0, len(entries))
	for ordinal, entry := range entries {
		metadata, exists := byID[entry.AssetID]
		if !exists || entry.Ordinal != ordinal {
			return nil, ConfigurationOrderSnapshot{}, ErrConfigurationUnavailable
		}
		delete(byID, entry.AssetID)
		result.Assets = append(result.Assets, metadata)
	}
	return partition, result, nil
}

func configurationOrderSave(ctx context.Context, client *ent.Client, ownerID int, agent string, kind ConfigurationKind, partition *ent.ConfigurationOrder, assets []ConfigurationAsset) (int64, error) {
	var err error
	if partition == nil {
		partition, err = client.ConfigurationOrder.Create().SetUserID(ownerID).SetAgentID(agent).SetKind(configurationorder.Kind(kind)).SetRevision(1).Save(ctx)
		if err != nil {
			return 0, err
		}
	} else {
		if partition.Revision == math.MaxInt64 {
			return 0, ErrConfigurationLimit
		}
		count, err := client.ConfigurationOrder.Update().Where(configurationorder.IDEQ(partition.ID), configurationorder.RevisionEQ(partition.Revision)).AddRevision(1).Save(ctx)
		if err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, ErrConfigurationConflict
		}
		partition.Revision++
	}
	if _, err := client.ConfigurationOrderEntry.Delete().Where(configurationorderentry.OrderIDEQ(partition.ID)).Exec(ctx); err != nil {
		return 0, err
	}
	for ordinal, metadata := range assets {
		asset, err := client.ConfigurationAsset.Query().Where(configurationasset.PublicIDEQ(metadata.ID), configurationasset.UserIDEQ(ownerID), configurationasset.AgentIDEQ(agent), configurationasset.KindEQ(configurationasset.Kind(kind))).Select(configurationasset.FieldID).Only(ctx)
		if err != nil {
			return 0, err
		}
		if _, err := client.ConfigurationOrderEntry.Create().SetOrderID(partition.ID).SetAssetID(asset.ID).SetOrdinal(ordinal).Save(ctx); err != nil {
			return 0, err
		}
	}
	return partition.Revision, nil
}

func configurationOrderAppend(ctx context.Context, client *ent.Client, owner *ent.User, asset *ent.ConfigurationAsset) error {
	kind := ConfigurationKind(asset.Kind)
	if !configurationOrderPartition(asset.AgentID, kind) {
		return nil
	}
	partition, snapshot, err := configurationOrderData(ctx, client, owner.UserKey, asset.AgentID, kind, asset.PublicID)
	if err != nil {
		return err
	}
	snapshot.Assets = append(snapshot.Assets, configurationMetadata(asset))
	_, err = configurationOrderSave(ctx, client, owner.ID, asset.AgentID, kind, partition, snapshot.Assets)
	return err
}

func configurationOrderRemove(ctx context.Context, client *ent.Client, owner string, asset *ent.ConfigurationAsset) error {
	kind := ConfigurationKind(asset.Kind)
	if !configurationOrderPartition(asset.AgentID, kind) {
		return nil
	}
	partition, snapshot, err := configurationOrderData(ctx, client, owner, asset.AgentID, kind, "")
	if err != nil {
		return err
	}
	index := slices.IndexFunc(snapshot.Assets, func(metadata ConfigurationAsset) bool { return metadata.ID == asset.PublicID })
	if index < 0 {
		return ErrConfigurationUnavailable
	}
	account, err := findUser(ctx, client, owner)
	if err != nil {
		return err
	}
	_, err = configurationOrderSave(ctx, client, account.ID, asset.AgentID, kind, partition, slices.Delete(snapshot.Assets, index, index+1))
	return err
}

func configurationOrderedList(ctx context.Context, client *ent.Client, owner string) ([]ConfigurationAsset, error) {
	assets, err := configurationOrderAssets(ctx, client, owner)
	if err != nil {
		return nil, err
	}
	result := make([]ConfigurationAsset, 0, len(assets))
	groups := make(map[string]bool)
	for _, asset := range assets {
		kind := ConfigurationKind(asset.Kind)
		group := asset.AgentID + "/" + string(kind)
		if !configurationOrderPartition(asset.AgentID, kind) {
			result = append(result, configurationMetadata(asset))
			continue
		}
		if groups[group] {
			continue
		}
		groups[group] = true
		_, snapshot, err := configurationOrderData(ctx, client, owner, asset.AgentID, kind, "")
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot.Assets...)
	}
	slices.SortStableFunc(result, func(first, second ConfigurationAsset) int {
		if comparison := cmp.Compare(first.AgentID, second.AgentID); comparison != 0 {
			return comparison
		}
		return cmp.Compare(string(first.Kind), string(second.Kind))
	})
	return result, nil
}
