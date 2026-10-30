package configclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	configurationnativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var ErrInvalidNativeProof = errors.New("invalid native proof")
var ErrNativeProofSignature = errors.New("invalid native proof signature")

const maxNativeProofBytes = 512 << 10

type proofPayload struct{ data *[]byte }
type opaqueProof struct{ payload *proofPayload }
type Transcript struct{ opaqueProof }
type Signature struct{ opaqueProof }

func (value opaqueProof) CopyBytes() []byte { return bytes.Clone(value.raw()) }
func (opaqueProof) String() string          { return "[redacted native proof]" }
func (opaqueProof) GoString() string        { return "[redacted native proof]" }
func (opaqueProof) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted native proof]")
}
func (opaqueProof) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted native proof]"`), nil
}
func (opaqueProof) MarshalText() ([]byte, error) {
	return []byte("[redacted native proof]"), nil
}
func (opaqueProof) LogValue() slog.Value { return slog.StringValue("[redacted native proof]") }
func (value opaqueProof) raw() []byte {
	if value.payload == nil || value.payload.data == nil {
		return nil
	}
	return *value.payload.data
}

type ConsumerBinding struct {
	ServerOrigin         string
	OwnerID              string
	NativeSessionBinding []byte
	OperationID          []byte
	ConsumerKeySHA256    []byte
}

type proofEncoder []byte

func (encoded *proofEncoder) u32(value uint32) {
	*encoded = binary.BigEndian.AppendUint32(*encoded, value)
}
func (encoded *proofEncoder) u64(value uint64) {
	*encoded = binary.BigEndian.AppendUint64(*encoded, value)
}
func (encoded *proofEncoder) blob(value []byte) {
	encoded.u32(uint32(len(value)))
	*encoded = append(*encoded, value...)
}
func (encoded *proofEncoder) text(value string) { encoded.blob([]byte(value)) }
func newTranscript(purpose string) proofEncoder {
	var encoded proofEncoder
	encoded.text("cognate/native-config-proof/v1")
	encoded.text(purpose)
	encoded.u32(1)
	return encoded
}
func finishTranscript(encoded proofEncoder) (Transcript, error) {
	if len(encoded) == 0 || len(encoded) > maxNativeProofBytes {
		return Transcript{}, ErrInvalidNativeProof
	}
	return Transcript{opaqueProof{&proofPayload{new([]byte(encoded))}}}, nil
}

