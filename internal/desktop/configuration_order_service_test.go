package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/httpapi"
	serverstate "github.com/ca-x/cognate/internal/server/state"
)

func configurationOrderSeed(test *testing.T, fixture *configurationFixture, owner, agent, kind, name string) configurationAsset {
	test.Helper()
	asset, err := fixture.store.CreateConfigurationAsset(test.Context(), owner, serverstate.ConfigurationAssetDraft{AgentID: agent, Kind: serverstate.ConfigurationKind(kind), Name: name, SchemaVersion: 97, Payload: serverstate.NewConfigurationPayload([]byte(`{"future":"synthetic-order-body-secret-never-read"}`))})
	if err != nil {
		test.Fatal("seed private unknown-schema metadata")
	}
	return configurationAsset{ID: asset.ID, AgentID: agent, Kind: kind, Name: asset.Name, SchemaVersion: uint32(asset.SchemaVersion), Revision: asset.Revision}
}

func configurationOrderNoContentCalls(test *testing.T, fixture *configurationFixture) {
	test.Helper()
	for _, method := range []string{"GetAsset", "ReadDraft", "Preview", "BeginNativeConsumer", "AuthorizeNativeMaterialization", "ConsumeNativeMaterialization"} {
		if fixture.count(method) != 0 {
			test.Fatalf("metadata sorting called forbidden method %s", method)
		}
	}
}

func TestConfigurationOrderServiceAllPartitions(test *testing.T) {
	fixture := newConfigurationFixture(test)
	agents := []string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"}
	kinds := []string{"provider", "mcp", "prompt", "agent-settings", "workspace"}
	for _, agent := range agents {
		for _, kind := range kinds {
			configurationOrderSeed(test, fixture, fixture.owner, agent, kind, agent+" "+kind)
		}
	}
	identity := configurationTestIdentity(test, fixture)
	defer identity.clear()
	assets, err := fixture.service.ConfigurationAssets(test.Context(), identity)
	if err != nil || len(assets) != 70 {
		test.Fatal("did not read complete owner metadata")
	}
	partitions := configurationOrderPartitions(assets, configurationOrderPartition{})
	if len(partitions) != 70 {
		test.Fatal("agent/kind partitions collapsed or filtered unknown schema")
	}
	for _, partition := range partitions {
		snapshot, err := fixture.service.ConfigurationOrder(test.Context(), identity, partition)
		if err != nil || len(snapshot.Assets) != 1 || snapshot.Assets[0].SchemaVersion != 97 || snapshot.Assets[0].Editable {
			test.Fatal("unknown schema partition was not sortable metadata")
		}
	}
	configurationOrderNoContentCalls(test, fixture)
}

func TestConfigurationOrderServiceComplete250(test *testing.T) {
	fixture := newConfigurationFixture(test)
	for index := range 250 {
		configurationOrderSeed(test, fixture, fixture.owner, "opencode", "provider", fmt.Sprintf("Asset %03d", index))
	}
	beforeMetadata, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil {
		test.Fatal("read seed metadata")
	}
	metadata := make(map[string]serverstate.ConfigurationAsset)
	for _, asset := range beforeMetadata {
		metadata[asset.ID] = asset
	}
	identity := configurationTestIdentity(test, fixture)
	defer identity.clear()
	before, err := fixture.service.ConfigurationOrder(test.Context(), identity, configurationOrderPartition{AgentID: "opencode", Kind: "provider"})
	if err != nil || len(before.Assets) != 250 {
		test.Fatal("complete 250 item read was truncated")
	}
	ids := configurationOrderIDs(before.Assets)
	slices.Reverse(ids)
	after, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, ids)
	if err != nil || after.Revision != before.Revision+1 || !slices.Equal(configurationOrderIDs(after.Assets), ids) || fixture.count("ReplaceAssetOrder") != 1 {
		test.Fatal("complete permutation CAS failed")
	}
	persisted, err := fixture.store.ConfigurationAssetOrder(test.Context(), fixture.owner, "opencode", serverstate.ConfigurationKindProvider)
	if err != nil || len(persisted.Assets) != 250 || persisted.Assets[0].ID != ids[0] {
		test.Fatal("250 item order was not persisted")
	}
	for _, asset := range persisted.Assets {
		if !reflect.DeepEqual(metadata[asset.ID], asset) {
			test.Fatal("order changed content metadata or timestamps")
		}
	}
	configurationOrderNoContentCalls(test, fixture)
}

