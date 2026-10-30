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

type nativeCodexPreparedData struct {
	adapter     nativesessionauth.AdapterFields
	declaration agentconfig.CodexProviderDeclarationV1
	digest      [32]byte
}
type NativeCodexPreparedData struct {
	nativeOpaque[nativeCodexPreparedData]
}
type NativeCodexPrepareValidator func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeCodexPreparedData, error)
type NativeCodexMaterializer func(context.Context, ConfigurationAsset, ConfigurationPayload, nativesessionauth.CodexApply, string, int64) (NativeMaterialization, error)

func cloneNativeCodexDeclaration(value agentconfig.CodexProviderDeclarationV1) agentconfig.CodexProviderDeclarationV1 {
	encoded, _ := json.Marshal(value)
	defer clear(encoded)
	var cloned agentconfig.CodexProviderDeclarationV1
	if json.Unmarshal(encoded, &cloned) != nil {
		return agentconfig.CodexProviderDeclarationV1{}
	}
	return cloned
}
func NewNativeCodexPreparedData(adapter nativesessionauth.AdapterFields, declaration agentconfig.CodexProviderDeclarationV1) (NativeCodexPreparedData, error) {
	digest, err := nativesessionauth.HashCodexDeclaration(declaration)
	if err != nil {
		return NativeCodexPreparedData{}, ErrNativePrecondition
	}
	cloned := cloneNativeCodexDeclaration(declaration)
	actual, err := nativesessionauth.HashCodexDeclaration(cloned)
	if err != nil || actual != digest {
		return NativeCodexPreparedData{}, ErrNativePrecondition
	}
	return NativeCodexPreparedData{nativeOpaque: nativeWrap(nativeCodexPreparedData{adapter: adapter, declaration: cloned, digest: digest})}, nil
}

type nativeCodexPreparedValue struct {
	record    nativeCodexRecord
	data      NativeCodexPreparedData
	challenge [32]byte
}
type NativeCodexPrepared struct {
	nativeOpaque[nativeCodexPreparedValue]
}

func (prepared NativeCodexPrepared) Binding() nativesessionauth.CodexPreparation {
	if !prepared.present() {
		return nativesessionauth.CodexPreparation{}
	}
	return prepared.box.value.record.preparation
}
func (prepared NativeCodexPrepared) CopyDeclaration() agentconfig.CodexProviderDeclarationV1 {
	if !prepared.present() {
		return agentconfig.CodexProviderDeclarationV1{}
	}
	return cloneNativeCodexDeclaration(prepared.box.value.data.box.value.declaration)
}
func (prepared NativeCodexPrepared) Challenge() [32]byte {
	if !prepared.present() {
		return [32]byte{}
	}
	return prepared.box.value.challenge
}

func (store *Store) nativeRevalidateCodex(ctx context.Context, tx *ent.Tx, record nativeCodexRecord, validate NativeCodexPrepareValidator) (NativeCodexPreparedData, error) {
	request := record.preparation.CopyRequest()
	metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, request.OwnerID, request.AssetID)
	if err != nil {
		return NativeCodexPreparedData{}, err
	}
	defer clear(payload.data)
	if err := nativeCheckAsset(metadata, request.PrepareRequest); err != nil {
		return NativeCodexPreparedData{}, err
	}
	data, err := validate(ctx, metadata, payload)
	if err != nil {
		return NativeCodexPreparedData{}, err
	}
	wire := record.preparation.CopyWire()
	if !data.present() || data.box.value.adapter != request.Adapter || subtle.ConstantTimeCompare(data.box.value.digest[:], wire.Codex.DeclarationSha256) != 1 {
		return NativeCodexPreparedData{}, ErrNativePrecondition
	}
	return data, nil
}

