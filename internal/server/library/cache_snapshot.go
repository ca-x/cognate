package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/spf13/afero"
)

var ErrSnapshotArchiveInvalid = errors.New("snapshot archive source refused")
var ErrSnapshotArchiveUncertain = errors.New("snapshot archive source cleanup uncertain")

type snapshotArchivePrivate struct{}

func (snapshotArchivePrivate) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private snapshot archive]")
}
func (snapshotArchivePrivate) MarshalJSON() ([]byte, error) {
	return []byte(`"[private snapshot archive]"`), nil
}
func (snapshotArchivePrivate) LogValue() slog.Value {
	return slog.StringValue("[private snapshot archive]")
}

type SnapshotArchiveSource struct {
	snapshotArchivePrivate
	get func() *snapshotArchiveScope
}

type ArchiveCopySummary struct {
	snapshotArchivePrivate
	bytes  int64
	digest [32]byte
}

func (summary ArchiveCopySummary) Bytes() int64     { return summary.bytes }
func (summary ArchiveCopySummary) SHA256() [32]byte { return summary.digest }

type snapshotArchiveScope struct {
	mutex   sync.Mutex
	cache   *ArchiveCache
	store   *state.Store
	ctx     context.Context
	cancel  context.CancelFunc
	live    bool
	active  int
	changed chan struct{}
	failure error
}

type snapshotArchiveContextKey struct{}
type snapshotArchiveHold struct {
	cache *ArchiveCache
	file  afero.File
}

var snapshotArchiveUncertain sync.Map

func snapshotArchiveError(ctx context.Context, err error) error {
	for _, known := range []error{state.ErrLocalSnapshotUncertain, ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotCleanup} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, known := range []error{ErrSnapshotArchiveInvalid, maintenance.ErrClosed, maintenance.ErrLease, maintenance.ErrQuiescing, maintenance.ErrDirectory, maintenance.ErrLock, maintenance.ErrUnsupported, state.ErrLocalSnapshotInvalid, state.ErrLocalSnapshotBudget, state.ErrLocalSnapshotCleanup, state.ErrLocalSnapshotUncertain} {
		if errors.Is(err, known) {
			return known
		}
	}
	if err != nil {
		return ErrSnapshotArchiveInvalid
	}
	return nil
}

func snapshotArchiveMerge(previous, next error) error {
	for _, known := range []error{state.ErrLocalSnapshotUncertain, ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotCleanup} {
		if errors.Is(previous, known) || errors.Is(next, known) {
			return known
		}
	}
	if next != nil {
		return next
	}
	return previous
}

