package configclient

import (
	"bytes"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"google.golang.org/protobuf/proto"
)

func TestGeminiExtensionsCannotBeIgnoredByCodexProof(test *testing.T) {
	_, request, response, local := clientCodexFixture(test)
	consumer := ConsumerBinding{OwnerID: response.Binding.OwnerId, ServerOrigin: response.Binding.ServerOrigin, OperationID: request.OperationId, NativeSessionBinding: response.Binding.NativeSessionBinding, ConsumerKeySHA256: response.Binding.ConsumerKeySha256}
	commitment, err := HashCodexIntent(response.Binding, local, 11, 1700000240000)
	if err != nil {
		test.Fatal("valid old intent setup")
	}
	authorize := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: response.Binding.PrepareId, OperationId: request.OperationId, IntentCommitment: commitment[:], RequestedSlots: []nativev1.SecretSlot{3}, ClientNonce: request.ClientNonce, IntentExpiresAtUnixMs: 1700000240000, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 11}}, CodexLocalBinding: local}
	apply := &nativev1.ApplyBinding{Preparation: response.Binding, IntentCommitment: commitment[:], IntentExpiresAtUnixMs: 1700000240000, CodexLocalBinding: local}
	test.Run("prepare", func(test *testing.T) {
		before, err := CodexPrepareTranscript(consumer, request)
		if err != nil {
			test.Fatal("old prepare positive control")
		}
		mixed := proto.Clone(request).(*nativev1.PrepareNativeApplyRequest)
		mixed.GeminiContractVersion = 1
		after, err := CodexPrepareTranscript(consumer, mixed)
		if err == nil {
			test.Fatalf("mixed prepare accepted; canonical_unchanged=%t", bytes.Equal(before.CopyBytes(), after.CopyBytes()))
		}
	})
	test.Run("preparation", func(test *testing.T) {
		before, err := HashCodexPreparation(response.Binding)
		if err != nil {
			test.Fatal("old preparation positive control")
		}
		mixed := proto.Clone(response.Binding).(*nativev1.PreparationBinding)
		mixed.Gemini = &nativev1.GeminiPreparationV1{ContractVersion: 1, DeclarationSha256: bytes.Repeat([]byte{17}, 32)}
		after, err := HashCodexPreparation(mixed)
		if err == nil {
			test.Fatalf("mixed preparation accepted; canonical_unchanged=%t", before == after)
		}
	})
	test.Run("authorize", func(test *testing.T) {
		before, err := CodexAuthorizeTranscript(response.Binding, response.AuthorizeChallenge, authorize)
		if err != nil {
			test.Fatal("old authorize positive control")
		}
		mixed := proto.Clone(authorize).(*nativev1.AuthorizeNativeMaterializationRequest)
		mixed.GeminiLocalBinding = &nativev1.GeminiLocalBindingV1{BindingVersion: 1}
		after, err := CodexAuthorizeTranscript(response.Binding, response.AuthorizeChallenge, mixed)
		if err == nil {
			test.Fatalf("mixed authorize accepted; canonical_unchanged=%t", bytes.Equal(before.CopyBytes(), after.CopyBytes()))
		}
	})
	test.Run("apply", func(test *testing.T) {
		before, err := HashCodexApply(apply)
		if err != nil {
			test.Fatal("old apply positive control")
		}
		mixed := proto.Clone(apply).(*nativev1.ApplyBinding)
		mixed.GeminiLocalBinding = &nativev1.GeminiLocalBindingV1{BindingVersion: 1}
		after, err := HashCodexApply(mixed)
		if err == nil {
			test.Fatalf("mixed apply accepted; canonical_unchanged=%t", before == after)
		}
	})
	test.Run("prepared-response", func(test *testing.T) {
		if ValidateCodexPreparedDeclaration(response) != nil {
			test.Fatal("old declaration positive control")
		}
		mixed := proto.Clone(response).(*nativev1.PrepareNativeApplyResponse)
		mixed.GeminiDeclaration = &nativev1.GeminiDeclarationV1{Version: 1}
		if ValidateCodexPreparedDeclaration(mixed) == nil {
			test.Fatal("mixed declaration response accepted")
		}
	})
}

