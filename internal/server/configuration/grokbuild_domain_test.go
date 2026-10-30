package configuration

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

const grokBuildStoredDraft = `{"version":1,"variant":"grokbuild-provider","provider":{"mode":"custom-model","model":{"profile":"local-profile","upstreamModel":"x-ai/upstream","endpoint":"https://example.invalid/v1","name":"Synthetic Grok","apiBackend":"responses","contextWindow":500000}},"secrets":{}}`

func TestGrokBuildBaselineEnvelope(test *testing.T) {
	store, _, _ := geminiStore(test)
	metadata, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: state.ConfigurationKindProvider, Name: "Grok", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(grokBuildStoredDraft))})
	if err != nil {
		test.Fatal("real SQLite baseline setup", err)
	}
	view, err := New(store).Edit(test.Context(), "alice", metadata.ID)
	if err != nil || view.Metadata != metadata || view.Readiness != NeedsCredential || len(view.Secrets) != 1 || view.Secrets[0].Slot != "grokbuild.api-key" || view.Secrets[0].Present {
		test.Fatalf("valid persisted Grok draft must be editable without a credential: %v", err)
	}
}

func grokBuildDraft() Draft {
	return Draft{Name: " Grok asset ", GrokBuildProvider: &GrokBuildProvider{Mode: "custom-model", Model: &GrokBuildModel{Profile: "本地 profile", UpstreamModel: "x-ai/upstream-v2", Endpoint: "https://ExAmPle.invalid:443/a%2Fb", Name: "提供商", APIBackend: "responses", ContextWindow: 9007199254740991}}}
}

func grokBuildReplacement(test *testing.T, value string) []SecretMutation {
	test.Helper()
	secret := NewSecret([]byte(value))
	test.Cleanup(secret.Clear)
	return []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Replace, Replacement: secret}}
}

func grokBuildCheckView(test *testing.T, view EditView, input Draft, present bool, revision int64) {
	test.Helper()
	ready := CredentialComplete
	if input.GrokBuildProvider.Mode == "custom-model" && !present && input.GrokBuildProvider.Model.EnvKey == nil {
		ready = NeedsCredential
	}
	if view.Metadata.AgentID != "grokbuild" || view.Metadata.Kind != "provider" || view.Metadata.SchemaVersion != 1 || view.Metadata.Revision != revision || view.Metadata.Name != strings.TrimSpace(input.Name) || !reflect.DeepEqual(view.GrokBuildProvider, input.GrokBuildProvider) || view.Provider != (ClaudeProvider{}) || view.CodexProvider != nil || view.GeminiProvider != nil || view.OpenCodeProvider != nil || view.HasPreservedExtras || view.Readiness != ready || !reflect.DeepEqual(view.Secrets, []SecretStatus{{Slot: GrokBuildAPIKeySlot, Present: present}}) {
		test.Fatal("Grok metadata/provider/slot/readiness full parity failed")
	}
}

func grokBuildKeyEquals(test *testing.T, store Store, id, expected string) bool {
	test.Helper()
	_, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", id)
	if err != nil {
		test.Fatal(err)
	}
	_, secret, _, err := decodeGrokBuildEnvelope(payload)
	defer secret.Clear()
	return err == nil && bytes.Equal(secretBytes(secret), []byte(expected))
}

