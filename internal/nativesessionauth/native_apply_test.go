package nativesessionauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
)

func TestNativeApplyPrepareCanonicalParityAndEvidence(test *testing.T) {
	master := bytes.Repeat([]byte{5}, 32)
	verifier, _ := NewVerifier(master)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	bearer, _ := ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	principal := ConsumerFields{OwnerID: "alice", ServerOrigin: "https://cognate.example", PublicKey: [32]byte(key.Public().(ed25519.PublicKey))}
	request := PrepareRequest{OwnerID: "alice", AssetID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", AssetRevision: 1, AgentID: "claude", SchemaVersion: 1, Adapter: AdapterFields{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: string(bytes.Repeat([]byte{'a'}, 64)), Capability: "claude.provider-switch.v1"}, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1}
	wire := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: 1, AgentId: "claude", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:]}
	keyHash := sha256.Sum256(principal.PublicKey[:])
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: "alice", ServerOrigin: principal.ServerOrigin, NativeSessionBinding: principal.SessionBinding[:], OperationID: principal.OperationID[:], ConsumerKeySHA256: keyHash[:]}, wire)
	if err != nil {
		test.Fatal(err)
	}
	signature := ed25519.Sign(key, transcript.CopyBytes())
	fields := PrepareFields{Consumer: principal, Request: request}
	proof, err := verifier.VerifyPrepare(bearer, fields, signature)
	digest, hashErr := PrepareFieldsDigest(fields)
	nativeDigest, _ := bearer.TokenDigest()
	if err != nil || hashErr != nil || !proof.MatchesApply(verifier.payload.value.keyID, nativeDigest, "prepare", digest) {
		test.Fatal("server bridge does not match frozen client canonical")
	}
	fields.Request.SelectionRevision++
	if _, err := verifier.VerifyPrepare(bearer, fields, signature); err == nil {
		test.Fatal("field substitution accepted")
	}
}

