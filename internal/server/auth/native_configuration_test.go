package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestNativeConfigurationPrepareRealSessionAndSameNonce(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	challenge, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	fields, _ := challenge.BindFields()
	issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	if err != nil {
		test.Fatal(err)
	}
	asset, err := fixture.owner.store.CreateConfigurationAsset(test.Context(), fixture.ownerID, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: "synthetic", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{"synthetic":true}`))})
	if err != nil {
		test.Fatal(err)
	}
	adapter := nativesessionauth.AdapterFields{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: string(bytes.Repeat([]byte{'a'}, 64)), Capability: "claude.provider-switch.v1"}
	request := nativesessionauth.PrepareRequest{OwnerID: fixture.ownerID, AssetID: asset.ID, AssetRevision: 1, AgentID: "claude", SchemaVersion: 1, Adapter: adapter, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: [16]byte(start.OperationID), Nonce: [32]byte(bytes.Repeat([]byte{6}, 32))}
	keyHash := sha256.Sum256(start.PublicKey)
	wire := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: fixture.ownerID, AssetId: asset.ID, ExpectedAssetRevision: 1, AgentId: "claude", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: adapter.PluginID, PluginVersion: adapter.PluginVersion, DefinitionDigest: adapter.DefinitionDigest, RequiredCapabilities: []string{adapter.Capability}}, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: start.OperationID, ClientNonce: request.Nonce[:]}
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: fixture.ownerID, ServerOrigin: start.Origin, NativeSessionBinding: fields.NativeSessionBinding[:], OperationID: start.OperationID, ConsumerKeySHA256: keyHash[:]}, wire)
	if err != nil {
		test.Fatal(err)
	}
	patch := nativesessionauth.PatchFields{}
	for index := range patch {
		patch[index].Action = 3
	}
	validator := state.NativePrepareValidator(func(_ context.Context, _ state.ConfigurationAsset, _ state.ConfigurationPayload) (state.NativePreparedData, error) {
		return state.NativePreparedData{Adapter: adapter, Patch: patch}, nil
	})
	signature := ed25519.Sign(key, transcript.CopyBytes())
	prepared, err := fixture.native.Prepare(test.Context(), issued.Reveal(), request, signature, validator)
	if err != nil || prepared.Binding().PrepareID == "" {
		test.Fatalf("real Prepare failed: %v", err)
	}
	replayed, err := fixture.native.Prepare(test.Context(), issued.Reveal(), request, signature, validator)
	if err != nil || replayed.Binding() != prepared.Binding() {
		test.Fatal("safe Prepare replay changed identity or expiry")
	}
	authorize := nativesessionauth.AuthorizeRequest{PrepareID: prepared.Binding().PrepareID, OperationID: request.OperationID, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: prepared.Binding().ExpiresMS}
	wirePrepared := nativeWirePreparation(prepared.Binding())
	wireAuthorize := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: authorize.PrepareID, OperationId: authorize.OperationID[:], IntentCommitment: authorize.Commitment[:], Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: authorize.IntentExpiresMS}
	authorizeChallenge := prepared.Challenge()
	transcript, err = configclient.AuthorizeTranscript(wirePrepared, authorizeChallenge[:], wireAuthorize)
	if err != nil {
		test.Fatal(err)
	}
	bound, err := fixture.native.Authorize(test.Context(), issued.Reveal(), authorize, ed25519.Sign(key, transcript.CopyBytes()), validator)
	if err != nil || len(bound.Reveal()) != 0 || bound.ID() != "" || bound.Binding().Preparation != prepared.Binding() {
		test.Fatalf("native-login must bind without a grant: %v", err)
	}
	statusRequest := nativesessionauth.StatusRequest{PrepareID: authorize.PrepareID, OperationID: request.OperationID}
	transcript, err = configclient.StatusTranscript(wirePrepared, authorizeChallenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: authorize.PrepareID, OperationId: request.OperationID[:], ClientNonce: statusRequest.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	status, err := fixture.native.Status(test.Context(), issued.Reveal(), statusRequest, ed25519.Sign(key, transcript.CopyBytes()))
	if err != nil || status.State != 1 || status.GrantID != "" {
		test.Fatal("native-login status incorrectly claims a grant")
	}
}

func TestNativeGrantPublicationAfterRealCommitWithheld(test *testing.T) {
	for _, mode := range []string{"cancel", "unknown"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeServiceFixture(test)
			start, key := nativeServiceStart(fixture)
			challenge, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
			if err != nil {
				test.Fatal(err)
			}
			fields, _ := challenge.BindFields()
			issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
			if err != nil {
				test.Fatal(err)
			}
			asset, err := fixture.owner.store.CreateConfigurationAsset(test.Context(), fixture.ownerID, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: "synthetic", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{"synthetic":true}`))})
			if err != nil {
				test.Fatal(err)
			}
			adapter := nativesessionauth.AdapterFields{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: strings.Repeat("a", 64), Capability: "claude.provider-switch.v1"}
			request := nativesessionauth.PrepareRequest{OwnerID: fixture.ownerID, AssetID: asset.ID, AssetRevision: 1, AgentID: "claude", SchemaVersion: 1, Adapter: adapter, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: fields.OperationID}
			parsed, _ := nativesessionauth.ParseNativeBearer(issued.Reveal())
			credential, _ := fixture.owner.store.NativeCredentialFromBearer(parsed)
			snapshot, err := fixture.owner.store.NativeConfigurationSnapshot(test.Context(), credential, start.Origin, fields.OperationID, "")
			if err != nil {
				test.Fatal(err)
			}
			consumer := snapshot.Consumer()
			keyHash := sha256.Sum256(consumer.PublicKey[:])
			transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: fixture.ownerID, ServerOrigin: start.Origin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: fields.OperationID[:], ConsumerKeySHA256: keyHash[:]}, &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: fixture.ownerID, AssetId: asset.ID, ExpectedAssetRevision: 1, AgentId: "claude", SchemaVersion: 1, Adapter: nativeWirePreparation(nativesessionauth.PreparationFields{Consumer: consumer, Request: request}).Adapter, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: fields.OperationID[:], ClientNonce: request.Nonce[:]})
			if err != nil {
				test.Fatal(err)
			}
			var patch nativesessionauth.PatchFields
			for index := range patch {
				patch[index].Action = 3
			}
			patch[0] = nativesessionauth.PatchOperation{Action: 2, SecretSlot: 1}
			validate := func(context.Context, state.ConfigurationAsset, state.ConfigurationPayload) (state.NativePreparedData, error) {
				return state.NativePreparedData{Adapter: adapter, Patch: patch, RequiredSlot: 1}, nil
			}
			prepared, err := fixture.native.Prepare(test.Context(), issued.Reveal(), request, ed25519.Sign(key, transcript.CopyBytes()), validate)
			if err != nil {
				test.Fatal(err)
			}
			authorize := nativesessionauth.AuthorizeRequest{PrepareID: prepared.Binding().PrepareID, OperationID: fields.OperationID, RequiredSlot: 1, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: prepared.Binding().ExpiresMS}
			snapshot, err = fixture.owner.store.NativeConfigurationSnapshot(test.Context(), credential, start.Origin, fields.OperationID, authorize.PrepareID)
			if err != nil {
				test.Fatal(err)
			}
			authorizeChallenge := prepared.Challenge()
			transcript, err = configclient.AuthorizeTranscript(nativeWirePreparation(prepared.Binding()), authorizeChallenge[:], &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: authorize.PrepareID, OperationId: fields.OperationID[:], IntentCommitment: authorize.Commitment[:], RequestedSlots: []nativev1.SecretSlot{1}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: authorize.IntentExpiresMS})
			if err != nil {
				test.Fatal(err)
			}
			proof, err := fixture.native.box.value.verifier.VerifyAuthorize(parsed, snapshot.AuthorizeFields(authorize), ed25519.Sign(key, transcript.CopyBytes()))
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			calls := 0
			grant, err := nativeIssueGrant(ctx, func(token nativesessionauth.ParsedGrantToken) (state.NativeAuthorization, error) {
				calls++
				result, err := fixture.owner.store.AuthorizeNativeConfigurationGrant(ctx, credential, start.Origin, authorize, proof, token, validate)
				if err != nil {
					return result, err
				}
				if mode == "cancel" {
					cancel()
					return result, nil
				}
				return state.NativeAuthorization{}, state.ErrNativeOutcomeUnknown
			})
			if calls != 1 || len(grant.Reveal()) != 0 || grant.ID() != "" || grant.Binding().Preparation.PrepareID != "" {
				test.Fatal("postcommit canceled/unknown issuance was published or retried")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "unknown" && !errors.Is(err, state.ErrNativeOutcomeUnknown) {
				test.Fatalf("wrong withheld classification: %v", err)
			}
			database, openErr := sql.Open("sqlite3", fixture.path)
			if openErr != nil {
				test.Fatal(openErr)
			}
			defer database.Close()
			var count int
			if err := database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_materialization_grants").Scan(&count); err != nil || count != 1 {
				test.Fatal("probe did not preserve actual COMMIT")
			}
		})
	}
}

