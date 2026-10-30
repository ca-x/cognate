package state

import (
	"bytes"
	"context"
	"crypto/subtle"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
)

type nativeCodexSnapshot struct {
	consumer  nativesessionauth.ConsumerFields
	record    nativeCodexRecord
	challenge [32]byte
	grant     *ent.NativeMaterializationGrant
	phase     nativeapplypreparation.Phase
}

type NativeCodexApplySnapshot struct {
	nativeOpaque[nativeCodexSnapshot]
}

func (snapshot NativeCodexApplySnapshot) Consumer() nativesessionauth.ConsumerFields {
	if !snapshot.present() {
		return nativesessionauth.ConsumerFields{}
	}
	return snapshot.box.value.consumer
}
func (snapshot NativeCodexApplySnapshot) Preparation() nativesessionauth.CodexPreparation {
	if !snapshot.present() {
		return nativesessionauth.CodexPreparation{}
	}
	return snapshot.box.value.record.preparation
}
func (snapshot NativeCodexApplySnapshot) AuthorizeFields(request nativesessionauth.CodexAuthorizeRequest) nativesessionauth.CodexAuthorizeFields {
	if !snapshot.present() {
		return nativesessionauth.CodexAuthorizeFields{}
	}
	return nativesessionauth.CodexAuthorizeFields{Preparation: snapshot.box.value.record.preparation, Local: request.Local, Challenge: snapshot.box.value.challenge, Request: request.AuthorizeRequest}
}
func (snapshot NativeCodexApplySnapshot) ConsumeFields(request nativesessionauth.ConsumeRequest) nativesessionauth.CodexConsumeFields {
	if !snapshot.present() || snapshot.box.value.grant == nil {
		return nativesessionauth.CodexConsumeFields{}
	}
	value := snapshot.box.value
	return nativesessionauth.CodexConsumeFields{Apply: value.record.apply, AuthorizeChallenge: value.challenge, ConsumeChallenge: [32]byte(value.grant.ConsumeChallenge), GrantExpiresMS: value.grant.ExpiresMs, Request: request}
}
func (snapshot NativeCodexApplySnapshot) StatusFields(request nativesessionauth.StatusRequest) nativesessionauth.CodexStatusFields {
	if !snapshot.present() {
		return nativesessionauth.CodexStatusFields{}
	}
	return nativesessionauth.CodexStatusFields{Preparation: snapshot.box.value.record.preparation, Challenge: snapshot.box.value.challenge, Request: request}
}

func (store *Store) nativeCodexSnapshotTx(ctx context.Context, tx *ent.Tx, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (NativeCodexApplySnapshot, func(time.Time) error, error) {
	child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, operation, store.state.now)
	if err != nil {
		return NativeCodexApplySnapshot{}, nil, err
	}
	value := nativeCodexSnapshot{consumer: nativeConsumerFields(child, parent)}
	if prepareID != "" {
		row, record, err := store.nativeLoadCodexPrepared(ctx, tx, child, value.consumer, prepareID)
		if err != nil {
			return NativeCodexApplySnapshot{}, nil, err
		}
		value.record, value.challenge, value.phase = record, [32]byte(row.AuthorizeChallenge), row.Phase
		value.grant, err = row.QueryGrant().Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return NativeCodexApplySnapshot{}, nil, err
		}
		if value.grant != nil && !nativeCodexGrantValid(value.grant, child, record) || record.apply.CopyWire() != nil && value.grant == nil {
			return NativeCodexApplySnapshot{}, nil, ErrNativeUnavailable
		}
	}
	return NativeCodexApplySnapshot{nativeOpaque: nativeWrap(value)}, gate, nil
}
func (store *Store) NativeCodexConfigurationSnapshot(ctx context.Context, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (NativeCodexApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeCodexApplySnapshot, func(time.Time) error, error) {
		return store.nativeCodexSnapshotTx(ctx, tx, credential, origin, operation, prepareID)
	})
}
func (store *Store) PreflightNativeCodexPrepare(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.CodexPrepareRequest, proof nativesessionauth.VerifiedApplyProof) (NativeCodexApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeCodexApplySnapshot, func(time.Time) error, error) {
		snapshot, gate, err := store.nativeCodexSnapshotTx(ctx, tx, credential, origin, request.OperationID, "")
		if err != nil {
			return NativeCodexApplySnapshot{}, nil, err
		}
		digest, err := nativesessionauth.CodexPrepareFieldsDigest(nativesessionauth.CodexPrepareFields{Consumer: snapshot.Consumer(), Request: request})
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "prepare", digest) {
			return NativeCodexApplySnapshot{}, nil, ErrNativeUnauthorized
		}
		return snapshot, gate, nil
	})
}
func (store *Store) PreflightNativeCodexAuthorize(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.CodexAuthorizeRequest, proof nativesessionauth.VerifiedApplyProof) (NativeCodexApplySnapshot, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeCodexApplySnapshot, func(time.Time) error, error) {
		snapshot, identity, err := store.nativeCodexSnapshotTx(ctx, tx, credential, origin, request.OperationID, request.PrepareID)
		if err != nil {
			return NativeCodexApplySnapshot{}, nil, err
		}
		digest, err := nativesessionauth.CodexAuthorizeFieldsDigest(snapshot.AuthorizeFields(request))
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "authorize", digest) {
			return NativeCodexApplySnapshot{}, nil, ErrNativeUnauthorized
		}
		if snapshot.box.value.phase != nativeapplypreparation.PhasePatchReady || snapshot.box.value.grant != nil || snapshot.box.value.record.apply.CopyWire() != nil {
			return NativeCodexApplySnapshot{}, nil, ErrNativePrecondition
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

type nativeCodexConsumePreflight struct {
	snapshot  NativeCodexApplySnapshot
	rejection error
}

func (store *Store) PreflightNativeCodexConsume(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.ConsumeRequest, proof nativesessionauth.VerifiedApplyProof) (NativeCodexApplySnapshot, error) {
	result, err := store.nativeTransaction(ctx, func(tx *ent.Tx) (nativeCodexConsumePreflight, func(time.Time) error, error) {
		snapshot, identity, err := store.nativeCodexSnapshotTx(ctx, tx, credential, origin, request.OperationID, request.PrepareID)
		if err != nil {
			return nativeCodexConsumePreflight{}, nil, err
		}
		digest, err := nativesessionauth.CodexConsumeFieldsDigest(snapshot.ConsumeFields(request))
		tokenHash, present := request.Token.TokenDigest()
		grant := snapshot.box.value.grant
		if err != nil || !present || grant == nil || grant.PublicID != request.GrantID || subtle.ConstantTimeCompare(grant.TokenHash, tokenHash[:]) != 1 || !proof.MatchesApply(store.state.keyID, *credential.box.value, "consume", digest) {
			return nativeCodexConsumePreflight{}, nil, ErrNativeUnauthorized
		}
		if snapshot.box.value.phase != nativeapplypreparation.PhaseBoundWithGrant || grant.ConsumedMs != 0 || grant.Retired || grant.Expired {
			return nativeCodexConsumePreflight{}, nil, ErrNativePrecondition
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
					return nativeCodexConsumePreflight{}, nil, err
				}
				return nativeCodexConsumePreflight{rejection: ErrNativePrecondition}, identity, nil
			}
			return nativeCodexConsumePreflight{}, nil, err
		}
		return nativeCodexConsumePreflight{snapshot: snapshot}, gate, nil
	})
	if err != nil {
		return NativeCodexApplySnapshot{}, err
	}
	return result.snapshot, result.rejection
}

