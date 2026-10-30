package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/configurationasset"
	"github.com/ca-x/cognate/internal/server/ent/pack"
	"github.com/ca-x/cognate/internal/server/ent/packconfigurationattachment"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

const MaxPackConfigurationAttachments = 16

var (
	ErrConfigurationInUse           = errors.New("configuration asset in use")
	ErrPackConfigurationUnsupported = errors.New("pack configuration target unsupported")
)

type PackConfigurationReference struct {
	AssetID       string
	AssetRevision int64
	TargetID      string
}

type PackConfigurationEntryState string

type PackConfigurationNativeAvailability string

const PackConfigurationNativeUnavailable PackConfigurationNativeAvailability = "unavailable"

const (
	PackConfigurationCurrent     PackConfigurationEntryState = "current"
	PackConfigurationStaleAsset  PackConfigurationEntryState = "stale-asset"
	PackConfigurationUnsupported PackConfigurationEntryState = "unsupported"
)

type PackConfigurationEntry struct {
	ID string
	PackConfigurationReference
	AgentID            string
	Kind               ConfigurationKind
	SchemaVersion      int
	Ordinal            int
	State              PackConfigurationEntryState
	NativeAvailability PackConfigurationNativeAvailability
}

type PackConfigurationSnapshot struct {
	PackID                string
	PackRevision          int64
	ConfigurationRevision int64
	Entries               []PackConfigurationEntry
	Digest                [32]byte
}

func (store *Store) PackConfigurationAttachments(ctx context.Context, owner, packID string) (PackConfigurationSnapshot, error) {
	if _, present := ctx.Value(mcpContextKey{}).(mcpAuthorization); present {
		return PackConfigurationSnapshot{}, ErrMCPForbidden
	}
	if !validPackKey(packID) {
		return PackConfigurationSnapshot{}, ErrPackNotFound
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return PackConfigurationSnapshot{}, packConfigurationFailure(err)
	}
	defer tx.Rollback()
	entry, err := tx.Pack.Query().Where(pack.PublicIDEQ(packID), pack.HasUserWith(user.UserKeyEQ(owner))).Only(ctx)
	if err != nil {
		return PackConfigurationSnapshot{}, packConfigurationFailure(err)
	}
	result, err := packConfigurationSnapshotTx(ctx, tx, entry)
	if err != nil {
		return PackConfigurationSnapshot{}, packConfigurationFailure(err)
	}
	if err := tx.Commit(); err != nil {
		return PackConfigurationSnapshot{}, packConfigurationFailure(err)
	}
	return result, nil
}

func (store *Store) ReplacePackConfigurationAttachments(ctx context.Context, owner, packID string, expectedPackRevision, expectedConfigurationRevision int64, refs []PackConfigurationReference) (PackConfigurationSnapshot, error) {
	if _, present := ctx.Value(mcpContextKey{}).(mcpAuthorization); present {
		return PackConfigurationSnapshot{}, ErrMCPForbidden
	}
	if !validPackKey(packID) {
		return PackConfigurationSnapshot{}, ErrPackNotFound
	}
	if expectedPackRevision < 1 || expectedPackRevision == math.MaxInt64 || expectedConfigurationRevision < 1 || expectedConfigurationRevision == math.MaxInt64 {
		return PackConfigurationSnapshot{}, ErrPackConflict
	}
	if len(refs) > MaxPackConfigurationAttachments {
		return PackConfigurationSnapshot{}, ErrPackLimit
	}
	type referenceKey struct {
		assetID  string
		targetID string
	}
	seen := make(map[referenceKey]bool, len(refs))
	for _, ref := range refs {
		key := referenceKey{ref.AssetID, ref.TargetID}
		if ref.AssetRevision < 1 || !validPackKey(ref.AssetID) || seen[key] {
			return PackConfigurationSnapshot{}, ErrPackInvalid
		}
		seen[key] = true
	}
	var result PackConfigurationSnapshot
	err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		changed, err := tx.Pack.Update().Where(pack.PublicIDEQ(packID), pack.HasUserWith(user.UserKeyEQ(owner)), pack.RevisionEQ(expectedPackRevision), pack.ConfigurationRevisionEQ(expectedConfigurationRevision)).
			AddRevision(1).AddConfigurationRevision(1).SetUpdatedAt(store.state.now()).Save(ctx)
		if err != nil {
			return err
		}
		entry, err := tx.Pack.Query().Where(pack.PublicIDEQ(packID), pack.HasUserWith(user.UserKeyEQ(owner))).Only(ctx)
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrPackConflict
		}
		previous, err := tx.PackConfigurationAttachment.Query().Where(packconfigurationattachment.PackIDEQ(entry.ID)).All(ctx)
		if err != nil {
			return err
		}
		type attachmentKey struct {
			assetID  int
			targetID string
		}
		identities := make(map[attachmentKey]string, len(previous))
		for _, row := range previous {
			identities[attachmentKey{row.ConfigurationAssetID, row.TargetID}] = row.PublicID
		}
		builders := make([]*ent.PackConfigurationAttachmentCreate, 0, len(refs))
		for ordinal, ref := range refs {
			asset, err := tx.ConfigurationAsset.Query().Where(configurationasset.PublicIDEQ(ref.AssetID), configurationasset.UserIDEQ(entry.UserID)).
				Select(configurationasset.FieldID, configurationasset.FieldAgentID, configurationasset.FieldKind, configurationasset.FieldSchemaVersion, configurationasset.FieldRevision).Only(ctx)
			if ent.IsNotFound(err) {
				return ErrConfigurationNotFound
			}
			if err != nil {
				return err
			}
			if asset.Revision != ref.AssetRevision {
				return ErrConfigurationConflict
			}
			if !packConfigurationTarget(asset.AgentID, ConfigurationKind(asset.Kind), asset.SchemaVersion, ref.TargetID) {
				return ErrPackConfigurationUnsupported
			}
			id := identities[attachmentKey{asset.ID, ref.TargetID}]
			if id == "" {
				id = rand.Text()
			}
			builders = append(builders, tx.PackConfigurationAttachment.Create().SetPublicID(id).SetPackID(entry.ID).SetConfigurationAssetID(asset.ID).SetAssetRevision(ref.AssetRevision).SetTargetID(ref.TargetID).SetOrdinal(ordinal))
		}
		if _, err := tx.PackConfigurationAttachment.Delete().Where(packconfigurationattachment.PackIDEQ(entry.ID)).Exec(ctx); err != nil {
			return err
		}
		if len(builders) != 0 {
			if err := tx.PackConfigurationAttachment.CreateBulk(builders...).Exec(ctx); err != nil {
				return err
			}
		}
		result, err = packConfigurationSnapshotTx(ctx, tx, entry)
		return err
	})
	if err != nil {
		return PackConfigurationSnapshot{}, packConfigurationFailure(err)
	}
	return result, nil
}

