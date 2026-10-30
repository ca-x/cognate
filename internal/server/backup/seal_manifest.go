package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"hash"
	"io"
	"iter"
	"log/slog"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"encoding/json/jsontext"
)

type sealRoot struct {
	Format              string                   `json:"format"`
	Version             uint32                   `json:"version"`
	Suite               uint32                   `json:"suite"`
	AppVersion          string                   `json:"app_version"`
	SchemaSHA256        string                   `json:"schema_sha256"`
	InstanceID          string                   `json:"instance_id"`
	BackupID            string                   `json:"backup_id"`
	AttemptID           string                   `json:"attempt_id"`
	Scope               string                   `json:"scope"`
	CutID               string                   `json:"cut_id"`
	CutMode             string                   `json:"cut_mode"`
	CachePolicy         string                   `json:"cache_policy"`
	MasterKeyID         string                   `json:"master_key_id"`
	Database            SealFileDescription      `json:"database"`
	Inventory           SealInventoryDescription `json:"inventory"`
	Archives            uint32                   `json:"archives"`
	ClosureSHA256       string                   `json:"closure_sha256"`
	ExternalConfigNames []string                 `json:"external_config_names"`
	Totals              sealTotals               `json:"totals"`
}
type sealTotals struct {
	DataObjects     uint32 `json:"data_objects"`
	ManifestObjects uint32 `json:"manifest_objects"`
	PlaintextBytes  int64  `json:"plaintext_bytes"`
	CiphertextBytes int64  `json:"ciphertext_bytes_without_complete"`
}
type sealPagePin struct {
	Ordinal uint32 `json:"ordinal"`
	Bytes   int64  `json:"ciphertext_bytes"`
	Hash    string `json:"ciphertext_sha256"`
}
type sealPageHeader struct {
	Kind    string `json:"kind"`
	Version uint32 `json:"version"`
	Ordinal uint32 `json:"ordinal"`
	Records uint32 `json:"records"`
}
type sealComplete struct {
	Format         string `json:"format"`
	Version        uint32 `json:"version"`
	Suite          uint32 `json:"suite"`
	InstanceID     string `json:"instance_id"`
	BackupID       string `json:"backup_id"`
	AttemptID      string `json:"attempt_id"`
	BootstrapBytes int64  `json:"bootstrap_bytes"`
	BootstrapHash  string `json:"bootstrap_sha256"`
	RootBytes      int64  `json:"root_ciphertext_bytes"`
	RootHash       string `json:"root_ciphertext_sha256"`
	Objects        uint32 `json:"object_count_without_complete"`
	CipherBytes    int64  `json:"ciphertext_bytes_without_complete"`
}
type sealOrder struct {
	next     uint32
	kind     byte
	id       [32]byte
	offset   int64
	previous int64
	counts   [3]uint32
	lengths  [3]int64
	archives uint32
}
type sealOpenState struct {
	mutex                                        sync.Mutex
	changed                                      chan struct{}
	engine                                       *sealEngine
	ctx                                          context.Context
	cancel                                       context.CancelFunc
	stop                                         func() bool
	identity                                     attemptData
	dek                                          [32]byte
	prefix                                       string
	catalog                                      *os.File
	catalogCreated, catalogReadOnly              bool
	catalogCharge                                *sealCharge
	complete                                     sealComplete
	root                                         sealRoot
	rootLength                                   int64
	pageCount, nextPage, dataCount, nextData     uint32
	order                                        sealOrder
	cipherBytes, plainBytes, heldPlain, heldDisk int64
	logicalHash                                  hash.Hash
	previous                                     sealRecord
	phase                                        byte
	closed, failed, finished                     bool
	active                                       int
	closeOnce                                    sync.Once
	closeError                                   error
}

type SealFileDescription struct {
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
	Extents uint32 `json:"extents"`
}
type SealInventoryDescription struct {
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
	Extents uint32 `json:"extents"`
	Records uint32 `json:"records"`
	Owners  uint32 `json:"owners"`
}
type SealMetadata struct {
	AppVersion          string
	SchemaSHA256        string
	CutID               string
	MasterKeyID         string
	Database            SealFileDescription
	Inventory           SealInventoryDescription
	Archives            uint32
	ClosureSHA256       string
	ExternalConfigNames []string
}

func (SealMetadata) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private backup metadata]")
}
func (SealMetadata) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (SealMetadata) LogValue() slog.Value         { return slog.StringValue("[private backup metadata]") }

type SealExtentRecord struct {
	Kind             string `json:"kind"`
	Ordinal          uint32 `json:"ordinal"`
	FileKind         string `json:"file_kind"`
	FileID           string `json:"file_id"`
	Offset           int64  `json:"offset"`
	PlaintextBytes   int64  `json:"plaintext_bytes"`
	PlaintextSHA256  string `json:"plaintext_sha256"`
	CiphertextBytes  int64  `json:"ciphertext_bytes"`
	CiphertextSHA256 string `json:"ciphertext_sha256"`
}
type OpenSession struct{ get func() *sealOpenState }
type CryptoVerifiedBundle struct{ get func() *sealOpenState }

