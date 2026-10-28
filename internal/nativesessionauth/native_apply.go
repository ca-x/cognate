package nativesessionauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ConsumerFields struct {
	OwnerID, ServerOrigin string
	SessionBinding        [32]byte
	OperationID           [16]byte
	PublicKey             [32]byte
}

type AdapterFields struct {
	APIVersion                                uint32
	PluginID, PluginVersion, DefinitionDigest string
	Capability                                string
}

type PrepareRequest struct {
	OwnerID, AssetID, AgentID, ProfileID string
	AssetRevision                        int64
	SchemaVersion, Target, Scope         uint32
	Adapter                              AdapterFields
	SelectionRevision                    uint64
	OperationID                          [16]byte
	Nonce                                [32]byte
}

type PrepareFields struct {
	Consumer ConsumerFields
	Request  PrepareRequest
}

type PreparationFields struct {
	Consumer              ConsumerFields
	Request               PrepareRequest
	RequiredSlot          uint32
	PrepareID             string
	PreparedMS, ExpiresMS int64
	PatchSHA256           [32]byte
}

type ApplyFields struct {
	Preparation     PreparationFields
	Commitment      [32]byte
	IntentExpiresMS int64
}

type ConsentFields struct {
	Kind     uint32
	Revision uint64
	PolicyID string
}

type AuthorizeRequest struct {
	PrepareID       string
	OperationID     [16]byte
	Commitment      [32]byte
	RequiredSlot    uint32
	Consent         ConsentFields
	Nonce           [32]byte
	IntentExpiresMS int64
}

type AuthorizeFields struct {
	Preparation PreparationFields
	Challenge   [32]byte
	Request     AuthorizeRequest
}

type ParsedGrantToken struct{ opaque[[32]byte] }

func ParseGrantToken(raw []byte) (ParsedGrantToken, error) {
	if len(raw) != 32 {
		return ParsedGrantToken{}, ErrInvalidEvidence
	}
	return ParsedGrantToken{opaque: wrap(sha256.Sum256(raw))}, nil
}

func (token ParsedGrantToken) TokenDigest() ([32]byte, bool) {
	if !token.present() {
		return [32]byte{}, false
	}
	return *token.payload.value, true
}

type ConsumeRequest struct {
	PrepareID, GrantID string
	Token              ParsedGrantToken
	OperationID        [16]byte
	Commitment, Nonce  [32]byte
}

type ConsumeFields struct {
	Apply                                ApplyFields
	AuthorizeChallenge, ConsumeChallenge [32]byte
	GrantExpiresMS                       int64
	Request                              ConsumeRequest
}

type StatusRequest struct {
	PrepareID, GrantID string
	OperationID        [16]byte
	Nonce              [32]byte
}

type StatusFields struct {
	Preparation PreparationFields
	Challenge   [32]byte
	Request     StatusRequest
}

type PatchOperation struct {
	Action, SecretSlot uint32
	Literal            string
}

type PatchFields [7]PatchOperation

func HashPatch(patch PatchFields) ([32]byte, error) {
	encoded := blob(nil, []byte("cognate/native-config-patch/v1"))
	encoded = binary.BigEndian.AppendUint32(encoded, 1)
	encoded = binary.BigEndian.AppendUint32(encoded, 7)
	selected := 0
	for index, operation := range patch {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(index+1))
		encoded = binary.BigEndian.AppendUint32(encoded, operation.Action)
		switch operation.Action {
		case 1:
			if index < 2 || operation.SecretSlot != 0 || len(operation.Literal) == 0 || !utf8.ValidString(operation.Literal) || strings.ContainsFunc(operation.Literal, unicode.IsControl) || len(operation.Literal) > 256 && index != 2 {
				return [32]byte{}, ErrInvalidEvidence
			}
			if index == 2 {
				endpoint, err := url.Parse(operation.Literal)
				if err != nil || len(operation.Literal) > 2048 || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Scheme != "https" && endpoint.Scheme != "http" || strings.ContainsAny(operation.Literal, "?#") {
					return [32]byte{}, ErrInvalidEvidence
				}
			}
			encoded = blob(encoded, []byte(operation.Literal))
		case 2:
			if index > 1 || operation.SecretSlot != uint32(index+1) || operation.Literal != "" {
				return [32]byte{}, ErrInvalidEvidence
			}
			selected++
			encoded = binary.BigEndian.AppendUint32(encoded, operation.SecretSlot)
		case 3:
			if operation.Literal != "" || operation.SecretSlot != 0 {
				return [32]byte{}, ErrInvalidEvidence
			}
		default:
			return [32]byte{}, ErrInvalidEvidence
		}
	}
	if selected > 1 || selected == 0 && patch[2].Action != 3 {
		return [32]byte{}, ErrInvalidEvidence
	}
	return sha256.Sum256(encoded), nil
}

