package nativesessionauth

import (
	"bytes"
	"crypto/ed25519"
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

func ExclusiveNativeVariant(message proto.Message) bool {
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

const GeminiDefinitionDigest = "b0ba50fab171fea7889625da4ed9d36dc9b811303a166b66fb23bb1b232bd095"

type GeminiPrepareRequest struct {
	PrepareRequest
	ContractVersion uint32
}
type GeminiPrepareFields struct {
	Consumer ConsumerFields
	Request  GeminiPrepareRequest
}
type geminiPreparationData struct {
	consumer ConsumerFields
	request  GeminiPrepareRequest
	wire     *nativev1.PreparationBinding
}
type geminiLocalData struct {
	declaration [32]byte
	wire        *nativev1.GeminiLocalBindingV1
}
type geminiApplyData struct {
	preparation GeminiPreparation
	local       GeminiLocalBinding
	revision    uint64
	commitment  [32]byte
	expiry      int64
}
type GeminiPreparation struct{ opaque[geminiPreparationData] }
type GeminiLocalBinding struct{ opaque[geminiLocalData] }
type GeminiApply struct{ opaque[geminiApplyData] }
type GeminiAuthorizeRequest struct {
	AuthorizeRequest
	Local GeminiLocalBinding
}
type GeminiAuthorizeFields struct {
	Preparation GeminiPreparation
	Local       GeminiLocalBinding
	Challenge   [32]byte
	Request     AuthorizeRequest
}
type GeminiConsumeFields struct {
	Apply                                GeminiApply
	AuthorizeChallenge, ConsumeChallenge [32]byte
	GrantExpiresMS                       int64
	Request                              ConsumeRequest
}
type GeminiStatusFields struct {
	Preparation GeminiPreparation
	Challenge   [32]byte
	Request     StatusRequest
}

func geminiKnown(message proto.Message) bool {
	budget := 1024
	return message != nil && message.ProtoReflect().IsValid() && ExclusiveNativeVariant(message) && codexKnown(message.ProtoReflect(), 0, &budget) && proto.Size(message) <= 16<<10
}
func geminiPrefix(purpose string) codexEncoder {
	var encoded codexEncoder
	encoded.text("cognate-native-gemini/1/" + purpose)
	return encoded
}
func geminiHash(purpose string, body []byte) [32]byte {
	encoded := geminiPrefix(purpose)
	encoded.bytes(body)
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
		if !codexLiteral(text, 2048) || strings.ContainsAny(text, "?#\\") || err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || !utf8.ValidString(endpoint.Path) || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) {
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
func HashGeminiDeclaration(value agentconfig.GeminiProviderDeclarationV1) ([32]byte, error) {
	if !validGeminiDeclaration(value) {
		return [32]byte{}, ErrInvalidEvidence
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > agentconfig.MaxGeminiDeclarationBytes {
		return [32]byte{}, ErrInvalidEvidence
	}
	return geminiHash("declaration", body), nil
}
func GeminiDeclarationWire(value agentconfig.GeminiProviderDeclarationV1) (*nativev1.GeminiDeclarationV1, error) {
	if _, err := HashGeminiDeclaration(value); err != nil {
		return nil, err
	}
	wire := &nativev1.GeminiDeclarationV1{Version: 1, Target: string(value.Target), AuthMode: 1, SelectedType: value.SelectedType, LocalAuthPolicy: string(value.LocalAuthPolicy)}
	if value.Endpoint != nil {
		wire.Endpoint = new(*value.Endpoint)
	}
	if value.EnvModel != nil {
		wire.EnvModel = new(*value.EnvModel)
	}
	if value.SettingsModel != nil {
		wire.SettingsModel = new(*value.SettingsModel)
	}
	if value.AuthMode == agentconfig.GeminiAPIKeyAuth {
		wire.AuthMode = 2
		wire.CredentialReference = new(string(*value.CredentialReference))
		wire.RequiredSlots = []nativev1.SecretSlot{4}
		transport := publicv1.GeminiKeyTransport(1)
		if *value.KeyTransport == agentconfig.GeminiBearer {
			transport = 2
		}
		wire.KeyTransport = new(transport)
	}
	return wire, nil
}
func GeminiDeclarationFromWire(wire *nativev1.GeminiDeclarationV1) (agentconfig.GeminiProviderDeclarationV1, error) {
	if !geminiKnown(wire) {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidEvidence
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
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidEvidence
	}
	if wire.KeyTransport != nil {
		switch *wire.KeyTransport {
		case 1:
			value.KeyTransport = new(agentconfig.GeminiNativeDefault)
		case 2:
			value.KeyTransport = new(agentconfig.GeminiBearer)
		default:
			return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidEvidence
		}
	}
	if wire.CredentialReference != nil {
		value.CredentialReference = new(agentconfig.GeminiSecretSlot(*wire.CredentialReference))
	}
	for _, slot := range wire.RequiredSlots {
		if slot != 4 {
			return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidEvidence
		}
		value.RequiredSlots = append(value.RequiredSlots, agentconfig.GeminiAPIKeySecret)
	}
	if _, err := HashGeminiDeclaration(value); err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, err
	}
	return value, nil
}
func GeminiPrepareWireCanonical(origin, owner string, session, operation, keyHash []byte, wire *nativev1.PrepareNativeApplyRequest) ([]byte, error) {
	if !geminiKnown(wire) || !validApplyOwner(owner) || !ValidServerOrigin(origin) || len(session) != 32 || len(operation) != 16 || len(keyHash) != 32 || wire.ExpectedOwnerId != owner || !bytes.Equal(wire.OperationId, operation) || !validApplyAsset(wire.AssetId) || wire.ExpectedAssetRevision <= 0 || wire.AgentId != "gemini" || wire.SchemaVersion != 1 || wire.Target != 3 || wire.Scope != 1 || !validID(wire.ProfileId) || wire.SelectionRevision == 0 || len(wire.ClientNonce) != 32 || len(wire.ProofSignature) != 0 && len(wire.ProofSignature) != 64 || wire.GeminiContractVersion != 1 || wire.Adapter == nil || wire.Adapter.ApiVersion != 1 || wire.Adapter.PluginId != "builtin.gemini" || wire.Adapter.PluginVersion != "1.1.0" || wire.Adapter.DefinitionDigest != GeminiDefinitionDigest || !slices.Equal(wire.Adapter.RequiredCapabilities, []string{"gemini.provider-plan.v1"}) {
		return nil, ErrInvalidEvidence
	}
	encoded := geminiPrefix("prepare")
	encoded.text(origin)
	encoded.text(owner)
	encoded.bytes(session)
	encoded.bytes(operation)
	encoded.bytes(keyHash)
	copyWire := proto.Clone(wire).(*nativev1.PrepareNativeApplyRequest)
	copyWire.ProofSignature = nil
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copyWire)
	if err != nil {
		return nil, ErrInvalidEvidence
	}
	encoded.bytes(body)
	return encoded, nil
}
func geminiPrepareWire(request GeminiPrepareRequest) *nativev1.PrepareNativeApplyRequest {
	return &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: request.AgentID, SchemaVersion: request.SchemaVersion, Adapter: &nativev1.AdapterBinding{ApiVersion: request.Adapter.APIVersion, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: nativev1.Target(request.Target), Scope: nativev1.Scope(request.Scope), ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, OperationId: slices.Clone(request.OperationID[:]), ClientNonce: slices.Clone(request.Nonce[:]), GeminiContractVersion: request.ContractVersion}
}
func geminiPrepareCanonical(fields GeminiPrepareFields) ([]byte, error) {
	consumer := fields.Consumer
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	return GeminiPrepareWireCanonical(consumer.ServerOrigin, consumer.OwnerID, consumer.SessionBinding[:], consumer.OperationID[:], keyHash[:], geminiPrepareWire(fields.Request))
}
func NewGeminiPreparation(consumer ConsumerFields, request GeminiPrepareRequest, id string, preparedMS, expiresMS int64, digest [32]byte, slot uint32) (GeminiPreparation, error) {
	if _, err := geminiPrepareCanonical(GeminiPrepareFields{Consumer: consumer, Request: request}); err != nil || !validID(id) || preparedMS <= 0 || expiresMS <= preparedMS || expiresMS-preparedMS > 300000 || digest == [32]byte{} || slot != 0 && slot != 4 {
		return GeminiPreparation{}, ErrInvalidEvidence
	}
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	wireRequest := geminiPrepareWire(request)
	wire := &nativev1.PreparationBinding{OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: slices.Clone(consumer.SessionBinding[:]), OperationId: slices.Clone(consumer.OperationID[:]), AssetId: request.AssetID, AssetRevision: request.AssetRevision, AgentId: request.AgentID, AssetKind: 1, SchemaVersion: request.SchemaVersion, Adapter: wireRequest.Adapter, Target: 3, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, PrepareExpiresAtUnixMs: expiresMS, ConsumerKeySha256: slices.Clone(keyHash[:]), PrepareId: id, PreparedAtUnixMs: preparedMS, Gemini: &nativev1.GeminiPreparationV1{ContractVersion: 1, DeclarationSha256: slices.Clone(digest[:])}}
	if slot == 4 {
		wire.RequiredSlots = []nativev1.SecretSlot{4}
	}
	return GeminiPreparation{opaque: wrap(geminiPreparationData{consumer: consumer, request: request, wire: wire})}, nil
}
func ValidGeminiPreparation(wire *nativev1.PreparationBinding) bool {
	if !geminiKnown(wire) || wire.Target != 3 || wire.Gemini == nil || wire.Gemini.ContractVersion != 1 || len(wire.Gemini.DeclarationSha256) != 32 || [32]byte(wire.Gemini.DeclarationSha256) == [32]byte{} || !validID(wire.PrepareId) || wire.PreparedAtUnixMs <= 0 || wire.PrepareExpiresAtUnixMs <= wire.PreparedAtUnixMs || wire.PrepareExpiresAtUnixMs-wire.PreparedAtUnixMs > 300000 || wire.AssetKind != 1 || len(wire.RequiredSlots) > 1 || len(wire.RequiredSlots) == 1 && wire.RequiredSlots[0] != 4 {
		return false
	}
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: wire.OwnerId, AssetId: wire.AssetId, ExpectedAssetRevision: wire.AssetRevision, AgentId: wire.AgentId, SchemaVersion: wire.SchemaVersion, Adapter: wire.Adapter, Target: wire.Target, Scope: wire.Scope, ProfileId: wire.ProfileId, SelectionRevision: wire.SelectionRevision, OperationId: wire.OperationId, ClientNonce: make([]byte, 32), GeminiContractVersion: wire.Gemini.ContractVersion}
	_, err := GeminiPrepareWireCanonical(wire.ServerOrigin, wire.OwnerId, wire.NativeSessionBinding, wire.OperationId, wire.ConsumerKeySha256, request)
	return err == nil
}
func HashGeminiWirePreparation(wire *nativev1.PreparationBinding) ([32]byte, error) {
	if !ValidGeminiPreparation(wire) {
		return [32]byte{}, ErrInvalidEvidence
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(wire)
	if err != nil {
		return [32]byte{}, ErrInvalidEvidence
	}
	return geminiHash("preparation", body), nil
}
func (value GeminiPreparation) CopyWire() *nativev1.PreparationBinding {
	if !value.present() {
		return nil
	}
	return proto.Clone(value.payload.value.wire).(*nativev1.PreparationBinding)
}
func (value GeminiPreparation) CopyRequest() GeminiPrepareRequest {
	if !value.present() {
		return GeminiPrepareRequest{}
	}
	return value.payload.value.request
}
func (value GeminiPreparation) CopyConsumer() ConsumerFields {
	if !value.present() {
		return ConsumerFields{}
	}
	return value.payload.value.consumer
}
func HashGeminiPreparation(value GeminiPreparation) ([32]byte, error) {
	return HashGeminiWirePreparation(value.CopyWire())
}
func NewGeminiLocalBinding(digest [32]byte, wire *nativev1.GeminiLocalBindingV1) (GeminiLocalBinding, error) {
	if !geminiKnown(wire) || wire.BindingVersion != 1 || !geminiModel(wire.NativeProfileId) || len(wire.NativeProfileId) > 128 || len(wire.NativeProfileSha256) != 32 || len(wire.CodecProfileSha256) != 32 || wire.EffectiveScope != 1 || wire.LocalAuthPolicy != "preserve-local-login-v1" || len(wire.ObservationWitness) != 32 || [32]byte(wire.ObservationWitness) == [32]byte{} || len(wire.Roles) != 2 || len(wire.FieldDecisions) != 6 {
		return GeminiLocalBinding{}, ErrInvalidEvidence
	}
	for index, role := range wire.Roles {
		if role == nil || role.Role != nativev1.GeminiRole(index+1) || role.Presence < 1 || role.Presence > 2 || len(role.BaselineHmac) != 32 || [32]byte(role.BaselineHmac) == [32]byte{} {
			return GeminiLocalBinding{}, ErrInvalidEvidence
		}
	}
	for index, field := range wire.FieldDecisions {
		if field == nil || field.Field != nativev1.GeminiField(index+1) || field.Ownership < 1 || field.Ownership > 4 || field.Action < 1 || field.Action > 3 || field.Cleanup < 1 || field.Cleanup > 2 || field.Ownership == 3 || field.Ownership == 1 && (field.Action == 3 || field.Cleanup != 1) || field.Cleanup == 2 && field.Action != 3 || field.Action == 3 && field.Cleanup != 2 {
			return GeminiLocalBinding{}, ErrInvalidEvidence
		}
	}
	return GeminiLocalBinding{opaque: wrap(geminiLocalData{declaration: digest, wire: proto.Clone(wire).(*nativev1.GeminiLocalBindingV1)})}, nil
}
func (value GeminiLocalBinding) CopyWire() *nativev1.GeminiLocalBindingV1 {
	if !value.present() {
		return nil
	}
	return proto.Clone(value.payload.value.wire).(*nativev1.GeminiLocalBindingV1)
}
func (value GeminiLocalBinding) DeclarationSHA256() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.payload.value.declaration
}
func HashGeminiWireIntent(preparation *nativev1.PreparationBinding, local *nativev1.GeminiLocalBindingV1, revision uint64, expiry int64) ([32]byte, error) {
	prepared, err := HashGeminiWirePreparation(preparation)
	if err != nil || revision == 0 || expiry <= preparation.GetPreparedAtUnixMs() || expiry > preparation.GetPrepareExpiresAtUnixMs() {
		return [32]byte{}, ErrInvalidEvidence
	}
	if _, err := NewGeminiLocalBinding([32]byte(preparation.Gemini.DeclarationSha256), local); err != nil {
		return [32]byte{}, err
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(local)
	if err != nil {
		return [32]byte{}, ErrInvalidEvidence
	}
	encoded := geminiPrefix("intent")
	encoded.bytes(prepared[:])
	encoded.bytes(body)
	encoded.wide(revision)
	encoded.wide(uint64(expiry))
	return sha256.Sum256(encoded), nil
}
func NewGeminiApply(preparation GeminiPreparation, local GeminiLocalBinding, revision uint64, expiry int64) (GeminiApply, error) {
	if !local.present() || !ValidGeminiPreparation(preparation.CopyWire()) || local.DeclarationSHA256() != [32]byte(preparation.CopyWire().GetGemini().GetDeclarationSha256()) {
		return GeminiApply{}, ErrInvalidEvidence
	}
	commitment, err := HashGeminiWireIntent(preparation.CopyWire(), local.CopyWire(), revision, expiry)
	if err != nil {
		return GeminiApply{}, err
	}
	return GeminiApply{opaque: wrap(geminiApplyData{preparation: preparation, local: local, revision: revision, commitment: commitment, expiry: expiry})}, nil
}
func (value GeminiApply) CopyWire() *nativev1.ApplyBinding {
	if !value.present() {
		return nil
	}
	data := value.payload.value
	return &nativev1.ApplyBinding{Preparation: data.preparation.CopyWire(), IntentCommitment: slices.Clone(data.commitment[:]), IntentExpiresAtUnixMs: data.expiry, GeminiLocalBinding: data.local.CopyWire()}
}
func (value GeminiApply) Preparation() GeminiPreparation {
	if !value.present() {
		return GeminiPreparation{}
	}
	return value.payload.value.preparation
}
func (value GeminiApply) LocalBinding() GeminiLocalBinding {
	if !value.present() {
		return GeminiLocalBinding{}
	}
	return value.payload.value.local
}
func (value GeminiApply) Commitment() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.payload.value.commitment
}
func (value GeminiApply) ConfirmationRevision() uint64 {
	if !value.present() {
		return 0
	}
	return value.payload.value.revision
}
func HashGeminiApply(preparation GeminiPreparation, commitment [32]byte, expiry int64) ([32]byte, error) {
	prepared, err := HashGeminiPreparation(preparation)
	wire := preparation.CopyWire()
	if err != nil || expiry <= wire.GetPreparedAtUnixMs() || expiry > wire.GetPrepareExpiresAtUnixMs() {
		return [32]byte{}, ErrInvalidEvidence
	}
	encoded := geminiPrefix("apply")
	encoded.bytes(prepared[:])
	encoded.bytes(commitment[:])
	encoded.wide(uint64(expiry))
	return sha256.Sum256(encoded), nil
}
func GeminiAuthorizeWireCanonical(preparation *nativev1.PreparationBinding, challenge []byte, request *nativev1.AuthorizeNativeMaterializationRequest) ([]byte, error) {
	prepared, err := HashGeminiWirePreparation(preparation)
	if err != nil || !geminiKnown(request) || request.CodexLocalBinding != nil || request.PrepareId != preparation.PrepareId || !bytes.Equal(request.OperationId, preparation.OperationId) || !slices.Equal(request.RequestedSlots, preparation.RequiredSlots) || request.GetExplicitConfirmation() == nil || request.GetExplicitConfirmation().ConfirmationRevision == 0 || len(request.ClientNonce) != 32 || len(challenge) != 32 || len(request.ProofSignature) != 0 && len(request.ProofSignature) != 64 {
		return nil, ErrInvalidEvidence
	}
	intent, err := HashGeminiWireIntent(preparation, request.GeminiLocalBinding, request.GetExplicitConfirmation().ConfirmationRevision, request.IntentExpiresAtUnixMs)
	if err != nil || !bytes.Equal(intent[:], request.IntentCommitment) {
		return nil, ErrInvalidEvidence
	}
	copyWire := proto.Clone(request).(*nativev1.AuthorizeNativeMaterializationRequest)
	copyWire.ProofSignature = nil
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copyWire)
	if err != nil {
		return nil, ErrInvalidEvidence
	}
	encoded := geminiPrefix("authorize")
	encoded.bytes(prepared[:])
	encoded.bytes(challenge)
	encoded.bytes(body)
	return encoded, nil
}
func geminiAuthorizeCanonical(fields GeminiAuthorizeFields) ([]byte, error) {
	request := fields.Request
	wire := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: request.PrepareID, OperationId: slices.Clone(request.OperationID[:]), IntentCommitment: slices.Clone(request.Commitment[:]), ClientNonce: slices.Clone(request.Nonce[:]), IntentExpiresAtUnixMs: request.IntentExpiresMS, GeminiLocalBinding: fields.Local.CopyWire()}
	if request.RequiredSlot != 0 {
		wire.RequestedSlots = []nativev1.SecretSlot{nativev1.SecretSlot(request.RequiredSlot)}
	}
	if request.Consent.Kind != 1 || request.Consent.PolicyID != "" {
		return nil, ErrInvalidEvidence
	}
	wire.Authorization = &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: request.Consent.Revision}}
	return GeminiAuthorizeWireCanonical(fields.Preparation.CopyWire(), fields.Challenge[:], wire)
}
func GeminiWireApplyDigest(wire *nativev1.ApplyBinding) ([32]byte, error) {
	if !geminiKnown(wire) || wire.CodexLocalBinding != nil || len(wire.IntentCommitment) != 32 {
		return [32]byte{}, ErrInvalidEvidence
	}
	prepared, err := HashGeminiWirePreparation(wire.Preparation)
	if err != nil || wire.IntentExpiresAtUnixMs <= wire.Preparation.PreparedAtUnixMs || wire.IntentExpiresAtUnixMs > wire.Preparation.PrepareExpiresAtUnixMs {
		return [32]byte{}, ErrInvalidEvidence
	}
	if _, err := NewGeminiLocalBinding([32]byte(wire.Preparation.Gemini.DeclarationSha256), wire.GeminiLocalBinding); err != nil {
		return [32]byte{}, err
	}
	encoded := geminiPrefix("apply")
	encoded.bytes(prepared[:])
	encoded.bytes(wire.IntentCommitment)
	encoded.wide(uint64(wire.IntentExpiresAtUnixMs))
	return sha256.Sum256(encoded), nil
}
func GeminiConsumeCanonicalFromDigest(apply [32]byte, preparation *nativev1.PreparationBinding, commitment []byte, expiry int64, authorize, consume []byte, grantExpiry int64, request ConsumeRequest) ([]byte, error) {
	token, present := request.Token.TokenDigest()
	if !ValidGeminiPreparation(preparation) || len(preparation.RequiredSlots) != 1 || !present || request.PrepareID != preparation.PrepareId || !validID(request.GrantID) || !bytes.Equal(request.OperationID[:], preparation.OperationId) || !bytes.Equal(request.Commitment[:], commitment) || len(authorize) != 32 || len(consume) != 32 || grantExpiry <= preparation.PreparedAtUnixMs || grantExpiry > expiry {
		return nil, ErrInvalidEvidence
	}
	encoded := geminiPrefix("consume")
	encoded.bytes(apply[:])
	encoded.bytes(authorize)
	encoded.text(request.GrantID)
	encoded.bytes(token[:])
	encoded.bytes(consume)
	encoded.wide(uint64(grantExpiry))
	encoded.bytes(request.OperationID[:])
	encoded.bytes(request.Commitment[:])
	encoded.bytes(request.Nonce[:])
	return encoded, nil
}
func geminiConsumeCanonical(fields GeminiConsumeFields) ([]byte, error) {
	wire := fields.Apply.CopyWire()
	digest, err := GeminiWireApplyDigest(wire)
	if err != nil {
		return nil, err
	}
	return GeminiConsumeCanonicalFromDigest(digest, wire.Preparation, wire.IntentCommitment, wire.IntentExpiresAtUnixMs, fields.AuthorizeChallenge[:], fields.ConsumeChallenge[:], fields.GrantExpiresMS, fields.Request)
}
func GeminiStatusWireCanonical(preparation *nativev1.PreparationBinding, challenge []byte, request StatusRequest) ([]byte, error) {
	digest, err := HashGeminiWirePreparation(preparation)
	if err != nil || len(challenge) != 32 || request.PrepareID != preparation.PrepareId || !bytes.Equal(request.OperationID[:], preparation.OperationId) || request.GrantID != "" && !validID(request.GrantID) {
		return nil, ErrInvalidEvidence
	}
	encoded := geminiPrefix("status")
	encoded.bytes(digest[:])
	encoded.bytes(challenge)
	encoded.text(request.PrepareID)
	encoded.text(request.GrantID)
	encoded.bytes(request.OperationID[:])
	encoded.bytes(request.Nonce[:])
	return encoded, nil
}
func geminiStatusCanonical(fields GeminiStatusFields) ([]byte, error) {
	return GeminiStatusWireCanonical(fields.Preparation.CopyWire(), fields.Challenge[:], fields.Request)
}
func GeminiPrepareFieldsDigest(fields GeminiPrepareFields) ([32]byte, error) {
	body, err := geminiPrepareCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return geminiHash("stage-evidence", body), nil
}
func GeminiAuthorizeFieldsDigest(fields GeminiAuthorizeFields) ([32]byte, error) {
	body, err := geminiAuthorizeCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return geminiHash("stage-evidence", body), nil
}
func GeminiConsumeFieldsDigest(fields GeminiConsumeFields) ([32]byte, error) {
	body, err := geminiConsumeCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return geminiHash("stage-evidence", body), nil
}
func GeminiStatusFieldsDigest(fields GeminiStatusFields) ([32]byte, error) {
	body, err := geminiStatusCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return geminiHash("stage-evidence", body), nil
}
func (verifier *Verifier) verifyGemini(bearer ParsedNativeBearer, purpose string, key [32]byte, body, signature []byte) (VerifiedApplyProof, error) {
	digest, present := bearer.TokenDigest()
	if verifier == nil || !verifier.present() || !present || len(body) == 0 || len(body) > 16<<10 || len(signature) != 64 || !ed25519.Verify(key[:], body, signature) {
		return VerifiedApplyProof{}, ErrInvalidEvidence
	}
	return VerifiedApplyProof{opaque: wrap(applyEvidence{native: digest, canonical: geminiHash("stage-evidence", body), keyID: verifier.payload.value.keyID, purpose: purpose})}, nil
}
func (verifier *Verifier) VerifyGeminiPrepare(bearer ParsedNativeBearer, fields GeminiPrepareFields, signature []byte) (VerifiedApplyProof, error) {
	body, err := geminiPrepareCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyGemini(bearer, "prepare", fields.Consumer.PublicKey, body, signature)
}
func (verifier *Verifier) VerifyGeminiAuthorize(bearer ParsedNativeBearer, fields GeminiAuthorizeFields, signature []byte) (VerifiedApplyProof, error) {
	body, err := geminiAuthorizeCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyGemini(bearer, "authorize", fields.Preparation.CopyConsumer().PublicKey, body, signature)
}
func (verifier *Verifier) VerifyGeminiConsume(bearer ParsedNativeBearer, fields GeminiConsumeFields, signature []byte) (VerifiedApplyProof, error) {
	body, err := geminiConsumeCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyGemini(bearer, "consume", fields.Apply.Preparation().CopyConsumer().PublicKey, body, signature)
}
func (verifier *Verifier) VerifyGeminiStatus(bearer ParsedNativeBearer, fields GeminiStatusFields, signature []byte) (VerifiedApplyProof, error) {
	body, err := geminiStatusCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyGemini(bearer, "status", fields.Preparation.CopyConsumer().PublicKey, body, signature)
}