func TestConfigurationOrderServiceOwnerCASAndEmptyABA(test *testing.T) {
	fixture := newConfigurationFixture(test)
	first := configurationOrderSeed(test, fixture, fixture.owner, "gemini", "prompt", "First")
	second := configurationOrderSeed(test, fixture, fixture.owner, "gemini", "prompt", "Second")
	identity := configurationTestIdentity(test, fixture)
	defer identity.clear()
	foreign, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.server.URL, fixture.foreignSession)
	if err != nil {
		test.Fatal("read foreign identity")
	}
	defer foreign.clear()
	partition := configurationOrderPartition{AgentID: "gemini", Kind: "prompt"}
	before, err := fixture.service.ConfigurationOrder(test.Context(), identity, partition)
	if err != nil {
		test.Fatal("read owner partition")
	}
	foreignOrder, err := fixture.service.ConfigurationOrder(test.Context(), foreign, partition)
	if err != nil || len(foreignOrder.Assets) != 0 {
		test.Fatal("foreign owner learned partition metadata")
	}
	ids := []string{second.ID, first.ID}
	if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), foreign, before, ids); !errors.Is(err, errConfigurationConflict) {
		test.Fatal("cross-owner permutation accepted")
	}
	if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, ids); err != nil {
		test.Fatal("valid owner CAS failed")
	}
	if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, ids); !errors.Is(err, errConfigurationConflict) {
		test.Fatal("stale revision CAS accepted")
	}
	count := fixture.count("ReplaceAssetOrder")
	for _, invalid := range [][]string{{first.ID}, {first.ID, first.ID}, {first.ID, "invalid"}} {
		if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, invalid); !errors.Is(err, errConfigurationInvalid) || fixture.count("ReplaceAssetOrder") != count {
			test.Fatal("incomplete or duplicate permutation reached server")
		}
	}
	emptyPartition := configurationOrderPartition{AgentID: "kimi", Kind: "workspace"}
	empty, err := fixture.service.ConfigurationOrder(test.Context(), identity, emptyPartition)
	if err != nil || empty.Revision != 0 || len(empty.Assets) != 0 {
		test.Fatal("initial empty partition failed")
	}
	temporary := configurationOrderSeed(test, fixture, fixture.owner, "kimi", "workspace", "Temporary")
	if err := fixture.store.DeleteConfigurationAsset(test.Context(), fixture.owner, temporary.ID, temporary.Revision); err != nil {
		test.Fatal("delete temporary member")
	}
	if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, empty, nil); !errors.Is(err, errConfigurationConflict) {
		test.Fatal("empty ABA accepted old zero revision")
	}
	current, err := fixture.service.ConfigurationOrder(test.Context(), identity, emptyPartition)
	if err != nil || current.Revision != 2 || len(current.Assets) != 0 {
		test.Fatal("empty partition lost independent revision")
	}
	configurationOrderNoContentCalls(test, fixture)
}

func configurationOrderDropReceipt(test *testing.T, fixture *configurationFixture) {
	test.Helper()
	handler := httpapi.NewWithOptions(fixture.auth, httpapi.Options{Configuration: fixture.domain, PublicOrigin: fixture.server.URL})
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if !strings.HasSuffix(request.URL.Path, "/ReplaceAssetOrder") {
			return false
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			test.Error("synthetic order did not commit before receipt loss")
		}
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return true
	})
}

