package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"github.com/ca-x/cognate/internal/server/ent/nativematerializationgrant"
)

var ErrNativePrecondition = errors.New("native operation precondition failed")

type NativePreparedData struct {
	Adapter      nativesessionauth.AdapterFields
	Patch        nativesessionauth.PatchFields
	RequiredSlot uint32
}

type NativePrepareValidator func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativePreparedData, error)

type nativePreparedRecord struct {
	Preparation nativesessionauth.PreparationFields
	Patch       nativesessionauth.PatchFields
	Apply       *nativesessionauth.ApplyFields
	Consent     *nativesessionauth.ConsentFields
}

type nativePreparedValue struct {
	record    nativePreparedRecord
	challenge [32]byte
}

type NativePrepared struct {
	nativeOpaque[nativePreparedValue]
}

func (prepared NativePrepared) Binding() nativesessionauth.PreparationFields {
	if !prepared.present() {
		return nativesessionauth.PreparationFields{}
	}
	return prepared.box.value.record.Preparation
}

func (prepared NativePrepared) Patch() nativesessionauth.PatchFields {
	if !prepared.present() {
		return nativesessionauth.PatchFields{}
	}
	return prepared.box.value.record.Patch
}

func (prepared NativePrepared) Challenge() [32]byte {
	if !prepared.present() {
		return [32]byte{}
	}
	return prepared.box.value.challenge
}

func nativePreparedAAD(row *ent.NativeApplyPreparation, consumer nativesessionauth.ConsumerFields) []byte {
	encoded := []byte("cognate/native-apply-preparation/v1")
	for _, value := range []string{consumer.OwnerID, consumer.ServerOrigin, row.PublicID, string(row.Phase)} {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(value)))
		encoded = append(encoded, value...)
	}
	encoded = append(encoded, consumer.SessionBinding[:]...)
	return append(encoded, consumer.OperationID[:]...)
}

func (store *Store) nativeSealPrepared(row *ent.NativeApplyPreparation, consumer nativesessionauth.ConsumerFields, record nativePreparedRecord) error {
	plaintext, err := json.Marshal(record)
	defer clear(plaintext)
	if err != nil || len(plaintext) > 16<<10 {
		return ErrNativeQuota
	}
	row.Ciphertext = store.state.configurationAEAD.Seal(nil, nil, plaintext, nativePreparedAAD(row, consumer))
	row.Charge = nativePreparationCharge(row)
	if row.Charge > 16<<10 {
		return ErrNativeQuota
	}
	return nil
}

func (store *Store) nativeLoadPrepared(ctx context.Context, tx *ent.Tx, child *ent.NativeConsumerSession, consumer nativesessionauth.ConsumerFields, id string) (*ent.NativeApplyPreparation, nativePreparedRecord, error) {
	var record nativePreparedRecord
	if !validPackKey(id) {
		return nil, record, ErrNativeInvalid
	}
	row, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.PublicIDEQ(id), nativeapplypreparation.NativeSessionIDEQ(child.ID), nativeapplypreparation.UserIDEQ(child.UserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, record, ErrConfigurationNotFound
	}
	if err != nil {
		return nil, record, err
	}
	if !nativePreparationRecordValid(row) {
		return nil, record, ErrNativeUnavailable
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, row.Ciphertext, nativePreparedAAD(row, consumer))
	defer clear(plaintext)
	if err != nil || json.Unmarshal(plaintext, &record, json.RejectUnknownMembers(true)) != nil {
		return nil, record, ErrNativeUnavailable
	}
	patchHash, err := nativesessionauth.HashPatch(record.Patch)
	if _, bindingErr := nativesessionauth.HashPreparation(record.Preparation); err != nil || bindingErr != nil || patchHash != record.Preparation.PatchSHA256 || record.Preparation.Consumer != consumer || record.Preparation.PrepareID != row.PublicID || record.Preparation.PreparedMS != row.IssuedMs || record.Preparation.ExpiresMS != row.ExpiresMs || row.ExpiresMs > child.ExpiresMs || !bytes.Equal(row.OperationID, child.OperationID) {
		return nil, record, ErrNativeUnavailable
	}
	requestDigest, digestErr := nativesessionauth.PrepareFieldsDigest(nativesessionauth.PrepareFields{Consumer: consumer, Request: record.Preparation.Request})
	if digestErr != nil || subtle.ConstantTimeCompare(row.RequestDigest, requestDigest[:]) != 1 {
		return nil, record, ErrNativeUnavailable
	}
	if record.Apply != nil && (record.Apply.Preparation != record.Preparation || record.Consent == nil) || record.Apply == nil && record.Consent != nil {
		return nil, record, ErrNativeUnavailable
	}
	if row.Phase == nativeapplypreparation.PhasePatchReady && record.Apply != nil || (row.Phase == nativeapplypreparation.PhaseBoundWithGrant || row.Phase == nativeapplypreparation.PhaseBoundNoSecret) && record.Apply == nil {
		return nil, record, ErrNativeUnavailable
	}
	if record.Apply != nil {
		if _, err := nativesessionauth.HashApply(*record.Apply); err != nil {
			return nil, record, ErrNativeUnavailable
		}
	}
	return row, record, nil
}