func encodeApplySlot(encoded []byte, slot uint32) []byte {
	if slot == 0 {
		return binary.BigEndian.AppendUint32(encoded, 0)
	}
	return binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(encoded, 1), slot)
}

func preparationEncoded(fields PreparationFields) ([]byte, error) {
	consumer, request := fields.Consumer, fields.Request
	if _, err := prepareCanonical(PrepareFields{Consumer: consumer, Request: request}); err != nil || fields.RequiredSlot > 2 || !validID(fields.PrepareID) || fields.PreparedMS <= 0 || fields.ExpiresMS <= fields.PreparedMS || fields.ExpiresMS-fields.PreparedMS > 300000 {
		return nil, ErrInvalidEvidence
	}
	encoded := blob(nil, []byte(consumer.OwnerID))
	encoded = blob(encoded, []byte(consumer.ServerOrigin))
	encoded = blob(encoded, consumer.SessionBinding[:])
	encoded = blob(encoded, consumer.OperationID[:])
	encoded = blob(encoded, []byte(request.AssetID))
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(request.AssetRevision))
	encoded = blob(encoded, []byte(request.AgentID))
	encoded = binary.BigEndian.AppendUint32(encoded, 1)
	encoded = binary.BigEndian.AppendUint32(encoded, request.SchemaVersion)
	encoded = encodeApplyAdapter(encoded, request.Adapter)
	encoded = binary.BigEndian.AppendUint32(encoded, request.Target)
	encoded = binary.BigEndian.AppendUint32(encoded, request.Scope)
	encoded = blob(encoded, []byte(request.ProfileID))
	encoded = binary.BigEndian.AppendUint64(encoded, request.SelectionRevision)
	encoded = encodeApplySlot(encoded, fields.RequiredSlot)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(fields.ExpiresMS))
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	encoded = blob(encoded, keyHash[:])
	encoded = blob(encoded, []byte(fields.PrepareID))
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(fields.PreparedMS))
	return blob(encoded, fields.PatchSHA256[:]), nil
}

func HashPreparation(fields PreparationFields) ([32]byte, error) {
	encoded, err := preparationEncoded(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(append(blob(nil, []byte("cognate/native-config-preparation/v1")), encoded...)), nil
}

func HashApply(fields ApplyFields) ([32]byte, error) {
	encoded, err := preparationEncoded(fields.Preparation)
	if err != nil || fields.IntentExpiresMS <= fields.Preparation.PreparedMS || fields.IntentExpiresMS > fields.Preparation.ExpiresMS {
		return [32]byte{}, ErrInvalidEvidence
	}
	encoded = blob(blob(nil, []byte("cognate/native-config-apply/v1")), encoded)
	encoded = blob(encoded, fields.Commitment[:])
	return sha256.Sum256(binary.BigEndian.AppendUint64(encoded, uint64(fields.IntentExpiresMS))), nil
}

func authorizeCanonical(fields AuthorizeFields) ([]byte, error) {
	request := fields.Request
	digest, err := HashPreparation(fields.Preparation)
	if err != nil || request.PrepareID != fields.Preparation.PrepareID || request.OperationID != fields.Preparation.Consumer.OperationID || request.RequiredSlot != fields.Preparation.RequiredSlot || request.IntentExpiresMS <= fields.Preparation.PreparedMS || request.IntentExpiresMS > fields.Preparation.ExpiresMS || request.Consent.Revision == 0 || !(request.Consent.Kind == 1 && request.Consent.PolicyID == "" || request.Consent.Kind == 2 && validID(request.Consent.PolicyID)) {
		return nil, ErrInvalidEvidence
	}
	encoded := blob(applyCanonical("authorize"), digest[:])
	encoded = blob(encoded, fields.Challenge[:])
	encoded = blob(encoded, []byte(request.PrepareID))
	encoded = blob(encoded, request.OperationID[:])
	encoded = blob(encoded, request.Commitment[:])
	encoded = encodeApplySlot(encoded, request.RequiredSlot)
	encoded = binary.BigEndian.AppendUint32(encoded, request.Consent.Kind)
	if request.Consent.Kind == 2 {
		encoded = blob(encoded, []byte(request.Consent.PolicyID))
	}
	encoded = binary.BigEndian.AppendUint64(encoded, request.Consent.Revision)
	encoded = blob(encoded, request.Nonce[:])
	return binary.BigEndian.AppendUint64(encoded, uint64(request.IntentExpiresMS)), nil
}

func consumeCanonical(fields ConsumeFields) ([]byte, error) {
	request := fields.Request
	digest, err := HashApply(fields.Apply)
	tokenHash, present := request.Token.TokenDigest()
	if err != nil || !present || request.PrepareID != fields.Apply.Preparation.PrepareID || !validID(request.GrantID) || fields.Apply.Preparation.RequiredSlot == 0 || request.OperationID != fields.Apply.Preparation.Consumer.OperationID || request.Commitment != fields.Apply.Commitment || fields.GrantExpiresMS <= fields.Apply.Preparation.PreparedMS || fields.GrantExpiresMS > fields.Apply.IntentExpiresMS {
		return nil, ErrInvalidEvidence
	}
	encoded := blob(applyCanonical("consume"), digest[:])
	encoded = blob(encoded, fields.AuthorizeChallenge[:])
	encoded = blob(encoded, []byte(request.GrantID))
	encoded = blob(encoded, tokenHash[:])
	encoded = blob(encoded, fields.ConsumeChallenge[:])
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(fields.GrantExpiresMS))
	encoded = blob(encoded, request.OperationID[:])
	encoded = blob(encoded, request.Commitment[:])
	return blob(encoded, request.Nonce[:]), nil
}

