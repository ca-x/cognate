package configuration

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func geminiStore(test *testing.T) (*state.Store, string, []byte) {
	test.Helper()
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("synthetic private directory setup failed")
	}
	path := filepath.Join(directory, "gemini.db")
	master := bytes.Repeat([]byte{71}, 32)
	store, err := state.Open(test.Context(), path, master)
	if err != nil {
		test.Fatal("synthetic SQLite setup failed")
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if store.CreateUser(test.Context(), owner) != nil {
			test.Fatal("synthetic owner setup failed")
		}
	}
	return store, path, master
}

func geminiDraft(mode agentconfig.GeminiAuthMode) Draft {
	provider := &GeminiProvider{AuthMode: mode, EnvModel: new("Gemini/env-v1"), SettingsModel: new("Gemini/settings-v2")}
	if mode == agentconfig.GeminiAPIKeyAuth {
		provider.Endpoint = new("https://provider.invalid:443/a%2Fb")
		provider.KeyTransport = new(agentconfig.GeminiBearer)
	}
	return Draft{Name: "  Gemini asset  ", GeminiProvider: provider}
}

func geminiReplacement(test *testing.T, value string) []SecretMutation {
	test.Helper()
	secret := NewSecret([]byte(value))
	test.Cleanup(secret.Clear)
	return []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Replace, Replacement: secret}}
}

func geminiViewCheck(test *testing.T, view EditView, input Draft, present bool, revision int64) {
	test.Helper()
	wantReadiness := CredentialComplete
	if input.GeminiProvider.AuthMode == agentconfig.GeminiAPIKeyAuth && !present {
		wantReadiness = NeedsCredential
	}
	if view.Metadata.AgentID != "gemini" || view.Metadata.Kind != state.ConfigurationKindProvider || view.Metadata.SchemaVersion != 1 || view.Metadata.Revision != revision || view.Provider != (ClaudeProvider{}) || view.CodexProvider != nil || !reflect.DeepEqual(view.GeminiProvider, input.GeminiProvider) || !reflect.DeepEqual(view.Secrets, []SecretStatus{{Slot: GeminiAPIKeySlot, Present: present}}) || view.Readiness != wantReadiness || view.HasPreservedExtras {
		test.Fatal("Gemini typed view, presence, slot or readiness mismatch")
	}
}