func TestGeminiExtensionsCannotBeIgnoredByClaudeProof(test *testing.T) {
	_, request, response, _ := clientCodexFixture(test)
	request.AgentId, request.Target, request.CodexContractVersion = "claude", 1, 0
	request.Adapter.PluginId = "builtin.claude"
	request.Adapter.RequiredCapabilities = []string{"claude.provider-switch.v1"}
	patch := new(nativev1.ClaudeProviderPatch)
	for index := range 7 {
		patch.Operations = append(patch.Operations, &nativev1.PatchOperation{Slot: nativev1.EnvSlot(index + 1), Action: &nativev1.PatchOperation_Remove{Remove: new(nativev1.RemoveEnv)}})
	}
	patchHash, err := HashPatch(1, patch)
	if err != nil {
		test.Fatal("old Claude patch setup")
	}
	response.Binding.AgentId, response.Binding.Target, response.Binding.Codex = "claude", 1, nil
	response.Binding.RequiredSlots = nil
	response.Binding.PatchSha256 = patchHash[:]
	response.Patch, response.CodexDeclaration, response.MaterializationRequired = patch, nil, false
	consumer := ConsumerBinding{OwnerID: response.Binding.OwnerId, ServerOrigin: response.Binding.ServerOrigin, OperationID: request.OperationId, NativeSessionBinding: response.Binding.NativeSessionBinding, ConsumerKeySHA256: response.Binding.ConsumerKeySha256}
	authorize := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: response.Binding.PrepareId, OperationId: request.OperationId, IntentCommitment: bytes.Repeat([]byte{7}, 32), ClientNonce: request.ClientNonce, IntentExpiresAtUnixMs: 1700000240000, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 11}}}
	apply := &nativev1.ApplyBinding{Preparation: response.Binding, IntentCommitment: authorize.IntentCommitment, IntentExpiresAtUnixMs: authorize.IntentExpiresAtUnixMs}
	test.Run("prepare", func(test *testing.T) {
		before, err := PrepareTranscript(consumer, request)
		if err != nil {
			test.Fatal("old prepare positive control")
		}
		mixed := proto.Clone(request).(*nativev1.PrepareNativeApplyRequest)
		mixed.GeminiContractVersion = 1
		after, err := PrepareTranscript(consumer, mixed)
		if err == nil {
			test.Fatalf("mixed prepare accepted; canonical_unchanged=%t", bytes.Equal(before.CopyBytes(), after.CopyBytes()))
		}
	})
	test.Run("preparation", func(test *testing.T) {
		before, err := HashPreparation(response.Binding)
		if err != nil {
			test.Fatal("old preparation positive control")
		}
		mixed := proto.Clone(response.Binding).(*nativev1.PreparationBinding)
		mixed.Gemini = &nativev1.GeminiPreparationV1{ContractVersion: 1, DeclarationSha256: bytes.Repeat([]byte{17}, 32)}
		after, err := HashPreparation(mixed)
		if err == nil {
			test.Fatalf("mixed preparation accepted; canonical_unchanged=%t", before == after)
		}
	})
	test.Run("authorize", func(test *testing.T) {
		before, err := AuthorizeTranscript(response.Binding, response.AuthorizeChallenge, authorize)
		if err != nil {
			test.Fatal("old authorize positive control")
		}
		mixed := proto.Clone(authorize).(*nativev1.AuthorizeNativeMaterializationRequest)
		mixed.GeminiLocalBinding = &nativev1.GeminiLocalBindingV1{BindingVersion: 1}
		after, err := AuthorizeTranscript(response.Binding, response.AuthorizeChallenge, mixed)
		if err == nil {
			test.Fatalf("mixed authorize accepted; canonical_unchanged=%t", bytes.Equal(before.CopyBytes(), after.CopyBytes()))
		}
	})
	test.Run("apply", func(test *testing.T) {
		before, err := HashApply(apply)
		if err != nil {
			test.Fatal("old apply positive control")
		}
		mixed := proto.Clone(apply).(*nativev1.ApplyBinding)
		mixed.GeminiLocalBinding = &nativev1.GeminiLocalBindingV1{BindingVersion: 1}
		after, err := HashApply(mixed)
		if err == nil {
			test.Fatalf("mixed apply accepted; canonical_unchanged=%t", before == after)
		}
	})
	test.Run("prepared-response", func(test *testing.T) {
		if ValidatePreparedPatch(response) != nil {
			test.Fatal("old declaration positive control")
		}
		mixed := proto.Clone(response).(*nativev1.PrepareNativeApplyResponse)
		mixed.GeminiDeclaration = &nativev1.GeminiDeclarationV1{Version: 1}
		if ValidatePreparedPatch(mixed) == nil {
			test.Fatal("mixed declaration response accepted")
		}
	})
}
