package state

import (
	"context"
	"crypto/subtle"

	"github.com/ca-x/cognate/internal/server/ent/localaccount"
	"github.com/ca-x/cognate/internal/server/ent/oidcidentity"
	"github.com/ca-x/cognate/internal/server/ent/session"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

type OIDCBinding struct {
	ID     int
	Issuer string
}

func (store *Store) OIDCBindings(ctx context.Context, owner string) ([]OIDCBinding, error) {
	identities, err := store.state.client.OIDCIdentity.Query().Where(oidcidentity.HasUserWith(user.UserKeyEQ(owner))).Order(oidcidentity.ByID()).Limit(128).All(ctx)
	if err != nil {
		return nil, err
	}
	bindings := make([]OIDCBinding, 0, len(identities))
	for _, identity := range identities {
		bindings = append(bindings, OIDCBinding{ID: identity.ID, Issuer: identity.Issuer})
	}
	return bindings, nil
}

func (store *Store) LocalAccountForUser(ctx context.Context, owner string) (PasswordAccount, error) {
	account, err := store.state.client.LocalAccount.Query().Where(localaccount.HasUserWith(user.UserKeyEQ(owner))).Only(ctx)
	if err != nil {
		return PasswordAccount{}, ErrAccountNotFound
	}
	return PasswordAccount{UserKey: owner, PasswordHash: append([]byte(nil), account.PasswordHash...)}, nil
}

func (store *Store) UnlinkOIDC(ctx context.Context, owner string, tokenHash, passwordHash []byte, identityID int) error {
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := store.state.now().UTC()
	proof, err := tx.Session.Query().Where(session.TokenHashEQ(tokenHash), session.IssuedAtLTE(now), session.ExpiresAtGT(now), session.HasUserWith(user.UserKeyEQ(owner))).Only(ctx)
	if err != nil {
		return ErrSessionInvalid
	}
	account, err := tx.LocalAccount.Query().Where(localaccount.UserIDEQ(proof.UserID)).Only(ctx)
	if err != nil || subtle.ConstantTimeCompare(account.PasswordHash, passwordHash) != 1 {
		return ErrSessionInvalid
	}
	deleted, err := tx.OIDCIdentity.Delete().Where(oidcidentity.IDEQ(identityID), oidcidentity.UserIDEQ(proof.UserID)).Exec(ctx)
	if err != nil {
		return err
	}
	if deleted != 1 {
		return ErrOIDCIdentityConflict
	}
	if _, err := tx.Session.Delete().Where(session.UserIDEQ(proof.UserID), session.TokenHashNEQ(tokenHash)).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
