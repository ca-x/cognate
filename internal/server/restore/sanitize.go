package restore

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
)

type SanitizeRequest struct {
	OperationID [16]byte
	RevokedAt   time.Time
}
type Sanitized struct{ state *sanitizedState }
type SanitizedCandidate struct{ state *sanitizedView }
type sanitizedState struct {
	content *candidateState
	root    *runtimegen.OfflineRoot
	request SanitizeRequest
	source  state.RestoreDatabaseEvidence
	facts   state.RestoreRevocationEvidence
}
type sanitizedView struct {
	owner  *sanitizedState
	ctx    context.Context
	active atomic.Bool
	borrow *sanitizedBorrow
	taken  *publicationOwnedFiles
}
type sanitizedBorrow struct {
	root, lock *os.File
	ctx        context.Context
	active     atomic.Bool
}
type publicationOwnedFiles struct {
	files    *candidateFiles
	key      [32]byte
	root     *runtimegen.OfflineRoot
	request  SanitizeRequest
	metadata backup.SealMetadata
	source   state.RestoreDatabaseEvidence
	facts    state.RestoreRevocationEvidence
	phase    atomic.Uint32
}

const (
	publicationPending uint32 = iota
	publicationReady
	publicationQuarantined
)

func SanitizeCandidate(ctx context.Context, root *runtimegen.OfflineRoot, source *Candidate, request SanitizeRequest) (*Sanitized, error) {
	if ctx == nil || root == nil || source == nil || source.value == nil || request.OperationID == [16]byte{} || request.RevokedAt.IsZero() || request.RevokedAt.Location() != time.UTC {
		return nil, ErrCandidateInvalid
	}
	return sanitizeCandidate(ctx, root, source, request)
}

func (owner *Sanitized) WithCandidate(ctx context.Context, root *runtimegen.OfflineRoot, consume func(context.Context, SanitizedCandidate) error) error {
	if owner == nil || owner.state == nil || ctx == nil || root == nil || root != owner.state.root || consume == nil {
		return ErrCandidateInvalid
	}
	taken, err := owner.withCandidate(ctx, root, consume)
	if taken != nil && err == nil && !taken.phase.CompareAndSwap(publicationPending, publicationReady) {
		taken.phase.Store(publicationQuarantined)
		return ErrCandidateUncertain
	}
	return err
}
func (owner *Sanitized) Close() error {
	if owner == nil || owner.state == nil {
		return nil
	}
	return (&Candidate{value: owner.state.content}).Close()
}
func (candidate SanitizedCandidate) Valid() bool {
	if candidate.state == nil || !candidate.state.active.Load() || candidate.state.ctx.Err() != nil || candidate.state.borrow == nil || !candidate.state.borrow.active.Load() {
		return false
	}
	content := candidate.state.owner.content
	content.mutex.Lock()
	defer content.mutex.Unlock()
	return !content.closed && content.ready && content.active && content.files != nil
}
func takeForPublication(candidate SanitizedCandidate) (*publicationOwnedFiles, error) {
	if !candidate.Valid() {
		return nil, ErrCandidateInvalid
	}
	owner := candidate.state.owner
	content := owner.content
	content.mutex.Lock()
	defer content.mutex.Unlock()
	if content.closed || !content.ready || !content.active || content.files == nil || candidate.state.ctx.Err() != nil || !candidate.state.active.Load() {
		return nil, ErrCandidateInvalid
	}
	result := &publicationOwnedFiles{files: content.files, key: content.key, root: owner.root, request: owner.request, metadata: content.metadata, source: owner.source, facts: owner.facts}
	candidate.state.taken = result
	content.files, content.ready = nil, false
	clear(content.key[:])
	candidate.state.active.Store(false)
	return result, nil
}

func (owned *publicationOwnedFiles) readyForPublication(root *runtimegen.OfflineRoot) bool {
	return owned != nil && root != nil && owned.root == root && owned.phase.Load() == publicationReady && owned.files != nil && owned.request.OperationID != [16]byte{} && owned.source.Valid() && owned.facts.Database().Valid()
}
func (Sanitized) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private sanitized candidate owner]")
}
func (Sanitized) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (Sanitized) LogValue() slog.Value {
	return slog.StringValue("[private sanitized candidate owner]")
}
func (SanitizedCandidate) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private sanitized candidate view]")
}
func (SanitizedCandidate) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (SanitizedCandidate) LogValue() slog.Value {
	return slog.StringValue("[private sanitized candidate view]")
}