func (engine SealEngine) SealPage(ctx context.Context, attempt SealAttempt, ordinal, records uint32, source iter.Seq2[SealExtentRecord, error]) (result SealedFile, returnedError error) {
	if source == nil || records == 0 || ordinal == 0 {
		return result, ErrInvalid
	}
	state, scoped, release, err := engine.metadataCall(ctx, attempt, 1)
	if err != nil {
		return result, err
	}
	defer release()
	defer func() {
		if returnedError != nil || result.get == nil {
			state.mutex.Lock()
			state.poison = true
			state.mutex.Unlock()
		}
	}()
	if ordinal != state.pageCount+1 || ordinal >= state.engine.limits.MaxObjects {
		return result, ErrInvalid
	}
	result, err = state.engine.metadataFile(scoped, state, 2, ordinal, sealFrame-192, func(writer io.Writer) error {
		if err := writeSealJSON(scoped, writer, sealPageHeader{Kind: "page", Version: 1, Ordinal: ordinal, Records: records}); err != nil {
			return err
		}
		var count uint32
		for row, rowError := range source {
			if rowError != nil {
				return ErrInvalid
			}
			if count >= records {
				return ErrInvalid
			}
			record, err := decodeExtentRecord(row)
			if err != nil {
				return err
			}
			existing, err := state.record(0, record.ordinal)
			if err != nil || existing.length != record.length || existing.plainHash != record.plainHash || existing.cipherBytes != record.cipherBytes || existing.cipherHash != record.cipherHash {
				return ErrInvalid
			}
			if err = state.order.accept(record); err != nil {
				return err
			}
			encoded := record.encode()
			if written, writeErr := state.catalog.WriteAt(encoded[:], int64(state.locations[0][record.ordinal]-1)*128); writeErr != nil || written != 128 {
				return ErrInvalid
			}
			if err = writeSealJSON(scoped, writer, row); err != nil {
				return err
			}
			count++
		}
		if count != records {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	ref := result.Ref().read()
	file, err := sealOpen(state.engine.directory, sealObjectName(state.identity, 2, ordinal), false)
	if err != nil {
		return SealedFile{}, mergeSealCleanup(err, result.Close())
	}
	var encoded [96]byte
	_, readErr := file.ReadAt(encoded[:], 0)
	closeErr := state.engine.closeFile(file)
	if readErr != nil || closeErr != nil {
		return SealedFile{}, mergeSealCleanup(ErrSealUncertain, result.Close())
	}
	if err = scoped.Err(); err != nil {
		return SealedFile{}, mergeSealCleanup(err, result.Close())
	}
	header, err := parseSealHeader(encoded[:])
	if err != nil {
		return SealedFile{}, mergeSealCleanup(err, result.Close())
	}
	if err = state.addRecord(sealRecord{ordinal: ordinal, length: header.length, cipherBytes: ref.size, cipherHash: ref.digest}, 1); err != nil {
		return SealedFile{}, mergeSealCleanup(err, result.Close())
	}
	state.pageCount++
	return result, nil
}

func (engine SealEngine) metadataCall(ctx context.Context, attempt SealAttempt, phase byte) (*sealAttempt, context.Context, func(), error) {
	if engine.get == nil || attempt.get == nil {
		return nil, nil, nil, ErrInvalid
	}
	state := attempt.get()
	if state.engine != engine.get() {
		return nil, nil, nil, ErrInvalid
	}
	scoped, release, err := state.engine.enter(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	state.mutex.Lock()
	if state.closed || state.poison || state.phase > phase || phase == 2 && state.phase != 1 || phase == 3 && state.phase != 2 {
		state.mutex.Unlock()
		release()
		return nil, nil, nil, ErrInvalid
	}
	if state.active != 0 {
		state.mutex.Unlock()
		release()
		return nil, nil, nil, ErrBusy
	}
	state.phase = phase
	state.active++
	state.mutex.Unlock()
	scoped, cancel := context.WithCancel(scoped)
	stop := context.AfterFunc(state.ctx, cancel)
	return state, scoped, func() {
		stop()
		cancel()
		state.mutex.Lock()
		state.active--
		close(state.changed)
		state.changed = make(chan struct{})
		state.mutex.Unlock()
		release()
	}, nil
}

func (engine SealEngine) SealRoot(ctx context.Context, attempt SealAttempt, metadata SealMetadata) (result SealedFile, returnedError error) {
	if len(metadata.ExternalConfigNames) > 32 {
		return result, ErrInvalid
	}
	state, scoped, release, err := engine.metadataCall(ctx, attempt, 2)
	if err != nil {
		return result, err
	}
	defer release()
	defer func() {
		if returnedError != nil || result.get == nil {
			state.mutex.Lock()
			state.poison = true
			state.mutex.Unlock()
		}
	}()
	root := sealRoot{Format: "cognate-backup-root", Version: 1, Suite: 1, AppVersion: metadata.AppVersion, SchemaSHA256: metadata.SchemaSHA256, InstanceID: hex.EncodeToString(state.identity.instance[:]), BackupID: hex.EncodeToString(state.identity.backup[:]), AttemptID: hex.EncodeToString(state.identity.attempt[:]), Scope: "instance", CutID: metadata.CutID, CutMode: "managed-exclusive-staged", CachePolicy: "authoritative-closure", MasterKeyID: metadata.MasterKeyID, Database: metadata.Database, Inventory: metadata.Inventory, Archives: metadata.Archives, ClosureSHA256: metadata.ClosureSHA256, ExternalConfigNames: slices.Clone(metadata.ExternalConfigNames)}
	if err = root.valid(state.identity, state.engine.limits.MaxInventoryRecords); err != nil {
		return result, err
	}
	if state.dataCount != state.order.next || state.pageCount == 0 || state.order.counts[0] != root.Database.Extents || state.order.counts[1] != root.Inventory.Extents || state.order.lengths[0] != root.Database.Bytes || state.order.lengths[1] != root.Inventory.Bytes || state.order.archives != root.Archives {
		return result, ErrInvalid
	}
	if err = state.verifyData(scoped, root); err != nil {
		return result, err
	}
	root.Totals = sealTotals{DataObjects: state.dataCount, ManifestObjects: state.pageCount + 1, CiphertextBytes: 156}
	for group := 0; group < 2; group++ {
		count := state.dataCount
		start := uint32(0)
		if group == 1 {
			count = state.pageCount
			start = 1
		}
		for index := start; index < count+start; index++ {
			record, recordErr := state.record(group, index)
			if recordErr != nil {
				return result, recordErr
			}
			root.Totals.PlaintextBytes += record.length
			root.Totals.CiphertextBytes += record.cipherBytes
		}
	}
	basePlain, baseCipher := root.Totals.PlaintextBytes, root.Totals.CiphertextBytes
	var length int64
	for iteration := 0; iteration < 12; iteration++ {
		counter := &sealCountWriter{}
		if err = writeSealRoot(scoped, counter, root, state); err != nil {
			return result, err
		}
		next := counter.count
		if next == length {
			break
		}
		length = next
		cipherBytes, sizeErr := sealCipherSize(length)
		if sizeErr != nil {
			return result, sizeErr
		}
		root.Totals.PlaintextBytes = basePlain + length
		root.Totals.CiphertextBytes = baseCipher + cipherBytes
		if iteration == 11 {
			return result, ErrInvalid
		}
	}
	if root.Totals.DataObjects+root.Totals.ManifestObjects > state.engine.limits.MaxObjects {
		return result, ErrSealBudget
	}
	result, err = state.engine.metadataFile(scoped, state, 3, 0, sealFrame-192, func(writer io.Writer) error { return writeSealRoot(scoped, writer, root, state) })
	if err != nil {
		return result, err
	}
	state.root = result.Ref()
	state.manifest = root
	return result, nil
}

func (engine SealEngine) SealComplete(ctx context.Context, attempt SealAttempt) (result SealedFile, returnedError error) {
	state, scoped, release, err := engine.metadataCall(ctx, attempt, 3)
	if err != nil {
		return result, err
	}
	defer release()
	defer func() {
		if returnedError != nil || result.get == nil {
			state.mutex.Lock()
			state.poison = true
			state.mutex.Unlock()
		}
	}()
	if state.root.read == nil {
		return result, ErrInvalid
	}
	root, bootstrap := state.root.read(), state.bootstrap.read()
	complete := sealComplete{Format: "cognate-backup-complete", Version: 1, Suite: 1, InstanceID: hex.EncodeToString(state.identity.instance[:]), BackupID: hex.EncodeToString(state.identity.backup[:]), AttemptID: hex.EncodeToString(state.identity.attempt[:]), BootstrapBytes: 156, BootstrapHash: hex.EncodeToString(bootstrap.digest[:]), RootBytes: root.size, RootHash: hex.EncodeToString(root.digest[:]), Objects: 1 + state.manifest.Totals.DataObjects + state.manifest.Totals.ManifestObjects, CipherBytes: state.manifest.Totals.CiphertextBytes}
	for group := 0; group < 2; group++ {
		count, start := state.dataCount, uint32(0)
		purpose := byte(1)
		if group == 1 {
			count, start, purpose = state.pageCount, 1, 2
		}
		for index := start; index < count+start; index++ {
			record, err := state.record(group, index)
			if err != nil {
				return result, err
			}
			if err = state.checkSealed(scoped, purpose, index, record.cipherBytes, record.cipherHash, nil); err != nil {
				return result, err
			}
		}
	}
	if err = state.checkSealed(scoped, 0, 0, bootstrap.size, bootstrap.digest, nil); err != nil {
		return result, err
	}
	if err = state.checkSealed(scoped, 3, 0, root.size, root.digest, nil); err != nil {
		return result, err
	}
	return state.engine.metadataFile(scoped, state, 4, 0, 16384-192, func(writer io.Writer) error { return writeSealJSON(scoped, writer, complete) })
}
func (OpenSession) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private backup open session]")
}
func (OpenSession) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (OpenSession) LogValue() slog.Value         { return slog.StringValue("[private backup open session]") }
func (CryptoVerifiedBundle) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private crypto verified bundle]")
}
func (CryptoVerifiedBundle) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (CryptoVerifiedBundle) LogValue() slog.Value {
	return slog.StringValue("[private crypto verified bundle]")
}