func packConfigurationFailure(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrPackNotFound, ErrPackConflict, ErrPackInvalid, ErrPackLimit, ErrConfigurationNotFound, ErrConfigurationConflict, ErrPackConfigurationUnsupported, ErrMCPForbidden} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ent.IsNotFound(err) {
		return ErrPackNotFound
	}
	return ErrConfigurationUnavailable
}

func packConfigurationTarget(agentID string, kind ConfigurationKind, schemaVersion int, targetID string) bool {
	if kind != ConfigurationKindProvider || schemaVersion != 1 || targetID != agentID+".provider.user" {
		return false
	}
	switch agentID {
	case "claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp":
		return true
	default:
		return false
	}
}

func packConfigurationSnapshotTx(ctx context.Context, tx *ent.Tx, entry *ent.Pack) (PackConfigurationSnapshot, error) {
	if entry.Revision < 1 || entry.ConfigurationRevision < 1 {
		return PackConfigurationSnapshot{}, ErrConfigurationUnavailable
	}
	rows, err := tx.PackConfigurationAttachment.Query().Where(packconfigurationattachment.PackIDEQ(entry.ID)).
		Order(ent.Asc(packconfigurationattachment.FieldOrdinal)).Limit(MaxPackConfigurationAttachments + 1).
		WithConfigurationAsset(func(query *ent.ConfigurationAssetQuery) {
			query.Select(configurationasset.FieldID, configurationasset.FieldUserID, configurationasset.FieldPublicID, configurationasset.FieldAgentID, configurationasset.FieldKind, configurationasset.FieldSchemaVersion, configurationasset.FieldRevision)
		}).All(ctx)
	if err != nil {
		return PackConfigurationSnapshot{}, err
	}
	if len(rows) > MaxPackConfigurationAttachments {
		return PackConfigurationSnapshot{}, ErrConfigurationUnavailable
	}
	result := PackConfigurationSnapshot{PackID: entry.PublicID, PackRevision: entry.Revision, ConfigurationRevision: entry.ConfigurationRevision, Entries: make([]PackConfigurationEntry, 0, len(rows))}
	for ordinal, row := range rows {
		asset := row.Edges.ConfigurationAsset
		if asset == nil || asset.UserID != entry.UserID || row.Ordinal != ordinal || row.AssetRevision < 1 || !validPackKey(row.PublicID) || !validPackKey(asset.PublicID) || asset.Revision < 1 || asset.SchemaVersion < 1 || asset.SchemaVersion > 65535 || !utf8.ValidString(asset.AgentID) || !utf8.ValidString(string(asset.Kind)) || !utf8.ValidString(row.TargetID) {
			return PackConfigurationSnapshot{}, ErrConfigurationUnavailable
		}
		state := PackConfigurationCurrent
		if asset.Revision != row.AssetRevision {
			state = PackConfigurationStaleAsset
		} else if !packConfigurationTarget(asset.AgentID, ConfigurationKind(asset.Kind), asset.SchemaVersion, row.TargetID) {
			state = PackConfigurationUnsupported
		}
		result.Entries = append(result.Entries, PackConfigurationEntry{
			ID: row.PublicID, AssetID: asset.PublicID, AssetRevision: row.AssetRevision, TargetID: row.TargetID,
			AgentID: asset.AgentID, Kind: ConfigurationKind(asset.Kind), SchemaVersion: asset.SchemaVersion, Ordinal: ordinal, State: state, NativeAvailability: PackConfigurationNativeUnavailable,
		})
	}
	result.Digest = packConfigurationDigest(result)
	return result, nil
}

func packConfigurationDigest(snapshot PackConfigurationSnapshot) [32]byte {
	encoded := []byte("cognate/pack-configuration-snapshot/v1")
	appendString := func(value string) {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(value)))
		encoded = append(encoded, value...)
	}
	appendString(snapshot.PackID)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(snapshot.PackRevision))
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(snapshot.ConfigurationRevision))
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(len(snapshot.Entries)))
	for _, entry := range snapshot.Entries {
		appendString(entry.ID)
		appendString(entry.AssetID)
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(entry.AssetRevision))
		appendString(entry.AgentID)
		appendString(string(entry.Kind))
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(entry.SchemaVersion))
		appendString(entry.TargetID)
	}
	return sha256.Sum256(encoded)
}
