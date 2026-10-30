package configuration

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func openCodeDraft(format string) Draft {
	provider := &agentconfig.OpenCodeProvider{ProviderID: "same-provider", Format: format}
	if format == "v1" {
		provider.V1 = &agentconfig.OpenCodeV1{Name: new("Local provider"), Options: &agentconfig.OpenCodeV1Options{APIKey: new("opencode.api-key"), BaseURL: new("https://synthetic.invalid/a%2Fb")}}
	} else {
		provider.V2 = &agentconfig.OpenCodeV2{Package: new("@ai-sdk/openai-compatible"), Settings: &agentconfig.OpenCodeSettings{Timeout: &agentconfig.OpenCodeTimeout{Disabled: new(false)}}, Body: new(map[string]agentconfig.OpenCodeValue{"apiKey": {Reference: new("opencode.api-key")}})}
	}
	return Draft{Name: "  OpenCode alternative  ", OpenCodeProvider: provider}
}

func TestOpenCodeProviderCopySingleSnapshot(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := openCodeDraft("v1")
	asset, err := service.Create(test.Context(), "alice", input, openCodeReplacement(test, "opencode.api-key"))
	if err != nil {
		test.Fatal("copy snapshot fixture failed")
	}
	observed := &geminiObservedStore{Store: store}
	observed.afterRead = func() {
		updated := openCodeDraft("v2")
		updated.OpenCodeProvider.ProviderID = "changed-live-provider"
		if _, err := service.Update(test.Context(), "alice", asset.Metadata.ID, 1, updated, nil); err != nil {
			test.Fatal("concurrent source update failed")
		}
	}
	copied, err := New(observed).Copy(test.Context(), "alice", asset.Metadata.ID, 1, "snapshot-copy")
	if err != nil || observed.reads != 1 || observed.creates != 1 || copied.Readiness != NeedsCredential || !reflect.DeepEqual(copied.OpenCodeProvider, input.OpenCodeProvider) {
		test.Fatal("copy reread or mixed source snapshots")
	}
	if _, err := service.Copy(test.Context(), "alice", asset.Metadata.ID, 1, "stale-copy"); !errors.Is(err, ErrConflict) {
		test.Fatal("copy accepted stale source revision")
	}
}

func TestOpenCodeProviderQuotaAndFutureMetadata(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	for index := range state.MaxConfigurationAssets {
		if index == 0 {
			if _, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "future-agent", Kind: state.ConfigurationKindProvider, Name: "future", SchemaVersion: 2, Payload: state.NewConfigurationPayload([]byte(`{"future":true}`))}); err != nil {
				test.Fatal("future metadata fixture failed")
			}
			continue
		}
		if _, err := service.Create(test.Context(), "alice", openCodeDraft("v1"), nil); err != nil {
			test.Fatal("valid owner quota slot failed")
		}
	}
	assets, err := service.List(test.Context(), "alice")
	if err != nil || len(assets) != state.MaxConfigurationAssets {
		test.Fatal("metadata list lost future or OpenCode assets")
	}
	if _, err := service.Create(test.Context(), "alice", openCodeDraft("v2"), nil); !errors.Is(err, ErrLimit) {
		test.Fatal("OpenCode bypassed shared owner quota")
	}
	var source state.ConfigurationAsset
	for _, asset := range assets {
		if asset.AgentID == "opencode" {
			source = asset
			break
		}
	}
	if source.ID == "" {
		test.Fatal("quota fixture has no OpenCode source")
	}
	if _, err := service.Copy(test.Context(), "alice", source.ID, source.Revision, "over-quota"); !errors.Is(err, ErrLimit) {
		test.Fatal("OpenCode copy bypassed shared owner quota")
	}
	if _, err := service.Create(test.Context(), "bob", openCodeDraft("v2"), nil); err != nil {
		test.Fatal("quota crossed owner boundary")
	}
	for _, asset := range assets {
		if asset.AgentID != "future-agent" {
			continue
		}
		if _, err := service.Get(test.Context(), "alice", asset.ID); err != nil {
			test.Fatal("future metadata get blocked")
		}
		if _, err := service.Edit(test.Context(), "alice", asset.ID); !errors.Is(err, ErrUnsupported) {
			test.Fatal("unknown future payload editable")
		}
		if err := service.Delete(test.Context(), "alice", asset.ID, 1); err != nil {
			test.Fatal("future metadata deletion blocked")
		}
	}
}

type openCodeViewTailContext struct {
	context.Context
	cancel context.CancelFunc
}

func (ctx *openCodeViewTailContext) Err() error {
	programCounter, _, _, ok := runtime.Caller(1)
	if ok {
		caller := runtime.FuncForPC(programCounter)
		if caller != nil && strings.HasSuffix(caller.Name(), ".openCodeView") {
			ctx.cancel()
		}
	}
	return ctx.Context.Err()
}

