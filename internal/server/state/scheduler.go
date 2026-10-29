package state

import (
	"context"
	"fmt"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/githublist"
	"github.com/ca-x/cognate/internal/server/ent/githubmembership"
	"github.com/ca-x/cognate/internal/server/ent/githubrepository"
	"github.com/ca-x/cognate/internal/server/ent/predicate"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

type ListSyncUser struct {
	Cursor int
	Key    string
}

type ListCollectionCandidate struct {
	Cursor     int
	Repository GitHubRepository
}

func (store *Store) ListSyncUsers(ctx context.Context, after, limit int) ([]ListSyncUser, error) {
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid GitHub scheduler page bounds")
	}
	entries, err := store.state.client.User.Query().Where(user.IDGT(after), user.HasGithubCredentialWith(githubcredential.StatusEQ(githubcredential.StatusReady), githubcredential.KeyIDEQ(store.state.keyID))).Order(ent.Asc(user.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read GitHub scheduler users: %w", err)
	}
	users := make([]ListSyncUser, 0, len(entries))
	for _, entry := range entries {
		users = append(users, ListSyncUser{Cursor: entry.ID, Key: entry.UserKey})
	}
	return users, nil
}

func (store *Store) withCurrentListSync(ctx context.Context, result ListSyncResult, apply func(context.Context, *ent.Tx, *ent.GitHubCredential, github.Token) error) error {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return admissionError
	}
	defer releaseWriter()
	ticket := result.ticket
	if !validSyncTicket(ticket) || result.client == nil || result.fetchID == "" {
		return ErrSyncSuperseded
	}
	return result.client.CommitSnapshot(ctx, result.snapshot, func(ctx context.Context, _ github.ListsSnapshot) error {
		tx, err := store.state.client.Tx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		owner, credential, token, err := store.lockCatalogOwner(ctx, tx, ticket.userKey)
		if err != nil {
			return err
		}
		if owner.ID != ticket.userID || credential.ViewerID != ticket.viewerID || credential.Generation != ticket.generation || credential.SyncSequence != ticket.sequence || credential.SyncAppliedSequence != ticket.sequence || credential.SyncFetchID != result.fetchID || !result.client.MatchesToken(token) {
			return ErrSyncSuperseded
		}
		if err := apply(ctx, tx, credential, token); err != nil {
			return err
		}
		if err := store.checkCatalogFresh(token, nil); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (store *Store) collectionMemberships(result ListSyncResult) []predicate.GitHubMembership {
	now := store.state.now().UTC()
	return []predicate.GitHubMembership{githubmembership.VisibleEQ(true), githubmembership.HasListWith(githublist.UserIDEQ(result.ticket.userID), githublist.ViewerIDEQ(result.ticket.viewerID), githublist.CredentialGenerationEQ(result.ticket.generation), githublist.SubscribedEQ(true), githublist.VisibleEQ(true), githublist.VerifiedAtGT(now.Add(-store.state.observationMaxAge)), githublist.VerifiedAtLTE(now))}
}

func (store *Store) ListCollectionBatch(ctx context.Context, result ListSyncResult, limit int) ([]ListCollectionCandidate, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid GitHub collection batch limit")
	}
	var candidates []ListCollectionCandidate
	err := store.withCurrentListSync(ctx, result, func(ctx context.Context, tx *ent.Tx, credential *ent.GitHubCredential, _ github.Token) error {
		query := tx.GitHubRepository.Query().Where(githubrepository.UserIDEQ(result.ticket.userID), githubrepository.ViewerIDEQ(result.ticket.viewerID), githubrepository.PrivateEQ(false), githubrepository.HasMembershipsWith(store.collectionMemberships(result)...)).Order(ent.Asc(githubrepository.FieldID)).Limit(limit)
		entries, err := query.Clone().Where(githubrepository.IDGT(credential.CollectionCursor)).All(ctx)
		if err != nil {
			return err
		}
		if len(entries) == 0 && credential.CollectionCursor != 0 {
			entries, err = query.All(ctx)
			if err != nil {
				return err
			}
		}
		for _, entry := range entries {
			candidates = append(candidates, ListCollectionCandidate{Cursor: entry.ID, Repository: GitHubRepository{ID: entry.RepositoryKey, NameWithOwner: entry.NameWithOwner, DatabaseID: entry.DatabaseID, Private: entry.Private}})
		}
		return nil
	})
	return candidates, err
}

func (store *Store) AdvanceListCollection(ctx context.Context, result ListSyncResult, cursor int) error {
	if cursor < 1 {
		return fmt.Errorf("invalid GitHub collection cursor")
	}
	return store.withCurrentListSync(ctx, result, func(ctx context.Context, tx *ent.Tx, credential *ent.GitHubCredential, _ github.Token) error {
		return tx.GitHubCredential.UpdateOneID(credential.ID).SetCollectionCursor(cursor).Exec(ctx)
	})
}
