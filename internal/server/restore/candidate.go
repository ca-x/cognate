package restore

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/state"
)

var ErrCandidateInvalid = errors.New("restore candidate refused")
var ErrCandidateBudget = errors.New("restore candidate budget exceeded")
var ErrCandidateBusy = errors.New("restore candidate busy")
var ErrCandidateClosed = errors.New("restore candidate closed")
var ErrCandidateKey = errors.New("restore candidate original key refused")
var ErrCandidateCleanup = errors.New("restore candidate cleanup failed")
var ErrCandidateUncertain = errors.New("restore candidate cleanup uncertain")
var ErrCandidateUnsupported = errors.New("restore candidate unsupported")

type CandidateLimits struct {
	Database                                                               state.LocalSnapshotLimits
	MaxArchives                                                            uint32
	MaxInventoryBytes, MaxContentBytes, MaxStagingBytes, ArchiveCacheBytes int64
	OperationTimeout                                                       time.Duration
}

func (limits CandidateLimits) valid() bool {
	return limits.Database.Valid() && limits.Database.MaxRecords <= 1000000 && limits.MaxArchives > 0 && limits.MaxArchives <= 100000 && limits.MaxInventoryBytes > 0 && limits.MaxInventoryBytes <= 64<<20 && limits.MaxContentBytes > 0 && limits.MaxContentBytes <= 1<<40 && limits.MaxStagingBytes > 0 && limits.MaxStagingBytes <= 1<<40+256 && limits.ArchiveCacheBytes > 0 && limits.ArchiveCacheBytes <= 1<<40 && limits.OperationTimeout > 0
}

func (limits CandidateLimits) cachePolicy() string {
	return "skillhub-archive-budget-v1:" + strconv.FormatInt(limits.ArchiveCacheBytes, 10) + "\n"
}

type Candidate struct{ value *candidateState }
type VerifiedCandidate struct{ value *verifiedCandidateState }
type verifiedCandidateState struct {
	owner    *candidateState
	ctx      context.Context
	active   atomic.Bool
	evidence state.RestoreDatabaseEvidence
}
type candidateState struct {
	mutex                            sync.Mutex
	ctx                              context.Context
	cancel                           context.CancelFunc
	done                             chan struct{}
	handoff                          chan struct{}
	active, closed, ready, delivered bool
	limits                           CandidateLimits
	metadata                         backup.SealMetadata
	key                              [32]byte
	files                            *candidateFiles
	closeOnce                        sync.Once
	closeError                       error
}
type candidateFileOps struct {
	sync        func(*os.File) error
	close       func(*os.File) error
	unlink      func(*os.File, string, bool) error
	finalRemove func(string) error
}
type candidateFiles struct {
	parent, root, archives                       *os.File
	parentInfo, rootInfo, archiveInfo, fenceInfo os.FileInfo
	parentName                                   string
	fenceMade, rootMade, archivesMade            bool
	entries                                      map[string]os.FileInfo
	ops                                          candidateFileOps
	failure                                      error
}

const candidateFence = ".restore-candidate-fence"
const candidateFenceText = "cognate-restore-candidate-v1\n"

