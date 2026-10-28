package state

import (
	"context"
	"errors"
	"time"

	"github.com/ca-x/skillhub/internal/server/ent"
	"github.com/ca-x/skillhub/internal/server/ent/collectedskill"
	"github.com/ca-x/skillhub/internal/server/ent/pack"
	"github.com/ca-x/skillhub/internal/server/ent/packitem"
	"github.com/ca-x/skillhub/internal/server/ent/packshare"
	"github.com/ca-x/skillhub/internal/server/ent/predicate"
)

var ErrPackShareUnavailable = errors.New("pack share invalid, expired, or revoked")

type PackShare struct {
	ID        string
	PackID    string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

func (store *Store) CreatePackShare(ctx context.Context, userKey, packID, shareID string, digest []byte, expiry *time.Time) (PackShare, error) {
	if !validPackKey(packID) || !validPackKey(shareID) || len(digest) != 32 || expiry != nil && (!expiry.After(store.state.now()) || expiry.After(store.state.now().Add(366*24*time.Hour))) {
		return PackShare{}, ErrPackInvalid
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return PackShare{}, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return PackShare{}, err
	}
	defer tx.Rollback()
	changed, err := tx.Pack.Update().Where(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(packID)).SetUpdatedAt(store.state.now()).Save(ctx)
	if err != nil {
		return PackShare{}, err
	}
	if changed != 1 {
		return PackShare{}, ErrPackNotFound
	}
	entry, err := tx.Pack.Query().Where(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(packID)).Only(ctx)
	if err != nil {
		return PackShare{}, err
	}
	all, err := tx.PackShare.Query().Where(packshare.PackIDEQ(entry.ID)).Count(ctx)
	if err != nil {
		return PackShare{}, err
	}
	active, err := tx.PackShare.Query().Where(packshare.PackIDEQ(entry.ID), packshare.RevokedAtIsNil(), packshare.Or(packshare.ExpiresAtIsNil(), packshare.ExpiresAtGT(store.state.now()))).Count(ctx)
	if err != nil {
		return PackShare{}, err
	}
	if all >= 1000 || active >= 100 {
		return PackShare{}, ErrPackLimit
	}
	created, err := tx.PackShare.Create().SetPublicID(shareID).SetPackID(entry.ID).SetTokenHash(digest).SetNillableExpiresAt(expiry).SetCreatedAt(store.state.now()).Save(ctx)
	if err != nil {
		return PackShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return PackShare{}, err
	}
	return packShareInfo(created, packID), nil
}

func (store *Store) ListPackShares(ctx context.Context, userKey, packID string) ([]PackShare, error) {
	if !validPackKey(packID) {
		return nil, ErrPackNotFound
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return nil, err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	exists, err := tx.Pack.Query().Where(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(packID)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrPackNotFound
	}
	entries, err := tx.PackShare.Query().Where(packshare.HasPackWith(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(packID))).Order(ent.Desc(packshare.FieldCreatedAt), ent.Asc(packshare.FieldPublicID)).Limit(1000).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PackShare, 0, len(entries))
	for _, entry := range entries {
		result = append(result, packShareInfo(entry, packID))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (store *Store) RevokePackShare(ctx context.Context, userKey, packID, shareID string) error {
	if !validPackKey(packID) || !validPackKey(shareID) {
		return ErrPackNotFound
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return err
	}
	changed, err := store.state.client.PackShare.Update().Where(packshare.PublicIDEQ(shareID), packshare.HasPackWith(pack.UserIDEQ(owner.ID), pack.PublicIDEQ(packID))).SetRevokedAt(store.state.now()).Save(ctx)
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPackNotFound
	}
	return nil
}

func (store *Store) SharedPack(ctx context.Context, shareID string, digest []byte) (Pack, error) {
	if !validPackKey(shareID) || len(digest) != 32 {
		return Pack{}, ErrPackShareUnavailable
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return Pack{}, err
	}
	defer tx.Rollback()
	entry, err := tx.PackShare.Query().Where(activePackShare(shareID, digest, store.state.now())...).WithPack(func(query *ent.PackQuery) { withPackItems(query) }).Only(ctx)
	if ent.IsNotFound(err) {
		return Pack{}, ErrPackShareUnavailable
	}
	if err != nil {
		return Pack{}, err
	}
	if entry.Edges.Pack == nil {
		return Pack{}, ErrPackShareUnavailable
	}
	result, err := packDetails(entry.Edges.Pack)
	if err != nil {
		return Pack{}, err
	}
	if err := tx.Commit(); err != nil {
		return Pack{}, err
	}
	if entry.ExpiresAt != nil && !entry.ExpiresAt.After(store.state.now()) {
		return Pack{}, ErrPackShareUnavailable
	}
	return result, nil
}

func (store *Store) AuthorizePackSkill(ctx context.Context, shareID string, digest []byte, skillID, contentHash string, revision int64) error {
	if !validPackKey(shareID) || !validPackKey(skillID) || len(digest) != 32 || !validContentHash(contentHash) || revision <= 0 {
		return ErrPackShareUnavailable
	}
	entry, err := store.state.client.PackShare.Query().Where(append(activePackShare(shareID, digest, store.state.now()),
		packshare.HasPackWith(pack.RevisionEQ(revision), pack.HasItemsWith(packitem.HasSkillWith(collectedskill.PublicIDEQ(skillID), collectedskill.ContentHashEQ(contentHash)))),
	)...).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrPackShareUnavailable
	}
	if err != nil {
		return err
	}
	if entry.ExpiresAt != nil && !entry.ExpiresAt.After(store.state.now()) {
		return ErrPackShareUnavailable
	}
	return ctx.Err()
}

func activePackShare(shareID string, digest []byte, now time.Time) []predicate.PackShare {
	return []predicate.PackShare{packshare.PublicIDEQ(shareID), packshare.TokenHashEQ(digest), packshare.RevokedAtIsNil(), packshare.Or(packshare.ExpiresAtIsNil(), packshare.ExpiresAtGT(now))}
}

func packShareInfo(entry *ent.PackShare, packID string) PackShare {
	return PackShare{ID: entry.PublicID, PackID: packID, CreatedAt: entry.CreatedAt, ExpiresAt: entry.ExpiresAt, RevokedAt: entry.RevokedAt}
}
