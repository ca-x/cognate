package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"github.com/ca-x/cognate/internal/server/ent/nativematerializationgrant"
)

type nativeGeminiPreparedData struct {
	adapter     nativesessionauth.AdapterFields
	declaration agentconfig.GeminiProviderDeclarationV1
	digest      [32]byte
}
type NativeGeminiPreparedData struct {
	nativeOpaque[nativeGeminiPreparedData]
}
type NativeGeminiPrepareValidator func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeGeminiPreparedData, error)
type NativeGeminiMaterializer func(context.Context, ConfigurationAsset, ConfigurationPayload, nativesessionauth.GeminiApply, string, int64) (NativeMaterialization, error)

func cloneNativeGeminiDeclaration(value agentconfig.GeminiProviderDeclarationV1) agentconfig.GeminiProviderDeclarationV1 {
	encoded, _ := json.Marshal(value)
	defer clear(encoded)
	var cloned agentconfig.GeminiProviderDeclarationV1
	if json.Unmarshal(encoded, &cloned) != nil {
		return agentconfig.GeminiProviderDeclarationV1{}
	}
	return cloned
}
func NewNativeGeminiPreparedData(adapter nativesessionauth.AdapterFields, declaration agentconfig.GeminiProviderDeclarationV1) (NativeGeminiPreparedData, error) {
	digest, err := nativesessionauth.HashGeminiDeclaration(declaration)
	if err != nil {
		return NativeGeminiPreparedData{}, ErrNativePrecondition
	}
	cloned := cloneNativeGeminiDeclaration(declaration)
	actual, err := nativesessionauth.HashGeminiDeclaration(cloned)
	if err != nil || actual != digest {
		return NativeGeminiPreparedData{}, ErrNativePrecondition
	}
	return NativeGeminiPreparedData{nativeOpaque: nativeWrap(nativeGeminiPreparedData{adapter: adapter, declaration: cloned, digest: digest})}, nil
}

type nativeGeminiPreparedValue struct {
	record    nativeGeminiRecord
	data      NativeGeminiPreparedData
	challenge [32]byte
}
type NativeGeminiPrepared struct {
	nativeOpaque[nativeGeminiPreparedValue]
}

func (prepared NativeGeminiPrepared) Binding() nativesessionauth.GeminiPreparation {
	if !prepared.present() {
		return nativesessionauth.GeminiPreparation{}
	}
	return prepared.box.value.record.preparation
}
func (prepared NativeGeminiPrepared) CopyDeclaration() agentconfig.GeminiProviderDeclarationV1 {
	if !prepared.present() {
		return agentconfig.GeminiProviderDeclarationV1{}
	}
	return cloneNativeGeminiDeclaration(prepared.box.value.data.box.value.declaration)
}
func (prepared NativeGeminiPrepared) Challenge() [32]byte {
	if !prepared.present() {
		return [32]byte{}
	}
	return prepared.box.value.challenge
}

func (store *Store) nativeRevalidateGemini(ctx context.Context, tx *ent.Tx, record nativeGeminiRecord, validate NativeGeminiPrepareValidator) (NativeGeminiPreparedData, error) {
	request := record.preparation.CopyRequest()
	metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, request.OwnerID, request.AssetID)
	if err != nil {
		return NativeGeminiPreparedData{}, err
	}
	defer clear(payload.data)
	if err := nativeCheckAsset(metadata, request.PrepareRequest); err != nil {
		return NativeGeminiPreparedData{}, err
	}
	data, err := validate(ctx, metadata, payload)
	if err != nil {
		return NativeGeminiPreparedData{}, err
	}
	wire := record.preparation.CopyWire()
	if !data.present() || data.box.value.adapter != request.Adapter || subtle.ConstantTimeCompare(data.box.value.digest[:], wire.Gemini.DeclarationSha256) != 1 {
		return NativeGeminiPreparedData{}, ErrNativePrecondition
	}
	return data, nil
}

