package state

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
)

type nativeGeminiSnapshot struct {
	consumer  nativesessionauth.ConsumerFields
	record    nativeGeminiRecord
	challenge [32]byte
	grant     *ent.NativeMaterializationGrant
	phase     nativeapplypreparation.Phase
}

type NativeGeminiApplySnapshot struct {
	nativeOpaque[nativeGeminiSnapshot]
}

func (snapshot NativeGeminiApplySnapshot) Consumer() nativesessionauth.ConsumerFields {
	if !snapshot.present() {
		return nativesessionauth.ConsumerFields{}
	}
	return snapshot.box.value.consumer
}
func (snapshot NativeGeminiApplySnapshot) Preparation() nativesessionauth.GeminiPreparation {
	if !snapshot.present() {
		return nativesessionauth.GeminiPreparation{}
	}
	return snapshot.box.value.record.preparation
}
func (snapshot NativeGeminiApplySnapshot) AuthorizeFields(request nativesessionauth.GeminiAuthorizeRequest) nativesessionauth.GeminiAuthorizeFields {
	if !snapshot.present() {
		return nativesessionauth.GeminiAuthorizeFields{}
	}
	return nativesessionauth.GeminiAuthorizeFields{Preparation: snapshot.box.value.record.preparation, Local: request.Local, Challenge: snapshot.box.value.challenge, Request: request.AuthorizeRequest}
}
func (snapshot NativeGeminiApplySnapshot) ConsumeFields(request nativesessionauth.ConsumeRequest) nativesessionauth.GeminiConsumeFields {
	if !snapshot.present() || snapshot.box.value.grant == nil {
		return nativesessionauth.GeminiConsumeFields{}
	}
	value := snapshot.box.value
	return nativesessionauth.GeminiConsumeFields{Apply: value.record.apply, AuthorizeChallenge: value.challenge, ConsumeChallenge: [32]byte(value.grant.ConsumeChallenge), GrantExpiresMS: value.grant.ExpiresMs, Request: request}
}
func (snapshot NativeGeminiApplySnapshot) StatusFields(request nativesessionauth.StatusRequest) nativesessionauth.GeminiStatusFields {
	if !snapshot.present() {
		return nativesessionauth.GeminiStatusFields{}
	}
	return nativesessionauth.GeminiStatusFields{Preparation: snapshot.box.value.record.preparation, Challenge: snapshot.box.value.challenge, Request: request}
}

func (store *Store) nativeGeminiSnapshotTx(ctx context.Context, tx *ent.Tx, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (NativeGeminiApplySnapshot, func(time.Time) error, error) {
	child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, operation, store.state.now)
	if err != nil {
		return NativeGeminiApplySnapshot{}, nil, err
	}
	value := nativeGeminiSnapshot{consumer: nativeConsumerFields(child, parent)}
	if prepareID != "" {
		row, record, err := store.nativeLoadGeminiPrepared(ctx, tx, child, value.consumer, prepareID)
		if err != nil {
			return NativeGeminiApplySnapshot{}, nil, err
		}
		value.record, value.challenge, value.phase = record, [32]byte(row.AuthorizeChallenge), row.Phase
		value.grant, err = row.QueryGrant().Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return NativeGeminiApplySnapshot{}, nil, err
		}
		if value.grant != nil && !nativeGeminiGrantValid(value.grant, child, record) || record.apply.CopyWire() != nil && len(record.preparation.CopyWire().RequiredSlots) == 1 && value.grant == nil {
			return NativeGeminiApplySnapshot{}, nil, ErrNativeUnavailable
		}
	}
	return NativeGeminiApplySnapshot{nativeOpaque: nativeWrap(value)}, gate, nil
}
func (store *Store) NativeGeminiConfigurationSnapshot(ctx context.Context, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (NativeGeminiApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGeminiApplySnapshot, func(time.Time) error, error) {
		return store.nativeGeminiSnapshotTx(ctx, tx, credential, origin, operation, prepareID)
	})
}
func (store *Store) PreflightNativeGeminiPrepare(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.GeminiPrepareRequest, proof nativesessionauth.VerifiedApplyProof) (NativeGeminiApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGeminiApplySnapshot, func(time.Time) error, error) {
		snapshot, gate, err := store.nativeGeminiSnapshotTx(ctx, tx, credential, origin, request.OperationID, "")
		if err != nil {
			return NativeGeminiApplySnapshot{}, nil, err
		}
		digest, err := nativesessionauth.GeminiPrepareFieldsDigest(nativesessionauth.GeminiPrepareFields{Consumer: snapshot.Consumer(), Request: request})
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "prepare", digest) {
			return NativeGeminiApplySnapshot{}, nil, ErrNativeUnauthorized
		}
		return snapshot, gate, nil
	})
}
func (store *Store) PreflightNativeGeminiAuthorize(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.GeminiAuthorizeRequest, proof nativesessionauth.VerifiedApplyProof) (NativeGeminiApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGeminiApplySnapshot, func(time.Time) error, error) {
		snapshot, identity, err := store.nativeGeminiSnapshotTx(ctx, tx, credential, origin, request.OperationID, request.PrepareID)
		if err != nil {
			return NativeGeminiApplySnapshot{}, nil, err
		}
		digest, err := nativesessionauth.GeminiAuthorizeFieldsDigest(snapshot.AuthorizeFields(request))
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "authorize", digest) {
			return NativeGeminiApplySnapshot{}, nil, ErrNativeUnauthorized
		}
		if snapshot.box.value.phase != nativeapplypreparation.PhasePatchReady || snapshot.box.value.grant != nil || snapshot.box.value.record.apply.CopyWire() != nil {
			return NativeGeminiApplySnapshot{}, nil, ErrNativePrecondition
		}
		gate := func(instant time.Time) error {
			if err := identity(instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(snapshot.box.value.record.preparation.CopyWire().PreparedAtUnixMs), time.UnixMilli(request.IntentExpiresMS), instant) {
				return ErrNativePrecondition
			}
			return nil
		}
		return snapshot, gate, nil
	})
}