func (store *Store) readConfigurationAssetVersionTx(ctx context.Context, tx *ent.Tx, owner, id string) (ConfigurationAsset, ConfigurationPayload, error) {
	entry, err := loadConfigurationAsset(ctx, tx.Client(), owner, id, false)
	if err != nil {
		return ConfigurationAsset{}, ConfigurationPayload{}, err
	}
	if len(entry.Ciphertext) < store.state.configurationAEAD.Overhead()+1 || len(entry.Ciphertext) > MaxConfigurationPayloadBytes+store.state.configurationAEAD.Overhead() {
		return ConfigurationAsset{}, ConfigurationPayload{}, ErrConfigurationUnavailable
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, entry.Ciphertext, configurationAAD(owner, entry))
	if err != nil {
		return ConfigurationAsset{}, ConfigurationPayload{}, ErrConfigurationUnavailable
	}
	return configurationMetadata(entry), ConfigurationPayload{data: plaintext}, nil
}

func nativeCheckAsset(metadata ConfigurationAsset, request nativesessionauth.PrepareRequest) error {
	if metadata.Revision != request.AssetRevision {
		return ErrConfigurationConflict
	}
	if metadata.ID != request.AssetID || metadata.AgentID != request.AgentID || metadata.SchemaVersion != int(request.SchemaVersion) || metadata.Kind != ConfigurationKindProvider {
		return ErrNativePrecondition
	}
	return nil
}

