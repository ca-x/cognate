package configclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/v2"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	publicv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/proto"
)

func codexReasoning(value publicv1.CodexReasoningEffort) (agentconfig.CodexReasoningEffort, bool) {
	switch value {
	case 1:
		return "none", true
	case 2:
		return "minimal", true
	case 3:
		return "low", true
	case 4:
		return "medium", true
	case 5:
		return "high", true
	case 6:
		return "xhigh", true
	case 7:
		return "max", true
	}
	return "", false
}
func codexModel(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index, letter := range value {
		if letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' {
			continue
		}
		if index == 0 || !strings.ContainsRune("._:/-", letter) {
			return false
		}
	}
	return true
}
func codexLabel(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func copyCodexOptional[Value any](value *Value) *Value {
	if value == nil {
		return nil
	}
	return new(*value)
}

func codexDeclaration(wire *nativev1.CodexDeclarationV1) (agentconfig.CodexProviderDeclarationV1, bool) {
	if !knownMessage(wire) || wire.ApiVersion != 1 || wire.Target != "codex.config" || wire.ProviderId != "cognate" || wire.WireApi != "responses" || wire.CredentialReference != "codex.provider-key" || !slices.Equal(wire.RequiredSlots, []nativev1.SecretSlot{3}) || wire.LocalAuthPolicy != "preserve-local-login-v1" || !codexModel(wire.Model) || !codexLabel(wire.Endpoint, 2048) || strings.ContainsAny(wire.Endpoint, "?#\\") {
		return agentconfig.CodexProviderDeclarationV1{}, false
	}
	endpoint, err := url.Parse(wire.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || !utf8.ValidString(endpoint.Path) || strings.ContainsFunc(endpoint.Path, unicode.IsControl) {
		return agentconfig.CodexProviderDeclarationV1{}, false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return agentconfig.CodexProviderDeclarationV1{}, false
		}
	}
	declaration := agentconfig.CodexProviderDeclarationV1{APIVersion: 1, Target: agentconfig.CodexConfig, ProviderID: wire.ProviderId, WireAPI: wire.WireApi, Endpoint: wire.Endpoint, Model: wire.Model, CredentialReference: agentconfig.CodexProviderKeySecret, RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, LocalAuthPolicy: agentconfig.CodexPreserveLocalLoginV1}
	if wire.ReasoningEffort != nil {
		effort, valid := codexReasoning(*wire.ReasoningEffort)
		if !valid {
			return agentconfig.CodexProviderDeclarationV1{}, false
		}
		declaration.ReasoningEffort = new(effort)
	}
	if wire.Catalog != nil {
		if len(wire.Catalog.Models) == 0 || len(wire.Catalog.Models) > 32 {
			return agentconfig.CodexProviderDeclarationV1{}, false
		}
		declaration.Catalog = new(agentconfig.CodexCatalogDesired)
		models := make(map[string]bool, len(wire.Catalog.Models))
		for _, entry := range wire.Catalog.Models {
			if entry == nil || !codexModel(entry.Model) || models[entry.Model] || entry.DisplayName != nil && !codexLabel(*entry.DisplayName, 256) || entry.ContextWindow != nil && (*entry.ContextWindow == 0 || *entry.ContextWindow > 2147483647) {
				return agentconfig.CodexProviderDeclarationV1{}, false
			}
			models[entry.Model] = true
			model := agentconfig.CodexCatalogModelDesired{Model: entry.Model, DisplayName: copyCodexOptional(entry.DisplayName), ContextWindow: copyCodexOptional(entry.ContextWindow), SupportsParallelToolCalls: copyCodexOptional(entry.SupportsParallelToolCalls), BaseInstructions: copyCodexOptional(entry.BaseInstructions)}
			if entry.BaseInstructions != nil {
				body := *entry.BaseInstructions
				if len(body) == 0 || len(body) > 2048 || !utf8.ValidString(body) || strings.ContainsFunc(body, func(letter rune) bool {
					return unicode.IsControl(letter) && letter != '\r' && letter != '\n' && letter != '\t'
				}) {
					return agentconfig.CodexProviderDeclarationV1{}, false
				}
			}
			if entry.InputModalities != nil {
				items := entry.InputModalities.Values
				if len(items) == 0 || len(items) > 2 {
					return agentconfig.CodexProviderDeclarationV1{}, false
				}
				model.InputModalities = new([]agentconfig.CodexModality{})
				for index, item := range items {
					if slices.Contains(items[:index], item) {
						return agentconfig.CodexProviderDeclarationV1{}, false
					}
					switch item {
					case 1:
						*model.InputModalities = append(*model.InputModalities, "text")
					case 2:
						*model.InputModalities = append(*model.InputModalities, "image")
					default:
						return agentconfig.CodexProviderDeclarationV1{}, false
					}
				}
			}
			if entry.ReasoningLevels != nil {
				items := entry.ReasoningLevels.Values
				if len(items) == 0 || len(items) > 7 {
					return agentconfig.CodexProviderDeclarationV1{}, false
				}
				model.ReasoningLevels = new([]agentconfig.CodexReasoningEffort{})
				for index, item := range items {
					effort, valid := codexReasoning(item)
					if !valid || slices.Contains(items[:index], item) {
						return agentconfig.CodexProviderDeclarationV1{}, false
					}
					*model.ReasoningLevels = append(*model.ReasoningLevels, effort)
				}
			}
			if entry.DefaultReasoningLevel != nil {
				effort, valid := codexReasoning(*entry.DefaultReasoningLevel)
				if !valid || model.ReasoningLevels == nil || !slices.Contains(*model.ReasoningLevels, effort) {
					return agentconfig.CodexProviderDeclarationV1{}, false
				}
				model.DefaultReasoningLevel = new(effort)
			}
			if model.Model == declaration.Model && declaration.ReasoningEffort != nil && model.ReasoningLevels != nil && !slices.Contains(*model.ReasoningLevels, *declaration.ReasoningEffort) {
				return agentconfig.CodexProviderDeclarationV1{}, false
			}
			declaration.Catalog.Models = append(declaration.Catalog.Models, model)
		}
		if !models[declaration.Model] {
			return agentconfig.CodexProviderDeclarationV1{}, false
		}
		encoded, err := json.Marshal(declaration.Catalog)
		if err != nil || len(encoded) > 32<<10 {
			return agentconfig.CodexProviderDeclarationV1{}, false
		}
	}
	desired := agentconfig.CodexProviderDesired{AuthMode: agentconfig.CodexProviderKeyAuth, KeyPresent: true, Endpoint: declaration.Endpoint, Model: declaration.Model, ReasoningEffort: declaration.ReasoningEffort, Catalog: declaration.Catalog}
	encoded, err := json.Marshal(desired)
	if err != nil || len(encoded) > 48<<10 {
		return agentconfig.CodexProviderDeclarationV1{}, false
	}
	encoded, err = json.Marshal(declaration)
	if err != nil || len(encoded) > 64<<10 {
		return agentconfig.CodexProviderDeclarationV1{}, false
	}
	return declaration, true
}

func encodeCodexOptional[Value any](encoded *proofEncoder, value *Value, encode func(Value)) {
	if value == nil {
		encoded.u32(0)
	} else {
		encoded.u32(1)
		encode(*value)
	}
}
func codexTextList[Value ~string](encoded *proofEncoder, items []Value) {
	encoded.u32(uint32(len(items)))
	for _, item := range items {
		encoded.text(string(item))
	}
}
func HashCodexDeclaration(wire *nativev1.CodexDeclarationV1) ([32]byte, error) {
	value, valid := codexDeclaration(wire)
	if !valid {
		return [32]byte{}, ErrInvalidNativeProof
	}
	var encoded proofEncoder
	encoded.text("cognate/native-codex-declaration/v1")
	encoded.u32(1)
	encoded.u32(uint32(value.APIVersion))
	encoded.text(string(value.Target))
	encoded.text(value.ProviderID)
	encoded.text(value.WireAPI)
	encoded.text(value.Endpoint)
	encoded.text(value.Model)
	encodeCodexOptional(&encoded, value.ReasoningEffort, func(effort agentconfig.CodexReasoningEffort) { encoded.text(string(effort)) })
	encodeCodexOptional(&encoded, value.Catalog, func(catalog agentconfig.CodexCatalogDesired) {
		encoded.u32(uint32(len(catalog.Models)))
		for _, model := range catalog.Models {
			encoded.text(model.Model)
			encodeCodexOptional(&encoded, model.DisplayName, encoded.text)
			encodeCodexOptional(&encoded, model.ContextWindow, encoded.u64)
			encodeCodexOptional(&encoded, model.SupportsParallelToolCalls, func(enabled bool) {
				if enabled {
					encoded.u32(1)
				} else {
					encoded.u32(0)
				}
			})
			encodeCodexOptional(&encoded, model.InputModalities, func(items []agentconfig.CodexModality) { codexTextList(&encoded, items) })
			encodeCodexOptional(&encoded, model.BaseInstructions, encoded.text)
			encodeCodexOptional(&encoded, model.ReasoningLevels, func(items []agentconfig.CodexReasoningEffort) { codexTextList(&encoded, items) })
			encodeCodexOptional(&encoded, model.DefaultReasoningLevel, func(effort agentconfig.CodexReasoningEffort) { encoded.text(string(effort)) })
		}
	})
	encoded.text(string(value.CredentialReference))
	codexTextList(&encoded, value.RequiredSlots)
	encoded.text(string(value.LocalAuthPolicy))
	if len(encoded) > 64<<10 {
		return [32]byte{}, ErrInvalidNativeProof
	}
	return sha256.Sum256(encoded), nil
}
func validCodexAdapter(value *nativev1.AdapterBinding) bool {
	return knownMessage(value) && proto.Equal(value, codexNativeAdapterBinding())
}
func validCodexPrepare(value *nativev1.PrepareNativeApplyRequest) bool {
	return knownMessage(value) && validOwner(value.ExpectedOwnerId) && assetID(value.AssetId) && value.ExpectedAssetRevision > 0 && value.AgentId == "codex" && value.SchemaVersion == 1 && validCodexAdapter(value.Adapter) && value.Target == 2 && value.Scope == 1 && opaqueID(value.ProfileId) && value.SelectionRevision > 0 && len(value.OperationId) == 16 && len(value.ClientNonce) == 32 && optionalSignature(value.ProofSignature) && value.CodexContractVersion == 1
}
func validCodexPreparation(value *nativev1.PreparationBinding) bool {
	return knownMessage(value) && value.AgentId == "codex" && value.AssetKind == 1 && value.SchemaVersion == 1 && value.Target == 2 && value.Scope == 1 && validOwner(value.OwnerId) && validOrigin(value.ServerOrigin) && len(value.NativeSessionBinding) == 32 && len(value.OperationId) == 16 && assetID(value.AssetId) && value.AssetRevision > 0 && validCodexAdapter(value.Adapter) && opaqueID(value.ProfileId) && value.SelectionRevision > 0 && slices.Equal(value.RequiredSlots, []nativev1.SecretSlot{3}) && value.PreparedAtUnixMs > 0 && value.PrepareExpiresAtUnixMs > value.PreparedAtUnixMs && value.PrepareExpiresAtUnixMs-value.PreparedAtUnixMs <= 300000 && len(value.ConsumerKeySha256) == 32 && opaqueID(value.PrepareId) && len(value.PatchSha256) == 0 && value.Codex != nil && value.Codex.ContractVersion == 1 && len(value.Codex.DeclarationSha256) == 32
}
func codexPreparationEncoding(value *nativev1.PreparationBinding) (proofEncoder, error) {
	if !validCodexPreparation(value) {
		return nil, ErrInvalidNativeProof
	}
	var encoded proofEncoder
	encoded.text(value.OwnerId)
	encoded.text(value.ServerOrigin)
	encoded.blob(value.NativeSessionBinding)
	encoded.blob(value.OperationId)
	encoded.text(value.AssetId)
	encoded.u64(uint64(value.AssetRevision))
	encoded.text(value.AgentId)
	encoded.u32(uint32(value.AssetKind))
	encoded.u32(value.SchemaVersion)
	encodeAdapter(&encoded, value.Adapter)
	encoded.u32(uint32(value.Target))
	encoded.u32(uint32(value.Scope))
	encoded.text(value.ProfileId)
	encoded.u64(value.SelectionRevision)
	encodeSlots(&encoded, value.RequiredSlots)
	encoded.u64(uint64(value.PrepareExpiresAtUnixMs))
	encoded.blob(value.ConsumerKeySha256)
	encoded.text(value.PrepareId)
	encoded.u64(uint64(value.PreparedAtUnixMs))
	encoded.u32(value.Codex.ContractVersion)
	encoded.blob(value.Codex.DeclarationSha256)
	if len(encoded) > 4096 {
		return nil, ErrInvalidNativeProof
	}
	return encoded, nil
}
func HashCodexPreparation(value *nativev1.PreparationBinding) ([32]byte, error) {
	preparation, err := codexPreparationEncoding(value)
	if err != nil {
		return [32]byte{}, err
	}
	var encoded proofEncoder
	encoded.text("cognate/native-codex-preparation/v1")
	encoded = append(encoded, preparation...)
	return sha256.Sum256(encoded), nil
}
func validCodexLocal(value *nativev1.CodexLocalBindingV1) bool {
	if !knownMessage(value) || value.BindingVersion != 1 || !codexModel(value.NativeProfileId) || len(value.NativeProfileSha256) != 32 || len(value.TemplateSha256) != 32 || value.EffectiveScope != 1 || value.ProviderOwnership != 1 && value.ProviderOwnership != 2 || value.CatalogOwnership < 1 || value.CatalogOwnership > 3 || len(value.ManagedModels) > 32 || value.CatalogOwnership != 2 && len(value.ManagedModels) > 0 || value.LocalAuthPolicy != "preserve-local-login-v1" || value.RequiresOpenaiAuth == nil || len(value.ObservationWitness) != 32 || bytes.Equal(value.ObservationWitness, make([]byte, 32)) || len(value.Baselines) != 3 {
		return false
	}
	for index, model := range value.ManagedModels {
		if !codexModel(model) || slices.Contains(value.ManagedModels[:index], model) {
			return false
		}
	}
	for index, baseline := range value.Baselines {
		if baseline == nil || baseline.Role != nativev1.CodexRole(index+1) {
			return false
		}
		switch state := baseline.State.(type) {
		case *nativev1.CodexRoleBaseline_Missing:
			if state.Missing == nil {
				return false
			}
		case *nativev1.CodexRoleBaseline_Present:
			if state.Present == nil || len(state.Present.Sha256) != 32 {
				return false
			}
		case *nativev1.CodexRoleBaseline_NotObserved:
			if state.NotObserved == nil || index == 0 {
				return false
			}
		default:
			return false
		}
	}
	if value.KeyringState < 1 || value.KeyringState > 4 || value.FileLoginState < 1 || value.FileLoginState > 4 {
		return false
	}
	observed := value.Baselines[1].GetNotObserved() == nil
	login := false
	switch value.AuthStore {
	case 1:
		if !observed || value.FileLoginState != 1 && value.FileLoginState != 2 {
			return false
		}
		login = value.FileLoginState == 1
	case 2:
		if value.KeyringState != 1 && value.KeyringState != 2 {
			return false
		}
		login = value.KeyringState == 1
	case 3:
		if value.KeyringState == 1 {
			login = true
		} else if value.KeyringState == 2 {
			if !observed || value.FileLoginState != 1 && value.FileLoginState != 2 {
				return false
			}
			login = value.FileLoginState == 1
		} else {
			return false
		}
	default:
		return false
	}
	return login == *value.RequiresOpenaiAuth && !(value.Baselines[1].GetPresent() == nil && value.FileLoginState == 1)
}
func codexLocalEncoding(declaration []byte, value *nativev1.CodexLocalBindingV1) (proofEncoder, error) {
	if len(declaration) != 32 || !validCodexLocal(value) {
		return nil, ErrInvalidNativeProof
	}
	var encoded proofEncoder
	encoded.text("cognate/native-codex-local-binding/v1")
	encoded.u32(value.BindingVersion)
	encoded.blob(declaration)
	encoded.text(value.NativeProfileId)
	encoded.blob(value.NativeProfileSha256)
	encoded.blob(value.TemplateSha256)
	encoded.u32(uint32(value.EffectiveScope))
	encoded.u32(uint32(value.ProviderOwnership))
	encoded.u32(uint32(value.CatalogOwnership))
	codexTextList(&encoded, value.ManagedModels)
	encoded.u32(uint32(value.AuthStore))
	encoded.u32(uint32(value.KeyringState))
	encoded.u32(uint32(value.FileLoginState))
	encoded.text(value.LocalAuthPolicy)
	if *value.RequiresOpenaiAuth {
		encoded.u32(1)
	} else {
		encoded.u32(0)
	}
	encoded.blob(value.ObservationWitness)
	encoded.u32(uint32(len(value.Baselines)))
	for _, baseline := range value.Baselines {
		encoded.u32(uint32(baseline.Role))
		switch state := baseline.State.(type) {
		case *nativev1.CodexRoleBaseline_Missing:
			encoded.u32(1)
			encoded.blob(nil)
		case *nativev1.CodexRoleBaseline_Present:
			encoded.u32(2)
			encoded.blob(state.Present.Sha256)
		case *nativev1.CodexRoleBaseline_NotObserved:
			encoded.u32(3)
			encoded.blob(nil)
		}
	}
	if len(encoded) > 8192 {
		return nil, ErrInvalidNativeProof
	}
	return encoded, nil
}
func HashCodexLocalBinding(declaration []byte, value *nativev1.CodexLocalBindingV1) ([32]byte, error) {
	encoded, err := codexLocalEncoding(declaration, value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}
func HashCodexIntent(preparation *nativev1.PreparationBinding, local *nativev1.CodexLocalBindingV1, confirmationRevision uint64, intentExpiresMS int64) ([32]byte, error) {
	prepared, err := HashCodexPreparation(preparation)
	if err != nil || confirmationRevision == 0 || intentExpiresMS <= preparation.PreparedAtUnixMs || intentExpiresMS > preparation.PrepareExpiresAtUnixMs {
		return [32]byte{}, ErrInvalidNativeProof
	}
	bound, err := HashCodexLocalBinding(preparation.Codex.DeclarationSha256, local)
	if err != nil {
		return [32]byte{}, err
	}
	var encoded proofEncoder
	encoded.text("cognate/native-codex-intent/v1")
	encoded.u32(1)
	encoded.blob(prepared[:])
	encoded.blob(preparation.Codex.DeclarationSha256)
	encoded.blob(bound[:])
	encoded.u32(1)
	encoded.u64(confirmationRevision)
	encodeSlots(&encoded, []nativev1.SecretSlot{3})
	encoded.u64(uint64(intentExpiresMS))
	return sha256.Sum256(encoded), nil
}
func HashCodexApply(value *nativev1.ApplyBinding) ([32]byte, error) {
	if !knownMessage(value) || !validCodexPreparation(value.Preparation) || len(value.IntentCommitment) != 32 || value.IntentExpiresAtUnixMs <= value.Preparation.PreparedAtUnixMs || value.IntentExpiresAtUnixMs > value.Preparation.PrepareExpiresAtUnixMs || !validCodexLocal(value.CodexLocalBinding) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	prepared, err := HashCodexPreparation(value.Preparation)
	if err != nil {
		return [32]byte{}, err
	}
	var encoded proofEncoder
	encoded.text("cognate/native-codex-apply/v1")
	encoded.blob(prepared[:])
	encoded.blob(value.IntentCommitment)
	encoded.u64(uint64(value.IntentExpiresAtUnixMs))
	return sha256.Sum256(encoded), nil
}
func ValidateCodexPreparedDeclaration(value *nativev1.PrepareNativeApplyResponse) error {
	if !knownMessage(value) || !validCodexPreparation(value.Binding) || value.Patch != nil || !value.MaterializationRequired || len(value.AuthorizeChallenge) != 32 {
		return ErrInvalidNativeProof
	}
	hash, err := HashCodexDeclaration(value.CodexDeclaration)
	if err != nil || !bytes.Equal(hash[:], value.Binding.Codex.DeclarationSha256) {
		return ErrInvalidNativeProof
	}
	return nil
}
func codexTranscript(purpose string) proofEncoder {
	var encoded proofEncoder
	encoded.text("cognate/native-codex-proof/v1")
	encoded.text(purpose)
	encoded.u32(1)
	return encoded
}
func CodexPrepareTranscript(binding ConsumerBinding, value *nativev1.PrepareNativeApplyRequest) (Transcript, error) {
	if !validCodexPrepare(value) || !validOrigin(binding.ServerOrigin) || binding.OwnerID != value.ExpectedOwnerId || len(binding.NativeSessionBinding) != 32 || !bytes.Equal(binding.OperationID, value.OperationId) || len(binding.ConsumerKeySHA256) != 32 {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := codexTranscript("prepare")
	encoded.text(binding.ServerOrigin)
	encoded.text(binding.OwnerID)
	encoded.blob(binding.NativeSessionBinding)
	encoded.blob(binding.OperationID)
	encoded.blob(binding.ConsumerKeySHA256)
	encoded.text(value.ExpectedOwnerId)
	encoded.text(value.AssetId)
	encoded.u64(uint64(value.ExpectedAssetRevision))
	encoded.text(value.AgentId)
	encoded.u32(value.SchemaVersion)
	encodeAdapter(&encoded, value.Adapter)
	encoded.u32(uint32(value.Target))
	encoded.u32(uint32(value.Scope))
	encoded.text(value.ProfileId)
	encoded.u64(value.SelectionRevision)
	encoded.blob(value.OperationId)
	encoded.blob(value.ClientNonce)
	encoded.u32(value.CodexContractVersion)
	return finishTranscript(encoded)
}
func validCodexAuthorize(value *nativev1.AuthorizeNativeMaterializationRequest) bool {
	return knownMessage(value) && opaqueID(value.PrepareId) && len(value.OperationId) == 16 && len(value.IntentCommitment) == 32 && slices.Equal(value.RequestedSlots, []nativev1.SecretSlot{3}) && len(value.ClientNonce) == 32 && optionalSignature(value.ProofSignature) && value.GetExplicitConfirmation() != nil && value.GetExplicitConfirmation().ConfirmationRevision > 0 && value.IntentExpiresAtUnixMs > 0 && validCodexLocal(value.CodexLocalBinding)
}
func CodexAuthorizeTranscript(preparation *nativev1.PreparationBinding, challenge []byte, value *nativev1.AuthorizeNativeMaterializationRequest) (Transcript, error) {
	prepared, err := HashCodexPreparation(preparation)
	if err != nil || len(challenge) != 32 || !validCodexAuthorize(value) || value.PrepareId != preparation.PrepareId || !bytes.Equal(value.OperationId, preparation.OperationId) {
		return Transcript{}, ErrInvalidNativeProof
	}
	revision := value.GetExplicitConfirmation().ConfirmationRevision
	intent, err := HashCodexIntent(preparation, value.CodexLocalBinding, revision, value.IntentExpiresAtUnixMs)
	if err != nil || !bytes.Equal(intent[:], value.IntentCommitment) {
		return Transcript{}, ErrInvalidNativeProof
	}
	bound, _ := HashCodexLocalBinding(preparation.Codex.DeclarationSha256, value.CodexLocalBinding)
	encoded := codexTranscript("authorize")
	encoded.blob(prepared[:])
	encoded.blob(challenge)
	encoded.text(value.PrepareId)
	encoded.blob(value.OperationId)
	encoded.blob(value.IntentCommitment)
	encodeSlots(&encoded, value.RequestedSlots)
	encoded.u32(1)
	encoded.u64(revision)
	encoded.blob(value.ClientNonce)
	encoded.u64(uint64(value.IntentExpiresAtUnixMs))
	encoded.blob(bound[:])
	return finishTranscript(encoded)
}
func CodexConsumeTranscript(binding *nativev1.ApplyBinding, authorizeChallenge, consumeChallenge []byte, grantExpiry int64, value *nativev1.ConsumeNativeMaterializationRequest) (Transcript, error) {
	apply, err := HashCodexApply(binding)
	if err != nil || !validConsumeRequest(value) || len(authorizeChallenge) != 32 || len(consumeChallenge) != 32 || value.PrepareId != binding.Preparation.PrepareId || !bytes.Equal(value.OperationId, binding.Preparation.OperationId) || !bytes.Equal(value.IntentCommitment, binding.IntentCommitment) || grantExpiry <= binding.Preparation.PreparedAtUnixMs || grantExpiry > binding.IntentExpiresAtUnixMs {
		return Transcript{}, ErrInvalidNativeProof
	}
	token := sha256.Sum256(value.GrantToken)
	encoded := codexTranscript("consume")
	encoded.blob(apply[:])
	encoded.blob(authorizeChallenge)
	encoded.text(value.GrantId)
	encoded.blob(token[:])
	encoded.blob(consumeChallenge)
	encoded.u64(uint64(grantExpiry))
	encoded.blob(value.OperationId)
	encoded.blob(value.IntentCommitment)
	encoded.blob(value.ClientNonce)
	return finishTranscript(encoded)
}
func CodexStatusTranscript(preparation *nativev1.PreparationBinding, challenge []byte, value *nativev1.GetNativeMaterializationStatusRequest) (Transcript, error) {
	prepared, err := HashCodexPreparation(preparation)
	if err != nil || len(challenge) != 32 || !validStatusRequest(value) || value.PrepareId != preparation.PrepareId || !bytes.Equal(value.OperationId, preparation.OperationId) {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := codexTranscript("status")
	encoded.blob(prepared[:])
	encoded.blob(challenge)
	encoded.text(value.PrepareId)
	encoded.text(value.GrantId)
	encoded.blob(value.OperationId)
	encoded.blob(value.ClientNonce)
	return finishTranscript(encoded)
}
