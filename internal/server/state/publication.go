package state

import (
	"context"
	"errors"
	"math"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
)

var (
	ErrPublicationInvalid      = errors.New("invalid publication revision")
	ErrPublicationConfirmation = errors.New("confirm full skill directory contents before publishing")
	ErrPublicationConflict     = errors.New("publication or skill revision changed")
)

type Publication struct {
	RepositoryID string
	Published    bool
	Revision     int64
}

func ownerPublication(ctx context.Context, tx *ent.Tx, userKey, repositoryID string) (*ent.CollectedRepository, error) {
	owner, err := findUser(ctx, tx.Client(), userKey)
	if err != nil {
		return nil, err
	}
	entry, err := tx.CollectedRepository.Query().Where(collectedrepository.PublicIDEQ(repositoryID), collectedrepository.UserIDEQ(owner.ID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrRepositoryNotFound
	}
	return entry, err
}

func (store *Store) Publication(ctx context.Context, userKey, repositoryID string) (Publication, error) {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return Publication{}, err
	}
	defer tx.Rollback()
	entry, err := ownerPublication(ctx, tx, userKey, repositoryID)
	if err != nil {
		return Publication{}, err
	}
	return Publication{RepositoryID: entry.PublicID, Published: entry.Published, Revision: entry.PublicationRevision}, tx.Commit()
}

func (store *Store) SetPublication(ctx context.Context, userKey, repositoryID string, revision int64, published, confirmFullContents bool) (Publication, error) {
	if revision < 1 || revision == math.MaxInt64 {
		return Publication{}, ErrPublicationInvalid
	}
	if published && !confirmFullContents {
		return Publication{}, ErrPublicationConfirmation
	}
	var result Publication
	err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		entry, err := ownerPublication(ctx, tx, userKey, repositoryID)
		if err != nil {
			return err
		}
		if entry.PublicationRevision != revision {
			return ErrPublicationConflict
		}
		if entry.Published != published {
			count, err := tx.CollectedRepository.Update().Where(collectedrepository.IDEQ(entry.ID), collectedrepository.PublicationRevisionEQ(revision)).SetPublished(published).AddPublicationRevision(1).Save(ctx)
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrPublicationConflict
			}
			entry.PublicationRevision++
		}
		result = Publication{RepositoryID: entry.PublicID, Published: published, Revision: entry.PublicationRevision}
		return nil
	})
	if err != nil {
		return Publication{}, err
	}
	return result, nil
}
