package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func grokBuildConfigurationDraft(env *string) *configurationv1.Draft {
	return &configurationv1.Draft{Name: "Grok fixture", Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL, Model: &configurationv1.GrokBuildModel{Profile: "Local profile", UpstreamModel: "x-ai/grok-4.5", Endpoint: "https://synthetic.invalid/v1", Name: "Synthetic provider", ApiBackend: "responses", ContextWindow: 500000, EnvKey: env}}}}
}

func grokBuildNativeDraft() *configurationv1.Draft {
	return &configurationv1.Draft{Name: "Native Grok", Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN}}}
}

func TestGrokBuildConfigurationCRUD(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "grok-crud-owner")
			_, stranger := mcpSession(test, accounts, "grok-crud-stranger")
			endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			draft := grokBuildConfigurationDraft(nil)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "grokbuild.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-grok-crud-key"}}}}, session))
			if err != nil {
				test.Fatalf("create failed: %s", connect.CodeOf(err))
			}
			view := created.Msg.View
			if view == nil || !proto.Equal(view.Draft, draft) || view.Asset.AgentId != "grokbuild" || view.Asset.Revision != 1 || len(view.Secrets) != 1 || !view.Secrets[0].Present || view.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE {
				test.Fatal("create lost provider fields, key status or readiness")
			}
			id := view.Asset.Id
			configurationAssertStoredSecret(test, store, owner, id, 1, "synthetic-grok-crud-key")
			got, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: id}, session))
			if err != nil || !proto.Equal(got.Msg.Asset, view.Asset) {
				test.Fatal("metadata read changed persisted asset")
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || !proto.Equal(read.Msg.View, view) {
				test.Fatal("draft read changed typed provider")
			}
			if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, stranger)); connect.CodeOf(err) != connect.CodeNotFound {
				test.Fatal("cross-owner draft read was not concealed")
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, stranger)); connect.CodeOf(err) != connect.CodeNotFound {
				test.Fatal("cross-owner update accepted")
			}
			draft.GetGrokbuildProvider().Model.EnvKey = new("GROK_TEST_ENV")
			updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, session))
			if err != nil || updated.Msg.View.Asset.Revision != 2 || !proto.Equal(updated.Msg.View.Draft, draft) || !updated.Msg.View.Secrets[0].Present {
				test.Fatal("Keep update did not retain key alongside env reference")
			}
			configurationAssertStoredSecret(test, store, owner, id, 2, "synthetic-grok-crud-key")
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: grokBuildNativeDraft()}, session)); connect.CodeOf(err) != connect.CodeAborted {
				test.Fatal("stale revision bypassed CAS")
			}
			copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 2, Name: "Grok copy"}, session))
			if err != nil || copied.Msg.View.Asset.Id == id || copied.Msg.View.Secrets[0].Present || copied.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE || !proto.Equal(copied.Msg.View.Draft.GetGrokbuildProvider(), draft.GetGrokbuildProvider()) {
				test.Fatal("copy lost model/env or copied credential")
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: grokBuildNativeDraft()}, session)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				test.Fatal("native transition silently discarded retained credential")
			}
			native, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: grokBuildNativeDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "grokbuild.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}}}, session))
			if err != nil || native.Msg.View.Asset.Revision != 3 || native.Msg.View.Secrets[0].Present || !proto.Equal(native.Msg.View.Draft, grokBuildNativeDraft()) {
				test.Fatal("explicit Clear and native transition were not atomic")
			}
			listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			if err != nil || len(listed.Msg.Assets) != 2 {
				test.Fatal("actual SQLite list does not contain source and copy")
			}
			order, err := client.GetAssetOrder(test.Context(), configurationRequest(&configurationv1.GetAssetOrderRequest{AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER}, session))
			if err != nil || len(order.Msg.Snapshot.Assets) != 2 {
				test.Fatal("provider partition order lost source or copy")
			}
			ordered, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(&configurationv1.ReplaceAssetOrderRequest{AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, ExpectedRevision: order.Msg.Snapshot.Revision, AssetIds: []string{copied.Msg.View.Asset.Id, id}}, session))
			if err != nil || ordered.Msg.Snapshot.Assets[0].Id != copied.Msg.View.Asset.Id || ordered.Msg.Snapshot.Assets[1].Id != id {
				test.Fatal("order replacement lost exact membership or order")
			}
			if _, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(&configurationv1.ReplaceAssetOrderRequest{AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, ExpectedRevision: order.Msg.Snapshot.Revision, AssetIds: []string{id, copied.Msg.View.Asset.Id}}, session)); connect.CodeOf(err) != connect.CodeAborted {
				test.Fatal("order replacement bypassed CAS")
			}
			for _, target := range []struct {
				id       string
				revision int64
			}{{id, 3}, {copied.Msg.View.Asset.Id, 1}} {
				if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: target.id, ExpectedRevision: target.revision}, session)); err != nil {
					test.Fatal("delete failed")
				}
			}
			entries, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(entries) != 0 {
				test.Fatal("delete left actual durable entries")
			}
		})
	}
}