func TestGrokBuildEncryptedLifecycle(test *testing.T) {
	store, path, key := geminiStore(test)
	service := New(store)
	input := grokBuildDraft()
	view, err := service.Create(test.Context(), "alice", input, nil)
	if err != nil {
		test.Fatal(err)
	}
	grokBuildCheckView(test, view, input, false, 1)
	if desired, err := service.GrokBuildProviderDesired(test.Context(), "alice", view.Metadata.ID, 1); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(desired, agentconfig.GrokBuildProviderDesired{}) {
		test.Fatal("incomplete draft obtained a declaration projection")
	}
	input.GrokBuildProvider.Model.Profile = "caller changed"
	read, err := service.Edit(test.Context(), "alice", view.Metadata.ID)
	if err != nil || read.GrokBuildProvider.Model.Profile != "本地 profile" {
		test.Fatal("saved provider retained caller alias")
	}
	input = grokBuildDraft()
	changes := grokBuildReplacement(test, " synthetic-grok-key\t")
	updated, err := service.Update(test.Context(), "alice", view.Metadata.ID, 1, input, changes)
	if err != nil || !grokBuildKeyEquals(test, store, view.Metadata.ID, " synthetic-grok-key\t") || len(secretBytes(changes[0].Replacement)) == 0 {
		test.Fatal("exact replacement or caller ownership failed")
	}
	grokBuildCheckView(test, updated, input, true, 2)
	input.GrokBuildProvider.Model.EnvKey = new("XAI_GROK_NAME")
	updated, err = service.Update(test.Context(), "alice", view.Metadata.ID, 2, input, []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Keep}})
	if err != nil {
		test.Fatal(err)
	}
	grokBuildCheckView(test, updated, input, true, 3)
	for _, call := range []func() error{
		func() error { _, err := service.Edit(test.Context(), "bob", view.Metadata.ID); return err },
		func() error { _, err := service.Copy(test.Context(), "bob", view.Metadata.ID, 3, "other"); return err },
		func() error {
			_, err := service.Update(test.Context(), "bob", view.Metadata.ID, 3, input, nil)
			return err
		},
		func() error { return service.Delete(test.Context(), "bob", view.Metadata.ID, 3) },
		func() error {
			_, err := service.GrokBuildProviderDesired(test.Context(), "bob", view.Metadata.ID, 3)
			return err
		},
	} {
		if !errors.Is(call(), ErrNotFound) {
			test.Fatal("owner isolation failed")
		}
	}
	listed, err := service.List(test.Context(), "bob")
	if err != nil || len(listed) != 0 {
		test.Fatal("metadata crossed owners")
	}
	if metadata, err := service.Get(test.Context(), "alice", view.Metadata.ID); err != nil || metadata != updated.Metadata {
		test.Fatal("metadata read differs from authoritative version")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	reloaded, err := New(reopened).Edit(test.Context(), "alice", view.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	grokBuildCheckView(test, reloaded, input, true, 3)
	if err := New(reopened).Delete(test.Context(), "alice", view.Metadata.ID, 2); !errors.Is(err, ErrConflict) {
		test.Fatal("stale Delete succeeded")
	}
	if err := New(reopened).Delete(test.Context(), "alice", view.Metadata.ID, 3); err != nil {
		test.Fatal(err)
	}
}

func TestGrokBuildCredentialTransitions(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := grokBuildDraft()
	created, err := service.Create(test.Context(), "alice", input, grokBuildReplacement(test, "synthetic-kept"))
	if err != nil {
		test.Fatal(err)
	}
	native := Draft{Name: "Native", GrokBuildProvider: &GrokBuildProvider{Mode: "native-login"}}
	before, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", created.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	for _, mutations := range [][]SecretMutation{nil, {{Slot: GrokBuildAPIKeySlot, Action: Keep}}, grokBuildReplacement(test, "synthetic-new-native")} {
		result, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, native, mutations)
		if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(result, EditView{}) {
			test.Fatal("native retained-key change was not explicitly cleared")
		}
		metadata, unchanged, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", created.Metadata.ID)
		if err != nil || metadata != before || !bytes.Equal(payload.Reveal(), unchanged.Reveal()) {
			test.Fatal("rejected native transition changed stored state")
		}
	}
	cleared, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, native, []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Clear}})
	if err != nil {
		test.Fatal(err)
	}
	grokBuildCheckView(test, cleared, native, false, 2)
	for _, mutations := range [][]SecretMutation{
		{{Slot: APIKeySlot, Action: Clear}},
		{{Slot: GrokBuildAPIKeySlot, Action: Clear}, {Slot: GrokBuildAPIKeySlot, Action: Clear}},
		{{Slot: GrokBuildAPIKeySlot, Action: "future"}},
		{{Slot: GrokBuildAPIKeySlot, Action: Replace}},
		{{Slot: GrokBuildAPIKeySlot, Action: Keep, Replacement: NewSecret([]byte("not-allowed"))}},
		{{Slot: GrokBuildAPIKeySlot, Action: Clear, Replacement: NewSecret([]byte("not-allowed"))}},
	} {
		for index := range mutations {
			defer mutations[index].Replacement.Clear()
		}
		result, err := service.Update(test.Context(), "alice", created.Metadata.ID, 2, input, mutations)
		if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(result, EditView{}) {
			test.Fatal("invalid mutation accepted")
		}
	}
	for _, value := range []string{"", "bad\nkey", "bad\rkey", "bad\x00key", "bad\xff", strings.Repeat("x", 8193)} {
		if _, err := service.Create(test.Context(), "alice", input, grokBuildReplacement(test, value)); !errors.Is(err, ErrInvalid) {
			test.Fatal("invalid replacement accepted")
		}
	}
	if _, err := service.Create(test.Context(), "alice", input, []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Keep}}); !errors.Is(err, ErrInvalid) {
		test.Fatal("create Keep accepted")
	}
	for _, revision := range []int64{0, 1, math.MaxInt64} {
		if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, revision, input, nil); err == nil {
			test.Fatal("invalid/stale revision accepted")
		}
	}
	input.GrokBuildProvider.Model.EnvKey = new("XAI_ENV_NAME")
	updated, err := service.Update(test.Context(), "alice", created.Metadata.ID, 2, input, nil)
	if err != nil {
		test.Fatal(err)
	}
	grokBuildCheckView(test, updated, input, false, 3)
	input.GrokBuildProvider.Model.EnvKey = nil
	updated, err = service.Update(test.Context(), "alice", created.Metadata.ID, 3, input, []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Clear}})
	if err != nil {
		test.Fatal(err)
	}
	grokBuildCheckView(test, updated, input, false, 4)
}

