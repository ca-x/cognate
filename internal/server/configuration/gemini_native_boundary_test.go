package configuration

import (
	"crypto/ed25519"
	"errors"
	"reflect"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestGeminiBoundaryRealMoejsCapturedCAS(test *testing.T) {
	fixture := newNativeOrderFixture(test, APIKey)
	draft := Draft{Name: "captured Gemini", GeminiProvider: &GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://provider.invalid/v1beta"), KeyTransport: new(agentconfig.GeminiNativeDefault), EnvModel: new("env-original"), SettingsModel: new("settings-original")}}
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, draft, []SecretMutation{{Slot: GeminiAPIKeySlot, Action: Replace, Replacement: NewSecret([]byte("synthetic-boundary-Gemini-key"))}})
	if err != nil {
		test.Fatal("encrypted Gemini domain asset")
	}
	snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal("real native session positive control")
	}
	consumer := snapshot.Consumer()
	pin := configclient.GeminiNativeAdapterPin()
	request := nativesessionauth.GeminiPrepareRequest{PrepareRequest: fixture.prepare, ContractVersion: 1}
	request.AssetID, request.AssetRevision, request.AgentID, request.Target = asset.Metadata.ID, asset.Metadata.Revision, "gemini", 3
	request.Adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, Capability: "gemini.provider-plan.v1"}
	wire := wirePrepareRequest(request.PrepareRequest)
	wire.GeminiContractVersion = 1
	transcript, err := configclient.GeminiPrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wire)
	if err != nil {
		test.Fatal("independent Gemini wire proof")
	}
	signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
	probe := &nativeOrderReadProbe{Store: fixture.native.domain.store}
	fixture.native.domain.store = probe
	bad := append([]byte(nil), signature...)
	bad[0] ^= 1
	if result, err := fixture.native.PrepareGemini(test.Context(), fixture.bearer, request, bad); err == nil || result.Binding().CopyWire() != nil || probe.reads != 0 {
		test.Fatal("invalid signature reached asset plaintext")
	}
	validate, declaration, err := fixture.native.nativeGeminiPlan(test.Context(), request)
	if err != nil || declaration.EnvModel == nil || *declaration.EnvModel != "env-original" || declaration.SettingsModel == nil || *declaration.SettingsModel != "settings-original" || len(declaration.RequiredSlots) != 1 {
		test.Fatal("real moejs omitted independent Gemini fields")
	}
	draft.GeminiProvider.SettingsModel = nil
	secret := NewSecret([]byte("synthetic-boundary-Gemini-key"))
	defer secret.Clear()
	payload, err := encodeGeminiEnvelope(*draft.GeminiProvider, secret)
	if err != nil {
		test.Fatal("changed optional-presence fixture")
	}
	if result, err := validate(test.Context(), asset.Metadata, payload); !errors.Is(err, state.ErrConfigurationConflict) || !reflect.DeepEqual(result, state.NativeGeminiPreparedData{}) {
		test.Fatal("same metadata hid changed desired input")
	}
	if _, err := fixture.domain.Update(test.Context(), fixture.owner, asset.Metadata.ID, asset.Metadata.Revision, draft, nil); err != nil {
		test.Fatal("real concurrent SQLite CAS update")
	}
	if result, err := fixture.consumer.PrepareGemini(test.Context(), fixture.bearer, request, signature, validate); !errors.Is(err, state.ErrConfigurationConflict) || result.Binding().CopyWire() != nil {
		test.Fatal("final transaction ignored stale captured revision")
	}
}
