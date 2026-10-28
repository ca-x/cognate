package state_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/server/state"
)

const configurationSentinel = "configuration-test-only-secret-1eab7ca5-do-not-log"

func configurationFixture(test *testing.T) (*state.Store, string, []byte) {
	test.Helper()
	path := filepath.Join(privateDirectory(test), "configuration.db")
	key := bytes.Repeat([]byte{73}, 32)
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

func configurationDraft() state.ConfigurationAssetDraft {
	return state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "  Synthetic provider  ", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(configurationSentinel))}
}

func TestConfigurationAssetCRUDRestartAndOwnerIsolation(test *testing.T) {
	store, path, key := configurationFixture(test)
	draft := configurationDraft()
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
	if err != nil {
		test.Fatalf("create: %v", err)
	}
	if len(created.ID) != 26 || created.Name != "Synthetic provider" || created.AgentID != "codex" || created.Kind != draft.Kind || created.SchemaVersion != 1 || created.Revision != 1 || created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		test.Fatalf("invalid metadata: %+v", created)
	}
	for _, id := range []string{created.ID, strings.Repeat("A", 26), "invalid"} {
		if _, err := store.ConfigurationAsset(test.Context(), "bob", id); !errors.Is(err, state.ErrConfigurationNotFound) {
			test.Fatalf("foreign metadata: %v", err)
		}
		if _, err := store.ReadConfigurationAsset(test.Context(), "bob", id); !errors.Is(err, state.ErrConfigurationNotFound) {
			test.Fatalf("foreign payload: %v", err)
		}
		if _, err := store.UpdateConfigurationAsset(test.Context(), "bob", id, 1, draft); !errors.Is(err, state.ErrConfigurationNotFound) {
			test.Fatalf("foreign update: %v", err)
		}
		if err := store.DeleteConfigurationAsset(test.Context(), "bob", id, 1); !errors.Is(err, state.ErrConfigurationNotFound) {
			test.Fatalf("foreign delete: %v", err)
		}
	}
	listed, err := store.ListConfigurationAssets(test.Context(), "alice")
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		test.Fatalf("list: %+v %v", listed, err)
	}
	foreign, err := store.ListConfigurationAssets(test.Context(), "bob")
	if err != nil || len(foreign) != 0 {
		test.Fatalf("foreign list: %+v %v", foreign, err)
	}
	draft.Name, draft.SchemaVersion, draft.Payload = "Revised", 2, state.NewConfigurationPayload([]byte("replacement-"+configurationSentinel))
	updated, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, draft)
	if err != nil || updated.Revision != 2 || updated.Name != "Revised" || updated.SchemaVersion != 2 || !updated.CreatedAt.Equal(created.CreatedAt) || updated.UpdatedAt.Before(created.UpdatedAt) {
		test.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, draft); !errors.Is(err, state.ErrConfigurationConflict) {
		test.Fatalf("stale update: %v", err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", created.ID, 1); !errors.Is(err, state.ErrConfigurationConflict) {
		test.Fatalf("stale delete: %v", err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reopened.Close() })
	metadata, err := reopened.ConfigurationAsset(test.Context(), "alice", created.ID)
	if err != nil || metadata.Revision != 2 || metadata.Name != "Revised" {
		test.Fatalf("restart metadata: %+v %v", metadata, err)
	}
	payload, err := reopened.ReadConfigurationAsset(test.Context(), "alice", created.ID)
	if err != nil || string(payload.Reveal()) != "replacement-"+configurationSentinel {
		test.Fatalf("restart payload unavailable: %v", err)
	}
	if err := reopened.DeleteConfigurationAsset(test.Context(), "alice", created.ID, 2); err != nil {
		test.Fatal(err)
	}
	if _, err := reopened.ReadConfigurationAsset(test.Context(), "alice", created.ID); !errors.Is(err, state.ErrConfigurationNotFound) {
		test.Fatalf("deleted asset readable: %v", err)
	}
}

