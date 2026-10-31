package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/localaccount"
	"github.com/ca-x/cognate/internal/server/ent/servermetadata"
)

const bootstrapMetadataName = "bootstrap-admin-v1"

var ErrBootstrapRequired = errors.New("administrator initialization requires a password")

func (store *Store) BootstrapPasswordAdministrator(ctx context.Context, username string, passwordHash []byte) (string, error) {
	ctx, releaseWriter, err := store.enterWriter(ctx)
	if err != nil {
		return "", err
	}
	defer releaseWriter()
	if validateIdentifier(username) != nil || strings.ContainsRune(username, '\x00') {
		return "", fmt.Errorf("invalid administrator username")
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return "", fmt.Errorf("start administrator initialization transaction")
	}
	defer func() { _ = tx.Rollback() }()
	aad := []byte(bootstrapMetadataName + ":" + store.state.keyID)
	metadata, err := tx.ServerMetadata.Query().Where(servermetadata.NameEQ(bootstrapMetadataName)).Only(ctx)
	if err == nil {
		if metadata.KeyID != store.state.keyID {
			return "", fmt.Errorf("administrator initialization key mismatch")
		}
		binding, err := store.state.aead.Open(nil, nil, metadata.KeyProof, aad)
		defer clear(binding)
		if err != nil {
			return "", fmt.Errorf("administrator initialization binding is invalid")
		}
		storedUsername, userKey, found := strings.Cut(string(binding), "\x00")
		parsed, parseError := uuid.Parse(userKey)
		if !found || storedUsername != username || parseError != nil || parsed.String() != userKey {
			return "", fmt.Errorf("administrator initialization identity does not match")
		}
		account, err := tx.LocalAccount.Query().Where(localaccount.UsernameEQ(username)).WithUser().Only(ctx)
		if err != nil || account.Edges.User == nil || account.Edges.User.UserKey != userKey {
			return "", fmt.Errorf("administrator initialization account is unavailable")
		}
		return userKey, nil
	}
	if !ent.IsNotFound(err) {
		return "", fmt.Errorf("read administrator initialization binding")
	}
	occupied, err := tx.User.Query().Exist(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect administrator initialization eligibility")
	}
	if occupied {
		return "", fmt.Errorf("administrator initialization requires an empty instance; use explicit administrator account IDs for existing accounts")
	}
	if passwordHash == nil {
		return "", ErrBootstrapRequired
	}
	if len(passwordHash) < 32 || len(passwordHash) > 1024 {
		return "", fmt.Errorf("invalid administrator password hash")
	}
	userKey := uuid.New().String()
	owner, err := tx.User.Create().SetUserKey(userKey).Save(ctx)
	if err != nil {
		return "", fmt.Errorf("create administrator identity")
	}
	if err := tx.LocalAccount.Create().SetUserID(owner.ID).SetUsername(username).SetPasswordHash(passwordHash).Exec(ctx); err != nil {
		return "", fmt.Errorf("create administrator account")
	}
	binding := []byte(username + "\x00" + userKey)
	defer clear(binding)
	proof := store.state.aead.Seal(nil, nil, binding, aad)
	if err := tx.ServerMetadata.Create().SetName(bootstrapMetadataName).SetKeyID(store.state.keyID).SetKeyProof(proof).Exec(ctx); err != nil {
		return "", fmt.Errorf("record administrator initialization binding")
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("administrator initialization outcome may be uncertain")
	}
	return userKey, nil
}
