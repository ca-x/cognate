package nativesessionauth

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidEvidence = errors.New("invalid native authentication evidence")

const PurposeConfigurationApply uint32 = 1

type payload[Value any] struct{ value *Value }
type opaque[Value any] struct{ payload *payload[Value] }

func wrap[Value any](value Value) opaque[Value] {
	return opaque[Value]{payload: &payload[Value]{value: new(value)}}
}

func (opaque[Value]) String() string   { return "[redacted native authentication]" }
func (opaque[Value]) GoString() string { return "[redacted native authentication]" }
func (opaque[Value]) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted native authentication]")
}
func (opaque[Value]) LogValue() slog.Value {
	return slog.StringValue("[redacted native authentication]")
}
func (opaque[Value]) MarshalText() ([]byte, error) {
	return []byte("[redacted native authentication]"), nil
}
func (opaque[Value]) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted native authentication]"`), nil
}
func (value opaque[Value]) present() bool {
	return value.payload != nil && value.payload.value != nil
}

type verifierKeys struct {
	signing [32]byte
	keyID   string
}
type ownerEvidence struct {
	digest [32]byte
	keyID  string
}
type bindingEvidence struct {
	parent [32]byte
	digest [32]byte
	keyID  string
}

type Verifier struct{ opaque[verifierKeys] }
type VerifiedOwnerSession struct{ opaque[ownerEvidence] }
type VerifiedBindProof struct{ opaque[bindingEvidence] }
type ParsedNativeBearer struct{ opaque[[32]byte] }

type BindFields struct {
	ServerOrigin                 string
	OwnerID                      string
	Purpose                      uint32
	OperationID                  [16]byte
	ConsumerPublicKey            [32]byte
	ClientNonce                  [32]byte
	ChallengeID                  string
	Challenge                    [32]byte
	NativeSessionBinding         [32]byte
	ChallengeExpiresAtUnixMS     int64
	NativeSessionExpiresAtUnixMS int64
}

func NewVerifier(masterKey []byte) (*Verifier, error) {
	if len(masterKey) != 32 {
		return nil, ErrInvalidEvidence
	}
	signing, err := hkdf.Key(sha256.New, masterKey, nil, "skillhub/session-signing/v1", 32)
	if err != nil {
		return nil, ErrInvalidEvidence
	}
	defer clear(signing)
	fingerprint := sha256.Sum256(masterKey)
	return &Verifier{opaque: wrap(verifierKeys{signing: [32]byte(signing), keyID: base64.RawURLEncoding.EncodeToString(fingerprint[:])})}, nil
}

func canonical32(value string) ([32]byte, bool) {
	if len(value) != 43 {
		return [32]byte{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	defer clear(decoded)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return [32]byte{}, false
	}
	return [32]byte(decoded), true
}

func (verifier *Verifier) VerifyOwner(raw string) (VerifiedOwnerSession, error) {
	if verifier == nil || !verifier.present() || len(raw) != 87 {
		return VerifiedOwnerSession{}, ErrInvalidEvidence
	}
	encodedNonce, encodedMAC, found := strings.Cut(raw, ".")
	nonce, nonceOK := canonical32(encodedNonce)
	defer clear(nonce[:])
	signature, signatureOK := canonical32(encodedMAC)
	if !found || !nonceOK || !signatureOK {
		return VerifiedOwnerSession{}, ErrInvalidEvidence
	}
	keys := verifier.payload.value
	mac := hmac.New(sha256.New, keys.signing[:])
	_, _ = mac.Write(nonce[:])
	if !hmac.Equal(signature[:], mac.Sum(nil)) {
		return VerifiedOwnerSession{}, ErrInvalidEvidence
	}
	return VerifiedOwnerSession{opaque: wrap(ownerEvidence{digest: sha256.Sum256([]byte(raw)), keyID: keys.keyID})}, nil
}

func (evidence VerifiedOwnerSession) TokenDigest() ([32]byte, bool) {
	if !evidence.present() {
		return [32]byte{}, false
	}
	return evidence.payload.value.digest, true
}

func (evidence VerifiedOwnerSession) MatchesKeyID(keyID string) bool {
	return evidence.present() && subtle.ConstantTimeCompare([]byte(evidence.payload.value.keyID), []byte(keyID)) == 1
}

func (verifier *Verifier) VerifyBind(parent VerifiedOwnerSession, fields BindFields, signature []byte) (VerifiedBindProof, error) {
	if verifier == nil || !verifier.present() || !parent.MatchesKeyID(verifier.payload.value.keyID) || len(signature) != ed25519.SignatureSize {
		return VerifiedBindProof{}, ErrInvalidEvidence
	}
	canonical, err := bindCanonical(fields)
	if err != nil || !ed25519.Verify(fields.ConsumerPublicKey[:], canonical, signature) {
		return VerifiedBindProof{}, ErrInvalidEvidence
	}
	parentDigest, _ := parent.TokenDigest()
	return VerifiedBindProof{opaque: wrap(bindingEvidence{parent: parentDigest, digest: bindDigest(canonical), keyID: verifier.payload.value.keyID})}, nil
}

func (proof VerifiedBindProof) MatchesBinding(keyID string, parent, digest [32]byte) bool {
	return proof.present() && subtle.ConstantTimeCompare([]byte(proof.payload.value.keyID), []byte(keyID)) == 1 &&
		subtle.ConstantTimeCompare(proof.payload.value.parent[:], parent[:]) == 1 &&
		subtle.ConstantTimeCompare(proof.payload.value.digest[:], digest[:]) == 1
}

func BindFieldsDigest(fields BindFields) ([32]byte, error) {
	canonical, err := bindCanonical(fields)
	if err != nil {
		return [32]byte{}, err
	}
	return bindDigest(canonical), nil
}

func blob(encoded, value []byte) []byte {
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(value)))
	return append(encoded, value...)
}

