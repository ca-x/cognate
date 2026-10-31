package backup

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/library"
)

type ControllerOptions struct {
	Generation    [32]byte
	AppVersion    string
	ContentLimits library.ContentSnapshotLimits
	MaxJobs       uint32
	JobTimeout    time.Duration
}

type Job struct {
	ID, Phase, Code      string
	CreatedAt, UpdatedAt time.Time
	Objects              uint32
	CiphertextBytes      int64
	CancelRequested      bool
}

type JobPage struct {
	Jobs []Job
	Next string
}
type Controller struct{ get func() *controllerState }

type controllerState struct {
	store                 *controlState
	service               *library.Service
	target                *S3Target
	engine                SealEngine
	options               ControllerOptions
	fingerprint, instance string
	ctx                   context.Context
	cancel                context.CancelFunc
	active                *controlActive
	closing               atomic.Bool
	closed                bool
}

type controlActive struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

var errControlPutUnknown = errors.New("backup PUT unknown")

func (Controller) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private backup controller]")
}
func (Controller) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (Controller) LogValue() slog.Value         { return slog.StringValue("[private backup controller]") }

func NewController(ctx context.Context, store *ControlStore, service *library.Service, target *S3Target, engine SealEngine, options ControllerOptions) (*Controller, error) {
	if ctx == nil || store == nil || store.get == nil || service == nil || target == nil || engine.get == nil || !controlValidOptions(options) {
		return nil, ErrInvalid
	}
	for _, character := range options.AppVersion {
		if character < 33 || character > 126 {
			return nil, ErrInvalid
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	instance := store.InstanceID()
	if target.options.read == nil || target.options.read().instance != instance {
		return nil, ErrControlConflict
	}
	target.mutex.Lock()
	targetClosed, targetUncertain := target.closed, target.closeFailed
	target.mutex.Unlock()
	if targetUncertain {
		return nil, ErrControlUncertain
	}
	if targetClosed {
		return nil, ErrClosed
	}
	_, release, err := engine.get().enter(ctx)
	if err != nil {
		return nil, controlError(err)
	}
	release()
	digest := controlOptionsHash(options)
	physical := store.get()
	physical.mutex.Lock()
	defer physical.mutex.Unlock()
	if err := physical.check(); err != nil {
		return nil, controlError(err)
	}
	if physical.attached {
		return nil, ErrBusy
	}
	candidate := physical.clone()
	changed := false
	for index := range candidate.Jobs {
		record := &candidate.Jobs[index]
		if controlRunning(record.Job.Phase) {
			record.Job.Phase, record.Job.Code = "interrupted", "interrupted"
			if record.CompleteAttempted {
				record.Job.Phase = "commit_unknown"
			}
			record.Job.UpdatedAt = controlTime(record.Job.UpdatedAt)
			changed = true
		}
	}
	if changed {
		if err := physical.persist(candidate); err != nil {
			return nil, err
		}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	state := &controllerState{store: physical, service: service, target: target, engine: engine, options: options, fingerprint: hex.EncodeToString(digest[:]), instance: physical.data.Instance, ctx: lifetime, cancel: cancel}
	physical.attached = true
	return &Controller{get: func() *controllerState { return state }}, nil
}

func controlValidOptions(options ControllerOptions) bool {
	limits, database := options.ContentLimits, options.ContentLimits.Database
	return options.Generation != [32]byte{} && options.MaxJobs > 0 && options.MaxJobs <= 1000 && options.JobTimeout > 0 && len(options.AppVersion) > 0 && len(options.AppVersion) <= 64 && limits.MaxArchives > 0 && limits.MaxArchives <= 100000 && limits.MaxInventoryBytes > 0 && limits.MaxInventoryBytes <= 64<<20 && limits.MaxContentBytes > 0 && limits.MaxContentBytes <= 1<<40 && database.MaxDBBytes > 0 && database.MaxInventoryBytes > 0 && database.MaxStagingBytes > 0 && database.MaxRecords > 0 && database.MaxRecords <= 1000000 && database.SQLiteHeapBytes >= 65536 && database.SQLiteHeapBytes <= 1<<30 && database.WorkerAddressSpaceBytes > database.SQLiteHeapBytes && database.WorkerCPUSeconds > 0 && database.WorkerCPUSeconds <= math.MaxInt32 && database.MaxWALBytes > 0 && database.MaxSHMBytes >= 32768 && database.DrainTimeout > 0 && database.OperationTimeout > 0
}

func controlOptionsHash(options ControllerOptions) [32]byte {
	limits := options.ContentLimits
	database := limits.Database
	encoded := append([]byte("cognate/server-backup/options/v1\x00"), options.Generation[:]...)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(len(options.AppVersion)))
	encoded = append(encoded, options.AppVersion...)
	for _, number := range []uint64{uint64(options.MaxJobs), uint64(options.JobTimeout), uint64(limits.MaxArchives), uint64(limits.MaxInventoryBytes), uint64(limits.MaxContentBytes), uint64(database.MaxDBBytes), uint64(database.MaxInventoryBytes), uint64(database.MaxStagingBytes), database.MaxRecords, uint64(database.SQLiteHeapBytes), uint64(database.WorkerAddressSpaceBytes), database.WorkerCPUSeconds, uint64(database.MaxWALBytes), uint64(database.MaxSHMBytes), uint64(database.DrainTimeout), uint64(database.OperationTimeout)} {
		encoded = binary.BigEndian.AppendUint64(encoded, number)
	}
	digest := sha256.Sum256(encoded)
	clear(encoded)
	return digest
}

func (physical *controlState) clone() controlJournal {
	candidate := physical.data
	candidate.Jobs = slices.Clone(candidate.Jobs)
	return candidate
}
func controlTime(previous time.Time) time.Time {
	now := time.Now().UTC()
	if now.Before(previous) {
		return previous
	}
	return now
}
func controlRunning(phase string) bool {
	return phase == "pending" || phase == "capturing" || phase == "uploading" || phase == "committing"
}

func (controller *Controller) acquire(ctx context.Context) (*controllerState, error) {
	if controller == nil || controller.get == nil || ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := controller.get()
	if state.closing.Load() {
		return nil, ErrClosed
	}
	state.store.mutex.Lock()
	if state.closed || state.closing.Load() {
		state.store.mutex.Unlock()
		return nil, ErrClosed
	}
	if err := state.store.check(); err != nil {
		state.store.mutex.Unlock()
		return nil, controlError(err)
	}
	return state, nil
}

func (controller *Controller) Start(ctx context.Context, principal, requestID string) (Job, error) {
	if !controlUUID(principal) || !controlUUID(requestID) {
		return Job{}, ErrInvalid
	}
	state, err := controller.acquire(ctx)
	if err != nil {
		return Job{}, err
	}
	defer state.store.mutex.Unlock()
	for _, record := range state.store.data.Jobs {
		if record.Principal == principal && record.Request == requestID {
			if record.Options != state.fingerprint {
				return Job{}, ErrControlConflict
			}
			return record.Job, nil
		}
	}
	if state.active != nil {
		return Job{}, ErrBusy
	}
	if len(state.store.data.Jobs) >= int(state.options.MaxJobs) {
		return Job{}, ErrControlBudget
	}
	now := time.Now().UTC()
	job := Job{ID: uuid.New().String(), Phase: "pending", CreatedAt: now, UpdatedAt: now}
	candidate := state.store.clone()
	candidate.Jobs = append(candidate.Jobs, controlJob{Job: job, Principal: principal, Request: requestID, Generation: hex.EncodeToString(state.options.Generation[:]), Options: state.fingerprint})
	if err := state.store.persist(candidate); err != nil {
		if state.active != nil && state.store.uncertain {
			state.active.cancel()
		}
		return Job{}, err
	}
	scoped, cancel := context.WithTimeout(state.ctx, state.options.JobTimeout)
	active := &controlActive{id: job.ID, cancel: cancel, done: make(chan struct{})}
	state.active = active
	go state.run(scoped, active)
	return job, nil
}

func (state *controllerState) index(id string) int {
	return slices.IndexFunc(state.store.data.Jobs, func(record controlJob) bool { return record.Job.ID == id })
}

func (controller *Controller) Get(ctx context.Context, jobID string) (Job, error) {
	if !controlUUID(jobID) {
		return Job{}, ErrInvalid
	}
	state, err := controller.acquire(ctx)
	if err != nil {
		return Job{}, err
	}
	defer state.store.mutex.Unlock()
	index := state.index(jobID)
	if index < 0 {
		return Job{}, ErrControlNotFound
	}
	return state.store.data.Jobs[index].Job, nil
}

func (controller *Controller) List(ctx context.Context, limit int, before string) (JobPage, error) {
	if limit < 1 || limit > 1000 || before != "" && !controlUUID(before) {
		return JobPage{}, ErrInvalid
	}
	state, err := controller.acquire(ctx)
	if err != nil {
		return JobPage{}, err
	}
	defer state.store.mutex.Unlock()
	end := len(state.store.data.Jobs)
	if before != "" {
		end = state.index(before)
		if end < 0 {
			return JobPage{}, ErrControlNotFound
		}
	}
	page := JobPage{Jobs: make([]Job, 0, min(limit, end))}
	for index := end - 1; index >= 0 && len(page.Jobs) < limit; index-- {
		page.Jobs = append(page.Jobs, state.store.data.Jobs[index].Job)
	}
	if end > limit {
		page.Next = page.Jobs[len(page.Jobs)-1].ID
	}
	return page, nil
}

func (state *controllerState) updateLocked(id string, apply func(*controlJob) error) (Job, error) {
	index := state.index(id)
	if index < 0 {
		return Job{}, ErrControlNotFound
	}
	candidate := state.store.clone()
	if err := apply(&candidate.Jobs[index]); err != nil {
		return Job{}, err
	}
	candidate.Jobs[index].Job.UpdatedAt = controlTime(candidate.Jobs[index].Job.UpdatedAt)
	if err := state.store.persist(candidate); err != nil {
		if state.active != nil && state.store.uncertain {
			state.active.cancel()
		}
		return Job{}, err
	}
	return candidate.Jobs[index].Job, nil
}

func (controller *Controller) Cancel(ctx context.Context, principal, jobID string) (Job, error) {
	if !controlUUID(principal) || !controlUUID(jobID) {
		return Job{}, ErrInvalid
	}
	state, err := controller.acquire(ctx)
	if err != nil {
		return Job{}, err
	}
	defer state.store.mutex.Unlock()
	index := state.index(jobID)
	if index < 0 {
		return Job{}, ErrControlNotFound
	}
	record := state.store.data.Jobs[index]
	if record.Job.Phase == "remote_committed" || record.Job.Phase == "failed" || record.Job.Phase == "canceled" || record.Job.CancelRequested && (record.CompleteAttempted && record.Job.Phase == "commit_unknown" || state.active != nil && state.active.id == jobID) {
		return record.Job, nil
	}
	job, err := state.updateLocked(jobID, func(record *controlJob) error {
		record.Job.CancelRequested = true
		if state.active == nil || state.active.id != jobID {
			if record.CompleteAttempted {
				record.Job.Phase = "commit_unknown"
			} else {
				record.Job.Phase = "canceled"
			}
			record.Job.Code = "canceled"
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if state.active != nil && state.active.id == jobID {
		state.active.cancel()
	}
	return job, nil
}

func (state *controllerState) change(id string, apply func(*controlJob) error) error {
	state.store.mutex.Lock()
	defer state.store.mutex.Unlock()
	if state.closed {
		return ErrClosed
	}
	if state.store.uncertain {
		return ErrControlUncertain
	}
	_, err := state.updateLocked(id, apply)
	return err
}

func (controller *Controller) Reconcile(ctx context.Context, principal, jobID string) (Job, error) {
	if !controlUUID(principal) || !controlUUID(jobID) {
		return Job{}, ErrInvalid
	}
	state, err := controller.acquire(ctx)
	if err != nil {
		return Job{}, err
	}
	index := state.index(jobID)
	if index < 0 {
		state.store.mutex.Unlock()
		return Job{}, ErrControlNotFound
	}
	record := state.store.data.Jobs[index]
	if record.Options != state.fingerprint || record.Generation != hex.EncodeToString(state.options.Generation[:]) {
		state.store.mutex.Unlock()
		return Job{}, ErrControlConflict
	}
	if state.active != nil {
		state.store.mutex.Unlock()
		return Job{}, ErrBusy
	}
	if len(record.Catalog) == 0 {
		state.store.mutex.Unlock()
		return Job{}, ErrControlConflict
	}
	scoped, cancel := context.WithTimeout(ctx, state.options.JobTimeout)
	stop := context.AfterFunc(state.ctx, cancel)
	active := &controlActive{id: jobID, cancel: cancel, done: make(chan struct{})}
	state.active = active
	state.store.mutex.Unlock()
	defer stop()
	defer cancel()
	code := state.verify(scoped, record)
	state.store.mutex.Lock()
	defer state.store.mutex.Unlock()
	job, persistError := state.updateLocked(jobID, func(record *controlJob) error {
		canceled := record.Job.CancelRequested || record.Job.Phase == "canceled"
		if code == "" {
			record.Job.Phase, record.Job.Code = "remote_committed", ""
			record.CompleteAttempted = true
		} else {
			record.Job.Phase, record.Job.Code = "interrupted", code
			if record.CompleteAttempted {
				record.Job.Phase = "commit_unknown"
			} else if canceled {
				record.Job.Phase = "canceled"
				if code != "uncertain" {
					record.Job.Code = "canceled"
				}
			}
		}
		return nil
	})
	if code == "uncertain" {
		state.store.poison()
		persistError = ErrControlUncertain
	}
	state.active = nil
	close(active.done)
	return job, persistError
}

func (state *controllerState) verify(ctx context.Context, record controlJob) string {
	for _, object := range record.Catalog {
		ref, err := object.ref(state.instance, record.Job.ID)
		if err != nil {
			return "invalid"
		}
		verified, err := state.target.VerifyExact(ctx, ref)
		if state.targetUncertain() {
			return "uncertain"
		}
		if verified == Missing && err == nil {
			return "missing"
		}
		if verified != Verified || err != nil {
			return "verify_unknown"
		}
	}
	return ""
}

func (controller *Controller) Close(ctx context.Context) error {
	if controller == nil || controller.get == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	state := controller.get()
	state.closing.Store(true)
	state.cancel()
	if err := state.lockClose(ctx); err != nil {
		return err
	}
	if state.closed {
		state.store.mutex.Unlock()
		return nil
	}
	active := state.active
	state.store.mutex.Unlock()
	if active != nil {
		select {
		case <-active.done:
		case <-ctx.Done():
			return errors.Join(ErrControlUncertain, ctx.Err())
		}
	}
	if err := state.lockClose(ctx); err != nil {
		return err
	}
	defer state.store.mutex.Unlock()
	if state.closed {
		return nil
	}
	if state.store.uncertain {
		return ErrControlUncertain
	}
	state.closed = true
	state.store.attached = false
	return nil
}

func (state *controllerState) lockClose(ctx context.Context) error {
	if state.store.mutex.TryLock() {
		return nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.Join(ErrControlUncertain, ctx.Err())
		case <-ticker.C:
			if state.store.mutex.TryLock() {
				return nil
			}
		}
	}
}

func controlPhase(phase string) bool {
	switch phase {
	case "pending", "capturing", "uploading", "committing", "remote_committed", "commit_unknown", "interrupted", "failed", "canceled":
		return true
	}
	return false
}
func controlCode(code string) bool {
	switch code {
	case "", "invalid", "unavailable", "budget", "conflict", "uncertain", "canceled", "interrupted", "put_unknown", "verify_unknown", "missing":
		return true
	}
	return false
}