func BindTranscript(begin *configurationnativev1.BeginNativeConsumerRequest, challenge *configurationnativev1.BeginNativeConsumerResponse) (Transcript, error) {
	if !validBegin(begin) || !validChallenge(challenge) || begin.ExpectedOwnerId != challenge.OwnerId || begin.ExpectedServerOrigin != challenge.ServerOrigin {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := newTranscript("bind")
	encoded.text(challenge.ServerOrigin)
	encoded.text(challenge.OwnerId)
	encoded.u32(uint32(begin.Purpose))
	encoded.blob(begin.OperationId)
	encoded.blob(begin.ConsumerPublicKey)
	encoded.blob(begin.ClientNonce)
	encoded.text(challenge.ChallengeId)
	encoded.blob(challenge.Challenge)
	encoded.blob(challenge.NativeSessionBinding)
	encoded.u64(uint64(challenge.ChallengeExpiresAtUnixMs))
	encoded.u64(uint64(challenge.NativeSessionExpiresAtUnixMs))
	return finishTranscript(encoded)
}

func PrepareTranscript(binding ConsumerBinding, request *configurationnativev1.PrepareNativeApplyRequest) (Transcript, error) {
	if !validOwner(binding.OwnerID) || !validOrigin(binding.ServerOrigin) || len(binding.NativeSessionBinding) != 32 || len(binding.OperationID) != 16 || len(binding.ConsumerKeySHA256) != 32 || !validPrepareRequest(request) || request.ExpectedOwnerId != binding.OwnerID || !bytes.Equal(request.OperationId, binding.OperationID) {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := newTranscript("prepare")
	encoded.text(binding.ServerOrigin)
	encoded.text(binding.OwnerID)
	encoded.blob(binding.NativeSessionBinding)
	encoded.blob(binding.OperationID)
	encoded.blob(binding.ConsumerKeySHA256)
	encoded.text(request.ExpectedOwnerId)
	encoded.text(request.AssetId)
	encoded.u64(uint64(request.ExpectedAssetRevision))
	encoded.text(request.AgentId)
	encoded.u32(request.SchemaVersion)
	encodeAdapter(&encoded, request.Adapter)
	encoded.u32(uint32(request.Target))
	encoded.u32(uint32(request.Scope))
	encoded.text(request.ProfileId)
	encoded.u64(request.SelectionRevision)
	encoded.blob(request.OperationId)
	encoded.blob(request.ClientNonce)
	return finishTranscript(encoded)
}

func AuthorizeTranscript(preparation *configurationnativev1.PreparationBinding, challenge []byte, request *configurationnativev1.AuthorizeNativeMaterializationRequest) (Transcript, error) {
	hash, err := HashPreparation(preparation)
	if err != nil || len(challenge) != 32 || !validAuthorizeRequest(request) || request.PrepareId != preparation.PrepareId || !bytes.Equal(request.OperationId, preparation.OperationId) || !slices.Equal(request.RequestedSlots, preparation.RequiredSlots) || request.IntentExpiresAtUnixMs <= preparation.PreparedAtUnixMs || request.IntentExpiresAtUnixMs > preparation.PrepareExpiresAtUnixMs {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := newTranscript("authorize")
	encoded.blob(hash[:])
	encoded.blob(challenge)
	encoded.text(request.PrepareId)
	encoded.blob(request.OperationId)
	encoded.blob(request.IntentCommitment)
	encodeSlots(&encoded, request.RequestedSlots)
	switch authorization := request.Authorization.(type) {
	case *configurationnativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation:
		encoded.u32(1)
		encoded.u64(authorization.ExplicitConfirmation.ConfirmationRevision)
	case *configurationnativev1.AuthorizeNativeMaterializationRequest_AutoPolicy:
		encoded.u32(2)
		encoded.blob(authorization.AutoPolicy.PolicyId)
		encoded.u64(authorization.AutoPolicy.PolicyRevision)
	}
	encoded.blob(request.ClientNonce)
	encoded.u64(uint64(request.IntentExpiresAtUnixMs))
	return finishTranscript(encoded)
}

func ConsumeTranscript(binding *configurationnativev1.ApplyBinding, authorizeChallenge, consumeChallenge []byte, grantExpiry int64, request *configurationnativev1.ConsumeNativeMaterializationRequest) (Transcript, error) {
	hash, err := HashApply(binding)
	if err != nil || len(authorizeChallenge) != 32 || len(consumeChallenge) != 32 || !validConsumeRequest(request) || len(binding.Preparation.RequiredSlots) == 0 || grantExpiry <= binding.Preparation.PreparedAtUnixMs || grantExpiry > binding.IntentExpiresAtUnixMs || request.PrepareId != binding.Preparation.PrepareId || !bytes.Equal(request.OperationId, binding.Preparation.OperationId) || !bytes.Equal(request.IntentCommitment, binding.IntentCommitment) {
		return Transcript{}, ErrInvalidNativeProof
	}
	tokenHash := sha256.Sum256(request.GrantToken)
	encoded := newTranscript("consume")
	encoded.blob(hash[:])
	encoded.blob(authorizeChallenge)
	encoded.text(request.GrantId)
	encoded.blob(tokenHash[:])
	encoded.blob(consumeChallenge)
	encoded.u64(uint64(grantExpiry))
	encoded.blob(request.OperationId)
	encoded.blob(request.IntentCommitment)
	encoded.blob(request.ClientNonce)
	return finishTranscript(encoded)
}

func StatusTranscript(preparation *configurationnativev1.PreparationBinding, challenge []byte, request *configurationnativev1.GetNativeMaterializationStatusRequest) (Transcript, error) {
	hash, err := HashPreparation(preparation)
	if err != nil || len(challenge) != 32 || !validStatusRequest(request) || request.PrepareId != preparation.PrepareId || !bytes.Equal(request.OperationId, preparation.OperationId) {
		return Transcript{}, ErrInvalidNativeProof
	}
	encoded := newTranscript("status")
	encoded.blob(hash[:])
	encoded.blob(challenge)
	encoded.text(request.PrepareId)
	encoded.text(request.GrantId)
	encoded.blob(request.OperationId)
	encoded.blob(request.ClientNonce)
	return finishTranscript(encoded)
}

func HashPreparation(binding *configurationnativev1.PreparationBinding) ([32]byte, error) {
	if !validPreparation(binding) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	var encoded proofEncoder
	encoded.text("cognate/native-config-preparation/v1")
	encodePreparation(&encoded, binding)
	return sha256.Sum256(encoded), nil
}

func HashApply(binding *configurationnativev1.ApplyBinding) ([32]byte, error) {
	if !validApply(binding) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	var preparation, encoded proofEncoder
	encodePreparation(&preparation, binding.Preparation)
	encoded.text("cognate/native-config-apply/v1")
	encoded.blob(preparation)
	encoded.blob(binding.IntentCommitment)
	encoded.u64(uint64(binding.IntentExpiresAtUnixMs))
	return sha256.Sum256(encoded), nil
}

func HashPatch(target configurationnativev1.Target, patch *configurationnativev1.ClaudeProviderPatch) ([32]byte, error) {
	if target != configurationnativev1.Target_TARGET_CLAUDE_SETTINGS || !validPatch(patch) {
		return [32]byte{}, ErrInvalidNativeProof
	}
	var encoded proofEncoder
	encoded.text("cognate/native-config-patch/v1")
	encoded.u32(uint32(target))
	encoded.u32(uint32(len(patch.Operations)))
	for _, operation := range patch.Operations {
		encoded.u32(uint32(operation.Slot))
		switch action := operation.Action.(type) {
		case *configurationnativev1.PatchOperation_SetLiteral:
			encoded.u32(1)
			encoded.text(action.SetLiteral.Value)
		case *configurationnativev1.PatchOperation_SetSelectedSecret:
			encoded.u32(2)
			encoded.u32(uint32(action.SetSelectedSecret.Slot))
		case *configurationnativev1.PatchOperation_Remove:
			encoded.u32(3)
		}
	}
	return sha256.Sum256(encoded), nil
}

func ValidatePreparedPatch(response *configurationnativev1.PrepareNativeApplyResponse) error {
	if !knownMessage(response) || response.CodexDeclaration != nil || !validPreparation(response.Binding) || len(response.AuthorizeChallenge) != 32 {
		return ErrInvalidNativeProof
	}
	hash, err := HashPatch(response.Binding.Target, response.Patch)
	if err != nil || !bytes.Equal(hash[:], response.Binding.PatchSha256) {
		return ErrInvalidNativeProof
	}
	var slots []configurationnativev1.SecretSlot
	for _, operation := range response.Patch.Operations {
		if selected := operation.GetSetSelectedSecret(); selected != nil {
			slots = append(slots, selected.Slot)
		}
	}
	if !slices.Equal(slots, response.Binding.RequiredSlots) || response.MaterializationRequired != (len(slots) > 0) {
		return ErrInvalidNativeProof
	}
	return nil
}

func SignNativeProof(key ed25519.PrivateKey, transcript Transcript) (Signature, error) {
	if len(key) != ed25519.PrivateKeySize || len(transcript.raw()) == 0 {
		return Signature{}, ErrInvalidNativeProof
	}
	derived := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	defer clear(derived)
	if subtle.ConstantTimeCompare(derived, key) != 1 {
		return Signature{}, ErrInvalidNativeProof
	}
	signature := ed25519.Sign(key, transcript.raw())
	return Signature{opaqueProof{&proofPayload{new(signature)}}}, nil
}

func VerifyNativeProof(key ed25519.PublicKey, transcript Transcript, signature []byte) error {
	if len(key) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || len(transcript.raw()) == 0 {
		return ErrInvalidNativeProof
	}
	if !ed25519.Verify(key, transcript.raw(), signature) {
		return ErrNativeProofSignature
	}
	return nil
}

func encodeAdapter(encoded *proofEncoder, adapter *configurationnativev1.AdapterBinding) {
	encoded.u32(adapter.ApiVersion)
	encoded.text(adapter.PluginId)
	encoded.text(adapter.PluginVersion)
	encoded.text(adapter.DefinitionDigest)
	encoded.u32(uint32(len(adapter.RequiredCapabilities)))
	for _, capability := range adapter.RequiredCapabilities {
		encoded.text(capability)
	}
}

func encodeSlots(encoded *proofEncoder, slots []configurationnativev1.SecretSlot) {
	encoded.u32(uint32(len(slots)))
	for _, slot := range slots {
		encoded.u32(uint32(slot))
	}
}

func encodePreparation(encoded *proofEncoder, binding *configurationnativev1.PreparationBinding) {
	encoded.text(binding.OwnerId)
	encoded.text(binding.ServerOrigin)
	encoded.blob(binding.NativeSessionBinding)
	encoded.blob(binding.OperationId)
	encoded.text(binding.AssetId)
	encoded.u64(uint64(binding.AssetRevision))
	encoded.text(binding.AgentId)
	encoded.u32(uint32(binding.AssetKind))
	encoded.u32(binding.SchemaVersion)
	encodeAdapter(encoded, binding.Adapter)
	encoded.u32(uint32(binding.Target))
	encoded.u32(uint32(binding.Scope))
	encoded.text(binding.ProfileId)
	encoded.u64(binding.SelectionRevision)
	encodeSlots(encoded, binding.RequiredSlots)
	encoded.u64(uint64(binding.PrepareExpiresAtUnixMs))
	encoded.blob(binding.ConsumerKeySha256)
	encoded.text(binding.PrepareId)
	encoded.u64(uint64(binding.PreparedAtUnixMs))
	encoded.blob(binding.PatchSha256)
}

func validBegin(request *configurationnativev1.BeginNativeConsumerRequest) bool {
	return knownMessage(request) && request.Purpose == configurationnativev1.NativePurpose_NATIVE_PURPOSE_CONFIGURATION_APPLY && len(request.OperationId) == 16 && len(request.ConsumerPublicKey) == 32 && len(request.ClientNonce) == 32 && validOwner(request.ExpectedOwnerId) && validOrigin(request.ExpectedServerOrigin)
}

func validChallenge(response *configurationnativev1.BeginNativeConsumerResponse) bool {
	return knownMessage(response) && opaqueID(response.ChallengeId) && len(response.Challenge) == 32 && len(response.NativeSessionBinding) == 32 && validOwner(response.OwnerId) && validOrigin(response.ServerOrigin) && response.ChallengeExpiresAtUnixMs > 0 && response.NativeSessionExpiresAtUnixMs >= response.ChallengeExpiresAtUnixMs
}

func validPrepareRequest(request *configurationnativev1.PrepareNativeApplyRequest) bool {
	if request != nil && request.CodexContractVersion != 0 {
		return false
	}
	return knownMessage(request) && validOwner(request.ExpectedOwnerId) && assetID(request.AssetId) && request.ExpectedAssetRevision > 0 && request.AgentId == "claude" && request.SchemaVersion == 1 && validAdapter(request.Adapter) && request.Target == configurationnativev1.Target_TARGET_CLAUDE_SETTINGS && request.Scope == configurationnativev1.Scope_SCOPE_USER && opaqueID(request.ProfileId) && request.SelectionRevision > 0 && len(request.OperationId) == 16 && len(request.ClientNonce) == 32 && optionalSignature(request.ProofSignature)
}

func validPreparation(binding *configurationnativev1.PreparationBinding) bool {
	if binding != nil && binding.Codex != nil {
		return false
	}
	return knownMessage(binding) && validOwner(binding.OwnerId) && validOrigin(binding.ServerOrigin) && len(binding.NativeSessionBinding) == 32 && len(binding.OperationId) == 16 && assetID(binding.AssetId) && binding.AssetRevision > 0 && binding.AgentId == "claude" && binding.AssetKind == configurationv1.AssetKind_ASSET_KIND_PROVIDER && binding.SchemaVersion == 1 && validAdapter(binding.Adapter) && binding.Target == configurationnativev1.Target_TARGET_CLAUDE_SETTINGS && binding.Scope == configurationnativev1.Scope_SCOPE_USER && opaqueID(binding.ProfileId) && binding.SelectionRevision > 0 && validSlots(binding.RequiredSlots) && binding.PreparedAtUnixMs > 0 && binding.PrepareExpiresAtUnixMs > binding.PreparedAtUnixMs && binding.PrepareExpiresAtUnixMs-binding.PreparedAtUnixMs <= 300000 && len(binding.ConsumerKeySha256) == 32 && opaqueID(binding.PrepareId) && len(binding.PatchSha256) == 32
}

func validApply(binding *configurationnativev1.ApplyBinding) bool {
	if binding != nil && binding.CodexLocalBinding != nil {
		return false
	}
	return knownMessage(binding) && validPreparation(binding.Preparation) && len(binding.IntentCommitment) == 32 && binding.IntentExpiresAtUnixMs > binding.Preparation.PreparedAtUnixMs && binding.IntentExpiresAtUnixMs <= binding.Preparation.PrepareExpiresAtUnixMs
}

func validAuthorizeRequest(request *configurationnativev1.AuthorizeNativeMaterializationRequest) bool {
	if !knownMessage(request) || request.CodexLocalBinding != nil || !opaqueID(request.PrepareId) || len(request.OperationId) != 16 || len(request.IntentCommitment) != 32 || !validSlots(request.RequestedSlots) || len(request.ClientNonce) != 32 || !optionalSignature(request.ProofSignature) || request.IntentExpiresAtUnixMs <= 0 {
		return false
	}
	switch authorization := request.Authorization.(type) {
	case *configurationnativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation:
		return authorization != nil && authorization.ExplicitConfirmation != nil && authorization.ExplicitConfirmation.ConfirmationRevision > 0
	case *configurationnativev1.AuthorizeNativeMaterializationRequest_AutoPolicy:
		return authorization != nil && authorization.AutoPolicy != nil && opaqueID(string(authorization.AutoPolicy.PolicyId)) && authorization.AutoPolicy.PolicyRevision > 0
	default:
		return false
	}
}

func validConsumeRequest(request *configurationnativev1.ConsumeNativeMaterializationRequest) bool {
	return knownMessage(request) && opaqueID(request.PrepareId) && opaqueID(request.GrantId) && len(request.GrantToken) == 32 && len(request.OperationId) == 16 && len(request.IntentCommitment) == 32 && len(request.ClientNonce) == 32 && optionalSignature(request.ProofSignature)
}

func validStatusRequest(request *configurationnativev1.GetNativeMaterializationStatusRequest) bool {
	return knownMessage(request) && opaqueID(request.PrepareId) && (request.GrantId == "" || opaqueID(request.GrantId)) && len(request.OperationId) == 16 && len(request.ClientNonce) == 32 && optionalSignature(request.ProofSignature)
}

func optionalSignature(value []byte) bool { return len(value) == 0 || len(value) == 64 }

func validSlots(slots []configurationnativev1.SecretSlot) bool {
	return len(slots) == 0 || len(slots) == 1 && (slots[0] == configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_API_KEY || slots[0] == configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_AUTH_TOKEN)
}

func validAdapter(adapter *configurationnativev1.AdapterBinding) bool {
	if !knownMessage(adapter) || adapter.ApiVersion != 1 || adapter.PluginId != "builtin.claude" || adapter.PluginVersion != "1.1.0" || len(adapter.DefinitionDigest) != 64 || !slices.Equal(adapter.RequiredCapabilities, []string{"claude.provider-switch.v1"}) {
		return false
	}
	for _, letter := range adapter.DefinitionDigest {
		if !(letter >= '0' && letter <= '9' || letter >= 'a' && letter <= 'f') {
			return false
		}
	}
	return true
}

func validPatch(patch *configurationnativev1.ClaudeProviderPatch) bool {
	if !knownMessage(patch) || len(patch.Operations) != 7 {
		return false
	}
	selected := 0
	for index, operation := range patch.Operations {
		if operation == nil || operation.Slot != configurationnativev1.EnvSlot(index+1) {
			return false
		}
		switch action := operation.Action.(type) {
		case *configurationnativev1.PatchOperation_SetLiteral:
			if action == nil || action.SetLiteral == nil || index < 2 || !validPatchLiteral(index, action.SetLiteral.Value) {
				return false
			}
		case *configurationnativev1.PatchOperation_SetSelectedSecret:
			if action == nil || action.SetSelectedSecret == nil || index > 1 || action.SetSelectedSecret.Slot != configurationnativev1.SecretSlot(index+1) {
				return false
			}
			selected++
		case *configurationnativev1.PatchOperation_Remove:
			if action == nil || action.Remove == nil {
				return false
			}
		default:
			return false
		}
	}
	return selected <= 1 && (selected > 0 || patch.Operations[2].GetRemove() != nil)
}

func validPatchLiteral(index int, value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	if index != 2 {
		return len(value) <= 256
	}
	parsed, err := url.Parse(value)
	return len(value) <= 2048 && err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && !strings.ContainsAny(value, "?#")
}

func validOwner(value string) bool {
	return len(value) > 0 && len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func opaqueID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '_' || letter == '-') {
			return false
		}
	}
	return true
}

func assetID(value string) bool {
	if len(value) != 26 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return false
		}
	}
	return true
}

