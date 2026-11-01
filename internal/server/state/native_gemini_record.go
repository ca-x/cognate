package state

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json/v2"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"google.golang.org/protobuf/proto"
)

type nativeGeminiRecord struct {
	preparation nativesessionauth.GeminiPreparation
	apply       nativesessionauth.GeminiApply
}
type geminiStoredRecord struct {
	Version     uint32
	Preparation []byte
	Nonce       [32]byte
	Apply       []byte
	Revision    uint64
}

func nativeEncodeGeminiRecord(record nativeGeminiRecord) ([]byte, error) {
	if _, err := nativesessionauth.HashGeminiPreparation(record.preparation); err != nil {
		return nil, ErrNativeUnavailable
	}
	stored := geminiStoredRecord{Version: 1, Nonce: record.preparation.CopyRequest().Nonce}
	var err error
	stored.Preparation, err = (proto.MarshalOptions{Deterministic: true}).Marshal(record.preparation.CopyWire())
	if err != nil {
		return nil, ErrNativeUnavailable
	}
	if wire := record.apply.CopyWire(); wire != nil {
		if !proto.Equal(wire.Preparation, record.preparation.CopyWire()) {
			return nil, ErrNativeUnavailable
		}
		stored.Apply, err = (proto.MarshalOptions{Deterministic: true}).Marshal(wire)
		if err != nil {
			return nil, ErrNativeUnavailable
		}
		stored.Revision = record.apply.ConfirmationRevision()
	}
	body, err := json.Marshal(stored)
	if err != nil || len(body) > 15<<10 {
		return nil, ErrNativeQuota
	}
	return append([]byte("GM3\x00"), body...), nil
}
func nativeDecodeGeminiRecord(body []byte, consumer nativesessionauth.ConsumerFields) (nativeGeminiRecord, error) {
	if len(body) > 16<<10 || !bytes.HasPrefix(body, []byte("GM3\x00")) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	var stored geminiStoredRecord
	if json.Unmarshal(body[4:], &stored, json.RejectUnknownMembers(true)) != nil || stored.Version != 1 {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	wire := new(nativev1.PreparationBinding)
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(stored.Preparation, wire) != nil || !nativesessionauth.ValidGeminiPreparation(wire) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	request := nativesessionauth.GeminiPrepareRequest{PrepareRequest: nativesessionauth.PrepareRequest{OwnerID: wire.OwnerId, AssetID: wire.AssetId, AssetRevision: wire.AssetRevision, AgentID: wire.AgentId, SchemaVersion: wire.SchemaVersion, Adapter: nativesessionauth.AdapterFields{APIVersion: wire.Adapter.ApiVersion, PluginID: wire.Adapter.PluginId, PluginVersion: wire.Adapter.PluginVersion, DefinitionDigest: wire.Adapter.DefinitionDigest, Capability: wire.Adapter.RequiredCapabilities[0]}, Target: uint32(wire.Target), Scope: uint32(wire.Scope), ProfileID: wire.ProfileId, SelectionRevision: wire.SelectionRevision, OperationID: consumer.OperationID, Nonce: stored.Nonce}, ContractVersion: 1}
	slot := uint32(0)
	if len(wire.RequiredSlots) == 1 {
		slot = 4
	}
	preparation, err := nativesessionauth.NewGeminiPreparation(consumer, request, wire.PrepareId, wire.PreparedAtUnixMs, wire.PrepareExpiresAtUnixMs, [32]byte(wire.Gemini.DeclarationSha256), slot)
	if err != nil || !proto.Equal(preparation.CopyWire(), wire) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(wire)
	if err != nil || !bytes.Equal(canonical, stored.Preparation) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	record := nativeGeminiRecord{preparation: preparation}
	if len(stored.Apply) == 0 {
		if stored.Revision != 0 {
			return nativeGeminiRecord{}, ErrNativeUnavailable
		}
		return record, nil
	}
	apply := new(nativev1.ApplyBinding)
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(stored.Apply, apply) != nil || !proto.Equal(apply.Preparation, wire) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	local, err := nativesessionauth.NewGeminiLocalBinding([32]byte(wire.Gemini.DeclarationSha256), apply.GeminiLocalBinding)
	if err != nil {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	record.apply, err = nativesessionauth.NewGeminiApply(preparation, local, stored.Revision, apply.IntentExpiresAtUnixMs)
	if err != nil || !proto.Equal(record.apply.CopyWire(), apply) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	canonical, err = (proto.MarshalOptions{Deterministic: true}).Marshal(apply)
	if err != nil || !bytes.Equal(canonical, stored.Apply) {
		return nativeGeminiRecord{}, ErrNativeUnavailable
	}
	return record, nil
}
func (store *Store) nativeSealGeminiPrepared(row *ent.NativeApplyPreparation, consumer nativesessionauth.ConsumerFields, record nativeGeminiRecord) error {
	plaintext, err := nativeEncodeGeminiRecord(record)
	defer clear(plaintext)
	if err != nil {
		return err
	}
	row.Ciphertext = store.state.configurationAEAD.Seal(nil, nil, plaintext, nativePreparedAAD(row, consumer))
	row.Charge = nativePreparationCharge(row)
	if len(row.Ciphertext) > 16<<10 || row.Charge > 16<<10 {
		return ErrNativeQuota
	}
	return nil
}
func (store *Store) nativeLoadGeminiPrepared(ctx context.Context, tx *ent.Tx, child *ent.NativeConsumerSession, consumer nativesessionauth.ConsumerFields, id string) (*ent.NativeApplyPreparation, nativeGeminiRecord, error) {
	if !validPackKey(id) {
		return nil, nativeGeminiRecord{}, ErrNativeInvalid
	}
	row, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.PublicIDEQ(id), nativeapplypreparation.NativeSessionIDEQ(child.ID), nativeapplypreparation.UserIDEQ(child.UserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nativeGeminiRecord{}, ErrConfigurationNotFound
	}
	if err != nil {
		return nil, nativeGeminiRecord{}, err
	}
	if !nativePreparationRecordValid(row) || len(row.Ciphertext) > 16<<10 {
		return nil, nativeGeminiRecord{}, ErrNativeUnavailable
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, row.Ciphertext, nativePreparedAAD(row, consumer))
	defer clear(plaintext)
	if err != nil {
		return nil, nativeGeminiRecord{}, ErrNativeUnavailable
	}
	record, err := nativeDecodeGeminiRecord(plaintext, consumer)
	if err != nil {
		return nil, nativeGeminiRecord{}, ErrNativeUnavailable
	}
	wire := record.preparation.CopyWire()
	if wire.PrepareId != row.PublicID || wire.PreparedAtUnixMs != row.IssuedMs || wire.PrepareExpiresAtUnixMs != row.ExpiresMs || row.ExpiresMs > child.ExpiresMs || !bytes.Equal(row.OperationID, child.OperationID) {
		return nil, nativeGeminiRecord{}, ErrNativeUnavailable
	}
	digest, err := nativesessionauth.GeminiPrepareFieldsDigest(nativesessionauth.GeminiPrepareFields{Consumer: consumer, Request: record.preparation.CopyRequest()})
	if err != nil || subtle.ConstantTimeCompare(row.RequestDigest, digest[:]) != 1 {
		return nil, nativeGeminiRecord{}, ErrNativeUnavailable
	}
	bound := record.apply.CopyWire() != nil
	if row.Phase == nativeapplypreparation.PhasePatchReady && bound || row.Phase != nativeapplypreparation.PhasePatchReady && !bound || row.Phase == nativeapplypreparation.PhaseBoundNoSecret && len(wire.RequiredSlots) != 0 || row.Phase == nativeapplypreparation.PhaseBoundWithGrant && len(wire.RequiredSlots) != 1 {
		return nil, nativeGeminiRecord{}, ErrNativeUnavailable
	}
	return row, record, nil
}