func TestGrokBuildClosedUnion(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	for _, family := range []string{"claude", "codex", "gemini", "opencode"} {
		test.Run(family, func(test *testing.T) {
			input := grokBuildDraft()
			switch family {
			case "claude":
				input.Provider.AuthMode = NativeLogin
			case "codex":
				input.CodexProvider = &CodexProvider{}
			case "gemini":
				input.GeminiProvider = &GeminiProvider{}
			case "opencode":
				input.OpenCodeProvider = &agentconfig.OpenCodeProvider{}
			}
			if view, err := service.Create(test.Context(), "alice", input, nil); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(view, EditView{}) {
				test.Fatal("mixed provider union accepted")
			}
		})
	}
	claude, err := service.Create(test.Context(), "alice", Draft{Name: "Claude", Provider: ClaudeProvider{AuthMode: NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.Update(test.Context(), "alice", claude.Metadata.ID, 1, grokBuildDraft(), nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("cross-agent mutation accepted")
	}
}

func TestGrokBuildCopySnapshot(test *testing.T) {
	for _, shape := range []string{"native", "key", "env", "both"} {
		test.Run(shape, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			service := New(store)
			input := grokBuildDraft()
			mutations := grokBuildReplacement(test, "synthetic-source-only")
			if shape == "native" {
				input.GrokBuildProvider = &GrokBuildProvider{Mode: "native-login"}
				mutations = nil
			}
			if shape == "env" || shape == "both" {
				input.GrokBuildProvider.Model.EnvKey = new("REF_NAME")
			}
			if shape == "env" {
				mutations = nil
			}
			created, err := service.Create(test.Context(), "alice", input, mutations)
			if err != nil {
				test.Fatal(err)
			}
			observed := &geminiObservedStore{Store: store, afterRead: func() {
				changed := Draft{Name: "changed", GrokBuildProvider: cloneGrokBuildProvider(*input.GrokBuildProvider)}
				if changed.GrokBuildProvider.Model != nil {
					changed.GrokBuildProvider.Model.Profile = "next-revision"
				}
				if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, changed, nil); err != nil {
					test.Fatal(err)
				}
			}}
			copyView, err := New(observed).Copy(test.Context(), "alice", created.Metadata.ID, 1, "copy")
			if err != nil || observed.reads != 1 || observed.creates != 1 || !grokBuildKeyEquals(test, store, copyView.Metadata.ID, "") {
				test.Fatal("Copy must use one source snapshot and never copy key")
			}
			input.Name = "copy"
			grokBuildCheckView(test, copyView, input, false, 1)
			if _, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 1, "stale"); !errors.Is(err, ErrConflict) {
				test.Fatal("stale Copy accepted")
			}
		})
	}
}