func (engine SealEngine) BeginOpen(ctx context.Context, bootstrap, complete *os.File, expected Attempt) (result OpenSession, returnedError error) {
	if engine.get == nil || expected.read == nil || bootstrap == nil || complete == nil {
		return result, ErrInvalid
	}
	owner := engine.get()
	scoped, release, err := owner.enter(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	identity := expected.read()
	secret, bootstrapHash, err := owner.unwrapBootstrap(scoped, bootstrap, identity)
	if err != nil {
		return result, err
	}
	keep, cancel := context.WithTimeout(ctx, owner.limits.OperationTimeout)
	state := &sealOpenState{changed: make(chan struct{}), engine: owner, ctx: keep, cancel: cancel, identity: identity, dek: secret, nextPage: 1}
	clear(secret[:])
	state.stop = context.AfterFunc(owner.ctx, cancel)
	owner.directory.mutex.Lock()
	owner.directory.refs++
	owner.directory.mutex.Unlock()
	defer func() {
		if returnedError != nil {
			returnedError = mergeSealCleanup(returnedError, state.close())
		}
	}()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, ErrUnavailable
	}
	state.prefix = fmt.Sprintf("o-%x-", random)
	state.catalogCharge, err = owner.reserve(0, 0, int64(owner.limits.MaxObjects)*128, 0)
	if err != nil {
		return result, err
	}
	state.catalog, err = sealOpen(owner.directory, state.prefix+"index", true)
	if err != nil {
		return result, err
	}
	state.catalogCreated = true
	info, err := sealBorrow(complete)
	if err != nil {
		return result, err
	}
	if info.Size() > 16384 || info.Size() <= 192 {
		return result, ErrInvalid
	}
	name, charge, header, err := state.decryptPrivate(scoped, complete, 4, 0, info.Size(), [32]byte{})
	if err != nil {
		return result, err
	}
	input, err := sealOpen(owner.directory, name, false)
	if err != nil {
		if cleanupErr := owner.removePrivate(name, charge, nil); cleanupErr != nil {
			return result, cleanupErr
		}
		return result, err
	}
	data, readErr := readSealSmall(input, 16384)
	closeErr := owner.closeFile(input)
	if cleanupErr := owner.removePrivate(name, charge, closeErr); cleanupErr != nil {
		return result, cleanupErr
	}
	if readErr != nil || scoped.Err() != nil {
		return result, sealError(scoped, readErr)
	}
	if header.purpose != 4 || strictSealJSON(data, &state.complete) != nil || state.complete.valid(identity, bootstrapHash, owner.limits) != nil {
		return result, ErrInvalid
	}
	state.cipherBytes = 156
	owner.mutex.Lock()
	owner.sessions[state] = struct{}{}
	owner.mutex.Unlock()
	return OpenSession{get: func() *sealOpenState { return state }}, nil
}

func (engine *sealEngine) unwrapBootstrap(ctx context.Context, file *os.File, identity attemptData) (secret [32]byte, digest [32]byte, err error) {
	info, err := sealBorrow(file)
	if err != nil || info.Size() != 156 {
		return secret, digest, ErrInvalid
	}
	var encoded [156]byte
	if count, readErr := file.ReadAt(encoded[:], 0); readErr != nil || count != 156 {
		return secret, digest, ErrInvalid
	}
	if string(encoded[:8]) != "CGBKBT01" || binary.BigEndian.Uint16(encoded[8:10]) != 1 || binary.BigEndian.Uint16(encoded[10:12]) != 1 || !bytes.Equal(encoded[12:16], make([]byte, 4)) || !bytes.Equal(encoded[16:32], identity.instance[:]) || !bytes.Equal(encoded[32:48], identity.backup[:]) || !bytes.Equal(encoded[48:64], identity.attempt[:]) || binary.BigEndian.Uint32(encoded[80:84]) != sealFrame || binary.BigEndian.Uint32(encoded[84:88]) != 60 || !bytes.Equal(encoded[88:96], make([]byte, 8)) {
		return secret, digest, ErrInvalid
	}
	engine.key.mutex.Lock()
	if !bytes.Equal(encoded[64:80], engine.key.id[:]) {
		engine.key.mutex.Unlock()
		return secret, digest, ErrInvalid
	}
	key, deriveErr := sealDerive(engine.key.material[:], identity.attempt, "cognate/f4/bootstrap-key/v1\x00", encoded[:96])
	engine.key.mutex.Unlock()
	if deriveErr != nil {
		return secret, digest, deriveErr
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return secret, digest, ErrInvalid
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return secret, digest, ErrInvalid
	}
	plain, err := aead.Open(nil, nil, encoded[96:], append([]byte("cognate/f4/bootstrap-aad/v1\x00"), encoded[:96]...))
	if err != nil || len(plain) != 32 {
		return secret, digest, ErrInvalid
	}
	defer clear(plain)
	if err = sealRepin(file, info); err != nil {
		return secret, digest, err
	}
	if err = ctx.Err(); err != nil {
		return secret, digest, err
	}
	copy(secret[:], plain)
	return secret, sha256.Sum256(encoded[:]), nil
}

func (complete sealComplete) valid(identity attemptData, bootstrapHash [32]byte, limits SealLimits) error {
	if complete.Format != "cognate-backup-complete" || complete.Version != 1 || complete.Suite != 1 || complete.InstanceID != hex.EncodeToString(identity.instance[:]) || complete.BackupID != hex.EncodeToString(identity.backup[:]) || complete.AttemptID != hex.EncodeToString(identity.attempt[:]) || complete.BootstrapBytes != 156 || complete.BootstrapHash != hex.EncodeToString(bootstrapHash[:]) || complete.RootBytes <= 192 || complete.RootBytes > sealFrame || !sealHex(complete.RootHash, 32) || complete.Objects < 5 || complete.Objects > limits.MaxObjects+1 || complete.CipherBytes < complete.RootBytes+156 || complete.CipherBytes > limits.TotalCipherBytes {
		return ErrInvalid
	}
	return nil
}

func (state *sealOpenState) enter(ctx context.Context) (context.Context, func(), error) {
	scoped, release, err := state.engine.enter(ctx)
	if err != nil {
		return nil, nil, err
	}
	state.mutex.Lock()
	if state.closed || state.failed || state.ctx.Err() != nil {
		state.mutex.Unlock()
		release()
		return nil, nil, ErrClosed
	}
	if state.active != 0 {
		state.mutex.Unlock()
		release()
		return nil, nil, ErrBusy
	}
	state.active++
	state.mutex.Unlock()
	scoped, cancel := context.WithCancel(scoped)
	stop := context.AfterFunc(state.ctx, cancel)
	return scoped, func() {
		stop()
		cancel()
		state.mutex.Lock()
		state.active--
		close(state.changed)
		state.changed = make(chan struct{})
		state.mutex.Unlock()
		release()
	}, nil
}

