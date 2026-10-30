package configuration

import (
	"crypto/ed25519"
	"database/sql"
	"errors"
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

func TestOpenCodeProviderDesiredAndRegistryParity(test *testing.T) {
	store, _, _ := geminiStore(test)
	service := New(store)
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("real builtin Registry failed")
	}
	var descriptor agentconfig.Descriptor
	for _, entry := range registry.List() {
		if entry.Manifest.AgentID == "opencode" {
			descriptor = entry
		}
	}
	var calls atomic.Int32
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusTeapot)
	}))
	defer endpoint.Close()
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			provider := openCodeFullProvider(format)
			if provider.V1 != nil {
				provider.V1.Options.BaseURL = new(endpoint.URL + "/a%2Fb")
			}
			observed := &geminiObservedStore{Store: store}
			asset, err := service.Create(test.Context(), "alice", Draft{Name: "parity", OpenCodeProvider: &provider}, openCodeReplacement(test, "opencode.api-key"))
			if err != nil {
				test.Fatal("parity SQLite fixture failed")
			}
			desired, err := New(observed).OpenCodeProviderDesired(test.Context(), "alice", asset.Metadata.ID, 1)
			if err != nil || observed.reads != 1 || !reflect.DeepEqual(desired.Provider, provider) || !reflect.DeepEqual(desired.Slots, []agentconfig.OpenCodeSlotStatus{{Slot: "opencode.api-key", Present: true}, {Slot: "opencode.value.01", Present: false}}) {
				test.Fatal("desired did not use a single complete snapshot")
			}
			manifest := descriptor.Manifest
			request := agentconfig.OpenCodeProviderPlanRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: manifest.Capabilities, Desired: desired}
			planned, err := registry.PlanOpenCodeProvider(test.Context(), request)
			if err != nil || !reflect.DeepEqual(planned.Provider, provider) || !reflect.DeepEqual(planned.Plan.RequiredSlots, []string{"opencode.api-key", "opencode.value.01"}) {
				test.Fatal("real SQLite -> typed -> moejs reference parity failed")
			}
			if _, err := service.OpenCodeProviderDesired(test.Context(), "bob", asset.Metadata.ID, 1); !errors.Is(err, ErrNotFound) {
				test.Fatal("desired owner boundary failed")
			}
			if _, err := service.OpenCodeProviderDesired(test.Context(), "alice", asset.Metadata.ID, 2); !errors.Is(err, ErrConflict) {
				test.Fatal("desired CAS boundary failed")
			}
		})
	}
	if calls.Load() != 0 {
		test.Fatal("provider planning performed a network call")
	}
}

func TestOpenCodeAssetNativeDenied(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			fixture := newNativeOrderFixture(test, APIKey)
			asset, err := fixture.domain.Create(test.Context(), fixture.owner, openCodeDraft(format), openCodeReplacement(test, "opencode.api-key"))
			if err != nil {
				test.Fatal("OpenCode native rejection fixture failed")
			}
			if _, _, err := fixture.domain.load(test.Context(), fixture.owner, asset.Metadata.ID); !errors.Is(err, ErrUnsupported) {
				test.Fatal("Claude native loader widened")
			}
			if _, _, err := fixture.domain.loadEditable(test.Context(), fixture.owner, asset.Metadata.ID); !errors.Is(err, ErrUnsupported) {
				test.Fatal("legacy/native editable loader widened")
			}
			snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
			if err != nil {
				test.Fatal("native proof positive control failed")
			}
			consumer := snapshot.Consumer()
			request := fixture.prepare
			request.AssetID, request.AssetRevision = asset.Metadata.ID, asset.Metadata.Revision
			transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wirePrepareRequest(request))
			if err != nil {
				test.Fatal("OpenCode targeted valid proof construction failed")
			}
			signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
			if _, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, request, signature); err != nil {
				test.Fatal("valid proof did not reach business gate")
			}
			if _, err := fixture.native.Prepare(test.Context(), fixture.bearer, request, signature); !errors.Is(err, ErrUnsupported) {
				test.Fatal("OpenCode asset accepted for Claude native consumption")
			}
			codexRequest, codexSignature := codexBoundaryRequest(test, fixture, asset.Metadata)
			if _, err := fixture.consumer.PreflightCodexPrepare(test.Context(), fixture.bearer, codexRequest, codexSignature); err != nil {
				test.Fatal("Codex native proof positive control failed")
			}
			if _, err := fixture.native.PrepareCodex(test.Context(), fixture.bearer, codexRequest, codexSignature); !errors.Is(err, ErrUnsupported) {
				test.Fatal("OpenCode asset accepted for Codex native consumption")
			}
			database, err := sql.Open("sqlite", fixture.path)
			if err != nil {
				test.Fatal("native durable row oracle failed")
			}
			defer database.Close()
			for _, query := range []string{"SELECT count(*) FROM native_apply_preparations", "SELECT count(*) FROM native_materialization_grants"} {
				var count int
				if database.QueryRowContext(test.Context(), query).Scan(&count) != nil || count != 0 {
					test.Fatal("native rejection left preparation/grant state")
				}
			}
		})
	}
}

func TestOpenCodeAssetsNoLocalSideEffects(test *testing.T) {
	home := test.TempDir()
	for _, name := range []string{"opencode.json", "opencode.jsonc", "selection.json", "current"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("synthetic-foreign-content"), 0600); err != nil {
			test.Fatal("local baseline setup failed")
		}
	}
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	store, _, _ := geminiStore(test)
	service := New(store)
	for _, format := range []string{"v1", "v2"} {
		asset, err := service.Create(test.Context(), "alice", openCodeDraft(format), nil)
		if err != nil {
			test.Fatal("local side effect fixture failed")
		}
		if _, err := service.Copy(test.Context(), "alice", asset.Metadata.ID, 1, "copy"); err != nil {
			test.Fatal("local side effect copy failed")
		}
		if err := service.Delete(test.Context(), "alice", asset.Metadata.ID, 1); err != nil {
			test.Fatal("local side effect delete failed")
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 4 {
		test.Fatal("asset operations added local files")
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(home, entry.Name()))
		if err != nil || string(data) != "synthetic-foreign-content" {
			test.Fatal("asset operations changed local configuration")
		}
	}
}
