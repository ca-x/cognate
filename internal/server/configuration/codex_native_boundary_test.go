package configuration

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func TestCodexAssetNativeDenied(test *testing.T) {
	fixture := newNativeOrderFixture(test, APIKey)
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, Draft{Name: "Codex not native", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://provider.example/v1", Model: "model-test"}}, []SecretMutation{{Slot: CodexProviderKeySlot, Action: Replace, Replacement: NewSecret([]byte("synthetic-codex-native-boundary"))}})
	if err != nil {
		test.Fatal(err)
	}
	if _, _, err := fixture.domain.load(test.Context(), fixture.owner, asset.Metadata.ID); !errors.Is(err, ErrUnsupported) {
		test.Fatal("Claude native loader must remain closed to Codex")
	}
	snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal("positive control must prove valid native consumer and prepare signature")
	}
	consumer := snapshot.Consumer()
	request := fixture.prepare
	request.AssetID = asset.Metadata.ID
	request.AssetRevision = asset.Metadata.Revision
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wirePrepareRequest(request))
	if err != nil {
		test.Fatal(err)
	}
	signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
	if _, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, request, signature); err != nil {
		test.Fatal("Codex asset identifier must reach business gate with a valid proof")
	}
	if _, err := fixture.native.Prepare(test.Context(), fixture.bearer, request, signature); !errors.Is(err, ErrUnsupported) {
		test.Fatal("validly signed Claude request must not prepare a Codex asset")
	}
	database, err := sql.Open("sqlite", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, query := range []string{"SELECT count(*) FROM native_apply_preparations", "SELECT count(*) FROM native_materialization_grants"} {
		var count int
		if err := database.QueryRowContext(test.Context(), query).Scan(&count); err != nil || count != 0 {
			test.Fatal("denied Codex prepare must not allocate native authorization rows")
		}
	}
}

func codexBoundaryRequest(test *testing.T, fixture *nativeOrderFixture, asset state.ConfigurationAsset) (nativesessionauth.CodexPrepareRequest, []byte) {
	test.Helper()
	snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal("positive native authentication failed")
	}
	consumer := snapshot.Consumer()
	pin := configclient.CodexNativeAdapterPin()
	request := nativesessionauth.CodexPrepareRequest{PrepareRequest: fixture.prepare, ContractVersion: 1}
	request.AssetID, request.AssetRevision, request.AgentID, request.Target = asset.ID, asset.Revision, "codex", 2
	request.Adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, Capability: "codex.provider-plan.v1"}
	wire := wirePrepareRequest(request.PrepareRequest)
	wire.CodexContractVersion = 1
	transcript, err := configclient.CodexPrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wire)
	if err != nil {
		test.Fatal("independent Codex request encoder failed")
	}
	return request, ed25519.Sign(fixture.key, transcript.CopyBytes())
}

func TestCodexNativeBoundaryUnsupportedAndCopy(test *testing.T) {
	const envelope = `{"version":1,"variant":"codex-provider","provider":{"authMode":"provider-key","endpoint":"https://boundary.invalid/v1","model":"alpha"},"secrets":{"provider_key":"synthetic-boundary-key"}}`
	for _, mode := range []string{"native-login", "missing-key", "copy-no-key", "extras-null", "unknown-kind", "unknown-schema", "unknown-agent"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeOrderFixture(test, APIKey)
			store := fixture.domain.store.(*state.Store)
			raw := envelope
			draft := state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "synthetic-rejection", SchemaVersion: 1}
			switch mode {
			case "native-login":
				raw = `{"version":1,"variant":"codex-provider","provider":{"authMode":"native-login"},"secrets":{}}`
			case "missing-key":
				raw = strings.Replace(raw, `"provider_key":"synthetic-boundary-key"`, "", 1)
			case "extras-null":
				raw = strings.Replace(raw, `"version":1`, `"future":null,"version":1`, 1)
			case "unknown-kind":
				draft.Kind = state.ConfigurationKindMCP
			case "unknown-schema":
				draft.SchemaVersion = 2
			case "unknown-agent":
				draft.AgentID = "future-agent"
			}
			draft.Payload = state.NewConfigurationPayload([]byte(raw))
			asset, err := store.CreateConfigurationAsset(test.Context(), fixture.owner, draft)
			if err != nil {
				test.Fatal("cannot persist rejection fixture")
			}
			if mode == "copy-no-key" {
				copied, err := fixture.domain.Copy(test.Context(), fixture.owner, asset.ID, asset.Revision, "copy-without-key")
				if err != nil {
					test.Fatal("real typed asset Copy failed")
				}
				asset = copied.Metadata
			}
			request, signature := codexBoundaryRequest(test, fixture, asset)
			prepared, err := fixture.native.PrepareCodex(test.Context(), fixture.bearer, request, signature)
			if err == nil || prepared.Binding().CopyWire() != nil || strings.Contains(err.Error(), "synthetic-boundary-key") {
				test.Fatal("unsupported/copy asset gained native authorization or leaked key")
			}
			database, err := sql.Open("sqlite", fixture.path)
			if err != nil {
				test.Fatal("cannot inspect actual poststate")
			}
			defer database.Close()
			var count int
			if database.QueryRowContext(test.Context(), "SELECT count(*) FROM native_apply_preparations").Scan(&count) != nil || count != 0 {
				test.Fatal("rejected asset allocated preparation")
			}
		})
	}
}