func statusCanonical(fields StatusFields) ([]byte, error) {
	request := fields.Request
	digest, err := HashPreparation(fields.Preparation)
	if err != nil || request.PrepareID != fields.Preparation.PrepareID || request.OperationID != fields.Preparation.Consumer.OperationID || request.GrantID != "" && !validID(request.GrantID) {
		return nil, ErrInvalidEvidence
	}
	encoded := blob(applyCanonical("status"), digest[:])
	encoded = blob(encoded, fields.Challenge[:])
	encoded = blob(encoded, []byte(request.PrepareID))
	encoded = blob(encoded, []byte(request.GrantID))
	encoded = blob(encoded, request.OperationID[:])
	return blob(encoded, request.Nonce[:]), nil
}

func AuthorizeFieldsDigest(fields AuthorizeFields) ([32]byte, error) {
	encoded, err := authorizeCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return applyDigest(encoded), nil
}

func ConsumeFieldsDigest(fields ConsumeFields) ([32]byte, error) {
	encoded, err := consumeCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return applyDigest(encoded), nil
}

func StatusFieldsDigest(fields StatusFields) ([32]byte, error) {
	encoded, err := statusCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return applyDigest(encoded), nil
}

func (verifier *Verifier) VerifyAuthorize(bearer ParsedNativeBearer, fields AuthorizeFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := authorizeCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyApply(bearer, "authorize", fields.Preparation.Consumer.PublicKey, encoded, signature)
}

func (verifier *Verifier) VerifyConsume(bearer ParsedNativeBearer, fields ConsumeFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := consumeCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyApply(bearer, "consume", fields.Apply.Preparation.Consumer.PublicKey, encoded, signature)
}

func (verifier *Verifier) VerifyStatus(bearer ParsedNativeBearer, fields StatusFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := statusCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyApply(bearer, "status", fields.Preparation.Consumer.PublicKey, encoded, signature)
}

type applyEvidence struct {
	native, canonical [32]byte
	keyID, purpose    string
}

type VerifiedApplyProof struct{ opaque[applyEvidence] }

func (proof VerifiedApplyProof) MatchesApply(keyID string, native [32]byte, purpose string, digest [32]byte) bool {
	return proof.present() && subtle.ConstantTimeCompare([]byte(proof.payload.value.keyID), []byte(keyID)) == 1 && proof.payload.value.purpose == purpose && subtle.ConstantTimeCompare(proof.payload.value.native[:], native[:]) == 1 && subtle.ConstantTimeCompare(proof.payload.value.canonical[:], digest[:]) == 1
}

func applyCanonical(purpose string) []byte {
	return binary.BigEndian.AppendUint32(blob(blob(nil, []byte("cognate/native-config-proof/v1")), []byte(purpose)), 1)
}

func applyDigest(canonical []byte) [32]byte {
	return sha256.Sum256(blob(blob(nil, []byte("cognate/native-config-stage-evidence/v1")), canonical))
}

