package state

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func snapshotMerge(previous, next error) error {
	for _, known := range []error{ErrLocalSnapshotUncertain, ErrLocalSnapshotCleanup} {
		if errors.Is(previous, known) || errors.Is(next, known) {
			return known
		}
	}
	if next != nil {
		return next
	}
	return previous
}

func snapshotError(ctx context.Context, err error) error {
	for _, known := range []error{ErrLocalSnapshotUncertain, ErrLocalSnapshotCleanup} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, known := range []error{ErrLocalSnapshotInvalid, ErrLocalSnapshotBudget, ErrLocalSnapshotResource, ErrLocalSnapshotBusy, maintenance.ErrUnsupported, maintenance.ErrClosed, maintenance.ErrLease, maintenance.ErrQuiescing, maintenance.ErrDirectory, maintenance.ErrLock, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	if err == nil {
		return nil
	}
	return ErrLocalSnapshotInvalid
}

var (
	ErrLocalSnapshotInvalid   = errors.New("local snapshot refused")
	ErrLocalSnapshotBudget    = errors.New("local snapshot budget exceeded")
	ErrLocalSnapshotCleanup   = errors.New("local snapshot cleanup failed")
	ErrLocalSnapshotUncertain = errors.New("local snapshot cleanup uncertain")
	ErrLocalSnapshotResource  = errors.New("local snapshot resource controls unavailable")
	ErrLocalSnapshotBusy      = errors.New("local snapshot reader is busy")
)

type LocalSnapshotLimits struct {
	MaxDBBytes, MaxInventoryBytes, MaxStagingBytes int64
	MaxRecords                                     uint64
	SQLiteHeapBytes, WorkerAddressSpaceBytes       int64
	WorkerCPUSeconds                               uint64
	MaxWALBytes, MaxSHMBytes                       int64
	DrainTimeout, OperationTimeout                 time.Duration
}

func (limits LocalSnapshotLimits) valid() bool {
	return limits.MaxDBBytes > 0 && limits.MaxInventoryBytes > 0 && limits.MaxStagingBytes > 0 && limits.MaxRecords > 0 && limits.MaxRecords <= math.MaxInt64 && limits.SQLiteHeapBytes >= 65536 && limits.SQLiteHeapBytes <= 1<<30 && limits.WorkerAddressSpaceBytes > limits.SQLiteHeapBytes && limits.WorkerCPUSeconds > 0 && limits.WorkerCPUSeconds <= math.MaxInt32 && limits.MaxWALBytes > 0 && limits.MaxSHMBytes >= 32768 && limits.DrainTimeout > 0 && limits.OperationTimeout > 0
}

type LocalSnapshotView struct{ get func() *localSnapshotScope }
type LocalSnapshotSummary struct{ get func() localSnapshotManifest }
type LocalSnapshotMetadata struct{ get func() localSnapshotManifest }
type LocalSnapshotCursor struct{ get func() snapshotCursor }
type LocalSnapshotReferencePage struct{ get func() snapshotReferencePage }
type LocalSnapshotReference struct{ get func() snapshotReference }

type localSnapshotScope struct {
	mutex                  sync.Mutex
	store                  *Store
	ctx                    context.Context
	cancel                 context.CancelFunc
	token                  *maintenance.Exclusive
	stage                  *localSnapshotStage
	manifest               localSnapshotManifest
	live, queued, poisoned bool
	active                 int
	failure                error
	changed                chan struct{}
}

type localSnapshotManifest struct {
	Format          string               `json:"format"`
	Version         int                  `json:"version"`
	ContentClosure  string               `json:"contentClosure"`
	ID              string               `json:"id"`
	DatabaseBytes   int64                `json:"database_bytes"`
	DatabaseSHA256  string               `json:"database_sha256"`
	SQLiteVersion   string               `json:"sqlite_version"`
	SchemaSHA256    string               `json:"schema_sha256"`
	ReferenceBytes  int64                `json:"reference_bytes"`
	ReferenceSHA256 string               `json:"reference_sha256"`
	Repositories    uint64               `json:"repositories"`
	Skills          uint64               `json:"skills"`
	Packs           uint64               `json:"packs"`
	Items           uint64               `json:"items"`
	TotalRecords    uint64               `json:"total_records"`
	KeyID           string               `json:"key_id"`
	KeyProof        []byte               `json:"key_proof"`
	PolicyProof     []byte               `json:"policy_proof"`
	Credentials     []snapshotCredential `json:"credentials"`
}

type snapshotCredential struct {
	UserKey    string `json:"user_key"`
	ViewerID   string `json:"viewer_id"`
	ClientID   string `json:"client_id"`
	KeyID      string `json:"key_id"`
	Generation string `json:"generation"`
	Ciphertext []byte `json:"ciphertext"`
}

type snapshotCursor struct {
	scope  *localSnapshotScope
	offset int64
	count  uint64
}
type snapshotReferencePage struct {
	rows   []LocalSnapshotReference
	cursor LocalSnapshotCursor
	done   bool
}
type snapshotReference struct {
	skillName, skillDescription string
	Kind                        string `json:"kind"`
	ID                          int64  `json:"id"`
	PublicID                    string `json:"public_id,omitzero"`
	Owner                       int64  `json:"owner,omitzero"`
	Repository                  int64  `json:"repository,omitzero"`
	Pack                        int64  `json:"pack,omitzero"`
	Skill                       int64  `json:"skill,omitzero"`
	Name                        string `json:"name,omitzero"`
	Branch                      string `json:"branch,omitzero"`
	ArchiveHash                 string `json:"archive_hash,omitzero"`
	SkillCount                  int64  `json:"skill_count,omitzero"`
	Revision                    int64  `json:"revision,omitzero"`
	Path                        string `json:"path,omitzero"`
	ContentHash                 string `json:"content_hash,omitzero"`
	ItemCount                   int64  `json:"item_count,omitzero"`
	Slug                        string `json:"slug,omitzero"`
}

func (LocalSnapshotView) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot view")
}
func (LocalSnapshotView) MarshalJSON() ([]byte, error) { return []byte(`"local snapshot view"`), nil }
func (LocalSnapshotView) LogValue() slog.Value         { return slog.StringValue("local snapshot view") }
func (LocalSnapshotSummary) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot summary")
}
func (LocalSnapshotSummary) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot summary"`), nil
}
func (LocalSnapshotSummary) LogValue() slog.Value { return slog.StringValue("local snapshot summary") }
func (LocalSnapshotMetadata) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot metadata")
}
func (LocalSnapshotMetadata) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot metadata"`), nil
}
func (LocalSnapshotMetadata) LogValue() slog.Value {
	return slog.StringValue("local snapshot metadata")
}
func (LocalSnapshotCursor) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot cursor")
}
func (LocalSnapshotCursor) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot cursor"`), nil
}
func (LocalSnapshotCursor) LogValue() slog.Value { return slog.StringValue("local snapshot cursor") }
func (LocalSnapshotReferencePage) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot reference page")
}
func (LocalSnapshotReferencePage) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot reference page"`), nil
}
func (LocalSnapshotReferencePage) LogValue() slog.Value {
	return slog.StringValue("local snapshot reference page")
}
func (LocalSnapshotReference) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot reference")
}
func (LocalSnapshotReference) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot reference"`), nil
}
func (LocalSnapshotReference) LogValue() slog.Value {
	return slog.StringValue("local snapshot reference")
}

