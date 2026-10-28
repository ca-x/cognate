package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

type nativeAuthBox[Value any] struct{ value *Value }
type nativeAuthOpaque[Value any] struct{ box *nativeAuthBox[Value] }

func nativeAuthWrap[Value any](value Value) nativeAuthOpaque[Value] {
	return nativeAuthOpaque[Value]{box: &nativeAuthBox[Value]{value: new(value)}}
}

func (nativeAuthOpaque[Value]) String() string   { return "[redacted native credential]" }
func (nativeAuthOpaque[Value]) GoString() string { return "[redacted native credential]" }
func (nativeAuthOpaque[Value]) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted native credential]")
}
func (nativeAuthOpaque[Value]) LogValue() slog.Value {
	return slog.StringValue("[redacted native credential]")
}
func (nativeAuthOpaque[Value]) MarshalText() ([]byte, error) {
	return []byte("[redacted native credential]"), nil
}
func (nativeAuthOpaque[Value]) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted native credential]"`), nil
}
func (value nativeAuthOpaque[Value]) present() bool {
	return value.box != nil && value.box.value != nil
}

type nativeConsumerService struct {
	owner    *Service
	verifier *nativesessionauth.Verifier
	origin   string
	bind     func(context.Context, state.OwnerSessionCredential, state.NativeConsumerBind) (state.NativeConsumerIdentity, error)
}

type NativeConsumerService struct {
	nativeAuthOpaque[nativeConsumerService]
}

type issuedNativeConsumer struct {
	raw       string
	identity  state.NativeConsumerIdentity
	expiresAt time.Time
}

type IssuedNativeConsumer struct {
	nativeAuthOpaque[issuedNativeConsumer]
}

func (token IssuedNativeConsumer) Reveal() string {
	if !token.present() {
		return ""
	}
	return token.box.value.raw
}

func (token IssuedNativeConsumer) Identity() state.NativeConsumerIdentity {
	if !token.present() {
		return state.NativeConsumerIdentity{}
	}
	return token.box.value.identity
}

func (token IssuedNativeConsumer) ExpiresAt() time.Time {
	if !token.present() {
		return time.Time{}
	}
	return token.box.value.expiresAt
}

func NewNativeConsumerService(owner *Service, masterKey []byte, trustedOrigin string) (*NativeConsumerService, error) {
	if owner == nil || !owner.store.MatchesKey(masterKey) || !strings.HasPrefix(trustedOrigin, "https://") || !nativesessionauth.ValidServerOrigin(trustedOrigin) {
		return nil, state.ErrNativeInvalid
	}
	verifier, err := nativesessionauth.NewVerifier(masterKey)
	if err != nil {
		return nil, state.ErrNativeInvalid
	}
	return &NativeConsumerService{nativeAuthOpaque: nativeAuthWrap(nativeConsumerService{owner: owner, verifier: verifier, origin: trustedOrigin, bind: owner.store.BindNativeConsumer})}, nil
}

func (service *NativeConsumerService) parent(ctx context.Context, raw string) (state.OwnerSessionCredential, nativesessionauth.VerifiedOwnerSession, error) {
	if err := ctx.Err(); err != nil {
		return state.OwnerSessionCredential{}, nativesessionauth.VerifiedOwnerSession{}, err
	}
	if service == nil || !service.present() {
		return state.OwnerSessionCredential{}, nativesessionauth.VerifiedOwnerSession{}, state.ErrNativeUnauthorized
	}
	config := service.box.value
	legacy, err := config.owner.sessionDigest(raw)
	if err != nil {
		return state.OwnerSessionCredential{}, nativesessionauth.VerifiedOwnerSession{}, state.ErrNativeUnauthorized
	}
	evidence, err := config.verifier.VerifyOwner(raw)
	digest, present := evidence.TokenDigest()
	if err != nil || !present || subtle.ConstantTimeCompare(legacy[:], digest[:]) != 1 {
		return state.OwnerSessionCredential{}, nativesessionauth.VerifiedOwnerSession{}, state.ErrNativeUnauthorized
	}
	credential, err := config.owner.store.OwnerCredentialFromVerified(evidence)
	return credential, evidence, err
}