func TestConfigurationOrderServiceCommittedUnknown(test *testing.T) {
	fixture := newConfigurationFixture(test)
	configurationOrderSeed(test, fixture, fixture.owner, "claude", "mcp", "First")
	configurationOrderSeed(test, fixture, fixture.owner, "claude", "mcp", "Second")
	identity := configurationTestIdentity(test, fixture)
	defer identity.clear()
	before, err := fixture.service.ConfigurationOrder(test.Context(), identity, configurationOrderPartition{AgentID: "claude", Kind: "mcp"})
	if err != nil {
		test.Fatal("read initial snapshot")
	}
	ids := configurationOrderIDs(before.Assets)
	slices.Reverse(ids)
	configurationOrderDropReceipt(test, fixture)
	if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, ids); !errors.Is(err, errConfigurationOrderUnknown) || fixture.count("ReplaceAssetOrder") != 1 {
		test.Fatal("lost committed response was not Unknown or was replayed")
	}
	fixture.setIntercept(nil)
	current, err := fixture.service.ConfigurationOrder(test.Context(), identity, before.configurationOrderPartition)
	if err != nil || !slices.Equal(configurationOrderIDs(current.Assets), ids) || current.Revision != before.Revision+1 {
		test.Fatal("fault was not after actual SQLite commit")
	}
}

func TestConfigurationOrderServiceInvalidAcknowledgmentIsUnknown(test *testing.T) {
	fixture := newConfigurationFixture(test)
	configurationOrderSeed(test, fixture, fixture.owner, "codex", "provider", "First")
	configurationOrderSeed(test, fixture, fixture.owner, "codex", "provider", "Second")
	identity := configurationTestIdentity(test, fixture)
	defer identity.clear()
	before, err := fixture.service.ConfigurationOrder(test.Context(), identity, configurationOrderPartition{AgentID: "codex", Kind: "provider"})
	if err != nil {
		test.Fatal("read initial order")
	}
	ids := configurationOrderIDs(before.Assets)
	slices.Reverse(ids)
	handler := connect.NewUnaryHandler(configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure, func(context.Context, *connect.Request[configurationv1.ReplaceAssetOrderRequest]) (*connect.Response[configurationv1.ReplaceAssetOrderResponse], error) {
		return connect.NewResponse(&configurationv1.ReplaceAssetOrderResponse{Snapshot: &configurationv1.AssetOrderSnapshot{AgentId: before.AgentID, Kind: configurationOrderKind(before.Kind), Revision: before.Revision + 1}}), nil
	})
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if !strings.HasSuffix(request.URL.Path, "/ReplaceAssetOrder") {
			return false
		}
		handler.ServeHTTP(writer, request)
		return true
	})
	if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, ids); !errors.Is(err, errConfigurationOrderUnknown) {
		test.Fatal("invalid acknowledgment was reported as a rejected or successful write")
	}
}

func TestConfigurationOrderServiceRejectsInvalidSnapshots(test *testing.T) {
	for _, scenario := range []string{"agent", "kind", "revision", "duplicate", "unknown-wire"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			asset := configurationOrderSeed(test, fixture, fixture.owner, "hermes", "agent-settings", "Metadata")
			identity := configurationTestIdentity(test, fixture)
			defer identity.clear()
			view := &configurationv1.AssetOrderSnapshot{AgentId: asset.AgentID, Kind: configurationOrderKind(asset.Kind), Revision: 1, Assets: []*configurationv1.Asset{{Id: asset.ID, AgentId: asset.AgentID, Kind: configurationOrderKind(asset.Kind), Name: asset.Name, SchemaVersion: asset.SchemaVersion, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}}}
			switch scenario {
			case "agent":
				view.AgentId = "codex"
			case "kind":
				view.Assets[0].Kind = configurationv1.AssetKind_ASSET_KIND_WORKSPACE
			case "revision":
				view.Revision = -1
			case "duplicate":
				view.Assets = append(view.Assets, view.Assets[0])
			case "unknown-wire":
				view.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			}
			handler := connect.NewUnaryHandler(configurationv1connect.ConfigurationServiceGetAssetOrderProcedure, func(context.Context, *connect.Request[configurationv1.GetAssetOrderRequest]) (*connect.Response[configurationv1.GetAssetOrderResponse], error) {
				return connect.NewResponse(&configurationv1.GetAssetOrderResponse{Snapshot: view}), nil
			})
			fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
				if !strings.HasSuffix(request.URL.Path, "/GetAssetOrder") {
					return false
				}
				handler.ServeHTTP(writer, request)
				return true
			})
			if _, err := fixture.service.ConfigurationOrder(test.Context(), identity, configurationOrderPartition{AgentID: asset.AgentID, Kind: asset.Kind}); !errors.Is(err, errConfigurationInvalid) {
				test.Fatal("invalid order snapshot accepted")
			}
		})
	}
}