type localSnapshotContextKey struct{}
type localSnapshotContextValue struct{ get func() *localSnapshotScope }

func (store *Store) WithLocalSnapshot(ctx context.Context, limits LocalSnapshotLimits, consume func(context.Context, LocalSnapshotView) error) (summary LocalSnapshotSummary, returnedError error) {
	if ctx == nil || consume == nil || !limits.valid() {
		return summary, ErrLocalSnapshotInvalid
	}
	if err := snapshotPlatform(ctx); err != nil {
		return summary, err
	}
	if store == nil || store.state == nil || store.state.maintenance == nil || store.state.snapshotSource == nil {
		return summary, ErrLocalSnapshotInvalid
	}
	if ctx.Value(localSnapshotContextKey{}) != nil || ctx.Value(storeWriterContextKey{}) != nil {
		return summary, maintenance.ErrLease
	}
	scoped, cancel := context.WithTimeout(ctx, limits.OperationTimeout)
	operation := &localSnapshotScope{store: store, ctx: scoped, cancel: cancel, live: true, queued: true, changed: make(chan struct{})}
	managed := store.state.maintenance
	managed.mutex.Lock()
	if managed.closing || managed.snapshotPoison || managed.snapshotCleanup {
		failure := error(maintenance.ErrClosed)
		if managed.snapshotPoison {
			failure = ErrLocalSnapshotUncertain
		} else if managed.snapshotCleanup {
			failure = ErrLocalSnapshotCleanup
		}
		managed.mutex.Unlock()
		cancel()
		return summary, failure
	}
	if managed.snapshotOps == nil {
		managed.snapshotOps = make(map[*localSnapshotScope]struct{})
	}
	managed.snapshotOps[operation] = struct{}{}
	managed.mutex.Unlock()
	var guard *os.File
	completed := false
	defer func() {
		callerError := scoped.Err()
		operation.mutex.Lock()
		operation.live = false
		if operation.active != 0 {
			returnedError = snapshotMerge(returnedError, ErrLocalSnapshotInvalid)
		}
		cancel()
		for operation.active != 0 {
			changed := operation.changed
			operation.mutex.Unlock()
			<-changed
			operation.mutex.Lock()
		}
		poisoned := operation.poisoned
		returnedError = snapshotMerge(returnedError, operation.failure)
		operation.mutex.Unlock()
		if !poisoned {
			cleanup := operation.stage.cleanup()
			returnedError = snapshotMerge(returnedError, cleanup)
			if errors.Is(cleanup, ErrLocalSnapshotUncertain) {
				poisoned = true
			}
			if errors.Is(cleanup, ErrLocalSnapshotCleanup) {
				managed.mutex.Lock()
				managed.snapshotCleanup = true
				managed.mutex.Unlock()
			}
		}
		if !poisoned && guard != nil && guard.Close() != nil {
			retainSnapshotResource(guard)
			poisoned = true
			returnedError = ErrLocalSnapshotUncertain
		}
		if errors.Is(returnedError, ErrLocalSnapshotCleanup) {
			managed.mutex.Lock()
			managed.snapshotCleanup = true
			managed.mutex.Unlock()
		}
		if poisoned {
			retainSnapshotResource(struct {
				operation *localSnapshotScope
				guard     *os.File
			}{operation, guard})
			managed.mutex.Lock()
			managed.snapshotPoison = true
			managed.mutex.Unlock()
			returnedError = ErrLocalSnapshotUncertain
		} else if operation.token != nil {
			operation.token.Release()
		}
		if callerError != nil {
			returnedError = snapshotMerge(returnedError, callerError)
		}
		if err := ctx.Err(); err != nil {
			returnedError = snapshotMerge(returnedError, err)
		} else if deadline, ok := scoped.Deadline(); ok && !time.Now().Before(deadline) {
			returnedError = snapshotMerge(returnedError, context.DeadlineExceeded)
		}
		cancel()
		managed.mutex.Lock()
		delete(managed.snapshotOps, operation)
		close(managed.changed)
		managed.changed = make(chan struct{})
		managed.mutex.Unlock()
		if returnedError == nil && completed {
			data := operation.manifest
			data.KeyProof = nil
			data.PolicyProof = nil
			summary = LocalSnapshotSummary{get: func() localSnapshotManifest { return data }}
		} else {
			summary = LocalSnapshotSummary{}
		}
	}()
	drain, stop := context.WithTimeout(scoped, limits.DrainTimeout)
	token, err := managed.instance.Quiesce(drain)
	stop()
	if err != nil {
		return summary, snapshotError(scoped, err)
	}
	operation.token = token
	managed.mutex.Lock()
	operation.queued = false
	managed.mutex.Unlock()
	if err = store.state.snapshotSource.inspect(scoped, limits); err != nil {
		if errors.Is(err, ErrLocalSnapshotUncertain) {
			operation.poisoned = true
		}
		return summary, err
	}
	guard, err = managed.instance.AcquireSnapshotGuard(scoped, token)
	if err != nil {
		if errors.Is(err, maintenance.ErrSnapshotGuardUncertain) {
			operation.poisoned = true
			return summary, ErrLocalSnapshotUncertain
		}
		return summary, snapshotError(scoped, err)
	}
	operation.stage, err = store.state.snapshotSource.newStage()
	if err != nil {
		return summary, err
	}
	response, terminal, err := snapshotRunWorker(scoped, store.state.snapshotSource, operation.stage, guard, limits)
	if !terminal {
		operation.poisoned = true
	}
	if err != nil {
		return summary, err
	}
	if err = store.state.snapshotSource.recheck(); err != nil {
		return summary, err
	}
	operation.manifest, err = snapshotVerifyStage(scoped, operation.stage, response, store, limits)
	if err != nil {
		if errors.Is(err, ErrLocalSnapshotUncertain) {
			operation.poisoned = true
		}
		return summary, err
	}
	callbackContext := context.WithValue(scoped, localSnapshotContextKey{}, localSnapshotContextValue{get: func() *localSnapshotScope { return operation }})
	if err = consume(callbackContext, LocalSnapshotView{get: func() *localSnapshotScope { return operation }}); err != nil {
		return summary, snapshotError(scoped, err)
	}
	if err = store.state.snapshotSource.recheck(); err != nil {
		return summary, err
	}
	if err = scoped.Err(); err != nil {
		return summary, err
	}
	completed = true
	return summary, nil
}

