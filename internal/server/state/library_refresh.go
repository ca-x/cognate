package state

import (
	"context"
	"crypto/rand"
	"errors"
	"math"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/collectedskill"
	"github.com/ca-x/cognate/internal/server/ent/pack"
	"github.com/ca-x/cognate/internal/server/ent/packitem"
)

var (
	ErrRepositoryRefreshConflict  = errors.New("repository version changed")
	ErrRepositoryRefreshPublished = errors.New("unpublish the repository before refreshing changed contents")
	ErrReferencedSkillRemoved     = errors.New("remove pack references before removing a skill")
)

type RepositoryVersion struct {
	ArchiveHash         string
	PublicationRevision int64
}

type RepositoryRefreshSnapshot struct {
	Repository CollectedRepository
	Version    RepositoryVersion
	Published  bool
}

type RefreshedPack struct {
	ID       string
	Revision int64
}

type RepositoryRefreshResult struct {
	Repository              CollectedRepository
	Version                 RepositoryVersion
	Changed                 bool
	Added, Updated, Removed int
	Packs                   []RefreshedPack
}

func refreshRepositoryResult(ctx context.Context, tx *ent.Tx, entry *ent.CollectedRepository) (CollectedRepository, error) {
	skills, err := tx.CollectedSkill.Query().Where(collectedskill.RepositoryIDEQ(entry.ID)).Order(ent.Asc(collectedskill.FieldPath)).All(ctx)
	if err != nil {
		return CollectedRepository{}, err
	}
	result := collectedRepository(entry)
	result.Skills = make([]CollectedSkill, 0, len(skills))
	for _, selected := range skills {
		result.Skills = append(result.Skills, CollectedSkill{ID: selected.PublicID, Name: selected.Name, Description: selected.Description, Path: selected.Path, ContentHash: selected.ContentHash})
	}
	return result, nil
}

func (store *Store) ReadRepositoryRefresh(ctx context.Context, owner, id string) (RepositoryRefreshSnapshot, error) {
	result, err := store.CollectedRepository(ctx, owner, id)
	if err != nil {
		return RepositoryRefreshSnapshot{}, err
	}
	return RepositoryRefreshSnapshot{Repository: result, Version: RepositoryVersion{ArchiveHash: result.ArchiveHash, PublicationRevision: result.PublicationRevision}, Published: result.Published}, nil
}

func (store *Store) RefreshCollectedRepository(ctx context.Context, owner, id string, expected RepositoryVersion, archiveHash string, skills []repository.ArchivedSkill) (RepositoryRefreshResult, error) {
	if !validContentHash(expected.ArchiveHash) || expected.PublicationRevision < 1 || expected.PublicationRevision == math.MaxInt64 {
		return RepositoryRefreshResult{}, ErrRepositoryRefreshConflict
	}
	var result RepositoryRefreshResult
	err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		if err := store.guardMCPTransaction(ctx, tx, owner, MCPCatalogWrite); err != nil {
			return err
		}
		entry, err := ownerPublication(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		if entry.ArchiveHash != expected.ArchiveHash || entry.PublicationRevision != expected.PublicationRevision {
			return ErrRepositoryRefreshConflict
		}
		if err := validateCollection(repository.Reference{NameWithOwner: entry.NameWithOwner, Branch: entry.Branch}, archiveHash, skills); err != nil {
			return err
		}
		previous, err := tx.CollectedSkill.Query().Where(collectedskill.RepositoryIDEQ(entry.ID)).All(ctx)
		if err != nil {
			return err
		}
		candidate := make(map[string]repository.ArchivedSkill, len(skills))
		for _, selected := range skills {
			candidate[selected.Path] = selected
		}
		if archiveHash == entry.ArchiveHash {
			if len(previous) != len(candidate) {
				return ErrInvalidCollection
			}
			for _, selected := range previous {
				proposed, exists := candidate[selected.Path]
				if !exists || refreshSkillChanged(selected, proposed) {
					return ErrInvalidCollection
				}
			}
			if _, _, err := refreshPackReferences(ctx, tx, entry, previous); err != nil {
				return err
			}
			detail, err := refreshRepositoryResult(ctx, tx, entry)
			if err != nil {
				return err
			}
			result = RepositoryRefreshResult{Repository: detail, Version: expected}
			return ctx.Err()
		}
		if entry.Published {
			return ErrRepositoryRefreshPublished
		}
		count, err := tx.CollectedRepository.Update().Where(collectedrepository.IDEQ(entry.ID), collectedrepository.UserIDEQ(entry.UserID), collectedrepository.ArchiveHashEQ(expected.ArchiveHash), collectedrepository.PublicationRevisionEQ(expected.PublicationRevision), collectedrepository.PublishedEQ(false)).SetArchiveHash(archiveHash).SetSkillCount(len(skills)).AddPublicationRevision(1).Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrRepositoryRefreshConflict
		}
		entry.ArchiveHash, entry.SkillCount, entry.PublicationRevision = archiveHash, len(skills), entry.PublicationRevision+1
		result = RepositoryRefreshResult{Changed: true, Version: RepositoryVersion{ArchiveHash: archiveHash, PublicationRevision: entry.PublicationRevision}}
		result.Packs, err = store.refreshSkillsAndPacks(ctx, tx, entry, previous, candidate, &result)
		if err != nil {
			return err
		}
		result.Repository, err = refreshRepositoryResult(ctx, tx, entry)
		if err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return RepositoryRefreshResult{}, err
	}
	return result, nil
}

