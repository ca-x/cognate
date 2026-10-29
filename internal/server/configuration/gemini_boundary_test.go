package configuration

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestGeminiProviderDesiredAndRegistryParity(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("real builtin Registry failed")
	}
	var descriptor agentconfig.Descriptor
	for _, entry := range registry.List() {
		if entry.Manifest.AgentID == "gemini" {
			descriptor = entry
		}
	}
	if descriptor.Manifest.PluginVersion != "1.1.0" || !reflect.DeepEqual(descriptor.Manifest.Capabilities, []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}) {
		test.Fatal("parity did not consume the full Task1 module/host identity")
	}
	var network atomic.Int32
	providerServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		network.Add(1)
		writer.WriteHeader(http.StatusTeapot)
	}))
	defer providerServer.Close()
	for bits := range 32 {
		input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
		input.GeminiProvider.Endpoint = new(providerServer.URL + "/a%2Fb")
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
		mutations := geminiReplacement(test, "synthetic-parity-never-in-js")
		if bits&16 == 0 {
			input.GeminiProvider.AuthMode = agentconfig.GeminiNativeLogin
			input.GeminiProvider.Endpoint = nil
			input.GeminiProvider.KeyTransport = nil
			mutations = nil
		}
		asset, err := service.Create(test.Context(), "alice", input, mutations)
		if err != nil {
			test.Fatal("parity asset failed")
		}
		observed := &geminiObservedStore{Store: store}
		desired, err := New(observed).GeminiProviderDesired(test.Context(), "alice", asset.Metadata.ID, 1)
		if err != nil || observed.reads != 1 || !reflect.DeepEqual(desired, agentconfig.GeminiProviderDesired{AuthMode: input.GeminiProvider.AuthMode, Endpoint: input.GeminiProvider.Endpoint, KeyTransport: input.GeminiProvider.KeyTransport, EnvModel: input.GeminiProvider.EnvModel, SettingsModel: input.GeminiProvider.SettingsModel, KeyPresent: input.GeminiProvider.AuthMode == agentconfig.GeminiAPIKeyAuth}) {
			test.Fatal("desired did not use the actual single authoritative snapshot")
		}
		encoded, err := json.Marshal(desired)
		if err != nil || len(encoded) > agentconfig.MaxGeminiDesiredBytes || bytes.Contains(encoded, []byte("synthetic-parity-never-in-js")) || bytes.Contains(encoded, []byte(asset.Metadata.ID)) || bytes.Contains(encoded, []byte("alice")) {
			test.Fatal("desired contained secret or identity data")
		}
		request := agentconfig.GeminiProviderPlanRequest{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: descriptor.Manifest.Target, Capabilities: descriptor.Manifest.Capabilities, Desired: desired}
		declaration, err := registry.PlanGeminiProvider(test.Context(), request)
		expected := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: "gemini.settings", AuthMode: input.GeminiProvider.AuthMode, Endpoint: input.GeminiProvider.Endpoint, KeyTransport: input.GeminiProvider.KeyTransport, EnvModel: input.GeminiProvider.EnvModel, SettingsModel: input.GeminiProvider.SettingsModel, SelectedType: "oauth-personal", RequiredSlots: []agentconfig.GeminiSecretSlot{}, LocalAuthPolicy: "preserve-local-login-v1"}
		if input.GeminiProvider.AuthMode == agentconfig.GeminiAPIKeyAuth {
			expected.SelectedType = "gemini-api-key"
			expected.CredentialReference = new(agentconfig.GeminiAPIKeySecret)
			expected.RequiredSlots = []agentconfig.GeminiSecretSlot{"gemini.api-key"}
		}
		if err != nil || !reflect.DeepEqual(declaration, expected) {
			test.Fatal("real module declaration differed from the complete persisted provider")
		}
		for _, pair := range [][2]*string{{input.GeminiProvider.Endpoint, desired.Endpoint}, {input.GeminiProvider.EnvModel, desired.EnvModel}, {input.GeminiProvider.SettingsModel, desired.SettingsModel}, {desired.Endpoint, declaration.Endpoint}, {desired.EnvModel, declaration.EnvModel}, {desired.SettingsModel, declaration.SettingsModel}} {
			if pair[0] != nil && pair[0] == pair[1] {
				test.Fatal("desired or declaration retained a mutable input alias")
			}
		}
		if _, err := service.GeminiProviderDesired(test.Context(), "bob", asset.Metadata.ID, 1); !errors.Is(err, ErrNotFound) {
			test.Fatal("desired crossed owners")
		}
		if _, err := service.GeminiProviderDesired(test.Context(), "alice", asset.Metadata.ID, 2); !errors.Is(err, ErrConflict) {
			test.Fatal("desired ignored expected revision")
		}
	}
	input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	asset, err := service.Create(test.Context(), "alice", input, geminiReplacement(test, "synthetic-snapshot-desired"))
	if err != nil {
		test.Fatal("desired snapshot fixture failed")
	}
	observed := &geminiObservedStore{Store: store, afterRead: func() {
		changed := geminiDraft(agentconfig.GeminiAPIKeyAuth)
		changed.GeminiProvider.EnvModel = new("new-revision")
		if _, err := service.Update(test.Context(), "alice", asset.Metadata.ID, 1, changed, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Clear}}); err != nil {
			test.Fatal("desired snapshot interleave failed")
		}
	}}
	desired, err := New(observed).GeminiProviderDesired(test.Context(), "alice", asset.Metadata.ID, 1)
	if err != nil || observed.reads != 1 || !desired.KeyPresent || *desired.EnvModel != "Gemini/env-v1" {
		test.Fatal("desired mixed provider and credential revisions")
	}
	if network.Load() != 0 {
		test.Fatal("asset or planner issued a provider network request")
	}
}