func validOrigin(value string) bool {
	if len(value) == 0 || len(value) > 2048 || !utf8.ValidString(value) || strings.ContainsAny(value, "?#\\") || strings.ContainsFunc(value, unicode.IsSpace) || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	origin, err := url.Parse(value)
	if err != nil || origin.Scheme != "https" && origin.Scheme != "http" || origin.Hostname() == "" || origin.User != nil || origin.Opaque != "" || origin.Path != "" || origin.RawPath != "" || origin.String() != value || strings.ToLower(origin.Host) != origin.Host {
		return false
	}
	hostname := origin.Hostname()
	address, addressErr := netip.ParseAddr(hostname)
	if addressErr == nil {
		if address.Zone() != "" || address.String() != hostname {
			return false
		}
	} else {
		for label := range strings.SplitSeq(hostname, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, letter := range label {
				if !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-') {
					return false
				}
			}
		}
	}
	if origin.Scheme == "http" && hostname != "localhost" && (addressErr != nil || !address.IsLoopback()) {
		return false
	}
	if port := origin.Port(); port != "" {
		number, err := strconv.Atoi(port)
		return err == nil && number > 0 && number <= 65535 && strconv.Itoa(number) == port
	}
	return !strings.HasSuffix(origin.Host, ":")
}

func knownMessage(message proto.Message) bool {
	if message == nil || !message.ProtoReflect().IsValid() {
		return false
	}
	budget := maxNativeProofBytes
	return knownTree(message.ProtoReflect(), 0, &budget) && proto.Size(message) <= maxNativeProofBytes
}

