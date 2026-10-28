package httpapi

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
)

func configurationRequest[Message any](message *Message, token string) *connect.Request[Message] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func TestConfigurationOwnerLifecycleAndCAS(test *testing.T) {
	test.Run("binary", func(test *testing.T) { configurationLifecycle(test) })
	test.Run("json", func(test *testing.T) { configurationLifecycle(test, connect.WithProtoJSON()) })
}

func configurationLifecycle(test *testing.T, options ...connect.ClientOption) {
	test.Helper()
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-lifecycle")
	_, foreign := mcpSession(test, service, "configuration-foreign")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
	created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Primary"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "lifecycle-sentinel"}}}}, session))
	if err != nil {
		test.Fatalf("create: %s", connect.CodeOf(err))
	}
	asset := created.Msg.View.Asset
	operations := []struct {
		name string
		call func(string) error
	}{
		{"get", func(token string) error {
			_, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, token))
			return err
		}},
		{"edit", func(token string) error {
			_, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, token))
			return err
		}},
		{"update", func(token string) error {
			_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: configurationDraft("Forbidden")}, token))
			return err
		}},
		{"copy", func(token string) error {
			_, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 1, Name: "Forbidden copy"}, token))
			return err
		}},
		{"delete", func(token string) error {
			_, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 1}, token))
			return err
		}},
	}
	for _, operation := range operations {
		test.Run(operation.name, func(test *testing.T) {
			if code := connect.CodeOf(operation.call("")); code != connect.CodeUnauthenticated {
				test.Fatalf("anonymous: %s", code)
			}
			if code := connect.CodeOf(operation.call(foreign)); code != connect.CodeNotFound {
				test.Fatalf("cross-owner: %s", code)
			}
		})
	}
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, foreign))
	if err != nil || len(listed.Msg.Assets) != 0 {
		test.Fatal("cross-owner list leaked metadata")
	}
	updates := []*configurationv1.SecretMutation{
		{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}},
		{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "replacement-sentinel"}},
		{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}},
	}
	for index, mutation := range updates {
		updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: int64(index + 1), Draft: configurationDraft("Edited"), Secrets: []*configurationv1.SecretMutation{mutation}}, session))
		if err != nil || updated.Msg.View.Asset.Revision != int64(index+2) || updated.Msg.View.Secrets[0].Present != (index < 2) {
			test.Fatalf("secret mutation %d: %s", index, connect.CodeOf(err))
		}
		encoded, _ := protojson.Marshal(updated.Msg)
		if strings.Contains(string(encoded), "sentinel") {
			test.Fatal("update echoed secret")
		}
	}
	if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: configurationDraft("Stale")}, session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale update succeeded")
	}
	if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 1}, session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale delete succeeded")
	}
	if _, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 1, Name: "Stale"}, session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale source copied")
	}
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Go(func() {
			_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 4, Draft: configurationDraft("Concurrent"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "concurrent-sentinel"}}}}, session))
			results <- err
		})
	}
	workers.Wait()
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if connect.CodeOf(err) == connect.CodeAborted {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		test.Fatalf("CAS outcomes: %d success, %d conflicts", successes, conflicts)
	}
	copy, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 5, Name: "Copy without secrets"}, session))
	if err != nil || copy.Msg.View.Secrets[0].Present || copy.Msg.View.Secrets[1].Present || copy.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL {
		test.Fatal("copy carried credentials")
	}
	read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, session))
	if err != nil || !read.Msg.View.Secrets[0].Present || read.Msg.View.Asset.Revision != 5 {
		test.Fatal("copy changed source")
	}
	encoded, _ := protojson.Marshal(read.Msg)
	if strings.Contains(string(encoded), "sentinel") {
		test.Fatal("read returned secret")
	}
	if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 5}, session)); err != nil {
		test.Fatal("owner delete failed")
	}
	if _, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, session)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("deleted metadata survived")
	}
	if err := service.Logout(test.Context(), session); err != nil {
		test.Fatal(err)
	}
	if _, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("revoked session survived")
	}
}

func TestConfigurationMixedMetadataDoesNotDecodePayload(test *testing.T) {
	service, store := testServiceAndStore(test)
	owner, session := mcpSession(test, service, "configuration-mixed")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	if _, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Claude")}, session)); err != nil {
		test.Fatal("typed create failed")
	}
	for _, kind := range []state.ConfigurationKind{state.ConfigurationKindProvider, state.ConfigurationKindMCP, state.ConfigurationKindPrompt, state.ConfigurationKindAgentSettings, state.ConfigurationKindWorkspace} {
		entry, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: "future-agent", Kind: kind, Name: "Opaque future asset", SchemaVersion: 42, Payload: state.NewConfigurationPayload([]byte("opaque-private-sentinel"))})
		if err != nil {
			test.Fatal(err)
		}
		get, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: entry.ID}, session))
		if err != nil || get.Msg.Asset.AgentId != "future-agent" || get.Msg.Asset.SchemaVersion != 42 || get.Msg.Asset.Kind == configurationv1.AssetKind_ASSET_KIND_UNSPECIFIED {
			test.Fatal("future metadata hidden or downgraded")
		}
		if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: entry.ID}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			test.Fatal("unknown typed edit not rejected")
		}
		if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: entry.ID, ExpectedRevision: 1, Draft: configurationDraft("Unsupported")}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			test.Fatal("unknown typed update not rejected")
		}
		if _, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: entry.ID, ExpectedSourceRevision: 1, Name: "Unsupported"}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			test.Fatal("unknown typed copy not rejected")
		}
	}
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
	if err != nil || len(listed.Msg.Assets) != 6 {
		test.Fatal("mixed metadata list failed")
	}
	for _, asset := range listed.Msg.Assets {
		if asset.AgentId != "future-agent" {
			continue
		}
		if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 1}, session)); err != nil {
			test.Fatal("unknown asset cannot be deleted")
		}
	}
}