func TestGrokBuildCASAcrossStores(test *testing.T) {
	for _, first := range []string{"left", "right"} {
		test.Run(first, func(test *testing.T) {
			store, path, key := geminiStore(test)
			created, err := New(store).Create(test.Context(), "alice", grokBuildDraft(), grokBuildReplacement(test, "original"))
			if err != nil {
				test.Fatal(err)
			}
			peer, err := state.Open(test.Context(), path, key)
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
			gate := &geminiCASGate{arrived: make(chan struct{}, 2), release: make(chan struct{}), done: make(chan struct{}), first: first}
			wrappers := []*geminiCASStore{{Store: store, gate: gate, role: "left"}, {Store: peer, gate: gate, role: "right"}}
			type outcome struct {
				role string
				view EditView
				err  error
			}
			results := make(chan outcome, 2)
			var group sync.WaitGroup
			var released sync.Once
			defer func() { cancel(); released.Do(func() { close(gate.release) }); group.Wait() }()
			for _, wrapper := range wrappers {
				group.Go(func() {
					input := grokBuildDraft()
					input.Name = wrapper.role
					changes := []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Clear}}
					if wrapper.role == "left" {
						changes = grokBuildReplacement(test, "left-winner")
					}
					view, err := New(wrapper).Update(ctx, "alice", created.Metadata.ID, 1, input, changes)
					results <- outcome{role: wrapper.role, view: view, err: err}
				})
			}
			for range 2 {
				select {
				case <-gate.arrived:
				case <-ctx.Done():
					test.Fatal("snapshot barrier timed out")
				}
			}
			released.Do(func() { close(gate.release) })
			for range 2 {
				select {
				case result := <-results:
					if result.role == first {
						if result.err != nil || result.view.Metadata.Revision != 2 {
							test.Fatal("scheduled CAS winner failed")
						}
					} else if !errors.Is(result.err, ErrConflict) || !reflect.DeepEqual(result.view, EditView{}) {
						test.Fatal("CAS loser did not conflict with zero view")
					}
				case <-ctx.Done():
					test.Fatal("CAS result timed out")
				}
			}
			group.Wait()
			want := ""
			if first == "left" {
				want = "left-winner"
			}
			if wrappers[0].reads.Load()+wrappers[1].reads.Load() != 2 || wrappers[0].writes.Load()+wrappers[1].writes.Load() != 2 || !grokBuildKeyEquals(test, peer, created.Metadata.ID, want) {
				test.Fatal("CAS retried or lost winner credentials")
			}
		})
	}
}