func TestCodexNativeBoundaryCapturedInputCAS(test *testing.T) {
	fixture := newNativeOrderFixture(test, APIKey)
	draft := Draft{Name: "captured-Codex", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://captured.invalid/v1", Model: "alpha", Catalog: &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "alpha", SupportsParallelToolCalls: new(false)}}}}}
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, draft, []SecretMutation{{Slot: CodexProviderKeySlot, Action: Replace, Replacement: NewSecret([]byte("synthetic-capture-key"))}})
	if err != nil {
		test.Fatal("cannot create captured input fixture")
	}
	request, signature := codexBoundaryRequest(test, fixture, asset.Metadata)
	if _, err := fixture.consumer.PreflightCodexPrepare(test.Context(), fixture.bearer, request, signature); err != nil {
		test.Fatal("preflight before plugin failed")
	}
	validate, declaration, err := fixture.native.nativeCodexPlan(test.Context(), request)
	if err != nil || declaration.Catalog.Models[0].SupportsParallelToolCalls == nil || *declaration.Catalog.Models[0].SupportsParallelToolCalls {
		test.Fatal("real A plugin failed to capture complete optional input")
	}
	draft.CodexProvider.Catalog.Models[0].SupportsParallelToolCalls = nil
	secret := NewSecret([]byte("synthetic-capture-key"))
	defer secret.Clear()
	different, err := encodeCodexEnvelope(*draft.CodexProvider, secret)
	if err != nil {
		test.Fatal("cannot encode changed optional presence")
	}
	if result, err := validate(test.Context(), asset.Metadata, different); !errors.Is(err, state.ErrConfigurationConflict) || !reflect.DeepEqual(result, state.NativeCodexPreparedData{}) {
		test.Fatal("same metadata hid changed captured desired input")
	}
	if _, err := fixture.domain.Update(test.Context(), fixture.owner, asset.Metadata.ID, asset.Metadata.Revision, draft, nil); err != nil {
		test.Fatal("real concurrent asset CAS update failed")
	}
	if result, err := fixture.consumer.PrepareCodex(test.Context(), fixture.bearer, request, signature, validate); !errors.Is(err, state.ErrConfigurationConflict) || result.Binding().CopyWire() != nil {
		test.Fatal("post-plugin transaction accepted stale asset revision")
	}
}

