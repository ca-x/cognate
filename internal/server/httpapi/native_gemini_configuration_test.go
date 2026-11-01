package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func TestGeminiMixedVariantRejectedOnRealCodexTLS(test *testing.T) {
	test.Run("prepare", func(test *testing.T) {
		fixture := newNativeHTTPFixture(test)
		asset := codexHTTPAsset(test, fixture)
		flow := &codexHTTPFlow{fixture.bootstrap(test)}
		request := nativeCodexPrepareWire()
		request.ExpectedOwnerId = flow.consumer.OwnerID
		request.AssetId = asset.Metadata.ID
		request.ExpectedAssetRevision = asset.Metadata.Revision
		request.OperationId = bytes.Clone(flow.operation)
		request.ProofSignature = nil
		transcript, err := configclient.CodexPrepareTranscript(flow.consumer, request)
		if err != nil {
			test.Fatal("valid old Codex prepare setup")
		}
		request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
		request.GeminiContractVersion = 1
		response, err := fixture.client.PrepareNativeApply(test.Context(), fixture.request(request, flow.bearer))
		if err == nil || response != nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatalf("mixed Gemini/Codex prepare: accepted=%t response_present=%t safe_code=%s", err == nil, response != nil, connect.CodeOf(err))
		}
	})
	test.Run("authorize", func(test *testing.T) {
		fixture := newNativeHTTPFixture(test)
		asset := codexHTTPAsset(test, fixture)
		flow := &codexHTTPFlow{fixture.bootstrap(test)}
		flow.prepare(test, asset)
		request := flow.authorizeRequest(test)
		request.Msg.GeminiLocalBinding = &nativev1.GeminiLocalBindingV1{BindingVersion: 1}
		response, err := fixture.client.AuthorizeNativeMaterialization(test.Context(), request)
		if err == nil || response != nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatalf("mixed Gemini/Codex authorize: accepted=%t response_present=%t safe_code=%s", err == nil, response != nil, connect.CodeOf(err))
		}
	})
}

const geminiHTTPSecret = "synthetic-gemini-native-exact-key"

func geminiHTTPAsset(test *testing.T, fixture *nativeHTTPFixture, mode agentconfig.GeminiAuthMode) configuration.EditView {
	test.Helper()
	provider := &configuration.GeminiProvider{AuthMode: mode, EnvModel: new("env-model"), SettingsModel: new("settings-model")}
	var mutations []configuration.SecretMutation
	if mode == agentconfig.GeminiAPIKeyAuth {
		provider.Endpoint = new("https://gemini-fixture.invalid/v1beta")
		provider.KeyTransport = new(agentconfig.GeminiNativeDefault)
		mutations = []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(geminiHTTPSecret))}}
	}
	asset, err := fixture.domain.Create(test.Context(), fixture.ownerID, configuration.Draft{Name: "synthetic-Gemini-native", GeminiProvider: provider}, mutations)
	if err != nil {
		test.Fatal("encrypted Gemini asset setup")
	}
	return asset
}
func geminiHTTPPrepare(test *testing.T, flow *nativeHTTPFlow, asset configuration.EditView) *nativev1.PrepareNativeApplyResponse {
	test.Helper()
	pin := configclient.GeminiNativeAdapterPin()
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: flow.consumer.OwnerID, AssetId: asset.Metadata.ID, ExpectedAssetRevision: asset.Metadata.Revision, AgentId: "gemini", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, RequiredCapabilities: []string{"gemini.provider-plan.v1"}}, Target: 3, Scope: 1, ProfileId: "synthetic-gemini-profile", SelectionRevision: 1, OperationId: bytes.Clone(flow.operation), ClientNonce: bytes.Repeat([]byte{6}, 32), GeminiContractVersion: 1}
	transcript, err := configclient.GeminiPrepareTranscript(flow.consumer, request)
	if err != nil {
		test.Fatal("Gemini client prepare proof setup")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	response, err := flow.fixture.client.PrepareNativeApply(test.Context(), flow.fixture.request(request, flow.bearer))
	if err != nil || response == nil {
		test.Fatalf("real Gemini Prepare rejected: safe_code=%s", connect.CodeOf(err))
	}
	if configclient.ValidateGeminiPreparedDeclaration(response.Msg) != nil || response.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("invalid safe Gemini preparation")
	}
	return response.Msg
}
func geminiHTTPProjection(prepared *nativev1.PrepareNativeApplyResponse) *nativev1.GeminiLocalBindingV1 {
	value := &nativev1.GeminiLocalBindingV1{BindingVersion: 1, NativeProfileId: "synthetic-protocol-not-qualification", NativeProfileSha256: bytes.Repeat([]byte{1}, 32), CodecProfileSha256: bytes.Repeat([]byte{2}, 32), EffectiveScope: 1, LocalAuthPolicy: "preserve-local-login-v1", ObservationWitness: bytes.Repeat([]byte{3}, 32), Roles: []*nativev1.GeminiRoleBaseline{{Role: 1, Presence: 1, BaselineHmac: bytes.Repeat([]byte{4}, 32)}, {Role: 2, Presence: 1, BaselineHmac: bytes.Repeat([]byte{5}, 32)}}}
	declaration := prepared.GeminiDeclaration
	declared := [6]bool{declaration.AuthMode == 2, declaration.Endpoint != nil, declaration.KeyTransport != nil && *declaration.KeyTransport == 2, declaration.EnvModel != nil, declaration.SettingsModel != nil, true}
	for index, written := range declared {
		action := nativev1.GeminiAction(1)
		if written {
			action = 2
		}
		value.FieldDecisions = append(value.FieldDecisions, &nativev1.GeminiFieldDecision{Field: nativev1.GeminiField(index + 1), Ownership: 1, Action: action, Cleanup: 1})
	}
	return value
}
func geminiHTTPAuthorizeRequest(test *testing.T, flow *nativeHTTPFlow, prepared *nativev1.PrepareNativeApplyResponse) *connect.Request[nativev1.AuthorizeNativeMaterializationRequest] {
	test.Helper()
	local := geminiHTTPProjection(prepared)
	expiry := prepared.Binding.PrepareExpiresAtUnixMs
	commitment, err := configclient.HashGeminiIntent(prepared.Binding, local, 7, expiry)
	if err != nil {
		test.Fatal("Gemini intent setup")
	}
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: prepared.Binding.PrepareId, OperationId: bytes.Clone(flow.operation), IntentCommitment: commitment[:], RequestedSlots: append([]nativev1.SecretSlot(nil), prepared.Binding.RequiredSlots...), Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 7}}, ClientNonce: bytes.Repeat([]byte{9}, 32), IntentExpiresAtUnixMs: expiry, GeminiLocalBinding: local}
	transcript, err := configclient.GeminiAuthorizeTranscript(prepared.Binding, prepared.AuthorizeChallenge, request)
	if err != nil {
		test.Fatal("Gemini authorize proof setup")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}
