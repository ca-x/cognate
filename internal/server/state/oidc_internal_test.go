package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/server/ent"
)

func TestOIDCBindingCannotCommitAfterConcurrentSessionRevocation(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	passwordHash := []byte(rand.Text() + rand.Text())
	user, err := store.CreatePasswordAccount(t.Context(), "alice", passwordHash)
	if err != nil {
		t.Fatal(err)
	}
	proof := sha256.Sum256([]byte(rand.Text()))
	now := time.Now().UTC()
	if err := store.CreatePasswordSession(t.Context(), "alice", passwordHash, proof[:], now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var revoked bool
	store.state.client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if _, ok := query.(*ent.SessionQuery); ok && err == nil {
				once.Do(func() {
					if revokeErr := store.RevokeSession(ctx, proof[:]); revokeErr != nil {
						t.Error(revokeErr)
					} else {
						revoked = true
					}
				})
			}
			return value, err
		})
	}))
	digest := sha256.Sum256([]byte(rand.Text()))
	_, err = store.CreateOIDCSession(t.Context(), OIDCSessionPlan{Issuer: "https://issuer.example", Subject: "subject", BindUser: user, BindSessionHash: proof[:], TokenHash: digest[:], IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err == nil || !revoked {
		t.Fatalf("binding committed after revocation or test did not revoke: %v", err)
	}
	if _, _, err := store.SessionUser(t.Context(), digest[:], now.Add(time.Second)); err == nil {
		t.Fatal("revoked binding created a session")
	}
}