func (state *sealOpenState) nextRef() (ObjectRef, byte, error) {
	attempt, _ := NewAttempt(state.identity.instance, state.identity.backup, state.identity.attempt)
	var kind ObjectKind
	var purpose byte
	var ordinal uint32
	var size int64
	var digest [32]byte
	switch state.phase {
	case 0:
		kind, purpose, size = ManifestPage, 3, state.complete.RootBytes
		_, _ = hex.Decode(digest[:], []byte(state.complete.RootHash))
	case 1:
		kind, purpose, ordinal = ManifestPage, 2, state.nextPage
		record, err := readSealRecord(state.catalog, int64(state.engine.limits.MaxObjects-ordinal)*128)
		if err != nil {
			return ObjectRef{}, 0, err
		}
		size, digest = record.cipherBytes, record.cipherHash
	case 2:
		kind, purpose, ordinal = DataExtent, 1, state.nextData
		record, err := readSealRecord(state.catalog, int64(ordinal)*128)
		if err != nil {
			return ObjectRef{}, 0, err
		}
		size, digest = record.cipherBytes, record.cipherHash
	case 3:
		return ObjectRef{}, 0, nil
	default:
		return ObjectRef{}, 0, ErrInvalid
	}
	ref, err := NewObjectRef(attempt, kind, ordinal, size, digest)
	return ref, purpose, err
}
func (session OpenSession) NextRef(ctx context.Context) (ObjectRef, error) {
	if session.get == nil {
		return ObjectRef{}, ErrInvalid
	}
	state := session.get()
	_, release, err := state.enter(ctx)
	if err != nil {
		return ObjectRef{}, err
	}
	defer release()
	ref, _, err := state.nextRef()
	return ref, err
}

func (session OpenSession) AcceptFile(ctx context.Context, source *os.File) (returnedError error) {
	if session.get == nil || source == nil {
		return ErrInvalid
	}
	state := session.get()
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	defer func() {
		if returnedError != nil {
			state.mutex.Lock()
			state.failed = true
			state.mutex.Unlock()
		}
	}()
	ref, purpose, err := state.nextRef()
	if err != nil || ref.read == nil {
		return ErrInvalid
	}
	expected := ref.read()
	name, charge, header, err := state.decryptPrivate(scoped, source, purpose, expected.ordinal, expected.size, expected.digest)
	if err != nil {
		return err
	}
	retained := false
	var closeError error
	defer func() {
		if !retained {
			if cleanupErr := state.engine.removePrivate(name, charge, closeError); cleanupErr != nil {
				returnedError = cleanupErr
			}
		}
	}()
	input, err := sealOpen(state.engine.directory, name, false)
	if err != nil {
		return err
	}
	if purpose == 3 {
		err = state.readRoot(input, header.length)
	} else if purpose == 2 {
		err = state.readPage(input, expected.ordinal)
	} else {
		record, readErr := readSealRecord(state.catalog, int64(expected.ordinal)*128)
		if readErr != nil {
			err = readErr
		} else if record.length != header.length {
			err = ErrInvalid
		} else {
			err = state.hashData(scoped, input, record)
		}
	}
	closeError = state.engine.closeFile(input)
	if closeError != nil {
		return ErrSealUncertain
	}
	if err != nil {
		return sealError(scoped, err)
	}
	if err = scoped.Err(); err != nil {
		return err
	}
	state.cipherBytes += expected.size
	state.plainBytes += header.length
	switch purpose {
	case 3:
		state.rootLength = header.length
		state.phase = 1
	case 2:
		state.nextPage++
		if state.nextPage > state.pageCount {
			if err = state.verifyDescriptors(); err != nil {
				return err
			}
			state.phase = 2
		}
	case 1:
		state.heldPlain += charge.plain
		state.heldDisk += charge.disk
		retained = true
		state.nextData++
		if state.nextData == state.dataCount {
			state.phase = 3
		}
	}
	return nil
}