func TestOpenCodeProviderCommittedViewTailCancellation(test *testing.T) {
	store, _, _ := geminiStore(test)
	parent, cancel := context.WithCancel(test.Context())
	defer cancel()
	ctx := &openCodeViewTailContext{Context: parent, cancel: cancel}
	view, err := New(store).Create(ctx, "alice", openCodeDraft("v2"), nil)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(view, EditView{}) {
		test.Fatal("committed final view escaped cancellation gate")
	}
	assets, err := New(store).List(test.Context(), "alice")
	if err != nil || len(assets) != 1 {
		test.Fatal("return-tail cancellation incorrectly implied rollback")
	}
}

func TestOpenCodeProviderSecretTransitions(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := openCodeDraft("v1")
	for _, mutations := range [][]SecretMutation{
		{{Slot: "opencode.api-key", Action: Keep}},
		{{Slot: "claude.api-key", Action: Clear}},
		{{Slot: "opencode.api-key", Action: Clear}, {Slot: "opencode.api-key", Action: Clear}},
		openCodeReplacement(test, "opencode.value.01"),
	} {
		if view, err := service.Create(test.Context(), "alice", input, mutations); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(view, EditView{}) {
			test.Fatal("invalid create secret mutation accepted")
		}
	}
	created, err := service.Create(test.Context(), "alice", input, openCodeReplacement(test, "opencode.api-key"))
	if err != nil {
		test.Fatal("transition fixture failed")
	}
	input.OpenCodeProvider.V1.Options.APIKey = nil
	if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("removed live credential without explicit CLEAR")
	}
	if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, []SecretMutation{{Slot: "opencode.api-key", Action: Keep}}); !errors.Is(err, ErrInvalid) {
		test.Fatal("KEEP retained unreferenced credential")
	}
	updated, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, []SecretMutation{{Slot: "opencode.api-key", Action: Clear}})
	if err != nil || len(updated.Secrets) != 0 || updated.Readiness != CredentialComplete {
		test.Fatal("explicit credential removal failed")
	}
	for name, mutate := range map[string]func(*Draft){
		"claude": func(draft *Draft) { draft.Provider.AuthMode = NativeLogin },
		"codex":  func(draft *Draft) { draft.CodexProvider = &CodexProvider{} },
		"gemini": func(draft *Draft) { draft.GeminiProvider = &GeminiProvider{} },
	} {
		test.Run(name, func(test *testing.T) {
			mixed := openCodeDraft("v2")
			mutate(&mixed)
			if _, err := service.Create(test.Context(), "alice", mixed, nil); !errors.Is(err, ErrInvalid) {
				test.Fatal("mixed provider input accepted")
			}
		})
	}
}

func TestOpenCodeProviderSecretBudget(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := openCodeDraft("v2")
	body := make(map[string]agentconfig.OpenCodeValue)
	mutations := make([]SecretMutation, 0, 9)
	for index, slot := range []string{"opencode.value.01", "opencode.value.02", "opencode.value.03", "opencode.value.04", "opencode.value.05", "opencode.value.06", "opencode.value.07", "opencode.value.08", "opencode.value.09"} {
		body[slot] = agentconfig.OpenCodeValue{Reference: new(slot)}
		value := bytes.Repeat([]byte{'x'}, 8192)
		if index == 8 {
			value = []byte{'x'}
		}
		secret := NewSecret(value)
		test.Cleanup(secret.Clear)
		clear(value)
		mutations = append(mutations, SecretMutation{Slot: Slot(slot), Action: Replace, Replacement: secret})
	}
	input.OpenCodeProvider.V2.Body = &body
	if _, err := service.Create(test.Context(), "alice", input, mutations); !errors.Is(err, ErrInvalid) {
		test.Fatal("aggregate secret budget bypassed")
	}
	created, err := service.Create(test.Context(), "alice", input, mutations[:8])
	if err != nil || created.Readiness != NeedsCredential || len(created.Secrets) != 9 {
		test.Fatal("exact secret budget rejected or missing ref hidden")
	}
	if len(secretBytes(mutations[0].Replacement)) != 8192 || secretBytes(mutations[0].Replacement)[0] != 'x' {
		test.Fatal("borrowed caller secret was cleared")
	}
}

