package state

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/collectedskill"
	"github.com/ca-x/cognate/internal/server/ent/pack"
	"github.com/ca-x/cognate/internal/server/ent/packitem"
	"github.com/ca-x/cognate/internal/server/ent/packshare"
	"github.com/ca-x/cognate/internal/skill"
)

const MaxPackItems = 256

var (
	ErrPackNotFound         = errors.New("pack not found")
	ErrPackConflict         = errors.New("pack or skill revision changed")
	ErrPackInvalid          = errors.New("invalid pack name, description, or selection")
	ErrPackSlugConflict     = errors.New("different skill content requires distinct aliases")
	ErrPackSkillUnavailable = errors.New("selected skill unavailable")
	ErrPackLimit            = errors.New("pack capacity exhausted")
)

type PackDraft struct {
	Name        string
	Description string
	Items       []PackSelection
}

type PackSelection struct {
	SkillID string
	Slug    string
}

type Pack struct {
	ID          string
	Name        string
	Description string
	Revision    int64
	ItemCount   int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Items       []PackSkill
}

type PackSkill struct {
	SkillID        string
	Slug           string
	Name           string
	Description    string
	ContentHash    string
	RepositoryID   string
	RepositoryName string
	Path           string
	ArchiveHash    string
}

func (store *Store) CreatePack(ctx context.Context, userKey string, draft PackDraft) (Pack, error) {
	return store.savePack(ctx, userKey, "", 0, draft)
}

func (store *Store) UpdatePack(ctx context.Context, userKey, id string, expectedRevision int64, draft PackDraft) (Pack, error) {
	if !validPackKey(id) || expectedRevision <= 0 || expectedRevision == math.MaxInt64 {
		return Pack{}, ErrPackConflict
	}
	return store.savePack(ctx, userKey, id, expectedRevision, draft)
}

func (store *Store) savePack(ctx context.Context, userKey, id string, expectedRevision int64, draft PackDraft) (Pack, error) {
	draft, err := normalizePackDraft(draft)
	if err != nil {
		return Pack{}, err
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return Pack{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return Pack{}, err
	}
	defer tx.Rollback()
	if err := store.guardMCPTransaction(ctx, tx, userKey, MCPPacksWrite); err != nil {
		return Pack{}, err
	}
	var entry *ent.Pack
	if id == "" {
		entry, err = tx.Pack.Create().SetPublicID(rand.Text()).SetUserID(owner.ID).SetName(draft.Name).SetDescription(draft.Description).SetItemCount(len(draft.Items)).SetCreatedAt(store.state.now()).SetUpdatedAt(store.state.now()).Save(ctx)
		if err != nil {
			return Pack{}, err
		}
		count, err := tx.Pack.Query().Where(pack.UserIDEQ(owner.ID)).Count(ctx)
		if err != nil {
			return Pack{}, err
		}
		if count > 100 {
			return Pack{}, ErrPackLimit
		}
	} else {
		if err := fencePack(ctx, tx, owner.ID, id, expectedRevision, store.state.now()); err != nil {
			return Pack{}, err
		}
		entry, err = tx.Pack.Query().Where(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(id)).Only(ctx)
		if err != nil {
			return Pack{}, err
		}
		if err := tx.Pack.UpdateOne(entry).SetName(draft.Name).SetDescription(draft.Description).SetItemCount(len(draft.Items)).Exec(ctx); err != nil {
			return Pack{}, err
		}
		if _, err := tx.PackItem.Delete().Where(packitem.PackIDEQ(entry.ID)).Exec(ctx); err != nil {
			return Pack{}, err
		}
	}
	resolved, err := resolvePackSelections(ctx, tx, owner.ID, draft.Items)
	if err != nil {
		return Pack{}, err
	}
	builders := make([]*ent.PackItemCreate, 0, len(resolved))
	for _, item := range resolved {
		builders = append(builders, tx.PackItem.Create().SetPackID(entry.ID).SetSkillID(item.skillID).SetSlug(item.slug))
	}
	if err := tx.PackItem.CreateBulk(builders...).Exec(ctx); err != nil {
		return Pack{}, err
	}
	loaded, err := withPackItems(tx.Pack.Query()).Where(pack.IDEQ(entry.ID)).Only(ctx)
	if err != nil {
		return Pack{}, err
	}
	result, err := packDetails(loaded)
	if err != nil {
		return Pack{}, err
	}
	if err := tx.Commit(); err != nil {
		return Pack{}, err
	}
	return result, nil
}

func (store *Store) ListPacks(ctx context.Context, userKey string) ([]Pack, error) {
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return nil, err
	}
	entries, err := store.state.client.Pack.Query().Where(pack.UserIDEQ(owner.ID)).Order(ent.Desc(pack.FieldUpdatedAt), ent.Asc(pack.FieldPublicID)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Pack, 0, len(entries))
	for _, entry := range entries {
		result = append(result, packSummary(entry))
	}
	return result, nil
}

