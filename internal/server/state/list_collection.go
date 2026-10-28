package state

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/githubrepository"
)

func (store *Store) CollectListRepository(ctx context.Context, result ListSyncResult, repositoryID string, reference repository.Reference, archiveHash string, skills []repository.ArchivedSkill) (CollectedRepository, error) {
	if err := validateCollection(reference, archiveHash, skills); err != nil || reference.Branch != "" {
		return CollectedRepository{}, ErrInvalidCollection
	}
	var collected CollectedRepository
	err := store.withCurrentListSync(ctx, result, func(ctx context.Context, tx *ent.Tx, _ *ent.GitHubCredential, token github.Token) error {
		memberships := store.collectionMemberships(result)
		entry, err := tx.GitHubRepository.Query().Where(githubrepository.UserIDEQ(result.ticket.userID), githubrepository.ViewerIDEQ(result.ticket.viewerID), githubrepository.RepositoryKeyEQ(repositoryID), githubrepository.PrivateEQ(false), githubrepository.HasMembershipsWith(memberships...)).WithMemberships(func(query *ent.GitHubMembershipQuery) { query.Where(memberships...).WithList() }).Only(ctx)
		if ent.IsNotFound(err) {
			return ErrSyncSuperseded
		}
		if err != nil {
			return err
		}
		if strings.ToLower(entry.NameWithOwner) != reference.NameWithOwner {
			return ErrSyncSuperseded
		}
		observed, err := collectionObservations(entry)
		if err != nil {
			return err
		}
		existing, err := tx.CollectedRepository.Query().Where(collectionIdentity(result.ticket.userID, reference)...).Only(ctx)
		if err == nil {
			collected = collectedRepository(existing)
			return store.checkCatalogFresh(token, observed)
		}
		if !ent.IsNotFound(err) {
			return err
		}
		count, err := tx.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(result.ticket.userID)).Count(ctx)
		if err != nil {
			return err
		}
		if count >= 1000 {
			return ErrLibraryLimit
		}
		saved, err := tx.CollectedRepository.Create().SetPublicID(rand.Text()).SetUserID(result.ticket.userID).SetNameWithOwner(reference.NameWithOwner).SetBranch("").SetArchiveHash(archiveHash).SetSkillCount(len(skills)).SetManual(false).SetCollectedAt(store.state.now()).Save(ctx)
		if err != nil {
			return err
		}
		builders := make([]*ent.CollectedSkillCreate, 0, len(skills))
		for _, entrySkill := range skills {
			builders = append(builders, tx.CollectedSkill.Create().SetPublicID(rand.Text()).SetRepositoryID(saved.ID).SetName(entrySkill.Name).SetDescription(entrySkill.Description).SetPath(entrySkill.Path).SetContentHash(entrySkill.ContentHash))
		}
		if err := tx.CollectedSkill.CreateBulk(builders...).Exec(ctx); err != nil {
			return err
		}
		collected = collectedRepository(saved)
		return store.checkCatalogFresh(token, observed)
	})
	if err != nil {
		return CollectedRepository{}, err
	}
	return collected, nil
}

func collectionObservations(entry *ent.GitHubRepository) ([]time.Time, error) {
	if len(entry.Edges.Memberships) == 0 {
		return nil, ErrObservationExpired
	}
	observed := make([]time.Time, 0, len(entry.Edges.Memberships))
	for _, membership := range entry.Edges.Memberships {
		if membership.Edges.List == nil || membership.Edges.List.VerifiedAt == nil {
			return nil, ErrObservationExpired
		}
		observed = append(observed, *membership.Edges.List.VerifiedAt)
	}
	return observed, nil
}
