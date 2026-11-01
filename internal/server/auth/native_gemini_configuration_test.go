package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestGeminiAuthRealParentProofAndRevocation(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	challenge, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal("actual owner Begin")
	}
	fields, _ := challenge.BindFields()
	issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	if err != nil {
		test.Fatal("actual owner Bind")
	}
	asset, err := fixture.owner.store.CreateConfigurationAsset(test.Context(), fixture.ownerID, state.ConfigurationAssetDraft{AgentID: "gemini", Kind: state.ConfigurationKindProvider, Name: "no-secret Gemini auth", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("synthetic-native-login-payload"))})
	if err != nil {
		test.Fatal("private encrypted test asset")
	}
	pin := configclient.GeminiNativeAdapterPin()
	adapter := nativesessionauth.AdapterFields{APIVersion: 1, PluginID: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, Capability: "gemini.provider-plan.v1"}
	request := nativesessionauth.GeminiPrepareRequest{PrepareRequest: nativesessionauth.PrepareRequest{OwnerID: fixture.ownerID, AssetID: asset.ID, AssetRevision: asset.Revision, AgentID: "gemini", SchemaVersion: 1, Adapter: adapter, Target: 3, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: fields.OperationID}, ContractVersion: 1}
	wire := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: "gemini", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: adapter.PluginID, PluginVersion: adapter.PluginVersion, DefinitionDigest: adapter.DefinitionDigest, RequiredCapabilities: []string{adapter.Capability}}, Target: 3, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: fields.OperationID[:], ClientNonce: request.Nonce[:], GeminiContractVersion: 1}
	hash := sha256.Sum256(start.PublicKey)
	transcript, err := configclient.GeminiPrepareTranscript(configclient.ConsumerBinding{OwnerID: request.OwnerID, ServerOrigin: start.Origin, NativeSessionBinding: fields.NativeSessionBinding[:], OperationID: start.OperationID, ConsumerKeySHA256: hash[:]}, wire)
	if err != nil {
		test.Fatal("independent Gemini proof")
	}
	signature := ed25519.Sign(key, transcript.CopyBytes())
	data, err := state.NewNativeGeminiPreparedData(adapter, agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: agentconfig.GeminiSettings, AuthMode: agentconfig.GeminiNativeLogin, SelectedType: "oauth-personal", RequiredSlots: []agentconfig.GeminiSecretSlot{}, LocalAuthPolicy: agentconfig.GeminiPreserveLocalLoginV1})
	if err != nil {
		test.Fatal("no-secret declaration")
	}
	calls := 0
	validate := func(context.Context, state.ConfigurationAsset, state.ConfigurationPayload) (state.NativeGeminiPreparedData, error) {
		calls++
		return data, nil
	}
	bad := append([]byte(nil), signature...)
	bad[0] ^= 1
	if result, err := fixture.native.PrepareGemini(test.Context(), issued.Reveal(), request, bad, validate); err == nil || result.Binding().CopyWire() != nil || calls != 0 {
		test.Fatal("invalid Gemini proof reached validator")
	}
	prepared, err := fixture.native.PrepareGemini(test.Context(), issued.Reveal(), request, signature, validate)
	if err != nil || calls != 1 || len(prepared.Binding().CopyWire().RequiredSlots) != 0 {
		test.Fatal("actual no-secret native preparation")
	}
	protocol, err := fixture.native.ConfigurationProtocol(test.Context(), issued.Reveal(), request.OperationID, prepared.Binding().CopyWire().PrepareId)
	if err != nil || protocol != 3 {
		test.Fatal("authenticated Gemini family routing")
	}
	if fixture.owner.Logout(test.Context(), fixture.parent.Reveal()) != nil {
		test.Fatal("actual parent logout")
	}
	if _, err := fixture.native.GeminiConfigurationSnapshot(test.Context(), issued.Reveal(), request.OperationID, prepared.Binding().CopyWire().PrepareId); err == nil {
		test.Fatal("Gemini native child survived parent logout")
	}
}
