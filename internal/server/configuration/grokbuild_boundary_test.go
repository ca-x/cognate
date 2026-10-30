package configuration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestGrokBuildDesiredRegistryParity(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	var descriptor agentconfig.Descriptor
	for _, item := range registry.List() {
		if item.Manifest.AgentID == "grokbuild" {
			descriptor = item
		}
	}
	if descriptor.Manifest.PluginID != "builtin.grokbuild" || descriptor.Manifest.PluginVersion != "1.0.0" || descriptor.Manifest.Target != "grokbuild.config" {
		test.Fatal("wrong planner prerequisite identity")
	}
	test.Setenv("GROK_ENV_NAME", "synthetic-never-read-env-value-6d21")
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()
	for _, shape := range []string{"native", "key", "env", "both", "min", "missing"} {
		test.Run(shape, func(test *testing.T) {
			input := grokBuildDraft()
			input.GrokBuildProvider.Model.Endpoint = server.URL + "/a%2Fb"
			mutations := grokBuildReplacement(test, "synthetic-never-js-key-41bc")
			if shape == "native" {
				input.GrokBuildProvider = &GrokBuildProvider{Mode: "native-login"}
				mutations = nil
			}
			if shape == "env" || shape == "both" {
				input.GrokBuildProvider.Model.EnvKey = new("GROK_ENV_NAME")
			}
			if shape == "env" || shape == "missing" {
				mutations = nil
			}
			if shape == "min" {
				input.GrokBuildProvider.Model.ContextWindow = 1
			}
			asset, err := service.Create(test.Context(), "alice", input, mutations)
			if err != nil {
				test.Fatal(err)
			}
			observed := &geminiObservedStore{Store: store}
			desired, err := New(observed).GrokBuildProviderDesired(test.Context(), "alice", asset.Metadata.ID, 1)
			if shape == "missing" {
				if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(desired, agentconfig.GrokBuildProviderDesired{}) {
					test.Fatal("missing credential obtained desired")
				}
				return
			}
			if err != nil || observed.reads != 1 {
				test.Fatal("desired did not consume exactly one authenticated snapshot")
			}
			var expected agentconfig.GrokBuildProviderDeclarationV1
			literal := `{"version":1,"target":"grokbuild.config","mode":"custom-model","model":{"profile":"本地 profile","upstreamModel":"x-ai/upstream-v2","endpoint":"https://example.invalid","name":"提供商","apiBackend":"responses","contextWindow":9007199254740991,"keyPresent":true},"credentialReference":"grokbuild.api-key","requiredSlots":["grokbuild.api-key"],"localAuthPolicy":"preserve-local-login-v1"}`
			if err := json.Unmarshal([]byte(literal), &expected); err != nil {
				test.Fatal(err)
			}
			if shape == "native" {
				expected = agentconfig.GrokBuildProviderDeclarationV1{Version: 1, Target: "grokbuild.config", Mode: "native-login", RequiredSlots: []agentconfig.GrokBuildSecretSlot{}, LocalAuthPolicy: "preserve-local-login-v1"}
			} else {
				expected.Model.Endpoint = server.URL + "/a%2Fb"
			}
			if shape == "env" || shape == "both" {
				expected.Model.EnvKey = new("GROK_ENV_NAME")
			}
			if shape == "env" {
				expected.Model.KeyPresent = false
				expected.CredentialReference = nil
				expected.RequiredSlots = []agentconfig.GrokBuildSecretSlot{}
			}
			if shape == "min" {
				expected.Model.ContextWindow = 1
			}
			request := agentconfig.GrokBuildProviderPlanRequest{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: descriptor.Manifest.Target, Capabilities: descriptor.Manifest.Capabilities, Desired: desired}
			declaration, err := registry.PlanGrokBuildProvider(test.Context(), request)
			if err != nil || !reflect.DeepEqual(declaration, expected) || desired.Mode != expected.Mode || !reflect.DeepEqual(desired.Model, expected.Model) {
				test.Fatal("persisted desired/real moejs declaration full literal mismatch", err)
			}
			encoded, err := json.Marshal(desired)
			if err != nil || bytes.Contains(encoded, []byte("synthetic-never")) || bytes.Contains(encoded, []byte(asset.Metadata.ID)) || bytes.Contains(encoded, []byte("alice")) {
				test.Fatal("desired included credential or identity")
			}
			if desired.Model != nil {
				desired.Model.Profile = "changed"
				if desired.Model.EnvKey != nil {
					*desired.Model.EnvKey = "CHANGED"
				}
				read, err := service.Edit(test.Context(), "alice", asset.Metadata.ID)
				if err != nil || read.GrokBuildProvider.Model.Profile != "本地 profile" {
					test.Fatal("desired retained persisted alias")
				}
			}
			if value, err := service.GrokBuildProviderDesired(test.Context(), "alice", asset.Metadata.ID, 2); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(value, agentconfig.GrokBuildProviderDesired{}) {
				test.Fatal("stale desired accepted")
			}
		})
	}
	if requests.Load() != 0 {
		test.Fatal("planner/CRUD contacted provider")
	}
}

