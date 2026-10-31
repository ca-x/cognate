package state

import (
	"context"
	"fmt"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

func (store *Store) beginGitHubTransaction(ctx context.Context, userKey string) (*ent.Tx, error) {
	if err := validateIdentifier(userKey); err != nil {
		return nil, err
	}
	transaction, err := store.state.client.Tx(context.WithValue(ctx, githubTransactionContextKey{}, true))
	if err != nil {
		return nil, err
	}
	if err := transaction.GitHubCredential.Update().Where(githubcredential.HasUserWith(user.UserKeyEQ(userKey))).AddSyncSequence(0).Exec(ctx); err != nil {
		_ = transaction.Rollback()
		return nil, fmt.Errorf("lock GitHub transaction before reading: %w", err)
	}
	return transaction, nil
}
