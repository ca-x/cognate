//go:build linux

package restore

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/sys/unix"
)

type sanitizeGuard struct {
	file      *os.File
	root      *runtimegen.OfflineRoot
	closed    bool
	closeFile func(*os.File) error
}

func newSanitizeGuard(ctx context.Context, root *runtimegen.OfflineRoot, closeFile func(*os.File) error) (*sanitizeGuard, error) {
	guard := &sanitizeGuard{root: root, closeFile: closeFile}
	err := root.WithWorkerFiles(ctx, func(_, lock *os.File) error {
		descriptor, err := unix.FcntlInt(lock.Fd(), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return ErrCandidateInvalid
		}
		guard.file = os.NewFile(uintptr(descriptor), "restore coordinator guard")
		return nil
	})
	if err != nil {
		if guard.file != nil {
			guard.retain()
		}
		return nil, candidateError(err)
	}
	return guard, nil
}
func (guard *sanitizeGuard) retain() {
	if guard == nil || guard.file == nil || guard.closed {
		return
	}
	candidateRetained.Lock()
	candidateRetained.files = append(candidateRetained.files, guard.file)
	candidateRetained.Unlock()
}
func (guard *sanitizeGuard) finish(ctx context.Context) error {
	if guard == nil {
		return nil
	}
	closed := false
	err := guard.root.WithRetainedWorkerFiles(ctx, func(_, _ *os.File) error {
		if guard.closeFile(guard.file) != nil {
			guard.retain()
			return runtimegen.ErrUncertain
		}
		closed = true
		guard.closed = true
		return nil
	})
	if !closed {
		guard.retain()
	}
	if errors.Is(err, runtimegen.ErrUncertain) {
		return ErrCandidateUncertain
	}
	return candidateError(err)
}

func sanitizeCandidate(ctx context.Context, root *runtimegen.OfflineRoot, source *Candidate, request SanitizeRequest) (result *Sanitized, returnedError error) {
	owned := source.value
	ctx, cancel := context.WithTimeout(ctx, owned.limits.OperationTimeout)
	defer cancel()
	var guard *sanitizeGuard
	var barrier chan struct{}
	var before state.RestoreDatabaseEvidence
	var facts state.RestoreRevocationEvidence
	mutation := false
	coreReturned := false
	owned.mutex.Lock()
	if owned.closed {
		owned.mutex.Unlock()
		return nil, ErrCandidateClosed
	}
	if !owned.ready || owned.active || owned.delivered || owned.handoff != nil || owned.files == nil {
		owned.mutex.Unlock()
		return nil, ErrCandidateBusy
	}
	barrier = make(chan struct{})
	owned.handoff = barrier
	closeFile := owned.files.ops.close
	owned.mutex.Unlock()
	defer func() {
		if !coreReturned && guard != nil {
			returnedError = ErrCandidateUncertain
		}
		if returnedError != nil && !mutation && !errors.Is(candidateError(returnedError), ErrCandidateUncertain) && guard != nil {
			returnedError = candidateMerge(returnedError, guard.finish(ctx))
		}
		quarantine := mutation || errors.Is(candidateError(returnedError), ErrCandidateUncertain)
		if returnedError != nil && quarantine && owned.files != nil {
			failure := ErrCandidateUncertain
			if guard != nil && guard.closed {
				failure = ErrCandidateCleanup
			}
			owned.files.failure = candidateMerge(owned.files.failure, failure)
			guard.retain()
			returnedError = candidateMerge(returnedError, failure)
		}
		if barrier != nil {
			owned.mutex.Lock()
			close(barrier)
			owned.handoff = nil
			owned.mutex.Unlock()
		}
		if returnedError != nil && quarantine {
			returnedError = candidateMerge(returnedError, source.Close())
		}
		if returnedError != nil {
			result = nil
		}
	}()
	var guardError error
	guard, guardError = newSanitizeGuard(ctx, root, closeFile)
	if guardError != nil {
		return nil, guardError
	}
	inspect := func(ctx context.Context) (state.RestoreDatabaseEvidence, error) {
		return owned.inspectDatabaseUsing(ctx, func(ctx context.Context, parent, database *os.File, key []byte, limits state.LocalSnapshotLimits) (state.RestoreDatabaseEvidence, error) {
			return state.InspectRestoreCandidateOffline(ctx, root, parent, database, key, limits)
		})
	}
	returnedError = source.withVerified(ctx, inspect, func(ctx context.Context, verified VerifiedCandidate) error {
		before = verified.value.evidence
		if err := sanitizeBudget(owned); err != nil {
			return err
		}
		owned.mutex.Lock()
		if owned.closed || owned.handoff != barrier {
			owned.mutex.Unlock()
			return ErrCandidateClosed
		}
		owned.ready = false
		mutation = true
		owned.mutex.Unlock()
		return owned.files.mutateDatabase(ctx, func(database *os.File) (int64, [32]byte, error) {
			var err error
			facts, err = state.RevokeRestoreCandidate(ctx, root, owned.files.root, database, owned.key[:], before, request.OperationID, request.RevokedAt, owned.limits.Database)
			size, digest := facts.Database().Database()
			return size, digest, err
		})
	})
	coreReturned = true
	if returnedError != nil {
		return nil, returnedError
	}
	if err := ctx.Err(); err != nil {
		return nil, candidateError(err)
	}
	owned.mutex.Lock()
	defer owned.mutex.Unlock()
	if owned.closed || ctx.Err() != nil {
		return nil, ErrCandidateClosed
	}
	if err := guard.finish(ctx); err != nil {
		return nil, err
	}
	lifetime, stop := context.WithCancel(context.Background())
	content := &candidateState{ctx: lifetime, cancel: stop, limits: owned.limits, metadata: owned.metadata, key: owned.key, files: owned.files, ready: true}
	owned.files = nil
	clear(owned.key[:])
	return &Sanitized{state: &sanitizedState{content: content, root: root, request: request, source: before, facts: facts}}, nil
}