func (store *Store) PrepareNativeConfigurationApply(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.PrepareRequest, proof nativesessionauth.VerifiedApplyProof, validate NativePrepareValidator) (NativePrepared, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return NativePrepared{}, admissionError
	}
	defer releaseWriter()
	if validate == nil || !credential.present() {
		return NativePrepared{}, ErrNativeUnauthorized
	}
	id := rand.Text()
	var challenge [32]byte
	_, _ = rand.Read(challenge[:])
	run := func() (NativePrepared, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativePrepared, func(time.Time) error, error) {
			child, parent, identityGate, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
			if err != nil {
				return NativePrepared{}, nil, err
			}
			consumer := nativeConsumerFields(child, parent)
			digest, err := nativesessionauth.PrepareFieldsDigest(nativesessionauth.PrepareFields{Consumer: consumer, Request: request})
			if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "prepare", digest) {
				return NativePrepared{}, nil, ErrNativeUnauthorized
			}
			previous, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.NativeSessionIDEQ(child.ID)).Only(ctx)
			if err == nil {
				row, record, err := store.nativeLoadPrepared(ctx, tx, child, consumer, previous.PublicID)
				if err != nil {
					return NativePrepared{}, nil, err
				}
				if subtle.ConstantTimeCompare(row.RequestDigest, digest[:]) != 1 {
					return NativePrepared{}, nil, ErrConfigurationConflict
				}
				return NativePrepared{nativeOpaque: nativeWrap(nativePreparedValue{record: record, challenge: [32]byte(row.AuthorizeChallenge)})}, identityGate, nil
			}
			if !ent.IsNotFound(err) {
				return NativePrepared{}, nil, err
			}
			metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, consumer.OwnerID, request.AssetID)
			if err != nil {
				return NativePrepared{}, nil, err
			}
			defer clear(payload.data)
			if err := nativeCheckAsset(metadata, request); err != nil {
				return NativePrepared{}, nil, err
			}
			data, err := validate(ctx, metadata, payload)
			if err != nil {
				return NativePrepared{}, nil, err
			}
			patchHash, err := nativesessionauth.HashPatch(data.Patch)
			if err != nil || data.Adapter != request.Adapter || data.RequiredSlot > 2 || data.RequiredSlot == 0 && (data.Patch[0].Action != 3 || data.Patch[1].Action != 3) || data.RequiredSlot != 0 && data.Patch[data.RequiredSlot-1].SecretSlot != data.RequiredSlot {
				return NativePrepared{}, nil, ErrNativePrecondition
			}
			now := store.state.now().UTC()
			record := nativePreparedRecord{Preparation: nativesessionauth.PreparationFields{Consumer: consumer, Request: request, RequiredSlot: data.RequiredSlot, PrepareID: id, PreparedMS: now.UnixMilli(), ExpiresMS: child.ExpiresMs, PatchSHA256: patchHash}, Patch: data.Patch}
			row := &ent.NativeApplyPreparation{PublicID: id, UserID: child.UserID, NativeSessionID: child.ID, OperationID: bytes.Clone(child.OperationID), RequestDigest: digest[:], AuthorizeChallenge: challenge[:], Phase: nativeapplypreparation.PhasePatchReady, IssuedMs: now.UnixMilli(), ExpiresMs: child.ExpiresMs}
			if err := store.nativeSealPrepared(row, consumer, record); err != nil {
				return NativePrepared{}, nil, err
			}
			if err := nativeCheckQuota(ctx, tx, nativeUsageRow{2, child.ParentSessionID, child.UserID, row.Charge}, nativeDefaultBudget()); err != nil {
				return NativePrepared{}, nil, err
			}
			_, err = tx.NativeApplyPreparation.Create().SetPublicID(row.PublicID).SetUserID(row.UserID).SetNativeSessionID(row.NativeSessionID).SetOperationID(row.OperationID).SetRequestDigest(row.RequestDigest).SetAuthorizeChallenge(row.AuthorizeChallenge).SetCiphertext(row.Ciphertext).SetPhase(row.Phase).SetIssuedMs(row.IssuedMs).SetExpiresMs(row.ExpiresMs).SetCharge(row.Charge).Save(ctx)
			gate := func(instant time.Time) error {
				if err := identityGate(instant); err != nil {
					return err
				}
				if !nativeActive(now, time.UnixMilli(row.ExpiresMs), instant) {
					return ErrNativePrecondition
				}
				return nil
			}
			return NativePrepared{nativeOpaque: nativeWrap(nativePreparedValue{record: record, challenge: challenge})}, gate, err
		})
	}
	value, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativePrepared{}, pruneErr
		}
		return run()
	}
	return value, err
}

type nativeAuthorization struct {
	apply     nativesessionauth.ApplyFields
	grantID   string
	challenge [32]byte
	expiresMS int64
}

type NativeAuthorization struct {
	nativeOpaque[nativeAuthorization]
}

func (value NativeAuthorization) Binding() nativesessionauth.ApplyFields {
	if !value.present() {
		return nativesessionauth.ApplyFields{}
	}
	return value.box.value.apply
}
func (value NativeAuthorization) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.grantID
}
func (value NativeAuthorization) Challenge() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.box.value.challenge
}
func (value NativeAuthorization) ExpiresMS() int64 {
	if !value.present() {
		return 0
	}
	return value.box.value.expiresMS
}

type NativeGrantStatus struct {
	State                 uint32
	GrantID               string
	ExpiresMS, ConsumedMS int64
}

func (store *Store) nativeRevalidatePrepared(ctx context.Context, tx *ent.Tx, record nativePreparedRecord, validate NativePrepareValidator) error {
	metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, record.Preparation.Consumer.OwnerID, record.Preparation.Request.AssetID)
	if err != nil {
		return err
	}
	defer clear(payload.data)
	if err := nativeCheckAsset(metadata, record.Preparation.Request); err != nil {
		return err
	}
	data, err := validate(ctx, metadata, payload)
	if err != nil {
		return err
	}
	if data.Adapter != record.Preparation.Request.Adapter || data.RequiredSlot != record.Preparation.RequiredSlot || data.Patch != record.Patch {
		return ErrNativePrecondition
	}
	return nil
}

func (store *Store) AuthorizeNativeConfigurationGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.AuthorizeRequest, proof nativesessionauth.VerifiedApplyProof, token nativesessionauth.ParsedGrantToken, validate NativePrepareValidator) (NativeAuthorization, error) {
	return store.authorizeNativeConfigurationGrant(ctx, credential, origin, request, proof, token, validate, nativeDefaultBudget())
}

func (store *Store) authorizeNativeConfigurationGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.AuthorizeRequest, proof nativesessionauth.VerifiedApplyProof, token nativesessionauth.ParsedGrantToken, validate NativePrepareValidator, budget nativeBudget) (NativeAuthorization, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return NativeAuthorization{}, admissionError
	}
	defer releaseWriter()
	if !credential.present() || validate == nil {
		return NativeAuthorization{}, ErrNativeUnauthorized
	}
	id := rand.Text()
	var challenge [32]byte
	_, _ = rand.Read(challenge[:])
	run := func() (NativeAuthorization, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeAuthorization, func(time.Time) error, error) {
			child, parent, identityGate, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
			if err != nil {
				return NativeAuthorization{}, nil, err
			}
			consumer := nativeConsumerFields(child, parent)
			row, record, err := store.nativeLoadPrepared(ctx, tx, child, consumer, request.PrepareID)
			if err != nil {
				return NativeAuthorization{}, nil, err
			}
			fields := nativesessionauth.AuthorizeFields{Preparation: record.Preparation, Challenge: [32]byte(row.AuthorizeChallenge), Request: request}
			digest, err := nativesessionauth.AuthorizeFieldsDigest(fields)
			if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "authorize", digest) {
				return NativeAuthorization{}, nil, ErrNativeUnauthorized
			}
			if row.Phase != nativeapplypreparation.PhasePatchReady || record.Apply != nil {
				return NativeAuthorization{}, nil, ErrNativePrecondition
			}
			if err := store.nativeRevalidatePrepared(ctx, tx, record, validate); err != nil {
				return NativeAuthorization{}, nil, err
			}
			now := store.state.now().UTC()
			apply := nativesessionauth.ApplyFields{Preparation: record.Preparation, Commitment: request.Commitment, IntentExpiresMS: request.IntentExpiresMS}
			bindingDigest, err := nativesessionauth.HashApply(apply)
			if err != nil {
				return NativeAuthorization{}, nil, ErrNativeInvalid
			}
			record.Apply, record.Consent = new(apply), new(request.Consent)
			row.Phase = nativeapplypreparation.PhaseBoundNoSecret
			result := nativeAuthorization{apply: apply}
			var grant *ent.NativeMaterializationGrant
			if record.Preparation.RequiredSlot != 0 {
				tokenHash, present := token.TokenDigest()
				if !present {
					return NativeAuthorization{}, nil, ErrNativeUnauthorized
				}
				row.Phase = nativeapplypreparation.PhaseBoundWithGrant
				grant = &ent.NativeMaterializationGrant{PublicID: id, UserID: child.UserID, NativeSessionID: child.ID, PrepareID: row.ID, TokenHash: tokenHash[:], ConsumeChallenge: challenge[:], BindingDigest: bindingDigest[:], IssuedMs: now.UnixMilli(), ExpiresMs: min(now.Add(time.Minute).UnixMilli(), request.IntentExpiresMS, child.ExpiresMs, parent.ExpiresAt.UnixMilli())}
				grant.Charge = nativeGrantCharge(grant)
				result.grantID, result.challenge, result.expiresMS = id, challenge, grant.ExpiresMs
			}
			if err := store.nativeSealPrepared(row, consumer, record); err != nil {
				return NativeAuthorization{}, nil, err
			}
			changes := []nativeQuotaChange{{id: row.ID, post: nativeUsageRow{2, child.ParentSessionID, child.UserID, row.Charge}}}
			if grant != nil {
				changes = append(changes, nativeQuotaChange{post: nativeUsageRow{3, child.ParentSessionID, child.UserID, grant.Charge}})
			}
			if err := nativeCheckPostStateQuota(ctx, tx, changes, budget); err != nil {
				return NativeAuthorization{}, nil, err
			}
			count, err := tx.NativeApplyPreparation.Update().Where(nativeapplypreparation.IDEQ(row.ID), nativeapplypreparation.PhaseEQ(nativeapplypreparation.PhasePatchReady)).SetPhase(row.Phase).SetCiphertext(row.Ciphertext).SetCharge(row.Charge).Save(ctx)
			if err != nil {
				return NativeAuthorization{}, nil, err
			}
			if count != 1 {
				return NativeAuthorization{}, nil, ErrNativePrecondition
			}
			if grant != nil {
				_, err = tx.NativeMaterializationGrant.Create().SetPublicID(grant.PublicID).SetUserID(grant.UserID).SetNativeSessionID(grant.NativeSessionID).SetPrepareID(grant.PrepareID).SetTokenHash(grant.TokenHash).SetConsumeChallenge(grant.ConsumeChallenge).SetBindingDigest(grant.BindingDigest).SetIssuedMs(grant.IssuedMs).SetExpiresMs(grant.ExpiresMs).SetCharge(grant.Charge).Save(ctx)
			}
			gate := func(instant time.Time) error {
				if err := identityGate(instant); err != nil {
					return err
				}
				if !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(request.IntentExpiresMS), instant) || grant != nil && !nativeActive(now, time.UnixMilli(grant.ExpiresMs), instant) {
					return ErrNativePrecondition
				}
				return nil
			}
			return NativeAuthorization{nativeOpaque: nativeWrap(result)}, gate, err
		})
	}
	value, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeAuthorization{}, pruneErr
		}
		return run()
	}
	return value, err
}

