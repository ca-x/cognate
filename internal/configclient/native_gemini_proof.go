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
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/proto"
)

func geminiKnown(message proto.Message) bool {
	return knownMessage(message) && proto.Size(message) <= 16<<10
}
func geminiExclusiveMessage(message proto.Message) bool {
	switch value := message.(type) {
	case *nativev1.PrepareNativeApplyRequest:
		switch value.Target {
		case 1:
			return value.CodexContractVersion == 0 && value.GeminiContractVersion == 0
		case 2:
			return value.GeminiContractVersion == 0
		case 3:
			return value.CodexContractVersion == 0
		}
	case *nativev1.PreparationBinding:
		switch value.Target {
		case 1:
			return value.Codex == nil && value.Gemini == nil
		case 2:
			return value.Gemini == nil && len(value.PatchSha256) == 0
		case 3:
			return value.Codex == nil && len(value.PatchSha256) == 0
		}
	case *nativev1.ApplyBinding:
		switch value.GetPreparation().GetTarget() {
		case 1:
			return value.CodexLocalBinding == nil && value.GeminiLocalBinding == nil
		case 2:
			return value.GeminiLocalBinding == nil
		case 3:
			return value.CodexLocalBinding == nil
		}
	case *nativev1.AuthorizeNativeMaterializationRequest:
		return value.CodexLocalBinding == nil || value.GeminiLocalBinding == nil
	case *nativev1.PrepareNativeApplyResponse:
		switch value.GetBinding().GetTarget() {
		case 1:
			return value.CodexDeclaration == nil && value.GeminiDeclaration == nil
		case 2:
			return value.Patch == nil && value.GeminiDeclaration == nil
		case 3:
			return value.Patch == nil && value.CodexDeclaration == nil
		}
	default:
		return true
	}
	return false
}

