package state

import (
	"bytes"
	"context"
	"crypto/subtle"
	"math"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumerchallenge"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumersession"
)

type nativeAuthSnapshot struct {
	Challenge  nativeChallengeSnapshot
	ParentHash [32]byte
	KeyID      string
}

type nativeApplySnapshot struct {
	consumer  nativesessionauth.ConsumerFields
	prepared  nativePreparedRecord
	challenge [32]byte
	grant     *ent.NativeMaterializationGrant
	phase     nativeapplypreparation.Phase
}

type NativeApplySnapshot struct {
	nativeOpaque[nativeApplySnapshot]
}

func (snapshot NativeApplySnapshot) Consumer() nativesessionauth.ConsumerFields {
	if !snapshot.present() {
		return nativesessionauth.ConsumerFields{}
	}
	return snapshot.box.value.consumer
}

func nativeConsumerGate(ctx context.Context, tx *ent.Tx, credential NativeConsumerCredential, origin string, operation [16]byte, clock func() time.Time) (*ent.NativeConsumerSession, *ent.Session, func(time.Time) error, error) {
	if !credential.present() {
		return nil, nil, nil, ErrNativeUnauthorized
	}
	count, err := tx.NativeConsumerSession.Update().Where(nativeconsumersession.TokenHashEQ(credential.box.value[:]), nativeconsumersession.OriginEQ(origin), nativeconsumersession.OperationIDEQ(operation[:]), nativeconsumersession.PurposeEQ(nativeconsumersession.PurposeConfigurationApply), nativeconsumersession.GateVersionLT(math.MaxInt64)).AddGateVersion(1).Save(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if count != 1 {
		return nil, nil, nil, ErrNativeUnauthorized
	}
	child, err := tx.NativeConsumerSession.Query().Where(nativeconsumersession.TokenHashEQ(credential.box.value[:])).WithParentSession(func(query *ent.SessionQuery) { query.WithUser() }).Only(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	parent := child.Edges.ParentSession
	if !nativeSessionRecordValid(child) {
		return nil, nil, nil, ErrNativeUnavailable
	}
	if parent == nil || parent.Edges.User == nil || child.UserID != parent.UserID || !bytes.Equal(child.TokenHash, credential.box.value[:]) {
		return nil, nil, nil, ErrNativeUnauthorized
	}
	gate := func(instant time.Time) error {
		if err := nativeParentGate(parent, instant); err != nil {
			return err
		}
		if !nativeActive(time.UnixMilli(child.IssuedMs), time.UnixMilli(child.ExpiresMs), instant) {
			return ErrNativeUnauthorized
		}
		return nil
	}
	return child, parent, gate, gate(clock().UTC())
}

func nativeConsumerFields(child *ent.NativeConsumerSession, parent *ent.Session) nativesessionauth.ConsumerFields {
	return nativesessionauth.ConsumerFields{OwnerID: parent.Edges.User.UserKey, ServerOrigin: child.Origin, SessionBinding: [32]byte(child.SessionBinding), OperationID: [16]byte(child.OperationID), PublicKey: [32]byte(child.PublicKey)}
}

func (store *Store) NativeConfigurationSnapshot(ctx context.Context, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (NativeApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeApplySnapshot, func(time.Time) error, error) {
		return store.nativeConfigurationSnapshotTx(ctx, tx, credential, origin, operation, prepareID)
	})
}

func (store *Store) nativeConfigurationSnapshotTx(ctx context.Context, tx *ent.Tx, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (NativeApplySnapshot, func(time.Time) error, error) {
	child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, operation, store.state.now)
	if err != nil {
		return NativeApplySnapshot{}, nil, err
	}
	value := nativeApplySnapshot{consumer: nativeConsumerFields(child, parent)}
	if prepareID != "" {
		row, record, err := store.nativeLoadPrepared(ctx, tx, child, value.consumer, prepareID)
		if err != nil {
			return NativeApplySnapshot{}, nil, err
		}
		value.prepared, value.challenge, value.phase = record, [32]byte(row.AuthorizeChallenge), row.Phase
		value.grant, err = row.QueryGrant().Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return NativeApplySnapshot{}, nil, err
		}
		if value.grant != nil && !nativePreparedGrantValid(value.grant, child, record) {
			return NativeApplySnapshot{}, nil, ErrNativeUnavailable
		}
	}
	return NativeApplySnapshot{nativeOpaque: nativeWrap(value)}, gate, nil
}

func (store *Store) PreflightNativeConfigurationPrepare(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.PrepareRequest, proof nativesessionauth.VerifiedApplyProof) (NativeApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeApplySnapshot, func(time.Time) error, error) {
		snapshot, gate, err := store.nativeConfigurationSnapshotTx(ctx, tx, credential, origin, request.OperationID, "")
		if err != nil {
			return NativeApplySnapshot{}, nil, err
		}
		digest, err := nativesessionauth.PrepareFieldsDigest(nativesessionauth.PrepareFields{Consumer: snapshot.Consumer(), Request: request})
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "prepare", digest) {
			return NativeApplySnapshot{}, nil, ErrNativeUnauthorized
		}
		return snapshot, gate, nil
	})
}