func sanitizeBudget(owned *candidateState) error {
	maximum := owned.limits.Database.MaxDBBytes
	if maximum <= 0 || maximum > 1<<40 {
		return ErrCandidateBudget
	}
	journal := 2*maximum + 65536
	remaining := owned.limits.MaxStagingBytes
	contentRemaining := owned.limits.MaxContentBytes - maximum
	if contentRemaining < 0 {
		return ErrCandidateBudget
	}
	for _, size := range []int64{maximum, journal, int64(len(candidateFenceText))} {
		if size > remaining {
			return ErrCandidateBudget
		}
		remaining -= size
	}
	for name, info := range owned.files.entries {
		if name == "server.db" {
			continue
		}
		if info == nil || info.Size() < 0 || info.Size() > remaining {
			return ErrCandidateBudget
		}
		remaining -= info.Size()
		if name == "inventory.jsonl" || strings.HasSuffix(name, ".tar.gz") {
			if info.Size() > contentRemaining {
				return ErrCandidateBudget
			}
			contentRemaining -= info.Size()
		}
	}
	if maximum+journal > owned.limits.Database.MaxStagingBytes {
		return ErrCandidateBudget
	}
	return nil
}

func (owner *Sanitized) withCandidate(ctx context.Context, root *runtimegen.OfflineRoot, consume func(context.Context, SanitizedCandidate) error) (taken *publicationOwnedFiles, returnedError error) {
	owned := owner.state.content
	var scope *sanitizedView
	completed := false
	defer func() {
		owned.mutex.Lock()
		if scope != nil {
			taken = scope.taken
		}
		owned.mutex.Unlock()
		if taken != nil && (!completed || returnedError != nil) {
			taken.phase.Store(publicationQuarantined)
		}
	}()
	inspect := func(ctx context.Context) (evidence state.RestoreDatabaseEvidence, err error) {
		err = owned.files.withFile("server.db", func(database *os.File) error {
			var err error
			evidence, err = state.InspectRestoreCandidateOffline(ctx, root, owned.files.root, database, owned.key[:], owned.limits.Database)
			return err
		})
		if err != nil {
			return state.RestoreDatabaseEvidence{}, err
		}
		if !sanitizeSameEvidence(evidence, owner.state.facts.Database()) {
			return state.RestoreDatabaseEvidence{}, ErrCandidateInvalid
		}
		return evidence, nil
	}
	returnedError = (&Candidate{value: owned}).withVerified(ctx, inspect, func(ctx context.Context, _ VerifiedCandidate) error {
		return candidateError(root.WithWorkerFiles(ctx, func(rootFile, lock *os.File) error {
			borrow := &sanitizedBorrow{root: rootFile, lock: lock, ctx: ctx}
			borrow.active.Store(true)
			defer borrow.active.Store(false)
			view := &sanitizedView{owner: owner.state, ctx: ctx, borrow: borrow}
			scope = view
			view.active.Store(true)
			defer view.active.Store(false)
			return consume(ctx, SanitizedCandidate{state: view})
		}))
	})
	completed = true
	return taken, returnedError
}

func sanitizeSameEvidence(first, second state.RestoreDatabaseEvidence) bool {
	firstSize, firstHash := first.Database()
	secondSize, secondHash := second.Database()
	firstBytes, firstReferences, firstRows := first.References()
	secondBytes, secondReferences, secondRows := second.References()
	firstRepos, firstSkills, firstPacks, firstItems := first.ReferenceCounts()
	secondRepos, secondSkills, secondPacks, secondItems := second.ReferenceCounts()
	return first.Valid() && second.Valid() && firstSize == secondSize && firstHash == secondHash && firstBytes == secondBytes && firstReferences == secondReferences && firstRows == secondRows && firstRepos == secondRepos && firstSkills == secondSkills && firstPacks == secondPacks && firstItems == secondItems && first.SchemaFingerprint() == second.SchemaFingerprint() && first.KeyIdentity() == second.KeyIdentity() && first.ObservationMaxAge() == second.ObservationMaxAge()
}