func (state *sealOpenState) decryptPrivate(ctx context.Context, source *os.File, purpose byte, ordinal uint32, size int64, digest [32]byte) (name string, charge *sealCharge, header sealHeader, returnedError error) {
	if size > state.engine.limits.InputStagingBytes {
		return name, nil, header, ErrSealBudget
	}
	var encoded [96]byte
	if count, err := source.ReadAt(encoded[:], 0); err != nil || count != 96 {
		return name, nil, header, ErrInvalid
	}
	header, err := parseSealHeader(encoded[:])
	if err != nil || header.attempt != state.identity || header.purpose != purpose || header.ordinal != ordinal {
		return name, nil, header, ErrInvalid
	}
	charge, err = state.engine.reserve(header.length, 0, header.length, 0)
	if err != nil {
		return name, nil, header, err
	}
	name = fmt.Sprintf("%sp-%d-%08x", state.prefix, purpose, ordinal)
	output, err := sealOpen(state.engine.directory, name, true)
	if err != nil {
		charge.release()
		return name, nil, header, err
	}
	_, plainHash, err := decryptSealFile(ctx, source, state.identity, purpose, ordinal, state.dek[:], size, digest, output)
	if err == nil {
		err = state.engine.ops.sync(output)
	}
	closeErr := state.engine.closeFile(output)
	if err == nil && closeErr != nil {
		err = ErrSealUncertain
	}
	if err == nil {
		err = state.engine.ops.sync(state.engine.directory.root)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && purpose == 1 {
		record, recordErr := readSealRecord(state.catalog, int64(ordinal)*128)
		if recordErr != nil || plainHash != record.plainHash {
			err = ErrInvalid
		}
	}
	if err != nil {
		if cleanupErr := state.engine.removePrivate(name, charge, closeErr); cleanupErr != nil {
			return name, nil, header, cleanupErr
		}
		return name, nil, header, sealError(ctx, err)
	}
	return name, charge, header, nil
}

func (state *sealOpenState) readRoot(file *os.File, length int64) error {
	if err := sealJSONEnd(file, length); err != nil {
		return err
	}
	decoder := jsontext.NewDecoder(&sealOffsetReader{ctx: state.ctx, file: file})
	token, err := decoder.ReadToken()
	if err != nil || token.Kind() != '{' {
		return ErrInvalid
	}
	target := reflect.ValueOf(&state.root).Elem()
	descriptor := target.Type()
	var seen uint64
	pages := false
	for decoder.PeekKind() != '}' {
		key, err := decoder.ReadToken()
		if err != nil || key.Kind() != '"' {
			return ErrInvalid
		}
		if key.String() == "pages" {
			if pages {
				return ErrInvalid
			}
			pages = true
			start, err := decoder.ReadToken()
			if err != nil || start.Kind() != '[' {
				return ErrInvalid
			}
			for decoder.PeekKind() != ']' {
				value, err := decoder.ReadValue()
				if err != nil {
					return ErrInvalid
				}
				var pin sealPagePin
				if strictSealJSON(value, &pin) != nil || pin.Ordinal != state.pageCount+1 || pin.Ordinal >= state.engine.limits.MaxObjects || pin.Bytes <= 192 || pin.Bytes > sealFrame || !sealHex(pin.Hash, 32) {
					return ErrInvalid
				}
				record := sealRecord{ordinal: pin.Ordinal, cipherBytes: pin.Bytes}
				_, _ = hex.Decode(record.cipherHash[:], []byte(pin.Hash))
				encoded := record.encode()
				if count, err := state.catalog.WriteAt(encoded[:], int64(state.engine.limits.MaxObjects-pin.Ordinal)*128); err != nil || count != 128 {
					return ErrInvalid
				}
				state.pageCount++
			}
			if _, err = decoder.ReadToken(); err != nil {
				return ErrInvalid
			}
			continue
		}
		found := -1
		for index := 0; index < descriptor.NumField(); index++ {
			if descriptor.Field(index).Tag.Get("json") == key.String() {
				found = index
				break
			}
		}
		if found < 0 || seen&(1<<found) != 0 {
			return ErrInvalid
		}
		seen |= 1 << found
		value, err := decoder.ReadValue()
		if err != nil || strictSealJSON(value, target.Field(found).Addr().Interface()) != nil {
			return ErrInvalid
		}
	}
	if _, err = decoder.ReadToken(); err != nil {
		return ErrInvalid
	}
	if _, err = decoder.ReadToken(); err != io.EOF {
		return ErrInvalid
	}
	if !pages || state.pageCount == 0 || seen != (uint64(1)<<descriptor.NumField())-1 {
		return ErrInvalid
	}
	if err = state.root.valid(state.identity, state.engine.limits.MaxInventoryRecords); err != nil {
		return err
	}
	totals := state.root.Totals
	if totals.DataObjects < 2 || totals.ManifestObjects != state.pageCount+1 || totals.DataObjects > state.engine.limits.MaxObjects-totals.ManifestObjects || totals.PlaintextBytes <= 0 || totals.PlaintextBytes > state.engine.limits.TotalPlainBytes || totals.CiphertextBytes != state.complete.CipherBytes || 1+totals.DataObjects+totals.ManifestObjects != state.complete.Objects {
		return ErrInvalid
	}
	state.dataCount = totals.DataObjects
	return nil
}

func (state *sealOpenState) readPage(file *os.File, ordinal uint32) error {
	reader := bufio.NewReaderSize(&sealOffsetReader{ctx: state.ctx, file: file}, 65536)
	line, err := readSealLine(reader)
	if err != nil {
		return err
	}
	var header sealPageHeader
	if strictSealJSON(line, &header) != nil || header.Kind != "page" || header.Version != 1 || header.Ordinal != ordinal || header.Records == 0 || header.Records > state.dataCount-state.order.next {
		return ErrInvalid
	}
	for index := uint32(0); index < header.Records; index++ {
		line, err = readSealLine(reader)
		if err != nil {
			return err
		}
		var row SealExtentRecord
		if strictSealJSON(line, &row) != nil {
			return ErrInvalid
		}
		record, err := decodeExtentRecord(row)
		if err != nil || state.order.accept(record) != nil {
			return ErrInvalid
		}
		encoded := record.encode()
		if count, err := state.catalog.WriteAt(encoded[:], int64(record.ordinal)*128); err != nil || count != 128 {
			return ErrInvalid
		}
	}
	if _, err = reader.ReadByte(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func (state *sealOpenState) verifyDescriptors() error {
	if state.order.next != state.dataCount || state.order.counts[0] != state.root.Database.Extents || state.order.counts[1] != state.root.Inventory.Extents || state.order.lengths[0] != state.root.Database.Bytes || state.order.lengths[1] != state.root.Inventory.Bytes || state.order.archives != state.root.Archives {
		return ErrInvalid
	}
	var plain, cipher int64
	for ordinal := uint32(0); ordinal < state.dataCount; ordinal++ {
		record, err := readSealRecord(state.catalog, int64(ordinal)*128)
		if err != nil {
			return err
		}
		plain += record.length
		cipher += record.cipherBytes
	}
	if state.plainBytes+plain != state.root.Totals.PlaintextBytes || state.cipherBytes+cipher != state.root.Totals.CiphertextBytes {
		return ErrInvalid
	}
	before, err := sealBorrow(state.catalog)
	if err != nil {
		return err
	}
	if state.engine.ops.sync(state.catalog) != nil {
		return ErrSealCleanup
	}
	file := state.catalog
	state.catalog = nil
	if state.engine.closeFile(file) != nil {
		return ErrSealUncertain
	}
	state.catalog, err = sealOpen(state.engine.directory, state.prefix+"index", false)
	if err != nil {
		return err
	}
	if err = sealRepin(state.catalog, before); err != nil {
		return err
	}
	state.catalogReadOnly = true
	return nil
}

func (state *sealOpenState) hashData(ctx context.Context, file *os.File, record sealRecord) error {
	if state.logicalHash == nil || record.kind != state.previous.kind || record.id != state.previous.id {
		if err := state.finishLogical(); err != nil {
			return err
		}
		state.logicalHash = sha256.New()
	}
	buffer := make([]byte, 32768)
	if _, err := io.CopyBuffer(state.logicalHash, &sealOffsetReader{ctx: ctx, file: file}, buffer); err != nil {
		return sealError(ctx, err)
	}
	state.previous = record
	return nil
}
func (state *sealOpenState) finishLogical() error {
	if state.logicalHash == nil {
		return nil
	}
	expected := state.root.Database.SHA256
	if state.previous.kind == 2 {
		expected = state.root.Inventory.SHA256
	} else if state.previous.kind == 3 {
		expected = hex.EncodeToString(state.previous.id[:])
	}
	if hex.EncodeToString(state.logicalHash.Sum(nil)) != expected {
		return ErrInvalid
	}
	return nil
}

func (session OpenSession) Finish(ctx context.Context) (CryptoVerifiedBundle, error) {
	if session.get == nil {
		return CryptoVerifiedBundle{}, ErrInvalid
	}
	state := session.get()
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return CryptoVerifiedBundle{}, err
	}
	defer release()
	if state.phase != 3 || !state.catalogReadOnly || state.nextData != state.dataCount || state.finishLogical() != nil || state.plainBytes != state.root.Totals.PlaintextBytes || state.cipherBytes != state.complete.CipherBytes {
		return CryptoVerifiedBundle{}, ErrInvalid
	}
	if err = scoped.Err(); err != nil {
		return CryptoVerifiedBundle{}, err
	}
	state.finished = true
	return CryptoVerifiedBundle{get: func() *sealOpenState { return state }}, nil
}

func (session OpenSession) Close() error {
	if session.get == nil {
		return nil
	}
	return session.get().close()
}
func (bundle CryptoVerifiedBundle) Close() error {
	if bundle.get == nil {
		return nil
	}
	return bundle.get().close()
}
func (state *sealOpenState) close() error {
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		state.closed = true
		state.cancel()
		if state.stop != nil {
			state.stop()
		}
		for state.active != 0 {
			changed := state.changed
			state.mutex.Unlock()
			<-changed
			state.mutex.Lock()
		}
		clear(state.dek[:])
		state.mutex.Unlock()
		for ordinal := uint32(0); ordinal < state.nextData; ordinal++ {
			if sealUnlink(state.engine.directory, fmt.Sprintf("%sp-1-%08x", state.prefix, ordinal)) != nil {
				state.closeError = ErrSealCleanup
			}
		}
		if state.catalog != nil {
			if state.engine.closeFile(state.catalog) != nil {
				state.closeError = ErrSealUncertain
			}
		}
		if state.catalogCreated {
			if sealUnlink(state.engine.directory, state.prefix+"index") != nil {
				state.closeError = mergeSealCleanup(state.closeError, ErrSealCleanup)
			}
		}
		state.engine.mutex.Lock()
		if state.engine.uncertain {
			state.closeError = ErrSealUncertain
		}
		state.engine.mutex.Unlock()
		if state.engine.ops.sync(state.engine.directory.root) != nil {
			state.closeError = mergeSealCleanup(state.closeError, ErrSealCleanup)
		}
		state.engine.finishCharge(state.catalogCharge, state.closeError)
		state.engine.finishCharge(&sealCharge{engine: state.engine, plain: state.heldPlain, disk: state.heldDisk}, state.closeError)
		if err := releaseSealDirectory(state.engine.directory); err != nil {
			state.closeError = mergeSealCleanup(state.closeError, err)
		}
		state.engine.mutex.Lock()
		delete(state.engine.sessions, state)
		state.engine.mutex.Unlock()
	})
	return state.closeError
}