func TestNativeApplyAuthorizationConsumptionAndStatusParity(test *testing.T) {
	master := bytes.Repeat([]byte{5}, 32)
	verifier, _ := NewVerifier(master)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	bearer, _ := ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	principal := ConsumerFields{OwnerID: "alice", ServerOrigin: "https://cognate.example", PublicKey: [32]byte(key.Public().(ed25519.PublicKey))}
	request := PrepareRequest{OwnerID: "alice", AssetID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", AssetRevision: 1, AgentID: "claude", SchemaVersion: 1, Adapter: AdapterFields{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: string(bytes.Repeat([]byte{'a'}, 64)), Capability: "claude.provider-switch.v1"}, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1}
	preparation := PreparationFields{Consumer: principal, Request: request, RequiredSlot: 1, PrepareID: "prepare", PreparedMS: 1000, ExpiresMS: 100000}
	keyHash := sha256.Sum256(principal.PublicKey[:])
	wire := &nativev1.PreparationBinding{OwnerId: "alice", ServerOrigin: principal.ServerOrigin, NativeSessionBinding: principal.SessionBinding[:], OperationId: principal.OperationID[:], AssetId: request.AssetID, AssetRevision: 1, AgentId: "claude", AssetKind: 1, SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, RequiredSlots: []nativev1.SecretSlot{1}, PrepareExpiresAtUnixMs: preparation.ExpiresMS, ConsumerKeySha256: keyHash[:], PrepareId: "prepare", PreparedAtUnixMs: 1000, PatchSha256: preparation.PatchSHA256[:]}
	bridgeHash, err := HashPreparation(preparation)
	clientHash, clientErr := configclient.HashPreparation(wire)
	if err != nil || clientErr != nil || bridgeHash != clientHash {
		test.Fatal("preparation hash differs from frozen helper")
	}
	authorize := AuthorizeRequest{PrepareID: "prepare", RequiredSlot: 1, Consent: ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: 80000}
	fields := AuthorizeFields{Preparation: preparation, Request: authorize}
	clientRequest := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: "prepare", OperationId: authorize.OperationID[:], IntentCommitment: authorize.Commitment[:], RequestedSlots: []nativev1.SecretSlot{1}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: 80000}
	transcript, err := configclient.AuthorizeTranscript(wire, fields.Challenge[:], clientRequest)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := verifier.VerifyAuthorize(bearer, fields, ed25519.Sign(key, transcript.CopyBytes())); err != nil {
		test.Fatal("authorize canonical differs")
	}
	fields.Request.Consent = ConsentFields{Kind: 2, PolicyID: "native-policy", Revision: 9}
	clientRequest.Authorization = &nativev1.AuthorizeNativeMaterializationRequest_AutoPolicy{AutoPolicy: &nativev1.AutoPolicy{PolicyId: []byte("native-policy"), PolicyRevision: 9}}
	transcript, err = configclient.AuthorizeTranscript(wire, fields.Challenge[:], clientRequest)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := verifier.VerifyAuthorize(bearer, fields, ed25519.Sign(key, transcript.CopyBytes())); err != nil {
		test.Fatal("auto-policy canonical differs")
	}
	apply := ApplyFields{Preparation: preparation, Commitment: authorize.Commitment, IntentExpiresMS: 80000}
	tokenBytes := bytes.Repeat([]byte{8}, 32)
	token, _ := ParseGrantToken(tokenBytes)
	consume := ConsumeFields{Apply: apply, GrantExpiresMS: 60000, Request: ConsumeRequest{PrepareID: "prepare", GrantID: "grant", Token: token}}
	wireApply := &nativev1.ApplyBinding{Preparation: wire, IntentCommitment: authorize.Commitment[:], IntentExpiresAtUnixMs: 80000}
	transcript, err = configclient.ConsumeTranscript(wireApply, consume.AuthorizeChallenge[:], consume.ConsumeChallenge[:], consume.GrantExpiresMS, &nativev1.ConsumeNativeMaterializationRequest{PrepareId: "prepare", GrantId: "grant", GrantToken: tokenBytes, OperationId: consume.Request.OperationID[:], IntentCommitment: consume.Request.Commitment[:], ClientNonce: consume.Request.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	signature := ed25519.Sign(key, transcript.CopyBytes())
	if _, err := verifier.VerifyConsume(bearer, consume, signature); err != nil {
		test.Fatal("consume canonical differs")
	}
	consume.GrantExpiresMS++
	if _, err := verifier.VerifyConsume(bearer, consume, signature); !errors.Is(err, ErrInvalidEvidence) {
		test.Fatal("grant expiry substitution accepted")
	}
	status := StatusFields{Preparation: preparation, Request: StatusRequest{PrepareID: "prepare", GrantID: "grant"}}
	transcript, err = configclient.StatusTranscript(wire, status.Challenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: "prepare", GrantId: "grant", OperationId: status.Request.OperationID[:], ClientNonce: status.Request.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := verifier.VerifyStatus(bearer, status, ed25519.Sign(key, transcript.CopyBytes())); err != nil {
		test.Fatal("status canonical differs")
	}
}

func TestNativeApplyPatchHashMatchesFrozenClient(test *testing.T) {
	for slot := uint32(0); slot <= 2; slot++ {
		var patch PatchFields
		wire := new(nativev1.ClaudeProviderPatch)
		for index := range patch {
			patch[index].Action = 3
			wire.Operations = append(wire.Operations, &nativev1.PatchOperation{Slot: nativev1.EnvSlot(index + 1), Action: &nativev1.PatchOperation_Remove{Remove: new(nativev1.RemoveEnv)}})
		}
		if slot != 0 {
			patch[slot-1] = PatchOperation{Action: 2, SecretSlot: slot}
			wire.Operations[slot-1].Action = &nativev1.PatchOperation_SetSelectedSecret{SetSelectedSecret: &nativev1.SetSelectedSecret{Slot: nativev1.SecretSlot(slot)}}
		}
		patch[3] = PatchOperation{Action: 1, Literal: "synthetic-model"}
		wire.Operations[3].Action = &nativev1.PatchOperation_SetLiteral{SetLiteral: &nativev1.SetLiteral{Value: "synthetic-model"}}
		serverHash, err := HashPatch(patch)
		clientHash, clientErr := configclient.HashPatch(1, wire)
		if err != nil || clientErr != nil || serverHash != clientHash {
			test.Fatal("safe seven-slot patch canonical differs")
		}
	}
}