func (store *Store) Pack(ctx context.Context, userKey, id string) (Pack, error) {
	if !validPackKey(id) {
		return Pack{}, ErrPackNotFound
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return Pack{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return Pack{}, err
	}
	defer tx.Rollback()
	entry, err := withPackItems(tx.Pack.Query()).Where(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return Pack{}, ErrPackNotFound
	}
	if err != nil {
		return Pack{}, err
	}
	result, err := packDetails(entry)
	if err != nil {
		return Pack{}, err
	}
	if err := tx.Commit(); err != nil {
		return Pack{}, err
	}
	return result, nil
}

func (store *Store) DeletePack(ctx context.Context, userKey, id string, expectedRevision int64) error {
	if !validPackKey(id) || expectedRevision <= 0 || expectedRevision == math.MaxInt64 {
		return ErrPackConflict
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := store.guardMCPTransaction(ctx, tx, userKey, MCPPacksWrite); err != nil {
		return err
	}
	if err := fencePack(ctx, tx, owner.ID, id, expectedRevision, store.state.now()); err != nil {
		return err
	}
	entry, err := tx.Pack.Query().Where(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(id)).Only(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.PackShare.Delete().Where(packshare.PackIDEQ(entry.ID)).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.PackItem.Delete().Where(packitem.PackIDEQ(entry.ID)).Exec(ctx); err != nil {
		return err
	}
	if err := tx.Pack.DeleteOne(entry).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func fencePack(ctx context.Context, tx *ent.Tx, ownerID int, id string, revision int64, now time.Time) error {
	changed, err := tx.Pack.Update().Where(pack.UserIDEQ(ownerID), pack.PublicIDEQ(id), pack.RevisionEQ(revision)).AddRevision(1).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return err
	}
	if changed == 1 {
		return nil
	}
	exists, err := tx.Pack.Query().Where(pack.UserIDEQ(ownerID), pack.PublicIDEQ(id)).Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return ErrPackNotFound
	}
	return ErrPackConflict
}

type resolvedPackItem struct {
	skillID int
	slug    string
}

func resolvePackSelections(ctx context.Context, tx *ent.Tx, ownerID int, selection []PackSelection) ([]resolvedPackItem, error) {
	keys := make([]string, 0, len(selection))
	for _, item := range selection {
		keys = append(keys, item.SkillID)
	}
	entries, err := tx.CollectedSkill.Query().Where(collectedskill.PublicIDIn(keys...), collectedskill.HasRepositoryWith(collectedrepository.UserIDEQ(ownerID))).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(selection) {
		return nil, ErrPackSkillUnavailable
	}
	byID := make(map[string]*ent.CollectedSkill, len(entries))
	for _, entry := range entries {
		byID[entry.PublicID] = entry
	}
	bySlug := make(map[string]string, len(selection))
	result := make([]resolvedPackItem, 0, len(selection))
	for _, item := range selection {
		entry := byID[item.SkillID]
		slug := item.Slug
		if slug == "" {
			slug = skill.PortableSlug(entry.Name)
		}
		if skill.ValidatePortableSlug(slug) != nil {
			return nil, ErrPackInvalid
		}
		if content, exists := bySlug[slug]; exists && content != entry.ContentHash {
			return nil, ErrPackSlugConflict
		}
		bySlug[slug] = entry.ContentHash
		result = append(result, resolvedPackItem{skillID: entry.ID, slug: slug})
	}
	return result, nil
}

func withPackItems(query *ent.PackQuery) *ent.PackQuery {
	return query.WithItems(func(items *ent.PackItemQuery) {
		items.Order(ent.Asc(packitem.FieldID)).WithSkill(func(skills *ent.CollectedSkillQuery) { skills.WithRepository() })
	})
}

func packSummary(entry *ent.Pack) Pack {
	return Pack{ID: entry.PublicID, Name: entry.Name, Description: entry.Description, Revision: entry.Revision, ItemCount: entry.ItemCount, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt}
}

func packDetails(entry *ent.Pack) (Pack, error) {
	result := packSummary(entry)
	if len(entry.Edges.Items) != entry.ItemCount {
		return Pack{}, ErrPackSkillUnavailable
	}
	result.Items = make([]PackSkill, 0, len(entry.Edges.Items))
	bySlug := make(map[string]string)
	for _, item := range entry.Edges.Items {
		selected := item.Edges.Skill
		if selected == nil || selected.Edges.Repository == nil || selected.Edges.Repository.UserID != entry.UserID || skill.ValidatePortableSlug(item.Slug) != nil || !validContentHash(selected.ContentHash) {
			return Pack{}, ErrPackSkillUnavailable
		}
		if hash, exists := bySlug[item.Slug]; exists && hash != selected.ContentHash {
			return Pack{}, ErrPackSlugConflict
		}
		bySlug[item.Slug] = selected.ContentHash
		repo := selected.Edges.Repository
		result.Items = append(result.Items, PackSkill{SkillID: selected.PublicID, Slug: item.Slug, Name: selected.Name, Description: selected.Description, ContentHash: selected.ContentHash, RepositoryID: repo.PublicID, RepositoryName: repo.NameWithOwner, Path: selected.Path, ArchiveHash: repo.ArchiveHash})
	}
	return result, nil
}

func normalizePackDraft(draft PackDraft) (PackDraft, error) {
	draft.Name, draft.Description = strings.TrimSpace(draft.Name), strings.TrimSpace(draft.Description)
	if draft.Name == "" || len(draft.Name) > 128 || !utf8.ValidString(draft.Name) || strings.ContainsFunc(draft.Name, unicode.IsControl) || len(draft.Description) > 2048 || !utf8.ValidString(draft.Description) || strings.ContainsAny(draft.Description, "\x00\r") || len(draft.Items) == 0 || len(draft.Items) > MaxPackItems {
		return PackDraft{}, ErrPackInvalid
	}
	seen := make(map[string]struct{}, len(draft.Items))
	for _, item := range draft.Items {
		if !validPackKey(item.SkillID) || item.Slug != "" && skill.ValidatePortableSlug(item.Slug) != nil {
			return PackDraft{}, ErrPackInvalid
		}
		if _, exists := seen[item.SkillID]; exists {
			return PackDraft{}, ErrPackInvalid
		}
		seen[item.SkillID] = struct{}{}
	}
	return draft, nil
}

func validPackKey(value string) bool {
	if len(value) != 26 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return false
		}
	}
	return true
}