func (store *Store) PrepareNativeGeminiApply(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.GeminiPrepareRequest, proof nativesessionauth.VerifiedApplyProof, validate NativeGeminiPrepareValidator) (NativeGeminiPrepared, error) {
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return NativeGeminiPrepared{}, err
	}
	defer release()
	if validate == nil || !credential.present() {
		return NativeGeminiPrepared{}, ErrNativeUnauthorized
	}
	id := rand.Text()
	var challenge [32]byte
	_, _ = rand.Read(challenge[:])
	run := func() (NativeGeminiPrepared, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGeminiPrepared, func(time.Time) error, error) {
			child, parent, identity, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
			if err != nil {
				return NativeGeminiPrepared{}, nil, err
			}
			consumer := nativeConsumerFields(child, parent)
			digest, err := nativesessionauth.GeminiPrepareFieldsDigest(nativesessionauth.GeminiPrepareFields{Consumer: consumer, Request: request})
			if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "prepare", digest) {
				return NativeGeminiPrepared{}, nil, ErrNativeUnauthorized
			}
			previous, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.NativeSessionIDEQ(child.ID)).Only(ctx)
			if err == nil {
				row, record, err := store.nativeLoadGeminiPrepared(ctx, tx, child, consumer, previous.PublicID)
				if err != nil {
					return NativeGeminiPrepared{}, nil, err
				}
				if subtle.ConstantTimeCompare(row.RequestDigest, digest[:]) != 1 {
					return NativeGeminiPrepared{}, nil, ErrConfigurationConflict
				}
				data, err := store.nativeRevalidateGemini(ctx, tx, record, validate)
				if err != nil {
					return NativeGeminiPrepared{}, nil, err
				}
				return NativeGeminiPrepared{nativeOpaque: nativeWrap(nativeGeminiPreparedValue{record: record, data: data, challenge: [32]byte(row.AuthorizeChallenge)})}, identity, nil
			}
			if !ent.IsNotFound(err) {
				return NativeGeminiPrepared{}, nil, err
			}
			metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, consumer.OwnerID, request.AssetID)
			if err != nil {
				return NativeGeminiPrepared{}, nil, err
			}
			defer clear(payload.data)
			if err := nativeCheckAsset(metadata, request.PrepareRequest); err != nil {
				return NativeGeminiPrepared{}, nil, err
			}
			data, err := validate(ctx, metadata, payload)
			if err != nil {
				return NativeGeminiPrepared{}, nil, err
			}
			if !data.present() || data.box.value.adapter != request.Adapter {
				return NativeGeminiPrepared{}, nil, ErrNativePrecondition
			}
			now := store.state.now().UTC()
			slot := uint32(0)
			if len(data.box.value.declaration.RequiredSlots) == 1 {
				slot = 4
			}
			preparation, err := nativesessionauth.NewGeminiPreparation(consumer, request, id, now.UnixMilli(), child.ExpiresMs, data.box.value.digest, slot)
			if err != nil {
				return NativeGeminiPrepared{}, nil, ErrNativePrecondition
			}
			record := nativeGeminiRecord{preparation: preparation}
			row := &ent.NativeApplyPreparation{PublicID: id, UserID: child.UserID, NativeSessionID: child.ID, OperationID: bytes.Clone(child.OperationID), RequestDigest: bytes.Clone(digest[:]), AuthorizeChallenge: bytes.Clone(challenge[:]), Phase: nativeapplypreparation.PhasePatchReady, IssuedMs: now.UnixMilli(), ExpiresMs: child.ExpiresMs}
			if err := store.nativeSealGeminiPrepared(row, consumer, record); err != nil {
				return NativeGeminiPrepared{}, nil, err
			}
			if err := nativeCheckQuota(ctx, tx, nativeUsageRow{2, child.ParentSessionID, child.UserID, row.Charge}, nativeDefaultBudget()); err != nil {
				return NativeGeminiPrepared{}, nil, err
			}
			_, err = tx.NativeApplyPreparation.Create().SetPublicID(row.PublicID).SetUserID(row.UserID).SetNativeSessionID(row.NativeSessionID).SetOperationID(row.OperationID).SetRequestDigest(row.RequestDigest).SetAuthorizeChallenge(row.AuthorizeChallenge).SetCiphertext(row.Ciphertext).SetPhase(row.Phase).SetIssuedMs(row.IssuedMs).SetExpiresMs(row.ExpiresMs).SetCharge(row.Charge).Save(ctx)
			gate := func(instant time.Time) error {
				if err := identity(instant); err != nil {
					return err
				}
				if !nativeActive(now, time.UnixMilli(row.ExpiresMs), instant) {
					return ErrNativePrecondition
				}
				return nil
			}
			return NativeGeminiPrepared{nativeOpaque: nativeWrap(nativeGeminiPreparedValue{record: record, data: data, challenge: challenge})}, gate, err
		})
	}
	value, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeGeminiPrepared{}, pruneErr
		}
		return run()
	}
	return value, err
}