func bindDigest(canonical []byte) [32]byte {
	return sha256.Sum256(blob(blob(nil, []byte("cognate/native-config-bind-evidence/v1")), canonical))
}

func bindCanonical(fields BindFields) ([]byte, error) {
	if fields.Purpose != PurposeConfigurationApply || !ValidServerOrigin(fields.ServerOrigin) ||
		len(fields.OwnerID) == 0 || len(fields.OwnerID) > 512 || !utf8.ValidString(fields.OwnerID) || strings.ContainsAny(fields.OwnerID, "\r\n\x00") ||
		!validID(fields.ChallengeID) || fields.ChallengeExpiresAtUnixMS <= 0 || fields.NativeSessionExpiresAtUnixMS < fields.ChallengeExpiresAtUnixMS {
		return nil, ErrInvalidEvidence
	}
	encoded := blob(nil, []byte("cognate/native-config-proof/v1"))
	encoded = blob(encoded, []byte("bind"))
	encoded = binary.BigEndian.AppendUint32(encoded, 1)
	encoded = blob(encoded, []byte(fields.ServerOrigin))
	encoded = blob(encoded, []byte(fields.OwnerID))
	encoded = binary.BigEndian.AppendUint32(encoded, fields.Purpose)
	encoded = blob(encoded, fields.OperationID[:])
	encoded = blob(encoded, fields.ConsumerPublicKey[:])
	encoded = blob(encoded, fields.ClientNonce[:])
	encoded = blob(encoded, []byte(fields.ChallengeID))
	encoded = blob(encoded, fields.Challenge[:])
	encoded = blob(encoded, fields.NativeSessionBinding[:])
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(fields.ChallengeExpiresAtUnixMS))
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(fields.NativeSessionExpiresAtUnixMS))
	return encoded, nil
}

func validID(value string) bool {
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

func ValidServerOrigin(value string) bool {
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

func ParseNativeBearer(raw string) (ParsedNativeBearer, error) {
	if len(raw) != 47 || !strings.HasPrefix(raw, "cn1.") {
		return ParsedNativeBearer{}, ErrInvalidEvidence
	}
	nonce, ok := canonical32(raw[4:])
	defer clear(nonce[:])
	if !ok {
		return ParsedNativeBearer{}, ErrInvalidEvidence
	}
	encoded := blob(blob(nil, []byte("cognate/native-consumer-token/v1")), nonce[:])
	defer clear(encoded)
	digest := sha256.Sum256(encoded)
	return ParsedNativeBearer{opaque: wrap(digest)}, nil
}

func (bearer ParsedNativeBearer) TokenDigest() ([32]byte, bool) {
	if !bearer.present() {
		return [32]byte{}, false
	}
	return *bearer.payload.value, true
}
