package state

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/localaccount"
	"github.com/ca-x/cognate/internal/server/ent/session"
)

var (
	ErrAccountNotFound = errors.New("local account not found")
	ErrAccountConflict = errors.New("local account already exists")
	ErrSessionInvalid  = errors.New("session is invalid")
)

type PasswordAccount struct {
	UserKey      string
	PasswordHash []byte
}

func (PasswordAccount) String() string   { return "[redacted local account]" }
func (PasswordAccount) GoString() string { return "[redacted local account]" }

func (store *Store) CreatePasswordAccount(ctx context.Context, username string, passwordHash []byte) (string, error) {
	if err := validateIdentifier(username); err != nil || len(passwordHash) < 32 || len(passwordHash) > 1024 {
		return "", fmt.Errorf("invalid local account")
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return "", fmt.Errorf("start account transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	userKey := uuid.New().String()
	owner, err := tx.User.Create().SetUserKey(userKey).Save(ctx)
	if err != nil {
		return "", fmt.Errorf("create account user: %w", err)
	}
	if err := tx.LocalAccount.Create().SetUserID(owner.ID).SetUsername(username).SetPasswordHash(passwordHash).Exec(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return "", ErrAccountConflict
		}
		return "", fmt.Errorf("create local account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("account creation outcome may be uncertain: %w", err)
	}
	return userKey, nil
}

func (store *Store) PasswordAccount(ctx context.Context, username string) (PasswordAccount, error) {
	if err := validateIdentifier(username); err != nil {
		return PasswordAccount{}, ErrAccountNotFound
	}
	account, err := store.state.client.LocalAccount.Query().Where(localaccount.UsernameEQ(username)).WithUser().Only(ctx)
	if ent.IsNotFound(err) {
		return PasswordAccount{}, ErrAccountNotFound
	}
	if err != nil {
		return PasswordAccount{}, fmt.Errorf("read local account: %w", err)
	}
	if account.Edges.User == nil {
		return PasswordAccount{}, fmt.Errorf("local account owner is unavailable")
	}
	return PasswordAccount{UserKey: account.Edges.User.UserKey, PasswordHash: append([]byte(nil), account.PasswordHash...)}, nil
}

func (store *Store) CreatePasswordSession(ctx context.Context, username string, expectedHash, tokenHash []byte, issuedAt, expiresAt time.Time) error {
	if len(expectedHash) < 32 || len(tokenHash) != 32 || issuedAt.IsZero() || !expiresAt.After(issuedAt) {
		return fmt.Errorf("invalid session material")
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start session transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	account, err := tx.LocalAccount.Query().Where(localaccount.UsernameEQ(username)).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("read session account: %w", err)
	}
	if subtle.ConstantTimeCompare(account.PasswordHash, expectedHash) != 1 {
		return ErrAccountNotFound
	}
	if err := tx.Session.Create().SetUserID(account.UserID).SetTokenHash(tokenHash).SetIssuedAt(issuedAt).SetExpiresAt(expiresAt).Exec(ctx); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("session creation outcome may be uncertain: %w", err)
	}
	return nil
}

func (store *Store) SessionUser(ctx context.Context, tokenHash []byte, now time.Time) (string, time.Time, error) {
	if len(tokenHash) != 32 || now.IsZero() {
		return "", time.Time{}, ErrSessionInvalid
	}
	entry, err := store.state.client.Session.Query().Where(session.TokenHashEQ(tokenHash), session.IssuedAtLTE(now), session.ExpiresAtGT(now)).WithUser().Only(ctx)
	if ent.IsNotFound(err) {
		return "", time.Time{}, ErrSessionInvalid
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read session: %w", err)
	}
	if entry.Edges.User == nil {
		return "", time.Time{}, ErrSessionInvalid
	}
	return entry.Edges.User.UserKey, entry.ExpiresAt, nil
}

func (store *Store) RevokeSession(ctx context.Context, tokenHash []byte) error {
	if len(tokenHash) != 32 {
		return ErrSessionInvalid
	}
	if _, err := store.state.client.Session.Delete().Where(session.TokenHashEQ(tokenHash)).Exec(ctx); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
