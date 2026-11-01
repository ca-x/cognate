package httpapi

import (
	"bytes"
	"context"
	"slices"
	"unicode/utf8"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"google.golang.org/protobuf/proto"
)

func nativeGeminiMessageValid(message proto.Message) (bool, bool) {
	switch value := message.(type) {
	case *nativev1.PrepareNativeApplyRequest:
		if value.Target != 3 && value.GeminiContractVersion == 0 {
			return false, false
		}
		_, err := nativesessionauth.GeminiPrepareWireCanonical("https://synthetic.invalid", value.ExpectedOwnerId, make([]byte, 32), value.OperationId, make([]byte, 32), value)
		return true, err == nil && len(value.ProofSignature) == 64
	case *nativev1.AuthorizeNativeMaterializationRequest:
		if value.GeminiLocalBinding == nil {
			return false, false
		}
		_, err := nativesessionauth.NewGeminiLocalBinding([32]byte{}, value.GeminiLocalBinding)
		return true, err == nil && value.CodexLocalBinding == nil && nativeIdentifier(value.PrepareId) && len(value.OperationId) == 16 && len(value.IntentCommitment) == 32 && (len(value.RequestedSlots) == 0 || slices.Equal(value.RequestedSlots, []nativev1.SecretSlot{4})) && value.GetExplicitConfirmation() != nil && value.GetExplicitConfirmation().ConfirmationRevision > 0 && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64 && value.IntentExpiresAtUnixMs > 0
	case *nativev1.PrepareNativeApplyResponse:
		if value.GetBinding().GetTarget() != 3 && value.GeminiDeclaration == nil {
			return false, false
		}
		if !nativesessionauth.ValidGeminiPreparation(value.Binding) || len(value.AuthorizeChallenge) != 32 || value.MaterializationRequired != (len(value.Binding.RequiredSlots) == 1) {
			return true, false
		}
		declaration, err := nativesessionauth.GeminiDeclarationFromWire(value.GeminiDeclaration)
		if err != nil {
			return true, false
		}
		digest, err := nativesessionauth.HashGeminiDeclaration(declaration)
		return true, err == nil && bytes.Equal(digest[:], value.Binding.Gemini.DeclarationSha256) && (len(declaration.RequiredSlots) == 1) == value.MaterializationRequired
	case *nativev1.AuthorizeNativeMaterializationResponse:
		if value.GetBinding().GetPreparation().GetTarget() != 3 && value.GetBinding().GetGeminiLocalBinding() == nil {
			return false, false
		}
		_, err := nativesessionauth.GeminiWireApplyDigest(value.Binding)
		if err != nil || value.MaterializationRequired != (len(value.Binding.Preparation.RequiredSlots) == 1) {
			return true, false
		}
		if !value.MaterializationRequired {
			return true, value.GrantId == "" && len(value.GrantToken) == 0 && len(value.ConsumeChallenge) == 0 && value.ExpiresAtUnixMs == 0
		}
		return true, nativeIdentifier(value.GrantId) && len(value.GrantToken) == 32 && len(value.ConsumeChallenge) == 32 && value.ExpiresAtUnixMs > value.Binding.Preparation.PreparedAtUnixMs && value.ExpiresAtUnixMs <= value.Binding.IntentExpiresAtUnixMs
	case *nativev1.ConsumeNativeMaterializationResponse:
		if value.GetBinding().GetPreparation().GetTarget() != 3 && value.GetBinding().GetGeminiLocalBinding() == nil {
			return false, false
		}
		_, err := nativesessionauth.GeminiWireApplyDigest(value.Binding)
		if err != nil || len(value.Binding.Preparation.RequiredSlots) != 1 || len(value.SlotValues) != 1 || value.SlotValues[0] == nil || value.SlotValues[0].Slot != 4 {
			return true, false
		}
		body := value.SlotValues[0].Plaintext
		return true, len(body) > 0 && len(body) <= 8192 && utf8.Valid(body) && !bytes.ContainsAny(body, "\x00\r\n") && nativeIdentifier(value.GrantId) && value.ConsumedAtUnixMs >= value.Binding.Preparation.PreparedAtUnixMs && value.ConsumedAtUnixMs < value.Binding.IntentExpiresAtUnixMs
	}
	return false, false
}
func (handler *nativeConfigurationHandler) prepareGemini(ctx context.Context, raw string, value *nativev1.PrepareNativeApplyRequest, input nativesessionauth.PrepareRequest) (*connect.Response[nativev1.PrepareNativeApplyResponse], error) {
	prepared, err := handler.service.PrepareGemini(ctx, raw, nativesessionauth.GeminiPrepareRequest{PrepareRequest: input, ContractVersion: value.GeminiContractVersion}, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	declaration, err := nativesessionauth.GeminiDeclarationWire(prepared.CopyDeclaration())
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	challenge := prepared.Challenge()
	binding := prepared.Binding().CopyWire()
	return handler.reply(ctx, &nativev1.PrepareNativeApplyResponse{Binding: binding, GeminiDeclaration: declaration, AuthorizeChallenge: challenge[:], MaterializationRequired: len(binding.RequiredSlots) == 1}, nil)
}
func (handler *nativeConfigurationHandler) authorizeGemini(ctx context.Context, raw string, value *nativev1.AuthorizeNativeMaterializationRequest, input nativesessionauth.AuthorizeRequest) (*connect.Response[nativev1.AuthorizeNativeMaterializationResponse], error) {
	issued, err := handler.service.AuthorizeGemini(ctx, raw, input, value.GeminiLocalBinding, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	defer issued.Destroy()
	response := &nativev1.AuthorizeNativeMaterializationResponse{Binding: issued.Binding().CopyWire()}
	if issued.ID() != "" {
		challenge := issued.Challenge()
		response.MaterializationRequired = true
		response.GrantId = issued.ID()
		response.GrantToken = issued.Reveal()
		response.ConsumeChallenge = challenge[:]
		response.ExpiresAtUnixMs = issued.ExpiresMS()
	}
	return handler.reply(ctx, response, nil)
}