func TestConfigurationSecretValidationAndOptionalPresence(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-validation")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	for _, secrets := range [][]*configurationv1.SecretMutation{
		{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}}},
		{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{}}},
		{{SlotId: "unknown-private-slot", Action: &configurationv1.SecretMutation_Replace{Replace: "private-sentinel"}}},
		{{SlotId: "claude.api-key"}},
		{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "private-sentinel\r\n"}}},
		{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: strings.Repeat("x", 8193)}}},
		{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}, {SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "private-sentinel"}}},
		{{SlotId: "claude.auth-token", Action: &configurationv1.SecretMutation_Replace{Replace: "private-sentinel"}}},
	} {
		_, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Invalid"), Secrets: secrets}, session))
		if connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), "private-sentinel") || strings.Contains(err.Error(), "unknown-private-slot") {
			test.Fatal("invalid secret accepted or echoed")
		}
	}
	for _, draft := range []*configurationv1.Draft{
		nil, {Name: "Missing variant"},
		{Name: "Missing mode", Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{}}},
		{Name: "Empty model", Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY, Models: &configurationv1.ClaudeModels{Haiku: new("")}}}},
		{Name: "Secret URL", Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY, Endpoint: new("https://private-sentinel@example.invalid")}}},
	} {
		_, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
		if connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), "private-sentinel") {
			test.Fatal("invalid draft accepted or echoed")
		}
	}
	draft := configurationDraft("Optional roundtrip")
	draft.GetClaudeProvider().Endpoint = new("https://example.invalid/v1")
	draft.GetClaudeProvider().Models = &configurationv1.ClaudeModels{Haiku: new("haiku-literal")}
	created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: strings.Repeat("x", 8192)}}}}, session))
	if err != nil {
		test.Fatalf("valid >4KiB secret rejected: %s", connect.CodeOf(err))
	}
	provider := created.Msg.View.Draft.GetClaudeProvider()
	if provider.Endpoint == nil || *provider.Endpoint != "https://example.invalid/v1" || provider.Models.Haiku == nil || *provider.Models.Haiku != "haiku-literal" || provider.Models.Sonnet != nil || provider.Models.DefaultModel != nil {
		test.Fatal("optional fields lost presence")
	}
	asset := created.Msg.View.Asset
	draft.GetClaudeProvider().AuthMode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN
	if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, session)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		test.Fatal("mode switch silently cleared old secret")
	}
	updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}, {SlotId: "claude.auth-token", Action: &configurationv1.SecretMutation_Replace{Replace: "token-private-sentinel"}}}}, session))
	if err != nil || updated.Msg.View.Secrets[0].Present || !updated.Msg.View.Secrets[1].Present {
		test.Fatal("explicit mode switch failed")
	}
	copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 2, Name: "Tokenless copy"}, session))
	if err != nil || copied.Msg.View.Secrets[1].Present || copied.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL {
		test.Fatal("auth-token copy carried secret")
	}
	list := configurationRequest(&configurationv1.ListAssetsRequest{}, session)
	list.Header().Add("Authorization", "Bearer "+session)
	if _, err := client.ListAssets(test.Context(), list); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("ambiguous session header accepted")
	}
}

func configurationDraft(name string) *configurationv1.Draft {
	return &configurationv1.Draft{Name: name, Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY}}}
}

func TestConfigurationConnectSessionAndCreate(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-owner")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	if _, err := client.ListAssets(test.Context(), connect.NewRequest(&configurationv1.ListAssetsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("anonymous configuration: %s", connect.CodeOf(err))
	}
	const secret = "configuration-secret-sentinel"
	created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{
		Draft: configurationDraft("Primary"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: secret}}},
	}, session))
	if err != nil {
		test.Fatalf("create: %s", connect.CodeOf(err))
	}
	if created.Msg.View.GetAsset().GetRevision() != 1 || created.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE {
		test.Fatal("asset was not created as credential-complete")
	}
	encoded, err := protojson.Marshal(created.Msg)
	if err != nil || strings.Contains(string(encoded), secret) || created.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("create leaked secret or permitted caching")
	}
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
	if err != nil || len(listed.Msg.Assets) != 1 || listed.Msg.Assets[0].Id != created.Msg.View.Asset.Id {
		test.Fatal("owner asset list unavailable")
	}
}
