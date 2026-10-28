package state

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/category"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/collectedskill"
	"github.com/ca-x/cognate/internal/skill"
)

var (
	ErrPublicCatalogInvalid = errors.New("invalid public catalog query, cursor, or limit")
	ErrPublicSkillNotFound  = errors.New("public skill not found")
)

type PublicCategory struct {
	ID         string
	Name       string
	SkillCount int
}

type PublicSkill struct {
	ID                  string
	Slug                string
	Name                string
	Description         string
	ContentHash         string
	RepositoryID        string
	RepositoryName      string
	Path                string
	Branch              string
	PublicationRevision int64
	Categories          []PublicCategory
}

type PublicSkillQuery struct {
	Query      string
	CategoryID string
	Limit      uint32
	Cursor     string
}

type PublicSkillPage struct {
	Skills     []PublicSkill
	NextCursor string
}

type PublicCategoryPage struct {
	Categories []PublicCategory
	NextCursor string
}

type PublicSkillArchive struct {
	ArchiveHash string
	Path        string
	ContentHash string
}

func publicPage(limit uint32, fallback int, cursor, binding string) (int, string, error) {
	if limit > 100 || len(cursor) > 256 {
		return 0, "", ErrPublicCatalogInvalid
	}
	if limit == 0 {
		limit = uint32(fallback)
	}
	if cursor == "" {
		return int(limit), "", nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != cursor {
		return 0, "", ErrPublicCatalogInvalid
	}
	prefix := "v1:" + binding + ":"
	last, ok := strings.CutPrefix(string(decoded), prefix)
	if !ok || !validPackKey(last) {
		return 0, "", ErrPublicCatalogInvalid
	}
	return int(limit), last, nil
}

func publicCursor(binding, last string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + binding + ":" + last))
}

func publicCategoryResult(ctx context.Context, entry *ent.Category) (PublicCategory, error) {
	count, err := entry.QuerySkills().Where(collectedskill.HasRepositoryWith(collectedrepository.PublishedEQ(true), collectedrepository.UserIDEQ(entry.UserID))).Count(ctx)
	return PublicCategory{ID: entry.PublicID, Name: entry.Name, SkillCount: count}, err
}

func publicSkillResult(ctx context.Context, entry *ent.CollectedSkill) (PublicSkill, error) {
	repo := entry.Edges.Repository
	if repo == nil || !repo.Published {
		return PublicSkill{}, ErrPublicSkillNotFound
	}
	result := PublicSkill{ID: entry.PublicID, Slug: skill.PortableSlug(entry.Name), Name: entry.Name, Description: entry.Description, ContentHash: entry.ContentHash, RepositoryID: repo.PublicID, RepositoryName: repo.NameWithOwner, Path: entry.Path, Branch: repo.Branch, PublicationRevision: repo.PublicationRevision, Categories: make([]PublicCategory, 0)}
	categories, err := entry.QueryCategories().Where(category.UserIDEQ(repo.UserID)).Order(ent.Asc(category.FieldPublicID)).Limit(8).All(ctx)
	if err != nil {
		return PublicSkill{}, err
	}
	for _, selected := range categories {
		value, err := publicCategoryResult(ctx, selected)
		if err != nil {
			return PublicSkill{}, err
		}
		result.Categories = append(result.Categories, value)
	}
	return result, nil
}