func (store *Store) NativeConfigurationGrantStatus(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.StatusRequest, proof nativesessionauth.VerifiedApplyProof) (NativeGrantStatus, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGrantStatus, func(time.Time) error, error) {
		child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		row, record, err := store.nativeLoadPrepared(ctx, tx, child, nativeConsumerFields(child, parent), request.PrepareID)
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		digest, err := nativesessionauth.StatusFieldsDigest(nativesessionauth.StatusFields{Preparation: record.Preparation, Challenge: [32]byte(row.AuthorizeChallenge), Request: request})
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "status", digest) {
			return NativeGrantStatus{}, nil, ErrNativeUnauthorized
		}
		grant, err := row.QueryGrant().Only(ctx)
		if ent.IsNotFound(err) && request.GrantID == "" {
			return NativeGrantStatus{State: 1}, gate, nil
		}
		if ent.IsNotFound(err) {
			return NativeGrantStatus{}, nil, ErrConfigurationNotFound
		}
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		if !nativePreparedGrantValid(grant, child, record) {
			return NativeGrantStatus{}, nil, ErrNativeUnavailable
		}
		if request.GrantID != "" && request.GrantID != grant.PublicID {
			return NativeGrantStatus{}, nil, ErrConfigurationNotFound
		}
		state := uint32(2)
		switch {
		case grant.ConsumedMs > 0:
			state = 3
		case grant.Retired:
			state = 5
		case grant.Expired || store.state.now().UnixMilli() >= grant.ExpiresMs:
			state = 4
			if !grant.Expired {
				if err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx); err != nil {
					return NativeGrantStatus{}, nil, err
				}
			}
		}
		return NativeGrantStatus{State: state, GrantID: grant.PublicID, ExpiresMS: grant.ExpiresMs, ConsumedMS: grant.ConsumedMs}, gate, nil
	})
}

type NativeMaterialization struct{ nativeOpaque[[]byte] }

func NewNativeMaterialization(encoded []byte) (NativeMaterialization, error) {
	if len(encoded) == 0 || len(encoded) > 512<<10 {
		return NativeMaterialization{}, ErrNativePrecondition
	}
	return NativeMaterialization{nativeOpaque: nativeWrap(bytes.Clone(encoded))}, nil
}
func (value NativeMaterialization) Reveal() []byte {
	if !value.present() {
		return nil
	}
	return bytes.Clone(*value.box.value)
}
func (value NativeMaterialization) Destroy() {
	if value.present() {
		clear(*value.box.value)
		*value.box.value = nil
	}
}

type NativeMaterializer func(context.Context, ConfigurationAsset, ConfigurationPayload, nativesessionauth.ApplyFields, string, int64) (NativeMaterialization, error)

type nativeConsumeOutcome struct {
	response  NativeMaterialization
	rejection error
}

func (store *Store) ConsumeNativeConfigurationGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.ConsumeRequest, proof nativesessionauth.VerifiedApplyProof, materialize NativeMaterializer) (NativeMaterialization, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return NativeMaterialization{}, admissionError
	}
	defer releaseWriter()
	if !credential.present() || materialize == nil {
		return NativeMaterialization{}, ErrNativeUnauthorized
	}
	var scratch NativeMaterialization
	result, err := store.nativeTransaction(ctx, func(tx *ent.Tx) (nativeConsumeOutcome, func(time.Time) error, error) {
		scratch.Destroy()
		child, parent, identityGate, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		row, record, err := store.nativeLoadPrepared(ctx, tx, child, nativeConsumerFields(child, parent), request.PrepareID)
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		grant, err := row.QueryGrant().Only(ctx)
		if ent.IsNotFound(err) {
			return nativeConsumeOutcome{}, nil, ErrNativeUnauthorized
		}
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		if !nativePreparedGrantValid(grant, child, record) {
			return nativeConsumeOutcome{}, nil, ErrNativeUnauthorized
		}
		fields := nativesessionauth.ConsumeFields{Apply: *record.Apply, AuthorizeChallenge: [32]byte(row.AuthorizeChallenge), ConsumeChallenge: [32]byte(grant.ConsumeChallenge), GrantExpiresMS: grant.ExpiresMs, Request: request}
		digest, err := nativesessionauth.ConsumeFieldsDigest(fields)
		tokenHash, present := request.Token.TokenDigest()
		bindingDigest, bindingErr := nativesessionauth.HashApply(*record.Apply)
		if err != nil || bindingErr != nil || !present || grant.PublicID != request.GrantID || subtle.ConstantTimeCompare(grant.TokenHash, tokenHash[:]) != 1 || subtle.ConstantTimeCompare(grant.BindingDigest, bindingDigest[:]) != 1 || !proof.MatchesApply(store.state.keyID, *credential.box.value, "consume", digest) {
			return nativeConsumeOutcome{}, nil, ErrNativeUnauthorized
		}
		if row.Phase != nativeapplypreparation.PhaseBoundWithGrant || grant.ConsumedMs != 0 || grant.Retired || grant.Expired {
			return nativeConsumeOutcome{}, nil, ErrNativePrecondition
		}
		now := store.state.now().UTC()
		if !nativeActive(time.UnixMilli(grant.IssuedMs), time.UnixMilli(grant.ExpiresMs), now) || now.UnixMilli() >= record.Apply.IntentExpiresMS {
			if now.UnixMilli() >= grant.ExpiresMs {
				if err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx); err != nil {
					return nativeConsumeOutcome{}, nil, err
				}
				return nativeConsumeOutcome{rejection: ErrNativePrecondition}, identityGate, nil
			}
			return nativeConsumeOutcome{}, nil, ErrNativePrecondition
		}
		metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, parent.Edges.User.UserKey, record.Preparation.Request.AssetID)
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		defer clear(payload.data)
		if err := nativeCheckAsset(metadata, record.Preparation.Request); err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		consumed := store.state.now().UTC()
		scratch, err = materialize(ctx, metadata, payload, *record.Apply, grant.PublicID, consumed.UnixMilli())
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		if !scratch.present() || len(*scratch.box.value) == 0 || len(*scratch.box.value) > 512<<10 {
			return nativeConsumeOutcome{}, nil, ErrNativePrecondition
		}
		gate := func(instant time.Time) error {
			if err := identityGate(instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(record.Preparation.PreparedMS), time.UnixMilli(record.Apply.IntentExpiresMS), instant) || !nativeActive(time.UnixMilli(grant.IssuedMs), time.UnixMilli(grant.ExpiresMs), instant) {
				return ErrNativePrecondition
			}
			return nil
		}
		instant := store.state.now().UTC()
		if err := gate(instant); err != nil {
			if identityGate(instant) == nil && instant.UnixMilli() >= grant.ExpiresMs {
				scratch.Destroy()
				if err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx); err != nil {
					return nativeConsumeOutcome{}, nil, err
				}
				return nativeConsumeOutcome{rejection: ErrNativePrecondition}, identityGate, nil
			}
			return nativeConsumeOutcome{}, nil, err
		}
		count, err := tx.NativeMaterializationGrant.Update().Where(nativematerializationgrant.IDEQ(grant.ID), nativematerializationgrant.ConsumedMsEQ(0), nativematerializationgrant.RetiredEQ(false), nativematerializationgrant.ExpiredEQ(false)).SetConsumedMs(consumed.UnixMilli()).Save(ctx)
		if err == nil && count != 1 {
			err = ErrNativePrecondition
		}
		return nativeConsumeOutcome{response: scratch}, gate, err
	})
	if err != nil {
		scratch.Destroy()
		return NativeMaterialization{}, err
	}
	if err := ctx.Err(); err != nil {
		scratch.Destroy()
		return NativeMaterialization{}, err
	}
	if result.rejection != nil {
		scratch.Destroy()
		return NativeMaterialization{}, result.rejection
	}
	return result.response, nil
}