type nativeGeminiAuthorization struct {
	apply     nativesessionauth.GeminiApply
	id        string
	challenge [32]byte
	expiry    int64
}
type NativeGeminiAuthorization struct {
	nativeOpaque[nativeGeminiAuthorization]
}

func (value NativeGeminiAuthorization) Binding() nativesessionauth.GeminiApply {
	if !value.present() {
		return nativesessionauth.GeminiApply{}
	}
	return value.box.value.apply
}
func (value NativeGeminiAuthorization) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.id
}
func (value NativeGeminiAuthorization) Challenge() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.box.value.challenge
}
func (value NativeGeminiAuthorization) ExpiresMS() int64 {
	if !value.present() {
		return 0
	}
	return value.box.value.expiry
}

func (store *Store) AuthorizeNativeGeminiGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.GeminiAuthorizeRequest, proof nativesessionauth.VerifiedApplyProof, token nativesessionauth.ParsedGrantToken, validate NativeGeminiPrepareValidator) (NativeGeminiAuthorization, error) {
	return store.authorizeNativeGeminiGrant(ctx, credential, origin, request, proof, token, validate, nativeDefaultBudget())
}
func (store *Store) authorizeNativeGeminiGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.GeminiAuthorizeRequest, proof nativesessionauth.VerifiedApplyProof, token nativesessionauth.ParsedGrantToken, validate NativeGeminiPrepareValidator, budget nativeBudget) (NativeGeminiAuthorization, error) {
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return NativeGeminiAuthorization{}, err
	}
	defer release()
	if validate == nil || !credential.present() {
		return NativeGeminiAuthorization{}, ErrNativeUnauthorized
	}
	id := rand.Text()
	var challenge [32]byte
	_, _ = rand.Read(challenge[:])
	run := func() (NativeGeminiAuthorization, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGeminiAuthorization, func(time.Time) error, error) {
			child, parent, identity, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
			if err != nil {
				return NativeGeminiAuthorization{}, nil, err
			}
			consumer := nativeConsumerFields(child, parent)
			row, record, err := store.nativeLoadGeminiPrepared(ctx, tx, child, consumer, request.PrepareID)
			if err != nil {
				return NativeGeminiAuthorization{}, nil, err
			}
			fields := nativesessionauth.GeminiAuthorizeFields{Preparation: record.preparation, Local: request.Local, Challenge: [32]byte(row.AuthorizeChallenge), Request: request.AuthorizeRequest}
			digest, err := nativesessionauth.GeminiAuthorizeFieldsDigest(fields)
			if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "authorize", digest) {
				return NativeGeminiAuthorization{}, nil, ErrNativeUnauthorized
			}
			if row.Phase != nativeapplypreparation.PhasePatchReady || record.apply.CopyWire() != nil {
				return NativeGeminiAuthorization{}, nil, ErrNativePrecondition
			}
			if _, err := store.nativeRevalidateGemini(ctx, tx, record, validate); err != nil {
				return NativeGeminiAuthorization{}, nil, err
			}
			apply, err := nativesessionauth.NewGeminiApply(record.preparation, request.Local, request.Consent.Revision, request.IntentExpiresMS)
			if err != nil || apply.Commitment() != request.Commitment {
				return NativeGeminiAuthorization{}, nil, ErrNativeInvalid
			}

			now := store.state.now().UTC()
			binding, err := nativesessionauth.HashGeminiApply(record.preparation, apply.Commitment(), request.IntentExpiresMS)
			if err != nil {
				return NativeGeminiAuthorization{}, nil, ErrNativeInvalid
			}
			record.apply = apply
			row.Phase = nativeapplypreparation.PhaseBoundNoSecret
			result := nativeGeminiAuthorization{apply: apply}
			var grant *ent.NativeMaterializationGrant
			if len(record.preparation.CopyWire().RequiredSlots) == 1 {
				tokenHash, present := token.TokenDigest()
				if !present {
					return NativeGeminiAuthorization{}, nil, ErrNativeUnauthorized
				}
				row.Phase = nativeapplypreparation.PhaseBoundWithGrant
				grant = &ent.NativeMaterializationGrant{PublicID: id, UserID: child.UserID, NativeSessionID: child.ID, PrepareID: row.ID, TokenHash: bytes.Clone(tokenHash[:]), ConsumeChallenge: bytes.Clone(challenge[:]), BindingDigest: bytes.Clone(binding[:]), IssuedMs: now.UnixMilli(), ExpiresMs: min(now.Add(time.Minute).UnixMilli(), request.IntentExpiresMS, child.ExpiresMs, parent.ExpiresAt.UnixMilli())}
				grant.Charge = nativeGrantCharge(grant)
				result.id, result.challenge, result.expiry = id, challenge, grant.ExpiresMs
			}
			if err := store.nativeSealGeminiPrepared(row, consumer, record); err != nil {
				return NativeGeminiAuthorization{}, nil, err
			}
			changes := []nativeQuotaChange{{id: row.ID, post: nativeUsageRow{2, child.ParentSessionID, child.UserID, row.Charge}}}
			if grant != nil {
				changes = append(changes, nativeQuotaChange{post: nativeUsageRow{3, child.ParentSessionID, child.UserID, grant.Charge}})
			}
			if err := nativeCheckPostStateQuota(ctx, tx, changes, budget); err != nil {
				return NativeGeminiAuthorization{}, nil, err
			}
			count, err := tx.NativeApplyPreparation.Update().Where(nativeapplypreparation.IDEQ(row.ID), nativeapplypreparation.PhaseEQ(nativeapplypreparation.PhasePatchReady)).SetPhase(row.Phase).SetCiphertext(row.Ciphertext).SetCharge(row.Charge).Save(ctx)
			if err != nil {
				return NativeGeminiAuthorization{}, nil, err
			}
			if count != 1 {
				return NativeGeminiAuthorization{}, nil, ErrNativePrecondition
			}
			if grant != nil {
				_, err = tx.NativeMaterializationGrant.Create().SetPublicID(grant.PublicID).SetUserID(grant.UserID).SetNativeSessionID(grant.NativeSessionID).SetPrepareID(grant.PrepareID).SetTokenHash(grant.TokenHash).SetConsumeChallenge(grant.ConsumeChallenge).SetBindingDigest(grant.BindingDigest).SetIssuedMs(grant.IssuedMs).SetExpiresMs(grant.ExpiresMs).SetCharge(grant.Charge).Save(ctx)
			}
			gate := func(instant time.Time) error {
				if err := identity(instant); err != nil {
					return err
				}
				if !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(request.IntentExpiresMS), instant) || grant != nil && !nativeActive(now, time.UnixMilli(grant.ExpiresMs), instant) {
					return ErrNativePrecondition
				}
				return nil
			}
			return NativeGeminiAuthorization{nativeOpaque: nativeWrap(result)}, gate, err
		})
	}
	value, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeGeminiAuthorization{}, pruneErr
		}
		return run()
	}
	return value, err
}