func refreshSkillChanged(previous *ent.CollectedSkill, candidate repository.ArchivedSkill) bool {
	return previous.Name != candidate.Name || previous.Description != candidate.Description || previous.ContentHash != candidate.ContentHash
}

func refreshPackReferences(ctx context.Context, tx *ent.Tx, entry *ent.CollectedRepository, previous []*ent.CollectedSkill) ([]*ent.Pack, map[int]bool, error) {
	ids := make([]int, 0, len(previous))
	for _, selected := range previous {
		ids = append(ids, selected.ID)
	}
	references, err := tx.PackItem.Query().Where(packitem.SkillIDIn(ids...)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	referenced, seenPacks := make(map[int]bool), make(map[int]bool)
	packIDs := make([]int, 0, len(references))
	for _, reference := range references {
		referenced[reference.SkillID] = true
		if !seenPacks[reference.PackID] {
			seenPacks[reference.PackID] = true
			packIDs = append(packIDs, reference.PackID)
		}
	}
	packs, err := withPackItems(tx.Pack.Query()).Where(pack.IDIn(packIDs...)).Order(ent.Asc(pack.FieldPublicID)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(packs) != len(packIDs) {
		return nil, nil, ErrPackSkillUnavailable
	}
	for _, selected := range packs {
		if selected.UserID != entry.UserID || !validPackKey(selected.PublicID) || selected.Revision < 1 {
			return nil, nil, ErrPackSkillUnavailable
		}
		if _, err := packDetails(selected); err != nil {
			return nil, nil, err
		}
	}
	return packs, referenced, nil
}

func (store *Store) refreshSkillsAndPacks(ctx context.Context, tx *ent.Tx, entry *ent.CollectedRepository, previous []*ent.CollectedSkill, candidate map[string]repository.ArchivedSkill, result *RepositoryRefreshResult) ([]RefreshedPack, error) {
	changed := make(map[int]bool)
	for _, selected := range previous {
		if proposed, exists := candidate[selected.Path]; exists && refreshSkillChanged(selected, proposed) {
			changed[selected.ID] = true
		}
	}
	packs, referenced, err := refreshPackReferences(ctx, tx, entry, previous)
	if err != nil {
		return nil, err
	}
	affected := make(map[int]bool)
	for _, selected := range packs {
		for _, item := range selected.Edges.Items {
			if changed[item.SkillID] {
				affected[selected.ID] = true
			}
		}
		if affected[selected.ID] && selected.Revision == math.MaxInt64 {
			return nil, ErrPackConflict
		}
	}
	for _, selected := range previous {
		proposed, exists := candidate[selected.Path]
		if !exists {
			if referenced[selected.ID] {
				return nil, ErrReferencedSkillRemoved
			}
			if err := tx.CollectedSkill.DeleteOne(selected).Exec(ctx); err != nil {
				return nil, err
			}
			result.Removed++
		} else if changed[selected.ID] {
			if err := tx.CollectedSkill.UpdateOne(selected).SetName(proposed.Name).SetDescription(proposed.Description).SetContentHash(proposed.ContentHash).Exec(ctx); err != nil {
				return nil, err
			}
			result.Updated++
		}
		delete(candidate, selected.Path)
	}
	for _, proposed := range candidate {
		if err := tx.CollectedSkill.Create().SetPublicID(rand.Text()).SetRepositoryID(entry.ID).SetPath(proposed.Path).SetName(proposed.Name).SetDescription(proposed.Description).SetContentHash(proposed.ContentHash).Exec(ctx); err != nil {
			return nil, err
		}
		result.Added++
	}
	refreshed := make([]RefreshedPack, 0, len(affected))
	for _, selected := range packs {
		if !affected[selected.ID] {
			continue
		}
		loaded, err := withPackItems(tx.Pack.Query()).Where(pack.IDEQ(selected.ID)).Only(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := packDetails(loaded); err != nil {
			return nil, err
		}
		if err := fencePack(ctx, tx, entry.UserID, selected.PublicID, selected.Revision, store.state.now()); err != nil {
			return nil, err
		}
		refreshed = append(refreshed, RefreshedPack{ID: selected.PublicID, Revision: selected.Revision + 1})
	}
	return refreshed, nil
}