func (store *Store) PrepareNativeCodexApply(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.CodexPrepareRequest, proof nativesessionauth.VerifiedApplyProof, validate NativeCodexPrepareValidator) (NativeCodexPrepared, error) {
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return NativeCodexPrepared{}, err
	}
	defer release()
	if validate == nil || !credential.present() {
		return NativeCodexPrepared{}, ErrNativeUnauthorized
	}
	id := rand.Text()
	var challenge [32]byte
	_, _ = rand.Read(challenge[:])
	run := func() (NativeCodexPrepared, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeCodexPrepared, func(time.Time) error, error) {
			child, parent, identity, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
			if err != nil {
				return NativeCodexPrepared{}, nil, err
			}
			consumer := nativeConsumerFields(child, parent)
			digest, err := nativesessionauth.CodexPrepareFieldsDigest(nativesessionauth.CodexPrepareFields{Consumer: consumer, Request: request})
			if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "prepare", digest) {
				return NativeCodexPrepared{}, nil, ErrNativeUnauthorized
			}
			previous, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.NativeSessionIDEQ(child.ID)).Only(ctx)
			if err == nil {
				row, record, err := store.nativeLoadCodexPrepared(ctx, tx, child, consumer, previous.PublicID)
				if err != nil {
					return NativeCodexPrepared{}, nil, err
				}
				if subtle.ConstantTimeCompare(row.RequestDigest, digest[:]) != 1 {
					return NativeCodexPrepared{}, nil, ErrConfigurationConflict
				}
				data, err := store.nativeRevalidateCodex(ctx, tx, record, validate)
				if err != nil {
					return NativeCodexPrepared{}, nil, err
				}
				return NativeCodexPrepared{nativeOpaque: nativeWrap(nativeCodexPreparedValue{record: record, data: data, challenge: [32]byte(row.AuthorizeChallenge)})}, identity, nil
			}
			if !ent.IsNotFound(err) {
				return NativeCodexPrepared{}, nil, err
			}
			metadata, payload, err := store.readConfigurationAssetVersionTx(ctx, tx, consumer.OwnerID, request.AssetID)
			if err != nil {
				return NativeCodexPrepared{}, nil, err
			}
			defer clear(payload.data)
			if err := nativeCheckAsset(metadata, request.PrepareRequest); err != nil {
				return NativeCodexPrepared{}, nil, err
			}
			data, err := validate(ctx, metadata, payload)
			if err != nil {
				return NativeCodexPrepared{}, nil, err
			}
			if !data.present() || data.box.value.adapter != request.Adapter {
				return NativeCodexPrepared{}, nil, ErrNativePrecondition
			}
			now := store.state.now().UTC()
			preparation, err := nativesessionauth.NewCodexPreparation(consumer, request, id, now.UnixMilli(), child.ExpiresMs, data.box.value.digest)
			if err != nil {
				return NativeCodexPrepared{}, nil, ErrNativePrecondition
			}
			record := nativeCodexRecord{preparation: preparation}
			row := &ent.NativeApplyPreparation{PublicID: id, UserID: child.UserID, NativeSessionID: child.ID, OperationID: bytes.Clone(child.OperationID), RequestDigest: bytes.Clone(digest[:]), AuthorizeChallenge: bytes.Clone(challenge[:]), Phase: nativeapplypreparation.PhasePatchReady, IssuedMs: now.UnixMilli(), ExpiresMs: child.ExpiresMs}
			if err := store.nativeSealCodexPrepared(row, consumer, record); err != nil {
				return NativeCodexPrepared{}, nil, err
			}
			if err := nativeCheckQuota(ctx, tx, nativeUsageRow{2, child.ParentSessionID, child.UserID, row.Charge}, nativeDefaultBudget()); err != nil {
				return NativeCodexPrepared{}, nil, err
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
			return NativeCodexPrepared{nativeOpaque: nativeWrap(nativeCodexPreparedValue{record: record, data: data, challenge: challenge})}, gate, err
		})
	}
	value, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeCodexPrepared{}, pruneErr
		}
		return run()
	}
	return value, err
}

type nativeCodexAuthorization struct {
	apply     nativesessionauth.CodexApply
	id        string
	challenge [32]byte
	expiry    int64
}
type NativeCodexAuthorization struct {
	nativeOpaque[nativeCodexAuthorization]
}

func (value NativeCodexAuthorization) Binding() nativesessionauth.CodexApply {
	if !value.present() {
		return nativesessionauth.CodexApply{}
	}
	return value.box.value.apply
}
func (value NativeCodexAuthorization) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.id
}
func (value NativeCodexAuthorization) Challenge() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.box.value.challenge
}
func (value NativeCodexAuthorization) ExpiresMS() int64 {
	if !value.present() {
		return 0
	}
	return value.box.value.expiry
}

func (store *Store) AuthorizeNativeCodexGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.CodexAuthorizeRequest, proof nativesessionauth.VerifiedApplyProof, token nativesessionauth.ParsedGrantToken, validate NativeCodexPrepareValidator) (NativeCodexAuthorization, error) {
	return store.authorizeNativeCodexGrant(ctx, credential, origin, request, proof, token, validate, nativeDefaultBudget())
}
func (store *Store) authorizeNativeCodexGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.CodexAuthorizeRequest, proof nativesessionauth.VerifiedApplyProof, token nativesessionauth.ParsedGrantToken, validate NativeCodexPrepareValidator, budget nativeBudget) (NativeCodexAuthorization, error) {
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return NativeCodexAuthorization{}, err
	}
	defer release()
	if validate == nil || !credential.present() {
		return NativeCodexAuthorization{}, ErrNativeUnauthorized
	}
	id := rand.Text()
	var challenge [32]byte
	_, _ = rand.Read(challenge[:])
	run := func() (NativeCodexAuthorization, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeCodexAuthorization, func(time.Time) error, error) {
			child, parent, identity, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
			if err != nil {
				return NativeCodexAuthorization{}, nil, err
			}
			consumer := nativeConsumerFields(child, parent)
			row, record, err := store.nativeLoadCodexPrepared(ctx, tx, child, consumer, request.PrepareID)
			if err != nil {
				return NativeCodexAuthorization{}, nil, err
			}
			fields := nativesessionauth.CodexAuthorizeFields{Preparation: record.preparation, Local: request.Local, Challenge: [32]byte(row.AuthorizeChallenge), Request: request.AuthorizeRequest}
			digest, err := nativesessionauth.CodexAuthorizeFieldsDigest(fields)
			if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "authorize", digest) {
				return NativeCodexAuthorization{}, nil, ErrNativeUnauthorized
			}
			if row.Phase != nativeapplypreparation.PhasePatchReady || record.apply.CopyWire() != nil {
				return NativeCodexAuthorization{}, nil, ErrNativePrecondition
			}
			data, err := store.nativeRevalidateCodex(ctx, tx, record, validate)
			if err != nil {
				return NativeCodexAuthorization{}, nil, err
			}
			local := request.Local.CopyWire()
			if data.box.value.declaration.Catalog != nil && (local.Baselines[2].GetNotObserved() != nil || local.CatalogOwnership == 3 || local.Baselines[2].GetMissing() != nil && local.CatalogOwnership != 1 || local.Baselines[2].GetPresent() != nil && local.CatalogOwnership != 2) {
				return NativeCodexAuthorization{}, nil, ErrNativePrecondition
			}
			apply, err := nativesessionauth.NewCodexApply(record.preparation, request.Local, request.Consent.Revision, request.IntentExpiresMS)
			if err != nil || apply.Commitment() != request.Commitment {
				return NativeCodexAuthorization{}, nil, ErrNativeInvalid
			}
			tokenHash, present := token.TokenDigest()
			if !present {
				return NativeCodexAuthorization{}, nil, ErrNativeUnauthorized
			}
			now := store.state.now().UTC()
			binding, err := nativesessionauth.HashCodexApply(record.preparation, apply.Commitment(), request.IntentExpiresMS)
			if err != nil {
				return NativeCodexAuthorization{}, nil, ErrNativeInvalid
			}
			record.apply = apply
			row.Phase = nativeapplypreparation.PhaseBoundWithGrant
			grant := &ent.NativeMaterializationGrant{PublicID: id, UserID: child.UserID, NativeSessionID: child.ID, PrepareID: row.ID, TokenHash: bytes.Clone(tokenHash[:]), ConsumeChallenge: bytes.Clone(challenge[:]), BindingDigest: bytes.Clone(binding[:]), IssuedMs: now.UnixMilli(), ExpiresMs: min(now.Add(time.Minute).UnixMilli(), request.IntentExpiresMS, child.ExpiresMs, parent.ExpiresAt.UnixMilli())}
			grant.Charge = nativeGrantCharge(grant)
			if err := store.nativeSealCodexPrepared(row, consumer, record); err != nil {
				return NativeCodexAuthorization{}, nil, err
			}
			changes := []nativeQuotaChange{{id: row.ID, post: nativeUsageRow{2, child.ParentSessionID, child.UserID, row.Charge}}, {post: nativeUsageRow{3, child.ParentSessionID, child.UserID, grant.Charge}}}
			if err := nativeCheckPostStateQuota(ctx, tx, changes, budget); err != nil {
				return NativeCodexAuthorization{}, nil, err
			}
			count, err := tx.NativeApplyPreparation.Update().Where(nativeapplypreparation.IDEQ(row.ID), nativeapplypreparation.PhaseEQ(nativeapplypreparation.PhasePatchReady)).SetPhase(row.Phase).SetCiphertext(row.Ciphertext).SetCharge(row.Charge).Save(ctx)
			if err != nil {
				return NativeCodexAuthorization{}, nil, err
			}
			if count != 1 {
				return NativeCodexAuthorization{}, nil, ErrNativePrecondition
			}
			_, err = tx.NativeMaterializationGrant.Create().SetPublicID(grant.PublicID).SetUserID(grant.UserID).SetNativeSessionID(grant.NativeSessionID).SetPrepareID(grant.PrepareID).SetTokenHash(grant.TokenHash).SetConsumeChallenge(grant.ConsumeChallenge).SetBindingDigest(grant.BindingDigest).SetIssuedMs(grant.IssuedMs).SetExpiresMs(grant.ExpiresMs).SetCharge(grant.Charge).Save(ctx)
			gate := func(instant time.Time) error {
				if err := identity(instant); err != nil {
					return err
				}
				if !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(request.IntentExpiresMS), instant) || !nativeActive(now, time.UnixMilli(grant.ExpiresMs), instant) {
					return ErrNativePrecondition
				}
				return nil
			}
			return NativeCodexAuthorization{nativeOpaque: nativeWrap(nativeCodexAuthorization{apply: apply, id: id, challenge: challenge, expiry: grant.ExpiresMs})}, gate, err
		})
	}
	value, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeCodexAuthorization{}, pruneErr
		}
		return run()
	}
	return value, err
}

