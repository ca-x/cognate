package state

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/mcpcredential"
	"github.com/ca-x/cognate/internal/server/ent/predicate"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

const (
	MCPCatalogRead      = "catalog:read"
	MCPCatalogWrite     = "catalog:write"
	MCPPacksRead        = "packs:read"
	MCPPacksWrite       = "packs:write"
	MCPCategoriesRead   = "categories:read"
	MCPCategoriesWrite  = "categories:write"
	MCPPublicationWrite = "publication:write"
	MaxMCPTokens        = 100
	MaxMCPValidity      = 365 * 24 * time.Hour
)

var (
	ErrMCPInvalid      = errors.New("invalid MCP credential metadata")
	ErrMCPUnauthorized = errors.New("invalid MCP credential")
	ErrMCPForbidden    = errors.New("MCP scope not granted")
	ErrMCPNotFound     = errors.New("MCP credential not found")
	ErrMCPConflict     = errors.New("MCP credential revision changed")
	ErrMCPLimit        = errors.New("MCP credential capacity exhausted")
	ErrMCPUnavailable  = errors.New("MCP credential storage unavailable")
)

type MCPMetadata struct {
	Name      string
	Scopes    []string
	ExpiresAt time.Time
}

type MCPCredential struct {
	ID        string
	Name      string
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
	Revision  int64
}

type MCPGrant struct {
	CredentialID string
	Owner        string
	Scopes       []string
	ExpiresAt    time.Time
	Revision     int64
	proof        *mcpProof
}

func (MCPGrant) String() string   { return "[redacted MCP authorization]" }
func (MCPGrant) GoString() string { return "[redacted MCP authorization]" }

func ValidMCPScope(scope string) bool {
	return scope == MCPCatalogRead || scope == MCPCatalogWrite || scope == MCPPacksRead || scope == MCPPacksWrite || scope == MCPCategoriesRead || scope == MCPCategoriesWrite || scope == MCPPublicationWrite
}

func normalizeMCPMetadata(metadata MCPMetadata, now time.Time) (MCPMetadata, error) {
	metadata.Name = strings.TrimSpace(metadata.Name)
	if metadata.Name == "" || len(metadata.Name) > 128 || !utf8.ValidString(metadata.Name) || strings.ContainsFunc(metadata.Name, unicode.IsControl) || !metadata.ExpiresAt.After(now) || metadata.ExpiresAt.After(now.Add(MaxMCPValidity)) || len(metadata.Scopes) == 0 || len(metadata.Scopes) > 7 {
		return MCPMetadata{}, ErrMCPInvalid
	}
	metadata.Scopes = slices.Clone(metadata.Scopes)
	slices.Sort(metadata.Scopes)
	for index, scope := range metadata.Scopes {
		if !ValidMCPScope(scope) || index > 0 && scope == metadata.Scopes[index-1] {
			return MCPMetadata{}, ErrMCPInvalid
		}
	}
	metadata.ExpiresAt = metadata.ExpiresAt.UTC()
	return metadata, nil
}

func (store *Store) CreateMCPCredential(ctx context.Context, owner string, hash []byte, metadata MCPMetadata) (MCPCredential, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return MCPCredential{}, admissionError
	}
	defer releaseWriter()
	metadata, err := normalizeMCPMetadata(metadata, store.state.now())
	if err != nil || len(hash) != 32 {
		return MCPCredential{}, ErrMCPInvalid
	}
	ownerRecord, err := findUser(ctx, store.state.client, owner)
	if err != nil {
		return MCPCredential{}, ErrMCPNotFound
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	defer tx.Rollback()
	entry, err := tx.MCPCredential.Create().SetUserID(ownerRecord.ID).SetPublicID(rand.Text()).SetName(metadata.Name).SetTokenHash(hash).SetScopes(metadata.Scopes).SetCreatedAt(store.state.now().UTC()).SetExpiresAt(metadata.ExpiresAt).Save(ctx)
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	count, err := tx.MCPCredential.Query().Where(mcpcredential.UserIDEQ(ownerRecord.ID)).Count(ctx)
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	if count > MaxMCPTokens {
		return MCPCredential{}, ErrMCPLimit
	}
	if err := tx.Commit(); err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	return mcpMetadata(entry), nil
}

func (store *Store) ListMCPCredentials(ctx context.Context, owner string) ([]MCPCredential, error) {
	entries, err := store.state.client.MCPCredential.Query().Where(mcpcredential.HasUserWith(user.UserKeyEQ(owner))).Order(ent.Desc(mcpcredential.FieldCreatedAt), ent.Asc(mcpcredential.FieldPublicID)).Limit(MaxMCPTokens).All(ctx)
	if err != nil {
		return nil, ErrMCPUnavailable
	}
	result := make([]MCPCredential, 0, len(entries))
	for _, entry := range entries {
		result = append(result, mcpMetadata(entry))
	}
	return result, nil
}

