package state

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/skillhub/internal/server/ent"
	"github.com/ca-x/skillhub/internal/server/ent/category"
	"github.com/ca-x/skillhub/internal/server/ent/collectedrepository"
	"github.com/ca-x/skillhub/internal/server/ent/collectedskill"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrCategoryInvalid          = errors.New("invalid category name or selection")
	ErrCategoryNotFound         = errors.New("category not found")
	ErrCategorySkillUnavailable = errors.New("skill not found")
	ErrCategoryConflict         = errors.New("category revision changed")
	ErrCategoryExists           = errors.New("category name already exists")
	ErrCategoryLimit            = errors.New("category capacity exhausted")
)

type Category struct {
	ID         string
	Name       string
	SkillCount int
	Revision   int64
}

func categoryName(input string) (string, string, error) {
	if len(input) > 80 || !utf8.ValidString(input) || strings.ContainsFunc(input, unicode.IsControl) {
		return "", "", ErrCategoryInvalid
	}
	name := norm.NFKC.String(strings.Join(strings.Fields(input), " "))
	if name == "" || len(name) > 80 {
		return "", "", ErrCategoryInvalid
	}
	return name, cases.Fold().String(name), nil
}

func categoryResult(ctx context.Context, entry *ent.Category) (Category, error) {
	count, err := entry.QuerySkills().Where(collectedskill.HasRepositoryWith(collectedrepository.UserIDEQ(entry.UserID))).Count(ctx)
	return Category{ID: entry.PublicID, Name: entry.Name, SkillCount: count, Revision: entry.Revision}, err
}

func categoryResults(ctx context.Context, entries []*ent.Category) ([]Category, error) {
	result := make([]Category, 0, len(entries))
	for _, entry := range entries {
		value, err := categoryResult(ctx, entry)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (store *Store) ListCategories(ctx context.Context, userKey string) ([]Category, error) {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	owner, err := findUser(ctx, tx.Client(), userKey)
	if err != nil {
		return nil, err
	}
	entries, err := tx.Category.Query().Where(category.UserIDEQ(owner.ID)).Order(ent.Asc(category.FieldNormalizedName), ent.Asc(category.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	result, err := categoryResults(ctx, entries)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func (store *Store) CreateCategory(ctx context.Context, userKey, input string) (Category, error) {
	name, normalized, err := categoryName(input)
	if err != nil {
		return Category{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()
	owner, err := findUser(ctx, tx.Client(), userKey)
	if err != nil {
		return Category{}, err
	}
	count, err := tx.Category.Query().Where(category.UserIDEQ(owner.ID)).Count(ctx)
	if err != nil {
		return Category{}, err
	}
	if count >= 100 {
		return Category{}, ErrCategoryLimit
	}
	entry, err := tx.Category.Create().SetPublicID(rand.Text()).SetUserID(owner.ID).SetName(name).SetNormalizedName(normalized).Save(ctx)
	if ent.IsConstraintError(err) {
		return Category{}, ErrCategoryExists
	}
	if err != nil {
		return Category{}, err
	}
	return Category{ID: entry.PublicID, Name: entry.Name, Revision: entry.Revision}, tx.Commit()
}

func ownerCategory(ctx context.Context, tx *ent.Tx, userKey, id string) (*ent.Category, error) {
	owner, err := findUser(ctx, tx.Client(), userKey)
	if err != nil {
		return nil, err
	}
	entry, err := tx.Category.Query().Where(category.UserIDEQ(owner.ID), category.PublicIDEQ(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrCategoryNotFound
	}
	return entry, err
}

func (store *Store) UpdateCategory(ctx context.Context, userKey, id string, revision int64, input string) (Category, error) {
	name, normalized, err := categoryName(input)
	if err != nil || revision < 1 || revision == math.MaxInt64 {
		return Category{}, ErrCategoryInvalid
	}
	var result Category
	err = store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		entry, err := ownerCategory(ctx, tx, userKey, id)
		if err != nil {
			return err
		}
		if entry.Revision != revision {
			return ErrCategoryConflict
		}
		count, err := tx.Category.Update().Where(category.IDEQ(entry.ID), category.RevisionEQ(revision)).SetName(name).SetNormalizedName(normalized).AddRevision(1).Save(ctx)
		if ent.IsConstraintError(err) {
			return ErrCategoryExists
		}
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrCategoryConflict
		}
		entry.Name, entry.Revision = name, revision+1
		result, err = categoryResult(ctx, entry)
		return err
	})
	if err != nil {
		return Category{}, err
	}
	return result, nil
}

func (store *Store) DeleteCategory(ctx context.Context, userKey, id string, revision int64) error {
	if revision < 1 {
		return ErrCategoryInvalid
	}
	return store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		entry, err := ownerCategory(ctx, tx, userKey, id)
		if err != nil {
			return err
		}
		if entry.Revision != revision {
			return ErrCategoryConflict
		}
		count, err := tx.Category.Delete().Where(category.IDEQ(entry.ID), category.RevisionEQ(revision)).Exec(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrCategoryConflict
		}
		return nil
	})
}

func ownerCategorySkill(ctx context.Context, tx *ent.Tx, userKey, id string) (*ent.CollectedSkill, int, error) {
	owner, err := findUser(ctx, tx.Client(), userKey)
	if err != nil {
		return nil, 0, err
	}
	entry, err := tx.CollectedSkill.Query().Where(collectedskill.PublicIDEQ(id), collectedskill.HasRepositoryWith(collectedrepository.UserIDEQ(owner.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, 0, ErrCategorySkillUnavailable
	}
	return entry, owner.ID, err
}

func (store *Store) SkillCategories(ctx context.Context, userKey, skillID string) ([]Category, error) {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	entry, ownerID, err := ownerCategorySkill(ctx, tx, userKey, skillID)
	if err != nil {
		return nil, err
	}
	entries, err := entry.QueryCategories().Where(category.UserIDEQ(ownerID)).Order(ent.Asc(category.FieldNormalizedName), ent.Asc(category.FieldID)).Limit(8).All(ctx)
	if err != nil {
		return nil, err
	}
	result, err := categoryResults(ctx, entries)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func (store *Store) SetSkillCategories(ctx context.Context, userKey, skillID string, ids []string) ([]Category, error) {
	if len(ids) > 8 {
		return nil, ErrCategoryInvalid
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validPackKey(id) || seen[id] {
			return nil, ErrCategoryInvalid
		}
		seen[id] = true
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	entry, ownerID, err := ownerCategorySkill(ctx, tx, userKey, skillID)
	if err != nil {
		return nil, err
	}
	entries, err := tx.Category.Query().Where(category.UserIDEQ(ownerID), category.PublicIDIn(ids...)).Order(ent.Asc(category.FieldNormalizedName), ent.Asc(category.FieldID)).Limit(8).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(ids) {
		return nil, ErrCategoryNotFound
	}
	internalIDs := make([]int, 0, len(entries))
	for _, selected := range entries {
		internalIDs = append(internalIDs, selected.ID)
	}
	if err := tx.CollectedSkill.UpdateOne(entry).ClearCategories().AddCategoryIDs(internalIDs...).Exec(ctx); err != nil {
		return nil, err
	}
	result, err := categoryResults(ctx, entries)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}
