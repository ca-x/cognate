package state

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strconv"

	"github.com/ca-x/skillhub/internal/server/ent"
	"github.com/ca-x/skillhub/internal/server/ent/servermetadata"
)

const keyMetadataName = "credential-key-v1"
const observationMetadataName = "catalog-observation-policy-v1"

func (store *Store) verifyDatabaseKey(ctx context.Context) error {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start server key verification: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	metadata, err := tx.ServerMetadata.Query().Where(servermetadata.NameEQ(keyMetadataName)).Only(ctx)
	if ent.IsNotFound(err) {
		entries, err := tx.GitHubCredential.Query().WithUser().All(ctx)
		if err != nil {
			return fmt.Errorf("inspect existing encrypted credentials: %w", err)
		}
		for _, entry := range entries {
			if entry.Edges.User == nil {
				return fmt.Errorf("encrypted credential has no owner")
			}
			if _, err := store.decryptCredential(entry.Edges.User.UserKey, entry); err != nil {
				return fmt.Errorf("existing credential key verification failed: %w", err)
			}
		}
		proof := store.state.aead.Seal(nil, nil, []byte(keyMetadataName), []byte(keyMetadataName+":"+store.state.keyID))
		if err := tx.ServerMetadata.Create().SetName(keyMetadataName).SetKeyID(store.state.keyID).SetKeyProof(proof).Exec(ctx); err != nil {
			return fmt.Errorf("record server key verification: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read server key verification: %w", err)
	} else {
		if metadata.KeyID != store.state.keyID {
			return fmt.Errorf("server credential encryption key does not match database")
		}
		proof, err := store.state.aead.Open(nil, nil, metadata.KeyProof, []byte(keyMetadataName+":"+store.state.keyID))
		if err != nil || subtle.ConstantTimeCompare(proof, []byte(keyMetadataName)) != 1 {
			return fmt.Errorf("server credential encryption key verification failed")
		}
	}
	if err := store.verifyObservationPolicy(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit server key verification: %w", err)
	}
	return nil
}

func (store *Store) verifyObservationPolicy(ctx context.Context, tx *ent.Tx) error {
	policy := []byte(strconv.FormatInt(int64(store.state.observationMaxAge), 10))
	aad := []byte(observationMetadataName + ":" + store.state.keyID)
	metadata, err := tx.ServerMetadata.Query().Where(servermetadata.NameEQ(observationMetadataName)).Only(ctx)
	if ent.IsNotFound(err) {
		proof := store.state.aead.Seal(nil, nil, policy, aad)
		if err := tx.ServerMetadata.Create().SetName(observationMetadataName).SetKeyID(store.state.keyID).SetKeyProof(proof).Exec(ctx); err != nil {
			return fmt.Errorf("record catalog observation policy: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read catalog observation policy: %w", err)
	}
	if metadata.KeyID != store.state.keyID {
		return fmt.Errorf("catalog observation policy key mismatch")
	}
	stored, err := store.state.aead.Open(nil, nil, metadata.KeyProof, aad)
	if err != nil || subtle.ConstantTimeCompare(stored, policy) != 1 {
		return fmt.Errorf("catalog observation policy does not match database")
	}
	return nil
}
