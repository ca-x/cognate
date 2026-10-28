package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/collectedskill"
	"github.com/ca-x/cognate/internal/server/ent/predicate"
)

var (
	ErrRepositoryNotFound = errors.New("collected repository not found")
	ErrLibraryLimit       = errors.New("repository collection limit reached")
	ErrInvalidCollection  = errors.New("invalid repository collection")
)

type CollectedRepository struct {
	ID                  string
	NameWithOwner       string
	Branch              string
	ArchiveHash         string
	SkillCount          int
	Manual              bool
	Published           bool
	PublicationRevision int64
	CollectedAt         time.Time
	Skills              []CollectedSkill
}

type CollectedSkill struct {
	ID          string
	Name        string
	Description string
	Path        string
	ContentHash string
}

func (store *Store) CollectRepository(ctx context.Context, userKey string, reference repository.Reference, archiveHash string, skills []repository.ArchivedSkill) (CollectedRepository, error) {
	if err := ctx.Err(); err != nil {
		return CollectedRepository{}, err
	}
	if err := validateCollection(reference, archiveHash, skills); err != nil {
		return CollectedRepository{}, err
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return CollectedRepository{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return CollectedRepository{}, err
	}
	defer tx.Rollback()
	if err := store.guardMCPTransaction(ctx, tx, userKey, MCPCatalogWrite); err != nil {
		return CollectedRepository{}, err
	}
	entry, err := tx.CollectedRepository.Create().SetPublicID(rand.Text()).SetUserID(owner.ID).SetNameWithOwner(reference.NameWithOwner).SetBranch(reference.Branch).SetArchiveHash(archiveHash).SetSkillCount(len(skills)).SetManual(true).SetCollectedAt(store.state.now()).Save(ctx)
	if ent.IsConstraintError(err) {
		_ = tx.Rollback()
		var result CollectedRepository
		err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
			if err := store.guardMCPTransaction(ctx, tx, userKey, MCPCatalogWrite); err != nil {
				return err
			}
			existing, err := tx.CollectedRepository.Query().Where(collectionIdentity(owner.ID, reference)...).Only(ctx)
			if err != nil {
				return err
			}
			if !existing.Manual {
				existing, err = tx.CollectedRepository.UpdateOne(existing).SetManual(true).Save(ctx)
				if err != nil {
					return err
				}
			}
			result = collectedRepository(existing)
			return nil
		})
		if err != nil {
			return CollectedRepository{}, err
		}
		return result, nil
	}
	if err != nil {
		return CollectedRepository{}, err
	}
	count, err := tx.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(owner.ID)).Count(ctx)
	if err != nil {
		return CollectedRepository{}, err
	}
	if count > 1000 {
		return CollectedRepository{}, ErrLibraryLimit
	}
	builders := make([]*ent.CollectedSkillCreate, 0, len(skills))
	for _, entrySkill := range skills {
		builders = append(builders, tx.CollectedSkill.Create().SetPublicID(rand.Text()).SetRepositoryID(entry.ID).SetName(entrySkill.Name).SetDescription(entrySkill.Description).SetPath(entrySkill.Path).SetContentHash(entrySkill.ContentHash))
	}
	if err := tx.CollectedSkill.CreateBulk(builders...).Exec(ctx); err != nil {
		return CollectedRepository{}, err
	}
	if err := ctx.Err(); err != nil {
		return CollectedRepository{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollectedRepository{}, err
	}
	return collectedRepository(entry), nil
}

func (store *Store) ListCollectedRepositories(ctx context.Context, userKey string) ([]CollectedRepository, error) {
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return nil, err
	}
	entries, err := store.state.client.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(owner.ID)).Order(ent.Asc(collectedrepository.FieldNameWithOwner), ent.Asc(collectedrepository.FieldBranch)).Limit(1000).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]CollectedRepository, 0, len(entries))
	for _, entry := range entries {
		result = append(result, collectedRepository(entry))
	}
	return result, nil
}