func (cache *ArchiveCache) WithSnapshotSource(ctx context.Context, store *state.Store, consume func(context.Context, SnapshotArchiveSource) error) (returnedError error) {
	if ctx == nil || cache == nil || cache.root == nil || cache.maintenance == nil || store == nil || !store.MaintenanceManaged() || consume == nil || ctx.Value(snapshotArchiveContextKey{}) != nil {
		return ErrSnapshotArchiveInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	managed := cache.maintenance
	managed.mutex.Lock()
	if managed.closing || managed.snapshotUncertain {
		failure := error(maintenance.ErrClosed)
		if managed.snapshotUncertain {
			failure = ErrSnapshotArchiveUncertain
		}
		managed.mutex.Unlock()
		return failure
	}
	managed.active++
	managed.mutex.Unlock()
	defer func() {
		managed.mutex.Lock()
		managed.active--
		close(managed.changed)
		managed.changed = make(chan struct{})
		managed.mutex.Unlock()
	}()
	if err := ValidateMaintenancePair(ctx, store, cache); err != nil {
		return snapshotArchiveError(ctx, err)
	}
	life, cancel := context.WithCancel(ctx)
	scope := &snapshotArchiveScope{cache: cache, store: store, ctx: life, cancel: cancel, live: true, changed: make(chan struct{})}
	scoped := context.WithValue(life, snapshotArchiveContextKey{}, scope)
	defer func() {
		scope.mutex.Lock()
		scope.live = false
		cancel()
		for scope.active != 0 {
			changed := scope.changed
			scope.mutex.Unlock()
			<-changed
			scope.mutex.Lock()
		}
		returnedError = snapshotArchiveMerge(returnedError, scope.failure)
		scope.mutex.Unlock()
		returnedError = snapshotArchiveError(ctx, returnedError)
	}()
	returnedError = consume(scoped, SnapshotArchiveSource{get: func() *snapshotArchiveScope { return scope }})
	return returnedError
}

func (source SnapshotArchiveSource) CopyVerified(ctx context.Context, hash string, destination io.Writer) (summary ArchiveCopySummary, returnedError error) {
	if ctx == nil || source.get == nil {
		return summary, maintenance.ErrLease
	}
	scope := source.get()
	scope.mutex.Lock()
	if !scope.live || ctx.Value(snapshotArchiveContextKey{}) != scope {
		scope.mutex.Unlock()
		return summary, maintenance.ErrLease
	}
	if scope.failure != nil {
		failure := scope.failure
		scope.mutex.Unlock()
		return summary, failure
	}
	if err := scope.store.ValidateLocalSnapshotContext(ctx); err != nil {
		scope.mutex.Unlock()
		return summary, err
	}
	scope.active++
	scope.mutex.Unlock()
	defer func() {
		if returnedError == nil {
			returnedError = scope.store.ValidateLocalSnapshotContext(ctx)
		}
		if returnedError == nil {
			returnedError = scope.ctx.Err()
		}
		returnedError = snapshotArchiveError(ctx, returnedError)
		scope.mutex.Lock()
		if returnedError != nil {
			summary = ArchiveCopySummary{}
			if scope.failure == nil || errors.Is(returnedError, ErrSnapshotArchiveUncertain) {
				scope.failure = returnedError
			}
			scope.cancel()
		}
		scope.active--
		close(scope.changed)
		scope.changed = make(chan struct{})
		scope.mutex.Unlock()
	}()
	returnedError = scope.store.WithLocalSnapshotReader(ctx, func(scoped context.Context) error {
		var err error
		summary, err = scope.copyVerified(scoped, hash, destination)
		return err
	})
	return summary, returnedError
}

func (scope *snapshotArchiveScope) copyVerified(ctx context.Context, hash string, destination io.Writer) (summary ArchiveCopySummary, returnedError error) {
	if destination == nil || !validArchiveHash(hash) || scope.ctx.Err() != nil {
		return summary, ErrSnapshotArchiveInvalid
	}
	cache := scope.cache
	if err := cache.verifyDirectory(); err != nil {
		return summary, err
	}
	name := hash + ".tar.gz"
	entry, err := cache.root.Lstat(name)
	if err != nil || !snapshotArchivePrivateFile(entry) {
		return summary, ErrSnapshotArchiveInvalid
	}
	file, err := afero.NewReadOnlyFs(cache.filesystem).Open(name)
	if err != nil {
		return summary, ErrSnapshotArchiveInvalid
	}
	defer func() {
		if file.Close() != nil {
			managed := cache.maintenance
			managed.mutex.Lock()
			managed.snapshotUncertain = true
			managed.mutex.Unlock()
			hold := &snapshotArchiveHold{cache: cache, file: file}
			snapshotArchiveUncertain.Store(hold, hold)
			returnedError = ErrSnapshotArchiveUncertain
		}
		if returnedError == nil {
			returnedError = scope.store.ValidateLocalSnapshotContext(ctx)
		}
		if returnedError == nil {
			returnedError = scope.ctx.Err()
		}
	}()
	before, err := file.Stat()
	if err != nil || !snapshotArchivePrivateFile(before) || !os.SameFile(entry, before) || before.Size() <= 0 || before.Size() > repository.MaxArchiveBytes {
		return summary, ErrSnapshotArchiveInvalid
	}
	digest := sha256.New()
	reader := snapshotArchiveReader{ctx: ctx, scope: scope, reader: file}
	count, err := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(reader, before.Size()+1))
	if err != nil {
		return summary, err
	}
	after, err := file.Stat()
	if err != nil || !snapshotArchivePrivateFile(after) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || count != before.Size() || hex.EncodeToString(digest.Sum(nil)) != hash {
		return summary, ErrSnapshotArchiveInvalid
	}
	entry, err = cache.root.Lstat(name)
	if err != nil || !snapshotArchivePrivateFile(entry) || !os.SameFile(entry, after) {
		return summary, ErrSnapshotArchiveInvalid
	}
	if err := cache.verifyDirectory(); err != nil {
		return summary, err
	}
	if err := scope.store.ValidateLocalSnapshotContext(ctx); err != nil {
		return summary, err
	}
	if err := scope.ctx.Err(); err != nil {
		return summary, err
	}
	summary.bytes = count
	copy(summary.digest[:], digest.Sum(nil))
	return summary, nil
}

type snapshotArchiveReader struct {
	ctx    context.Context
	scope  *snapshotArchiveScope
	reader io.Reader
}

func (reader snapshotArchiveReader) Read(output []byte) (int, error) {
	if err := reader.scope.ctx.Err(); err != nil {
		return 0, err
	}
	if err := reader.scope.store.ValidateLocalSnapshotContext(reader.ctx); err != nil {
		return 0, err
	}
	return reader.reader.Read(output)
}