type nativeGeminiConsumePreflight struct {
	snapshot  NativeGeminiApplySnapshot
	rejection error
}

func (store *Store) PreflightNativeGeminiConsume(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.ConsumeRequest, proof nativesessionauth.VerifiedApplyProof) (NativeGeminiApplySnapshot, error) {
	result, err := store.nativeTransaction(ctx, func(tx *ent.Tx) (nativeGeminiConsumePreflight, func(time.Time) error, error) {
		snapshot, identity, err := store.nativeGeminiSnapshotTx(ctx, tx, credential, origin, request.OperationID, request.PrepareID)
		if err != nil {
			return nativeGeminiConsumePreflight{}, nil, err
		}
		digest, err := nativesessionauth.GeminiConsumeFieldsDigest(snapshot.ConsumeFields(request))
		tokenHash, present := request.Token.TokenDigest()
		grant := snapshot.box.value.grant
		if err != nil || !present || grant == nil || grant.PublicID != request.GrantID || subtle.ConstantTimeCompare(grant.TokenHash, tokenHash[:]) != 1 || !proof.MatchesApply(store.state.keyID, *credential.box.value, "consume", digest) {
			return nativeGeminiConsumePreflight{}, nil, ErrNativeUnauthorized
		}
		if snapshot.box.value.phase != nativeapplypreparation.PhaseBoundWithGrant || grant.ConsumedMs != 0 || grant.Retired || grant.Expired {
			return nativeGeminiConsumePreflight{}, nil, ErrNativePrecondition
		}
		apply := snapshot.box.value.record.apply.CopyWire()
		gate := func(instant time.Time) error {
			if err := identity(instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(grant.IssuedMs), time.UnixMilli(grant.ExpiresMs), instant) || !nativeActive(time.UnixMilli(apply.Preparation.PreparedAtUnixMs), time.UnixMilli(apply.IntentExpiresAtUnixMs), instant) {
				return ErrNativePrecondition
			}
			return nil
		}
		instant := store.state.now().UTC()
		if err := gate(instant); err != nil {
			if identity(instant) == nil && instant.UnixMilli() >= grant.ExpiresMs {
				if err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx); err != nil {
					return nativeGeminiConsumePreflight{}, nil, err
				}
				return nativeGeminiConsumePreflight{rejection: ErrNativePrecondition}, identity, nil
			}
			return nativeGeminiConsumePreflight{}, nil, err
		}
		return nativeGeminiConsumePreflight{snapshot: snapshot}, gate, nil
	})
	if err != nil {
		return NativeGeminiApplySnapshot{}, err
	}
	return result.snapshot, result.rejection
}

func nativeGeminiGrantValid(grant *ent.NativeMaterializationGrant, child *ent.NativeConsumerSession, record nativeGeminiRecord) bool {
	apply := record.apply.CopyWire()
	if !nativeGrantRecordValid(grant) || grant.NativeSessionID != child.ID || grant.UserID != child.UserID || apply == nil || grant.IssuedMs < apply.Preparation.PreparedAtUnixMs || grant.ExpiresMs > apply.IntentExpiresAtUnixMs {
		return false
	}
	digest, err := nativesessionauth.HashGeminiApply(record.preparation, record.apply.Commitment(), apply.IntentExpiresAtUnixMs)
	return err == nil && subtle.ConstantTimeCompare(grant.BindingDigest, digest[:]) == 1
}
