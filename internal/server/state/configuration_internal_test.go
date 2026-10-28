package state

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/configurationasset"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

const configurationStorageSentinel = "synthetic-at-rest-secret-configuration-2fd581bd"

func configurationStorageFixture(test *testing.T) (*Store, string, []byte) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	path, key := filepath.Join(directory, "configuration.db"), bytes.Repeat([]byte{89}, 32)
	store, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	return store, path, key
}

func configurationStorageDraft() ConfigurationAssetDraft {
	return ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: "Synthetic asset", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte(configurationStorageSentinel))}
}

func configurationStoredRow(test *testing.T, store *Store, id string) *ent.ConfigurationAsset {
	test.Helper()
	entry, err := store.state.client.ConfigurationAsset.Query().Where(configurationasset.PublicIDEQ(id)).Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	return entry
}

func TestConfigurationAssetVersionReadConcurrentUpdate(test *testing.T) {
	store, path, key := configurationStorageFixture(test)
	draft := configurationStorageDraft()
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	peer, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peer.Close() })
	loaded, resume := make(chan struct{}), make(chan struct{})
	defer close(resume)
	var queries atomic.Int64
	store.state.client.ConfigurationAsset.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if queries.Add(1) == 1 {
				close(loaded)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return value, err
		})
	}))
	type result struct {
		metadata ConfigurationAsset
		payload  ConfigurationPayload
		err      error
	}
	finished := make(chan result, 1)
	go func() {
		metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", created.ID)
		finished <- result{metadata, payload, err}
	}()
	select {
	case early := <-finished:
		test.Fatalf("read did not load a version: %v", early.err)
	case <-loaded:
	case <-test.Context().Done():
		test.Fatal("version read did not reach SELECT")
	}
	draft.Name, draft.SchemaVersion, draft.Payload = "Revision two", 2, NewConfigurationPayload([]byte("synthetic-version-two"))
	updated, err := peer.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, draft)
	if err != nil {
		test.Fatal(err)
	}
	resume <- struct{}{}
	read := <-finished
	if read.err != nil || read.metadata != created || string(read.payload.Reveal()) != configurationStorageSentinel || queries.Load() != 1 {
		test.Fatalf("version read mixed snapshots or failed: revision=%d queries=%d error=%v", read.metadata.Revision, queries.Load(), read.err)
	}
	metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", created.ID)
	if err != nil || metadata != updated || string(payload.Reveal()) != "synthetic-version-two" {
		test.Fatalf("next read did not return the committed version: revision=%d error=%v", metadata.Revision, err)
	}
	revealed := payload.Reveal()
	revealed[0] ^= 1
	if string(payload.Reveal()) != "synthetic-version-two" {
		test.Fatal("version read payload did not clone on reveal")
	}
}

func TestConfigurationAssetVersionReadFailuresReturnEmpty(test *testing.T) {
	for _, scenario := range []string{"missing", "foreign", "canceled", "closed"} {
		test.Run(scenario, func(test *testing.T) {
			store, _, _ := configurationStorageFixture(test)
			created, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
			if err != nil {
				test.Fatal(err)
			}
			ctx, owner, id, expected := test.Context(), "alice", created.ID, ErrConfigurationNotFound
			switch scenario {
			case "missing":
				id = "missing"
			case "foreign":
				owner = "bob"
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, expected = canceled, ErrConfigurationUnavailable
			case "closed":
				if err := store.Close(); err != nil {
					test.Fatal(err)
				}
				expected = ErrConfigurationUnavailable
			}
			metadata, payload, err := store.ReadConfigurationAssetVersion(ctx, owner, id)
			if !errors.Is(err, expected) || metadata != (ConfigurationAsset{}) || payload.Reveal() != nil {
				test.Fatalf("failed version read returned data or wrong error: %v", err)
			}
		})
	}
}

func TestConfigurationAssetAtRestEncryptionNonceAndHKDFDomain(test *testing.T) {
	store, path, key := configurationStorageFixture(test)
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	first := configurationStoredRow(test, store, created.ID)
	updated, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	second := configurationStoredRow(test, store, updated.ID)
	if len(first.Ciphertext) != len(configurationStorageSentinel)+28 || bytes.Equal(first.Ciphertext[:12], second.Ciphertext[:12]) {
		test.Fatal("nonce was not independently random on update")
	}
	other, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	third := configurationStoredRow(test, store, other.ID)
	if bytes.Equal(second.Ciphertext[:12], third.Ciphertext[:12]) {
		test.Fatal("nonce was reused across assets")
	}
	for _, domain := range []string{"cognate/configuration-assets/v1", "cognate/configuration-assets/v2", "skillhub/configuration-assets/v1", ""} {
		derived := key
		if domain != "" {
			derived, err = hkdf.Key(sha256.New, key, nil, domain, 32)
			if err != nil {
				test.Fatal(err)
			}
		}
		block, err := aes.NewCipher(derived)
		if err != nil {
			test.Fatal(err)
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			test.Fatal(err)
		}
		plaintext, err := aead.Open(nil, nil, second.Ciphertext, configurationAAD("alice", second))
		if domain == "cognate/configuration-assets/v1" {
			if err != nil || string(plaintext) != configurationStorageSentinel {
				test.Fatalf("exact HKDF domain failed: %v", err)
			}
		} else if err == nil {
			test.Fatal("master key or another domain decrypted configuration")
		}
		clear(plaintext)
		if domain != "" {
			clear(derived)
		}
	}
	encoded, err := json.Marshal(second)
	if err != nil || strings.Contains(string(encoded), "ciphertext") || strings.Contains(string(encoded), configurationStorageSentinel) {
		test.Fatal("Sensitive ent JSON leaked configuration material")
	}
	if strings.Contains(fmt.Sprint(second), configurationStorageSentinel) {
		test.Fatal("ent formatting leaked plaintext")
	}
	checkFiles := func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			data, err := os.ReadFile(path + suffix)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				test.Fatal(err)
			}
			if bytes.Contains(data, []byte(configurationStorageSentinel)) {
				test.Fatalf("plaintext sentinel persisted in SQLite%s", suffix)
			}
		}
	}
	checkFiles()
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	checkFiles()
	wrongKey := bytes.Repeat([]byte{90}, 32)
	if reopened, err := Open(test.Context(), path, wrongKey); err == nil {
		_ = reopened.Close()
		test.Fatal("wrong master key reopened database")
	}
}