func nativePreparationCharge(row *ent.NativeApplyPreparation) int64 {
	return 512 + int64(len(row.PublicID)+len(row.OperationID)+len(row.RequestDigest)+len(row.AuthorizeChallenge)+len(row.Ciphertext)+len(row.Phase))
}

func nativeGrantCharge(row *ent.NativeMaterializationGrant) int64 {
	return 512 + int64(len(row.PublicID)+len(row.TokenHash)+len(row.ConsumeChallenge)+len(row.BindingDigest))
}

func nativePreparationRecordValid(row *ent.NativeApplyPreparation) bool {
	phase := string(row.Phase)
	return validPackKey(row.PublicID) && row.UserID > 0 && row.NativeSessionID > 0 && len(row.OperationID) == 16 && len(row.RequestDigest) == 32 && len(row.AuthorizeChallenge) == 32 && len(row.Ciphertext) > 0 && row.IssuedMs > 0 && row.ExpiresMs > row.IssuedMs && row.ExpiresMs-row.IssuedMs <= 300000 && (phase == "patch-ready" || phase == "bound-with-grant" || phase == "bound-no-secret" || phase == "expired" || phase == "retired") && row.Charge == nativePreparationCharge(row) && row.Charge >= 512 && row.Charge <= 16<<10
}

func nativeGrantRecordValid(row *ent.NativeMaterializationGrant) bool {
	return validPackKey(row.PublicID) && row.UserID > 0 && row.NativeSessionID > 0 && row.PrepareID > 0 && len(row.TokenHash) == 32 && len(row.ConsumeChallenge) == 32 && len(row.BindingDigest) == 32 && row.IssuedMs > 0 && row.ExpiresMs > row.IssuedMs && row.ExpiresMs-row.IssuedMs <= 60000 && (row.ConsumedMs == 0 || row.ConsumedMs >= row.IssuedMs && row.ConsumedMs < row.ExpiresMs) && row.Charge == nativeGrantCharge(row) && row.Charge >= 512 && row.Charge <= 4096
}

func nativePreparedGrantValid(grant *ent.NativeMaterializationGrant, child *ent.NativeConsumerSession, record nativePreparedRecord) bool {
	if !nativeGrantRecordValid(grant) || grant.NativeSessionID != child.ID || grant.UserID != child.UserID || record.Apply == nil || record.Preparation.RequiredSlot == 0 || grant.IssuedMs < record.Preparation.PreparedMS || grant.ExpiresMs > record.Apply.IntentExpiresMS {
		return false
	}
	digest, err := nativesessionauth.HashApply(*record.Apply)
	return err == nil && subtle.ConstantTimeCompare(grant.BindingDigest, digest[:]) == 1
}

type nativeUsageKey struct{ table, id int }
type nativeQuotaChange struct {
	id   int
	post nativeUsageRow
}
type nativeInventory struct {
	challenges []*ent.NativeConsumerChallenge
	children   []*ent.NativeConsumerSession
	prepares   []*ent.NativeApplyPreparation
	grants     []*ent.NativeMaterializationGrant
	usage      map[nativeUsageKey]nativeUsageRow
}

