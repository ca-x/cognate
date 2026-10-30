package state

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"math"
	"unicode/utf8"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
)

type nativeCodexRecord struct {
	preparation nativesessionauth.CodexPreparation
	apply       nativesessionauth.CodexApply
}

type codexRecordReader struct {
	rest   []byte
	failed bool
}

func (reader *codexRecordReader) take(size int) []byte {
	if reader.failed || size < 0 || size > len(reader.rest) {
		reader.failed = true
		return nil
	}
	value := reader.rest[:size]
	reader.rest = reader.rest[size:]
	return value
}
func (reader *codexRecordReader) number() uint32 {
	value := reader.take(4)
	if reader.failed {
		return 0
	}
	return binary.BigEndian.Uint32(value)
}
func (reader *codexRecordReader) wide() uint64 {
	value := reader.take(8)
	if reader.failed {
		return 0
	}
	return binary.BigEndian.Uint64(value)
}
func (reader *codexRecordReader) millis() int64 {
	value := reader.wide()
	if value == 0 || value > math.MaxInt64 {
		reader.failed = true
		return 0
	}
	return int64(value)
}
func (reader *codexRecordReader) blob(limit int) []byte {
	size := reader.number()
	if reader.failed || uint64(size) > uint64(limit) {
		reader.failed = true
		return nil
	}
	return reader.take(int(size))
}
func (reader *codexRecordReader) exact(size int) []byte {
	value := reader.blob(size)
	if len(value) != size {
		reader.failed = true
		return nil
	}
	return value
}
func (reader *codexRecordReader) text(limit int) string {
	value := reader.blob(limit)
	if !utf8.Valid(value) {
		reader.failed = true
		return ""
	}
	return string(value)
}
func (reader *codexRecordReader) done() bool { return !reader.failed && len(reader.rest) == 0 }

func codexRecordBlob(encoded, value []byte) []byte {
	return append(binary.BigEndian.AppendUint32(encoded, uint32(len(value))), value...)
}

func nativeEncodeCodexRecord(record nativeCodexRecord) ([]byte, error) {
	preparation, err := record.preparation.CopyCanonical()
	defer clear(preparation)
	if err != nil || len(preparation) > 4096 {
		return nil, ErrNativeUnavailable
	}
	encoded := append([]byte("CX2\x00"), 0, 0, 0, 1)
	encoded = codexRecordBlob(encoded, preparation)
	nonce := record.preparation.CopyRequest().Nonce
	encoded = codexRecordBlob(encoded, nonce[:])
	apply := record.apply.CopyWire()
	if apply == nil {
		return binary.BigEndian.AppendUint32(encoded, 0), nil
	}
	if !bytes.Equal(apply.Preparation.Codex.DeclarationSha256, record.preparation.CopyWire().Codex.DeclarationSha256) {
		clear(encoded)
		return nil, ErrNativeUnavailable
	}
	prepared, _ := nativesessionauth.HashCodexPreparation(record.preparation)
	boundPrepared, _ := nativesessionauth.HashCodexPreparation(record.apply.Preparation())
	if prepared != boundPrepared {
		clear(encoded)
		return nil, ErrNativeUnavailable
	}
	local, err := record.apply.LocalBinding().CopyCanonical()
	defer clear(local)
	if err != nil || len(local) > 8192 {
		clear(encoded)
		return nil, ErrNativeUnavailable
	}
	encoded = binary.BigEndian.AppendUint32(encoded, 1)
	encoded = codexRecordBlob(encoded, local)
	encoded = codexRecordBlob(encoded, apply.IntentCommitment)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(apply.IntentExpiresAtUnixMs))
	encoded = binary.BigEndian.AppendUint64(encoded, record.apply.ConfirmationRevision())
	if len(encoded) > 12396 || len(encoded) > 16<<10 {
		clear(encoded)
		return nil, ErrNativeQuota
	}
	return encoded, nil
}