func geminiHTTPConsumeRequest(test *testing.T, flow *nativeHTTPFlow, prepared *nativev1.PrepareNativeApplyResponse, grant *nativev1.AuthorizeNativeMaterializationResponse) *connect.Request[nativev1.ConsumeNativeMaterializationRequest] {
	test.Helper()
	request := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: prepared.Binding.PrepareId, GrantId: grant.GrantId, GrantToken: bytes.Clone(grant.GrantToken), OperationId: bytes.Clone(flow.operation), IntentCommitment: bytes.Clone(grant.Binding.IntentCommitment), ClientNonce: bytes.Repeat([]byte{12}, 32)}
	transcript, err := configclient.GeminiConsumeTranscript(grant.Binding, prepared.AuthorizeChallenge, grant.ConsumeChallenge, grant.ExpiresAtUnixMs, request)
	if err != nil {
		test.Fatal("Gemini consume proof setup")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}
func geminiHTTPStatus(test *testing.T, flow *nativeHTTPFlow, prepared *nativev1.PrepareNativeApplyResponse) *nativev1.GetNativeMaterializationStatusResponse {
	test.Helper()
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: prepared.Binding.PrepareId, OperationId: bytes.Clone(flow.operation), ClientNonce: bytes.Repeat([]byte{13}, 32)}
	transcript, err := configclient.GeminiStatusTranscript(prepared.Binding, prepared.AuthorizeChallenge, request)
	if err != nil {
		test.Fatal("Gemini status proof setup")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	response, err := flow.fixture.client.GetNativeMaterializationStatus(test.Context(), flow.fixture.request(request, flow.bearer))
	if err != nil {
		test.Fatalf("Gemini safe Status: %s", connect.CodeOf(err))
	}
	return response.Msg
}
func TestGeminiNativeTLSLifecycle(test *testing.T) {
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiAPIKeyAuth, agentconfig.GeminiNativeLogin} {
		test.Run(string(mode), func(test *testing.T) {
			fixture := newNativeHTTPFixture(test)
			asset := geminiHTTPAsset(test, fixture, mode)
			flow := fixture.bootstrap(test)
			prepared := geminiHTTPPrepare(test, flow, asset)
			if geminiHTTPStatus(test, flow, prepared).State != 1 {
				test.Fatal("prepared Status not not-issued")
			}
			response, err := fixture.client.AuthorizeNativeMaterialization(test.Context(), geminiHTTPAuthorizeRequest(test, flow, prepared))
			if err != nil {
				test.Fatalf("Gemini Authorize: %s", connect.CodeOf(err))
			}
			if mode == agentconfig.GeminiNativeLogin {
				if response.Msg.MaterializationRequired || response.Msg.GrantId != "" || len(response.Msg.GrantToken) != 0 || len(response.Msg.ConsumeChallenge) != 0 {
					test.Fatal("native-login issued material grant")
				}
				return
			}
			if geminiHTTPStatus(test, flow, prepared).State != 2 {
				test.Fatal("issued Status missing")
			}
			material, err := fixture.client.ConsumeNativeMaterialization(test.Context(), geminiHTTPConsumeRequest(test, flow, prepared, response.Msg))
			if err != nil || material == nil {
				test.Fatalf("Gemini Consume: %s", connect.CodeOf(err))
			}
			if len(material.Msg.SlotValues) != 1 || material.Msg.SlotValues[0].Slot != 4 || !bytes.Equal(material.Msg.SlotValues[0].Plaintext, []byte(geminiHTTPSecret)) {
				test.Fatal("exact Gemini slot4 bytes missing")
			}
			if geminiHTTPStatus(test, flow, prepared).State != 3 {
				test.Fatal("consumed Status missing")
			}
			if replay, err := fixture.client.ConsumeNativeMaterialization(test.Context(), geminiHTTPConsumeRequest(test, flow, prepared, response.Msg)); err == nil || replay != nil {
				test.Fatal("Gemini Consume replay succeeded")
			}
		})
	}
}
