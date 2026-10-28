package state

import (
	"context"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/mcpaudit"
	"github.com/ca-x/cognate/internal/server/ent/mcpcredential"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

const MaxMCPAuditEntries = 1000

type MCPAudit struct {
	CredentialID string
	Action       string
	Resource     string
	Outcome      string
	CreatedAt    time.Time
}

func validMCPAction(action string) bool {
	switch action {
	case "token.create", "token.update", "token.rotate", "token.revoke", "catalog_list", "catalog_get", "catalog_collect", "packs_list", "packs_get", "packs_create", "packs_update", "packs_delete", "categories_list", "categories_get", "categories_create", "categories_update", "categories_delete", "categories_assign", "publication_get", "publication_publish", "publication_unpublish":
		return true
	default:
		return false
	}
}

func (store *Store) RecordMCPAudit(ctx context.Context, credentialID, action, resource, outcome string) error {
	if !validPackKey(credentialID) || !validMCPAction(action) || resource != "" && !validPackKey(resource) || outcome != "success" && outcome != "denied" && outcome != "failure" {
		return ErrMCPInvalid
	}
	credential, err := store.state.client.MCPCredential.Query().Where(mcpcredential.PublicIDEQ(credentialID)).Select(mcpcredential.FieldID).Only(ctx)
	if err != nil {
		return ErrMCPUnavailable
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return ErrMCPUnavailable
	}
	defer tx.Rollback()
	if err := tx.MCPAudit.Create().SetCredentialID(credential.ID).SetAction(action).SetResource(resource).SetOutcome(mcpaudit.Outcome(outcome)).SetCreatedAt(store.state.now()).Exec(ctx); err != nil {
		return ErrMCPUnavailable
	}
	cutoff, err := tx.MCPAudit.Query().Where(mcpaudit.CredentialIDEQ(credential.ID)).Order(ent.Desc(mcpaudit.FieldID)).Offset(MaxMCPAuditEntries).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return ErrMCPUnavailable
	}
	if cutoff != nil {
		if _, err := tx.MCPAudit.Delete().Where(mcpaudit.CredentialIDEQ(credential.ID), mcpaudit.IDLTE(cutoff.ID)).Exec(ctx); err != nil {
			return ErrMCPUnavailable
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrMCPUnavailable
	}
	return nil
}

func (store *Store) ListMCPAudit(ctx context.Context, owner, credentialID string) ([]MCPAudit, error) {
	if _, err := store.MCPCredential(ctx, owner, credentialID); err != nil {
		return nil, err
	}
	entries, err := store.state.client.MCPAudit.Query().Where(mcpaudit.HasCredentialWith(mcpcredential.PublicIDEQ(credentialID), mcpcredential.HasUserWith(user.UserKeyEQ(owner)))).Order(ent.Desc(mcpaudit.FieldID)).Limit(MaxMCPAuditEntries).All(ctx)
	if err != nil {
		return nil, ErrMCPUnavailable
	}
	result := make([]MCPAudit, 0, len(entries))
	for _, entry := range entries {
		result = append(result, MCPAudit{CredentialID: credentialID, Action: entry.Action, Resource: entry.Resource, Outcome: string(entry.Outcome), CreatedAt: entry.CreatedAt})
	}
	return result, nil
}
