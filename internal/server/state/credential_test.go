package state_test

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/state"
)

func TestServerDatabaseRejectsLinksAndUntrustedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem permissions")
	}
	for _, mutation := range []string{"database-symlink", "database-hardlink", "wal-symlink", "untrusted-parent", "untrusted-ancestor", "symlink-ancestor"} {
		t.Run(mutation, func(t *testing.T) {
			directory := privateDirectory(t)
			path := filepath.Join(directory, "server.db")
			outside := filepath.Join(t.TempDir(), "other")
			if err := os.WriteFile(outside, []byte("unrelated"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "database-symlink":
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "database-hardlink":
				if err := os.Link(outside, path); err != nil {
					t.Fatal(err)
				}
			case "wal-symlink":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path+"-wal"); err != nil {
					t.Fatal(err)
				}
			case "untrusted-parent":
				if err := os.Chmod(directory, 0o777); err != nil {
					t.Fatal(err)
				}
			case "untrusted-ancestor":
				ancestor := filepath.Join(directory, "writable")
				if err := os.Mkdir(ancestor, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(ancestor, 0o777); err != nil {
					t.Fatal(err)
				}
				child := filepath.Join(ancestor, "private")
				if err := os.Mkdir(child, 0o700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(child, "server.db")
			case "symlink-ancestor":
				child := filepath.Join(directory, "private")
				if err := os.Mkdir(child, 0o700); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(directory, "alias")
				if err := os.Symlink(child, alias); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(alias, "server.db")
			}
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				t.Fatal(err)
			}
			store, err := state.Open(t.Context(), path, key)
			if err == nil {
				_ = store.Close()
				t.Fatal("accepted unsafe server database path")
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != "unrelated" {
				t.Fatalf("unrelated file changed: %v", err)
			}
		})
	}
}

func TestCredentialRoundTripIsEncryptedAndUserIsolated(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
	}
	secret := rand.Text()
	token, err := github.NewToken(secret, []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.PutCredential(t.Context(), "alice", "viewer-alice", "", token)
	if err != nil || generation == "" {
		t.Fatalf("store credential failed: %v", err)
	}
	record, err := store.Credential(t.Context(), "alice")
	if err != nil || record.Token.Reveal() != secret || record.ViewerID != "viewer-alice" || record.Generation != generation {
		t.Fatalf("credential round trip failed: %v", err)
	}
	for _, text := range []string{fmt.Sprintf("%+v", record), fmt.Sprintf("%#v", record), fmt.Sprintf("%+v", *store), fmt.Sprintf("%#v", *store)} {
		if strings.Contains(text, secret) {
			t.Fatal("credential leaked through formatting")
		}
	}
	if _, err := store.Credential(t.Context(), "bob"); !errors.Is(err, state.ErrCredentialNotFound) {
		t.Fatalf("other user could access credential: %v", err)
	}
	for _, file := range []string{path, path + "-wal"} {
		contents, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte(secret)) {
			t.Fatal("plaintext credential found in database or WAL")
		}
	}
}

func TestCredentialRejectsCrossUserCiphertextSwap(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
		token, err := github.NewToken(rand.Text(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutCredential(t.Context(), user, "same-viewer", "", token); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_credentials SET ciphertext = (SELECT ciphertext FROM git_hub_credentials WHERE user_id = (SELECT id FROM users WHERE user_key = 'alice')) WHERE user_id = (SELECT id FROM users WHERE user_key = 'bob')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Credential(t.Context(), "bob"); err == nil {
		t.Fatal("accepted another user's ciphertext")
	}
}

func TestStoredRefreshCredentialIsEncryptedAndNotExposed(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	access, refresh := rand.Text(), rand.Text()
	material, err := json.Marshal(map[string]any{"version": 1, "access": access, "refresh": refresh, "client_id": "client-id"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := github.DecodeStoredToken(material)
	clear(material)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	record, err := store.Credential(t.Context(), "alice")
	if err != nil || record.Token.Reveal() != access || record.Token.ClientID() != "client-id" {
		t.Fatalf("stored access credential failed: %v", err)
	}
	if _, ok := record.Token.RefreshToken(); ok {
		t.Fatal("ordinary read exposed refresh credential")
	}
	for _, file := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(access)) || bytes.Contains(data, []byte(refresh)) {
			t.Fatal("plaintext credential found in storage")
		}
	}
}

func TestCredentialCASAndTamperingFailClosed(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); !errors.Is(err, state.ErrCredentialConflict) {
		t.Fatalf("blind overwrite accepted: %v", err)
	}
	second, err := store.PutCredential(t.Context(), "alice", "viewer", first, token)
	if err != nil || second == first {
		t.Fatalf("conditional rotation failed: %v", err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", first, token); !errors.Is(err, state.ErrCredentialConflict) {
		t.Fatalf("stale rotation accepted: %v", err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_credentials SET viewer_id = 'other-viewer'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Credential(t.Context(), "alice"); err == nil {
		t.Fatal("accepted ciphertext with altered identity")
	}
}

func TestCredentialStoreRequiresKeyAndRejectsWrongKey(t *testing.T) {
	path := filepath.Join(privateDirectory(t), "server.db")
	if _, err := state.Open(t.Context(), path, nil); err == nil {
		t.Fatal("accepted missing encryption key")
	}
	key, other := make([]byte, 32), make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(other); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), path, other)
	if err == nil {
		if _, err := store.Credential(t.Context(), "alice"); err == nil {
			t.Fatal("wrong key decrypted credential")
		}
		if _, err := store.PutCredential(t.Context(), "alice", "viewer", generation, token); err == nil {
			t.Fatal("wrong key overwrote existing credential")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	store, err = state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil || credential.Generation != generation {
		t.Fatalf("original credential changed: %v", err)
	}
}

func TestWrongKeyCannotAddCredentialForAnotherUser(t *testing.T) {
	key, other := make([]byte, 32), make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(other); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(t.Context(), "bob"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer-alice", "", token); err != nil {
		t.Fatal(err)
	}
	wrong, err := state.Open(t.Context(), path, other)
	if err == nil {
		defer wrong.Close()
		if _, err := wrong.PutCredential(t.Context(), "bob", "viewer-bob", "", token); err == nil {
			t.Fatal("wrong key created a mixed-key database")
		}
	}
}

func TestExistingCredentialsAreAuthenticatedBeforeKeyMetadataAdoption(t *testing.T) {
	key, wrongKey := make([]byte, 32), make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM server_metadata`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if wrong, err := state.Open(t.Context(), path, wrongKey); err == nil {
		_ = wrong.Close()
		t.Fatal("adopted existing credentials with the wrong key")
	}
	store, err = state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Credential(t.Context(), "alice"); err != nil {
		t.Fatalf("correct key could not adopt legacy credentials: %v", err)
	}
}

func privateDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}