func nativeDecodeCodexPreparation(encoded []byte, consumer nativesessionauth.ConsumerFields, nonce [32]byte) (nativesessionauth.CodexPreparation, error) {
	reader := codexRecordReader{rest: encoded}
	owner, origin := reader.text(512), reader.text(2048)
	session, operation := reader.exact(32), reader.exact(16)
	request := nativesessionauth.CodexPrepareRequest{}
	request.OwnerID = owner
	request.AssetID = reader.text(26)
	request.AssetRevision = reader.millis()
	request.AgentID = reader.text(16)
	if reader.number() != 1 {
		return nativesessionauth.CodexPreparation{}, ErrNativeUnavailable
	}
	request.SchemaVersion = reader.number()
	request.Adapter.APIVersion = reader.number()
	request.Adapter.PluginID = reader.text(128)
	request.Adapter.PluginVersion = reader.text(128)
	request.Adapter.DefinitionDigest = reader.text(64)
	if reader.number() != 1 {
		return nativesessionauth.CodexPreparation{}, ErrNativeUnavailable
	}
	request.Adapter.Capability = reader.text(128)
	request.Target = reader.number()
	request.Scope = reader.number()
	request.ProfileID = reader.text(128)
	request.SelectionRevision = reader.wide()
	if reader.number() != 1 || reader.number() != 3 {
		return nativesessionauth.CodexPreparation{}, ErrNativeUnavailable
	}
	expiry, keyHash, id, prepared := reader.millis(), reader.exact(32), reader.text(64), reader.millis()
	request.ContractVersion = reader.number()
	digest := reader.exact(32)
	if !reader.done() || owner != consumer.OwnerID || origin != consumer.ServerOrigin || !bytes.Equal(session, consumer.SessionBinding[:]) || !bytes.Equal(operation, consumer.OperationID[:]) {
		return nativesessionauth.CodexPreparation{}, ErrNativeUnavailable
	}
	request.OperationID = consumer.OperationID
	request.Nonce = nonce
	value, err := nativesessionauth.NewCodexPreparation(consumer, request, id, prepared, expiry, [32]byte(digest))
	if err != nil || !bytes.Equal(keyHash, value.CopyWire().ConsumerKeySha256) {
		return nativesessionauth.CodexPreparation{}, ErrNativeUnavailable
	}
	canonical, err := value.CopyCanonical()
	defer clear(canonical)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return nativesessionauth.CodexPreparation{}, ErrNativeUnavailable
	}
	return value, nil
}