func (store *Store) NativeCodexGrantStatus(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.StatusRequest, proof nativesessionauth.VerifiedApplyProof) (NativeGrantStatus, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeGrantStatus, func(time.Time) error, error) {
		child, parent, gate, err := nativeConsumerGate(ctx, tx, credential, origin, request.OperationID, store.state.now)
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		row, record, err := store.nativeLoadCodexPrepared(ctx, tx, child, nativeConsumerFields(child, parent), request.PrepareID)
		if err != nil {
			return NativeGrantStatus{}, nil, err
		}
		digest, err := nativesessionauth.CodexStatusFieldsDigest(nativesessionauth.CodexStatusFields{Preparation: record.preparation, Challenge: [32]byte(row.AuthorizeChallenge), Request: request})
		if err != nil || !proof.MatchesApply(store.state.keyID, *credential.box.value, "status", digest) {
			return NativeGrantStatus{}, nil, ErrNativeUnauthorized
		}
		grant, err := row.QueryGrant().Only(ctx)
		if ent.IsNotFound(err) && request.GrantID == "" && record.apply.CopyWire() == nil {
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
		if !nativeCodexGrantValid(grant, child, record) {
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

func (store *Store) ConsumeNativeCodexGrant(ctx context.Context, credential NativeConsumerCredential, origin string, request nativesessionauth.ConsumeRequest, proof nativesessionauth.VerifiedApplyProof, materialize NativeCodexMaterializer) (NativeMaterialization, error) {
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
		row, record, err := store.nativeLoadCodexPrepared(ctx, tx, child, nativeConsumerFields(child, parent), request.PrepareID)
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
		if !nativeCodexGrantValid(grant, child, record) {
			return nativeConsumeOutcome{}, nil, ErrNativeUnauthorized
		}
		fields := nativesessionauth.CodexConsumeFields{Apply: record.apply, AuthorizeChallenge: [32]byte(row.AuthorizeChallenge), ConsumeChallenge: [32]byte(grant.ConsumeChallenge), GrantExpiresMS: grant.ExpiresMs, Request: request}
		digest, err := nativesessionauth.CodexConsumeFieldsDigest(fields)
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
