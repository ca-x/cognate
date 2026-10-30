package state

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/configurationasset"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

const (
	MaxConfigurationAssets       = 250
	MaxConfigurationPayloadBytes = 256 << 10
)

type ConfigurationKind string

const (
	ConfigurationKindProvider      ConfigurationKind = "provider"
	ConfigurationKindMCP           ConfigurationKind = "mcp"
	ConfigurationKindPrompt        ConfigurationKind = "prompt"
	ConfigurationKindAgentSettings ConfigurationKind = "agent-settings"
	ConfigurationKindWorkspace     ConfigurationKind = "workspace"
)

var (
	ErrConfigurationInvalid     = errors.New("invalid configuration asset")
	ErrConfigurationNotFound    = errors.New("configuration asset not found")
	ErrConfigurationConflict    = errors.New("configuration asset revision changed")
	ErrConfigurationLimit       = errors.New("configuration asset capacity exhausted")
	ErrConfigurationUnavailable = errors.New("configuration asset unavailable")
)

type ConfigurationPayload struct{ data []byte }

func NewConfigurationPayload(data []byte) ConfigurationPayload {
	return ConfigurationPayload{data: slices.Clone(data)}
}
func (payload ConfigurationPayload) Reveal() []byte { return slices.Clone(payload.data) }
func (payload ConfigurationPayload) Clone() ConfigurationPayload {
	return NewConfigurationPayload(payload.data)
}
func (ConfigurationPayload) String() string   { return "[redacted configuration payload]" }
func (ConfigurationPayload) GoString() string { return "[redacted configuration payload]" }
func (ConfigurationPayload) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted configuration payload]"`), nil
}
func (ConfigurationPayload) MarshalText() ([]byte, error) {
	return []byte("[redacted configuration payload]"), nil
}
func (ConfigurationPayload) LogValue() slog.Value {
	return slog.StringValue("[redacted configuration payload]")
}