func TestOpenCodeProviderCancellationAndCommitUnknown(test *testing.T) {
	for _, operation := range []string{"create", "update", "copy"} {
		for _, outcome := range []string{"cancel", "unknown"} {
			test.Run(operation+"/"+outcome, func(test *testing.T) {
				store, _, _ := geminiStore(test)
				input := openCodeDraft("v2")
				asset, err := New(store).Create(test.Context(), "alice", input, nil)
				if err != nil {
					test.Fatal("unknown fixture failed")
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
					result, err = service.Update(ctx, "alice", asset.Metadata.ID, 1, input, openCodeReplacement(test, "opencode.api-key"))
				case "copy":
					result, err = service.Copy(ctx, "alice", asset.Metadata.ID, 1, "copy")
				}
				if !errors.Is(err, want) || !reflect.DeepEqual(result, EditView{}) || observed.creates+observed.updates != 1 {
					test.Fatal("commit tail leaked result, retried or misclassified")
				}
				assets, err := New(store).List(test.Context(), "alice")
				wantCount := 2
				if operation == "update" {
					wantCount = 1
				}
				if err != nil || len(assets) != wantCount {
					test.Fatal("unknown outcome incorrectly implied rollback")
				}
				if operation == "update" {
					view, err := New(store).Edit(test.Context(), "alice", asset.Metadata.ID)
					if err != nil || view.Metadata.Revision != 2 || view.Readiness != CredentialComplete {
						test.Fatal("COMMIT not actually durable")
					}
				}
			})
		}
	}
	for _, operation := range []string{"edit", "update", "copy", "desired"} {
		test.Run("after-read/"+operation, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			input := openCodeDraft("v1")
			asset, err := New(store).Create(test.Context(), "alice", input, nil)
			if err != nil {
				test.Fatal("cancel fixture failed")
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			observed := &geminiObservedStore{Store: store, afterRead: cancel}
			service := New(observed)
			switch operation {
			case "edit":
				_, err = service.Edit(ctx, "alice", asset.Metadata.ID)
			case "update":
				_, err = service.Update(ctx, "alice", asset.Metadata.ID, 1, input, nil)
			case "copy":
				_, err = service.Copy(ctx, "alice", asset.Metadata.ID, 1, "copy")
			case "desired":
				_, err = service.OpenCodeProviderDesired(ctx, "alice", asset.Metadata.ID, 1)
			}
			if !errors.Is(err, context.Canceled) || observed.creates+observed.updates != 0 {
				test.Fatal("canceled snapshot advanced")
			}
		})
	}
}

func TestOpenCodeProviderCAS(test *testing.T) {
	for _, pair := range [][2]SecretAction{{Keep, Replace}, {Keep, Clear}, {Replace, Clear}} {
		for _, first := range []string{"left", "right"} {
			test.Run(string(pair[0])+"-"+string(pair[1])+"/"+first, func(test *testing.T) {
				store, path, master := geminiStore(test)
				other, err := state.Open(test.Context(), path, master)
				if err != nil {
					test.Fatal("second SQLite connection failed")
				}
				defer other.Close()
				input := openCodeDraft("v1")
				asset, err := New(store).Create(test.Context(), "alice", input, openCodeReplacement(test, "opencode.api-key"))
				if err != nil {
					test.Fatal("CAS fixture failed")
				}
				gate := &geminiCASGate{arrived: make(chan struct{}, 2), release: make(chan struct{}), done: make(chan struct{}), first: first}
				wrappers := []*geminiCASStore{{Store: store, gate: gate, role: "left"}, {Store: other, gate: gate, role: "right"}}
				type outcome struct {
					view   EditView
					err    error
					action SecretAction
				}
				results := make(chan outcome, 2)
				var workers sync.WaitGroup
				for index, wrapper := range wrappers {
					workers.Go(func() {
						changed := openCodeDraft("v1")
						changed.Name = wrapper.role
						mutation := SecretMutation{Slot: "opencode.api-key", Action: pair[index]}
						if mutation.Action == Replace {
							mutation.Replacement = NewSecret([]byte("synthetic-CAS-replacement"))
							defer mutation.Replacement.Clear()
						}
						view, err := New(wrapper).Update(test.Context(), "alice", asset.Metadata.ID, 1, changed, []SecretMutation{mutation})
						results <- outcome{view, err, mutation.Action}
					})
				}
				for range 2 {
					select {
					case <-gate.arrived:
					case <-test.Context().Done():
						test.Fatal("CAS barrier missing")
					}
				}
				close(gate.release)
				workers.Wait()
				close(results)
				wins := 0
				winnerAction := Keep
				for result := range results {
					if result.err == nil {
						wins++
						winnerAction = result.action
						if result.view.Metadata.Name != first || result.view.Metadata.Revision != 2 {
							test.Fatal("ordered CAS winner mismatch")
						}
					} else if !errors.Is(result.err, ErrConflict) || !reflect.DeepEqual(result.view, EditView{}) {
						test.Fatal("CAS loser result mismatch")
					}
				}
				if wins != 1 || wrappers[0].reads.Load()+wrappers[1].reads.Load() != 2 || wrappers[0].writes.Load()+wrappers[1].writes.Load() != 2 {
					test.Fatal("CAS retried or had multiple winners")
				}
				_, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", asset.Metadata.ID)
				if err != nil {
					test.Fatal("CAS durable oracle failed")
				}
				_, secrets, _, err := decodeOpenCodeEnvelope(payload)
				defer secrets.clear()
				want := "synthetic-private-opencode"
				if winnerAction == Replace {
					want = "synthetic-CAS-replacement"
				}
				if winnerAction == Clear {
					want = ""
				}
				if err != nil || string(secretBytes(secrets["opencode.api-key"])) != want {
					test.Fatal("losing CAS changed credentials")
				}
			})
		}
	}
}