func TestGeminiProviderAssetsLifecycle(test *testing.T) {
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiNativeLogin, agentconfig.GeminiAPIKeyAuth} {
		test.Run(string(mode), func(test *testing.T) {
			store, path, master := geminiStore(test)
			service := New(store)
			input := geminiDraft(mode)
			created, err := service.Create(test.Context(), "alice", input, nil)
			if err != nil {
				test.Fatal("Gemini native-login or missing-key asset creation failed")
			}
			geminiViewCheck(test, created, input, false, 1)
			if created.Metadata.Name != "Gemini asset" {
				test.Fatal("asset name normalization changed")
			}
			for _, call := range []func() error{
				func() error { _, err := service.Edit(test.Context(), "bob", created.Metadata.ID); return err },
				func() error { _, err := service.Get(test.Context(), "bob", created.Metadata.ID); return err },
				func() error {
					_, err := service.Update(test.Context(), "bob", created.Metadata.ID, 1, input, nil)
					return err
				},
				func() error {
					_, err := service.Copy(test.Context(), "bob", created.Metadata.ID, 1, "copy")
					return err
				},
				func() error { return service.Delete(test.Context(), "bob", created.Metadata.ID, 1) },
			} {
				if !errors.Is(call(), ErrNotFound) {
					test.Fatal("cross-owner access disclosed an asset")
				}
			}
			revision := int64(1)
			present := mode == agentconfig.GeminiAPIKeyAuth
			if present {
				if desired, err := service.GeminiProviderDesired(test.Context(), "alice", created.Metadata.ID, 1); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(desired, agentconfig.GeminiProviderDesired{}) {
					test.Fatal("missing credential generated planner-ready desired")
				}
				created, err = service.Update(test.Context(), "alice", created.Metadata.ID, revision, input, geminiReplacement(test, "synthetic-gemini-asset #=$\" value"))
				if err != nil {
					test.Fatal("explicit key replacement failed")
				}
				revision++
			}
			kept, err := service.Update(test.Context(), "alice", created.Metadata.ID, revision, input, nil)
			if err != nil {
				test.Fatal("same-revision omitted mutation failed")
			}
			revision++
			geminiViewCheck(test, kept, input, present, revision)
			if view, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, Draft{}, nil); !errors.Is(err, ErrConflict) || view.Metadata.ID != "" {
				test.Fatal("stale revision did not precede draft validation")
			}
			copied, err := service.Copy(test.Context(), "alice", created.Metadata.ID, revision, "Copy")
			if err != nil || copied.Metadata.ID == created.Metadata.ID {
				test.Fatal("owner copy failed or reused source identity")
			}
			geminiViewCheck(test, copied, input, false, 1)
			if err := store.Close(); err != nil {
				test.Fatal("store close failed")
			}
			restarted, err := state.Open(test.Context(), path, master)
			if err != nil {
				test.Fatal("encrypted store restart failed")
			}
			defer restarted.Close()
			service = New(restarted)
			read, err := service.Edit(test.Context(), "alice", created.Metadata.ID)
			if err != nil {
				test.Fatal("restarted typed read failed")
			}
			geminiViewCheck(test, read, input, present, revision)
			files, err := filepath.Glob(path + "*")
			if err != nil || len(files) == 0 {
				test.Fatal("no encrypted SQLite files inspected")
			}
			for _, file := range files {
				content, err := os.ReadFile(file)
				if err != nil || bytes.Contains(content, []byte("synthetic-gemini-asset")) {
					test.Fatal("stored credential was not encrypted")
				}
			}
			if err := service.Delete(test.Context(), "alice", created.Metadata.ID, revision-1); !errors.Is(err, ErrConflict) {
				test.Fatal("stale delete did not conflict")
			}
			if err := service.Delete(test.Context(), "alice", created.Metadata.ID, revision); err != nil {
				test.Fatal("owner CAS delete failed")
			}
		})
	}
}

func TestGeminiProviderMutationsAndClosedBranches(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	for index, mutations := range [][]SecretMutation{
		{{Slot: GeminiAPIKeySlot, Action: Keep}},
		{{Slot: APIKeySlot, Action: Clear}},
		{{Slot: AuthTokenSlot, Action: Clear}},
		{{Slot: CodexProviderKeySlot, Action: Clear}},
		{{Slot: "future.secret", Action: Clear}},
		{{Slot: GeminiAPIKeySlot, Action: "future"}},
		{{Slot: GeminiAPIKeySlot, Action: Clear}, {Slot: GeminiAPIKeySlot, Action: Clear}},
		{{Slot: GeminiAPIKeySlot, Action: Clear, Replacement: NewSecret([]byte("synthetic-invalid-clear"))}},
	} {
		test.Run("invalid-create-"+string(rune('a'+index)), func(test *testing.T) {
			defer func() {
				for index := range mutations {
					mutations[index].Replacement.Clear()
				}
			}()
			if view, err := service.Create(test.Context(), "alice", input, mutations); !errors.Is(err, ErrInvalid) || view.Metadata.ID != "" {
				test.Fatal("invalid mutation was accepted")
			}
		})
	}
	for _, bad := range []Draft{
		{},
		{Name: "mixed", Provider: ClaudeProvider{AuthMode: NativeLogin}, GeminiProvider: input.GeminiProvider},
		{Name: "mixed", CodexProvider: &CodexProvider{}, GeminiProvider: input.GeminiProvider},
	} {
		if view, err := service.Create(test.Context(), "alice", bad, nil); !errors.Is(err, ErrInvalid) || view.Metadata.ID != "" {
			test.Fatal("closed draft classifier accepted an empty or mixed branch")
		}
	}
	for _, value := range []string{"", strings.Repeat("x", 8193), "bad\x00value", "bad\rvalue", "bad\nvalue", "bad\xffvalue"} {
		if view, err := service.Create(test.Context(), "alice", input, geminiReplacement(test, value)); !errors.Is(err, ErrInvalid) || view.Metadata.ID != "" {
			test.Fatal("invalid credential bounds were accepted")
		}
	}
	for _, value := range []string{"x", strings.Repeat("x", 8192), " #=$'\" original "} {
		asset, err := service.Create(test.Context(), "alice", input, geminiReplacement(test, value))
		if err != nil || !geminiKeyEquals(test, store, asset.Metadata.ID, value) {
			test.Fatal("valid credential bytes were not preserved")
		}
		for _, mutation := range [][]SecretMutation{nil, {{Slot: GeminiAPIKeySlot, Action: Keep}}} {
			if view, err := service.Update(test.Context(), "alice", asset.Metadata.ID, 1, geminiDraft(agentconfig.GeminiNativeLogin), mutation); !errors.Is(err, ErrInvalid) || view.Metadata.ID != "" {
				test.Fatal("native-login silently dropped an existing credential")
			}
		}
		if view, err := service.Update(test.Context(), "alice", asset.Metadata.ID, 1, geminiDraft(agentconfig.GeminiNativeLogin), []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Clear}}); err != nil || view.Readiness != CredentialComplete || view.Secrets[0].Present {
			test.Fatal("explicit same-CAS clear transition failed")
		}
	}
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiNativeLogin, agentconfig.GeminiAPIKeyAuth} {
		created, err := service.Create(test.Context(), "alice", geminiDraft(mode), nil)
		if err != nil {
			test.Fatal("branch fixture creation failed")
		}
		for _, bad := range []Draft{{Name: "Claude", Provider: ClaudeProvider{AuthMode: NativeLogin}}, {Name: "Codex", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://example.invalid", Model: "model"}}} {
			if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, bad, nil); !errors.Is(err, ErrInvalid) {
				test.Fatal("Gemini asset changed its immutable agent branch")
			}
		}
	}
	for _, other := range []Draft{{Name: "Claude", Provider: ClaudeProvider{AuthMode: NativeLogin}}, {Name: "Codex", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://example.invalid", Model: "model"}}} {
		created, err := service.Create(test.Context(), "alice", other, nil)
		if err != nil {
			test.Fatal("other-agent fixture failed")
		}
		if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, nil); !errors.Is(err, ErrInvalid) {
			test.Fatal("old asset changed to Gemini")
		}
	}
}

