package httpapi

import (
	"bytes"
	"context"
	"slices"
	"unicode/utf8"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	publicv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func nativeCodexDeclarationWire(value agentconfig.CodexProviderDeclarationV1) (*nativev1.CodexDeclarationV1, error) {
	if _, err := nativesessionauth.HashCodexDeclaration(value); err != nil {
		return nil, state.ErrNativeUnavailable
	}
	result := &nativev1.CodexDeclarationV1{ApiVersion: 1, Target: string(value.Target), ProviderId: value.ProviderID, WireApi: value.WireAPI, Endpoint: value.Endpoint, Model: value.Model, CredentialReference: string(value.CredentialReference), RequiredSlots: []nativev1.SecretSlot{3}, LocalAuthPolicy: string(value.LocalAuthPolicy)}
	effort := func(value agentconfig.CodexReasoningEffort) publicv1.CodexReasoningEffort {
		return publicv1.CodexReasoningEffort(slices.Index(configurationCodexEfforts(), value))
	}
	if value.ReasoningEffort != nil {
		result.ReasoningEffort = new(effort(*value.ReasoningEffort))
	}
	if value.Catalog != nil {
		result.Catalog = new(publicv1.CodexCatalog)
		for _, entry := range value.Catalog.Models {
			model := &publicv1.CodexCatalogModel{Model: entry.Model, DisplayName: configurationClonePointer(entry.DisplayName), ContextWindow: configurationClonePointer(entry.ContextWindow), SupportsParallelToolCalls: configurationClonePointer(entry.SupportsParallelToolCalls), BaseInstructions: configurationClonePointer(entry.BaseInstructions)}
			if entry.InputModalities != nil {
				model.InputModalities = new(publicv1.CodexModalities)
				for _, modality := range *entry.InputModalities {
					switch modality {
					case "text":
						model.InputModalities.Values = append(model.InputModalities.Values, 1)
					case "image":
						model.InputModalities.Values = append(model.InputModalities.Values, 2)
					}
				}
			}
			if entry.ReasoningLevels != nil {
				model.ReasoningLevels = new(publicv1.CodexReasoningLevels)
				for _, level := range *entry.ReasoningLevels {
					model.ReasoningLevels.Values = append(model.ReasoningLevels.Values, effort(level))
				}
			}
			if entry.DefaultReasoningLevel != nil {
				model.DefaultReasoningLevel = new(effort(*entry.DefaultReasoningLevel))
			}
			result.Catalog.Models = append(result.Catalog.Models, model)
		}
	}
	return result, nil
}

func nativeCodexDeclaration(value *nativev1.CodexDeclarationV1) (agentconfig.CodexProviderDeclarationV1, bool) {
	if value == nil || value.ApiVersion != 1 || !slices.Equal(value.RequiredSlots, []nativev1.SecretSlot{3}) {
		return agentconfig.CodexProviderDeclarationV1{}, false
	}
	source := &publicv1.CodexProvider{AuthMode: 1, Endpoint: value.Endpoint, Model: value.Model, ReasoningEffort: value.ReasoningEffort, Catalog: value.Catalog}
	draft, _, err := configurationCodexInput(&publicv1.Draft{Payload: &publicv1.Draft_CodexProvider{CodexProvider: source}}, nil)
	if err != nil || draft.CodexProvider == nil {
		return agentconfig.CodexProviderDeclarationV1{}, false
	}
	provider := draft.CodexProvider
	result := agentconfig.CodexProviderDeclarationV1{APIVersion: int(value.ApiVersion), Target: agentconfig.Target(value.Target), ProviderID: value.ProviderId, WireAPI: value.WireApi, Endpoint: provider.Endpoint, Model: provider.Model, ReasoningEffort: provider.ReasoningEffort, Catalog: provider.Catalog, CredentialReference: agentconfig.CodexSecretSlot(value.CredentialReference), RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, LocalAuthPolicy: agentconfig.CodexLocalAuthPolicy(value.LocalAuthPolicy)}
	_, err = nativesessionauth.HashCodexDeclaration(result)
	return result, err == nil
}
func nativeCodexAdapterValid(value *nativev1.AdapterBinding) bool {
	return value != nil && value.ApiVersion == 1 && value.PluginId == "builtin.codex" && value.PluginVersion == "1.1.0" && len(value.DefinitionDigest) == 64 && slices.Equal(value.RequiredCapabilities, []string{"codex.provider-plan.v1"})
}
func nativeCodexPreparationValid(value *nativev1.PreparationBinding) bool {
	return value != nil && nativeOwnerValid(value.OwnerId) && nativeOriginValid(value.ServerOrigin) && len(value.NativeSessionBinding) == 32 && len(value.OperationId) == 16 && nativeAssetValid(value.AssetId) && value.AssetRevision > 0 && value.AgentId == "codex" && value.AssetKind == 1 && value.SchemaVersion == 1 && nativeCodexAdapterValid(value.Adapter) && value.Target == 2 && value.Scope == 1 && nativeIdentifier(value.ProfileId) && value.SelectionRevision > 0 && slices.Equal(value.RequiredSlots, []nativev1.SecretSlot{3}) && value.PreparedAtUnixMs > 0 && value.PrepareExpiresAtUnixMs > value.PreparedAtUnixMs && value.PrepareExpiresAtUnixMs-value.PreparedAtUnixMs <= 300000 && len(value.ConsumerKeySha256) == 32 && nativeIdentifier(value.PrepareId) && len(value.PatchSha256) == 0 && value.Codex != nil && value.Codex.ContractVersion == 1 && len(value.Codex.DeclarationSha256) == 32
}
func nativeCodexApplyValid(value *nativev1.ApplyBinding) bool {
	if value == nil || !nativeCodexPreparationValid(value.Preparation) || len(value.IntentCommitment) != 32 || value.IntentExpiresAtUnixMs <= value.Preparation.PreparedAtUnixMs || value.IntentExpiresAtUnixMs > value.Preparation.PrepareExpiresAtUnixMs {
		return false
	}
	_, err := nativesessionauth.NewCodexLocalBinding([32]byte(value.Preparation.Codex.DeclarationSha256), value.CodexLocalBinding)
	return err == nil
}
func nativeCodexMessageValid(message proto.Message) (bool, bool) {
	switch value := message.(type) {
	case *nativev1.PrepareNativeApplyRequest:
		if value.Target != 2 && value.CodexContractVersion == 0 {
			return false, false
		}
		return true, value.Target == 2 && value.CodexContractVersion == 1 && nativeOwnerValid(value.ExpectedOwnerId) && nativeAssetValid(value.AssetId) && value.ExpectedAssetRevision > 0 && value.AgentId == "codex" && value.SchemaVersion == 1 && nativeCodexAdapterValid(value.Adapter) && value.Scope == 1 && nativeIdentifier(value.ProfileId) && value.SelectionRevision > 0 && len(value.OperationId) == 16 && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64
	case *nativev1.AuthorizeNativeMaterializationRequest:
		if value.CodexLocalBinding == nil {
			return false, false
		}
		_, err := nativesessionauth.NewCodexLocalBinding([32]byte{}, value.CodexLocalBinding)
		return true, err == nil && nativeIdentifier(value.PrepareId) && len(value.OperationId) == 16 && len(value.IntentCommitment) == 32 && slices.Equal(value.RequestedSlots, []nativev1.SecretSlot{3}) && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64 && value.IntentExpiresAtUnixMs > 0 && value.GetExplicitConfirmation() != nil && value.GetExplicitConfirmation().ConfirmationRevision > 0
	case *nativev1.PrepareNativeApplyResponse:
		if value.CodexDeclaration == nil && (value.Binding == nil || value.Binding.Target != 2) {
			return false, false
		}
		if !nativeCodexPreparationValid(value.Binding) || value.Patch != nil || !value.MaterializationRequired || len(value.AuthorizeChallenge) != 32 {
			return true, false
		}
		declaration, valid := nativeCodexDeclaration(value.CodexDeclaration)
		if !valid {
			return true, false
		}
		hash, err := nativesessionauth.HashCodexDeclaration(declaration)
		return true, err == nil && bytes.Equal(hash[:], value.Binding.Codex.DeclarationSha256)
	case *nativev1.AuthorizeNativeMaterializationResponse:
		if value.Binding == nil || value.Binding.CodexLocalBinding == nil && value.Binding.GetPreparation().GetTarget() != 2 {
			return false, false
		}
		return true, nativeCodexApplyValid(value.Binding) && value.MaterializationRequired && nativeIdentifier(value.GrantId) && len(value.GrantToken) == 32 && len(value.ConsumeChallenge) == 32 && value.ExpiresAtUnixMs > value.Binding.GetPreparation().GetPreparedAtUnixMs() && value.ExpiresAtUnixMs <= value.Binding.IntentExpiresAtUnixMs
	case *nativev1.ConsumeNativeMaterializationResponse:
		if value.Binding == nil || value.Binding.CodexLocalBinding == nil && value.Binding.GetPreparation().GetTarget() != 2 {
			return false, false
		}
		if !nativeCodexApplyValid(value.Binding) || !nativeIdentifier(value.GrantId) || len(value.SlotValues) != 1 || value.SlotValues[0] == nil || value.SlotValues[0].Slot != 3 {
			return true, false
		}
		body := value.SlotValues[0].Plaintext
		return true, len(body) > 0 && len(body) <= 8192 && utf8.Valid(body) && !bytes.ContainsAny(body, "\x00\r\n") && value.ConsumedAtUnixMs >= value.Binding.Preparation.PreparedAtUnixMs && value.ConsumedAtUnixMs < value.Binding.IntentExpiresAtUnixMs
	}
	return false, false
}

func (handler *nativeConfigurationHandler) prepareCodex(ctx context.Context, raw string, value *nativev1.PrepareNativeApplyRequest, input nativesessionauth.PrepareRequest) (*connect.Response[nativev1.PrepareNativeApplyResponse], error) {
	prepared, err := handler.service.PrepareCodex(ctx, raw, nativesessionauth.CodexPrepareRequest{PrepareRequest: input, ContractVersion: value.CodexContractVersion}, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	declaration, err := nativeCodexDeclarationWire(prepared.CopyDeclaration())
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	challenge := prepared.Challenge()
	return handler.reply(ctx, &nativev1.PrepareNativeApplyResponse{Binding: prepared.Binding().CopyWire(), CodexDeclaration: declaration, AuthorizeChallenge: challenge[:], MaterializationRequired: true}, nil)
}
func (handler *nativeConfigurationHandler) authorizeCodex(ctx context.Context, raw string, value *nativev1.AuthorizeNativeMaterializationRequest, input nativesessionauth.AuthorizeRequest) (*connect.Response[nativev1.AuthorizeNativeMaterializationResponse], error) {
	issued, err := handler.service.AuthorizeCodex(ctx, raw, input, value.CodexLocalBinding, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	defer issued.Destroy()
	challenge := issued.Challenge()
	return handler.reply(ctx, &nativev1.AuthorizeNativeMaterializationResponse{Binding: issued.Binding().CopyWire(), MaterializationRequired: true, GrantId: issued.ID(), GrantToken: issued.Reveal(), ConsumeChallenge: challenge[:], ExpiresAtUnixMs: issued.ExpiresMS()}, nil)
}