func (bundle CryptoVerifiedBundle) Metadata(ctx context.Context) (SealMetadata, error) {
	if bundle.get == nil {
		return SealMetadata{}, ErrInvalid
	}
	state := bundle.get()
	_, release, err := state.enter(ctx)
	if err != nil {
		return SealMetadata{}, err
	}
	defer release()
	if !state.finished {
		return SealMetadata{}, ErrInvalid
	}
	root := state.root
	return SealMetadata{AppVersion: root.AppVersion, SchemaSHA256: root.SchemaSHA256, CutID: root.CutID, MasterKeyID: root.MasterKeyID, Database: root.Database, Inventory: root.Inventory, Archives: root.Archives, ClosureSHA256: root.ClosureSHA256, ExternalConfigNames: slices.Clone(root.ExternalConfigNames)}, nil
}

func (bundle CryptoVerifiedBundle) CopyFile(ctx context.Context, kind, id string, output io.Writer) (result int64, returnedError error) {
	if bundle.get == nil || output == nil {
		return 0, ErrInvalid
	}
	state := bundle.get()
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	if !state.finished {
		return 0, ErrInvalid
	}
	fileKind := byte(0)
	var fileID [32]byte
	switch kind {
	case "database":
		if id != "database" {
			return 0, ErrInvalid
		}
		fileKind = 1
	case "inventory":
		if id != "inventory" {
			return 0, ErrInvalid
		}
		fileKind = 2
	case "archive":
		fileKind = 3
		if !sealHex(id, 32) {
			return 0, ErrInvalid
		}
		_, _ = hex.Decode(fileID[:], []byte(id))
	default:
		return 0, ErrInvalid
	}
	for ordinal := uint32(0); ordinal < state.dataCount; ordinal++ {
		record, err := readSealRecord(state.catalog, int64(ordinal)*128)
		if err != nil {
			return 0, err
		}
		if record.kind != fileKind || record.id != fileID {
			continue
		}
		file, err := sealOpen(state.engine.directory, fmt.Sprintf("%sp-1-%08x", state.prefix, ordinal), false)
		if err != nil {
			return 0, err
		}
		hash := sha256.New()
		reader := &sealOffsetReader{ctx: scoped, file: file}
		count, copyErr := io.CopyBuffer(io.MultiWriter(output, hash), reader, make([]byte, 32768))
		closeErr := state.engine.closeFile(file)
		if closeErr != nil {
			return 0, ErrSealUncertain
		}
		if copyErr != nil || count != record.length || !bytes.Equal(hash.Sum(nil), record.plainHash[:]) {
			return 0, sealError(scoped, ErrInvalid)
		}
		result += count
	}
	if result == 0 {
		return 0, ErrInvalid
	}
	if err := scoped.Err(); err != nil {
		return 0, err
	}
	return result, nil
}

func readSealSmall(file *os.File, maximum int64) ([]byte, error) {
	info, err := sealBorrow(file)
	if err != nil || info.Size() < 2 || info.Size() > maximum {
		return nil, ErrInvalid
	}
	data := make([]byte, info.Size())
	if count, err := file.ReadAt(data, 0); err != nil || count != len(data) {
		return nil, ErrInvalid
	}
	if sealJSONEnd(file, info.Size()) != nil {
		return nil, ErrInvalid
	}
	return data, nil
}
func sealJSONEnd(file *os.File, length int64) error {
	if length < 2 {
		return ErrInvalid
	}
	var ending [2]byte
	if count, err := file.ReadAt(ending[:], length-2); err != nil || count != 2 || ending != [2]byte{'}', '\n'} {
		return ErrInvalid
	}
	return nil
}

type sealRecord struct {
	ordinal        uint32
	kind           byte
	id             [32]byte
	offset, length int64
	plainHash      [32]byte
	cipherBytes    int64
	cipherHash     [32]byte
}

func (record sealRecord) encode() (data [128]byte) {
	binary.BigEndian.PutUint32(data[:4], record.ordinal)
	data[4] = record.kind
	copy(data[5:37], record.id[:])
	binary.BigEndian.PutUint64(data[37:45], uint64(record.offset))
	binary.BigEndian.PutUint64(data[45:53], uint64(record.length))
	copy(data[53:85], record.plainHash[:])
	binary.BigEndian.PutUint64(data[85:93], uint64(record.cipherBytes))
	copy(data[93:125], record.cipherHash[:])
	return data
}

func (attempt *sealAttempt) addRecord(record sealRecord, group int) error {
	if attempt.entries >= attempt.engine.limits.MaxObjects {
		return ErrSealBudget
	}
	data := record.encode()
	if count, err := attempt.catalog.WriteAt(data[:], int64(attempt.entries)*128); err != nil || count != 128 {
		return ErrInvalid
	}
	attempt.locations[group][record.ordinal] = attempt.entries + 1
	attempt.entries++
	return nil
}

func readSealRecord(file *os.File, offset int64) (record sealRecord, err error) {
	var encoded [128]byte
	if count, readErr := file.ReadAt(encoded[:], offset); readErr != nil || count != 128 {
		return record, ErrInvalid
	}
	record.ordinal = binary.BigEndian.Uint32(encoded[:4])
	record.kind = encoded[4]
	copy(record.id[:], encoded[5:37])
	record.offset = int64(binary.BigEndian.Uint64(encoded[37:45]))
	record.length = int64(binary.BigEndian.Uint64(encoded[45:53]))
	copy(record.plainHash[:], encoded[53:85])
	record.cipherBytes = int64(binary.BigEndian.Uint64(encoded[85:93]))
	copy(record.cipherHash[:], encoded[93:125])
	return record, nil
}
func (attempt *sealAttempt) record(group int, ordinal uint32) (sealRecord, error) {
	if ordinal >= 100000 || attempt.locations[group][ordinal] == 0 {
		return sealRecord{}, ErrInvalid
	}
	return readSealRecord(attempt.catalog, int64(attempt.locations[group][ordinal]-1)*128)
}

func decodeExtentRecord(row SealExtentRecord) (record sealRecord, err error) {
	if row.Kind != "extent" || row.Ordinal >= 100000 || row.PlaintextBytes <= 0 || row.PlaintextBytes > sealExtentLimit || row.Offset < 0 || row.Offset > math.MaxInt64-row.PlaintextBytes {
		return record, ErrInvalid
	}
	record.ordinal, record.offset, record.length, record.cipherBytes = row.Ordinal, row.Offset, row.PlaintextBytes, row.CiphertextBytes
	switch row.FileKind {
	case "database":
		record.kind = 1
		if row.FileID != "database" {
			return record, ErrInvalid
		}
	case "inventory":
		record.kind = 2
		if row.FileID != "inventory" {
			return record, ErrInvalid
		}
	case "archive":
		record.kind = 3
		if !sealHex(row.FileID, 32) {
			return record, ErrInvalid
		}
		_, _ = hex.Decode(record.id[:], []byte(row.FileID))
	default:
		return record, ErrInvalid
	}
	if !sealHex(row.PlaintextSHA256, 32) || !sealHex(row.CiphertextSHA256, 32) {
		return record, ErrInvalid
	}
	_, _ = hex.Decode(record.plainHash[:], []byte(row.PlaintextSHA256))
	_, _ = hex.Decode(record.cipherHash[:], []byte(row.CiphertextSHA256))
	size, sizeErr := sealCipherSize(record.length)
	if sizeErr != nil || size != record.cipherBytes {
		return record, ErrInvalid
	}
	return record, nil
}

