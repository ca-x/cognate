package mcpaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"strings"

	"github.com/ca-x/cognate/internal/server/state"
)

const tokenPrefix = "smcp1_"

type Service struct{ store *state.Store }

type Secret struct{ value string }

func (Secret) String() string               { return "[redacted MCP credential]" }
func (Secret) GoString() string             { return "[redacted MCP credential]" }
func (Secret) MarshalText() ([]byte, error) { return []byte("[redacted MCP credential]"), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue("[redacted MCP credential]") }
func (secret Secret) Reveal() string        { return secret.value }
func (Service) String() string              { return "[redacted MCP credential service]" }
func (Service) GoString() string            { return "[redacted MCP credential service]" }

func NewService(store *state.Store) (*Service, error) {
	if store == nil {
		return nil, state.ErrMCPUnavailable
	}
	return &Service{store: store}, nil
}

func newSecret() (Secret, []byte) {
	material := make([]byte, 32)
	_, _ = rand.Read(material)
	value := tokenPrefix + base64.RawURLEncoding.EncodeToString(material)
	clear(material)
	hash := sha256.Sum256([]byte(value))
	return Secret{value: value}, hash[:]
}

func (service *Service) Create(ctx context.Context, owner string, metadata state.MCPMetadata) (state.MCPCredential, Secret, error) {
	secret, hash := newSecret()
	defer clear(hash)
	entry, err := service.store.CreateMCPCredential(ctx, owner, hash, metadata)
	if err == nil {
		err = service.store.RecordMCPAudit(ctx, entry.ID, "token.create", entry.ID, "success")
	}
	if err != nil {
		return state.MCPCredential{}, Secret{}, err
	}
	return entry, secret, nil
}

func (service *Service) List(ctx context.Context, owner string) ([]state.MCPCredential, error) {
	return service.store.ListMCPCredentials(ctx, owner)
}

func (service *Service) Get(ctx context.Context, owner, id string) (state.MCPCredential, error) {
	return service.store.MCPCredential(ctx, owner, id)
}

func (service *Service) Update(ctx context.Context, owner, id string, revision int64, metadata state.MCPMetadata) (state.MCPCredential, error) {
	entry, err := service.store.UpdateMCPCredential(ctx, owner, id, revision, metadata)
	if err == nil {
		err = service.store.RecordMCPAudit(ctx, entry.ID, "token.update", entry.ID, "success")
	}
	return entry, err
}

func (service *Service) Rotate(ctx context.Context, owner, id string, revision int64) (state.MCPCredential, Secret, error) {
	secret, hash := newSecret()
	defer clear(hash)
	entry, err := service.store.RotateMCPCredential(ctx, owner, id, revision, hash)
	if err == nil {
		err = service.store.RecordMCPAudit(ctx, entry.ID, "token.rotate", entry.ID, "success")
	}
	if err != nil {
		return state.MCPCredential{}, Secret{}, err
	}
	return entry, secret, nil
}

func (service *Service) Revoke(ctx context.Context, owner, id string, revision int64) (state.MCPCredential, error) {
	entry, err := service.store.RevokeMCPCredential(ctx, owner, id, revision)
	if err == nil {
		err = service.store.RecordMCPAudit(ctx, entry.ID, "token.revoke", entry.ID, "success")
	}
	return entry, err
}

func (service *Service) Authorize(ctx context.Context, value, scope string) (state.MCPGrant, error) {
	material, ok := strings.CutPrefix(value, tokenPrefix)
	if !ok || len(material) != 43 {
		return state.MCPGrant{}, state.ErrMCPUnauthorized
	}
	decoded, err := base64.RawURLEncoding.DecodeString(material)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != material {
		clear(decoded)
		return state.MCPGrant{}, state.ErrMCPUnauthorized
	}
	clear(decoded)
	hash := sha256.Sum256([]byte(value))
	defer clear(hash[:])
	return service.store.AuthorizeMCP(ctx, hash[:], scope)
}

func (service *Service) Audit(ctx context.Context, id, action, resource, outcome string) error {
	return service.store.RecordMCPAudit(ctx, id, action, resource, outcome)
}

func (service *Service) ListAudit(ctx context.Context, owner, id string) ([]state.MCPAudit, error) {
	return service.store.ListMCPAudit(ctx, owner, id)
}
