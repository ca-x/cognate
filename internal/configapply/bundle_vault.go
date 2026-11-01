package configapply

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json/v2"
)

func bundleAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCodexAttention
	}
	return cipher.NewGCM(block)
}
func bundleSeal(key, aad []byte, value any, limit int) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil || len(plain) > limit {
		clear(plain)
		return nil, ErrCodexCapacity
	}
	defer clear(plain)
	aead, err := bundleAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrCodexAttention
	}
	return json.Marshal(codexEnvelope{Version: 1, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, aad)})
}
func bundleUnseal(key, aad, body []byte, destination any, limit int) error {
	if len(body) > 2*limit+1024 {
		return ErrCodexCapacity
	}
	var envelope codexEnvelope
	if json.Unmarshal(body, &envelope, json.RejectUnknownMembers(true)) != nil || envelope.Version != 1 {
		return ErrCodexAttention
	}
	defer clear(envelope.Ciphertext)
	aead, err := bundleAEAD(key)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() || len(envelope.Ciphertext) > limit+aead.Overhead() {
		return ErrCodexAttention
	}
	plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return ErrCodexAttention
	}
	defer clear(plain)
	if json.Unmarshal(plain, destination, json.RejectUnknownMembers(true)) != nil {
		return ErrCodexAttention
	}
	return nil
}
