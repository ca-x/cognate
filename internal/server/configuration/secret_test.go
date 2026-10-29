package configuration

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestConfigurationSecretPrivateContainers(test *testing.T) {
	value := []byte("synthetic-private-credential-测试-913")
	secret := NewSecret(value)
	mutation := SecretMutation{Slot: APIKeySlot, Action: Replace, Replacement: secret}
	type privateHolder struct{ hidden any }
	values := []any{
		secret, &secret, mutation, &mutation,
		credentials{apiKey: secret},
		[]SecretMutation{mutation}, map[string]Secret{"entry": secret},
		privateHolder{hidden: secret}, privateHolder{hidden: mutation},
		struct{ hidden Secret }{hidden: secret},
		struct{ hidden SecretMutation }{hidden: mutation},
		struct{ hidden []SecretMutation }{hidden: []SecretMutation{mutation}},
		struct{ hidden map[string]Secret }{hidden: map[string]Secret{"entry": secret}},
		privateHolder{hidden: privateHolder{hidden: secret}},
	}
	markers := []string{
		string(value), hex.EncodeToString(value), strings.ToUpper(hex.EncodeToString(value)),
		base64.StdEncoding.EncodeToString(value), fmt.Sprint(value),
		strings.Trim(fmt.Sprint(value), "[]"),
	}
	assertPrivate := func(test *testing.T, output string) {
		test.Helper()
		for _, marker := range markers {
			if strings.Contains(output, marker) {
				test.Error("credential exposed by diagnostic encoding")
				return
			}
		}
	}
	formats := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%#x", "% x", "%+q", "%100v", "%.100v"}
	for index, entry := range values {
		test.Run(fmt.Sprintf("case-%02d", index), func(test *testing.T) {
			for _, format := range formats {
				assertPrivate(test, fmt.Sprintf(format, entry))
			}
			encoded, err := json.Marshal(entry)
			if err == nil {
				assertPrivate(test, string(encoded))
			}
			var output bytes.Buffer
			log.New(&output, "", 0).Print(entry)
			assertPrivate(test, output.String())
			for _, handler := range []slog.Handler{slog.NewTextHandler(&output, nil), slog.NewJSONHandler(&output, nil)} {
				output.Reset()
				slog.New(handler).Info("diagnostic", "value", entry)
				assertPrivate(test, output.String())
			}
		})
	}
}

func TestConfigurationServicePreservesCallerSecrets(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal("private fixture setup failed")
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "configuration.db"), bytes.Repeat([]byte{29}, 32))
	if err != nil {
		test.Fatal("store setup failed")
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal("owner setup failed")
	}
	service := New(store)
	for _, agent := range []string{"claude", "codex"} {
		test.Run(agent, func(test *testing.T) {
			input := Draft{Name: "Caller ownership", Provider: ClaudeProvider{AuthMode: APIKey}}
			slot := APIKeySlot
			if agent == "codex" {
				input = Draft{Name: "Caller ownership", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://provider.example/v1", Model: "synthetic-model"}}
				slot = CodexProviderKeySlot
			}
			secret := NewSecret([]byte("synthetic-caller-owned"))
			defer secret.Clear()
			mutations := []SecretMutation{{Slot: slot, Action: Replace, Replacement: secret}}
			created, err := service.Create(test.Context(), "alice", input, mutations)
			if err != nil || created.Readiness != CredentialComplete {
				test.Fatal("create failed")
			}
			updated, err := service.Update(test.Context(), "alice", created.Metadata.ID, created.Metadata.Revision, input, mutations)
			if err != nil || updated.Readiness != CredentialComplete {
				test.Fatal("replace update failed")
			}
			updated, err = service.Update(test.Context(), "alice", updated.Metadata.ID, updated.Metadata.Revision, input, nil)
			if err != nil || updated.Readiness != CredentialComplete {
				test.Fatal("KEEP update failed")
			}
			if _, err := service.Edit(test.Context(), "alice", updated.Metadata.ID); err != nil {
				test.Fatal("edit failed")
			}
			copied, err := service.Copy(test.Context(), "alice", updated.Metadata.ID, updated.Metadata.Revision, "No credentials")
			if err != nil || copied.Readiness != NeedsCredential {
				test.Fatal("copy leaked or rejected credential-free result")
			}
			if _, err := service.Create(test.Context(), "alice", input, append(mutations, mutations...)); err == nil {
				test.Fatal("duplicate replacement accepted")
			}
			if string(secretBytes(secret)) != "synthetic-caller-owned" {
				test.Fatal("service destroyed caller-owned credential")
			}
		})
	}
}

func TestConfigurationSecretOwnership(test *testing.T) {
	test.Run("constructor-and-clone", func(test *testing.T) {
		input := []byte("synthetic-owned-value")
		expected := bytes.Clone(input)
		secret := NewSecret(input)
		defer secret.Clear()
		clear(input)
		if !bytes.Equal(secretBytes(secret), expected) {
			test.Fatal("constructor retained caller memory")
		}
		clone := cloneSecret(secret)
		backing := secretBytes(clone)
		alias := clone
		clone.Clear()
		clone.Clear()
		if len(secretBytes(clone)) != 0 || !bytes.Equal(backing, make([]byte, len(backing))) || !bytes.Equal(secretBytes(alias), backing) {
			test.Fatal("clear did not erase owned memory")
		}
		if !bytes.Equal(secretBytes(secret), expected) {
			test.Fatal("clone clear destroyed original")
		}
		var empty Secret
		empty.Clear()
		var missing *Secret
		missing.Clear()
	})
	for _, action := range []SecretAction{Keep, Clear, Replace} {
		test.Run("merge-"+string(action), func(test *testing.T) {
			current := credentials{apiKey: NewSecret([]byte("synthetic-current-key"))}
			defer current.clear()
			expected := bytes.Clone(secretBytes(current.apiKey))
			mutation := SecretMutation{Slot: APIKeySlot, Action: action}
			if action == Replace {
				mutation.Replacement = NewSecret([]byte("synthetic-replacement-key"))
				defer mutation.Replacement.Clear()
			}
			merged, err := mergeSecrets(current, []SecretMutation{mutation}, false)
			if err != nil {
				test.Fatal("valid merge failed")
			}
			merged.clear()
			if !bytes.Equal(secretBytes(current.apiKey), expected) {
				test.Fatal("merged cleanup destroyed current snapshot")
			}
			if action == Replace && string(secretBytes(mutation.Replacement)) != "synthetic-replacement-key" {
				test.Fatal("merged cleanup destroyed caller replacement")
			}
		})
	}
	test.Run("parallel-read-only-input", func(test *testing.T) {
		current := credentials{apiKey: NewSecret([]byte("synthetic-shared-input"))}
		defer current.clear()
		var workers sync.WaitGroup
		for range 16 {
			workers.Go(func() {
				merged, err := mergeSecrets(current, nil, false)
				if err != nil || string(secretBytes(merged.apiKey)) != "synthetic-shared-input" {
					test.Error("read-only input changed during merge")
				}
				merged.clear()
			})
		}
		workers.Wait()
		if string(secretBytes(current.apiKey)) != "synthetic-shared-input" {
			test.Fatal("callee erased caller-owned input")
		}
	})
}