func TestGeminiAssetNativeDenied(test *testing.T) {
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiNativeLogin, agentconfig.GeminiAPIKeyAuth} {
		test.Run(string(mode), func(test *testing.T) {
			fixture := newNativeOrderFixture(test, APIKey)
			input := geminiDraft(mode)
			var changes []SecretMutation
			if mode == agentconfig.GeminiAPIKeyAuth {
				changes = geminiReplacement(test, "synthetic-gemini-native-denied")
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.owner, input, changes)
			if err != nil {
				test.Fatal("Gemini native boundary fixture failed")
			}
			if _, _, err := fixture.domain.load(test.Context(), fixture.owner, asset.Metadata.ID); !errors.Is(err, ErrUnsupported) {
				test.Fatal("native Claude-only loader was widened to Gemini")
			}
			snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
			if err != nil {
				test.Fatal("valid native consumer/proof positive control failed")
			}
			consumer := snapshot.Consumer()
			request := fixture.prepare
			request.AssetID = asset.Metadata.ID
			request.AssetRevision = asset.Metadata.Revision
			transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wirePrepareRequest(request))
			if err != nil {
				test.Fatal("Gemini-targeted proof construction failed")
			}
			signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
			if _, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, request, signature); err != nil {
				test.Fatal("valid Gemini asset binding did not reach business eligibility gate")
			}
			if _, err := fixture.native.Prepare(test.Context(), fixture.bearer, request, signature); !errors.Is(err, ErrUnsupported) {
				test.Fatal("valid proof incorrectly enabled Gemini native materialization")
			}
			database, err := sql.Open("sqlite", fixture.path)
			if err != nil {
				test.Fatal("native row oracle failed")
			}
			defer database.Close()
			for _, query := range []string{"SELECT count(*) FROM native_apply_preparations", "SELECT count(*) FROM native_materialization_grants"} {
				var count int
				if database.QueryRowContext(test.Context(), query).Scan(&count) != nil || count != 0 {
					test.Fatal("Gemini native denial created a preparation or grant")
				}
			}
		})
	}
}

func TestGeminiAssetsHaveNoLocalSideEffects(test *testing.T) {
	home := test.TempDir()
	baseline := map[string][]byte{".gemini/.env": []byte("foreign-content"), ".gemini/settings.json": []byte(`{"foreign":true}`), "selection.json": []byte("selection-baseline"), "current": []byte("current-baseline"), "Pack/manifest.json": []byte(`{"baseline":true}`)}
	for name, content := range baseline {
		path := filepath.Join(home, name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, content, 0600) != nil {
			test.Fatal("synthetic native baseline failed")
		}
	}
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	store, _, _ := geminiStore(test)
	service := New(store)
	input := geminiDraft(agentconfig.GeminiAPIKeyAuth)
	asset, err := service.Create(test.Context(), "alice", input, geminiReplacement(test, "synthetic-no-local-side-effect"))
	if err != nil {
		test.Fatal("side-effect fixture failed")
	}
	if _, err := service.GeminiProviderDesired(test.Context(), "alice", asset.Metadata.ID, 1); err != nil {
		test.Fatal("side-effect desired failed")
	}
	if _, err := service.Edit(test.Context(), "alice", asset.Metadata.ID); err != nil {
		test.Fatal("side-effect edit failed")
	}
	copied, err := service.Copy(test.Context(), "alice", asset.Metadata.ID, 1, "Copy")
	if err != nil {
		test.Fatal("side-effect copy failed")
	}
	if _, err := service.Update(test.Context(), "alice", asset.Metadata.ID, 1, input, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Clear}}); err != nil {
		test.Fatal("side-effect update failed")
	}
	if err := service.Delete(test.Context(), "alice", copied.Metadata.ID, 1); err != nil {
		test.Fatal("side-effect delete failed")
	}
	actual := make(map[string][]byte)
	if err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		actual[name], err = os.ReadFile(path)
		return err
	}); err != nil || !reflect.DeepEqual(actual, baseline) {
		test.Fatal("asset CRUD/desired changed native files, selection, current or Pack")
	}
}