func (store *Store) MCPCredential(ctx context.Context, owner, id string) (MCPCredential, error) {
	if !validPackKey(id) {
		return MCPCredential{}, ErrMCPNotFound
	}
	entry, err := store.state.client.MCPCredential.Query().Where(mcpOwner(owner, id)...).Only(ctx)
	if ent.IsNotFound(err) {
		return MCPCredential{}, ErrMCPNotFound
	}
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	return mcpMetadata(entry), nil
}

func (store *Store) UpdateMCPCredential(ctx context.Context, owner, id string, revision int64, metadata MCPMetadata) (MCPCredential, error) {
	metadata, err := normalizeMCPMetadata(metadata, store.state.now())
	if err != nil {
		return MCPCredential{}, err
	}
	return store.changeMCPCredential(ctx, owner, id, revision, func(update *ent.MCPCredentialUpdate) {
		update.SetName(metadata.Name).SetScopes(metadata.Scopes).SetExpiresAt(metadata.ExpiresAt)
	})
}

func (store *Store) RotateMCPCredential(ctx context.Context, owner, id string, revision int64, hash []byte) (MCPCredential, error) {
	if len(hash) != 32 {
		return MCPCredential{}, ErrMCPInvalid
	}
	return store.changeMCPCredential(ctx, owner, id, revision, func(update *ent.MCPCredentialUpdate) { update.SetTokenHash(hash) })
}

func (store *Store) RevokeMCPCredential(ctx context.Context, owner, id string, revision int64) (MCPCredential, error) {
	return store.changeMCPCredential(ctx, owner, id, revision, func(update *ent.MCPCredentialUpdate) { update.SetRevokedAt(store.state.now().UTC()) })
}

func (store *Store) changeMCPCredential(ctx context.Context, owner, id string, revision int64, change func(*ent.MCPCredentialUpdate)) (MCPCredential, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return MCPCredential{}, admissionError
	}
	defer releaseWriter()
	if !validPackKey(id) {
		return MCPCredential{}, ErrMCPNotFound
	}
	if revision <= 0 || revision == math.MaxInt64 {
		return MCPCredential{}, ErrMCPConflict
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	defer tx.Rollback()
	update := tx.MCPCredential.Update().Where(mcpOwner(owner, id)...).Where(mcpcredential.RevisionEQ(revision), mcpcredential.RevokedAtIsNil()).AddRevision(1)
	change(update)
	changed, err := update.Save(ctx)
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	entry, err := tx.MCPCredential.Query().Where(mcpOwner(owner, id)...).Only(ctx)
	if ent.IsNotFound(err) {
		return MCPCredential{}, ErrMCPNotFound
	}
	if err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	if changed != 1 {
		return MCPCredential{}, ErrMCPConflict
	}
	if err := tx.Commit(); err != nil {
		return MCPCredential{}, ErrMCPUnavailable
	}
	return mcpMetadata(entry), nil
}

func (store *Store) AuthorizeMCP(ctx context.Context, hash []byte, scope string) (MCPGrant, error) {
	return store.authorizeMCP(ctx, store.state.client, hash, scope)
}

func (store *Store) authorizeMCP(ctx context.Context, client *ent.Client, hash []byte, scope string) (MCPGrant, error) {
	if len(hash) != 32 {
		return MCPGrant{}, ErrMCPUnauthorized
	}
	entry, err := client.MCPCredential.Query().Where(mcpcredential.TokenHashEQ(hash), mcpcredential.RevokedAtIsNil()).WithUser().Only(ctx)
	if ent.IsNotFound(err) {
		return MCPGrant{}, ErrMCPUnauthorized
	}
	if err != nil {
		return MCPGrant{}, ErrMCPUnavailable
	}
	now := store.state.now()
	if entry.Edges.User == nil || entry.CreatedAt.After(now) || !entry.ExpiresAt.After(now) {
		return MCPGrant{}, ErrMCPUnauthorized
	}
	grant := MCPGrant{CredentialID: entry.PublicID, Owner: entry.Edges.User.UserKey, Scopes: slices.Clone(entry.Scopes), ExpiresAt: entry.ExpiresAt, Revision: entry.Revision}
	if scope != "" && (!ValidMCPScope(scope) || !slices.Contains(entry.Scopes, scope)) {
		return grant, ErrMCPForbidden
	}
	grant.proof = &mcpProof{hash: [32]byte(hash), credentialID: grant.CredentialID, owner: grant.Owner, revision: grant.Revision, expiresAt: grant.ExpiresAt}
	return grant, nil
}

func mcpOwner(owner, id string) []predicate.MCPCredential {
	return []predicate.MCPCredential{mcpcredential.PublicIDEQ(id), mcpcredential.HasUserWith(user.UserKeyEQ(owner))}
}

func mcpMetadata(entry *ent.MCPCredential) MCPCredential {
	return MCPCredential{ID: entry.PublicID, Name: entry.Name, Scopes: slices.Clone(entry.Scopes), CreatedAt: entry.CreatedAt, ExpiresAt: entry.ExpiresAt, RevokedAt: entry.RevokedAt, Revision: entry.Revision}
}