func TestOpenCodeProviderStorageReopenAndPrivacy(test *testing.T) {
	store, path, master := geminiStore(test)
	input := openCodeDraft("v2")
	asset, err := New(store).Create(test.Context(), "alice", input, openCodeReplacement(test, "opencode.api-key"))
	if err != nil {
		test.Fatal("reopen fixture failed")
	}
	if err := store.Close(); err != nil {
		test.Fatal("close failed")
	}
	other, err := state.Open(test.Context(), path, master)
	if err != nil {
		test.Fatal("reopen failed")
	}
	defer other.Close()
	view, err := New(other).Edit(test.Context(), "alice", asset.Metadata.ID)
	if err != nil || view.Readiness != CredentialComplete || !reflect.DeepEqual(view.OpenCodeProvider, input.OpenCodeProvider) {
		test.Fatal("encrypted envelope did not survive reopen")
	}
	encoded, err := json.Marshal(view)
	if err != nil || bytes.Contains(encoded, []byte("synthetic-private-opencode")) {
		test.Fatal("read view exposed a credential")
	}
	_, payload, err := other.ReadConfigurationAssetVersion(test.Context(), "alice", asset.Metadata.ID)
	if err != nil {
		test.Fatal("durable credential oracle failed")
	}
	_, secrets, _, err := decodeOpenCodeEnvelope(payload)
	defer secrets.clear()
	if err != nil || string(secretBytes(secrets["opencode.api-key"])) != "synthetic-private-opencode" {
		test.Fatal("credential not stored durably")
	}
}

func openCodeReplacement(test *testing.T, slot string) []SecretMutation {
	test.Helper()
	secret := NewSecret([]byte("synthetic-private-opencode"))
	test.Cleanup(secret.Clear)
	return []SecretMutation{{Slot: Slot(slot), Action: Replace, Replacement: secret}}
}

func TestOpenCodeProviderAssets(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			service := New(store)
			input := openCodeDraft(format)
			created, err := service.Create(test.Context(), "alice", input, openCodeReplacement(test, "opencode.api-key"))
			if err != nil {
				test.Fatal("OpenCode create failed")
			}
			if created.Metadata.AgentID != "opencode" || created.Metadata.Revision != 1 || created.Metadata.Name != "OpenCode alternative" || created.Readiness != CredentialComplete || !reflect.DeepEqual(created.OpenCodeProvider, input.OpenCodeProvider) {
				test.Fatal("create lost definition or presence")
			}
			if created.Provider != (ClaudeProvider{}) || created.CodexProvider != nil || created.GeminiProvider != nil || !reflect.DeepEqual(created.Secrets, []SecretStatus{{Slot: "opencode.api-key", Present: true}}) {
				test.Fatal("mixed provider output")
			}
			if _, err := service.Edit(test.Context(), "bob", created.Metadata.ID); !errors.Is(err, ErrNotFound) {
				test.Fatal("owner isolation failed")
			}
			edited, err := service.Edit(test.Context(), "alice", created.Metadata.ID)
			if err != nil || !reflect.DeepEqual(edited, created) {
				test.Fatal("edit lost definition")
			}
			copied, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 1, "Alternative copy")
			if err != nil || copied.Metadata.ID == created.Metadata.ID || copied.Readiness != NeedsCredential || !reflect.DeepEqual(copied.OpenCodeProvider, created.OpenCodeProvider) || !reflect.DeepEqual(copied.Secrets, []SecretStatus{{Slot: "opencode.api-key", Present: false}}) {
				test.Fatal("copy lost definition or copied credential")
			}
			updated, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, []SecretMutation{{Slot: "opencode.api-key", Action: Clear}})
			if err != nil || updated.Metadata.Revision != 2 || updated.Readiness != NeedsCredential {
				test.Fatal("clear failed")
			}
			if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, nil); !errors.Is(err, ErrConflict) {
				test.Fatal("stale update accepted")
			}
			if _, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 1, "stale"); !errors.Is(err, ErrConflict) {
				test.Fatal("stale copy accepted")
			}
			metadata, err := service.List(test.Context(), "alice")
			if err != nil || len(metadata) != 2 {
				test.Fatal("coexisting provider alternatives missing")
			}
			if err := service.Delete(test.Context(), "alice", created.Metadata.ID, 2); err != nil {
				test.Fatal("delete failed")
			}
		})
	}
}
