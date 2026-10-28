package github

import (
	"context"
	"crypto/rand"
	"testing"
	"time"
)

func TestCredentialStorageRoundTripPreservesRotationMetadata(t *testing.T) {
	access, refresh := rand.Text(), rand.Text()
	token, err := NewToken(access, []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	token.clientID = "client-id"
	token.refreshGrant = &refreshGrantState{value: refresh}
	token.expiresAt = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	token.refreshExpiresAt = token.expiresAt.Add(time.Hour)
	var stored []byte
	if err := token.WithStorageMaterial(t.Context(), func(data []byte) error {
		stored = append([]byte(nil), data...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeStoredToken(stored)
	if err != nil || restored.Reveal() != access || restored.clientID != token.clientID {
		t.Fatalf("restored token failed: %v", err)
	}
	gotRefresh, ok := restored.RefreshToken()
	if !ok || gotRefresh.Reveal() != refresh || !restored.expiresAt.Equal(token.expiresAt) || !restored.refreshExpiresAt.Equal(token.refreshExpiresAt) || len(restored.Scopes()) != 1 {
		t.Fatal("rotation metadata was not preserved")
	}
	token.validity.invalidated.Store(true)
	if err := token.WithStorageMaterial(context.Background(), func([]byte) error { t.Fatal("stored invalid token"); return nil }); err == nil {
		t.Fatal("accepted invalidated token")
	}
}