func (store *Store) CheckRepositoryCapacity(ctx context.Context, userKey string) error {
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return err
	}
	count, err := store.state.client.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(owner.ID)).Count(ctx)
	if err != nil {
		return err
	}
	if count >= 1000 {
		return ErrLibraryLimit
	}
	return nil
}

func (store *Store) LookupCollectedRepository(ctx context.Context, userKey string, reference repository.Reference) (CollectedRepository, error) {
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return CollectedRepository{}, err
	}
	entry, err := store.state.client.CollectedRepository.Query().Where(collectionIdentity(owner.ID, reference)...).Only(ctx)
	if ent.IsNotFound(err) {
		return CollectedRepository{}, ErrRepositoryNotFound
	}
	if err != nil {
		return CollectedRepository{}, err
	}
	return collectedRepository(entry), nil
}

func (store *Store) CollectedRepository(ctx context.Context, userKey, publicID string) (CollectedRepository, error) {
	if len(publicID) == 0 || len(publicID) > 64 {
		return CollectedRepository{}, ErrRepositoryNotFound
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return CollectedRepository{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return CollectedRepository{}, err
	}
	defer tx.Rollback()
	entry, err := tx.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(owner.ID), collectedrepository.PublicIDEQ(publicID)).WithSkills(func(query *ent.CollectedSkillQuery) { query.Order(ent.Asc(collectedskill.FieldPath)) }).Only(ctx)
	if ent.IsNotFound(err) {
		return CollectedRepository{}, ErrRepositoryNotFound
	}
	if err != nil {
		return CollectedRepository{}, err
	}
	result := collectedRepository(entry)
	result.Skills = make([]CollectedSkill, 0, len(entry.Edges.Skills))
	for _, entrySkill := range entry.Edges.Skills {
		result.Skills = append(result.Skills, CollectedSkill{ID: entrySkill.PublicID, Name: entrySkill.Name, Description: entrySkill.Description, Path: entrySkill.Path, ContentHash: entrySkill.ContentHash})
	}
	if err := tx.Commit(); err != nil {
		return CollectedRepository{}, err
	}
	return result, nil
}

func collectionIdentity(userID int, reference repository.Reference) []predicate.CollectedRepository {
	return []predicate.CollectedRepository{collectedrepository.UserIDEQ(userID), collectedrepository.NameWithOwnerEQ(reference.NameWithOwner), collectedrepository.BranchEQ(reference.Branch)}
}

func collectedRepository(entry *ent.CollectedRepository) CollectedRepository {
	return CollectedRepository{ID: entry.PublicID, NameWithOwner: entry.NameWithOwner, Branch: entry.Branch, ArchiveHash: entry.ArchiveHash, SkillCount: entry.SkillCount, Manual: entry.Manual, Published: entry.Published, PublicationRevision: entry.PublicationRevision, CollectedAt: entry.CollectedAt}
}

func validateCollection(reference repository.Reference, archiveHash string, skills []repository.ArchivedSkill) error {
	normalized, err := repository.ParseReference(reference.NameWithOwner, reference.Branch)
	if err != nil || normalized != reference || !validContentHash(archiveHash) || len(skills) == 0 || len(skills) > 512 {
		return ErrInvalidCollection
	}
	seen := make(map[string]struct{}, len(skills))
	for _, entry := range skills {
		if entry.Path == "" || len(entry.Path) > 1024 || !utf8.ValidString(entry.Path) || path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path || entry.Path == ".." || strings.HasPrefix(entry.Path, "../") || strings.ContainsAny(entry.Path, "\\\x00") {
			return ErrInvalidCollection
		}
		if entry.Name == "" || len(entry.Name) > 256 || !utf8.ValidString(entry.Name) || strings.ContainsFunc(entry.Name, unicode.IsControl) || entry.Description == "" || len(entry.Description) > 4096 || !utf8.ValidString(entry.Description) || !validContentHash(entry.ContentHash) {
			return ErrInvalidCollection
		}
		if _, exists := seen[entry.Path]; exists {
			return ErrInvalidCollection
		}
		seen[entry.Path] = struct{}{}
	}
	return nil
}

func validContentHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}