func (order *sealOrder) accept(record sealRecord) error {
	if record.ordinal != order.next || record.kind < 1 || record.kind > 3 {
		return ErrInvalid
	}
	same := order.next != 0 && record.kind == order.kind && record.id == order.id
	if same {
		if record.offset != order.offset || order.previous != sealExtentLimit {
			return ErrInvalid
		}
	} else {
		if record.offset != 0 || record.kind < order.kind || record.kind == order.kind && bytes.Compare(record.id[:], order.id[:]) <= 0 || order.next == 0 && record.kind != 1 || record.kind > order.kind+1 {
			return ErrInvalid
		}
		order.offset = 0
		if record.kind == 3 {
			order.archives++
		}
	}
	if record.length > math.MaxInt64-order.offset || record.length > math.MaxInt64-order.lengths[record.kind-1] {
		return ErrInvalid
	}
	order.offset += record.length
	order.previous = record.length
	order.lengths[record.kind-1] += record.length
	order.counts[record.kind-1]++
	order.kind = record.kind
	order.id = record.id
	order.next++
	return nil
}

func sealHex(value string, size int) bool {
	if len(value) != size*2 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func (root sealRoot) valid(identity attemptData, maxRecords uint32) error {
	if root.Format != "cognate-backup-root" || root.Version != 1 || root.Suite != 1 || root.InstanceID != hex.EncodeToString(identity.instance[:]) || root.BackupID != hex.EncodeToString(identity.backup[:]) || root.AttemptID != hex.EncodeToString(identity.attempt[:]) || root.Scope != "instance" || root.CutMode != "managed-exclusive-staged" || root.CachePolicy != "authoritative-closure" || len(root.AppVersion) == 0 || len(root.AppVersion) > 64 || !sealHex(root.SchemaSHA256, 32) || !sealHex(root.CutID, 16) || root.CutID == strings.Repeat("0", 32) || !sealHex(root.ClosureSHA256, 32) || root.Database.Bytes <= 0 || root.Inventory.Bytes <= 0 || root.Database.Extents == 0 || root.Inventory.Extents == 0 || !sealHex(root.Database.SHA256, 32) || !sealHex(root.Inventory.SHA256, 32) || root.Inventory.Records > maxRecords || root.Inventory.Owners > root.Inventory.Records || root.Archives > 100000 || len(root.ExternalConfigNames) > 32 {
		return ErrInvalid
	}
	for _, character := range root.AppVersion {
		if character < 33 || character > 126 {
			return ErrInvalid
		}
	}
	if len(root.MasterKeyID) != 43 {
		return ErrInvalid
	}
	keyID, err := base64.RawURLEncoding.DecodeString(root.MasterKeyID)
	if err != nil || len(keyID) != 32 || base64.RawURLEncoding.EncodeToString(keyID) != root.MasterKeyID {
		return ErrInvalid
	}
	for index, name := range root.ExternalConfigNames {
		if len(name) == 0 || len(name) > 64 || name[0] < 'A' || name[0] > 'Z' || index > 0 && root.ExternalConfigNames[index-1] >= name {
			return ErrInvalid
		}
		for _, character := range name {
			if !(character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_') {
				return ErrInvalid
			}
		}
	}
	return nil
}

type sealCountWriter struct{ count int64 }

func (writer *sealCountWriter) Write(data []byte) (int, error) {
	writer.count += int64(len(data))
	return len(data), nil
}

type sealLimitWriter struct {
	ctx       context.Context
	writer    io.Writer
	remaining int64
}

func (writer *sealLimitWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.remaining {
		return 0, ErrSealBudget
	}
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := writer.writer.Write(data)
	writer.remaining -= int64(count)
	return count, err
}
func writeSealJSON(ctx context.Context, writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data)+1 > 65536 {
		return ErrSealBudget
	}
	data = append(data, '\n')
	return sealWrite(ctx, writer, data)
}

func writeSealRoot(ctx context.Context, writer io.Writer, root sealRoot, attempt *sealAttempt) error {
	data, err := json.Marshal(root)
	if err != nil || len(data) > 65536 {
		return ErrInvalid
	}
	if err = sealWrite(ctx, writer, append(data[:len(data)-1], []byte(`,"pages":[`)...)); err != nil {
		return err
	}
	for ordinal := uint32(1); ordinal <= attempt.pageCount; ordinal++ {
		record, err := attempt.record(1, ordinal)
		if err != nil {
			return err
		}
		if ordinal > 1 {
			if err = sealWrite(ctx, writer, []byte(",")); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(sealPagePin{Ordinal: ordinal, Bytes: record.cipherBytes, Hash: hex.EncodeToString(record.cipherHash[:])})
		if err != nil {
			return ErrInvalid
		}
		if err = sealWrite(ctx, writer, encoded); err != nil {
			return err
		}
	}
	return sealWrite(ctx, writer, []byte("]}\n"))
}

func (engine *sealEngine) metadataFile(ctx context.Context, attempt *sealAttempt, purpose byte, ordinal uint32, maximum int64, produce func(io.Writer) error) (result SealedFile, returnedError error) {
	charge, err := engine.reserve(0, 0, maximum, 0)
	if err != nil {
		return result, err
	}
	var cleanupError error
	defer func() { engine.finishCharge(charge, cleanupError) }()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, ErrUnavailable
	}
	name := fmt.Sprintf("m-%x", random)
	file, err := sealOpen(engine.directory, name, true)
	if err != nil {
		return result, err
	}
	fileClosed := false
	defer func() {
		if !fileClosed {
			if engine.closeFile(file) != nil {
				cleanupError = ErrSealUncertain
			}
		}
		unlinkErr := sealUnlink(engine.directory, name)
		syncErr := engine.ops.sync(engine.directory.root)
		if unlinkErr != nil || syncErr != nil {
			cleanupError = mergeSealCleanup(cleanupError, ErrSealCleanup)
		}
		returnedError = mergeSealCleanup(returnedError, cleanupError)
		if ctx.Err() != nil || engine.ctx.Err() != nil {
			canceled := error(context.Canceled)
			if ctx.Err() != nil {
				canceled = ctx.Err()
			}
			returnedError = mergeSealCleanup(returnedError, canceled)
		}
		if returnedError != nil && result.get != nil {
			returnedError = mergeSealCleanup(returnedError, result.Close())
			result = SealedFile{}
		}
	}()
	err = produce(&sealLimitWriter{ctx: ctx, writer: file, remaining: maximum})
	if err == nil {
		err = engine.ops.sync(file)
	}
	closeErr := engine.closeFile(file)
	fileClosed = true
	if closeErr != nil {
		cleanupError = ErrSealUncertain
		return result, ErrSealUncertain
	}
	if err != nil {
		return result, sealError(ctx, err)
	}
	source, err := sealOpen(engine.directory, name, false)
	if err != nil {
		return result, err
	}
	defer func() {
		if engine.closeFile(source) != nil {
			cleanupError = ErrSealUncertain
			returnedError = ErrSealUncertain
		}
		returnedError = mergeSealCleanup(returnedError, ctx.Err())
		if returnedError != nil && result.get != nil {
			returnedError = mergeSealCleanup(returnedError, result.Close())
			result = SealedFile{}
		}
	}()
	info, err := sealBorrow(source)
	if err != nil {
		return result, err
	}
	header := sealHeader{purpose: purpose, attempt: attempt.identity, ordinal: ordinal, length: info.Size()}
	if _, err = rand.Read(header.nonce[:]); err != nil {
		return result, ErrUnavailable
	}
	size, err := sealCipherSize(header.length)
	if err != nil {
		return result, err
	}
	return engine.writeArtifact(ctx, attempt.identity, purpose, ordinal, header.length, size, func(output io.Writer) error {
		_, err := sealEncrypt(ctx, header, attempt.dek[:], &sealOffsetReader{ctx: ctx, file: source}, output)
		return err
	})
}