func TestGrokBuildCancellationAndUnknown(test *testing.T) {
	for _, operation := range []string{"create", "update", "copy"} {
		for _, outcome := range []string{"pre-cancel", "cancel-tail", "unknown"} {
			test.Run(operation+"/"+outcome, func(test *testing.T) {
				store, path, key := geminiStore(test)
				created, err := New(store).Create(test.Context(), "alice", grokBuildDraft(), grokBuildReplacement(test, "before"))
				if err != nil {
					test.Fatal(err)
				}
				peer, err := state.Open(test.Context(), path, key)
				if err != nil {
					test.Fatal(err)
				}
				defer peer.Close()
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				observed := &geminiObservedStore{Store: store, unknown: outcome == "unknown"}
				want := ErrUnavailable
				if outcome != "unknown" {
					want = context.Canceled
				}
				if outcome == "pre-cancel" {
					cancel()
				}
				if outcome == "cancel-tail" {
					observed.afterCommit = cancel
				}
				service := New(observed)
				var view EditView
				switch operation {
				case "create":
					view, err = service.Create(ctx, "alice", grokBuildDraft(), nil)
				case "update":
					view, err = service.Update(ctx, "alice", created.Metadata.ID, 1, grokBuildDraft(), grokBuildReplacement(test, "after"))
				case "copy":
					view, err = service.Copy(ctx, "alice", created.Metadata.ID, 1, "copied")
				}
				attempts := 1
				if outcome == "pre-cancel" {
					attempts = 0
				}
				if !errors.Is(err, want) || !reflect.DeepEqual(view, EditView{}) || observed.creates+observed.updates != attempts {
					test.Fatal("unknown/canceled mutation replayed or exposed success")
				}
				assets, err := peer.ListConfigurationAssets(test.Context(), "alice")
				count := 2
				if operation == "update" || attempts == 0 {
					count = 1
				}
				if err != nil || len(assets) != count {
					test.Fatal("response failure was mistaken for rollback")
				}
				if operation == "update" {
					expected := "after"
					if attempts == 0 {
						expected = "before"
					}
					if !grokBuildKeyEquals(test, peer, created.Metadata.ID, expected) {
						test.Fatal("durable key truth lost")
					}
				}
			})
		}
	}
}

func TestGrokBuildQuotaAndOrder(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	created, err := service.Create(test.Context(), "alice", grokBuildDraft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	copied, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 1, "copy")
	if err != nil {
		test.Fatal(err)
	}
	order, err := service.GetAssetOrder(test.Context(), "alice", "grokbuild", "provider")
	if err != nil || len(order.Assets) != 2 || order.Assets[0].ID != created.Metadata.ID || order.Assets[1].ID != copied.Metadata.ID {
		test.Fatal("create/copy order append failed")
	}
	ordered, err := service.ReplaceAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "grokbuild", Kind: "provider", ExpectedRevision: order.Revision, IDs: []string{copied.Metadata.ID, created.Metadata.ID}})
	if err != nil || ordered.Assets[0].ID != copied.Metadata.ID {
		test.Fatal("Grok complete metadata order CAS failed")
	}
	for _, ids := range [][]string{{created.Metadata.ID}, {created.Metadata.ID, created.Metadata.ID}, {created.Metadata.ID, strings.Repeat("A", 26)}} {
		if _, err := service.ReplaceAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "grokbuild", Kind: "provider", ExpectedRevision: ordered.Revision, IDs: ids}); err == nil {
			test.Fatal("invalid order accepted")
		}
	}
	if _, err := service.ReplaceAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "grokbuild", Kind: "provider", ExpectedRevision: order.Revision, IDs: []string{created.Metadata.ID, copied.Metadata.ID}}); !errors.Is(err, ErrConflict) {
		test.Fatal("stale order accepted")
	}
	if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, grokBuildDraft(), grokBuildReplacement(test, "key")); err != nil {
		test.Fatal(err)
	}
	after, err := service.GetAssetOrder(test.Context(), "alice", "grokbuild", "provider")
	if err != nil || after.Revision != ordered.Revision || after.Assets[0].ID != copied.Metadata.ID {
		test.Fatal("content update reordered partition")
	}
	if err := service.Delete(test.Context(), "alice", copied.Metadata.ID, 1); err != nil {
		test.Fatal(err)
	}
	for index := 1; index < state.MaxConfigurationAssets; index++ {
		input := grokBuildDraft()
		if index%2 == 0 {
			input = Draft{Name: "Claude", Provider: ClaudeProvider{AuthMode: NativeLogin}}
		}
		if _, err := service.Create(test.Context(), "alice", input, nil); err != nil {
			test.Fatal("mixed-owner quota filled early", err)
		}
	}
	if _, err := service.Create(test.Context(), "alice", grokBuildDraft(), nil); !errors.Is(err, ErrLimit) {
		test.Fatal("quota became per partition or exceeded250")
	}
	if _, err := service.Create(test.Context(), "bob", grokBuildDraft(), nil); err != nil {
		test.Fatal("quota crossed owners")
	}
	assets, err := service.List(test.Context(), "alice")
	if err != nil || len(assets) != 250 {
		test.Fatal("limit failure leaked an asset")
	}
}