func TestGrokBuildConfigurationViewBoundary(test *testing.T) {
	valid := func() configuration.EditView {
		input, secrets, err := configurationInput(grokBuildConfigurationDraft(new("GROK_TEST_ENV")), nil)
		configurationClearSecrets(secrets)
		if err != nil {
			test.Fatal("view fixture mapping failed")
		}
		return configuration.EditView{Metadata: state.ConfigurationAsset{ID: "synthetic-id", AgentID: "grokbuild", Kind: state.ConfigurationKindProvider, SchemaVersion: 1, Name: "Grok fixture", Revision: 1}, GrokBuildProvider: input.GrokBuildProvider, Secrets: []configuration.SecretStatus{{Slot: configuration.GrokBuildAPIKeySlot}}, Readiness: configuration.CredentialComplete}
	}
	entry := valid()
	view, err := configurationView(entry)
	if err != nil || view == nil || view.Draft.GetGrokbuildProvider() == nil {
		test.Fatal("valid view mapping failed")
	}
	*view.Draft.GetGrokbuildProvider().Model.EnvKey = "CHANGED_ENV"
	if *entry.GrokBuildProvider.Model.EnvKey != "GROK_TEST_ENV" {
		test.Fatal("view mapping aliases domain pointer")
	}
	for name, corrupt := range map[string]func(*configuration.EditView){
		"wrong-agent":     func(entry *configuration.EditView) { entry.Metadata.AgentID = "codex" },
		"wrong-kind":      func(entry *configuration.EditView) { entry.Metadata.Kind = state.ConfigurationKindMCP },
		"wrong-version":   func(entry *configuration.EditView) { entry.Metadata.SchemaVersion = 2 },
		"missing-status":  func(entry *configuration.EditView) { entry.Secrets = nil },
		"wrong-slot":      func(entry *configuration.EditView) { entry.Secrets[0].Slot = "codex.provider-key" },
		"wrong-readiness": func(entry *configuration.EditView) { entry.Readiness = configuration.NeedsCredential },
		"bad-endpoint": func(entry *configuration.EditView) {
			entry.GrokBuildProvider.Model.Endpoint = "https://synthetic.invalid:0/v1"
		},
		"bad-context":   func(entry *configuration.EditView) { entry.GrokBuildProvider.Model.ContextWindow = 0 },
		"bad-env":       func(entry *configuration.EditView) { entry.GrokBuildProvider.Model.EnvKey = new("") },
		"missing-model": func(entry *configuration.EditView) { entry.GrokBuildProvider.Model = nil },
	} {
		test.Run(name, func(test *testing.T) {
			entry := valid()
			corrupt(&entry)
			view, err := configurationGrokBuildView(entry)
			if err != configuration.ErrUnavailable || view != nil {
				test.Fatal("inconsistent backend view was exposed as usable data")
			}
		})
	}
}

func TestGrokBuildConfigurationReadinessAndMapperOwnership(test *testing.T) {
	for _, env := range []*string{nil, new("GROK_TEST_ENV")} {
		draft := grokBuildConfigurationDraft(env)
		mapped, mutations, err := configurationInput(draft, nil)
		configurationClearSecrets(mutations)
		if err != nil || mapped.GrokBuildProvider == nil || mapped.GrokBuildProvider.Model == nil {
			test.Fatal("valid custom model mapping failed")
		}
		if env != nil {
			*mapped.GrokBuildProvider.Model.EnvKey = "CHANGED_ENV"
			if *env != "GROK_TEST_ENV" {
				test.Fatal("input mapping aliases caller env pointer")
			}
		}
		accounts, store := testServiceAndStore(test)
		_, session := mcpSession(test, accounts, "grok-readiness")
		endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
		client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
		created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
		endpoint.Close()
		wanted := configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
		if env != nil {
			wanted = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
		}
		if err != nil || created.Msg.View.CredentialReadiness != wanted || created.Msg.View.Secrets[0].Present {
			test.Fatal("keyless custom readiness is not derived from env presence")
		}
	}
}

func TestGrokBuildConfigurationJSONCreate(test *testing.T) {
	accounts, store := testServiceAndStore(test)
	owner, session := mcpSession(test, accounts, "grok-json-create")
	endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
	defer endpoint.Close()
	const request = `{"draft":{"name":"Grok API","grokbuildProvider":{"mode":"GROK_BUILD_MODE_CUSTOM_MODEL","model":{"profile":"Local profile","upstreamModel":"x-ai/grok-4.5","endpoint":"https://synthetic.invalid/v1","name":"Synthetic provider","apiBackend":"responses","contextWindow":"500000","envKey":"XAI_TEST_KEY"}}},"secrets":[{"slotId":"grokbuild.api-key","replace":"SYNTHETIC-GROK-PRIVATE-CREDENTIAL"}]}`
	status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(request))
	if status != http.StatusOK {
		test.Fatalf("typed Grok CreateAsset must succeed, got status %d", status)
	}
	if strings.Contains(body, "SYNTHETIC-GROK-PRIVATE-CREDENTIAL") {
		test.Fatal("credential escaped typed response")
	}
	entries, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(entries) != 1 || entries[0].AgentID != "grokbuild" || entries[0].Revision != 1 {
		test.Fatal("successful RPC must create exactly one actual Grok asset")
	}
}