func TestConfigurationAssetTamperedIdentityAndCiphertextFailClosed(test *testing.T) {
	for _, tamper := range []string{"ciphertext-bit", "truncated", "cross-asset", "cross-owner-ciphertext", "owner-id", "owner-key", "asset-id", "agent", "kind", "schema", "revision", "name", "old-revision", "wrong-derived-key", "aad-purpose"} {
		test.Run(tamper, func(test *testing.T) {
			store, path, _ := configurationStorageFixture(test)
			created, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
			if err != nil {
				test.Fatal(err)
			}
			entry := configurationStoredRow(test, store, created.ID)
			owner, id := "alice", entry.PublicID
			database, err := sql.Open("sqlite", path)
			if err != nil {
				test.Fatal(err)
			}
			defer database.Close()
			var statement string
			var value any
			switch tamper {
			case "ciphertext-bit":
				value = bytes.Clone(entry.Ciphertext)
				ciphertext := value.([]byte)
				ciphertext[len(ciphertext)-1] ^= 1
				statement = "UPDATE configuration_assets SET ciphertext=? WHERE id=?"
			case "truncated":
				value = []byte{1}
				statement = "UPDATE configuration_assets SET ciphertext=? WHERE id=?"
			case "cross-asset", "cross-owner-ciphertext":
				otherOwner := "alice"
				if tamper == "cross-owner-ciphertext" {
					otherOwner = "bob"
				}
				other, err := store.CreateConfigurationAsset(test.Context(), otherOwner, configurationStorageDraft())
				if err != nil {
					test.Fatal(err)
				}
				value = configurationStoredRow(test, store, other.ID).Ciphertext
				statement = "UPDATE configuration_assets SET ciphertext=? WHERE id=?"
			case "owner-id":
				otherOwner, err := store.state.client.User.Query().Where(user.UserKeyEQ("bob")).Only(test.Context())
				if err != nil {
					test.Fatal(err)
				}
				value = otherOwner.ID
				owner = "bob"
				statement = "UPDATE configuration_assets SET user_id=? WHERE id=?"
			case "owner-key":
				if _, err := database.ExecContext(test.Context(), "UPDATE users SET user_key=? WHERE id=?", "renamed-owner", entry.UserID); err != nil {
					test.Fatal(err)
				}
				owner = "renamed-owner"
			case "asset-id":
				value = rand.Text()
				id = value.(string)
				statement = "UPDATE configuration_assets SET public_id=? WHERE id=?"
			case "agent":
				value = "gemini"
				statement = "UPDATE configuration_assets SET agent_id=? WHERE id=?"
			case "kind":
				value = "mcp"
				statement = "UPDATE configuration_assets SET kind=? WHERE id=?"
			case "schema":
				value = 2
				statement = "UPDATE configuration_assets SET schema_version=? WHERE id=?"
			case "revision":
				value = 2
				statement = "UPDATE configuration_assets SET revision=? WHERE id=?"
			case "name":
				value = "Relabeled"
				statement = "UPDATE configuration_assets SET name=? WHERE id=?"
			case "old-revision":
				if _, err := store.UpdateConfigurationAsset(test.Context(), owner, id, 1, configurationStorageDraft()); err != nil {
					test.Fatal(err)
				}
				value = entry.Ciphertext
				statement = "UPDATE configuration_assets SET ciphertext=? WHERE id=?"
			case "wrong-derived-key":
				store.state.configurationAEAD, err = newConfigurationAEAD(bytes.Repeat([]byte{91}, 32))
				if err != nil {
					test.Fatal(err)
				}
			case "aad-purpose":
				aad := configurationAAD(owner, entry)
				aad[0] ^= 1
				value = store.state.configurationAEAD.Seal(nil, nil, []byte(configurationStorageSentinel), aad)
				statement = "UPDATE configuration_assets SET ciphertext=? WHERE id=?"
			}
			if statement != "" {
				if _, err := database.ExecContext(test.Context(), statement, value, entry.ID); err != nil {
					test.Fatal(err)
				}
			}
			payload, err := store.ReadConfigurationAsset(test.Context(), owner, id)
			if !errors.Is(err, ErrConfigurationUnavailable) || len(payload.Reveal()) != 0 {
				test.Fatalf("tampered asset decrypted or leaked backend details: %v", err)
			}
			metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			if !errors.Is(err, ErrConfigurationUnavailable) || metadata != (ConfigurationAsset{}) || payload.Reveal() != nil {
				test.Fatalf("tampered version read returned data or wrong error: %v", err)
			}
		})
	}
}