func knownTree(message protoreflect.Message, depth int, budget *int) bool {
	if !message.IsValid() || depth >= 16 || len(message.GetUnknown()) > 0 {
		return false
	}
	valid := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			valid = false
			return false
		}
		check := func(value protoreflect.Value) bool {
			*budget--
			if *budget < 0 {
				return false
			}
			switch field.Kind() {
			case protoreflect.MessageKind:
				return knownTree(value.Message(), depth+1, budget)
			case protoreflect.StringKind:
				*budget -= len(value.String())
				return *budget >= 0 && utf8.ValidString(value.String())
			case protoreflect.BytesKind:
				*budget -= len(value.Bytes())
				return *budget >= 0
			case protoreflect.EnumKind:
				return value.Enum() > 0 && field.Enum().Values().ByNumber(value.Enum()) != nil
			default:
				return true
			}
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

func StrictUnmarshalNativeProof(data []byte, destination proto.Message) error {
	if !supportedProofMessage(destination) || len(data) > maxNativeProofBytes || !destination.ProtoReflect().IsValid() {
		return ErrInvalidNativeProof
	}
	switch destination.(type) {
	case *configurationnativev1.BeginNativeConsumerRequest, *configurationnativev1.BeginNativeConsumerResponse, *configurationnativev1.BindNativeConsumerRequest:
		if len(data) > 8192 {
			return ErrInvalidNativeProof
		}
	}
	if !strictProofWire(data, destination.ProtoReflect().Descriptor(), 0) {
		return ErrInvalidNativeProof
	}
	temporary := destination.ProtoReflect().New().Interface()
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, temporary) != nil || !validDecodedProof(temporary) {
		return ErrInvalidNativeProof
	}
	proto.Reset(destination)
	proto.Merge(destination, temporary)
	return nil
}