func TestConfigurationPayloadClonesAndRedactsEveryDefaultOutput(test *testing.T) {
	input := []byte(configurationSentinel)
	payload := state.NewConfigurationPayload(input)
	input[0] ^= 1
	clone := payload.Clone()
	for _, secret := range []state.ConfigurationPayload{payload, clone} {
		bytes := secret.Reveal()
		if string(bytes) != configurationSentinel {
			test.Fatal("input was retained instead of cloned")
		}
		bytes[0] ^= 1
		if string(secret.Reveal()) != configurationSentinel {
			test.Fatal("Reveal exposed backing storage")
		}
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("synthetic", "payload", payload, "draft", configurationDraft())
	encoded, err := json.Marshal(struct {
		Payload state.ConfigurationPayload
		Draft   state.ConfigurationAssetDraft
	}{payload, configurationDraft()})
	if err != nil {
		test.Fatal(err)
	}
	for _, output := range []string{fmt.Sprintf("%v %+v %#v", payload, payload, payload), string(encoded), logs.String(), fmt.Sprintf("%+v %#v", configurationDraft(), configurationDraft())} {
		if strings.Contains(output, configurationSentinel) {
			test.Fatal("default formatting leaked payload")
		}
	}
	store, _, _ := configurationFixture(test)
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	read, err := store.ReadConfigurationAsset(test.Context(), "alice", created.ID)
	if err != nil {
		test.Fatal(err)
	}
	revealed := read.Reveal()
	revealed[0] ^= 1
	again, err := store.ReadConfigurationAsset(test.Context(), "alice", created.ID)
	if err != nil || string(again.Reveal()) != configurationSentinel {
		test.Fatalf("caller modified saved payload: %v", err)
	}
	listed, err := store.ListConfigurationAssets(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	encoded, err = json.Marshal(listed)
	if err != nil || strings.Contains(string(encoded), configurationSentinel) || strings.Contains(string(encoded), "ciphertext") || strings.Contains(string(encoded), "payload") {
		test.Fatal("ordinary metadata exposes payload fields")
	}
}

func TestConfigurationAssetValidationAndImmutableIdentity(test *testing.T) {
	store, _, _ := configurationFixture(test)
	for _, invalid := range []struct {
		name   string
		change func(*state.ConfigurationAssetDraft)
	}{
		{"empty-agent", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = "" }},
		{"agent-upper", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = "Codex" }},
		{"agent-long", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = strings.Repeat("a", 65) }},
		{"agent-underscore", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = "my_agent" }},
		{"agent-dot", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = "my.agent" }},
		{"agent-leading-digit", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = "1agent" }},
		{"path-agent", func(draft *state.ConfigurationAssetDraft) { draft.AgentID = "/tmp/config" }},
		{"unknown-kind", func(draft *state.ConfigurationAssetDraft) { draft.Kind = "script" }},
		{"empty-name", func(draft *state.ConfigurationAssetDraft) { draft.Name = " \t " }},
		{"control-name", func(draft *state.ConfigurationAssetDraft) { draft.Name = "bad\x00name" }},
		{"utf8-name", func(draft *state.ConfigurationAssetDraft) { draft.Name = string([]byte{255}) }},
		{"long-name", func(draft *state.ConfigurationAssetDraft) { draft.Name = strings.Repeat("界", 43) }},
		{"schema-zero", func(draft *state.ConfigurationAssetDraft) { draft.SchemaVersion = 0 }},
		{"schema-large", func(draft *state.ConfigurationAssetDraft) { draft.SchemaVersion = 65536 }},
		{"empty-payload", func(draft *state.ConfigurationAssetDraft) { draft.Payload = state.ConfigurationPayload{} }},
		{"large-payload", func(draft *state.ConfigurationAssetDraft) {
			draft.Payload = state.NewConfigurationPayload(make([]byte, state.MaxConfigurationPayloadBytes+1))
		}},
	} {
		test.Run(invalid.name, func(test *testing.T) {
			draft := configurationDraft()
			invalid.change(&draft)
			if _, err := store.CreateConfigurationAsset(test.Context(), "alice", draft); !errors.Is(err, state.ErrConfigurationInvalid) {
				test.Fatalf("invalid accepted: %v", err)
			}
		})
	}
	for _, kind := range []state.ConfigurationKind{state.ConfigurationKindProvider, state.ConfigurationKindMCP, state.ConfigurationKindPrompt, state.ConfigurationKindAgentSettings, state.ConfigurationKindWorkspace} {
		draft := configurationDraft()
		draft.Kind, draft.AgentID, draft.Name, draft.SchemaVersion, draft.Payload = kind, "future-agent-42", strings.Repeat("n", 128), 65535, state.NewConfigurationPayload([]byte{1})
		created, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
		if err != nil {
			test.Fatalf("valid unknown agent/kind/bounds: %v", err)
		}
		changed := draft
		changed.AgentID = "other"
		if _, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, changed); !errors.Is(err, state.ErrConfigurationInvalid) {
			test.Fatalf("agent rewritten: %v", err)
		}
		changed = draft
		changed.Kind = "provider"
		if kind == state.ConfigurationKindProvider {
			changed.Kind = state.ConfigurationKindMCP
		}
		if _, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, changed); !errors.Is(err, state.ErrConfigurationInvalid) {
			test.Fatalf("kind rewritten: %v", err)
		}
		for _, revision := range []int64{0, -1, math.MaxInt64} {
			if _, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, revision, draft); !errors.Is(err, state.ErrConfigurationInvalid) {
				test.Fatalf("bad update revision: %v", err)
			}
			if revision != math.MaxInt64 {
				if err := store.DeleteConfigurationAsset(test.Context(), "alice", created.ID, revision); !errors.Is(err, state.ErrConfigurationInvalid) {
					test.Fatalf("bad delete revision: %v", err)
				}
			} else if err := store.DeleteConfigurationAsset(test.Context(), "alice", created.ID, revision); !errors.Is(err, state.ErrConfigurationConflict) {
				test.Fatalf("bad delete revision: %v", err)
			}
		}
	}
	draft := configurationDraft()
	draft.Payload = state.NewConfigurationPayload(make([]byte, state.MaxConfigurationPayloadBytes))
	if _, err := store.CreateConfigurationAsset(test.Context(), "alice", draft); err != nil {
		test.Fatalf("maximum payload rejected: %v", err)
	}
}