func nativeDecodeCodexLocal(encoded []byte, digest [32]byte) (nativesessionauth.CodexLocalBinding, error) {
	reader := codexRecordReader{rest: encoded}
	if reader.text(64) != "cognate/native-codex-local-binding/v1" {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	wire := &nativev1.CodexLocalBindingV1{BindingVersion: reader.number()}
	storedDigest := reader.exact(32)
	wire.NativeProfileId = reader.text(128)
	wire.NativeProfileSha256 = bytes.Clone(reader.exact(32))
	wire.TemplateSha256 = bytes.Clone(reader.exact(32))
	wire.EffectiveScope = nativev1.CodexEffectiveScope(reader.number())
	wire.ProviderOwnership = nativev1.CodexOwnership(reader.number())
	wire.CatalogOwnership = nativev1.CodexOwnership(reader.number())
	count := reader.number()
	if count > 32 {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	for range count {
		wire.ManagedModels = append(wire.ManagedModels, reader.text(128))
	}
	wire.AuthStore = nativev1.CodexAuthStore(reader.number())
	wire.KeyringState = nativev1.CodexKeyringState(reader.number())
	wire.FileLoginState = nativev1.CodexFileLoginState(reader.number())
	wire.LocalAuthPolicy = reader.text(64)
	login := reader.number()
	if login > 1 {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	wire.RequiresOpenaiAuth = new(login == 1)
	wire.ObservationWitness = bytes.Clone(reader.exact(32))
	if reader.number() != 3 {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	for range 3 {
		baseline := &nativev1.CodexRoleBaseline{Role: nativev1.CodexRole(reader.number())}
		presence := reader.number()
		hash := reader.blob(32)
		switch presence {
		case 1:
			if len(hash) != 0 {
				return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
			}
			baseline.State = &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}
		case 2:
			if len(hash) != 32 {
				return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
			}
			baseline.State = &nativev1.CodexRoleBaseline_Present{Present: &nativev1.CodexPresent{Sha256: bytes.Clone(hash)}}
		case 3:
			if len(hash) != 0 {
				return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
			}
			baseline.State = &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}
		default:
			return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
		}
		wire.Baselines = append(wire.Baselines, baseline)
	}
	if !reader.done() || subtle.ConstantTimeCompare(storedDigest, digest[:]) != 1 {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	value, err := nativesessionauth.NewCodexLocalBinding(digest, wire)
	if err != nil {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	canonical, err := value.CopyCanonical()
	defer clear(canonical)
	if err != nil || !bytes.Equal(encoded, canonical) {
		return nativesessionauth.CodexLocalBinding{}, ErrNativeUnavailable
	}
	return value, nil
}

func nativeDecodeCodexRecord(plaintext []byte, consumer nativesessionauth.ConsumerFields) (nativeCodexRecord, error) {
	if len(plaintext) > 16<<10 || len(plaintext) < 8 || !bytes.Equal(plaintext[:8], []byte{'C', 'X', '2', 0, 0, 0, 0, 1}) {
		return nativeCodexRecord{}, ErrNativeUnavailable
	}
	reader := codexRecordReader{rest: plaintext[8:]}
	encoded := reader.blob(4096)
	nonce := reader.exact(32)
	bound := reader.number()
	if reader.failed || bound > 1 {
		return nativeCodexRecord{}, ErrNativeUnavailable
	}
	preparation, err := nativeDecodeCodexPreparation(encoded, consumer, [32]byte(nonce))
	if err != nil {
		return nativeCodexRecord{}, err
	}
	record := nativeCodexRecord{preparation: preparation}
	if bound == 1 {
		encoded = reader.blob(8192)
		commitment := reader.exact(32)
		expiry, revision := reader.millis(), reader.wide()
		local, err := nativeDecodeCodexLocal(encoded, [32]byte(preparation.CopyWire().Codex.DeclarationSha256))
		if err != nil {
			return nativeCodexRecord{}, err
		}
		record.apply, err = nativesessionauth.NewCodexApply(preparation, local, revision, expiry)
		actual := record.apply.Commitment()
		if err != nil || subtle.ConstantTimeCompare(actual[:], commitment) != 1 {
			return nativeCodexRecord{}, ErrNativeUnavailable
		}
	}
	if !reader.done() {
		return nativeCodexRecord{}, ErrNativeUnavailable
	}
	return record, nil
}

func (store *Store) nativeSealCodexPrepared(row *ent.NativeApplyPreparation, consumer nativesessionauth.ConsumerFields, record nativeCodexRecord) error {
	plaintext, err := nativeEncodeCodexRecord(record)
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

func (store *Store) nativeLoadCodexPrepared(ctx context.Context, tx *ent.Tx, child *ent.NativeConsumerSession, consumer nativesessionauth.ConsumerFields, id string) (*ent.NativeApplyPreparation, nativeCodexRecord, error) {
	if !validPackKey(id) {
		return nil, nativeCodexRecord{}, ErrNativeInvalid
	}
	row, err := tx.NativeApplyPreparation.Query().Where(nativeapplypreparation.PublicIDEQ(id), nativeapplypreparation.NativeSessionIDEQ(child.ID), nativeapplypreparation.UserIDEQ(child.UserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nativeCodexRecord{}, ErrConfigurationNotFound
	}
	if err != nil {
		return nil, nativeCodexRecord{}, err
	}
	if !nativePreparationRecordValid(row) || len(row.Ciphertext) > 16<<10 {
		return nil, nativeCodexRecord{}, ErrNativeUnavailable
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, row.Ciphertext, nativePreparedAAD(row, consumer))
	defer clear(plaintext)
	if err != nil {
		return nil, nativeCodexRecord{}, ErrNativeUnavailable
	}
	record, err := nativeDecodeCodexRecord(plaintext, consumer)
	if err != nil {
		return nil, nativeCodexRecord{}, ErrNativeUnavailable
	}
	wire := record.preparation.CopyWire()
	if wire.PrepareId != row.PublicID || wire.PreparedAtUnixMs != row.IssuedMs || wire.PrepareExpiresAtUnixMs != row.ExpiresMs || row.ExpiresMs > child.ExpiresMs || !bytes.Equal(row.OperationID, child.OperationID) {
		return nil, nativeCodexRecord{}, ErrNativeUnavailable
	}
	digest, err := nativesessionauth.CodexPrepareFieldsDigest(nativesessionauth.CodexPrepareFields{Consumer: consumer, Request: record.preparation.CopyRequest()})
	if err != nil || subtle.ConstantTimeCompare(row.RequestDigest, digest[:]) != 1 {
		return nil, nativeCodexRecord{}, ErrNativeUnavailable
	}
	bound := record.apply.CopyWire() != nil
	if row.Phase == nativeapplypreparation.PhaseBoundNoSecret || row.Phase == nativeapplypreparation.PhasePatchReady && bound || row.Phase == nativeapplypreparation.PhaseBoundWithGrant && !bound {
		return nil, nativeCodexRecord{}, ErrNativeUnavailable
	}
	return row, record, nil
}
