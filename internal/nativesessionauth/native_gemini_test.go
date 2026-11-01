package nativesessionauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestGeminiWireAssignments(test *testing.T) {
	file := nativev1.File_skillhub_configuration_native_v1_native_proto
	for name, number := range map[protoreflect.Name]protoreflect.EnumNumber{"TARGET_GEMINI_SETTINGS": 3, "SECRET_SLOT_GEMINI_API_KEY": 4} {
		var found bool
		for index := range file.Enums().Len() {
			value := file.Enums().Get(index).Values().ByName(name)
			if value != nil {
				found = value.Number() == number
			}
		}
		if !found {
			test.Fatal("approved Gemini enum assignment missing")
		}
	}
	for _, assignment := range []struct {
		message, field protoreflect.Name
		number         protoreflect.FieldNumber
	}{
		{"PreparationBinding", "gemini", 41},
		{"ApplyBinding", "gemini_local_binding", 41},
		{"PrepareNativeApplyRequest", "gemini_contract_version", 41},
		{"AuthorizeNativeMaterializationRequest", "gemini_local_binding", 41},
		{"PrepareNativeApplyResponse", "gemini_declaration", 6},
	} {
		message := file.Messages().ByName(assignment.message)
		field := message.Fields().ByName(assignment.field)
		if field == nil || field.Number() != assignment.number {
			test.Fatal("approved Gemini variant assignment missing")
		}
	}
	service := file.Services().ByName("NativeConfigurationService")
	if service == nil || service.Methods().Len() != 6 {
		test.Fatal("native service RPC count changed")
	}
}