func (store *Store) ListPublicSkills(ctx context.Context, input PublicSkillQuery) (PublicSkillPage, error) {
	if len(input.Query) > 128 || !utf8.ValidString(input.Query) || strings.ContainsFunc(input.Query, unicode.IsControl) || input.CategoryID != "" && !validPackKey(input.CategoryID) {
		return PublicSkillPage{}, ErrPublicCatalogInvalid
	}
	text := strings.TrimSpace(input.Query)
	digest := sha256.Sum256([]byte(text + "\x00" + input.CategoryID))
	binding := "skills:" + hex.EncodeToString(digest[:])
	limit, last, err := publicPage(input.Limit, 24, input.Cursor, binding)
	if err != nil {
		return PublicSkillPage{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return PublicSkillPage{}, err
	}
	defer tx.Rollback()
	query := tx.CollectedSkill.Query().Where(collectedskill.HasRepositoryWith(collectedrepository.PublishedEQ(true))).WithRepository()
	if text != "" {
		query.Where(collectedskill.Or(collectedskill.NameContainsFold(text), collectedskill.DescriptionContainsFold(text), collectedskill.HasRepositoryWith(collectedrepository.NameWithOwnerContainsFold(text))))
	}
	if input.CategoryID != "" {
		query.Where(collectedskill.HasCategoriesWith(category.PublicIDEQ(input.CategoryID)))
	}
	if last != "" {
		query.Where(collectedskill.PublicIDGT(last))
	}
	entries, err := query.Order(ent.Asc(collectedskill.FieldPublicID)).Limit(limit + 1).All(ctx)
	if err != nil {
		return PublicSkillPage{}, err
	}
	result := PublicSkillPage{Skills: make([]PublicSkill, 0, min(limit, len(entries)))}
	if len(entries) > limit {
		result.NextCursor = publicCursor(binding, entries[limit-1].PublicID)
		entries = entries[:limit]
	}
	for _, entry := range entries {
		value, err := publicSkillResult(ctx, entry)
		if err != nil {
			return PublicSkillPage{}, err
		}
		result.Skills = append(result.Skills, value)
	}
	return result, tx.Commit()
}

func (store *Store) ListPublicCategories(ctx context.Context, limit uint32, cursor string) (PublicCategoryPage, error) {
	count, last, err := publicPage(limit, 64, cursor, "categories")
	if err != nil {
		return PublicCategoryPage{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return PublicCategoryPage{}, err
	}
	defer tx.Rollback()
	query := tx.Category.Query().Where(category.HasSkillsWith(collectedskill.HasRepositoryWith(collectedrepository.PublishedEQ(true))))
	if last != "" {
		query.Where(category.PublicIDGT(last))
	}
	entries, err := query.Order(ent.Asc(category.FieldPublicID)).Limit(count + 1).All(ctx)
	if err != nil {
		return PublicCategoryPage{}, err
	}
	result := PublicCategoryPage{Categories: make([]PublicCategory, 0, min(count, len(entries)))}
	if len(entries) > count {
		result.NextCursor = publicCursor("categories", entries[count-1].PublicID)
		entries = entries[:count]
	}
	for _, entry := range entries {
		value, err := publicCategoryResult(ctx, entry)
		if err != nil {
			return PublicCategoryPage{}, err
		}
		result.Categories = append(result.Categories, value)
	}
	return result, tx.Commit()
}

func visiblePublicSkill(ctx context.Context, client *ent.Client, id string) (*ent.CollectedSkill, error) {
	entry, err := client.CollectedSkill.Query().Where(collectedskill.PublicIDEQ(id), collectedskill.HasRepositoryWith(collectedrepository.PublishedEQ(true))).WithRepository().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrPublicSkillNotFound
	}
	return entry, err
}

func (store *Store) PublicSkill(ctx context.Context, id string) (PublicSkill, error) {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return PublicSkill{}, err
	}
	defer tx.Rollback()
	entry, err := visiblePublicSkill(ctx, tx.Client(), id)
	if err != nil {
		return PublicSkill{}, err
	}
	result, err := publicSkillResult(ctx, entry)
	if err != nil {
		return PublicSkill{}, err
	}
	return result, tx.Commit()
}

func (store *Store) AuthorizePublicSkill(ctx context.Context, id, contentHash string, revision int64) (PublicSkillArchive, error) {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return PublicSkillArchive{}, err
	}
	defer tx.Rollback()
	entry, err := visiblePublicSkill(ctx, tx.Client(), id)
	if err != nil {
		return PublicSkillArchive{}, err
	}
	if entry.ContentHash != contentHash || !validContentHash(contentHash) || entry.Edges.Repository.PublicationRevision != revision {
		return PublicSkillArchive{}, ErrPublicationConflict
	}
	if err := tx.Commit(); err != nil {
		return PublicSkillArchive{}, err
	}
	return PublicSkillArchive{ArchiveHash: entry.Edges.Repository.ArchiveHash, Path: entry.Path, ContentHash: entry.ContentHash}, nil
}