func geminiKeyEquals(test *testing.T, store Store, id, expected string) bool {
	test.Helper()
	_, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", id)
	if err != nil {
		test.Fatal("private credential oracle failed to read")
	}
	_, secret, _, err := decodeGeminiEnvelope(payload)
	defer secret.Clear()
	if err != nil {
		test.Fatal("private credential oracle failed to decode")
	}
	return bytes.Equal(secretBytes(secret), []byte(expected))
}

func TestGeminiProviderBoundsAndPresence(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	for bits := range 32 {
		input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
		if bits&1 == 0 {
			input.GeminiProvider.Endpoint = nil
		}
		if bits&2 == 0 {
			input.GeminiProvider.EnvModel = nil
		}
		if bits&4 == 0 {
			input.GeminiProvider.SettingsModel = nil
		}
		if bits&8 == 0 {
			input.GeminiProvider.KeyTransport = new(agentconfig.GeminiNativeDefault)
		}
		if bits&16 == 0 {
			input.GeminiProvider.AuthMode = agentconfig.GeminiNativeLogin
			input.GeminiProvider.Endpoint = nil
			input.GeminiProvider.KeyTransport = nil
		}
		created, err := service.Create(test.Context(), "alice", input, nil)
		if err != nil {
			test.Fatal("valid presence matrix failed")
		}
		geminiViewCheck(test, created, input, false, 1)
	}
	for index, change := range []func(*GeminiProvider){
		func(provider *GeminiProvider) { provider.AuthMode = "vertex" },
		func(provider *GeminiProvider) { provider.AuthMode = "" },
		func(provider *GeminiProvider) { provider.KeyTransport = nil },
		func(provider *GeminiProvider) { provider.KeyTransport = new(agentconfig.GeminiKeyTransport("")) },
		func(provider *GeminiProvider) { provider.KeyTransport = new(agentconfig.GeminiKeyTransport("header")) },
		func(provider *GeminiProvider) { provider.EnvModel = new("") },
		func(provider *GeminiProvider) { provider.SettingsModel = new(" a") },
		func(provider *GeminiProvider) { provider.EnvModel = new("a b") },
		func(provider *GeminiProvider) { provider.SettingsModel = new("模型") },
		func(provider *GeminiProvider) { provider.EnvModel = new(strings.Repeat("m", 257)) },
		func(provider *GeminiProvider) { provider.Endpoint = new("") },
		func(provider *GeminiProvider) { provider.Endpoint = new("http://127.0.0.1") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://u:p@example.invalid") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid?") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid#") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid:") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid:65536") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid:0") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid/%5c") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid/%0A") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid/%ff") },
		func(provider *GeminiProvider) { provider.Endpoint = new("https://example.invalid/ ") },
		func(provider *GeminiProvider) {
			provider.Endpoint = new("https://example.invalid/" + strings.Repeat("x", 2049))
		},
	} {
		test.Run("invalid-provider-"+string(rune('a'+index)), func(test *testing.T) {
			input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
			change(input.GeminiProvider)
			if view, err := service.Create(test.Context(), "alice", input, nil); !errors.Is(err, ErrInvalid) || view.Metadata.ID != "" {
				test.Fatal("invalid provider accepted or normalized")
			}
		})
	}
	maximum := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	maximum.Name = strings.Repeat("n", 128)
	maximum.GeminiProvider.Endpoint = new("https://example.invalid/" + strings.Repeat("x", 2048-len("https://example.invalid/")))
	maximum.GeminiProvider.EnvModel = new(strings.Repeat("a", 256))
	maximum.GeminiProvider.SettingsModel = new(strings.Repeat("b", 256))
	created, err := service.Create(test.Context(), "alice", maximum, geminiReplacement(test, strings.Repeat("k", 8192)))
	if err != nil {
		test.Fatal("maximum legal provider asset failed")
	}
	geminiViewCheck(test, created, maximum, true, 1)
	for _, name := range []string{"", " ", strings.Repeat("n", 129), "bad\x00name", "bad\xffname"} {
		input := geminiDraft(agentconfig.GeminiNativeLogin)
		input.Name = name
		if _, err := service.Create(test.Context(), "alice", input, nil); !errors.Is(err, ErrInvalid) {
			test.Fatal("name bounds accepted invalid bytes")
		}
	}
}

type geminiCASGate struct {
	arrived chan struct{}
	release chan struct{}
	done    chan struct{}
	first   string
}

type geminiCASStore struct {
	Store
	gate   *geminiCASGate
	role   string
	reads  atomic.Int32
	writes atomic.Int32
}

func (store *geminiCASStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	metadata, payload, err := store.Store.ReadConfigurationAssetVersion(ctx, owner, id)
	if store.reads.Add(1) == 1 {
		store.gate.arrived <- struct{}{}
		select {
		case <-store.gate.release:
		case <-ctx.Done():
			return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ctx.Err()
		}
		if store.gate.first != "" && store.gate.first != store.role {
			select {
			case <-store.gate.done:
			case <-ctx.Done():
				return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ctx.Err()
			}
		}
	}
	return metadata, payload, err
}

func (store *geminiCASStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.writes.Add(1)
	if store.role == store.gate.first {
		defer close(store.gate.done)
	}
	return store.Store.UpdateConfigurationAsset(ctx, owner, id, revision, draft)
}

func TestGeminiProviderCAS(test *testing.T) {
	for _, pair := range [][2]SecretAction{{Keep, Replace}, {Keep, Clear}, {Replace, Clear}} {
		for _, first := range []string{"", "left", "right"} {
			test.Run(string(pair[0])+"-"+string(pair[1])+"/"+first, func(test *testing.T) {
				store, path, master := geminiStore(test)
				other, err := state.Open(test.Context(), path, master)
				if err != nil {
					test.Fatal("second Store open failed")
				}
				defer other.Close()
				input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
				created, err := New(store).Create(test.Context(), "alice", input, geminiReplacement(test, "synthetic-cas-original"))
				if err != nil {
					test.Fatal("CAS asset setup failed")
				}
				gate := &geminiCASGate{arrived: make(chan struct{}, 2), release: make(chan struct{}), done: make(chan struct{}), first: first}
				wrappers := []*geminiCASStore{{Store: store, gate: gate, role: "left"}, {Store: other, gate: gate, role: "right"}}
				type result struct {
					role string
					view EditView
					err  error
				}
				results := make(chan result, 2)
				var workers sync.WaitGroup
				for index, wrapper := range wrappers {
					workers.Go(func() {
						changed := geminiDraft(agentconfig.GeminiAPIKeyAuth)
						changed.Name = wrapper.role
						mutation := SecretMutation{Slot: GeminiAPIKeySlot, Action: pair[index]}
						if mutation.Action == Replace {
							mutation.Replacement = NewSecret([]byte("synthetic-cas-new"))
							defer mutation.Replacement.Clear()
						}
						view, err := New(wrapper).Update(test.Context(), "alice", created.Metadata.ID, 1, changed, []SecretMutation{mutation})
						results <- result{wrapper.role, view, err}
					})
				}
				for range 2 {
					select {
					case <-gate.arrived:
					case <-test.Context().Done():
						test.Fatal("snapshot barrier did not arrive")
					}
				}
				close(gate.release)
				workers.Wait()
				close(results)
				winner := ""
				for result := range results {
					if result.err == nil {
						if winner != "" || result.view.Metadata.Revision != 2 {
							test.Fatal("CAS had multiple winners")
						}
						winner = result.role
					} else if !reflect.DeepEqual(result.view, EditView{}) || !errors.Is(result.err, ErrConflict) && (first != "" || !errors.Is(result.err, ErrUnavailable)) {
						test.Fatal("CAS loser error/view was outside the documented contract")
					}
				}
				if winner == "" || first != "" && winner != first || wrappers[0].reads.Load()+wrappers[1].reads.Load() != 2 || wrappers[0].writes.Load()+wrappers[1].writes.Load() != 2 {
					test.Fatal("CAS retried, rebased or violated ordered scheduling")
				}
				action := pair[0]
				if winner == "right" {
					action = pair[1]
				}
				expected := "synthetic-cas-original"
				if action == Replace {
					expected = "synthetic-cas-new"
				}
				if action == Clear {
					expected = ""
				}
				if !geminiKeyEquals(test, store, created.Metadata.ID, expected) {
					test.Fatal("CAS loser changed credentials or KEEP rebased")
				}
				read, err := New(store).Edit(test.Context(), "alice", created.Metadata.ID)
				if err != nil || read.Metadata.Revision != 2 || read.Metadata.Name != winner {
					test.Fatal("winner metadata did not match the credential snapshot")
				}
			})
		}
	}
}

type geminiObservedStore struct {
	Store
	reads       int
	creates     int
	updates     int
	afterRead   func()
	afterCommit func()
	unknown     bool
}

func (store *geminiObservedStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	store.reads++
	metadata, payload, err := store.Store.ReadConfigurationAssetVersion(ctx, owner, id)
	if store.afterRead != nil {
		store.afterRead()
	}
	return metadata, payload, err
}

func (store *geminiObservedStore) CreateConfigurationAsset(ctx context.Context, owner string, input state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.creates++
	metadata, err := store.Store.CreateConfigurationAsset(ctx, owner, input)
	if err == nil {
		if store.afterCommit != nil {
			store.afterCommit()
		}
		if store.unknown {
			return state.ConfigurationAsset{}, errors.New("synthetic-private-commit-unknown")
		}
	}
	return metadata, err
}

func (store *geminiObservedStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, input state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.updates++
	metadata, err := store.Store.UpdateConfigurationAsset(ctx, owner, id, revision, input)
	if err == nil {
		if store.afterCommit != nil {
			store.afterCommit()
		}
		if store.unknown {
			return state.ConfigurationAsset{}, errors.New("synthetic-private-commit-unknown")
		}
	}
	return metadata, err
}

func TestGeminiProviderSnapshotAndCopy(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	created, err := service.Create(test.Context(), "alice", input, geminiReplacement(test, "synthetic-snapshot-first"))
	if err != nil {
		test.Fatal("snapshot fixture setup failed")
	}
	changed := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	changed.GeminiProvider.SettingsModel = new("revision-two")
	observed := &geminiObservedStore{Store: store, afterRead: func() {
		if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, changed, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Clear}}); err != nil {
			test.Fatal("snapshot barrier writer failed")
		}
	}}
	view, err := New(observed).Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || observed.reads != 1 {
		test.Fatal("atomic edit used more than one snapshot")
	}
	geminiViewCheck(test, view, input, true, 1)
	observed.reads = 0
	observed.afterRead = func() {
		third := geminiDraft(agentconfig.GeminiAPIKeyAuth)
		third.GeminiProvider.SettingsModel = new("revision-three")
		if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 2, third, geminiReplacement(test, "synthetic-snapshot-third")); err != nil {
			test.Fatal("copy snapshot barrier writer failed")
		}
	}
	copied, err := New(observed).Copy(test.Context(), "alice", created.Metadata.ID, 2, "Copy")
	if err != nil || observed.reads != 1 || observed.creates != 1 {
		test.Fatal("copy did not use one source revision and one creation")
	}
	geminiViewCheck(test, copied, changed, false, 1)
	if !geminiKeyEquals(test, store, created.Metadata.ID, "synthetic-snapshot-third") {
		test.Fatal("copy changed the source credential")
	}
	if _, err := store.UpdateConfigurationAsset(test.Context(), "alice", created.Metadata.ID, 3, state.ConfigurationAssetDraft{AgentID: "gemini", Kind: state.ConfigurationKindProvider, Name: "Corrupt future", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{}`))}); err != nil {
		test.Fatal("stale revision fixture failed")
	}
	for _, call := range []func() error{
		func() error {
			_, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 3, "Stale")
			return err
		},
		func() error {
			_, err := service.GeminiProviderDesired(test.Context(), "alice", created.Metadata.ID, 3)
			return err
		},
	} {
		if !errors.Is(call(), ErrConflict) {
			test.Fatal("stale operation decoded the wrong envelope revision")
		}
	}
	listed, err := service.List(test.Context(), "alice")
	if err != nil || len(listed) != 2 {
		test.Fatal("stale copy created another asset")
	}
}

func TestGeminiProviderCancellationAndCommitUnknown(test *testing.T) {
	for _, operation := range []string{"create", "update", "copy"} {
		for _, outcome := range []string{"cancel", "unknown"} {
			test.Run(operation+"/"+outcome, func(test *testing.T) {
				store, _, _ := geminiStore(test)
				input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
				created, err := New(store).Create(test.Context(), "alice", input, geminiReplacement(test, "synthetic-commit-original"))
				if err != nil {
					test.Fatal("commit fixture failed")
				}
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				observed := &geminiObservedStore{Store: store, unknown: outcome == "unknown"}
				want := ErrUnavailable
				if outcome == "cancel" {
					observed.afterCommit = cancel
					want = context.Canceled
				}
				service := New(observed)
				var result EditView
				switch operation {
				case "create":
					result, err = service.Create(ctx, "alice", input, nil)
				case "update":
					result, err = service.Update(ctx, "alice", created.Metadata.ID, 1, input, geminiReplacement(test, "synthetic-commit-updated"))
				case "copy":
					result, err = service.Copy(ctx, "alice", created.Metadata.ID, 1, "Copy")
				}
				if !errors.Is(err, want) || !reflect.DeepEqual(result, EditView{}) || observed.creates+observed.updates != 1 {
					test.Fatal("postcommit result was replayed, exposed or misclassified")
				}
				assets, listErr := New(store).List(test.Context(), "alice")
				wantCount := 2
				if operation == "update" {
					wantCount = 1
				}
				if listErr != nil || len(assets) != wantCount {
					test.Fatal("postcommit cancellation or unknown faked rollback")
				}
				if operation == "update" {
					if !geminiKeyEquals(test, store, created.Metadata.ID, "synthetic-commit-updated") {
						test.Fatal("real COMMIT witness did not persist updated bytes")
					}
				} else if !geminiKeyEquals(test, store, created.Metadata.ID, "synthetic-commit-original") {
					test.Fatal("create/copy changed its source")
				}
			})
		}
	}
	store, _, _ := geminiStore(test)
	input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	asset, err := New(store).Create(test.Context(), "alice", input, nil)
	if err != nil {
		test.Fatal("cancellation fixture failed")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	observed := &geminiObservedStore{Store: store}
	if _, err := New(observed).Create(ctx, "alice", input, nil); !errors.Is(err, context.Canceled) || observed.creates != 0 {
		test.Fatal("pre-canceled create wrote an asset")
	}
	for _, operation := range []string{"edit", "update", "copy", "desired"} {
		test.Run("after-read/"+operation, func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			observed := &geminiObservedStore{Store: store, afterRead: cancel}
			service := New(observed)
			var err error
			switch operation {
			case "edit":
				_, err = service.Edit(ctx, "alice", asset.Metadata.ID)
			case "update":
				_, err = service.Update(ctx, "alice", asset.Metadata.ID, 1, input, nil)
			case "copy":
				_, err = service.Copy(ctx, "alice", asset.Metadata.ID, 1, "Copy")
			case "desired":
				_, err = service.GeminiProviderDesired(ctx, "alice", asset.Metadata.ID, 1)
			}
			if !errors.Is(err, context.Canceled) || observed.creates+observed.updates != 0 {
				test.Fatal("canceled read progressed to a write or successful view")
			}
		})
	}
}

func TestGeminiProviderQuotaMetadataAndClone(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	for index := range state.MaxConfigurationAssets {
		input := geminiDraft(agentconfig.GeminiNativeLogin)
		switch index % 3 {
		case 0:
			input = Draft{Name: "Claude", Provider: ClaudeProvider{AuthMode: NativeLogin}}
		case 1:
			input = Draft{Name: "Codex", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://example.invalid", Model: "model"}}
		}
		if _, err := service.Create(test.Context(), "alice", input, nil); err != nil {
			test.Fatal("cross-agent quota filled too early")
		}
	}
	if view, err := service.Create(test.Context(), "alice", geminiDraft(agentconfig.GeminiNativeLogin), nil); !errors.Is(err, ErrLimit) || view.Metadata.ID != "" {
		test.Fatal("Gemini exceeded the shared 250-asset quota")
	}
	if _, err := service.Create(test.Context(), "bob", geminiDraft(agentconfig.GeminiNativeLogin), nil); err != nil {
		test.Fatal("one owner exhausted another owner's quota")
	}
	listed, err := service.List(test.Context(), "alice")
	if err != nil || len(listed) != state.MaxConfigurationAssets {
		test.Fatal("mixed agent metadata failed")
	}
	if _, err := service.Copy(test.Context(), "alice", listed[0].ID, listed[0].Revision, "Over quota"); !errors.Is(err, ErrLimit) {
		test.Fatal("copy bypassed cross-agent quota")
	}
	for _, scenario := range []struct {
		agent  string
		schema int
	}{{"gemini", 2}, {"future-agent", 1}} {
		asset, err := store.CreateConfigurationAsset(test.Context(), "bob", state.ConfigurationAssetDraft{AgentID: scenario.agent, Kind: state.ConfigurationKindProvider, Name: "Future", SchemaVersion: scenario.schema, Payload: state.NewConfigurationPayload([]byte(`not typed`))})
		if err != nil {
			test.Fatal("future metadata fixture failed")
		}
		if _, err := service.Get(test.Context(), "bob", asset.ID); err != nil {
			test.Fatal("future metadata was not readable")
		}
		mixed, err := service.List(test.Context(), "bob")
		if err != nil || len(mixed) != 2 {
			test.Fatal("unknown metadata broke the mixed Gemini list")
		}
		if _, err := service.Edit(test.Context(), "bob", asset.ID); !errors.Is(err, ErrUnsupported) {
			test.Fatal("future schema or agent became editable")
		}
		if err := service.Delete(test.Context(), "bob", asset.ID, 1); err != nil {
			test.Fatal("future metadata could not be deleted")
		}
	}
	input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	created, err := service.Create(test.Context(), "bob", input, nil)
	if err != nil {
		test.Fatal("clone fixture failed")
	}
	*input.GeminiProvider.SettingsModel = "caller-change"
	if *created.GeminiProvider.SettingsModel == "caller-change" {
		test.Fatal("view aliases caller pointer")
	}
	*created.GeminiProvider.EnvModel = "view-change"
	read, err := service.Edit(test.Context(), "bob", created.Metadata.ID)
	if err != nil || *read.GeminiProvider.EnvModel != "Gemini/env-v1" {
		test.Fatal("view changed the persisted provider")
	}
	for _, revision := range []int64{0, -1, math.MaxInt64} {
		if _, err := service.Update(test.Context(), "bob", created.Metadata.ID, revision, input, nil); !errors.Is(err, ErrInvalid) {
			test.Fatal("invalid/overflowing update revision accepted")
		}
	}
}

func TestGeminiSecretOwnershipAndPrivacy(test *testing.T) {
	current := NewSecret([]byte("synthetic-gemini-current-private"))
	defer current.Clear()
	replacement := NewSecret([]byte("synthetic-gemini-replacement-private"))
	defer replacement.Clear()
	for _, changes := range [][]SecretMutation{nil, {{Slot: GeminiAPIKeySlot, Action: Keep}}, {{Slot: GeminiAPIKeySlot, Action: Replace, Replacement: replacement}}, {{Slot: GeminiAPIKeySlot, Action: Clear}}} {
		merged, err := mergeGeminiSecret(current, changes, false)
		if err != nil {
			test.Fatal("valid owned merge failed")
		}
		merged.Clear()
		if !bytes.Equal(secretBytes(current), []byte("synthetic-gemini-current-private")) || !bytes.Equal(secretBytes(replacement), []byte("synthetic-gemini-replacement-private")) {
			test.Fatal("merge cleanup erased caller-owned bytes")
		}
	}
	for _, changes := range [][]SecretMutation{{{Slot: APIKeySlot, Action: Clear}}, {{Slot: GeminiAPIKeySlot, Action: "bad"}}, {{Slot: GeminiAPIKeySlot, Action: Keep, Replacement: replacement}}, {{Slot: GeminiAPIKeySlot, Action: Clear}, {Slot: GeminiAPIKeySlot, Action: Clear}}} {
		merged, err := mergeGeminiSecret(current, changes, false)
		defer merged.Clear()
		if !errors.Is(err, ErrInvalid) || len(secretBytes(merged)) != 0 || !bytes.Equal(secretBytes(current), []byte("synthetic-gemini-current-private")) {
			test.Fatal("failed merge exposed bytes or cleared input")
		}
	}
	type hidden struct {
		replacement Secret
		nested      map[string][]Secret
	}
	type stripped Secret
	holders := []any{replacement, &replacement, []Secret{replacement}, map[string]Secret{"private": replacement}, hidden{replacement, map[string][]Secret{"private": {replacement}}}, stripped(replacement)}
	var diagnostics bytes.Buffer
	for _, holder := range holders {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%t", "%#08x", "%+020s", "%f", "%p"} {
			fmt.Fprintf(&diagnostics, format, holder)
		}
		encoded, _ := json.Marshal(holder)
		diagnostics.Write(encoded)
		log.New(&diagnostics, "", 0).Print(holder)
		for _, handler := range []slog.Handler{slog.NewTextHandler(&diagnostics, nil), slog.NewJSONHandler(&diagnostics, nil)} {
			slog.New(handler).Info("private fixture", "value", holder)
		}
	}
	for _, marker := range []string{"synthetic-gemini-replacement-private", fmt.Sprintf("%x", secretBytes(replacement)), fmt.Sprint(secretBytes(replacement))} {
		if bytes.Contains(diagnostics.Bytes(), []byte(marker)) {
			test.Fatal("opaque credential escaped a private diagnostic holder")
		}
	}
	store, _, _ := geminiStore(test)
	service := New(store)
	input := geminiDraft(agentconfig.GeminiNativeLogin)
	if _, err := service.Create(test.Context(), "alice", input, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Replace, Replacement: replacement}}); !errors.Is(err, ErrInvalid) {
		test.Fatal("native-login accepted a server credential")
	}
	input = geminiDraft(agentconfig.GeminiAPIKeyAuth)
	asset, err := service.Create(test.Context(), "alice", input, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Replace, Replacement: replacement}})
	if err != nil || !bytes.Equal(secretBytes(replacement), []byte("synthetic-gemini-replacement-private")) {
		test.Fatal("create erased caller mutation storage")
	}
	if _, err := service.Update(test.Context(), "alice", asset.Metadata.ID, 1, input, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Replace, Replacement: replacement}}); err != nil || !bytes.Equal(secretBytes(replacement), []byte("synthetic-gemini-replacement-private")) {
		test.Fatal("update erased caller mutation storage")
	}
}
