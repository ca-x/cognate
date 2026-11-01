package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
)

type geminiStateFlow struct {
	base     nativeConfigurationFlow
	consumer nativesessionauth.ConsumerFields
	request  nativesessionauth.GeminiPrepareRequest
	proof    nativesessionauth.VerifiedApplyProof
	data     NativeGeminiPreparedData
}

func newGeminiStateFlow(test *testing.T) *geminiStateFlow {
	test.Helper()
	base := newNativeConfigurationFlow(test)
	if _, err := base.fixture.store.state.client.NativeApplyPreparation.Delete().Exec(test.Context()); err != nil {
		test.Fatal("remove unrelated test preparation")
	}
	asset, err := base.fixture.store.CreateConfigurationAsset(test.Context(), "alice", ConfigurationAssetDraft{AgentID: "gemini", Kind: ConfigurationKindProvider, Name: "synthetic Gemini state", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-gemini-state-material"))})
	if err != nil {
		test.Fatal("encrypted Gemini asset")
	}
	snapshot, err := base.fixture.store.NativeGeminiConfigurationSnapshot(test.Context(), base.credential, base.requestOrigin(), base.request.OperationID, "")
	if err != nil {
		test.Fatal("actual native-purpose consumer")
	}
	request := nativesessionauth.GeminiPrepareRequest{PrepareRequest: base.request, ContractVersion: 1}
	request.AssetID, request.AssetRevision, request.AgentID, request.Target = asset.ID, asset.Revision, "gemini", 3
	request.Adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: "builtin.gemini", PluginVersion: "1.1.0", DefinitionDigest: nativesessionauth.GeminiDefinitionDigest, Capability: "gemini.provider-plan.v1"}
	request.Nonce = [32]byte(bytes.Repeat([]byte{6}, 32))
	declaration := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: agentconfig.GeminiSettings, AuthMode: agentconfig.GeminiAPIKeyAuth, SelectedType: "gemini-api-key", CredentialReference: new(agentconfig.GeminiAPIKeySecret), RequiredSlots: []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}, LocalAuthPolicy: agentconfig.GeminiPreserveLocalLoginV1, KeyTransport: new(agentconfig.GeminiNativeDefault)}
	data, err := NewNativeGeminiPreparedData(request.Adapter, declaration)
	if err != nil {
		test.Fatal("typed Gemini declaration")
	}
	flow := &geminiStateFlow{base: base, consumer: snapshot.Consumer(), request: request, data: data}
	flow.proof = flow.evidence(test, request)
	return flow
}
func (flow *geminiStateFlow) evidence(test *testing.T, request nativesessionauth.GeminiPrepareRequest) nativesessionauth.VerifiedApplyProof {
	test.Helper()
	consumer := flow.consumer
	hash := sha256.Sum256(consumer.PublicKey[:])
	adapter := request.Adapter
	wire := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: "gemini", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: adapter.PluginID, PluginVersion: adapter.PluginVersion, DefinitionDigest: adapter.DefinitionDigest, RequiredCapabilities: []string{adapter.Capability}}, Target: 3, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:], GeminiContractVersion: 1}
	transcript, err := configclient.GeminiPrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: hash[:]}, wire)
	if err != nil {
		test.Fatal("independent Gemini client proof")
	}
	proof, err := flow.base.verifier.VerifyGeminiPrepare(flow.base.bearer, nativesessionauth.GeminiPrepareFields{Consumer: consumer, Request: request}, ed25519.Sign(flow.base.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("actual verifier signature")
	}
	return proof
}
func (flow *geminiStateFlow) prepare(test *testing.T) NativeGeminiPrepared {
	test.Helper()
	prepared, err := flow.base.fixture.store.PrepareNativeGeminiApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request, flow.proof, func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeGeminiPreparedData, error) {
		return flow.data, nil
	})
	if err != nil {
		test.Fatal("real SQLite Gemini preparation")
	}
	return prepared
}
func TestGeminiStatePrepareOwnerRevisionAndProof(test *testing.T) {
	flow := newGeminiStateFlow(test)
	store := flow.base.fixture.store
	calls := 0
	validate := func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeGeminiPreparedData, error) {
		calls++
		return flow.data, nil
	}
	if _, err := store.PrepareNativeGeminiApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request, nativesessionauth.VerifiedApplyProof{}, validate); !errors.Is(err, ErrNativeUnauthorized) || calls != 0 {
		test.Fatal("invalid proof reached payload validation")
	}
	if store.CreateUser(test.Context(), "bob") != nil {
		test.Fatal("second owner")
	}
	foreign, err := store.CreateConfigurationAsset(test.Context(), "bob", ConfigurationAssetDraft{AgentID: "gemini", Kind: ConfigurationKindProvider, Name: "other owner", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-foreign-state-material"))})
	if err != nil {
		test.Fatal("foreign encrypted asset")
	}
	request := flow.request
	request.AssetID = foreign.ID
	if result, err := store.PrepareNativeGeminiApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, request, flow.evidence(test, request), validate); err == nil || result.Binding().CopyWire() != nil || calls != 0 {
		test.Fatal("cross-owner asset reached validator")
	}
	request = flow.request
	request.AssetRevision++
	if result, err := store.PrepareNativeGeminiApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, request, flow.evidence(test, request), validate); !errors.Is(err, ErrConfigurationConflict) || result.Binding().CopyWire() != nil || calls != 0 {
		test.Fatal("stale asset revision prepared")
	}
	first := flow.prepare(test)
	second := flow.prepare(test)
	if first.Binding().CopyWire().PrepareId != second.Binding().CopyWire().PrepareId || first.Challenge() != second.Challenge() {
		test.Fatal("same nonce preparation identity changed")
	}
	request = flow.request
	request.Nonce[0] ^= 1
	if _, err := store.PrepareNativeGeminiApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, request, flow.evidence(test, request), validate); !errors.Is(err, ErrConfigurationConflict) {
		test.Fatal("changed original nonce replayed preparation")
	}
}