func TestGrokBuildDraftValidation(test *testing.T) {
	store, _, _ := geminiStore(test)
	observed := &geminiObservedStore{Store: store}
	cases := []struct {
		name   string
		mutate func(*GrokBuildProvider)
		want   error
	}{
		{"model-missing", func(value *GrokBuildProvider) { value.Model = nil }, ErrInvalid},
		{"native-with-model", func(value *GrokBuildProvider) { value.Mode = "native-login" }, ErrInvalid},
		{"unknown-mode", func(value *GrokBuildProvider) { value.Mode = "future" }, ErrUnsupported},
		{"profile-empty", func(value *GrokBuildProvider) { value.Model.Profile = "" }, ErrInvalid},
		{"profile-overflow", func(value *GrokBuildProvider) { value.Model.Profile = strings.Repeat("p", 129) }, ErrInvalid},
		{"upstream-overflow", func(value *GrokBuildProvider) { value.Model.UpstreamModel = strings.Repeat("u", 257) }, ErrInvalid},
		{"name-overflow", func(value *GrokBuildProvider) { value.Model.Name = strings.Repeat("n", 129) }, ErrInvalid},
		{"utf8", func(value *GrokBuildProvider) { value.Model.Name = "bad\xff" }, ErrInvalid},
		{"control", func(value *GrokBuildProvider) { value.Model.Name = "bad\x00" }, ErrInvalid},
		{"edge-space", func(value *GrokBuildProvider) { value.Model.Profile = "p\u3000" }, ErrInvalid},
		{"backend", func(value *GrokBuildProvider) { value.Model.APIBackend = "chat-completions" }, ErrUnsupported},
		{"zero", func(value *GrokBuildProvider) { value.Model.ContextWindow = 0 }, ErrInvalid},
		{"overflow", func(value *GrokBuildProvider) { value.Model.ContextWindow = 9007199254740992 }, ErrInvalid},
		{"negative", func(value *GrokBuildProvider) { value.Model.ContextWindow = -1 }, ErrInvalid},
		{"env-empty", func(value *GrokBuildProvider) { value.Model.EnvKey = new("") }, ErrInvalid},
		{"env-digit", func(value *GrokBuildProvider) { value.Model.EnvKey = new("2KEY") }, ErrInvalid},
		{"env-unicode", func(value *GrokBuildProvider) { value.Model.EnvKey = new("KEY_值") }, ErrInvalid},
		{"env-overflow", func(value *GrokBuildProvider) { value.Model.EnvKey = new(strings.Repeat("K", 129)) }, ErrInvalid},
	}
	for _, current := range cases {
		test.Run(current.name, func(test *testing.T) {
			input := grokBuildDraft()
			current.mutate(input.GrokBuildProvider)
			view, err := New(observed).Create(test.Context(), "alice", input, nil)
			if !errors.Is(err, current.want) || !reflect.DeepEqual(view, EditView{}) {
				test.Fatal("invalid draft validation/classification failed")
			}
		})
	}
	for _, endpoint := range []string{"http://e.invalid/", "https:///path", "https://u:p@e.invalid/", "https://e.invalid?", "https://e.invalid#", "https://e.invalid:", "https://e.invalid:0", "https://e.invalid:65536", "https://e.invalid:word", "https://e.invalid/%00", "https://e.invalid/%5c", "https://e.invalid/%ff", "https://e.invalid/\\", "https://e.invalid/" + strings.Repeat("p", 2031)} {
		input := grokBuildDraft()
		input.GrokBuildProvider.Model.Endpoint = endpoint
		if _, err := New(observed).Create(test.Context(), "alice", input, nil); !errors.Is(err, ErrInvalid) {
			test.Fatal("invalid endpoint accepted")
		}
	}
	if observed.creates != 0 {
		test.Fatal("invalid draft reached Store mutation")
	}
}

type grokBuildMetadataStore struct {
	*state.Store
	reads atomic.Int32
}

func (store *grokBuildMetadataStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	store.reads.Add(1)
	return state.ConfigurationAsset{}, state.ConfigurationPayload{}, errors.New("synthetic-metadata-must-not-decrypt")
}

