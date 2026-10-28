package subscription

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/ca-x/cognate/internal/state"
)

func deriveCredentialKey(key []byte) ([]byte, error) {
	derived, err := hkdf.Key(sha256.New, key, nil, "skillhub/client-pack-credentials/v1", 32)
	if err != nil {
		return nil, fmt.Errorf("cannot derive client credential key")
	}
	return derived, nil
}

func (manager *Manager) credentialCipher() (cipher.AEAD, error) {
	block, err := aes.NewCipher(manager.key)
	if err != nil {
		return nil, fmt.Errorf("client credential key is unavailable")
	}
	return cipher.NewGCM(block)
}

func credentialAAD(id, server, pack string) []byte {
	result := []byte("skillhub/client-pack-credential/v1")
	for _, value := range []string{id, server, pack} {
		result = binary.BigEndian.AppendUint64(result, uint64(len(value)))
		result = append(result, value...)
	}
	return result
}

func (manager *Manager) sealCredential(id, server, pack string, plaintext []byte) ([]byte, error) {
	encryption, err := manager.credentialCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, encryption.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("cannot generate client credential nonce")
	}
	return encryption.Seal(append([]byte{1}, nonce...), nonce, plaintext, credentialAAD(id, server, pack)), nil
}

func (manager *Manager) openCredential(record state.Subscription, sealed []byte) ([]byte, error) {
	encryption, err := manager.credentialCipher()
	if err != nil {
		return nil, err
	}
	offset := 1 + encryption.NonceSize()
	if len(sealed) < offset+encryption.Overhead() || sealed[0] != 1 {
		return nil, fmt.Errorf("saved subscription credential is invalid")
	}
	plaintext, err := encryption.Open(nil, sealed[1:offset], sealed[offset:], credentialAAD(record.ID, record.Server, record.PackID))
	if err != nil {
		return nil, fmt.Errorf("client credential key does not match saved subscriptions")
	}
	return plaintext, nil
}

func (manager *Manager) verifyCredentials(ctx context.Context) error {
	records, err := manager.store.Subscriptions(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := manager.store.WithSubscriptionCredential(ctx, record.ID, func(sealed []byte) error {
			plaintext, err := manager.openCredential(record, sealed)
			clear(plaintext)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
