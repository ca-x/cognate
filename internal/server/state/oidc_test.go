package state_test

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/server/state"
)

func TestOIDCIdentityIsStableAndIssuerScoped(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(privateDirectory(t), "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var first string
	for _, issuer := range []string{"https://one.example", "https://one.example", "https://two.example"} {
		digest := sha256.Sum256([]byte(rand.Text()))
		issuedAt := time.Now().UTC()
		user, err := store.CreateOIDCSession(t.Context(), state.OIDCSessionPlan{Issuer: issuer, Subject: "subject", TokenHash: digest[:], IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = user
		} else if issuer == "https://one.example" && user != first || issuer == "https://two.example" && user == first {
			t.Fatal("OIDC identity was merged or reassigned")
		}
		owner, _, err := store.SessionUser(t.Context(), digest[:], issuedAt.Add(time.Second))
		if err != nil || owner != user {
			t.Fatalf("OIDC session owner: %v", err)
		}
	}
}

func TestOIDCBindingIsExplicitAndCannotTakeAnotherUser(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(privateDirectory(t), "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	issuedAt := time.Now().UTC()
	users := make(map[string]string)
	proofs := make(map[string][]byte)
	for _, username := range []string{"alice", "bob"} {
		passwordHash := []byte(rand.Text() + rand.Text())
		user, err := store.CreatePasswordAccount(t.Context(), username, passwordHash)
		if err != nil {
			t.Fatal(err)
		}
		proof := sha256.Sum256([]byte(rand.Text()))
		if err := store.CreatePasswordSession(t.Context(), username, passwordHash, proof[:], issuedAt, issuedAt.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		users[username], proofs[username] = user, proof[:]
	}
	digest := sha256.Sum256([]byte(rand.Text()))
	plan := state.OIDCSessionPlan{Issuer: "https://issuer.example", Subject: "subject", BindUser: users["alice"], BindSessionHash: proofs["alice"], TokenHash: digest[:], IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(time.Hour)}
	user, err := store.CreateOIDCSession(t.Context(), plan)
	if err != nil || user != users["alice"] {
		t.Fatalf("explicit binding: %v", err)
	}
	otherDigest := sha256.Sum256([]byte(rand.Text()))
	plan.TokenHash, plan.BindUser, plan.BindSessionHash = otherDigest[:], users["bob"], proofs["bob"]
	if _, err := store.CreateOIDCSession(t.Context(), plan); !errors.Is(err, state.ErrOIDCIdentityConflict) {
		t.Fatalf("identity transferred between users: %v", err)
	}
	if _, _, err := store.SessionUser(t.Context(), otherDigest[:], issuedAt.Add(time.Second)); !errors.Is(err, state.ErrSessionInvalid) {
		t.Fatalf("failed bind created a session: %v", err)
	}
	plan.BindUser = ""
	user, err = store.CreateOIDCSession(t.Context(), plan)
	if err != nil || user != users["alice"] {
		t.Fatalf("existing identity login: %v", err)
	}
	if err := store.RevokeSession(t.Context(), proofs["alice"]); err != nil {
		t.Fatal(err)
	}
	plan.Subject, plan.BindUser, plan.BindSessionHash = "new-subject", users["alice"], proofs["alice"]
	newDigest := sha256.Sum256([]byte(rand.Text()))
	plan.TokenHash = newDigest[:]
	if _, err := store.CreateOIDCSession(t.Context(), plan); !errors.Is(err, state.ErrSessionInvalid) {
		t.Fatalf("revoked session authorized binding: %v", err)
	}
}