func TestGeminiIndependentProofParityAndBoundMutations(test *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	defer clear(key)
	consumer := ConsumerFields{OwnerID: "owner-synthetic", ServerOrigin: "https://proof.example:8443", SessionBinding: [32]byte(bytes.Repeat([]byte{5}, 32)), OperationID: [16]byte(bytes.Repeat([]byte{1}, 16)), PublicKey: [32]byte(key.Public().(ed25519.PublicKey))}
	request := GeminiPrepareRequest{PrepareRequest: PrepareRequest{OwnerID: consumer.OwnerID, AssetID: strings.Repeat("A", 26), AssetRevision: 7, AgentID: "gemini", SchemaVersion: 1, Adapter: AdapterFields{APIVersion: 1, PluginID: "builtin.gemini", PluginVersion: "1.1.0", DefinitionDigest: GeminiDefinitionDigest, Capability: "gemini.provider-plan.v1"}, Target: 3, Scope: 1, ProfileID: "profile-synthetic", SelectionRevision: 3, OperationID: consumer.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{6}, 32))}, ContractVersion: 1}
	declaration := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: agentconfig.GeminiSettings, AuthMode: agentconfig.GeminiAPIKeyAuth, SelectedType: "gemini-api-key", CredentialReference: new(agentconfig.GeminiAPIKeySecret), RequiredSlots: []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}, LocalAuthPolicy: agentconfig.GeminiPreserveLocalLoginV1, Endpoint: new("https://provider.example/v1beta"), KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("env-one"), SettingsModel: new("settings-one")}
	digest, err := HashGeminiDeclaration(declaration)
	if err != nil {
		test.Fatal("declaration fixture")
	}
	preparation, err := NewGeminiPreparation(consumer, request, "prepare-synthetic", 1700000000000, 1700000300000, digest, 4)
	if err != nil {
		test.Fatal("preparation fixture")
	}
	localWire := &nativev1.GeminiLocalBindingV1{BindingVersion: 1, NativeProfileId: "synthetic-protocol-profile", NativeProfileSha256: bytes.Repeat([]byte{1}, 32), CodecProfileSha256: bytes.Repeat([]byte{2}, 32), EffectiveScope: 1, LocalAuthPolicy: "preserve-local-login-v1", ObservationWitness: bytes.Repeat([]byte{3}, 32)}
	for index := range 2 {
		localWire.Roles = append(localWire.Roles, &nativev1.GeminiRoleBaseline{Role: nativev1.GeminiRole(index + 1), Presence: 1, BaselineHmac: bytes.Repeat([]byte{byte(index + 4)}, 32)})
	}
	for index := range 6 {
		localWire.FieldDecisions = append(localWire.FieldDecisions, &nativev1.GeminiFieldDecision{Field: nativev1.GeminiField(index + 1), Ownership: 1, Action: 2, Cleanup: 1})
	}
	local, err := NewGeminiLocalBinding(digest, localWire)
	if err != nil {
		test.Fatal("local fixture")
	}
	apply, err := NewGeminiApply(preparation, local, 11, 1700000240000)
	if err != nil {
		test.Fatal("apply fixture")
	}
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	clientConsumer := configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: keyHash[:]}
	clientPrepare, err := configclient.GeminiPrepareTranscript(clientConsumer, geminiPrepareWire(request))
	neutralPrepare, neutralError := geminiPrepareCanonical(GeminiPrepareFields{Consumer: consumer, Request: request})
	if err != nil || neutralError != nil || !bytes.Equal(clientPrepare.CopyBytes(), neutralPrepare) {
		test.Fatal("prepare proof parity")
	}
	challenge := [32]byte(bytes.Repeat([]byte{9}, 32))
	expiry := int64(1700000240000)
	commitment := apply.Commitment()
	authorize := AuthorizeRequest{PrepareID: "prepare-synthetic", OperationID: consumer.OperationID, Commitment: commitment, RequiredSlot: 4, Consent: ConsentFields{Kind: 1, Revision: 11}, Nonce: [32]byte(bytes.Repeat([]byte{10}, 32)), IntentExpiresMS: expiry}
	clientAuthorize := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: authorize.PrepareID, OperationId: consumer.OperationID[:], IntentCommitment: commitment[:], RequestedSlots: []nativev1.SecretSlot{4}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 11}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: expiry, GeminiLocalBinding: localWire}
	clientAuth, err := configclient.GeminiAuthorizeTranscript(preparation.CopyWire(), challenge[:], clientAuthorize)
	neutralAuth, neutralError := geminiAuthorizeCanonical(GeminiAuthorizeFields{Preparation: preparation, Local: local, Challenge: challenge, Request: authorize})
	if err != nil || neutralError != nil || !bytes.Equal(clientAuth.CopyBytes(), neutralAuth) {
		test.Fatal("authorize proof parity")
	}
	tokenBytes := bytes.Repeat([]byte{7}, 32)
	token, _ := ParseGrantToken(tokenBytes)
	consumeChallenge := [32]byte(bytes.Repeat([]byte{8}, 32))
	consume := ConsumeRequest{PrepareID: "prepare-synthetic", GrantID: "grant-synthetic", Token: token, OperationID: consumer.OperationID, Commitment: commitment, Nonce: [32]byte(bytes.Repeat([]byte{12}, 32))}
	clientConsume, err := configclient.GeminiConsumeTranscript(apply.CopyWire(), challenge[:], consumeChallenge[:], 1700000060000, &nativev1.ConsumeNativeMaterializationRequest{PrepareId: consume.PrepareID, GrantId: consume.GrantID, GrantToken: tokenBytes, OperationId: consumer.OperationID[:], IntentCommitment: commitment[:], ClientNonce: consume.Nonce[:]})
	neutralConsume, neutralError := geminiConsumeCanonical(GeminiConsumeFields{Apply: apply, AuthorizeChallenge: challenge, ConsumeChallenge: consumeChallenge, GrantExpiresMS: 1700000060000, Request: consume})
	if err != nil || neutralError != nil || !bytes.Equal(clientConsume.CopyBytes(), neutralConsume) {
		test.Fatal("consume proof parity")
	}
	status := StatusRequest{PrepareID: "prepare-synthetic", OperationID: consumer.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{13}, 32))}
	clientStatus, err := configclient.GeminiStatusTranscript(preparation.CopyWire(), challenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: status.PrepareID, OperationId: consumer.OperationID[:], ClientNonce: status.Nonce[:]})
	neutralStatus, neutralError := geminiStatusCanonical(GeminiStatusFields{Preparation: preparation, Challenge: challenge, Request: status})
	if err != nil || neutralError != nil || !bytes.Equal(clientStatus.CopyBytes(), neutralStatus) {
		test.Fatal("status proof parity")
	}
	for _, mutate := range []func(*nativev1.GeminiLocalBindingV1){func(value *nativev1.GeminiLocalBindingV1) { value.ObservationWitness[0] ^= 1 }, func(value *nativev1.GeminiLocalBindingV1) { value.NativeProfileSha256[0] ^= 1 }, func(value *nativev1.GeminiLocalBindingV1) { value.CodecProfileSha256[0] ^= 1 }, func(value *nativev1.GeminiLocalBindingV1) { value.NativeProfileId += "-changed" }, func(value *nativev1.GeminiLocalBindingV1) { value.Roles[0].BaselineHmac[0] ^= 1 }, func(value *nativev1.GeminiLocalBindingV1) { value.Roles[1].Presence = 2 }, func(value *nativev1.GeminiLocalBindingV1) { value.FieldDecisions[0].Ownership = 4 }, func(value *nativev1.GeminiLocalBindingV1) { value.FieldDecisions[3].Action = 1 }, func(value *nativev1.GeminiLocalBindingV1) { value.FieldDecisions[4].Cleanup = 2 }, func(value *nativev1.GeminiLocalBindingV1) { value.FieldDecisions[5].Field = 1 }, func(value *nativev1.GeminiLocalBindingV1) { value.LocalAuthPolicy = "other" }, func(value *nativev1.GeminiLocalBindingV1) { value.EffectiveScope = 2 }, func(value *nativev1.GeminiLocalBindingV1) { value.ProtoReflect().SetUnknown([]byte{0x78, 1}) }} {
		changed := proto.Clone(localWire).(*nativev1.GeminiLocalBindingV1)
		mutate(changed)
		clientDigest, clientErr := configclient.HashGeminiIntent(preparation.CopyWire(), changed, 11, expiry)
		neutralDigest, neutralErr := HashGeminiWireIntent(preparation.CopyWire(), changed, 11, expiry)
		if (clientErr == nil) != (neutralErr == nil) || clientErr == nil && (clientDigest != neutralDigest || clientDigest == commitment) {
			test.Fatal("changed observation/field ignored or proof validators disagree")
		}
	}
	if bytes.Equal(clientPrepare.CopyBytes(), clientAuth.CopyBytes()) || bytes.Equal(clientConsume.CopyBytes(), clientStatus.CopyBytes()) {
		test.Fatal("proof purposes share canonical domain")
	}
}
