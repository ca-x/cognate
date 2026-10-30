package nativesessionauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json/v2"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const codexDefinition = "f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4"

type CodexPrepareRequest struct {
	PrepareRequest
	ContractVersion uint32
}

type CodexPrepareFields struct {
	Consumer ConsumerFields
	Request  CodexPrepareRequest
}

type codexPreparationData struct {
	consumer ConsumerFields
	request  CodexPrepareRequest
	wire     *nativev1.PreparationBinding
}

type codexLocalData struct {
	declaration [32]byte
	wire        *nativev1.CodexLocalBindingV1
}

type codexApplyData struct {
	preparation CodexPreparation
	local       CodexLocalBinding
	revision    uint64
	commitment  [32]byte
	expiry      int64
}

type CodexPreparation struct{ opaque[codexPreparationData] }
type CodexLocalBinding struct{ opaque[codexLocalData] }
type CodexApply struct{ opaque[codexApplyData] }

type CodexAuthorizeFields struct {
	Preparation CodexPreparation
	Local       CodexLocalBinding
	Challenge   [32]byte
	Request     AuthorizeRequest
}

type CodexAuthorizeRequest struct {
	AuthorizeRequest
	Local CodexLocalBinding
}

type CodexConsumeFields struct {
	Apply                                CodexApply
	AuthorizeChallenge, ConsumeChallenge [32]byte
	GrantExpiresMS                       int64
	Request                              ConsumeRequest
}

type CodexStatusFields struct {
	Preparation CodexPreparation
	Challenge   [32]byte
	Request     StatusRequest
}

func NewCodexPreparation(consumer ConsumerFields, request CodexPrepareRequest, prepareID string, preparedMS, expiresMS int64, declarationSHA256 [32]byte) (CodexPreparation, error) {
	if _, err := codexPrepareCanonical(CodexPrepareFields{Consumer: consumer, Request: request}); err != nil || !validID(prepareID) || preparedMS <= 0 || expiresMS <= preparedMS || expiresMS-preparedMS > 300000 {
		return CodexPreparation{}, ErrInvalidEvidence
	}
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	wire := &nativev1.PreparationBinding{OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: slices.Clone(consumer.SessionBinding[:]), OperationId: slices.Clone(consumer.OperationID[:]), AssetId: request.AssetID, AssetRevision: request.AssetRevision, AgentId: request.AgentID, AssetKind: configurationv1.AssetKind(1), SchemaVersion: request.SchemaVersion, Adapter: &nativev1.AdapterBinding{ApiVersion: request.Adapter.APIVersion, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: nativev1.Target(2), Scope: nativev1.Scope(1), ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, RequiredSlots: []nativev1.SecretSlot{3}, PrepareExpiresAtUnixMs: expiresMS, ConsumerKeySha256: slices.Clone(keyHash[:]), PrepareId: prepareID, PreparedAtUnixMs: preparedMS, Codex: &nativev1.CodexPreparationV1{ContractVersion: 1, DeclarationSha256: slices.Clone(declarationSHA256[:])}}
	return CodexPreparation{opaque: wrap(codexPreparationData{consumer: consumer, request: request, wire: wire})}, nil
}

func (value CodexPreparation) CopyWire() *nativev1.PreparationBinding {
	if !value.present() {
		return nil
	}
	return proto.Clone(value.payload.value.wire).(*nativev1.PreparationBinding)
}
func (value CodexPreparation) CopyRequest() CodexPrepareRequest {
	if !value.present() {
		return CodexPrepareRequest{}
	}
	return value.payload.value.request
}
func (value CodexPreparation) CopyCanonical() ([]byte, error) {
	return codexPreparationBytes(value)
}
func (value CodexPreparation) CopyConsumer() ConsumerFields {
	if !value.present() {
		return ConsumerFields{}
	}
	return value.payload.value.consumer
}
func (value CodexLocalBinding) CopyWire() *nativev1.CodexLocalBindingV1 {
	if !value.present() {
		return nil
	}
	return proto.Clone(value.payload.value.wire).(*nativev1.CodexLocalBindingV1)
}
func (value CodexLocalBinding) DeclarationSHA256() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.payload.value.declaration
}
func (value CodexLocalBinding) CopyCanonical() ([]byte, error) {
	return codexLocalBytes(value)
}
func (value CodexApply) CopyWire() *nativev1.ApplyBinding {
	if !value.present() {
		return nil
	}
	data := value.payload.value
	return &nativev1.ApplyBinding{Preparation: data.preparation.CopyWire(), IntentCommitment: slices.Clone(data.commitment[:]), IntentExpiresAtUnixMs: data.expiry, CodexLocalBinding: data.local.CopyWire()}
}
func (value CodexApply) Preparation() CodexPreparation {
	if !value.present() {
		return CodexPreparation{}
	}
	return value.payload.value.preparation
}
func (value CodexApply) LocalBinding() CodexLocalBinding {
	if !value.present() {
		return CodexLocalBinding{}
	}
	return value.payload.value.local
}
func (value CodexApply) Commitment() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.payload.value.commitment
}
func (value CodexApply) ConfirmationRevision() uint64 {
	if !value.present() {
		return 0
	}
	return value.payload.value.revision
}