func (store *Store) NativeGeminiGrantStatus(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.StatusRequest, proof nativesessionauth.VerifiedApplyProof) (NativeGrantStatus, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGrantStatus, func(time.Time) error, error) {
		child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		row, record, err := store.nativeLoadGeminiPrepared(ctx, tx, child, nativeConsumerFields(child, parent), request.PrepareID)
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		digest, err := nativesessionauth.GeminiStatusFieldsDigest(nativesessionauth.GeminiStatusFields{Preparation: record.preparation, Challenge: [32]byte(row.AuthorizeChallenge), Request: request})
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "status", digest) {
			return NativeGrantStatus{}, nil, ErrNativeUnauthorized
		}
		grant, err := row.QueryGrant().Only(ctx)
		if ent.IsNotFound(err) && request.GrantID == "" && (record.apply.CopyWire() == nil || len(record.preparation.CopyWire().RequiredSlots) == 0) {
			return NativeGrantStatus{State: 1}, gate, nil
		}
		if ent.IsNotFound(err) {
			if record.apply.CopyWire() != nil {
				return NativeGrantStatus{}, nil, ErrNativeUnavailable
			}
			return NativeGrantStatus{}, nil, ErrConfigurationNotFound
		}
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		if !nativeGeminiGrantValid(grant, child, record) {
			return NativeGrantStatus{}, nil, ErrNativeUnavailable
		}
		if request.GrantID != "" && request.GrantID != grant.PublicID {
			return NativeGrantStatus{}, nil, ErrConfigurationNotFound
		}
		phase := uint32(2)
		switch {
		case grant.ConsumedMs > 0:
			phase = 3
		case grant.Retired:
			phase = 5
		case grant.Expired || store.state.now().UnixMilli() >= grant.ExpiresMs:
			phase = 4
			if !grant.Expired {
				if err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx); err != nil {
					return NativeGrantStatus{}, nil, err
				}
			}
		}
		return NativeGrantStatus{State: phase, GrantID: grant.PublicID, ExpiresMS: grant.ExpiresMs, ConsumedMS: grant.ConsumedMs}, gate, nil
	})
}

func (store *Store) ConsumeNativeGeminiGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.ConsumeRequest, proof nativesessionauth.VerifiedApplyProof, materialize NativeGeminiMaterializer) (NativeMaterialization, error) {
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return NativeMaterialization{}, err
	}
	defer release()
	if materialize == nil || !credential.present() {
		return NativeMaterialization{}, ErrNativeUnauthorized
	}
	var scratch NativeMaterialization
	result, err := store.nativeTransaction(ctx, func(tx *ent.Tx) (nativeConsumeOutcome, func(time.Time) error, error) {
		scratch.Destroy()
		child, parent, identity, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		row, record, err := store.nativeLoadGeminiPrepared(ctx, tx, child, nativeConsumerFields(child, parent), request.PrepareID)
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
		if !nativeGeminiGrantValid(grant, child, record) {
			return nativeConsumeOutcome{}, nil, ErrNativeUnauthorized
		}
		fields := nativesessionauth.GeminiConsumeFields{Apply: record.apply, AuthorizeChallenge: [32]byte(row.AuthorizeChallenge), ConsumeChallenge: [32]byte(grant.ConsumeChallenge), GrantExpiresMS: grant.ExpiresMs, Request: request}
		digest, err := nativesessionauth.GeminiConsumeFieldsDigest(fields)
		token, present := request.Token.TokenDigest()
		if err != nil || !present || grant.PublicID != request.GrantID || subtle.ConstantTimeCompare(grant.TokenHash, token[:]) != 1 || !proof.MatchesApply(store.state.keyID, *credential.box.value, "consume", digest) {
			return nativeConsumeOutcome{}, nil, ErrNativeUnauthorized
		}
		if row.Phase != nativeapplypreparation.PhaseBoundWithGrant || grant.ConsumedMs != 0 || grant.Retired || grant.Expired {
			return nativeConsumeOutcome{}, nil, ErrNativePrecondition
		}
		apply := record.apply.CopyWire()
		gate := func(instant time.Time) error {
			if err := identity(instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(apply.IntentExpiresAtUnixMs), instant) || !nativeActive(time.UnixMilli(grant.IssuedMs), time.UnixMilli(grant.ExpiresMs), instant) {
				return ErrNativePrecondition
			}
			return nil
		}
		markExpired := func(instant time.Time) (nativeConsumeOutcome, func(time.Time) error, error) {
			if identity(instant) == nil && instant.UnixMilli() >= grant.ExpiresMs {
				scratch.Destroy()
				err := tx.NativeMaterializationGrant.UpdateOneID(grant.ID).SetExpired(true).Exec(ctx)
				return nativeConsumeOutcome{rejection: ErrNativePrecondition}, identity, err
			}
			return nativeConsumeOutcome{}, nil, ErrNativePrecondition
		}
		if instant := store.state.now().UTC(); gate(instant) != nil {
			return markExpired(instant)
		}
		metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, parent.Edges.User.UserKey, record.preparation.CopyRequest().AssetID)
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		defer clear(payload.data)
		if err := nativeCheckAsset(metadata, record.preparation.CopyRequest().PrepareRequest); err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		consumed := store.state.now().UTC()
		scratch, err = materialize(ctx, metadata, payload, record.apply, grant.PublicID, consumed.UnixMilli())
		if err != nil {
			return nativeConsumeOutcome{}, nil, err
		}
		if !scratch.present() || len(*scratch.box.value) == 0 || len(*scratch.box.value) > 512<<10 {
			return nativeConsumeOutcome{}, nil, ErrNativePrecondition
		}
		if instant := store.state.now().UTC(); gate(instant) != nil {
			return markExpired(instant)
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