func (store *Store) PreflightNativeConfigurationAuthorize(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.AuthorizeRequest, proof nativesessionauth.VerifiedApplyProof) (NativeApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeApplySnapshot, func(time.Time) error, error) {
		snapshot, identityGate, err := store.nativeConfigurationSnapshotTx(ctx, tx, credential, origin, request.OperationID, request.PrepareID)
		if err != nil {
			return NativeApplySnapshot{}, nil, err
		}
		digest, err := nativesessionauth.AuthorizeFieldsDigest(snapshot.AuthorizeFields(request))
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "authorize", digest) {
			return NativeApplySnapshot{}, nil, ErrNativeUnauthorized
		}
		if snapshot.box.value.phase != nativeapplypreparation.PhasePatchReady || snapshot.box.value.prepared.Apply != nil || snapshot.box.value.grant != nil {
			return NativeApplySnapshot{}, nil, ErrNativePrecondition
		}
		gate := func(instant time.Time) error {
			if err := identityGate(instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(snapshot.box.value.prepared.Preparation.PreparedMS), time.UnixMilli(request.IntentExpiresMS), instant) {
				return ErrNativePrecondition
			}
			return nil
		}
		return snapshot, gate, nil
	})
}

type nativeConsumePreflight struct {
	snapshot  NativeApplySnapshot
	rejection error
}

func (store *Store) PreflightNativeConfigurationConsume(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.ConsumeRequest, proof nativesessionauth.VerifiedApplyProof) (NativeApplySnapshot, error) {
	result, err := store.nativeTransaction(ctx, func(tx *ent.Tx) (nativeConsumePreflight, func(time.Time) error, error) {
		snapshot, identityGate, err := store.nativeConfigurationSnapshotTx(ctx, tx, credential, origin, request.OperationID, request.PrepareID)
		if err != nil {
			return nativeConsumePreflight{}, nil, err
		}
		digest, err := nativesessionauth.ConsumeFieldsDigest(snapshot.ConsumeFields(request))
		tokenHash, present := request.Token.TokenDigest()
		grant := snapshot.box.value.grant
		if err != nil || !present || grant == nil || grant.PublicID != request.GrantID || subtle.ConstantTimeCompare(grant.TokenHash, tokenHash[:]) != 1 || !proof.MatchesApply(store.state.keyID, *credential.box.value, "consume", digest) {
			return nativeConsumePreflight{}, nil, ErrNativeUnauthorized
		}
		apply := snapshot.box.value.prepared.Apply
		bindingDigest, err := nativesessionauth.HashApply(*apply)
		if err != nil || subtle.ConstantTimeCompare(grant.BindingDigest, bindingDigest[:]) != 1 {
			return nativeConsumePreflight{}, nil, ErrNativeUnauthorized
		}
		if snapshot.box.value.phase != nativeapplypreparation.PhaseBoundWithGrant || grant.ConsumedMs != 0 || grant.Retired || grant.Expired {
			return nativeConsumePreflight{}, nil, ErrNativePrecondition
		}
		gate := func(instant time.Time) error {
			if err := identityGate(instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(grant.IssuedMs), time.UnixMilli(grant.ExpiresMs), instant) || !nativeActive(time.UnixMilli(apply.Preparation.PreparedMS), time.UnixMilli(apply.IntentExpiresMS), instant) {
				return ErrNativePrecondition
			}
			return nil
		}
		instant := store.state.now().UTC()
		if err := gate(instant); err != nil {
			if identityGate(instant) == nil && instant.UnixMilli() >= grant.ExpiresMs {
				if err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx); err != nil {
					return nativeConsumePreflight{}, nil, err
				}
				return nativeConsumePreflight{rejection: ErrNativePrecondition}, identityGate, nil
			}
			return nativeConsumePreflight{}, nil, err
		}
		return nativeConsumePreflight{snapshot: snapshot}, gate, nil
	})
	if err != nil {
		return NativeApplySnapshot{}, err
	}
	return result.snapshot, result.rejection
}

func (snapshot NativeApplySnapshot) AuthorizeFields(request nativesessionauth.AuthorizeRequest) nativesessionauth.AuthorizeFields {
	if !snapshot.present() {
		return nativesessionauth.AuthorizeFields{}
	}
	return nativesessionauth.AuthorizeFields{Preparation: snapshot.box.value.prepared.Preparation, Challenge: snapshot.box.value.challenge, Request: request}
}

func (snapshot NativeApplySnapshot) ConsumeFields(request nativesessionauth.ConsumeRequest) nativesessionauth.ConsumeFields {
	if !snapshot.present() || snapshot.box.value.grant == nil || snapshot.box.value.prepared.Apply == nil {
		return nativesessionauth.ConsumeFields{}
	}
	value := snapshot.box.value
	return nativesessionauth.ConsumeFields{Apply: *value.prepared.Apply, AuthorizeChallenge: value.challenge, ConsumeChallenge: [32]byte(value.grant.ConsumeChallenge), GrantExpiresMS: value.grant.ExpiresMs, Request: request}
}

func (snapshot NativeApplySnapshot) StatusFields(request nativesessionauth.StatusRequest) nativesessionauth.StatusFields {
	if !snapshot.present() {
		return nativesessionauth.StatusFields{}
	}
	return nativesessionauth.StatusFields{Preparation: snapshot.box.value.prepared.Preparation, Challenge: snapshot.box.value.challenge, Request: request}
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