func geminiPrefix(purpose string) proofEncoder {
	var encoded proofEncoder
	encoded.text("cognate-native-gemini/1/" + purpose)
	return encoded
}
func geminiHash(purpose string, body []byte) [32]byte {
	encoded := geminiPrefix(purpose)
	encoded.blob(body)
	return sha256.Sum256(encoded)
}
func geminiModel(value string) bool {
	if len(value) < 1 || len(value) > 256 {
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
func validGeminiDeclaration(value agentconfig.GeminiProviderDeclarationV1) bool {
	if value.Version != 1 || value.Target != agentconfig.GeminiSettings || value.LocalAuthPolicy != agentconfig.GeminiPreserveLocalLoginV1 || value.RequiredSlots == nil {
		return false
	}
	switch value.AuthMode {
	case agentconfig.GeminiNativeLogin:
		if value.SelectedType != "oauth-personal" || value.Endpoint != nil || value.KeyTransport != nil || value.CredentialReference != nil || len(value.RequiredSlots) != 0 {
			return false
		}
	case agentconfig.GeminiAPIKeyAuth:
		if value.SelectedType != "gemini-api-key" || value.KeyTransport == nil || *value.KeyTransport != agentconfig.GeminiNativeDefault && *value.KeyTransport != agentconfig.GeminiBearer || value.CredentialReference == nil || *value.CredentialReference != agentconfig.GeminiAPIKeySecret || !slices.Equal(value.RequiredSlots, []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}) {
			return false
		}
	default:
		return false
	}
	for _, model := range []*string{value.EnvModel, value.SettingsModel} {
		if model != nil && !geminiModel(*model) {
			return false
		}
	}
	if value.Endpoint != nil {
		text := *value.Endpoint
		endpoint, err := url.Parse(text)
		if !codexLabel(text, 2048) || strings.ContainsAny(text, "?#\\") || err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || !utf8.ValidString(endpoint.Path) || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) {
			return false
		}
		if endpoint.Port() != "" {
			port, err := strconv.Atoi(endpoint.Port())
			if err != nil || port < 1 || port > 65535 {
				return false
			}
		}
	}
	return true
}
func hashGeminiDeclaration(value agentconfig.GeminiProviderDeclarationV1) ([32]byte, error) {
	if !validGeminiDeclaration(value) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > agentconfig.MaxGeminiDeclarationBytes {
		return [32]byte{}, ErrInvalidNativeProof
	}
	return geminiHash("declaration", body), nil
}
func decodeGeminiDeclaration(wire *nativev1.GeminiDeclarationV1) (agentconfig.GeminiProviderDeclarationV1, error) {
	if !geminiKnown(wire) {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidNativeProof
	}
	value := agentconfig.GeminiProviderDeclarationV1{Version: int(wire.Version), Target: agentconfig.Target(wire.Target), SelectedType: wire.SelectedType, LocalAuthPolicy: agentconfig.GeminiLocalAuthPolicy(wire.LocalAuthPolicy), RequiredSlots: []agentconfig.GeminiSecretSlot{}}
	if wire.Endpoint != nil {
		value.Endpoint = new(*wire.Endpoint)
	}
	if wire.EnvModel != nil {
		value.EnvModel = new(*wire.EnvModel)
	}
	if wire.SettingsModel != nil {
		value.SettingsModel = new(*wire.SettingsModel)
	}
	switch wire.AuthMode {
	case 1:
		value.AuthMode = agentconfig.GeminiNativeLogin
	case 2:
		value.AuthMode = agentconfig.GeminiAPIKeyAuth
	default:
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidNativeProof
	}
	if wire.KeyTransport != nil {
		switch *wire.KeyTransport {
		case 1:
			value.KeyTransport = new(agentconfig.GeminiNativeDefault)
		case 2:
			value.KeyTransport = new(agentconfig.GeminiBearer)
		default:
			return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidNativeProof
		}
	}
	if wire.CredentialReference != nil {
		value.CredentialReference = new(agentconfig.GeminiSecretSlot(*wire.CredentialReference))
	}
	for _, slot := range wire.RequiredSlots {
		if slot != 4 {
			return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidNativeProof
		}
		value.RequiredSlots = append(value.RequiredSlots, agentconfig.GeminiAPIKeySecret)
	}
	if _, err := hashGeminiDeclaration(value); err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, err
	}
	return value, nil
}
func GeminiPrepareWireCanonical(origin, owner string, session, operation, keyHash []byte, wire *nativev1.PrepareNativeApplyRequest) ([]byte, error) {
	if !geminiKnown(wire) || !validOwner(owner) || !validOrigin(origin) || len(session) != 32 || len(operation) != 16 || len(keyHash) != 32 || wire.ExpectedOwnerId != owner || !bytes.Equal(wire.OperationId, operation) || !assetID(wire.AssetId) || wire.ExpectedAssetRevision <= 0 || wire.AgentId != "gemini" || wire.SchemaVersion != 1 || wire.Target != 3 || wire.Scope != 1 || !opaqueID(wire.ProfileId) || wire.SelectionRevision == 0 || len(wire.ClientNonce) != 32 || len(wire.ProofSignature) != 0 && len(wire.ProofSignature) != 64 || wire.GeminiContractVersion != 1 || wire.Adapter == nil || wire.Adapter.ApiVersion != 1 || wire.Adapter.PluginId != "builtin.gemini" || wire.Adapter.PluginVersion != "1.1.0" || wire.Adapter.DefinitionDigest != nativeGeminiDigest || !slices.Equal(wire.Adapter.RequiredCapabilities, []string{"gemini.provider-plan.v1"}) {
		return nil, ErrInvalidNativeProof
	}
	encoded := geminiPrefix("prepare")
	encoded.text(origin)
	encoded.text(owner)
	encoded.blob(session)
	encoded.blob(operation)
	encoded.blob(keyHash)
	copyWire := proto.Clone(wire).(*nativev1.PrepareNativeApplyRequest)
	copyWire.ProofSignature = nil
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copyWire)
	if err != nil {
		return nil, ErrInvalidNativeProof
	}
	encoded.blob(body)
	return encoded, nil
}
func ValidGeminiPreparation(wire *nativev1.PreparationBinding) bool {
	if !geminiKnown(wire) || wire.Target != 3 || wire.Gemini == nil || wire.Gemini.ContractVersion != 1 || len(wire.Gemini.DeclarationSha256) != 32 || [32]byte(wire.Gemini.DeclarationSha256) == [32]byte{} || !opaqueID(wire.PrepareId) || wire.PreparedAtUnixMs <= 0 || wire.PrepareExpiresAtUnixMs <= wire.PreparedAtUnixMs || wire.PrepareExpiresAtUnixMs-wire.PreparedAtUnixMs > 300000 || wire.AssetKind != 1 || len(wire.RequiredSlots) > 1 || len(wire.RequiredSlots) == 1 && wire.RequiredSlots[0] != 4 {
		return false
	}
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: wire.OwnerId, AssetId: wire.AssetId, ExpectedAssetRevision: wire.AssetRevision, AgentId: wire.AgentId, SchemaVersion: wire.SchemaVersion, Adapter: wire.Adapter, Target: wire.Target, Scope: wire.Scope, ProfileId: wire.ProfileId, SelectionRevision: wire.SelectionRevision, OperationId: wire.OperationId, ClientNonce: make([]byte, 32), GeminiContractVersion: wire.Gemini.ContractVersion}
	_, err := GeminiPrepareWireCanonical(wire.ServerOrigin, wire.OwnerId, wire.NativeSessionBinding, wire.OperationId, wire.ConsumerKeySha256, request)
	return err == nil
}
func HashGeminiWirePreparation(wire *nativev1.PreparationBinding) ([32]byte, error) {
	if !ValidGeminiPreparation(wire) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(wire)
	if err != nil {
		return [32]byte{}, ErrInvalidNativeProof
	}
	return geminiHash("preparation", body), nil
}
func HashGeminiWireIntent(preparation *nativev1.PreparationBinding, local *nativev1.GeminiLocalBindingV1, revision uint64, expiry int64) ([32]byte, error) {
	prepared, err := HashGeminiWirePreparation(preparation)
	if err != nil || revision == 0 || expiry <= preparation.GetPreparedAtUnixMs() || expiry > preparation.GetPrepareExpiresAtUnixMs() {
		return [32]byte{}, ErrInvalidNativeProof
	}
	if !geminiClientLocalValid(local) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(local)
	if err != nil {
		return [32]byte{}, ErrInvalidNativeProof
	}
	encoded := geminiPrefix("intent")
	encoded.blob(prepared[:])
	encoded.blob(body)
	encoded.u64(revision)
	encoded.u64(uint64(expiry))
	return sha256.Sum256(encoded), nil
}
func GeminiAuthorizeWireCanonical(preparation *nativev1.PreparationBinding, challenge []byte, request *nativev1.AuthorizeNativeMaterializationRequest) ([]byte, error) {
	prepared, err := HashGeminiWirePreparation(preparation)
	if err != nil || !geminiKnown(request) || request.CodexLocalBinding != nil || request.PrepareId != preparation.PrepareId || !bytes.Equal(request.OperationId, preparation.OperationId) || !slices.Equal(request.RequestedSlots, preparation.RequiredSlots) || request.GetExplicitConfirmation() == nil || request.GetExplicitConfirmation().ConfirmationRevision == 0 || len(request.ClientNonce) != 32 || len(challenge) != 32 || len(request.ProofSignature) != 0 && len(request.ProofSignature) != 64 {
		return nil, ErrInvalidNativeProof
	}
	intent, err := HashGeminiWireIntent(preparation, request.GeminiLocalBinding, request.GetExplicitConfirmation().ConfirmationRevision, request.IntentExpiresAtUnixMs)
	if err != nil || !bytes.Equal(intent[:], request.IntentCommitment) {
		return nil, ErrInvalidNativeProof
	}
	copyWire := proto.Clone(request).(*nativev1.AuthorizeNativeMaterializationRequest)
	copyWire.ProofSignature = nil
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copyWire)
	if err != nil {
		return nil, ErrInvalidNativeProof
	}
	encoded := geminiPrefix("authorize")
	encoded.blob(prepared[:])
	encoded.blob(challenge)
	encoded.blob(body)
	return encoded, nil
}
func GeminiWireApplyDigest(wire *nativev1.ApplyBinding) ([32]byte, error) {
	if !geminiKnown(wire) || wire.CodexLocalBinding != nil || len(wire.IntentCommitment) != 32 {
		return [32]byte{}, ErrInvalidNativeProof
	}
	prepared, err := HashGeminiWirePreparation(wire.Preparation)
	if err != nil || wire.IntentExpiresAtUnixMs <= wire.Preparation.PreparedAtUnixMs || wire.IntentExpiresAtUnixMs > wire.Preparation.PrepareExpiresAtUnixMs {
		return [32]byte{}, ErrInvalidNativeProof
	}
	if !geminiClientLocalValid(wire.GeminiLocalBinding) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	encoded := geminiPrefix("apply")
	encoded.blob(prepared[:])
	encoded.blob(wire.IntentCommitment)
	encoded.u64(uint64(wire.IntentExpiresAtUnixMs))
	return sha256.Sum256(encoded), nil
}
func geminiClientLocalValid(wire *nativev1.GeminiLocalBindingV1) bool {
	if !geminiKnown(wire) || wire.BindingVersion != 1 || !geminiModel(wire.NativeProfileId) || len(wire.NativeProfileId) > 128 || len(wire.NativeProfileSha256) != 32 || len(wire.CodecProfileSha256) != 32 || wire.EffectiveScope != 1 || wire.LocalAuthPolicy != "preserve-local-login-v1" || len(wire.ObservationWitness) != 32 || [32]byte(wire.ObservationWitness) == [32]byte{} || len(wire.Roles) != 2 || len(wire.FieldDecisions) != 6 {
		return false
	}
	for index, role := range wire.Roles {
		if role == nil || role.Role != nativev1.GeminiRole(index+1) || role.Presence < 1 || role.Presence > 2 || len(role.BaselineHmac) != 32 || [32]byte(role.BaselineHmac) == [32]byte{} {
			return false
		}
	}
	for index, field := range wire.FieldDecisions {
		if field == nil || field.Field != nativev1.GeminiField(index+1) || field.Ownership < 1 || field.Ownership > 4 || field.Action < 1 || field.Action > 3 || field.Cleanup < 1 || field.Cleanup > 2 || field.Ownership == 3 || field.Ownership == 1 && (field.Action == 3 || field.Cleanup != 1) || field.Cleanup == 2 && field.Action != 3 || field.Action == 3 && field.Cleanup != 2 {
			return false
		}
	}
	return true
}
func HashGeminiDeclaration(wire *nativev1.GeminiDeclarationV1) ([32]byte, error) {
	declaration, err := decodeGeminiDeclaration(wire)
	if err != nil {
		return [32]byte{}, err
	}
	return hashGeminiDeclaration(declaration)
}
func HashGeminiIntent(preparation *nativev1.PreparationBinding, local *nativev1.GeminiLocalBindingV1, revision uint64, expiry int64) ([32]byte, error) {
	return HashGeminiWireIntent(preparation, local, revision, expiry)
}
func ValidateGeminiPreparedDeclaration(response *nativev1.PrepareNativeApplyResponse) error {
	if !geminiKnown(response) || !ValidGeminiPreparation(response.Binding) || len(response.AuthorizeChallenge) != 32 || response.MaterializationRequired != (len(response.Binding.RequiredSlots) == 1) {
		return ErrInvalidNativeProof
	}
	declaration, err := decodeGeminiDeclaration(response.GeminiDeclaration)
	if err != nil {
		return err
	}
	digest, err := hashGeminiDeclaration(declaration)
	if err != nil || !bytes.Equal(digest[:], response.Binding.Gemini.DeclarationSha256) || (declaration.AuthMode == agentconfig.GeminiAPIKeyAuth) != (len(response.Binding.RequiredSlots) == 1) {
		return ErrInvalidNativeProof
	}
	return nil
}
func GeminiPrepareTranscript(consumer ConsumerBinding, request *nativev1.PrepareNativeApplyRequest) (Transcript, error) {
	body, err := GeminiPrepareWireCanonical(consumer.ServerOrigin, consumer.OwnerID, consumer.NativeSessionBinding, consumer.OperationID, consumer.ConsumerKeySHA256, request)
	if err != nil {
		return Transcript{}, err
	}
	return finishTranscript(proofEncoder(body))
}
func GeminiAuthorizeTranscript(preparation *nativev1.PreparationBinding, challenge []byte, request *nativev1.AuthorizeNativeMaterializationRequest) (Transcript, error) {
	body, err := GeminiAuthorizeWireCanonical(preparation, challenge, request)
	if err != nil {
		return Transcript{}, err
	}
	return finishTranscript(proofEncoder(body))
}
func GeminiConsumeTranscript(binding *nativev1.ApplyBinding, authorize, consume []byte, expiry int64, request *nativev1.ConsumeNativeMaterializationRequest) (Transcript, error) {
	digest, err := GeminiWireApplyDigest(binding)
	if err != nil || !validConsumeRequest(request) || len(binding.Preparation.RequiredSlots) != 1 || len(authorize) != 32 || len(consume) != 32 || expiry <= binding.Preparation.PreparedAtUnixMs || expiry > binding.IntentExpiresAtUnixMs || request.PrepareId != binding.Preparation.PrepareId || !bytes.Equal(request.OperationId, binding.Preparation.OperationId) || !bytes.Equal(request.IntentCommitment, binding.IntentCommitment) {
		return Transcript{}, ErrInvalidNativeProof
	}
	token := sha256.Sum256(request.GrantToken)
	encoded := geminiPrefix("consume")
	encoded.blob(digest[:])
	encoded.blob(authorize)
	encoded.text(request.GrantId)
	encoded.blob(token[:])
	encoded.blob(consume)
	encoded.u64(uint64(expiry))
	encoded.blob(request.OperationId)
	encoded.blob(request.IntentCommitment)
	encoded.blob(request.ClientNonce)
	return finishTranscript(encoded)
}
func GeminiStatusTranscript(preparation *nativev1.PreparationBinding, challenge []byte, request *nativev1.GetNativeMaterializationStatusRequest) (Transcript, error) {
	digest, err := HashGeminiWirePreparation(preparation)
	if err != nil || !validStatusRequest(request) || len(challenge) != 32 || request.PrepareId != preparation.PrepareId || !bytes.Equal(request.OperationId, preparation.OperationId) {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := geminiPrefix("status")
	encoded.blob(digest[:])
	encoded.blob(challenge)
	encoded.text(request.PrepareId)
	encoded.text(request.GrantId)
	encoded.blob(request.OperationId)
	encoded.blob(request.ClientNonce)
	return finishTranscript(encoded)
}

func geminiClientProofValid(message proto.Message) (bool, bool) {
	switch value := message.(type) {
	case *nativev1.PrepareNativeApplyRequest:
		if value.Target != 3 && value.GeminiContractVersion == 0 {
			return false, false
		}
		_, err := GeminiPrepareWireCanonical("https://synthetic.invalid", value.ExpectedOwnerId, make([]byte, 32), value.OperationId, make([]byte, 32), value)
		return true, err == nil && len(value.ProofSignature) == 64
	case *nativev1.PreparationBinding:
		if value.Target != 3 && value.Gemini == nil {
			return false, false
		}
		return true, ValidGeminiPreparation(value)
	case *nativev1.ApplyBinding:
		if value.GetPreparation().GetTarget() != 3 && value.GeminiLocalBinding == nil {
			return false, false
		}
		_, err := GeminiWireApplyDigest(value)
		return true, err == nil
	case *nativev1.AuthorizeNativeMaterializationRequest:
		if value.GeminiLocalBinding == nil {
			return false, false
		}
		return true, knownMessage(value) && value.CodexLocalBinding == nil && geminiClientLocalValid(value.GeminiLocalBinding) && opaqueID(value.PrepareId) && len(value.OperationId) == 16 && len(value.IntentCommitment) == 32 && (len(value.RequestedSlots) == 0 || slices.Equal(value.RequestedSlots, []nativev1.SecretSlot{4})) && value.GetExplicitConfirmation() != nil && value.GetExplicitConfirmation().ConfirmationRevision > 0 && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64 && value.IntentExpiresAtUnixMs > 0
	case *nativev1.PrepareNativeApplyResponse:
		if value.GetBinding().GetTarget() != 3 && value.GeminiDeclaration == nil {
			return false, false
		}
		return true, ValidateGeminiPreparedDeclaration(value) == nil
	}
	return false, false
}

func geminiClientResponseValid(message proto.Message) (bool, bool) {
	if handled, valid := geminiClientProofValid(message); handled {
		return true, valid
	}
	switch value := message.(type) {
	case *nativev1.AuthorizeNativeMaterializationResponse:
		if value.GetBinding().GetPreparation().GetTarget() != 3 && value.GetBinding().GetGeminiLocalBinding() == nil {
			return false, false
		}
		_, err := GeminiWireApplyDigest(value.Binding)
		if err != nil || !knownMessage(value) || value.MaterializationRequired != (len(value.Binding.Preparation.RequiredSlots) == 1) {
			return true, false
		}
		if !value.MaterializationRequired {
			return true, value.GrantId == "" && len(value.GrantToken) == 0 && len(value.ConsumeChallenge) == 0 && value.ExpiresAtUnixMs == 0
		}
		return true, opaqueID(value.GrantId) && len(value.GrantToken) == 32 && len(value.ConsumeChallenge) == 32 && value.ExpiresAtUnixMs > value.Binding.Preparation.PreparedAtUnixMs && value.ExpiresAtUnixMs <= value.Binding.IntentExpiresAtUnixMs
	case *nativev1.ConsumeNativeMaterializationResponse:
		if value.GetBinding().GetPreparation().GetTarget() != 3 && value.GetBinding().GetGeminiLocalBinding() == nil {
			return false, false
		}
		_, err := GeminiWireApplyDigest(value.Binding)
		if err != nil || !knownMessage(value) || len(value.Binding.Preparation.RequiredSlots) != 1 || len(value.SlotValues) != 1 || value.SlotValues[0] == nil || value.SlotValues[0].Slot != 4 {
			return true, false
		}
		body := value.SlotValues[0].Plaintext
		return true, len(body) > 0 && len(body) <= 8192 && utf8.Valid(body) && !bytes.ContainsAny(body, "\x00\r\n") && opaqueID(value.GrantId) && value.ConsumedAtUnixMs >= value.Binding.Preparation.PreparedAtUnixMs && value.ConsumedAtUnixMs < value.Binding.IntentExpiresAtUnixMs
	}
	return false, false
}