func TestGrokBuildMetadataNoDecryptAndInUse(test *testing.T) {
	store, _, _ := geminiStore(test)
	asset, err := New(store).Create(test.Context(), "alice", grokBuildDraft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	metadataOnly := &grokBuildMetadataStore{Store: store}
	service := New(metadataOnly)
	if _, err := service.Get(test.Context(), "alice", asset.Metadata.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := service.List(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	order, err := service.GetAssetOrder(test.Context(), "alice", "grokbuild", "provider")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.ReplaceAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "grokbuild", Kind: "provider", ExpectedRevision: order.Revision, IDs: []string{asset.Metadata.ID}}); err != nil {
		test.Fatal(err)
	}
	if metadataOnly.reads.Load() != 0 {
		test.Fatal("metadata/order decrypted a payload")
	}
	reference, err := repository.ParseReference("synthetic/grok-fixture", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("c", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Fixture", Description: "Synthetic", Path: "skills/fixture"}, ContentHash: strings.Repeat("a", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	pack, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Fixture pack", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	bindings, err := store.PackConfigurationAttachments(test.Context(), "alice", pack.ID)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", pack.ID, bindings.PackRevision, bindings.ConfigurationRevision, []state.PackConfigurationReference{{AssetID: asset.Metadata.ID, AssetRevision: 1, TargetID: "grokbuild.provider.user"}}); err != nil {
		test.Fatal(err)
	}
	if err := New(store).Delete(test.Context(), "alice", asset.Metadata.ID, 1); !errors.Is(err, ErrInUse) {
		test.Fatal("existing Pack in-use fence was bypassed")
	}
}

type grokBuildReturnedStore struct {
	Store
	change func(*state.ConfigurationAsset)
}

func (store grokBuildReturnedStore) CreateConfigurationAsset(ctx context.Context, owner string, input state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	metadata, err := store.Store.CreateConfigurationAsset(ctx, owner, input)
	if err == nil {
		store.change(&metadata)
	}
	return metadata, err
}

func TestGrokBuildStoreResultBoundaries(test *testing.T) {
	for _, field := range []string{"id", "agent", "kind", "schema", "revision", "name"} {
		test.Run(field, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			observed := &geminiObservedStore{Store: store}
			returned := grokBuildReturnedStore{Store: observed, change: func(metadata *state.ConfigurationAsset) {
				switch field {
				case "id":
					metadata.ID = "invalid"
				case "agent":
					metadata.AgentID = "claude"
				case "kind":
					metadata.Kind = "mcp"
				case "schema":
					metadata.SchemaVersion = 2
				case "revision":
					metadata.Revision = 2
				case "name":
					metadata.Name = "wrong-returned-name"
				}
			}}
			view, err := New(returned).Create(test.Context(), "alice", grokBuildDraft(), nil)
			if !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(view, EditView{}) || observed.creates != 1 {
				test.Fatal("malformed Store result exposed success or retried")
			}
			assets, err := store.ListConfigurationAssets(test.Context(), "alice")
			if err != nil || len(assets) != 1 || assets[0].Revision != 1 || assets[0].AgentID != "grokbuild" {
				test.Fatal("malformed response implied rollback")
			}
		})
	}
	store, _, _ := geminiStore(test)
	future, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: "provider", Name: "Future schema", SchemaVersion: 2, Payload: state.NewConfigurationPayload([]byte(grokBuildStoredDraft))})
	if err != nil {
		test.Fatal(err)
	}
	service := New(store)
	if _, err := service.Get(test.Context(), "alice", future.ID); err != nil {
		test.Fatal("future metadata hidden")
	}
	if _, err := service.Edit(test.Context(), "alice", future.ID); !errors.Is(err, ErrUnsupported) {
		test.Fatal("future schema promoted to known envelope")
	}
	if _, err := service.Copy(test.Context(), "alice", future.ID, 1, "copy"); !errors.Is(err, ErrUnsupported) {
		test.Fatal("future schema copied destructively")
	}
}