func codexModelID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !letter && (index == 0 || !strings.ContainsRune("._:/-", rune(character))) {
			return false
		}
	}
	return true
}
func codexLiteral(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func codexEffort(value agentconfig.CodexReasoningEffort) bool {
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}
func codexDeclarationValid(value agentconfig.CodexProviderDeclarationV1) bool {
	if value.APIVersion != 1 || value.Target != agentconfig.CodexConfig || value.ProviderID != "cognate" || value.WireAPI != "responses" || value.CredentialReference != agentconfig.CodexProviderKeySecret || !slices.Equal(value.RequiredSlots, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) || value.LocalAuthPolicy != agentconfig.CodexPreserveLocalLoginV1 || !codexLiteral(value.Endpoint, 2048) || strings.ContainsAny(value.Endpoint, "?#\\") || !codexModelID(value.Model) || value.ReasoningEffort != nil && !codexEffort(*value.ReasoningEffort) {
		return false
	}
	endpoint, err := url.Parse(value.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || !utf8.ValidString(endpoint.Path) || strings.ContainsFunc(endpoint.Path, unicode.IsControl) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	if value.Catalog != nil {
		if len(value.Catalog.Models) < 1 || len(value.Catalog.Models) > 32 {
			return false
		}
		seen := make(map[string]bool, len(value.Catalog.Models))
		for _, model := range value.Catalog.Models {
			if !codexModelID(model.Model) || seen[model.Model] || model.DisplayName != nil && !codexLiteral(*model.DisplayName, 256) || model.ContextWindow != nil && (*model.ContextWindow == 0 || *model.ContextWindow > 2147483647) {
				return false
			}
			seen[model.Model] = true
			if model.BaseInstructions != nil {
				body := *model.BaseInstructions
				if len(body) < 1 || len(body) > 2048 || !utf8.ValidString(body) || strings.ContainsFunc(body, func(character rune) bool {
					return unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t'
				}) {
					return false
				}
			}
			if model.InputModalities != nil {
				items := *model.InputModalities
				if len(items) < 1 || len(items) > 2 {
					return false
				}
				for index, item := range items {
					if item != "text" && item != "image" || slices.Contains(items[:index], item) {
						return false
					}
				}
			}
			if model.ReasoningLevels != nil {
				items := *model.ReasoningLevels
				if len(items) < 1 || len(items) > 7 {
					return false
				}
				for index, item := range items {
					if !codexEffort(item) || slices.Contains(items[:index], item) {
						return false
					}
				}
			}
			if model.DefaultReasoningLevel != nil && (model.ReasoningLevels == nil || !slices.Contains(*model.ReasoningLevels, *model.DefaultReasoningLevel)) {
				return false
			}
			if model.Model == value.Model && value.ReasoningEffort != nil && model.ReasoningLevels != nil && !slices.Contains(*model.ReasoningLevels, *value.ReasoningEffort) {
				return false
			}
		}
		if !seen[value.Model] {
			return false
		}
		encoded, err := json.Marshal(value.Catalog)
		if err != nil || len(encoded) > agentconfig.MaxCodexCatalogBytes {
			return false
		}
	}
	desired := agentconfig.CodexProviderDesired{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: value.Endpoint, Model: value.Model, ReasoningEffort: value.ReasoningEffort, KeyPresent: true, Catalog: value.Catalog}
	encoded, err := json.Marshal(desired)
	if err != nil || len(encoded) > agentconfig.MaxCodexDesiredBytes {
		return false
	}
	encoded, err = json.Marshal(value)
	return err == nil && len(encoded) <= agentconfig.MaxCodexDeclarationBytes
}

type codexEncoder []byte

func (encoded *codexEncoder) number(value uint32) {
	*encoded = binary.BigEndian.AppendUint32(*encoded, value)
}
func (encoded *codexEncoder) wide(value uint64) {
	*encoded = binary.BigEndian.AppendUint64(*encoded, value)
}
func (encoded *codexEncoder) bytes(value []byte) { *encoded = blob(*encoded, value) }
func (encoded *codexEncoder) text(value string)  { encoded.bytes([]byte(value)) }
func (encoded *codexEncoder) presence(present bool) {
	if present {
		encoded.number(1)
	} else {
		encoded.number(0)
	}
}

func HashCodexDeclaration(value agentconfig.CodexProviderDeclarationV1) ([32]byte, error) {
	if !codexDeclarationValid(value) {
		return [32]byte{}, ErrInvalidEvidence
	}
	var encoded codexEncoder
	encoded.text("cognate/native-codex-declaration/v1")
	encoded.number(1)
	encoded.number(uint32(value.APIVersion))
	for _, text := range []string{string(value.Target), value.ProviderID, value.WireAPI, value.Endpoint, value.Model} {
		encoded.text(text)
	}
	encoded.presence(value.ReasoningEffort != nil)
	if value.ReasoningEffort != nil {
		encoded.text(string(*value.ReasoningEffort))
	}
	encoded.presence(value.Catalog != nil)
	if value.Catalog != nil {
		encoded.number(uint32(len(value.Catalog.Models)))
		for _, model := range value.Catalog.Models {
			encoded.text(model.Model)
			encoded.presence(model.DisplayName != nil)
			if model.DisplayName != nil {
				encoded.text(*model.DisplayName)
			}
			encoded.presence(model.ContextWindow != nil)
			if model.ContextWindow != nil {
				encoded.wide(*model.ContextWindow)
			}
			encoded.presence(model.SupportsParallelToolCalls != nil)
			if model.SupportsParallelToolCalls != nil {
				encoded.presence(*model.SupportsParallelToolCalls)
			}
			encoded.presence(model.InputModalities != nil)
			if model.InputModalities != nil {
				encoded.number(uint32(len(*model.InputModalities)))
				for _, item := range *model.InputModalities {
					encoded.text(string(item))
				}
			}
			encoded.presence(model.BaseInstructions != nil)
			if model.BaseInstructions != nil {
				encoded.text(*model.BaseInstructions)
			}
			encoded.presence(model.ReasoningLevels != nil)
			if model.ReasoningLevels != nil {
				encoded.number(uint32(len(*model.ReasoningLevels)))
				for _, item := range *model.ReasoningLevels {
					encoded.text(string(item))
				}
			}
			encoded.presence(model.DefaultReasoningLevel != nil)
			if model.DefaultReasoningLevel != nil {
				encoded.text(string(*model.DefaultReasoningLevel))
			}
		}
	}
	encoded.text(string(value.CredentialReference))
	encoded.number(uint32(len(value.RequiredSlots)))
	for _, slot := range value.RequiredSlots {
		encoded.text(string(slot))
	}
	encoded.text(string(value.LocalAuthPolicy))
	if len(encoded) > 64<<10 {
		return [32]byte{}, ErrInvalidEvidence
	}
	return sha256.Sum256(encoded), nil
}

func codexRequestValid(consumer ConsumerFields, request CodexPrepareRequest) bool {
	adapter := request.Adapter
	return validApplyOwner(consumer.OwnerID) && ValidServerOrigin(consumer.ServerOrigin) && request.OwnerID == consumer.OwnerID && request.OperationID == consumer.OperationID && validApplyAsset(request.AssetID) && request.AssetRevision > 0 && request.AgentID == "codex" && request.SchemaVersion == 1 && request.Target == 2 && request.Scope == 1 && validID(request.ProfileID) && request.SelectionRevision > 0 && request.ContractVersion == 1 && adapter.APIVersion == 1 && adapter.PluginID == "builtin.codex" && adapter.PluginVersion == "1.1.0" && adapter.DefinitionDigest == codexDefinition && adapter.Capability == "codex.provider-plan.v1"
}
func codexPrefix(purpose string) codexEncoder {
	var encoded codexEncoder
	encoded.text("cognate/native-codex-proof/v1")
	encoded.text(purpose)
	encoded.number(1)
	return encoded
}
func codexAdapter(encoded *codexEncoder, adapter AdapterFields) {
	encoded.number(adapter.APIVersion)
	encoded.text(adapter.PluginID)
	encoded.text(adapter.PluginVersion)
	encoded.text(adapter.DefinitionDigest)
	encoded.number(1)
	encoded.text(adapter.Capability)
}
func codexPrepareCanonical(fields CodexPrepareFields) ([]byte, error) {
	consumer, request := fields.Consumer, fields.Request
	if !codexRequestValid(consumer, request) {
		return nil, ErrInvalidEvidence
	}
	encoded := codexPrefix("prepare")
	encoded.text(consumer.ServerOrigin)
	encoded.text(consumer.OwnerID)
	encoded.bytes(consumer.SessionBinding[:])
	encoded.bytes(consumer.OperationID[:])
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	encoded.bytes(keyHash[:])
	encoded.text(request.OwnerID)
	encoded.text(request.AssetID)
	encoded.wide(uint64(request.AssetRevision))
	encoded.text(request.AgentID)
	encoded.number(request.SchemaVersion)
	codexAdapter(&encoded, request.Adapter)
	encoded.number(request.Target)
	encoded.number(request.Scope)
	encoded.text(request.ProfileID)
	encoded.wide(request.SelectionRevision)
	encoded.bytes(request.OperationID[:])
	encoded.bytes(request.Nonce[:])
	encoded.number(request.ContractVersion)
	return encoded, nil
}
func codexPreparationBytes(value CodexPreparation) ([]byte, error) {
	if !value.present() {
		return nil, ErrInvalidEvidence
	}
	data := value.payload.value
	consumer, request, wire := data.consumer, data.request, data.wire
	var encoded codexEncoder
	encoded.text(consumer.OwnerID)
	encoded.text(consumer.ServerOrigin)
	encoded.bytes(consumer.SessionBinding[:])
	encoded.bytes(consumer.OperationID[:])
	encoded.text(request.AssetID)
	encoded.wide(uint64(request.AssetRevision))
	encoded.text(request.AgentID)
	encoded.number(1)
	encoded.number(request.SchemaVersion)
	codexAdapter(&encoded, request.Adapter)
	encoded.number(request.Target)
	encoded.number(request.Scope)
	encoded.text(request.ProfileID)
	encoded.wide(request.SelectionRevision)
	encoded.number(1)
	encoded.number(3)
	encoded.wide(uint64(wire.PrepareExpiresAtUnixMs))
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	encoded.bytes(keyHash[:])
	encoded.text(wire.PrepareId)
	encoded.wide(uint64(wire.PreparedAtUnixMs))
	encoded.number(request.ContractVersion)
	encoded.bytes(wire.Codex.DeclarationSha256)
	if len(encoded) > 4096 {
		return nil, ErrInvalidEvidence
	}
	return encoded, nil
}
func HashCodexPreparation(value CodexPreparation) ([32]byte, error) {
	encoded, err := codexPreparationBytes(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(append(blob(nil, []byte("cognate/native-codex-preparation/v1")), encoded...)), nil
}

func codexKnown(message protoreflect.Message, depth int, budget *int) bool {
	if !message.IsValid() || depth >= 16 || len(message.GetUnknown()) != 0 {
		return false
	}
	valid := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		check := func(value protoreflect.Value) bool {
			*budget--
			if *budget < 0 {
				return false
			}
			switch field.Kind() {
			case protoreflect.MessageKind:
				return codexKnown(value.Message(), depth+1, budget)
			case protoreflect.EnumKind:
				return value.Enum() > 0 && field.Enum().Values().ByNumber(value.Enum()) != nil
			case protoreflect.StringKind:
				return utf8.ValidString(value.String())
			}
			return true
		}
		if field.IsMap() {
			valid = false
			return false
		}
		if field.IsList() {
			list := value.List()
			if list.Len() > *budget {
				valid = false
				return false
			}
			for index := range list.Len() {
				if !check(list.Get(index)) {
					valid = false
					return false
				}
			}
		} else {
			valid = check(value)
		}
		return valid
	})
	return valid
}
func NewCodexLocalBinding(declarationSHA256 [32]byte, wire *nativev1.CodexLocalBindingV1) (CodexLocalBinding, error) {
	budget := 1024
	if wire == nil || !codexKnown(wire.ProtoReflect(), 0, &budget) || wire.BindingVersion != 1 || !codexModelID(wire.NativeProfileId) || len(wire.NativeProfileSha256) != 32 || len(wire.TemplateSha256) != 32 || wire.EffectiveScope != 1 || wire.ProviderOwnership != 1 && wire.ProviderOwnership != 2 || wire.CatalogOwnership < 1 || wire.CatalogOwnership > 3 || len(wire.ManagedModels) > 32 || wire.RequiresOpenaiAuth == nil || wire.LocalAuthPolicy != "preserve-local-login-v1" || len(wire.ObservationWitness) != 32 || [32]byte(wire.ObservationWitness) == [32]byte{} || len(wire.Baselines) != 3 {
		return CodexLocalBinding{}, ErrInvalidEvidence
	}
	for index, model := range wire.ManagedModels {
		if !codexModelID(model) || slices.Contains(wire.ManagedModels[:index], model) {
			return CodexLocalBinding{}, ErrInvalidEvidence
		}
	}
	if wire.CatalogOwnership != 2 && len(wire.ManagedModels) != 0 {
		return CodexLocalBinding{}, ErrInvalidEvidence
	}
	for index, baseline := range wire.Baselines {
		if baseline == nil || baseline.Role != nativev1.CodexRole(index+1) {
			return CodexLocalBinding{}, ErrInvalidEvidence
		}
		switch state := baseline.State.(type) {
		case *nativev1.CodexRoleBaseline_Missing:
			if state.Missing == nil {
				return CodexLocalBinding{}, ErrInvalidEvidence
			}
		case *nativev1.CodexRoleBaseline_Present:
			if state.Present == nil || len(state.Present.Sha256) != 32 {
				return CodexLocalBinding{}, ErrInvalidEvidence
			}
		case *nativev1.CodexRoleBaseline_NotObserved:
			if state.NotObserved == nil || index == 0 {
				return CodexLocalBinding{}, ErrInvalidEvidence
			}
		default:
			return CodexLocalBinding{}, ErrInvalidEvidence
		}
	}
	if wire.KeyringState < 1 || wire.KeyringState > 4 || wire.FileLoginState < 1 || wire.FileLoginState > 4 {
		return CodexLocalBinding{}, ErrInvalidEvidence
	}
	fileObserved := wire.Baselines[1].GetNotObserved() == nil
	login := false
	switch wire.AuthStore {
	case 1:
		if !fileObserved || wire.FileLoginState != 1 && wire.FileLoginState != 2 {
			return CodexLocalBinding{}, ErrInvalidEvidence
		}
		login = wire.FileLoginState == 1
	case 2:
		if wire.KeyringState != 1 && wire.KeyringState != 2 {
			return CodexLocalBinding{}, ErrInvalidEvidence
		}
		login = wire.KeyringState == 1
	case 3:
		if wire.KeyringState == 1 {
			login = true
		} else if wire.KeyringState == 2 {
			if !fileObserved || wire.FileLoginState != 1 && wire.FileLoginState != 2 {
				return CodexLocalBinding{}, ErrInvalidEvidence
			}
			login = wire.FileLoginState == 1
		} else {
			return CodexLocalBinding{}, ErrInvalidEvidence
		}
	default:
		return CodexLocalBinding{}, ErrInvalidEvidence
	}
	if login != *wire.RequiresOpenaiAuth || wire.Baselines[1].GetPresent() == nil && wire.FileLoginState == 1 {
		return CodexLocalBinding{}, ErrInvalidEvidence
	}
	value := CodexLocalBinding{opaque: wrap(codexLocalData{declaration: declarationSHA256, wire: proto.Clone(wire).(*nativev1.CodexLocalBindingV1)})}
	if encoded, _ := codexLocalBytes(value); len(encoded) > 8192 {
		return CodexLocalBinding{}, ErrInvalidEvidence
	}
	return value, nil
}
func codexLocalBytes(value CodexLocalBinding) ([]byte, error) {
	if !value.present() {
		return nil, ErrInvalidEvidence
	}
	data := value.payload.value
	wire := data.wire
	var encoded codexEncoder
	encoded.text("cognate/native-codex-local-binding/v1")
	encoded.number(wire.BindingVersion)
	encoded.bytes(data.declaration[:])
	encoded.text(wire.NativeProfileId)
	encoded.bytes(wire.NativeProfileSha256)
	encoded.bytes(wire.TemplateSha256)
	encoded.number(uint32(wire.EffectiveScope))
	encoded.number(uint32(wire.ProviderOwnership))
	encoded.number(uint32(wire.CatalogOwnership))
	encoded.number(uint32(len(wire.ManagedModels)))
	for _, model := range wire.ManagedModels {
		encoded.text(model)
	}
	encoded.number(uint32(wire.AuthStore))
	encoded.number(uint32(wire.KeyringState))
	encoded.number(uint32(wire.FileLoginState))
	encoded.text(wire.LocalAuthPolicy)
	encoded.presence(*wire.RequiresOpenaiAuth)
	encoded.bytes(wire.ObservationWitness)
	encoded.number(3)
	for _, baseline := range wire.Baselines {
		encoded.number(uint32(baseline.Role))
		switch state := baseline.State.(type) {
		case *nativev1.CodexRoleBaseline_Missing:
			encoded.number(1)
			encoded.bytes(nil)
		case *nativev1.CodexRoleBaseline_Present:
			encoded.number(2)
			encoded.bytes(state.Present.Sha256)
		case *nativev1.CodexRoleBaseline_NotObserved:
			encoded.number(3)
			encoded.bytes(nil)
		}
	}
	return encoded, nil
}
func HashCodexLocalBinding(value CodexLocalBinding) ([32]byte, error) {
	encoded, err := codexLocalBytes(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}
func HashCodexIntent(preparation CodexPreparation, local CodexLocalBinding, confirmationRevision uint64, intentExpiresMS int64) ([32]byte, error) {
	preparationHash, err := HashCodexPreparation(preparation)
	if err != nil || !local.present() || confirmationRevision == 0 || intentExpiresMS <= preparation.payload.value.wire.PreparedAtUnixMs || intentExpiresMS > preparation.payload.value.wire.PrepareExpiresAtUnixMs || local.payload.value.declaration != [32]byte(preparation.payload.value.wire.Codex.DeclarationSha256) {
		return [32]byte{}, ErrInvalidEvidence
	}
	localHash, err := HashCodexLocalBinding(local)
	if err != nil {
		return [32]byte{}, err
	}
	var encoded codexEncoder
	encoded.text("cognate/native-codex-intent/v1")
	encoded.number(1)
	encoded.bytes(preparationHash[:])
	encoded.bytes(local.payload.value.declaration[:])
	encoded.bytes(localHash[:])
	encoded.number(1)
	encoded.wide(confirmationRevision)
	encoded.number(1)
	encoded.number(3)
	encoded.wide(uint64(intentExpiresMS))
	return sha256.Sum256(encoded), nil
}
func NewCodexApply(preparation CodexPreparation, local CodexLocalBinding, confirmationRevision uint64, intentExpiresMS int64) (CodexApply, error) {
	commitment, err := HashCodexIntent(preparation, local, confirmationRevision, intentExpiresMS)
	if err != nil {
		return CodexApply{}, err
	}
	return CodexApply{opaque: wrap(codexApplyData{preparation: preparation, local: local, revision: confirmationRevision, commitment: commitment, expiry: intentExpiresMS})}, nil
}
func HashCodexApply(preparation CodexPreparation, commitment [32]byte, intentExpiresMS int64) ([32]byte, error) {
	preparationHash, err := HashCodexPreparation(preparation)
	if err != nil || intentExpiresMS <= preparation.payload.value.wire.PreparedAtUnixMs || intentExpiresMS > preparation.payload.value.wire.PrepareExpiresAtUnixMs {
		return [32]byte{}, ErrInvalidEvidence
	}
	var encoded codexEncoder
	encoded.text("cognate/native-codex-apply/v1")
	encoded.bytes(preparationHash[:])
	encoded.bytes(commitment[:])
	encoded.wide(uint64(intentExpiresMS))
	return sha256.Sum256(encoded), nil
}
func codexAuthorizeCanonical(fields CodexAuthorizeFields) ([]byte, error) {
	request := fields.Request
	preparationHash, err := HashCodexPreparation(fields.Preparation)
	if err != nil {
		return nil, err
	}
	preparation := fields.Preparation.payload.value
	commitment, err := HashCodexIntent(fields.Preparation, fields.Local, request.Consent.Revision, request.IntentExpiresMS)
	if err != nil || commitment != request.Commitment || request.PrepareID != preparation.wire.PrepareId || request.OperationID != preparation.consumer.OperationID || request.RequiredSlot != 3 || request.Consent.Kind != 1 || request.Consent.PolicyID != "" {
		return nil, ErrInvalidEvidence
	}
	localHash, _ := HashCodexLocalBinding(fields.Local)
	encoded := codexPrefix("authorize")
	encoded.bytes(preparationHash[:])
	encoded.bytes(fields.Challenge[:])
	encoded.text(request.PrepareID)
	encoded.bytes(request.OperationID[:])
	encoded.bytes(commitment[:])
	encoded.number(1)
	encoded.number(3)
	encoded.number(1)
	encoded.wide(request.Consent.Revision)
	encoded.bytes(request.Nonce[:])
	encoded.wide(uint64(request.IntentExpiresMS))
	encoded.bytes(localHash[:])
	return encoded, nil
}
func codexConsumeCanonical(fields CodexConsumeFields) ([]byte, error) {
	if !fields.Apply.present() {
		return nil, ErrInvalidEvidence
	}
	data := fields.Apply.payload.value
	request := fields.Request
	preparation := data.preparation.payload.value
	applyHash, err := HashCodexApply(data.preparation, data.commitment, data.expiry)
	tokenHash, present := request.Token.TokenDigest()
	if err != nil || !present || request.PrepareID != preparation.wire.PrepareId || !validID(request.GrantID) || request.OperationID != preparation.consumer.OperationID || request.Commitment != data.commitment || fields.GrantExpiresMS <= preparation.wire.PreparedAtUnixMs || fields.GrantExpiresMS > data.expiry {
		return nil, ErrInvalidEvidence
	}
	encoded := codexPrefix("consume")
	encoded.bytes(applyHash[:])
	encoded.bytes(fields.AuthorizeChallenge[:])
	encoded.text(request.GrantID)
	encoded.bytes(tokenHash[:])
	encoded.bytes(fields.ConsumeChallenge[:])
	encoded.wide(uint64(fields.GrantExpiresMS))
	encoded.bytes(request.OperationID[:])
	encoded.bytes(data.commitment[:])
	encoded.bytes(request.Nonce[:])
	return encoded, nil
}
func codexStatusCanonical(fields CodexStatusFields) ([]byte, error) {
	preparationHash, err := HashCodexPreparation(fields.Preparation)
	request := fields.Request
	if err != nil || request.PrepareID != fields.Preparation.payload.value.wire.PrepareId || request.OperationID != fields.Preparation.payload.value.consumer.OperationID || request.GrantID != "" && !validID(request.GrantID) {
		return nil, ErrInvalidEvidence
	}
	encoded := codexPrefix("status")
	encoded.bytes(preparationHash[:])
	encoded.bytes(fields.Challenge[:])
	encoded.text(request.PrepareID)
	encoded.text(request.GrantID)
	encoded.bytes(request.OperationID[:])
	encoded.bytes(request.Nonce[:])
	return encoded, nil
}
func codexStageDigest(encoded []byte) [32]byte {
	return sha256.Sum256(blob(blob(nil, []byte("cognate/native-codex-stage-evidence/v1")), encoded))
}
func CodexPrepareFieldsDigest(fields CodexPrepareFields) ([32]byte, error) {
	encoded, err := codexPrepareCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return codexStageDigest(encoded), nil
}
func CodexAuthorizeFieldsDigest(fields CodexAuthorizeFields) ([32]byte, error) {
	encoded, err := codexAuthorizeCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return codexStageDigest(encoded), nil
}
func CodexConsumeFieldsDigest(fields CodexConsumeFields) ([32]byte, error) {
	encoded, err := codexConsumeCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return codexStageDigest(encoded), nil
}
func CodexStatusFieldsDigest(fields CodexStatusFields) ([32]byte, error) {
	encoded, err := codexStatusCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return codexStageDigest(encoded), nil
}
func (verifier *Verifier) verifyCodex(bearer ParsedNativeBearer, purpose string, publicKey [32]byte, canonical, signature []byte) (VerifiedApplyProof, error) {
	digest, present := bearer.TokenDigest()
	if verifier == nil || !verifier.present() || !present || len(canonical) == 0 || len(canonical) > 512<<10 || len(signature) != 64 || !ed25519.Verify(publicKey[:], canonical, signature) {
		return VerifiedApplyProof{}, ErrInvalidEvidence
	}
	return VerifiedApplyProof{opaque: wrap(applyEvidence{native: digest, canonical: codexStageDigest(canonical), keyID: verifier.payload.value.keyID, purpose: purpose})}, nil
}
func (verifier *Verifier) VerifyCodexPrepare(bearer ParsedNativeBearer, fields CodexPrepareFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := codexPrepareCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyCodex(bearer, "prepare", fields.Consumer.PublicKey, encoded, signature)
}
func (verifier *Verifier) VerifyCodexAuthorize(bearer ParsedNativeBearer, fields CodexAuthorizeFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := codexAuthorizeCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyCodex(bearer, "authorize", fields.Preparation.payload.value.consumer.PublicKey, encoded, signature)
}
func (verifier *Verifier) VerifyCodexConsume(bearer ParsedNativeBearer, fields CodexConsumeFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := codexConsumeCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyCodex(bearer, "consume", fields.Apply.payload.value.preparation.payload.value.consumer.PublicKey, encoded, signature)
}
func (verifier *Verifier) VerifyCodexStatus(bearer ParsedNativeBearer, fields CodexStatusFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := codexStatusCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyCodex(bearer, "status", fields.Preparation.payload.value.consumer.PublicKey, encoded, signature)
}
