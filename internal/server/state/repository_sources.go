package state

import (
	"context"
	"crypto/rand"
	"io/fs"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/repositorysource"
)

const maxRepositorySources = 256
const maxSkillSources = 512

type RepositorySource struct {
	ID                    string
	NameWithOwner         string
	Branch                string
	RegisteredAt          time.Time
	CollectedRepositoryID string
	Skills                []repository.SkillSource
}

type ImportRepositorySourceDraft struct {
	CandidateID string
	Repository  string
	Branch      string
}

type ImportSkillSourceReference struct {
	SourceID    string
	Repository  string
	Branch      string
	LogicalPath string
	SafeName    string
}

func (source ImportSkillSourceReference) Valid() bool {
	parsed, err := repository.ParseReference(source.Repository, source.Branch)
	if err != nil || parsed.NameWithOwner != source.Repository || parsed.Branch != source.Branch || len(source.Repository) > 256 {
		return false
	}
	if len(source.SafeName) == 0 || len(source.SafeName) > 256 || !utf8.ValidString(source.SafeName) || strings.TrimSpace(source.SafeName) != source.SafeName || strings.ContainsFunc(source.SafeName, unicode.IsControl) {
		return false
	}
	return len(source.LogicalPath) <= 1024 && utf8.ValidString(source.LogicalPath) && (source.LogicalPath == "" || fs.ValidPath(source.LogicalPath)) && !strings.ContainsAny(source.LogicalPath, "\\:") && !strings.ContainsFunc(source.LogicalPath, unicode.IsControl)
}

func (store *Store) ListRepositorySources(ctx context.Context, userKey string) ([]RepositorySource, error) {
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return nil, err
	}
	entries, err := store.state.client.RepositorySource.Query().Where(repositorysource.UserIDEQ(owner.ID)).Order(ent.Asc(repositorysource.FieldNameWithOwner), ent.Asc(repositorysource.FieldBranch)).Limit(maxRepositorySources + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) > maxRepositorySources {
		return nil, ErrLibraryLimit
	}
	collected, err := store.state.client.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(owner.ID)).Limit(1000).All(ctx)
	if err != nil {
		return nil, err
	}
	verified := make(map[repository.Reference]string, len(collected))
	for _, entry := range collected {
		verified[repository.Reference{NameWithOwner: entry.NameWithOwner, Branch: entry.Branch}] = entry.PublicID
	}
	result := make([]RepositorySource, 0, len(entries))
	for _, entry := range entries {
		result = append(result, RepositorySource{ID: entry.PublicID, NameWithOwner: entry.NameWithOwner, Branch: entry.Branch, RegisteredAt: entry.RegisteredAt, CollectedRepositoryID: verified[repository.Reference{NameWithOwner: entry.NameWithOwner, Branch: entry.Branch}], Skills: slices.Clone(entry.Skills)})
	}
	return result, nil
}

func (store *Store) DeleteRepositorySource(ctx context.Context, userKey, publicID string) error {
	if !validPackKey(publicID) {
		return ErrRepositoryNotFound
	}
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return err
	}
	defer release()
	return store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		if err := store.guardMCPTransaction(ctx, tx, userKey, MCPCatalogWrite); err != nil {
			return err
		}
		owner, err := findUser(ctx, tx.Client(), userKey)
		if err != nil {
			return err
		}
		removed, err := tx.RepositorySource.Delete().Where(repositorysource.UserIDEQ(owner.ID), repositorysource.PublicIDEQ(publicID)).Exec(ctx)
		if err != nil {
			return err
		}
		if removed != 1 {
			return ErrRepositoryNotFound
		}
		return nil
	})
}

func (store *Store) importRegisterSources(ctx context.Context, tx *ent.Tx, ownerID int, plan ImportSelectionPlan, finish *ImportFinishSelection) error {
	if !plan.RegisterSources {
		if len(finish.Sources)+len(finish.SkillSources) != 0 {
			return ErrImportInvalid
		}
		return nil
	}
	if len(finish.Sources) == 0 && len(finish.SkillSources) == 0 && finish.Phase != ImportCompleted {
		return nil
	}
	if len(finish.Sources) != len(plan.RepositoryIDs) || len(finish.SkillSources) > plan.SkillCount {
		return ErrImportInvalid
	}
	rows, err := tx.RepositorySource.Query().Where(repositorysource.UserIDEQ(ownerID)).Limit(maxRepositorySources + 1).All(ctx)
	if err != nil {
		return err
	}
	known := make(map[repository.Reference]*ent.RepositorySource, len(rows))
	references := 0
	for _, row := range rows {
		known[repository.Reference{NameWithOwner: row.NameWithOwner, Branch: row.Branch}] = row
		references += len(row.Skills)
	}
	if len(rows) > maxRepositorySources || references > maxSkillSources {
		return ErrImportQuota
	}
	register := func(reference repository.Reference) (*ent.RepositorySource, bool, error) {
		if row := known[reference]; row != nil {
			return row, false, nil
		}
		if len(known) >= maxRepositorySources {
			return nil, false, ErrImportQuota
		}
		row, err := tx.RepositorySource.Create().SetPublicID(rand.Text()).SetUserID(ownerID).SetNameWithOwner(reference.NameWithOwner).SetBranch(reference.Branch).SetSkills([]repository.SkillSource{}).SetRegisteredAt(store.state.now()).Save(ctx)
		if err != nil {
			return nil, false, err
		}
		known[reference] = row
		return row, true, nil
	}
	for index, source := range finish.Sources {
		parsed, err := repository.ParseReference(source.Repository, source.Branch)
		if err != nil || parsed.NameWithOwner != source.Repository || parsed.Branch != source.Branch || len(source.Repository) > 256 || source.CandidateID != plan.RepositoryIDs[index] {
			return ErrImportInvalid
		}
		row, created, err := register(parsed)
		if err != nil {
			return err
		}
		code := ImportRepositoryAlreadyRegistered
		if created {
			code = ImportRepositoryRegistered
		}
		finish.RepositoryOutcomes[index] = ImportRepositoryOutcome{CandidateID: source.CandidateID, Code: code, RepositoryID: row.PublicID}
	}
	seen := make(map[ImportSkillSourceReference]bool, len(finish.SkillSources))
	for index, source := range finish.SkillSources {
		if source.SourceID != "" || !source.Valid() || seen[source] {
			return ErrImportInvalid
		}
		seen[source] = true
		row, _, err := register(repository.Reference{NameWithOwner: source.Repository, Branch: source.Branch})
		if err != nil {
			return err
		}
		skill := repository.SkillSource{Name: source.SafeName, Path: source.LogicalPath}
		if !slices.Contains(row.Skills, skill) {
			if references >= maxSkillSources {
				return ErrImportQuota
			}
			references++
			row.Skills = append(row.Skills, skill)
			if _, err := tx.RepositorySource.UpdateOne(row).SetSkills(row.Skills).Save(ctx); err != nil {
				return err
			}
		}
		finish.SkillSources[index].SourceID = row.PublicID
	}
	return nil
}