func (store *Store) ValidateLocalSnapshotContext(ctx context.Context) error {
	if ctx == nil {
		return ErrLocalSnapshotInvalid
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	value, ok := ctx.Value(localSnapshotContextKey{}).(localSnapshotContextValue)
	if !ok || value.get == nil {
		return maintenance.ErrLease
	}
	operation := value.get()
	if operation == nil || operation.store != store {
		return maintenance.ErrLease
	}
	operation.mutex.Lock()
	valid := operation.live && !operation.poisoned
	operation.mutex.Unlock()
	if !valid || operation.ctx.Err() != nil || !operation.token.ValidFor(store.state.maintenance.instance) {
		return maintenance.ErrLease
	}
	return nil
}

func (store *Store) WithLocalSnapshotReader(ctx context.Context, read func(context.Context) error) (returnedError error) {
	if store == nil || read == nil {
		return ErrLocalSnapshotInvalid
	}
	if err := store.ValidateLocalSnapshotContext(ctx); err != nil {
		return err
	}
	value := ctx.Value(localSnapshotContextKey{}).(localSnapshotContextValue)
	view := LocalSnapshotView{get: value.get}
	operation, scoped, release, err := view.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	returnedError = read(scoped)
	if returnedError == nil {
		returnedError = store.ValidateLocalSnapshotContext(scoped)
	}
	operation.mutex.Lock()
	operation.failure = snapshotMerge(operation.failure, snapshotError(scoped, returnedError))
	if errors.Is(returnedError, ErrLocalSnapshotUncertain) {
		operation.poisoned = true
	}
	operation.mutex.Unlock()
	return returnedError
}

func (view LocalSnapshotView) enter(ctx context.Context) (*localSnapshotScope, context.Context, func(), error) {
	if view.get == nil {
		return nil, nil, nil, maintenance.ErrLease
	}
	operation := view.get()
	if err := operation.store.ValidateLocalSnapshotContext(ctx); err != nil {
		return nil, nil, nil, err
	}
	value, ok := ctx.Value(localSnapshotContextKey{}).(localSnapshotContextValue)
	if !ok || value.get == nil || value.get() != operation {
		return nil, nil, nil, maintenance.ErrLease
	}
	operation.mutex.Lock()
	if !operation.live || operation.poisoned {
		operation.mutex.Unlock()
		return nil, nil, nil, maintenance.ErrLease
	}
	if operation.active != 0 {
		operation.mutex.Unlock()
		return nil, nil, nil, ErrLocalSnapshotBusy
	}
	operation.active++
	operation.mutex.Unlock()
	scoped, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	stop := context.AfterFunc(operation.ctx, func() { cancel(); close(done) })
	release := func() {
		if !stop() {
			<-done
		}
		cancel()
		operation.mutex.Lock()
		operation.active--
		close(operation.changed)
		operation.changed = make(chan struct{})
		operation.mutex.Unlock()
	}
	return operation, scoped, release, nil
}

func (view LocalSnapshotView) Metadata(ctx context.Context) (LocalSnapshotMetadata, error) {
	operation, _, release, err := view.enter(ctx)
	if err != nil {
		return LocalSnapshotMetadata{}, err
	}
	defer release()
	data := operation.manifest
	data.KeyProof = nil
	data.PolicyProof = nil
	return LocalSnapshotMetadata{get: func() localSnapshotManifest { return data }}, nil
}

func (summary LocalSnapshotSummary) Metadata() LocalSnapshotMetadata {
	if summary.get == nil {
		return LocalSnapshotMetadata{}
	}
	return LocalSnapshotMetadata{get: summary.get}
}
func (metadata LocalSnapshotMetadata) Database() (int64, [32]byte) {
	if metadata.get == nil {
		return 0, [32]byte{}
	}
	data := metadata.get()
	var digest [32]byte
	decoded, _ := hex.DecodeString(data.DatabaseSHA256)
	copy(digest[:], decoded)
	return data.DatabaseBytes, digest
}
func (metadata LocalSnapshotMetadata) References() (int64, [32]byte, uint64) {
	if metadata.get == nil {
		return 0, [32]byte{}, 0
	}
	data := metadata.get()
	var digest [32]byte
	decoded, _ := hex.DecodeString(data.ReferenceSHA256)
	copy(digest[:], decoded)
	return data.ReferenceBytes, digest, data.TotalRecords
}
func (metadata LocalSnapshotMetadata) SchemaFingerprint() [32]byte {
	var digest [32]byte
	if metadata.get != nil {
		decoded, _ := hex.DecodeString(metadata.get().SchemaSHA256)
		copy(digest[:], decoded)
	}
	return digest
}
func (metadata LocalSnapshotMetadata) KeyIdentity() string {
	if metadata.get == nil {
		return ""
	}
	return metadata.get().KeyID
}

func (metadata LocalSnapshotMetadata) SnapshotID() [16]byte {
	var identity [16]byte
	if metadata.get != nil {
		decoded, _ := hex.DecodeString(metadata.get().ID)
		copy(identity[:], decoded)
	}
	return identity
}

func (metadata LocalSnapshotMetadata) ReferenceCounts() (uint64, uint64, uint64, uint64) {
	if metadata.get == nil {
		return 0, 0, 0, 0
	}
	data := metadata.get()
	return data.Repositories, data.Skills, data.Packs, data.Items
}

func (metadata LocalSnapshotMetadata) CandidateFormat() (string, int, string) {
	if metadata.get == nil {
		return "", 0, ""
	}
	data := metadata.get()
	return data.Format, data.Version, data.ContentClosure
}

func (metadata LocalSnapshotMetadata) SQLiteVersion() string {
	if metadata.get == nil {
		return ""
	}
	return metadata.get().SQLiteVersion
}

func (view LocalSnapshotView) CopyDatabase(ctx context.Context, writer io.Writer) (int64, error) {
	return view.copy(ctx, writer, true)
}
func (view LocalSnapshotView) CopyReferences(ctx context.Context, writer io.Writer) (int64, error) {
	return view.copy(ctx, writer, false)
}
func (view LocalSnapshotView) copy(ctx context.Context, writer io.Writer, database bool) (int64, error) {
	operation, scoped, release, err := view.enter(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	name, size, hash := "references.ndjson", operation.manifest.ReferenceBytes, operation.manifest.ReferenceSHA256
	if database {
		name, size, hash = "snapshot.sqlite", operation.manifest.DatabaseBytes, operation.manifest.DatabaseSHA256
	}
	count, err := snapshotCopy(scoped, operation.stage, name, size, hash, writer)
	operation.mutex.Lock()
	operation.failure = snapshotMerge(operation.failure, err)
	if errors.Is(err, ErrLocalSnapshotUncertain) {
		operation.poisoned = true
	}
	operation.mutex.Unlock()
	return count, err
}

func (view LocalSnapshotView) NextReferences(ctx context.Context, cursor LocalSnapshotCursor, limit int) (page LocalSnapshotReferencePage, returnedError error) {
	if limit < 1 || limit > 128 {
		return page, ErrLocalSnapshotInvalid
	}
	operation, scoped, release, err := view.enter(ctx)
	if err != nil {
		return page, err
	}
	defer release()
	defer func() {
		operation.mutex.Lock()
		operation.failure = snapshotMerge(operation.failure, snapshotError(scoped, returnedError))
		operation.mutex.Unlock()
	}()
	position := snapshotCursor{scope: operation}
	if cursor.get != nil {
		position = cursor.get()
		if position.scope != operation {
			return page, maintenance.ErrLease
		}
	}
	if position.offset < 0 || position.offset > operation.manifest.ReferenceBytes || position.count > operation.manifest.TotalRecords {
		return page, ErrLocalSnapshotInvalid
	}
	file, err := operation.stage.open("references.ndjson")
	if err != nil {
		return page, err
	}
	defer func() {
		if file.Close() != nil {
			retainSnapshotResource(file)
			operation.mutex.Lock()
			operation.poisoned = true
			operation.mutex.Unlock()
			returnedError = ErrLocalSnapshotUncertain
		}
		if returnedError != nil {
			page = LocalSnapshotReferencePage{}
		}
	}()
	if _, err = file.Seek(position.offset, io.SeekStart); err != nil {
		return page, ErrLocalSnapshotInvalid
	}
	reader := bufio.NewReaderSize(snapshotContextReader{ctx: scoped, reader: file}, 65536)
	rows := make([]LocalSnapshotReference, 0, limit)
	for len(rows) < limit && position.offset < operation.manifest.ReferenceBytes {
		if position.count >= operation.manifest.TotalRecords {
			return page, ErrLocalSnapshotInvalid
		}
		row, size, err := snapshotReadRow(reader)
		if err != nil {
			return page, snapshotError(scoped, err)
		}
		if size > operation.manifest.ReferenceBytes-position.offset {
			return page, ErrLocalSnapshotInvalid
		}
		position.offset += size
		position.count++
		rows = append(rows, LocalSnapshotReference{get: func() snapshotReference { return row }})
	}
	done := position.offset == operation.manifest.ReferenceBytes
	if done && position.count != operation.manifest.TotalRecords {
		return page, ErrLocalSnapshotInvalid
	}
	if err = scoped.Err(); err != nil {
		return page, err
	}
	data := snapshotReferencePage{rows: rows, cursor: LocalSnapshotCursor{get: func() snapshotCursor { return position }}, done: done}
	return LocalSnapshotReferencePage{get: func() snapshotReferencePage { return data }}, nil
}

func (page LocalSnapshotReferencePage) Rows() []LocalSnapshotReference {
	if page.get == nil {
		return nil
	}
	return append([]LocalSnapshotReference(nil), page.get().rows...)
}
func (page LocalSnapshotReferencePage) Cursor() LocalSnapshotCursor {
	if page.get == nil {
		return LocalSnapshotCursor{}
	}
	return page.get().cursor
}
func (page LocalSnapshotReferencePage) Done() bool { return page.get != nil && page.get().done }
func (row LocalSnapshotReference) Kind() string {
	if row.get == nil {
		return ""
	}
	return row.get().Kind
}
func (row LocalSnapshotReference) Identity() (int64, string) {
	if row.get == nil {
		return 0, ""
	}
	data := row.get()
	return data.ID, data.PublicID
}
func (row LocalSnapshotReference) Repository() (int64, string, string, string, int64, int64) {
	if row.get == nil || row.get().Kind != "repository" {
		return 0, "", "", "", 0, 0
	}
	data := row.get()
	return data.Owner, data.Name, data.Branch, data.ArchiveHash, data.SkillCount, data.Revision
}
func (row LocalSnapshotReference) Skill() (int64, string, string) {
	if row.get == nil || row.get().Kind != "skill" {
		return 0, "", ""
	}
	data := row.get()
	return data.Repository, data.Path, data.ContentHash
}
func (row LocalSnapshotReference) Pack() (int64, int64, int64) {
	if row.get == nil || row.get().Kind != "pack" {
		return 0, 0, 0
	}
	data := row.get()
	return data.Owner, data.Revision, data.ItemCount
}
func (row LocalSnapshotReference) PackItem() (int64, int64, string) {
	if row.get == nil || row.get().Kind != "pack_item" {
		return 0, 0, ""
	}
	data := row.get()
	return data.Pack, data.Skill, data.Slug
}