func TestCodexNativeBoundaryCASAndAuth(test *testing.T) {
	fixture := newNativeOrderFixture(test, APIKey)
	secret := []byte("synthetic-codex-native-boundary-key")
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, Draft{Name: "Codex explicit native", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://provider.invalid/v1", Model: "alpha"}}, []SecretMutation{{Slot: CodexProviderKeySlot, Action: Replace, Replacement: NewSecret(secret)}})
	if err != nil {
		test.Fatal("cannot create typed encrypted Codex asset")
	}
	snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal("real native credential failed")
	}
	consumer := snapshot.Consumer()
	pin := configclient.CodexNativeAdapterPin()
	request := nativesessionauth.CodexPrepareRequest{PrepareRequest: fixture.prepare, ContractVersion: 1}
	request.AssetID = asset.Metadata.ID
	request.AssetRevision = asset.Metadata.Revision
	request.AgentID = "codex"
	request.Target = 2
	request.Adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, Capability: "codex.provider-plan.v1"}
	wire := wirePrepareRequest(request.PrepareRequest)
	wire.CodexContractVersion = 1
	transcript, err := configclient.CodexPrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wire)
	if err != nil {
		test.Fatal("client independent prepare failed")
	}
	signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
	probe := &nativeOrderReadProbe{Store: fixture.native.domain.store}
	fixture.native.domain.store = probe
	bad := bytes.Clone(signature)
	bad[0] ^= 1
	if result, err := fixture.native.PrepareCodex(test.Context(), fixture.bearer, request, bad); err == nil || result.Binding().CopyWire() != nil || probe.reads != 0 {
		test.Fatal("invalid Codex proof reached domain asset read")
	}
	prepared, err := fixture.native.PrepareCodex(test.Context(), fixture.bearer, request, signature)
	if err != nil || prepared.Binding().CopyWire() == nil || probe.reads != 1 {
		test.Fatal("real moejs Codex preparation failed")
	}
	declaration := prepared.CopyDeclaration()
	if declaration.Endpoint != "https://provider.invalid/v1" || declaration.CredentialReference != agentconfig.CodexProviderKeySecret || len(declaration.RequiredSlots) != 1 {
		test.Fatal("full A declaration not returned")
	}
	local := &nativev1.CodexLocalBindingV1{BindingVersion: 1, NativeProfileId: "synthetic-protocol-only", NativeProfileSha256: bytes.Repeat([]byte{1}, 32), TemplateSha256: bytes.Repeat([]byte{2}, 32), EffectiveScope: 1, ProviderOwnership: 1, CatalogOwnership: 1, AuthStore: 2, KeyringState: 1, FileLoginState: 4, LocalAuthPolicy: "preserve-local-login-v1", RequiresOpenaiAuth: new(true), ObservationWitness: bytes.Repeat([]byte{3}, 32), Baselines: []*nativev1.CodexRoleBaseline{{Role: 1, State: &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}}, {Role: 2, State: &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}}, {Role: 3, State: &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}}}}
	expiry := prepared.Binding().CopyWire().PrepareExpiresAtUnixMs
	commitment, err := configclient.HashCodexIntent(prepared.Binding().CopyWire(), local, 1, expiry)
	if err != nil {
		test.Fatal("client complete intent failed")
	}
	authorize := nativesessionauth.AuthorizeRequest{PrepareID: prepared.Binding().CopyWire().PrepareId, OperationID: request.OperationID, Commitment: commitment, RequiredSlot: 3, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: expiry}
	authorizeChallenge := prepared.Challenge()
	transcript, err = configclient.CodexAuthorizeTranscript(prepared.Binding().CopyWire(), authorizeChallenge[:], &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: authorize.PrepareID, OperationId: authorize.OperationID[:], IntentCommitment: commitment[:], RequestedSlots: []nativev1.SecretSlot{3}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: expiry, CodexLocalBinding: local})
	if err != nil {
		test.Fatal("client authorize failed")
	}
	grant, err := fixture.native.AuthorizeCodex(test.Context(), fixture.bearer, authorize, local, ed25519.Sign(fixture.key, transcript.CopyBytes()))
	if err != nil || grant.ID() == "" {
		test.Fatal("real Codex domain authorization failed")
	}
	defer grant.Destroy()
	token, _ := nativesessionauth.ParseGrantToken(grant.Reveal())
	consume := nativesessionauth.ConsumeRequest{PrepareID: authorize.PrepareID, GrantID: grant.ID(), Token: token, OperationID: request.OperationID, Commitment: commitment}
	consumeChallenge := grant.Challenge()
	transcript, err = configclient.CodexConsumeTranscript(grant.Binding().CopyWire(), authorizeChallenge[:], consumeChallenge[:], grant.ExpiresMS(), &nativev1.ConsumeNativeMaterializationRequest{PrepareId: consume.PrepareID, GrantId: consume.GrantID, GrantToken: grant.Reveal(), OperationId: consume.OperationID[:], IntentCommitment: commitment[:], ClientNonce: consume.Nonce[:]})
	if err != nil {
		test.Fatal("client consume failed")
	}
	material, err := fixture.native.Consume(test.Context(), fixture.bearer, consume, ed25519.Sign(fixture.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("authenticated Codex route failed")
	}
	defer material.Destroy()
	encoded := material.Reveal()
	defer clear(encoded)
	response := new(nativev1.ConsumeNativeMaterializationResponse)
	if proto.Unmarshal(encoded, response) != nil || len(response.SlotValues) != 1 || response.SlotValues[0].Slot != 3 || !bytes.Equal(response.SlotValues[0].Plaintext, secret) || !proto.Equal(response.Binding, grant.Binding().CopyWire()) {
		test.Fatal("single exact slot3 bytes/binding changed")
	}
	status := nativesessionauth.StatusRequest{PrepareID: consume.PrepareID, GrantID: consume.GrantID, OperationID: request.OperationID}
	transcript, err = configclient.CodexStatusTranscript(prepared.Binding().CopyWire(), authorizeChallenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: status.PrepareID, GrantId: status.GrantID, OperationId: status.OperationID[:], ClientNonce: status.Nonce[:]})
	if err != nil {
		test.Fatal("status transcript failed")
	}
	result, err := fixture.native.Status(test.Context(), fixture.bearer, status, ed25519.Sign(fixture.key, transcript.CopyBytes()))
	if err != nil || result.State != 3 {
		test.Fatal("safe Codex Status failed")
	}
}