func (verifier *Verifier) verifyApply(bearer ParsedNativeBearer, purpose string, publicKey [32]byte, canonical, signature []byte) (VerifiedApplyProof, error) {
	digest, present := bearer.TokenDigest()
	if verifier == nil || !verifier.present() || !present || len(canonical) == 0 || len(canonical) > 512<<10 || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey[:], canonical, signature) {
		return VerifiedApplyProof{}, ErrInvalidEvidence
	}
	return VerifiedApplyProof{opaque: wrap(applyEvidence{native: digest, canonical: applyDigest(canonical), keyID: verifier.payload.value.keyID, purpose: purpose})}, nil
}

func validApplyOwner(owner string) bool {
	return len(owner) > 0 && len(owner) <= 512 && utf8.ValidString(owner) && !strings.ContainsAny(owner, "\r\n\x00")
}

func validApplyAsset(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, letter := range id {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return false
		}
	}
	return true
}

func validApplyAdapter(adapter AdapterFields) bool {
	if adapter.APIVersion != 1 || adapter.PluginID != "builtin.claude" || adapter.PluginVersion != "1.1.0" || adapter.Capability != "claude.provider-switch.v1" || len(adapter.DefinitionDigest) != 64 {
		return false
	}
	for _, letter := range adapter.DefinitionDigest {
		if !(letter >= '0' && letter <= '9' || letter >= 'a' && letter <= 'f') {
			return false
		}
	}
	return true
}

func encodeApplyAdapter(encoded []byte, adapter AdapterFields) []byte {
	encoded = binary.BigEndian.AppendUint32(encoded, adapter.APIVersion)
	for _, value := range []string{adapter.PluginID, adapter.PluginVersion, adapter.DefinitionDigest} {
		encoded = blob(encoded, []byte(value))
	}
	return blob(binary.BigEndian.AppendUint32(encoded, 1), []byte(adapter.Capability))
}

func validPrepareRequest(request PrepareRequest) bool {
	return validApplyOwner(request.OwnerID) && validApplyAsset(request.AssetID) && request.AssetRevision > 0 && request.AgentID == "claude" && request.SchemaVersion == 1 && validApplyAdapter(request.Adapter) && request.Target == 1 && request.Scope == 1 && validID(request.ProfileID) && request.SelectionRevision > 0
}

func prepareCanonical(fields PrepareFields) ([]byte, error) {
	consumer, request := fields.Consumer, fields.Request
	if !validApplyOwner(consumer.OwnerID) || !ValidServerOrigin(consumer.ServerOrigin) || !validPrepareRequest(request) || request.OwnerID != consumer.OwnerID || request.OperationID != consumer.OperationID {
		return nil, ErrInvalidEvidence
	}
	encoded := applyCanonical("prepare")
	encoded = blob(encoded, []byte(consumer.ServerOrigin))
	encoded = blob(encoded, []byte(consumer.OwnerID))
	encoded = blob(encoded, consumer.SessionBinding[:])
	encoded = blob(encoded, consumer.OperationID[:])
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	encoded = blob(encoded, keyHash[:])
	encoded = blob(encoded, []byte(request.OwnerID))
	encoded = blob(encoded, []byte(request.AssetID))
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(request.AssetRevision))
	encoded = blob(encoded, []byte(request.AgentID))
	encoded = binary.BigEndian.AppendUint32(encoded, request.SchemaVersion)
	encoded = encodeApplyAdapter(encoded, request.Adapter)
	encoded = binary.BigEndian.AppendUint32(encoded, request.Target)
	encoded = binary.BigEndian.AppendUint32(encoded, request.Scope)
	encoded = blob(encoded, []byte(request.ProfileID))
	encoded = binary.BigEndian.AppendUint64(encoded, request.SelectionRevision)
	encoded = blob(encoded, request.OperationID[:])
	return blob(encoded, request.Nonce[:]), nil
}

func PrepareFieldsDigest(fields PrepareFields) ([32]byte, error) {
	encoded, err := prepareCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return applyDigest(encoded), nil
}

func (verifier *Verifier) VerifyPrepare(bearer ParsedNativeBearer, fields PrepareFields, signature []byte) (VerifiedApplyProof, error) {
	encoded, err := prepareCanonical(fields)
	if err != nil {
		return VerifiedApplyProof{}, err
	}
	return verifier.verifyApply(bearer, "prepare", fields.Consumer.PublicKey, encoded, signature)
}