func TestConfigurationAssetConcurrentCASAcrossStores(test *testing.T) {
	store, path, key := configurationFixture(test)
	peer, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peer.Close() })
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	type outcome struct {
		entry   state.ConfigurationAsset
		payload string
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 12)
	var writers sync.WaitGroup
	for index := range 12 {
		writers.Go(func() {
			<-start
			draft := configurationDraft()
			draft.Name = fmt.Sprintf("writer-%d", index)
			payload := fmt.Sprintf("payload-%d-", index) + configurationSentinel
			draft.Payload = state.NewConfigurationPayload([]byte(payload))
			writer := store
			if index%2 == 1 {
				writer = peer
			}
			entry, err := writer.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, draft)
			results <- outcome{entry, payload, err}
		})
	}
	close(start)
	writers.Wait()
	close(results)
	var winner outcome
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result
		} else if !errors.Is(result.err, state.ErrConfigurationConflict) && !errors.Is(result.err, state.ErrConfigurationUnavailable) {
			test.Fatalf("uncontrolled concurrent error: %v", result.err)
		}
	}
	if successes != 1 || winner.entry.Revision != 2 {
		test.Fatalf("concurrent stale writers committed %d versions", successes)
	}
	metadata, err := peer.ConfigurationAsset(test.Context(), "alice", created.ID)
	if err != nil || metadata.Name != winner.entry.Name || metadata.Revision != 2 {
		test.Fatalf("metadata CAS lost: %v", err)
	}
	payload, err := peer.ReadConfigurationAsset(test.Context(), "alice", created.ID)
	if err != nil || string(payload.Reveal()) != winner.payload {
		test.Fatalf("payload/meta transaction split: %v", err)
	}
	if err := peer.DeleteConfigurationAsset(test.Context(), "alice", created.ID, 1); !errors.Is(err, state.ErrConfigurationConflict) {
		test.Fatalf("stale delete crossed new revision: %v", err)
	}
}

func TestConfigurationAssetCapacityIsOwnerScopedAndTransactional(test *testing.T) {
	store, path, key := configurationFixture(test)
	draft := configurationDraft()
	draft.Payload = state.NewConfigurationPayload([]byte{1})
	for range state.MaxConfigurationAssets - 1 {
		if _, err := store.CreateConfigurationAsset(test.Context(), "alice", draft); err != nil {
			test.Fatal(err)
		}
	}
	peer, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peer.Close() })
	start := make(chan struct{})
	results := make(chan error, 8)
	var creators sync.WaitGroup
	for index := range 8 {
		creators.Go(func() {
			<-start
			writer := store
			if index%2 == 1 {
				writer = peer
			}
			_, err := writer.CreateConfigurationAsset(test.Context(), "alice", draft)
			results <- err
		})
	}
	close(start)
	creators.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, state.ErrConfigurationLimit) && !errors.Is(err, state.ErrConfigurationUnavailable) {
			test.Fatalf("uncontrolled capacity error: %v", err)
		}
	}
	if successes != 1 {
		test.Fatalf("capacity admitted %d competing writers", successes)
	}
	listed, err := store.ListConfigurationAssets(test.Context(), "alice")
	if err != nil || len(listed) != state.MaxConfigurationAssets {
		test.Fatalf("capacity list: %d %v", len(listed), err)
	}
	if _, err := store.CreateConfigurationAsset(test.Context(), "alice", draft); !errors.Is(err, state.ErrConfigurationLimit) {
		test.Fatalf("capacity exceeded: %v", err)
	}
	if _, err := peer.CreateConfigurationAsset(test.Context(), "bob", draft); err != nil {
		test.Fatalf("one owner exhausted another owner: %v", err)
	}
	if err := peer.DeleteConfigurationAsset(test.Context(), "alice", listed[0].ID, listed[0].Revision); err != nil {
		test.Fatal(err)
	}
	if _, err := store.CreateConfigurationAsset(test.Context(), "alice", draft); err != nil {
		test.Fatalf("delete did not release capacity: %v", err)
	}
}

func TestConfigurationAssetBackendErrorsAreFixedAndSanitized(test *testing.T) {
	store, _, _ := configurationFixture(test)
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	_, createErr := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	_, listErr := store.ListConfigurationAssets(test.Context(), "alice")
	_, getErr := store.ConfigurationAsset(test.Context(), "alice", created.ID)
	_, readErr := store.ReadConfigurationAsset(test.Context(), "alice", created.ID)
	_, updateErr := store.UpdateConfigurationAsset(test.Context(), "alice", created.ID, 1, configurationDraft())
	deleteErr := store.DeleteConfigurationAsset(test.Context(), "alice", created.ID, 1)
	for _, err := range []error{createErr, listErr, getErr, readErr, updateErr, deleteErr} {
		if err != state.ErrConfigurationUnavailable {
			test.Fatalf("backend detail leaked: %v", err)
		}
	}
}