func (service *NativeConsumerService) Begin(ctx context.Context, ownerBearer string, start state.NativeConsumerStart) (state.NativeBindSnapshot, error) {
	parent, _, err := service.parent(ctx, ownerBearer)
	if err != nil {
		return state.NativeBindSnapshot{}, err
	}
	config := service.box.value
	if start.Origin != config.origin {
		return state.NativeBindSnapshot{}, state.ErrNativeInvalid
	}
	challenge, err := config.owner.store.BeginNativeConsumer(ctx, parent, start)
	if err != nil {
		return state.NativeBindSnapshot{}, err
	}
	return config.owner.store.NativeBindChallenge(ctx, parent, challenge.ID())
}

func (service *NativeConsumerService) Bind(ctx context.Context, ownerBearer, challengeID string, signature []byte) (IssuedNativeConsumer, error) {
	parent, evidence, err := service.parent(ctx, ownerBearer)
	if err != nil {
		return IssuedNativeConsumer{}, err
	}
	if len(signature) != ed25519.SignatureSize {
		return IssuedNativeConsumer{}, state.ErrNativeUnauthorized
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeBindChallenge(ctx, parent, challengeID)
	if err != nil {
		return IssuedNativeConsumer{}, err
	}
	fields, err := snapshot.BindFields()
	if err != nil {
		return IssuedNativeConsumer{}, err
	}
	if fields.ServerOrigin != config.origin {
		return IssuedNativeConsumer{}, state.ErrNativeUnauthorized
	}
	verified, err := config.verifier.VerifyBind(evidence, fields, signature)
	if err != nil {
		return IssuedNativeConsumer{}, state.ErrNativeUnauthorized
	}
	proof, err := config.owner.store.BindingProofFromVerified(parent, snapshot, verified)
	if err != nil {
		return IssuedNativeConsumer{}, err
	}
	var nonce [32]byte
	defer clear(nonce[:])
	if _, err := rand.Read(nonce[:]); err != nil {
		return IssuedNativeConsumer{}, state.ErrNativeUnavailable
	}
	raw := "cn1." + base64.RawURLEncoding.EncodeToString(nonce[:])
	parsed, err := nativesessionauth.ParseNativeBearer(raw)
	if err != nil {
		return IssuedNativeConsumer{}, state.ErrNativeUnavailable
	}
	credential, err := config.owner.store.NativeCredentialFromBearer(parsed)
	if err != nil {
		return IssuedNativeConsumer{}, err
	}
	identity, err := config.bind(ctx, parent, state.NativeConsumerBind{ChallengeID: challengeID, TokenHash: credential, Proof: proof})
	if err != nil {
		return IssuedNativeConsumer{}, nativeBindFailure(err)
	}
	if identity.ID() == "" {
		return IssuedNativeConsumer{}, state.ErrNativeUnavailable
	}
	issued := IssuedNativeConsumer{nativeAuthOpaque: nativeAuthWrap(issuedNativeConsumer{raw: raw, identity: identity, expiresAt: time.UnixMilli(fields.NativeSessionExpiresAtUnixMS)})}
	if err := ctx.Err(); err != nil {
		return IssuedNativeConsumer{}, err
	}
	return issued, nil
}

func nativeBindFailure(err error) error {
	var cancellation error
	for _, known := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			cancellation = known
			break
		}
	}
	if errors.Is(err, state.ErrNativeOutcomeUnknown) {
		return errors.Join(state.ErrNativeOutcomeUnknown, cancellation)
	}
	if cancellation != nil {
		return cancellation
	}
	for _, known := range []error{state.ErrNativeUnauthorized, state.ErrNativeInvalid, state.ErrNativeQuota, state.ErrNativeUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return state.ErrNativeUnavailable
}

func (service *NativeConsumerService) Authenticate(ctx context.Context, nativeBearer string, operationID []byte) (state.NativeConsumerIdentity, error) {
	if err := ctx.Err(); err != nil {
		return state.NativeConsumerIdentity{}, err
	}
	if service == nil || !service.present() {
		return state.NativeConsumerIdentity{}, state.ErrNativeUnauthorized
	}
	parsed, err := nativesessionauth.ParseNativeBearer(nativeBearer)
	if err != nil {
		return state.NativeConsumerIdentity{}, state.ErrNativeUnauthorized
	}
	config := service.box.value
	credential, err := config.owner.store.NativeCredentialFromBearer(parsed)
	if err != nil {
		return state.NativeConsumerIdentity{}, err
	}
	return config.owner.store.AuthenticateNativeConsumer(ctx, credential, config.origin, operationID)
}
