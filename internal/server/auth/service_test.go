package auth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
)

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := NewService(store, key)
	if err != nil {
		t.Fatal(err)
	}
	return service, path
}

func TestLocalAccountSessionAndRevocation(t *testing.T) {
	service, path := testService(t)
	password := []byte(rand.Text())
	user, err := service.Register(t.Context(), "Alice", password)
	if err != nil || user == "" {
		t.Fatalf("register failed: %v", err)
	}
	if _, err := service.Register(t.Context(), "alice", password); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("duplicate username = %v", err)
	}
	if _, err := service.Login(t.Context(), "alice", []byte(rand.Text())); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v", err)
	}
	if _, err := service.Login(t.Context(), "missing", password); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown account = %v", err)
	}
	session, err := service.Login(t.Context(), "ALICE", password)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprintf("%v", session), fmt.Sprintf("%+v", session), fmt.Sprintf("%#v", session)} {
		if strings.Contains(rendered, session.Reveal()) || strings.Contains(rendered, string(password)) {
			t.Fatal("session or password leaked through formatting")
		}
	}
	actual, err := service.Authenticate(t.Context(), session.Reveal())
	if err != nil || actual != user {
		t.Fatalf("authenticate = %q, error = %v", actual, err)
	}
	if err := service.Logout(t.Context(), session.Reveal()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), session.Reveal()); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("revoked session accepted: %v", err)
	}
	for _, file := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, password) || bytes.Contains(data, []byte(session.Reveal())) {
			t.Fatal("plaintext password or session found in database")
		}
	}
}

func TestSessionRejectsTamperingAndExpiry(t *testing.T) {
	service, _ := testService(t)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	password := []byte(rand.Text())
	if _, err := service.Register(t.Context(), "alice", password); err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	if !session.ExpiresAt().Equal(now.Add(sessionLifetime)) {
		t.Fatal("session expiry does not match stored lifetime")
	}
	tampered := session.Reveal()[:len(session.Reveal())-1] + "!"
	if _, err := service.Authenticate(t.Context(), tampered); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("tampered session accepted: %v", err)
	}
	now = now.Add(25 * time.Hour)
	if _, err := service.Authenticate(t.Context(), session.Reveal()); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestPasswordWorkRejectsExcessConcurrency(t *testing.T) {
	service, _ := testService(t)
	service.slots <- struct{}{}
	service.slots <- struct{}{}
	_, err := service.Login(t.Context(), "alice", []byte("valid-password"))
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("saturated authentication returned %v", err)
	}
}

func TestSessionRechecksExpiryAfterRead(t *testing.T) {
	service, _ := testService(t)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	password := []byte(rand.Text())
	if _, err := service.Register(t.Context(), "alice", password); err != nil {
		t.Fatal(err)
	}
	token, err := service.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	service.now = func() time.Time {
		reads++
		if reads > 1 {
			return now.Add(25 * time.Hour)
		}
		return now
	}
	if _, err := service.Authenticate(t.Context(), token.Reveal()); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired during read: %v", err)
	}
}

func TestServiceRejectsWrongMasterKey(t *testing.T) {
	service, _ := testService(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(service.store, key); err == nil {
		t.Fatal("accepted wrong master key")
	}
}