func supportedProofMessage(message proto.Message) bool {
	switch message.(type) {
	case *configurationnativev1.BeginNativeConsumerRequest, *configurationnativev1.BeginNativeConsumerResponse,
		*configurationnativev1.BindNativeConsumerRequest, *configurationnativev1.AdapterBinding,
		*configurationnativev1.PreparationBinding, *configurationnativev1.ApplyBinding,
		*configurationnativev1.ClaudeProviderPatch, *configurationnativev1.PrepareNativeApplyRequest,
		*configurationnativev1.PrepareNativeApplyResponse, *configurationnativev1.AuthorizeNativeMaterializationRequest,
		*configurationnativev1.ConsumeNativeMaterializationRequest, *configurationnativev1.GetNativeMaterializationStatusRequest:
		return true
	default:
		return false
	}
}

func validDecodedProof(message proto.Message) bool {
	switch value := message.(type) {
	case *configurationnativev1.BeginNativeConsumerRequest:
		return validBegin(value)
	case *configurationnativev1.BeginNativeConsumerResponse:
		return validChallenge(value)
	case *configurationnativev1.BindNativeConsumerRequest:
		return knownMessage(value) && opaqueID(value.ChallengeId) && len(value.ProofSignature) == 64
	case *configurationnativev1.AdapterBinding:
		return validAdapter(value)
	case *configurationnativev1.PreparationBinding:
		if value.Target == 2 {
			return validCodexPreparation(value)
		}
		return validPreparation(value)
	case *configurationnativev1.ApplyBinding:
		if value.Preparation != nil && value.Preparation.Target == 2 {
			_, err := HashCodexApply(value)
			return err == nil
		}
		return validApply(value)
	case *configurationnativev1.ClaudeProviderPatch:
		return validPatch(value)
	case *configurationnativev1.PrepareNativeApplyRequest:
		if value.Target == 2 {
			return validCodexPrepare(value) && len(value.ProofSignature) == 64
		}
		return validPrepareRequest(value) && len(value.ProofSignature) == 64
	case *configurationnativev1.PrepareNativeApplyResponse:
		if value.Binding != nil && value.Binding.Target == 2 {
			return ValidateCodexPreparedDeclaration(value) == nil
		}
		return ValidatePreparedPatch(value) == nil
	case *configurationnativev1.AuthorizeNativeMaterializationRequest:
		if value.CodexLocalBinding != nil {
			return validCodexAuthorize(value) && len(value.ProofSignature) == 64
		}
		return validAuthorizeRequest(value) && len(value.ProofSignature) == 64
	case *configurationnativev1.ConsumeNativeMaterializationRequest:
		return validConsumeRequest(value) && len(value.ProofSignature) == 64
	case *configurationnativev1.GetNativeMaterializationStatusRequest:
		return validStatusRequest(value) && len(value.ProofSignature) == 64
	default:
		return false
	}
}