func TestNativeConfigurationIssuedGrantOpaquePrivacy(test *testing.T) {
	sentinel := []byte("synthetic-native-grant-private-72")
	grant := IssuedNativeGrant{nativeAuthOpaque: nativeAuthWrap(issuedNativeGrant{raw: sentinel})}
	if !bytes.Equal(grant.Reveal(), sentinel) {
		test.Fatal("test must contain real raw credential")
	}
	for _, container := range []any{grant, &grant, struct{ hidden IssuedNativeGrant }{grant}, []IssuedNativeGrant{grant}, map[string]any{"value": grant}} {
		for _, verb := range "vTtbcdoOqxXUeEfFgGspw" {
			for _, prefix := range []string{"%", "%#", "%+", "%020.10"} {
				rendered := fmt.Sprintf(prefix+string(verb), container)
				if strings.Contains(rendered, string(sentinel)) || strings.Contains(rendered, fmt.Sprintf("%x", sentinel)) || strings.Contains(rendered, fmt.Sprint(sentinel)) {
					test.Fatal("credential formatting leaked")
				}
			}
		}
		for _, json := range []bool{false, true} {
			var output bytes.Buffer
			var handler slog.Handler = slog.NewTextHandler(&output, nil)
			if json {
				handler = slog.NewJSONHandler(&output, nil)
			}
			slog.New(handler).Info("synthetic", slog.Any("value", container))
			if strings.Contains(output.String(), string(sentinel)) || strings.Contains(output.String(), fmt.Sprintf("%x", sentinel)) {
				test.Fatal("real slog credential leak")
			}
		}
	}
}

func nativeWirePreparation(binding nativesessionauth.PreparationFields) *nativev1.PreparationBinding {
	request, consumer := binding.Request, binding.Consumer
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	var slots []nativev1.SecretSlot
	if binding.RequiredSlot != 0 {
		slots = []nativev1.SecretSlot{nativev1.SecretSlot(binding.RequiredSlot)}
	}
	return &nativev1.PreparationBinding{OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationId: consumer.OperationID[:], AssetId: request.AssetID, AssetRevision: request.AssetRevision, AgentId: request.AgentID, AssetKind: 1, SchemaVersion: request.SchemaVersion, Adapter: &nativev1.AdapterBinding{ApiVersion: request.Adapter.APIVersion, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: nativev1.Target(request.Target), Scope: nativev1.Scope(request.Scope), ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, RequiredSlots: slots, PrepareExpiresAtUnixMs: binding.ExpiresMS, ConsumerKeySha256: keyHash[:], PrepareId: binding.PrepareID, PreparedAtUnixMs: binding.PreparedMS, PatchSha256: binding.PatchSHA256[:]}
}