type ConfigurationAsset struct {
	ID            string            `json:"id"`
	AgentID       string            `json:"agent_id"`
	Kind          ConfigurationKind `json:"kind"`
	Name          string            `json:"name"`
	SchemaVersion int               `json:"schema_version"`
	Revision      int64             `json:"revision"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type ConfigurationAssetDraft struct {
	AgentID       string
	Kind          ConfigurationKind
	Name          string
	SchemaVersion int
	Payload       ConfigurationPayload
}

const configurationKeyDomain = "cognate/configuration-assets/v1"

func newConfigurationAEAD(masterKey []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, masterKey, nil, configurationKeyDomain, 32)
	if err != nil {
		return nil, ErrConfigurationUnavailable
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrConfigurationUnavailable
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func configurationAAD(owner string, entry *ent.ConfigurationAsset) []byte {
	result := []byte(configurationKeyDomain + "/payload")
	for _, value := range []string{owner, strconv.Itoa(entry.UserID), entry.PublicID, entry.AgentID, string(entry.Kind), entry.Name, strconv.Itoa(entry.SchemaVersion), strconv.FormatInt(entry.Revision, 10)} {
		result = binary.BigEndian.AppendUint64(result, uint64(len(value)))
		result = append(result, value...)
	}
	return result
}

func normalizeConfigurationDraft(draft ConfigurationAssetDraft) (ConfigurationAssetDraft, error) {
	if len(draft.AgentID) < 1 || len(draft.AgentID) > 64 || draft.AgentID[0] < 'a' || draft.AgentID[0] > 'z' {
		return ConfigurationAssetDraft{}, ErrConfigurationInvalid
	}
	for _, letter := range draft.AgentID {
		if !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-') {
			return ConfigurationAssetDraft{}, ErrConfigurationInvalid
		}
	}
	switch draft.Kind {
	case ConfigurationKindProvider, ConfigurationKindMCP, ConfigurationKindPrompt, ConfigurationKindAgentSettings, ConfigurationKindWorkspace:
	default:
		return ConfigurationAssetDraft{}, ErrConfigurationInvalid
	}
	draft.Name = strings.TrimSpace(draft.Name)
	if draft.Name == "" || len(draft.Name) > 128 || !utf8.ValidString(draft.Name) || strings.ContainsFunc(draft.Name, unicode.IsControl) || draft.SchemaVersion < 1 || draft.SchemaVersion > 65535 || len(draft.Payload.data) < 1 || len(draft.Payload.data) > MaxConfigurationPayloadBytes {
		return ConfigurationAssetDraft{}, ErrConfigurationInvalid
	}
	draft.Payload = draft.Payload.Clone()
	return draft, nil
}

func configurationFailure(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrConfigurationInvalid, ErrConfigurationNotFound, ErrConfigurationConflict, ErrConfigurationLimit} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ent.IsNotFound(err) {
		return ErrConfigurationNotFound
	}
	return ErrConfigurationUnavailable
}

func configurationMetadata(entry *ent.ConfigurationAsset) ConfigurationAsset {
	return ConfigurationAsset{ID: entry.PublicID, AgentID: entry.AgentID, Kind: ConfigurationKind(entry.Kind), Name: entry.Name, SchemaVersion: entry.SchemaVersion, Revision: entry.Revision, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt}
}

var configurationMetadataFields = []string{configurationasset.FieldPublicID, configurationasset.FieldAgentID, configurationasset.FieldKind, configurationasset.FieldName, configurationasset.FieldSchemaVersion, configurationasset.FieldRevision, configurationasset.FieldCreatedAt, configurationasset.FieldUpdatedAt}

func loadConfigurationAsset(ctx context.Context, client *ent.Client, owner, id string, metadataOnly bool) (*ent.ConfigurationAsset, error) {
	if !validPackKey(id) {
		return nil, ErrConfigurationNotFound
	}
	query := client.ConfigurationAsset.Query().Where(configurationasset.PublicIDEQ(id), configurationasset.HasUserWith(user.UserKeyEQ(owner)))
	if metadataOnly {
		query.Select(configurationMetadataFields...)
	}
	entry, err := query.Only(ctx)
	return entry, configurationFailure(err)
}

func (store *Store) CreateConfigurationAsset(ctx context.Context, owner string, draft ConfigurationAssetDraft) (ConfigurationAsset, error) {
	draft, err := normalizeConfigurationDraft(draft)
	if err != nil {
		return ConfigurationAsset{}, err
	}
	defer clear(draft.Payload.data)
	var result ConfigurationAsset
	err = store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		ownerEntry, err := findUser(ctx, tx.Client(), owner)
		if err != nil {
			return err
		}
		result, err = store.createConfigurationAssetTx(ctx, tx, ownerEntry, draft)
		return err
	})
	if err != nil {
		return ConfigurationAsset{}, configurationFailure(err)
	}
	return result, nil
}

func (store *Store) createConfigurationAssetTx(ctx context.Context, tx *ent.Tx, owner *ent.User, draft ConfigurationAssetDraft) (ConfigurationAsset, error) {
	count, err := tx.ConfigurationAsset.Query().Where(configurationasset.UserIDEQ(owner.ID)).Count(ctx)
	if err != nil {
		return ConfigurationAsset{}, err
	}
	if count >= MaxConfigurationAssets {
		return ConfigurationAsset{}, ErrConfigurationLimit
	}
	now := store.state.now().UTC()
	entry := &ent.ConfigurationAsset{PublicID: rand.Text(), UserID: owner.ID, AgentID: draft.AgentID, Kind: configurationasset.Kind(draft.Kind), Name: draft.Name, SchemaVersion: draft.SchemaVersion, Revision: 1, CreatedAt: now, UpdatedAt: now}
	ciphertext := store.state.configurationAEAD.Seal(nil, nil, draft.Payload.data, configurationAAD(owner.UserKey, entry))
	saved, err := tx.ConfigurationAsset.Create().SetPublicID(entry.PublicID).SetUserID(entry.UserID).SetAgentID(entry.AgentID).SetKind(entry.Kind).SetName(entry.Name).SetSchemaVersion(entry.SchemaVersion).SetRevision(entry.Revision).SetCreatedAt(now).SetUpdatedAt(now).SetCiphertext(ciphertext).Save(ctx)
	if err != nil {
		return ConfigurationAsset{}, err
	}
	return configurationMetadata(saved), nil
}

func (store *Store) ListConfigurationAssets(ctx context.Context, owner string) ([]ConfigurationAsset, error) {
	entries, err := store.state.client.ConfigurationAsset.Query().Where(configurationasset.HasUserWith(user.UserKeyEQ(owner))).Select(configurationMetadataFields...).Order(ent.Desc(configurationasset.FieldCreatedAt), ent.Asc(configurationasset.FieldPublicID)).Limit(MaxConfigurationAssets).All(ctx)
	if err != nil {
		return nil, configurationFailure(err)
	}
	result := make([]ConfigurationAsset, 0, len(entries))
	for _, entry := range entries {
		result = append(result, configurationMetadata(entry))
	}
	return result, nil
}

func (store *Store) ConfigurationAsset(ctx context.Context, owner, id string) (ConfigurationAsset, error) {
	entry, err := loadConfigurationAsset(ctx, store.state.client, owner, id, true)
	if err != nil {
		return ConfigurationAsset{}, err
	}
	return configurationMetadata(entry), nil
}

func (store *Store) ReadConfigurationAsset(ctx context.Context, owner, id string) (ConfigurationPayload, error) {
	_, payload, err := store.ReadConfigurationAssetVersion(ctx, owner, id)
	return payload, err
}

func (store *Store) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (ConfigurationAsset, ConfigurationPayload, error) {
	entry, err := loadConfigurationAsset(ctx, store.state.client, owner, id, false)
	if err != nil {
		return ConfigurationAsset{}, ConfigurationPayload{}, err
	}
	if len(entry.Ciphertext) < store.state.configurationAEAD.Overhead()+1 || len(entry.Ciphertext) > MaxConfigurationPayloadBytes+store.state.configurationAEAD.Overhead() {
		return ConfigurationAsset{}, ConfigurationPayload{}, ErrConfigurationUnavailable
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, entry.Ciphertext, configurationAAD(owner, entry))
	if err != nil {
		return ConfigurationAsset{}, ConfigurationPayload{}, ErrConfigurationUnavailable
	}
	defer clear(plaintext)
	return configurationMetadata(entry), NewConfigurationPayload(plaintext), nil
}

func (store *Store) UpdateConfigurationAsset(ctx context.Context, owner, id string, expectedRevision int64, draft ConfigurationAssetDraft) (ConfigurationAsset, error) {
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 {
		return ConfigurationAsset{}, ErrConfigurationInvalid
	}
	draft, err := normalizeConfigurationDraft(draft)
	if err != nil {
		return ConfigurationAsset{}, err
	}
	defer clear(draft.Payload.data)
	var result ConfigurationAsset
	err = store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		entry, err := loadConfigurationAsset(ctx, tx.Client(), owner, id, false)
		if err != nil {
			return err
		}
		if entry.Revision != expectedRevision {
			return ErrConfigurationConflict
		}
		if entry.AgentID != draft.AgentID || string(entry.Kind) != string(draft.Kind) {
			return ErrConfigurationInvalid
		}
		entry.Name, entry.SchemaVersion, entry.Revision, entry.UpdatedAt = draft.Name, draft.SchemaVersion, expectedRevision+1, store.state.now().UTC()
		ciphertext := store.state.configurationAEAD.Seal(nil, nil, draft.Payload.data, configurationAAD(owner, entry))
		count, err := tx.ConfigurationAsset.Update().Where(configurationasset.IDEQ(entry.ID), configurationasset.RevisionEQ(expectedRevision)).SetName(entry.Name).SetSchemaVersion(entry.SchemaVersion).SetRevision(entry.Revision).SetUpdatedAt(entry.UpdatedAt).SetCiphertext(ciphertext).Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConfigurationConflict
		}
		result = configurationMetadata(entry)
		return nil
	})
	if err != nil {
		return ConfigurationAsset{}, configurationFailure(err)
	}
	return result, nil
}

func (store *Store) DeleteConfigurationAsset(ctx context.Context, owner, id string, expectedRevision int64) error {
	if expectedRevision < 1 {
		return ErrConfigurationInvalid
	}
	err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		entry, err := loadConfigurationAsset(ctx, tx.Client(), owner, id, true)
		if err != nil {
			return err
		}
		if entry.Revision != expectedRevision {
			return ErrConfigurationConflict
		}
		count, err := tx.ConfigurationAsset.Delete().Where(configurationasset.PublicIDEQ(id), configurationasset.RevisionEQ(expectedRevision), configurationasset.HasUserWith(user.UserKeyEQ(owner))).Exec(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConfigurationConflict
		}
		return nil
	})
	return configurationFailure(err)
}