func canonicalVarint(data []byte) (uint64, int) {
	value, count := protowire.ConsumeVarint(data)
	if count < 0 || count != protowire.SizeVarint(value) {
		return 0, -1
	}
	return value, count
}

func strictProofWire(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	budget := 1024
	return strictProofValue(data, descriptor, depth, &budget)
}

func strictProofValue(data []byte, descriptor protoreflect.MessageDescriptor, depth int, budget *int) bool {
	if depth >= 16 {
		return false
	}
	seenFields := make(map[protoreflect.FieldNumber]bool)
	seenOneofs := make(map[protoreflect.FullName]bool)
	for len(data) > 0 {
		*budget--
		if *budget < 0 {
			return false
		}
		tag, count := canonicalVarint(data)
		if count < 0 || tag>>3 == 0 || tag>>3 > uint64(protowire.MaxValidNumber) {
			return false
		}
		data = data[count:]
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(tag >> 3))
		if field == nil || !field.IsList() && seenFields[field.Number()] {
			return false
		}
		seenFields[field.Number()] = true
		if oneof := field.ContainingOneof(); oneof != nil {
			if seenOneofs[oneof.FullName()] {
				return false
			}
			seenOneofs[oneof.FullName()] = true
		}
		wireType := protowire.Type(tag & 7)
		switch field.Kind() {
		case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
			if wireType != protowire.BytesType {
				return false
			}
			length, prefix := canonicalVarint(data)
			if prefix < 0 || length > uint64(len(data)-prefix) {
				return false
			}
			value := data[prefix : prefix+int(length)]
			data = data[prefix+int(length):]
			if field.Kind() == protoreflect.MessageKind && !strictProofValue(value, field.Message(), depth+1, budget) || field.Kind() == protoreflect.StringKind && !utf8.Valid(value) {
				return false
			}
		case protoreflect.EnumKind, protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Int64Kind, protoreflect.BoolKind:
			if wireType == protowire.BytesType && field.IsPacked() {
				length, prefix := canonicalVarint(data)
				if prefix < 0 || length == 0 || length > uint64(len(data)-prefix) {
					return false
				}
				packed := data[prefix : prefix+int(length)]
				data = data[prefix+int(length):]
				for len(packed) > 0 {
					*budget--
					if *budget < 0 {
						return false
					}
					value, size := canonicalVarint(packed)
					if size < 0 || !validWireScalar(field, value) {
						return false
					}
					packed = packed[size:]
				}
			} else {
				value, size := canonicalVarint(data)
				if wireType != protowire.VarintType || size < 0 || !validWireScalar(field, value) {
					return false
				}
				data = data[size:]
			}
		default:
			return false
		}
	}
	if descriptor.FullName() == "skillhub.configuration.native.v1.PreparationBinding" && seenFields[21] && seenFields[40] {
		return false
	}
	if descriptor.FullName() == "skillhub.configuration.native.v1.PrepareNativeApplyResponse" && seenFields[2] && seenFields[5] {
		return false
	}
	return true
}

func validWireScalar(field protoreflect.FieldDescriptor, value uint64) bool {
	if field.FullName() == "skillhub.configuration.native.v1.PrepareNativeApplyRequest.codex_contract_version" && value != 1 {
		return false
	}
	switch field.Kind() {
	case protoreflect.EnumKind:
		return value > 0 && value <= 0x7fffffff && field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) != nil
	case protoreflect.Uint32Kind:
		return value <= 0xffffffff
	case protoreflect.BoolKind:
		return value <= 1
	default:
		return true
	}
}