func (attempt *sealAttempt) checkSealed(ctx context.Context, purpose byte, ordinal uint32, size int64, digest [32]byte, plain io.Writer) (returnedError error) {
	file, err := sealOpen(attempt.engine.directory, sealObjectName(attempt.identity, purpose, ordinal), false)
	if err != nil {
		return err
	}
	defer func() {
		if attempt.engine.closeFile(file) != nil {
			returnedError = ErrSealUncertain
		}
	}()
	if plain == nil {
		info, err := sealBorrow(file)
		if err != nil || info.Size() != size {
			return ErrInvalid
		}
		hasher := sha256.New()
		buffer := make([]byte, 32768)
		reader := &sealOffsetReader{ctx: ctx, file: file}
		if _, err = io.CopyBuffer(hasher, reader, buffer); err != nil {
			return sealError(ctx, err)
		}
		if !bytes.Equal(hasher.Sum(nil), digest[:]) {
			return ErrInvalid
		}
		return sealRepin(file, info)
	}
	_, _, err = decryptSealFile(ctx, file, attempt.identity, purpose, ordinal, attempt.dek[:], size, digest, plain)
	return err
}

func decryptSealFile(ctx context.Context, file *os.File, identity attemptData, purpose byte, ordinal uint32, secret []byte, size int64, digest [32]byte, output io.Writer) (header sealHeader, plainHash [32]byte, err error) {
	info, err := sealBorrow(file)
	if err != nil || info.Size() != size {
		return header, plainHash, ErrInvalid
	}
	var encoded [96]byte
	if count, readErr := file.ReadAt(encoded[:], 0); readErr != nil || count != 96 {
		return header, plainHash, ErrInvalid
	}
	header, err = parseSealHeader(encoded[:])
	if err != nil || header.attempt != identity || header.purpose != purpose || header.ordinal != ordinal {
		return header, plainHash, ErrInvalid
	}
	expected, err := sealCipherSize(header.length)
	if err != nil || expected != size {
		return header, plainHash, ErrInvalid
	}
	hasher := sha256.New()
	_, _ = hasher.Write(encoded[:])
	reader := &sealOffsetReader{ctx: ctx, file: file, offset: 96}
	plainHash, err = sealDecrypt(ctx, header, secret, io.TeeReader(reader, hasher), output)
	if err != nil {
		return header, plainHash, err
	}
	if digest != [32]byte{} && !bytes.Equal(hasher.Sum(nil), digest[:]) {
		return header, plainHash, ErrInvalid
	}
	if err = sealRepin(file, info); err != nil {
		return header, plainHash, err
	}
	return header, plainHash, nil
}

func (attempt *sealAttempt) verifyData(ctx context.Context, root sealRoot) error {
	var previous sealRecord
	var full hash.Hash
	finish := func() error {
		if full == nil {
			return nil
		}
		digest := hex.EncodeToString(full.Sum(nil))
		expected := root.Database.SHA256
		if previous.kind == 2 {
			expected = root.Inventory.SHA256
		} else if previous.kind == 3 {
			expected = hex.EncodeToString(previous.id[:])
		}
		if digest != expected {
			return ErrInvalid
		}
		return nil
	}
	for ordinal := uint32(0); ordinal < attempt.dataCount; ordinal++ {
		record, err := attempt.record(0, ordinal)
		if err != nil {
			return err
		}
		if full == nil || record.kind != previous.kind || record.id != previous.id {
			if err = finish(); err != nil {
				return err
			}
			full = sha256.New()
		}
		if err = attempt.checkSealed(ctx, 1, ordinal, record.cipherBytes, record.cipherHash, full); err != nil {
			return err
		}
		previous = record
	}
	return finish()
}

func strictSealJSON(data []byte, destination any) error {
	if len(data) == 0 || len(data) > 65536 {
		return ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	target := reflect.TypeOf(destination)
	if target.Kind() != reflect.Pointer {
		return ErrInvalid
	}
	if err := scanSealJSON(decoder, target.Elem(), 0); err != nil {
		return err
	}
	if _, err := decoder.ReadToken(); err != io.EOF {
		return ErrInvalid
	}
	if json.Unmarshal(data, destination, json.RejectUnknownMembers(true)) != nil {
		return ErrInvalid
	}
	return nil
}
func scanSealJSON(decoder *jsontext.Decoder, target reflect.Type, depth int) error {
	if depth > 16 {
		return ErrInvalid
	}
	token, err := decoder.ReadToken()
	if err != nil || token.Kind() == 'n' {
		return ErrInvalid
	}
	switch target.Kind() {
	case reflect.Struct:
		if token.Kind() != '{' || target.NumField() > 64 {
			return ErrInvalid
		}
		var seen uint64
		for decoder.PeekKind() != '}' {
			key, err := decoder.ReadToken()
			if err != nil || key.Kind() != '"' {
				return ErrInvalid
			}
			found := -1
			for index := 0; index < target.NumField(); index++ {
				if strings.Split(target.Field(index).Tag.Get("json"), ",")[0] == key.String() {
					found = index
					break
				}
			}
			if found < 0 || seen&(1<<found) != 0 {
				return ErrInvalid
			}
			seen |= 1 << found
			if err = scanSealJSON(decoder, target.Field(found).Type, depth+1); err != nil {
				return err
			}
		}
		if _, err = decoder.ReadToken(); err != nil || seen != (uint64(1)<<target.NumField())-1 {
			return ErrInvalid
		}
	case reflect.Slice:
		if token.Kind() != '[' {
			return ErrInvalid
		}
		count := 0
		for decoder.PeekKind() != ']' {
			count++
			if count > 32 {
				return ErrInvalid
			}
			if err = scanSealJSON(decoder, target.Elem(), depth+1); err != nil {
				return err
			}
		}
		if _, err = decoder.ReadToken(); err != nil {
			return ErrInvalid
		}
	case reflect.String:
		if token.Kind() != '"' {
			return ErrInvalid
		}
	case reflect.Uint32, reflect.Int64:
		if token.Kind() != '0' {
			return ErrInvalid
		}
		for _, character := range token.String() {
			if character < '0' || character > '9' {
				return ErrInvalid
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}

func readSealLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > 65536 {
		return nil, ErrInvalid
	}
	return line, nil
}