func nativeLoadInventory(ctx context.Context, tx *ent.Tx) (nativeInventory, error) {
	var inventory nativeInventory
	var err error
	if inventory.challenges, err = tx.NativeConsumerChallenge.Query().WithParentSession().Limit(129).All(ctx); err != nil {
		return inventory, err
	}
	if inventory.children, err = tx.NativeConsumerSession.Query().WithParentSession().Limit(129).All(ctx); err != nil {
		return inventory, err
	}
	if inventory.prepares, err = tx.NativeApplyPreparation.Query().Limit(129).All(ctx); err != nil {
		return inventory, err
	}
	if inventory.grants, err = tx.NativeMaterializationGrant.Query().Limit(129).All(ctx); err != nil {
		return inventory, err
	}
	if len(inventory.challenges) > 128 || len(inventory.children) > 128 || len(inventory.prepares) > 128 || len(inventory.grants) > 128 {
		return inventory, ErrNativeUnavailable
	}
	inventory.usage = make(map[nativeUsageKey]nativeUsageRow)
	children := make(map[int]*ent.NativeConsumerSession)
	prepares := make(map[int]*ent.NativeApplyPreparation)
	for _, row := range inventory.challenges {
		if !nativeChallengeRecordValid(row) || row.Edges.ParentSession == nil || row.Edges.ParentSession.UserID != row.UserID {
			return inventory, ErrNativeUnavailable
		}
		inventory.usage[nativeUsageKey{0, row.ID}] = nativeUsageRow{0, row.ParentSessionID, row.UserID, row.Charge}
	}
	for _, row := range inventory.children {
		if !nativeSessionRecordValid(row) || row.Edges.ParentSession == nil || row.Edges.ParentSession.UserID != row.UserID {
			return inventory, ErrNativeUnavailable
		}
		children[row.ID] = row
		inventory.usage[nativeUsageKey{1, row.ID}] = nativeUsageRow{1, row.ParentSessionID, row.UserID, row.Charge}
	}
	for _, row := range inventory.prepares {
		child := children[row.NativeSessionID]
		if !nativePreparationRecordValid(row) || child == nil || child.UserID != row.UserID || !bytes.Equal(child.OperationID, row.OperationID) || row.IssuedMs < child.IssuedMs || row.ExpiresMs > child.ExpiresMs {
			return inventory, ErrNativeUnavailable
		}
		prepares[row.ID] = row
		inventory.usage[nativeUsageKey{2, row.ID}] = nativeUsageRow{2, child.ParentSessionID, row.UserID, row.Charge}
	}
	for _, row := range inventory.grants {
		child, prepare := children[row.NativeSessionID], prepares[row.PrepareID]
		if !nativeGrantRecordValid(row) || child == nil || prepare == nil || child.UserID != row.UserID || prepare.UserID != row.UserID || prepare.NativeSessionID != row.NativeSessionID || row.IssuedMs < prepare.IssuedMs || row.ExpiresMs > prepare.ExpiresMs || prepare.Phase == nativeapplypreparation.PhasePatchReady || prepare.Phase == nativeapplypreparation.PhaseBoundNoSecret {
			return inventory, ErrNativeUnavailable
		}
		inventory.usage[nativeUsageKey{3, row.ID}] = nativeUsageRow{3, child.ParentSessionID, row.UserID, row.Charge}
	}
	return inventory, nil
}

func nativeCheckPostStateQuota(ctx context.Context, tx *ent.Tx, changes []nativeQuotaChange, budget nativeBudget) error {
	if len(changes) == 0 || len(changes) > 2 {
		return ErrNativeUnavailable
	}
	inventory, err := nativeLoadInventory(ctx, tx)
	if err != nil {
		return err
	}
	rows := inventory.usage
	seen := make(map[nativeUsageKey]bool)
	parent, owner := changes[0].post.parent, changes[0].post.owner
	for position, change := range changes {
		post := change.post
		if post.table < 0 || post.table >= 4 || post.parent <= 0 || post.owner <= 0 || post.parent != parent || post.owner != owner || change.id < 0 {
			return ErrNativeUnavailable
		}
		if post.charge < 512 || post.charge > budget.rowBytes[post.table] {
			return ErrNativeQuota
		}
		key := nativeUsageKey{post.table, change.id}
		if change.id != 0 {
			previous, exists := rows[key]
			if !exists || seen[key] || previous.parent != parent || previous.owner != owner {
				return ErrNativeUnavailable
			}
			seen[key] = true
		} else {
			key.id = -position - 1
		}
		rows[key] = post
	}
	var usage [4][3]nativeLimit
	var total [3]nativeLimit
	for _, row := range rows {
		for scope := range 3 {
			if scope == 0 && row.parent != parent || scope == 1 && row.owner != owner {
				continue
			}
			usage[row.table][scope].rows++
			usage[row.table][scope].bytes += row.charge
			total[scope].rows++
			total[scope].bytes += row.charge
		}
	}
	for scope := range 3 {
		if total[scope].rows > budget.totals[scope].rows || total[scope].bytes > budget.totals[scope].bytes {
			return ErrNativeQuota
		}
		for table := range 4 {
			if usage[table][scope].rows > budget.tables[table][scope].rows || usage[table][scope].bytes > budget.tables[table][scope].bytes {
				return ErrNativeQuota
			}
		}
	}
	return nil
}

func (inventory nativeInventory) closure(root nativePruneRoot) (int, int64) {
	rows, charge := 1, root.charge
	if root.table != 1 {
		return rows, charge
	}
	for _, prepare := range inventory.prepares {
		if prepare.NativeSessionID == root.id {
			rows++
			charge += prepare.Charge
		}
	}
	for _, grant := range inventory.grants {
		if grant.NativeSessionID == root.id {
			rows++
			charge += grant.Charge
		}
	}
	return rows, charge
}
