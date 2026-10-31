package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"

	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type CaptureOptions struct {
	InstanceID, BackupID [16]byte
	AppVersion           string
	Limits               library.ContentSnapshotLimits
}

type CapturedSnapshot struct{ get func() *capturedSnapshot }

type capturedSnapshot struct {
	mutex      sync.Mutex
	changed    chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	active     int
	metadata   SealMetadata
	files      []SealedFile
	closeOnce  sync.Once
	closeError error
}

func (CapturedSnapshot) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private captured snapshot]")
}
func (CapturedSnapshot) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (CapturedSnapshot) LogValue() slog.Value {
	return slog.StringValue("[private captured snapshot]")
}

func (engine SealEngine) CaptureSnapshot(ctx context.Context, service *library.Service, options CaptureOptions) (result CapturedSnapshot, returnedError error) {
	if engine.get == nil || service == nil || options.InstanceID == [16]byte{} || options.BackupID == [16]byte{} || len(options.AppVersion) == 0 || len(options.AppVersion) > 64 {
		return result, ErrInvalid
	}
	for _, character := range options.AppVersion {
		if character < 33 || character > 126 {
			return result, ErrInvalid
		}
	}
	state := engine.get()
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	attempt, bootstrap, err := engine.BeginAttempt(scoped, options.InstanceID, options.BackupID)
	if err != nil {
		return result, err
	}
	builder := &captureBuilder{engine: engine, attempt: attempt, files: []SealedFile{bootstrap}}
	defer func() {
		if returnedError != nil || result.get == nil {
			for index := len(builder.files) - 1; index >= 0; index-- {
				returnedError = captureError(scoped, returnedError, builder.files[index].Close())
			}
			returnedError = captureError(scoped, returnedError, attempt.Close())
			result = CapturedSnapshot{}
		}
	}()
	var callbackError error
	var databaseExtents, inventoryExtents uint32
	summary, sourceError := service.WithContentSnapshot(scoped, options.Limits, func(ctx context.Context, view library.ContentSnapshotView) (callbackResult error) {
		defer func() { callbackError = callbackResult }()
		metadata, err := view.Metadata(ctx)
		if err != nil {
			return err
		}
		archives, err := view.ArchiveFiles(ctx)
		if err != nil {
			return err
		}
		databaseBytes, databaseHash := metadata.Database().Database()
		databaseExtents, err = builder.copyFile(ctx, "database", "database", databaseBytes, databaseHash, view.CopyDatabase)
		if err != nil {
			return err
		}
		inventoryBytes, inventoryHash, _, _ := metadata.Inventory()
		inventoryExtents, err = builder.copyFile(ctx, "inventory", "inventory", inventoryBytes, inventoryHash, view.CopyInventory)
		if err != nil {
			return err
		}
		previous := ""
		for _, archive := range archives {
			identity := archive.Hash()
			if !sealHex(identity, 32) || identity <= previous {
				return ErrInvalid
			}
			previous = identity
			var digest [32]byte
			_, _ = hex.Decode(digest[:], []byte(identity))
			if _, err := builder.copyFile(ctx, "archive", identity, archive.Bytes(), digest, func(ctx context.Context, output io.Writer) (int64, error) {
				copied, err := view.CopyArchive(ctx, identity, output)
				if err == nil && (copied.Bytes() != archive.Bytes() || copied.SHA256() != digest) {
					return 0, ErrInvalid
				}
				return copied.Bytes(), err
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err = captureError(scoped, callbackError, sourceError); err != nil {
		return result, err
	}
	metadata, err := captureMetadata(summary, options.AppVersion, databaseExtents, inventoryExtents)
	if err != nil {
		return result, err
	}
	for start := 0; start < len(builder.records); start += 128 {
		rows := builder.records[start:min(start+128, len(builder.records))]
		page, err := engine.SealPage(scoped, attempt, uint32(start/128+1), uint32(len(rows)), func(yield func(SealExtentRecord, error) bool) {
			for _, row := range rows {
				if !yield(row, nil) {
					return
				}
			}
		})
		if err != nil {
			return result, err
		}
		builder.files = append(builder.files, page)
	}
	root, err := engine.SealRoot(scoped, attempt, metadata)
	if err != nil {
		return result, err
	}
	builder.files = append(builder.files, root)
	complete, err := engine.SealComplete(scoped, attempt)
	if err != nil {
		return result, err
	}
	builder.files = append(builder.files, complete)
	if err = captureError(scoped, attempt.Close()); err != nil {
		return result, err
	}
	lifetime, cancel := context.WithCancel(state.ctx)
	snapshot := &capturedSnapshot{ctx: lifetime, cancel: cancel, changed: make(chan struct{}), metadata: metadata, files: builder.files}
	return CapturedSnapshot{get: func() *capturedSnapshot { return snapshot }}, nil
}

func captureMetadata(summary library.ContentSnapshotSummary, version string, databaseExtents, inventoryExtents uint32) (SealMetadata, error) {
	database := summary.Database()
	databaseBytes, databaseHash := database.Database()
	inventoryBytes, inventoryHash, records, owners := summary.Inventory()
	if records > math.MaxUint32 || owners > math.MaxUint32 {
		return SealMetadata{}, ErrSealBudget
	}
	schema, cut, closure := database.SchemaFingerprint(), database.SnapshotID(), summary.ClosureSHA256()
	return SealMetadata{AppVersion: version, SchemaSHA256: hex.EncodeToString(schema[:]), CutID: hex.EncodeToString(cut[:]), MasterKeyID: database.KeyIdentity(), Database: SealFileDescription{Bytes: databaseBytes, SHA256: hex.EncodeToString(databaseHash[:]), Extents: databaseExtents}, Inventory: SealInventoryDescription{Bytes: inventoryBytes, SHA256: hex.EncodeToString(inventoryHash[:]), Extents: inventoryExtents, Records: uint32(records), Owners: uint32(owners)}, Archives: summary.Archives(), ClosureSHA256: hex.EncodeToString(closure[:])}, nil
}

type captureBuilder struct {
	engine  SealEngine
	attempt SealAttempt
	files   []SealedFile
	records []SealExtentRecord
}

type captureChunkWriter struct {
	mutex          sync.Mutex
	ctx            context.Context
	builder        *captureBuilder
	kind, identity string
	length, count  int64
	offset         int64
	buffer         []byte
	held           int
	digest         hash.Hash
	failure        error
	closed         bool
}

func (builder *captureBuilder) copyFile(ctx context.Context, kind, identity string, length int64, expected [32]byte, produce func(context.Context, io.Writer) (int64, error)) (uint32, error) {
	if length <= 0 || min(length, sealExtentLimit) > builder.engine.get().limits.InputStagingBytes {
		return 0, ErrSealBudget
	}
	writer := &captureChunkWriter{ctx: ctx, builder: builder, kind: kind, identity: identity, length: length, buffer: make([]byte, min(length, sealExtentLimit)), digest: sha256.New()}
	defer func() {
		writer.mutex.Lock()
		writer.closed = true
		clear(writer.buffer)
		writer.buffer, writer.digest, writer.builder, writer.ctx = nil, nil, nil, nil
		writer.mutex.Unlock()
	}()
	start := len(builder.records)
	copied, produceError := produce(ctx, writer)
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.closed = true
	if err := captureError(ctx, writer.failure, produceError); err != nil {
		return 0, err
	}
	if copied != length || writer.count != length || [32]byte(writer.digest.Sum(nil)) != expected {
		return 0, ErrInvalid
	}
	if writer.held != 0 {
		if err := writer.flush(); err != nil {
			return 0, err
		}
	}
	return uint32(len(builder.records) - start), nil
}

func (writer *captureChunkWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.closed {
		return 0, ErrClosed
	}
	if writer.failure != nil {
		return 0, writer.failure
	}
	if writer.failure = writer.ctx.Err(); writer.failure != nil {
		return 0, writer.failure
	}
	if int64(len(payload)) > writer.length-writer.count {
		writer.failure = ErrInvalid
		return 0, writer.failure
	}
	written := 0
	for len(payload) != 0 {
		amount := copy(writer.buffer[writer.held:], payload)
		_, _ = writer.digest.Write(payload[:amount])
		writer.held += amount
		writer.count += int64(amount)
		written += amount
		payload = payload[amount:]
		if writer.held == len(writer.buffer) {
			if writer.failure = writer.flush(); writer.failure != nil {
				return written, writer.failure
			}
		}
	}
	writer.failure = writer.ctx.Err()
	return written, writer.failure
}

func (writer *captureChunkWriter) flush() error {
	payload := writer.buffer[:writer.held]
	digest := sha256.Sum256(payload)
	ordinal := uint32(len(writer.builder.records))
	artifact, err := writer.builder.engine.SealExtentSource(writer.ctx, writer.builder.attempt, ordinal, int64(len(payload)), digest, func(ctx context.Context, output io.Writer) error {
		return sealWrite(ctx, output, payload)
	})
	if err != nil {
		return err
	}
	writer.builder.files = append(writer.builder.files, artifact)
	ref := artifact.Ref().read()
	writer.builder.records = append(writer.builder.records, SealExtentRecord{Kind: "extent", Ordinal: ordinal, FileKind: writer.kind, FileID: writer.identity, Offset: writer.offset, PlaintextBytes: int64(len(payload)), PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
	writer.offset += int64(len(payload))
	clear(payload)
	writer.held = 0
	return nil
}

func captureError(ctx context.Context, failures ...error) error {
	var critical []error
	for _, known := range []error{ErrSealUncertain, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotUncertain, ErrSealCleanup, state.ErrLocalSnapshotCleanup} {
		for _, failure := range failures {
			if errors.Is(failure, known) {
				critical = append(critical, known)
				break
			}
		}
	}
	if len(critical) == 1 {
		return critical[0]
	}
	if len(critical) != 0 {
		return errors.Join(critical...)
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, failure := range failures {
		if failure == nil {
			continue
		}
		for _, known := range []error{ErrSealBudget, ErrInvalid, ErrClosed, ErrBusy, ErrUnsupported, library.ErrSnapshotArchiveInvalid, library.ErrClosed, state.ErrLocalSnapshotBudget, state.ErrLocalSnapshotInvalid, state.ErrLocalSnapshotResource, state.ErrLocalSnapshotBusy, context.Canceled, context.DeadlineExceeded} {
			if errors.Is(failure, known) {
				return known
			}
		}
		return ErrInvalid
	}
	return nil
}

func (snapshot CapturedSnapshot) Metadata(ctx context.Context) (SealMetadata, error) {
	if snapshot.get == nil || ctx == nil {
		return SealMetadata{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return SealMetadata{}, err
	}
	state := snapshot.get()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return SealMetadata{}, ErrClosed
	}
	if err := state.ctx.Err(); err != nil {
		return SealMetadata{}, err
	}
	return state.metadata, nil
}

func (snapshot CapturedSnapshot) WithFiles(ctx context.Context, consume func(context.Context, ObjectRef, *os.File) error) error {
	if snapshot.get == nil || ctx == nil || consume == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := snapshot.get()
	state.mutex.Lock()
	if state.closed {
		state.mutex.Unlock()
		return ErrClosed
	}
	if err := state.ctx.Err(); err != nil {
		state.mutex.Unlock()
		return err
	}
	state.active++
	state.mutex.Unlock()
	scoped, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	defer cancel()
	defer func() {
		state.mutex.Lock()
		state.active--
		close(state.changed)
		state.changed = make(chan struct{})
		state.mutex.Unlock()
	}()
	for _, file := range state.files {
		if err := file.WithFile(scoped, func(input *os.File) error { return consume(scoped, file.Ref(), input) }); err != nil {
			return err
		}
	}
	return scoped.Err()
}

func (snapshot CapturedSnapshot) Close() error {
	if snapshot.get == nil {
		return nil
	}
	state := snapshot.get()
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		state.closed = true
		state.cancel()
		for state.active != 0 {
			changed := state.changed
			state.mutex.Unlock()
			<-changed
			state.mutex.Lock()
		}
		state.metadata = SealMetadata{}
		state.mutex.Unlock()
		for index := len(state.files) - 1; index >= 0; index-- {
			state.closeError = captureError(nil, state.closeError, state.files[index].Close())
		}
		state.files = nil
	})
	return state.closeError
}
