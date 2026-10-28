package configuration_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

const sentinel = "synthetic-configuration-secret-7f4f3e"

func fixture(test *testing.T) (*state.Store, string, []byte) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "configuration.db")
	key := bytes.Repeat([]byte{83}, 32)
	store, err := state.Open(test.Context(), path, key)
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

func draft() configuration.Draft {
	return configuration.Draft{Name: "  Claude test  ", Provider: configuration.ClaudeProvider{AuthMode: configuration.APIKey, Models: configuration.ClaudeModels{Default: new("claude-test")}}}
}

func replacement(value string) []configuration.SecretMutation {
	return []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(value))}}
}

func TestCreateTypedProvider(test *testing.T) {
	store, _, _ := fixture(test)
	view, err := configuration.New(store).Create(test.Context(), "alice", draft(), replacement(sentinel))
	if err != nil {
		test.Fatalf("create typed provider: %v", err)
	}
	if view.Metadata.Name != "Claude test" || view.Metadata.AgentID != "claude" || view.Metadata.Kind != state.ConfigurationKindProvider || view.Metadata.Revision != 1 || view.Readiness != configuration.CredentialComplete {
		test.Fatalf("incorrect typed metadata or readiness: %+v", view)
	}
	if !view.Secrets[0].Present || view.Secrets[1].Present {
		test.Fatal("incorrect secret status")
	}
	encoded, err := json.Marshal(view)
	if err != nil || strings.Contains(string(encoded), sentinel) {
		test.Fatal("edit view leaked secret or cannot encode")
	}
}

func TestCRUDRestartAndOwnerIsolation(test *testing.T) {
	store, path, key := fixture(test)
	service := configuration.New(store)
	created, err := service.Create(test.Context(), "alice", draft(), replacement(sentinel))
	if err != nil {
		test.Fatal(err)
	}
	view, err := service.Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || view.Metadata.Revision != 1 || !view.Secrets[0].Present {
		test.Fatalf("edit: %+v %v", view, err)
	}
	for _, operation := range []func() error{
		func() error { _, err := service.Edit(test.Context(), "bob", created.Metadata.ID); return err },
		func() error { _, err := service.Get(test.Context(), "bob", created.Metadata.ID); return err },
		func() error {
			_, err := service.Update(test.Context(), "bob", created.Metadata.ID, 1, draft(), nil)
			return err
		},
		func() error {
			_, err := service.Copy(test.Context(), "bob", created.Metadata.ID, 1, "Copy")
			return err
		},
		func() error { return service.Delete(test.Context(), "bob", created.Metadata.ID, 1) },
	} {
		if err := operation(); !errors.Is(err, configuration.ErrNotFound) {
			test.Fatalf("foreign operation: %v", err)
		}
	}
	listed, err := service.List(test.Context(), "alice")
	if err != nil || len(listed) != 1 {
		test.Fatalf("list: %v", err)
	}
	foreign, err := service.List(test.Context(), "bob")
	if err != nil || len(foreign) != 0 {
		test.Fatalf("foreign list: %v", err)
	}
	changed := draft()
	changed.Name = "Changed"
	changed.Provider.Models.Opus = new("Opus CASE preserved")
	updated, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, changed, nil)
	if err != nil || updated.Metadata.Revision != 2 || !updated.Secrets[0].Present {
		test.Fatalf("update keep: %+v %v", updated, err)
	}
	if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, configuration.Draft{}, nil); !errors.Is(err, configuration.ErrConflict) {
		test.Fatalf("stale update before draft validation: %v", err)
	}
	if err := service.Delete(test.Context(), "alice", created.Metadata.ID, 1); !errors.Is(err, configuration.ErrConflict) {
		test.Fatalf("stale delete: %v", err)
	}
	copyView, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 2, "Copy")
	if err != nil || copyView.Metadata.ID == created.Metadata.ID || copyView.Secrets[0].Present || copyView.Readiness != configuration.NeedsCredential || *copyView.Provider.Models.Opus != "Opus CASE preserved" {
		test.Fatalf("copy: %+v %v", copyView, err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reopened.Close() })
	service = configuration.New(reopened)
	view, err = service.Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || view.Metadata.Revision != 2 || !view.Secrets[0].Present {
		test.Fatalf("restart: %+v %v", view, err)
	}
	if err := service.Delete(test.Context(), "alice", created.Metadata.ID, 2); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Edit(test.Context(), "alice", created.Metadata.ID); !errors.Is(err, configuration.ErrNotFound) {
		test.Fatalf("deleted: %v", err)
	}
}