func defaultCandidateOps() candidateFileOps {
	return candidateFileOps{sync: (*os.File).Sync, close: (*os.File).Close, unlink: candidateUnlink, finalRemove: os.Remove}
}
func StageCandidate(ctx context.Context, bundle backup.CryptoVerifiedBundle, parent *os.File, originalMasterKey []byte, limits CandidateLimits) (*Candidate, error) {
	return stageCandidate(ctx, bundle, parent, originalMasterKey, limits, defaultCandidateOps())
}
func stageCandidate(ctx context.Context, bundle backup.CryptoVerifiedBundle, parent *os.File, originalMasterKey []byte, limits CandidateLimits, operations candidateFileOps) (result *Candidate, returnedError error) {
	if ctx == nil || parent == nil || !limits.valid() {
		return nil, ErrCandidateInvalid
	}
	if len(originalMasterKey) != 32 {
		return nil, ErrCandidateKey
	}
	if err := ctx.Err(); err != nil {
		return nil, candidateError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, limits.OperationTimeout)
	defer cancel()
	metadata, err := bundle.Metadata(ctx)
	if err != nil {
		return nil, candidateError(err)
	}
	policy := limits.cachePolicy()
	controlBytes := int64(len(policy) + len(candidateFenceText))
	if metadata.Database.Bytes <= 0 || metadata.Inventory.Bytes <= 0 {
		return nil, ErrCandidateInvalid
	}
	if metadata.Database.Bytes > limits.Database.MaxDBBytes || metadata.Inventory.Bytes > limits.MaxInventoryBytes || metadata.Archives > limits.MaxArchives || metadata.Database.Bytes > limits.MaxContentBytes || metadata.Inventory.Bytes > limits.MaxContentBytes-metadata.Database.Bytes || controlBytes > limits.MaxStagingBytes || metadata.Database.Bytes > limits.MaxStagingBytes-controlBytes || metadata.Inventory.Bytes > limits.MaxStagingBytes-controlBytes-metadata.Database.Bytes {
		return nil, ErrCandidateBudget
	}
	lifetime, stop := context.WithCancel(context.Background())
	owned := &candidateState{ctx: lifetime, cancel: stop, limits: limits, metadata: metadata}
	copy(owned.key[:], originalMasterKey)
	result = &Candidate{value: owned}
	stageCompleted := false
	defer func() {
		if !stageCompleted {
			if returnedError == nil {
				returnedError = ErrCandidateUncertain
			}
			if owned.files != nil && errors.Is(candidateError(returnedError), ErrCandidateUncertain) {
				owned.files.failure = candidateMerge(owned.files.failure, ErrCandidateUncertain)
			}
			returnedError = candidateMerge(returnedError, result.Close())
			result = nil
		}
	}()
	owned.files = &candidateFiles{ops: operations}
	err = owned.files.initialize(parent)
	if err != nil {
		return result, err
	}
	for _, file := range []struct {
		name, kind, id string
		size           int64
	}{{"server.db", "database", "database", metadata.Database.Bytes}, {"inventory.jsonl", "inventory", "inventory", metadata.Inventory.Bytes}} {
		if err := owned.files.write(ctx, file.name, file.size, func(output io.Writer) error {
			count, err := bundle.CopyFile(ctx, file.kind, file.id, output)
			if err != nil {
				return err
			}
			if count != file.size {
				return ErrCandidateInvalid
			}
			return nil
		}); err != nil {
			return result, candidateError(err)
		}
	}
	evidence, err := owned.inspectDatabase(ctx)
	if err != nil {
		return result, err
	}
	payload, err := owned.files.read(ctx, "inventory.jsonl", metadata.Inventory.Bytes, limits.MaxInventoryBytes)
	if err != nil {
		return result, err
	}
	defer clear(payload)
	referenceBytes, referenceHash, references := evidence.References()
	description, err := readInventory(ctx, bytes.NewReader(payload), metadata, inventoryReferences{referenceBytes, referenceHash, references}, inventoryLimits{limits.MaxInventoryBytes, limits.MaxArchives, limits.MaxContentBytes})
	if err != nil {
		return result, candidateError(err)
	}
	archiveBytes := description.contentBytes - metadata.Database.Bytes - metadata.Inventory.Bytes
	if archiveBytes > limits.ArchiveCacheBytes || description.contentBytes > limits.MaxStagingBytes-controlBytes {
		return result, ErrCandidateBudget
	}
	if err := owned.files.write(ctx, "archives/.cache-budget", int64(len(policy)), func(output io.Writer) error { _, err := io.WriteString(output, policy); return err }); err != nil {
		return result, err
	}
	for _, archive := range description.archives {
		if archive.Bytes > repository.MaxArchiveBytes {
			return result, ErrCandidateBudget
		}
		if err := owned.files.write(ctx, "archives/"+archive.SHA256+".tar.gz", archive.Bytes, func(output io.Writer) error {
			count, err := bundle.CopyFile(ctx, "archive", archive.SHA256, output)
			if err != nil {
				return err
			}
			if count != archive.Bytes {
				return ErrCandidateInvalid
			}
			return nil
		}); err != nil {
			return result, err
		}
	}
	if err := owned.files.syncDirectories(); err != nil {
		return result, err
	}
	if err := owned.files.checkEntries(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, candidateError(err)
	}
	stageCompleted = true
	return result, nil
}
func (owned *candidateState) inspectDatabase(ctx context.Context) (evidence state.RestoreDatabaseEvidence, err error) {
	return owned.inspectDatabaseUsing(ctx, state.InspectRestoreDatabase)
}
func (owned *candidateState) inspectDatabaseUsing(ctx context.Context, inspect func(context.Context, *os.File, *os.File, []byte, state.LocalSnapshotLimits) (state.RestoreDatabaseEvidence, error)) (evidence state.RestoreDatabaseEvidence, err error) {
	err = owned.files.withFile("server.db", func(database *os.File) error {
		var inspectionError error
		evidence, inspectionError = inspect(ctx, owned.files.root, database, owned.key[:], owned.limits.Database)
		return inspectionError
	})
	if err != nil {
		return state.RestoreDatabaseEvidence{}, candidateError(err)
	}
	length, digest := evidence.Database()
	schema := evidence.SchemaFingerprint()
	if !evidence.Valid() || length != owned.metadata.Database.Bytes || hex.EncodeToString(digest[:]) != owned.metadata.Database.SHA256 || hex.EncodeToString(schema[:]) != owned.metadata.SchemaSHA256 || evidence.KeyIdentity() != owned.metadata.MasterKeyID {
		return state.RestoreDatabaseEvidence{}, ErrCandidateInvalid
	}
	return evidence, nil
}
func (candidate *Candidate) WithVerified(ctx context.Context, consume func(context.Context, VerifiedCandidate) error) (returnedError error) {
	return candidate.withVerified(ctx, nil, consume)
}
func (candidate *Candidate) withVerified(ctx context.Context, inspect func(context.Context) (state.RestoreDatabaseEvidence, error), consume func(context.Context, VerifiedCandidate) error) (returnedError error) {
	if candidate == nil || candidate.value == nil || ctx == nil || consume == nil {
		return ErrCandidateInvalid
	}
	owned := candidate.value
	owned.mutex.Lock()
	if owned.closed {
		owned.mutex.Unlock()
		return ErrCandidateClosed
	}
	if !owned.ready || owned.active || owned.delivered || owned.handoff != nil && inspect == nil {
		owned.mutex.Unlock()
		return ErrCandidateBusy
	}
	owned.active, owned.done = true, make(chan struct{})
	files := owned.files
	owned.mutex.Unlock()
	ctx, cancel := context.WithTimeout(ctx, owned.limits.OperationTimeout)
	stopped := make(chan struct{})
	stop := context.AfterFunc(owned.ctx, func() { cancel(); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	defer cancel()
	defer func() {
		if category := candidateError(returnedError); errors.Is(category, ErrCandidateUncertain) || errors.Is(category, ErrCandidateCleanup) {
			files.failure = candidateMerge(files.failure, category)
		}
		returnedError = candidateMerge(returnedError, files.failure)
		if ctx.Err() != nil {
			returnedError = candidateMerge(returnedError, ctx.Err())
		}
		owned.mutex.Lock()
		owned.active = false
		close(owned.done)
		owned.mutex.Unlock()
	}()
	if err := owned.files.checkEntries(); err != nil {
		return err
	}
	if inspect == nil {
		inspect = owned.inspectDatabase
	}
	evidence, err := inspect(ctx)
	if err != nil {
		return err
	}
	payload, err := owned.files.read(ctx, "inventory.jsonl", owned.metadata.Inventory.Bytes, owned.limits.MaxInventoryBytes)
	if err != nil {
		return err
	}
	defer clear(payload)
	referenceBytes, referenceHash, references := evidence.References()
	description, err := readInventory(ctx, bytes.NewReader(payload), owned.metadata, inventoryReferences{referenceBytes, referenceHash, references}, inventoryLimits{owned.limits.MaxInventoryBytes, owned.limits.MaxArchives, owned.limits.MaxContentBytes})
	if err != nil {
		return candidateError(err)
	}
	expectations, err := candidateArchiveExpectations(ctx, payload, references)
	if err != nil {
		return err
	}
	policyText := owned.limits.cachePolicy()
	policy, err := owned.files.read(ctx, "archives/.cache-budget", int64(len(policyText)), 64)
	if err != nil {
		return err
	}
	policyValid := string(policy) == policyText
	clear(policy)
	if !policyValid {
		return ErrCandidateInvalid
	}
	var usedReferences uint64
	for _, archive := range description.archives {
		repositories := expectations[archive.SHA256]
		for _, expected := range repositories {
			cost := uint64(len(expected.skills)) + 1
			if cost > references-usedReferences {
				return ErrCandidateBudget
			}
			usedReferences += cost
		}
		data, err := owned.files.read(ctx, "archives/"+archive.SHA256+".tar.gz", archive.Bytes, repository.MaxArchiveBytes)
		if err != nil {
			return err
		}
		err = verifyArchive(ctx, data, archiveExpectation{hash: archive.SHA256, bytes: archive.Bytes, repositories: repositories}, archiveLimits{bytes: repository.MaxArchiveBytes, references: references})
		clear(data)
		if err != nil {
			return candidateError(err)
		}
	}
	if err := owned.files.checkEntries(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return candidateError(err)
	}
	owned.mutex.Lock()
	if owned.closed {
		owned.mutex.Unlock()
		return ErrCandidateClosed
	}
	owned.delivered = true
	owned.mutex.Unlock()
	capability := &verifiedCandidateState{owner: owned, ctx: ctx, evidence: evidence}
	capability.active.Store(true)
	defer capability.active.Store(false)
	consumerReturned := false
	defer func() {
		if !consumerReturned {
			files.failure = candidateMerge(files.failure, ErrCandidateUncertain)
		}
	}()
	err = consume(ctx, VerifiedCandidate{value: capability})
	consumerReturned = true
	return candidateError(err)
}
func (candidate *Candidate) Close() error {
	if candidate == nil || candidate.value == nil {
		return nil
	}
	owned := candidate.value
	owned.closeOnce.Do(func() {
		owned.mutex.Lock()
		owned.closed = true
		owned.cancel()
		done, active, handoff := owned.done, owned.active, owned.handoff
		owned.mutex.Unlock()
		if active {
			<-done
		}
		if handoff != nil {
			<-handoff
		}
		clear(owned.key[:])
		if owned.files != nil {
			owned.closeError = owned.files.cleanup()
		}
	})
	return owned.closeError
}
func (verified VerifiedCandidate) Valid() bool {
	if verified.value == nil || !verified.value.active.Load() || verified.value.ctx.Err() != nil {
		return false
	}
	owned := verified.value.owner
	owned.mutex.Lock()
	defer owned.mutex.Unlock()
	return !owned.closed && owned.active && owned.ready
}
func (verified VerifiedCandidate) ClosureSHA256() [32]byte {
	if !verified.Valid() {
		return [32]byte{}
	}
	digest, _ := inventoryHash(verified.value.owner.metadata.ClosureSHA256)
	return digest
}
func (verified VerifiedCandidate) ObservationMaxAge() time.Duration {
	if !verified.Valid() {
		return 0
	}
	return verified.value.evidence.ObservationMaxAge()
}
func (Candidate) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private restore candidate]")
}
func (Candidate) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (Candidate) LogValue() slog.Value         { return slog.StringValue("[private restore candidate]") }
func (VerifiedCandidate) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private verified restore candidate]")
}
func (VerifiedCandidate) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (VerifiedCandidate) LogValue() slog.Value {
	return slog.StringValue("[private verified restore candidate]")
}
func candidateError(err error) error {
	if err == nil {
		return nil
	}
	for _, group := range []struct {
		output error
		inputs []error
	}{
		{ErrCandidateUncertain, []error{ErrCandidateUncertain, state.ErrRestoreUncertain, backup.ErrSealUncertain}},
		{ErrCandidateCleanup, []error{ErrCandidateCleanup, backup.ErrSealCleanup}},
		{context.Canceled, []error{context.Canceled}},
		{context.DeadlineExceeded, []error{context.DeadlineExceeded}},
		{ErrCandidateKey, []error{ErrCandidateKey, state.ErrRestoreKey}},
		{ErrCandidateBudget, []error{ErrCandidateBudget, errInventoryBudget, errArchiveBudget, state.ErrRestoreBudget, backup.ErrSealBudget}},
		{ErrCandidateUnsupported, []error{ErrCandidateUnsupported, state.ErrRestoreUnsupported, backup.ErrUnsupported}},
		{ErrCandidateBusy, []error{ErrCandidateBusy}},
		{ErrCandidateClosed, []error{ErrCandidateClosed}},
	} {
		for _, known := range group.inputs {
			if errors.Is(err, known) {
				return group.output
			}
		}
	}
	return ErrCandidateInvalid
}
func candidateMerge(previous, next error) error { return candidateError(errors.Join(previous, next)) }