func nativeCodexGrantValid(grant *ent.NativeMaterializationGrant, child *ent.NativeConsumerSession, record nativeCodexRecord) bool {
	apply := record.apply.CopyWire()
	if !nativeGrantRecordValid(grant) || grant.NativeSessionID != child.ID || grant.UserID != child.UserID || apply == nil || grant.IssuedMs < apply.Preparation.PreparedAtUnixMs || grant.ExpiresMs > apply.IntentExpiresAtUnixMs {
		return false
	}
	digest, err := nativesessionauth.HashCodexApply(record.preparation, record.apply.Commitment(), apply.IntentExpiresAtUnixMs)
	return err == nil && subtle.ConstantTimeCompare(grant.BindingDigest, digest[:]) == 1
}

func (store *Store) NativeConfigurationProtocol(ctx context.Context, credential NativeConsumerCredential, origin string, operation [16]byte, prepareID string) (uint32, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (uint32, func(time.Time) error, error) {
		child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, operation, store.state.now)
		if err != nil {
			return 0, nil, err
		}
		if !validPackKey(prepareID) {
			return 0, nil, ErrNativeInvalid
		}
		row, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.PublicIDEQ(prepareID), nativeapplypreparation.NativeSessionIDEQ(child.ID), nativeapplypreparation.UserIDEQ(child.UserID)).Only(ctx)
		if ent.IsNotFound(err) {
			return 0, nil, ErrConfigurationNotFound
		}
		if err != nil {
			return 0, nil, err
		}
		if !nativePreparationRecordValid(row) || len(row.Ciphertext) > 16<<10 {
			return 0, nil, ErrNativeUnavailable
		}
		consumer := nativeConsumerFields(child, parent)
		plaintext, err := store.state.configurationAEAD.Open(nil, nil, row.Ciphertext, nativePreparedAAD(row, consumer))
		defer clear(plaintext)
		if err != nil || len(plaintext) > 16<<10 || len(plaintext) == 0 {
			return 0, nil, ErrNativeUnavailable
		}
		if bytes.HasPrefix(plaintext, []byte("CX2\x00")) {
			if _, _, err := store.nativeLoadCodexPrepared(ctx, tx, child, consumer, prepareID); err != nil {
				return 0, nil, err
			}
			return 2, gate, nil
		}
		if plaintext[0] == '{' {
			if _, _, err := store.nativeLoadPrepared(ctx, tx, child, consumer, prepareID); err != nil {
				return 0, nil, err
			}
			return 1, gate, nil
		}
		return 0, nil, ErrNativeUnavailable
	})
}
