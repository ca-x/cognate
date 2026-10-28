package state

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumerchallenge"
)

type nativeAuthSnapshot struct {
	Challenge  nativeChallengeSnapshot
	ParentHash [32]byte
	KeyID      string
}

type NativeBindSnapshot struct {
	nativeOpaque[nativeAuthSnapshot]
}

func (store *Store) OwnerCredentialFromVerified(evidence nativesessionauth.VerifiedOwnerSession) (OwnerSessionCredential, error) {
	if store == nil || store.state == nil || !evidence.MatchesKeyID(store.state.keyID) {
		return OwnerSessionCredential{}, ErrNativeUnauthorized
	}
	digest, present := evidence.TokenDigest()
	if !present {
		return OwnerSessionCredential{}, ErrNativeUnauthorized
	}
	return OwnerSessionCredential{nativeOpaque: nativeWrap(digest)}, nil
}

func (store *Store) NativeCredentialFromBearer(bearer nativesessionauth.ParsedNativeBearer) (NativeConsumerCredential, error) {
	digest, present := bearer.TokenDigest()
	if store == nil || store.state == nil || !present {
		return NativeConsumerCredential{}, ErrNativeUnauthorized
	}
	return NativeConsumerCredential{nativeOpaque: nativeWrap(digest)}, nil
}

func (store *Store) NativeBindChallenge(ctx context.Context, parentCredential OwnerSessionCredential, challengeID string) (NativeBindSnapshot, error) {
	if store == nil || store.state == nil || !parentCredential.present() {
		return NativeBindSnapshot{}, ErrNativeUnauthorized
	}
	if !validPackKey(challengeID) {
		return NativeBindSnapshot{}, ErrNativeInvalid
	}
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeBindSnapshot, func(time.Time) error, error) {
		now := store.state.now().UTC()
		parent, err := nativeParent(ctx, tx, parentCredential, now)
		if err != nil {
			return NativeBindSnapshot{}, nil, err
		}
		challenge, err := tx.NativeConsumerChallenge.Query().Where(
			nativeconsumerchallenge.PublicIDEQ(challengeID),
			nativeconsumerchallenge.ParentSessionIDEQ(parent.ID),
			nativeconsumerchallenge.UserIDEQ(parent.UserID),
		).Only(ctx)
		if err != nil {
			return NativeBindSnapshot{}, nil, err
		}
		if !nativeChallengeRecordValid(challenge) {
			return NativeBindSnapshot{}, nil, ErrNativeUnavailable
		}
		gate := func(instant time.Time) error {
			if err := nativeParentGate(parent, instant); err != nil {
				return err
			}
			if challenge.Used || !nativeActive(time.UnixMilli(challenge.IssuedMs), time.UnixMilli(challenge.ExpiresMs), instant) {
				return ErrNativeUnauthorized
			}
			return nil
		}
		if err := gate(now); err != nil {
			return NativeBindSnapshot{}, nil, err
		}
		snapshot := nativeAuthSnapshot{Challenge: nativeSnapshot(challenge, parent.Edges.User.UserKey), ParentHash: *parentCredential.box.value, KeyID: store.state.keyID}
		result := NativeBindSnapshot{nativeOpaque: nativeWrap(snapshot)}
		if _, err := result.BindFields(); err != nil {
			return NativeBindSnapshot{}, nil, err
		}
		return result, gate, nil
	})
}

func (snapshot NativeBindSnapshot) BindFields() (nativesessionauth.BindFields, error) {
	if !snapshot.present() {
		return nativesessionauth.BindFields{}, ErrNativeUnauthorized
	}
	challenge := snapshot.box.value.Challenge
	if challenge.Purpose != NativeConfigurationApply {
		return nativesessionauth.BindFields{}, ErrNativeUnavailable
	}
	fields := nativesessionauth.BindFields{
		ServerOrigin: challenge.Origin, OwnerID: challenge.Owner, Purpose: nativesessionauth.PurposeConfigurationApply,
		OperationID: challenge.OperationID, ConsumerPublicKey: challenge.PublicKey, ClientNonce: challenge.ClientNonce,
		ChallengeID: challenge.ID, Challenge: challenge.Challenge, NativeSessionBinding: challenge.Binding,
		ChallengeExpiresAtUnixMS: challenge.ExpiresMS, NativeSessionExpiresAtUnixMS: challenge.NativeExpiresMS,
	}
	if _, err := nativesessionauth.BindFieldsDigest(fields); err != nil {
		return nativesessionauth.BindFields{}, ErrNativeUnavailable
	}
	return fields, nil
}

func (store *Store) BindingProofFromVerified(parent OwnerSessionCredential, snapshot NativeBindSnapshot, proof nativesessionauth.VerifiedBindProof) (NativeConsumerBindingProof, error) {
	if store == nil || store.state == nil || !parent.present() || !snapshot.present() ||
		subtle.ConstantTimeCompare([]byte(snapshot.box.value.KeyID), []byte(store.state.keyID)) != 1 ||
		subtle.ConstantTimeCompare(snapshot.box.value.ParentHash[:], parent.box.value[:]) != 1 {
		return NativeConsumerBindingProof{}, ErrNativeUnauthorized
	}
	fields, err := snapshot.BindFields()
	if err != nil {
		return NativeConsumerBindingProof{}, err
	}
	digest, err := nativesessionauth.BindFieldsDigest(fields)
	if err != nil || !proof.MatchesBinding(store.state.keyID, *parent.box.value, digest) {
		return NativeConsumerBindingProof{}, ErrNativeUnauthorized
	}
	return NativeConsumerBindingProof{nativeOpaque: nativeWrap(nativeBindEvidence{ParentHash: *parent.box.value, Challenge: snapshot.box.value.Challenge})}, nil
}