func TestGrokBuildDesiredSingleSnapshot(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	input := grokBuildDraft()
	input.GrokBuildProvider.Model.EnvKey = new("OLD_REF")
	created, err := service.Create(test.Context(), "alice", input, grokBuildReplacement(test, "old-key"))
	if err != nil {
		test.Fatal(err)
	}
	observed := &geminiObservedStore{Store: store, afterRead: func() {
		changed := grokBuildDraft()
		changed.GrokBuildProvider.Model.Profile = "new-profile"
		if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, changed, []SecretMutation{{Slot: GrokBuildAPIKeySlot, Action: Clear}}); err != nil {
			test.Fatal(err)
		}
	}}
	desired, err := New(observed).GrokBuildProviderDesired(test.Context(), "alice", created.Metadata.ID, 1)
	if err != nil || observed.reads != 1 || desired.Model == nil || !desired.Model.KeyPresent || desired.Model.Profile != "本地 profile" || *desired.Model.EnvKey != "OLD_REF" {
		test.Fatal("desired mixed credential/model revisions")
	}
}

func TestGrokBuildDesiredCancelAndJSShape(test *testing.T) {
	store, _, _ := geminiStore(test)
	input := grokBuildDraft()
	input.GrokBuildProvider.Model.EnvKey = new("GROK_NAME")
	asset, err := New(store).Create(test.Context(), "alice", input, grokBuildReplacement(test, "synthetic-not-in-js-631b"))
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	observed := &geminiObservedStore{Store: store, afterRead: cancel}
	if result, err := New(observed).GrokBuildProviderDesired(ctx, "alice", asset.Metadata.ID, 1); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, agentconfig.GrokBuildProviderDesired{}) {
		test.Fatal("canceled snapshot exposed desired")
	}
	desired, err := New(store).GrokBuildProviderDesired(test.Context(), "alice", asset.Metadata.ID, 1)
	if err != nil {
		test.Fatal(err)
	}
	module := `export function planGrokBuildProvider(input){
if(typeof process!=="undefined"||typeof require!=="undefined"||typeof fetch!=="undefined")throw Error("ambient capability");
if(Object.keys(input).sort().join(",")!=="mode,model"||Object.keys(input.model).sort().join(",")!=="apiBackend,contextWindow,endpoint,envKey,keyPresent,name,profile,upstreamModel")throw Error("extra input");
if(input.model.envKey!=="GROK_NAME"||input.model.keyPresent!==true)throw Error("presence/name not preserved");
return {version:1,target:"grokbuild.config",mode:input.mode,model:{...input.model},requiredSlots:["grokbuild.api-key"],credentialReference:"grokbuild.api-key",localAuthPolicy:"preserve-local-login-v1"};}`
	definition := agentadapter.Definition{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "fixture.grok-domain", PluginVersion: "1.0.0", AgentID: "grokbuild", Target: "grokbuild.config", Format: "toml", Capabilities: []agentconfig.Capability{"grokbuild.provider-plan.v1"}}, Module: module}
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	identity := registry.List()[0]
	declaration, err := registry.PlanGrokBuildProvider(test.Context(), agentconfig.GrokBuildProviderPlanRequest{APIVersion: 1, PluginID: identity.Manifest.PluginID, PluginVersion: "1.0.0", DefinitionDigest: identity.DefinitionDigest, Target: "grokbuild.config", Capabilities: identity.Manifest.Capabilities, Desired: desired})
	if err != nil || !reflect.DeepEqual(declaration.Model, desired.Model) {
		test.Fatal("real third-party input witness failed", err)
	}
	encoded, err := json.Marshal(declaration)
	if err != nil || strings.Contains(string(encoded), "synthetic-not-in-js-631b") {
		test.Fatal("secret leaked across module boundary")
	}
}

func TestGrokBuildNativeDeniedAndNoFiles(test *testing.T) {
	fixture := newNativeOrderFixture(test, APIKey)
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, grokBuildDraft(), grokBuildReplacement(test, "synthetic-native-denied"))
	if err != nil {
		test.Fatal(err)
	}
	if _, _, err := fixture.domain.load(test.Context(), fixture.owner, asset.Metadata.ID); !errors.Is(err, ErrUnsupported) {
		test.Fatal("Claude-only loader widened")
	}
	snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal("valid original proof failed")
	}
	consumer := snapshot.Consumer()
	request := fixture.prepare
	request.AssetID = asset.Metadata.ID
	request.AssetRevision = 1
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wirePrepareRequest(request))
	if err != nil {
		test.Fatal(err)
	}
	signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
	if _, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, request, signature); err != nil {
		test.Fatal("valid Grok asset proof did not reach eligibility gate")
	}
	if _, err := fixture.native.Prepare(test.Context(), fixture.bearer, request, signature); !errors.Is(err, ErrUnsupported) {
		test.Fatal("Grok native grant enabled")
	}
	database, err := sql.Open("sqlite", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, query := range []string{"SELECT count(*) FROM native_apply_preparations", "SELECT count(*) FROM native_materialization_grants"} {
		var count int
		if database.QueryRowContext(test.Context(), query).Scan(&count) != nil || count != 0 {
			test.Fatal("native denial wrote preparation/grant")
		}
	}
	home := test.TempDir()
	test.Setenv("HOME", home)
	path := filepath.Join(home, "grok-config.toml")
	baseline := []byte("foreign-native-content")
	if err := os.WriteFile(path, baseline, 0600); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.domain.Update(test.Context(), fixture.owner, asset.Metadata.ID, 1, grokBuildDraft(), nil); err != nil {
		test.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, baseline) {
		test.Fatal("domain mutated native filesystem")
	}
}
