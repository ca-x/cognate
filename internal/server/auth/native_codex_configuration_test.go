package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestCodexNativeAuthLifecycle(test *testing.T) {
	for _, mode := range []string{"success", "cancel-after-commit", "unknown-after-commit"} {
		test.Run(mode, func(test *testing.T) { codexNativeAuthLifecycle(test, mode) })
	}
}

func codexNativeAuthLifecycle(test *testing.T, mode string) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	challenge, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal("real parent Begin failed")
	}
	fields, _ := challenge.BindFields()
	issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	if err != nil {
		test.Fatal("real parent Bind failed")
	}
	asset, err := fixture.owner.store.CreateConfigurationAsset(test.Context(), fixture.ownerID, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "synthetic-c2", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("synthetic-auth-material"))})
	if err != nil {
		test.Fatal("cannot create synthetic asset")
	}
	pin := configclient.CodexNativeAdapterPin()
	adapter := nativesessionauth.AdapterFields{APIVersion: 1, PluginID: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, Capability: "codex.provider-plan.v1"}
	request := nativesessionauth.CodexPrepareRequest{PrepareRequest: nativesessionauth.PrepareRequest{OwnerID: fixture.ownerID, AssetID: asset.ID, AssetRevision: asset.Revision, AgentID: "codex", SchemaVersion: 1, Adapter: adapter, Target: 2, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: fields.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{6}, 32))}, ContractVersion: 1}
	keyHash := sha256.Sum256(start.PublicKey)
	wire := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: "codex", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: adapter.PluginID, PluginVersion: adapter.PluginVersion, DefinitionDigest: adapter.DefinitionDigest, RequiredCapabilities: []string{adapter.Capability}}, Target: 2, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: 1, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:], CodexContractVersion: 1}
	transcript, err := configclient.CodexPrepareTranscript(configclient.ConsumerBinding{OwnerID: request.OwnerID, ServerOrigin: start.Origin, NativeSessionBinding: fields.NativeSessionBinding[:], OperationID: start.OperationID, ConsumerKeySHA256: keyHash[:]}, wire)
	if err != nil {
		test.Fatal("prepare canonical builder failed")
	}
	signature := ed25519.Sign(key, transcript.CopyBytes())
	declaration := agentconfig.CodexProviderDeclarationV1{APIVersion: 1, Target: agentconfig.CodexConfig, ProviderID: "cognate", WireAPI: "responses", Endpoint: "https://synthetic.invalid/v1", Model: "alpha", CredentialReference: agentconfig.CodexProviderKeySecret, RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, LocalAuthPolicy: agentconfig.CodexPreserveLocalLoginV1}
	data, err := state.NewNativeCodexPreparedData(adapter, declaration)
	if err != nil {
		test.Fatal("typed data failed")
	}
	callbacks := 0
	validate := func(context.Context, state.ConfigurationAsset, state.ConfigurationPayload) (state.NativeCodexPreparedData, error) {
		callbacks++
		return data, nil
	}
	invalid := bytes.Clone(signature)
	invalid[0] ^= 1
	if result, err := fixture.native.PrepareCodex(test.Context(), issued.Reveal(), request, invalid, validate); err == nil || result.Binding().CopyWire() != nil || callbacks != 0 {
		test.Fatal("invalid proof reached validator")
	}
	if _, err := fixture.native.PreflightCodexPrepare(test.Context(), issued.Reveal(), request, signature); err != nil {
		test.Fatal("prepare preflight failed")
	}
	prepared, err := fixture.native.PrepareCodex(test.Context(), issued.Reveal(), request, signature, validate)
	if err != nil || callbacks != 1 {
		test.Fatal("real Codex Prepare failed")
	}
	protocol, err := fixture.native.ConfigurationProtocol(test.Context(), issued.Reveal(), request.OperationID, prepared.Binding().CopyWire().PrepareId)
	if err != nil || protocol != 2 {
		test.Fatal("authenticated record family route failed")
	}
	digest, _ := nativesessionauth.HashCodexDeclaration(declaration)
	localWire := &nativev1.CodexLocalBindingV1{BindingVersion: 1, NativeProfileId: "synthetic-only", NativeProfileSha256: bytes.Repeat([]byte{1}, 32), TemplateSha256: bytes.Repeat([]byte{2}, 32), EffectiveScope: 1, ProviderOwnership: 1, CatalogOwnership: 1, AuthStore: 2, KeyringState: 1, FileLoginState: 4, LocalAuthPolicy: "preserve-local-login-v1", RequiresOpenaiAuth: new(true), ObservationWitness: bytes.Repeat([]byte{3}, 32), Baselines: []*nativev1.CodexRoleBaseline{{Role: 1, State: &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}}, {Role: 2, State: &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}}, {Role: 3, State: &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}}}}
	local, err := nativesessionauth.NewCodexLocalBinding(digest, localWire)
	if err != nil {
		test.Fatal("synthetic protocol projection failed")
	}
	expiry := prepared.Binding().CopyWire().PrepareExpiresAtUnixMs
	commitment, err := configclient.HashCodexIntent(prepared.Binding().CopyWire(), localWire, 1, expiry)
	if err != nil {
		test.Fatal("independent client intent failed")
	}
	authorize := nativesessionauth.CodexAuthorizeRequest{AuthorizeRequest: nativesessionauth.AuthorizeRequest{PrepareID: prepared.Binding().CopyWire().PrepareId, OperationID: request.OperationID, Commitment: commitment, RequiredSlot: 3, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: expiry, Nonce: [32]byte(bytes.Repeat([]byte{9}, 32))}, Local: local}
	authorizeChallenge := prepared.Challenge()
	authorizeWire := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: authorize.PrepareID, OperationId: authorize.OperationID[:], IntentCommitment: commitment[:], RequestedSlots: []nativev1.SecretSlot{3}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: expiry, CodexLocalBinding: localWire}
	transcript, err = configclient.CodexAuthorizeTranscript(prepared.Binding().CopyWire(), authorizeChallenge[:], authorizeWire)
	if err != nil {
		test.Fatal("authorize canonical failed")
	}
	signature = ed25519.Sign(key, transcript.CopyBytes())
	if _, err := fixture.native.PreflightCodexAuthorize(test.Context(), issued.Reveal(), authorize, signature); err != nil {
		test.Fatal("authorize preflight failed")
	}
	if mode != "success" {
		credential, proof, err := fixture.native.codexAuthorizeEvidence(test.Context(), issued.Reveal(), authorize, signature)
		if err != nil {
			test.Fatal("real authoritative evidence failed")
		}
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		calls := 0
		grant, err := nativeIssueCodexGrant(ctx, func(token nativesessionauth.ParsedGrantToken) (state.NativeCodexAuthorization, error) {
			calls++
			result, err := fixture.owner.store.AuthorizeNativeCodexGrant(ctx, credential, start.Origin, authorize, proof, token, validate)
			if err != nil {
				return result, err
			}
			if mode == "cancel-after-commit" {
				cancel()
				return result, nil
			}
			return state.NativeCodexAuthorization{}, state.ErrNativeOutcomeUnknown
		})
		if calls != 1 || grant.ID() != "" || len(grant.Reveal()) != 0 || grant.Binding().CopyWire() != nil {
			test.Fatal("late canceled/unknown issuance leaked result or replayed")
		}
		if mode == "cancel-after-commit" && !errors.Is(err, context.Canceled) || mode == "unknown-after-commit" && !errors.Is(err, state.ErrNativeOutcomeUnknown) {
			test.Fatal("wrong late receipt classification")
		}
		database, err := sql.Open("sqlite3", fixture.path)
		if err != nil {
			test.Fatal("cannot inspect actual committed state")
		}
		defer database.Close()
		var count int
		if database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_materialization_grants").Scan(&count) != nil || count != 1 {
			test.Fatal("late receipt control did not actually COMMIT exactly one grant")
		}
		return
	}
	grant, err := fixture.native.AuthorizeCodex(test.Context(), issued.Reveal(), authorize, signature, validate)
	if err != nil || len(grant.Reveal()) != 32 {
		test.Fatal("real Codex grant failed")
	}
	defer grant.Destroy()
	copyToken := grant.Reveal()
	copyToken[0] ^= 1
	if bytes.Equal(copyToken, grant.Reveal()) {
		test.Fatal("grant Reveal aliases private token")
	}
	for _, value := range []any{grant, &grant, struct{ hidden IssuedCodexNativeGrant }{grant}, []IssuedCodexNativeGrant{grant}} {
		for _, verb := range []string{"%#v", "%t", "%b", "%x", "%d", "%s", "%q", "%+v", "%.128v"} {
			output := fmt.Sprintf(verb, value)
			if strings.Contains(output, hex.EncodeToString(grant.Reveal())) || strings.Contains(output, fmt.Sprint(grant.Reveal())) || strings.Contains(output, "synthetic-auth-material") {
				test.Fatal("grant private container leaks")
			}
		}
	}
	token, _ := nativesessionauth.ParseGrantToken(grant.Reveal())
	consume := nativesessionauth.ConsumeRequest{PrepareID: authorize.PrepareID, GrantID: grant.ID(), Token: token, OperationID: request.OperationID, Commitment: commitment, Nonce: [32]byte(bytes.Repeat([]byte{12}, 32))}
	consumeChallenge := grant.Challenge()
	consumeWire := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: consume.PrepareID, GrantId: consume.GrantID, GrantToken: grant.Reveal(), OperationId: consume.OperationID[:], IntentCommitment: commitment[:], ClientNonce: consume.Nonce[:]}
	transcript, err = configclient.CodexConsumeTranscript(grant.Binding().CopyWire(), authorizeChallenge[:], consumeChallenge[:], grant.ExpiresMS(), consumeWire)
	if err != nil {
		test.Fatal("consume canonical failed")
	}
	signature = ed25519.Sign(key, transcript.CopyBytes())
	if _, err := fixture.native.PreflightCodexConsume(test.Context(), issued.Reveal(), consume, signature); err != nil {
		test.Fatal("consume preflight failed")
	}
	materialize := func(_ context.Context, _ state.ConfigurationAsset, payload state.ConfigurationPayload, _ nativesessionauth.CodexApply, _ string, _ int64) (state.NativeMaterialization, error) {
		body := payload.Reveal()
		defer clear(body)
		return state.NewNativeMaterialization(body)
	}
	material, err := fixture.native.ConsumeCodex(test.Context(), issued.Reveal(), consume, signature, materialize)
	if err != nil || !bytes.Equal(material.Reveal(), []byte("synthetic-auth-material")) {
		test.Fatal("real consume failed")
	}
	material.Destroy()
	status := nativesessionauth.StatusRequest{PrepareID: consume.PrepareID, GrantID: consume.GrantID, OperationID: request.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{13}, 32))}
	transcript, err = configclient.CodexStatusTranscript(prepared.Binding().CopyWire(), authorizeChallenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: status.PrepareID, GrantId: status.GrantID, OperationId: status.OperationID[:], ClientNonce: status.Nonce[:]})
	if err != nil {
		test.Fatal("status canonical failed")
	}
	result, err := fixture.native.StatusCodex(test.Context(), issued.Reveal(), status, ed25519.Sign(key, transcript.CopyBytes()))
	if err != nil || result.State != 3 {
		test.Fatal("safe status failed")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if result, err := fixture.native.PrepareCodex(ctx, issued.Reveal(), request, signature, validate); err == nil || result.Binding().CopyWire() != nil {
		test.Fatal("canceled request admitted")
	}
}
