package configapply

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json/v2"
	"testing"
)

func TestBundleOldEnvelopeAndDomainIsolation(test *testing.T) {
	key := bytes.Repeat([]byte{17}, 32)
	aad := []byte("synthetic-existing-Codex-AAD")
	value := struct {
		Version int
		Value   string
	}{1, "synthetic-backup-content"}
	body, err := bundleSeal(key, aad, value, 1024)
	if err != nil {
		test.Fatal("shared vault seal")
	}
	defer clear(body)
	var envelope codexEnvelope
	if json.Unmarshal(body, &envelope, json.RejectUnknownMembers(true)) != nil || envelope.Version != 1 || len(envelope.Nonce) != 12 {
		test.Fatal("old Codex envelope serialization changed")
	}
	block, _ := aes.NewCipher(key)
	legacy, _ := cipher.NewGCM(block)
	plaintext, err := legacy.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		test.Fatal("old primitive cannot open extracted envelope")
	}
	defer clear(plaintext)
	var decoded struct {
		Version int
		Value   string
	}
	if json.Unmarshal(plaintext, &decoded) != nil || decoded != value {
		test.Fatal("old plaintext schema changed")
	}
	for _, changed := range []struct{ Key, AAD []byte }{{bytes.Repeat([]byte{18}, 32), aad}, {key, []byte("gemini-local-backup-v1")}, {key, []byte("synthetic-other-owner")}} {
		if bundleUnseal(changed.Key, changed.AAD, body, &decoded, 1024) == nil {
			test.Fatal("vault family/key/owner AAD isolation bypassed")
		}
	}
	if bundleUnseal(key, aad, body, &decoded, 1024) != nil || decoded != value {
		test.Fatal("extracted primitive cannot reopen old schema")
	}
}
