package state

import (
	"context"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
)

type mcpProof struct {
	hash         [32]byte
	credentialID string
	owner        string
	revision     int64
	expiresAt    time.Time
}

type mcpContextKey struct{}

type mcpAuthorization struct {
	proof *mcpProof
	scope string
}

func WithMCPAuthorization(ctx context.Context, grant MCPGrant, scope string) context.Context {
	return context.WithValue(ctx, mcpContextKey{}, mcpAuthorization{proof: grant.proof, scope: scope})
}

func (store *Store) checkMCPTransaction(ctx context.Context, tx *ent.Tx, owner, scope string) error {
	authorization, present := ctx.Value(mcpContextKey{}).(mcpAuthorization)
	if !present {
		return nil
	}
	proof := authorization.proof
	if proof == nil || proof.owner != owner || authorization.scope != scope {
		return ErrMCPForbidden
	}
	current, err := store.authorizeMCP(ctx, tx.Client(), proof.hash[:], scope)
	if err != nil {
		return err
	}
	if current.CredentialID != proof.credentialID || current.Owner != proof.owner || current.Revision != proof.revision || !current.ExpiresAt.Equal(proof.expiresAt) {
		return ErrMCPUnauthorized
	}
	return nil
}

func (store *Store) guardMCPTransaction(ctx context.Context, tx *ent.Tx, owner, scope string) error {
	if err := store.checkMCPTransaction(ctx, tx, owner, scope); err != nil {
		return err
	}
	if _, present := ctx.Value(mcpContextKey{}).(mcpAuthorization); present {
		tx.OnCommit(func(next ent.Committer) ent.Committer {
			return ent.CommitFunc(func(commitContext context.Context, transaction *ent.Tx) error {
				if err := store.checkMCPTransaction(ctx, transaction, owner, scope); err != nil {
					return err
				}
				return next.Commit(commitContext, transaction)
			})
		})
	}
	return nil
}
