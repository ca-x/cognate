package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/state/ent/operation"
	"github.com/gofrs/flock"
)

type Session struct {
	mutex       sync.Mutex
	store       *state.Store
	lock        *flock.Flock
	directory   string
	plan        string
	active      string
	startedWork bool
	stages      map[int]*skill.StagedSnapshot
	projections map[int]*publishedProjection
}

func OpenSession(ctx context.Context, directory string) (*Session, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve installation state directory: %w", err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve installation state directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect installation state directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("installation state path must be a directory")
	}
	database, err := state.ResolveDatabasePath(filepath.Join(absolute, "client.db"))
	if err != nil {
		return nil, fmt.Errorf("resolve installation database: %w", err)
	}
	lock, err := acquireLock(ctx, database+".install.lock")
	if err != nil {
		return nil, fmt.Errorf("lock installation state: %w", err)
	}
	store, err := state.Open(ctx, database)
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return &Session{store: store, lock: lock, directory: absolute}, nil
}

func (session *Session) Close() error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return nil
	}
	err := errors.Join(session.closeOperationHandles(), session.store.Close(), session.lock.Close())
	session.store = nil
	session.lock = nil
	return err
}

func (session *Session) closeOperationHandles() error {
	var handleErrors []error
	for _, stage := range session.stages {
		handleErrors = append(handleErrors, stage.Close())
	}
	for _, projection := range session.projections {
		if projection.link != nil {
			handleErrors = append(handleErrors, projection.link.Close())
		}
		handleErrors = append(handleErrors, projection.root.Close())
	}
	session.stages = nil
	session.projections = nil
	return errors.Join(handleErrors...)
}

func (session *Session) PendingOperations(ctx context.Context) ([]state.Operation, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return nil, fmt.Errorf("installation session is closed")
	}
	return session.store.PendingOperations(ctx)
}

func (session *Session) Installation(ctx context.Context, slug string) (state.Installation, error) {
	if err := ctx.Err(); err != nil {
		return state.Installation{}, err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return state.Installation{}, fmt.Errorf("installation session is closed")
	}
	return session.store.Get(ctx, slug)
}

func (session *Session) Backups(ctx context.Context) ([]state.Backup, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return nil, fmt.Errorf("installation session is closed")
	}
	return session.store.ListBackups(ctx)
}

func (session *Session) RecoverInterruptedOperations(ctx context.Context) ([]state.Operation, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return nil, fmt.Errorf("installation session is closed")
	}
	if session.startedWork {
		return nil, fmt.Errorf("cannot recover operations after starting work in this session")
	}
	pending, err := session.store.PendingOperations(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range pending {
		switch operation.Status(entry.Status) {
		case operation.StatusPlanned:
			err = session.store.CancelPlannedOperation(ctx, entry.Plan.ID)
		case operation.StatusActive:
			err = session.store.MarkOperationNeedsAttention(ctx, entry.Plan.ID)
		case operation.StatusNeedsAttention:
			continue
		default:
			return nil, fmt.Errorf("unknown pending operation state: %q", entry.Status)
		}
		if err != nil {
			return nil, fmt.Errorf("reconcile interrupted operation: %w", err)
		}
	}
	return session.store.PendingOperations(ctx)
}

func (session *Session) BeginOperation(ctx context.Context, plan state.OperationPlan) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return fmt.Errorf("installation session is closed")
	}
	pending, err := session.store.PendingOperations(ctx)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("pending installation operations require recovery")
	}
	if err := session.store.BeginOperation(ctx, plan); err != nil {
		return err
	}
	session.startedWork = true
	session.plan = plan.ID
	return nil
}

func (session *Session) CancelPlannedOperation(ctx context.Context, id string) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return fmt.Errorf("installation session is closed")
	}
	if err := session.store.CancelPlannedOperation(ctx, id); err != nil {
		return err
	}
	if session.plan == id {
		session.plan = ""
	}
	return nil
}

func (session *Session) StartOperation(ctx context.Context, id string) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return fmt.Errorf("installation session is closed")
	}
	if session.plan != id || id == "" {
		return fmt.Errorf("operation was not planned in this session")
	}
	if err := session.store.StartOperation(ctx, id); err != nil {
		return err
	}
	session.plan = ""
	session.active = id
	return nil
}

func (session *Session) CreateStage(ctx context.Context, id string, position int) (string, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return "", fmt.Errorf("installation session is closed")
	}
	if session.active != id || id == "" {
		return "", fmt.Errorf("operation was not started in this session")
	}
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("stage identity is unsupported on this platform")
	}
	entry, err := session.store.GetOperation(ctx, id)
	if err != nil {
		return "", err
	}
	if entry.Status != "active" || entry.Plan.Action != "install" {
		return "", fmt.Errorf("operation is not active")
	}
	if position < 0 || position >= len(entry.Plan.Targets) {
		return "", fmt.Errorf("stage target does not exist")
	}
	target := entry.Plan.Targets[position]
	if target.StageName == "" {
		return "", fmt.Errorf("target has no stage")
	}
	if target.StageIdentity != "" {
		return "", fmt.Errorf("stage identity is already recorded")
	}
	stage, err := skill.CreateStageAt(filepath.Dir(target.Path), target.StageName)
	if err != nil {
		return "", session.stageFailure(ctx, id, err)
	}
	if session.stages == nil {
		session.stages = make(map[int]*skill.StagedSnapshot)
	}
	session.stages[position] = stage
	info, err := stage.Info()
	if err != nil {
		return stage.Path(), session.stageFailure(ctx, id, err)
	}
	identity, err := objectid.FromFileInfo(info)
	if err != nil {
		return stage.Path(), session.stageFailure(ctx, id, err)
	}
	if err := session.store.RecordStageIdentity(ctx, id, position, identity); err != nil {
		return stage.Path(), session.stageFailure(ctx, id, err)
	}
	return stage.Path(), nil
}

func (session *Session) CopyStage(ctx context.Context, id string, position int, source string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return fmt.Errorf("installation session is closed")
	}
	if session.active != id || id == "" {
		return fmt.Errorf("operation was not started in this session")
	}
	stage, exists := session.stages[position]
	if !exists {
		return fmt.Errorf("stage was not created in this session")
	}
	entry, err := session.store.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	if entry.Status != "active" || position < 0 || position >= len(entry.Plan.Targets) {
		return fmt.Errorf("stage target is not active")
	}
	target := entry.Plan.Targets[position]
	if target.StageReady {
		return fmt.Errorf("stage is already ready")
	}
	if err := verifyStageIdentity(stage, target.StageIdentity); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := stage.CopyFromVerifiedContext(ctx, source, entry.Plan.ContentHash); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := verifyStageIdentity(stage, target.StageIdentity); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := session.store.MarkStageReady(ctx, id, position, target.StageIdentity, entry.Plan.ContentHash); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	return nil
}

func verifyStageIdentity(stage *skill.StagedSnapshot, expected string) error {
	info, err := stage.Info()
	if err != nil {
		return err
	}
	identity, err := objectid.FromFileInfo(info)
	if err != nil {
		return err
	}
	if identity != expected {
		return fmt.Errorf("stage identity does not match operation journal")
	}
	return nil
}

func (session *Session) PublishStage(ctx context.Context, id string, position int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return fmt.Errorf("installation session is closed")
	}
	if session.active != id || id == "" {
		return fmt.Errorf("operation was not started in this session")
	}
	stage, exists := session.stages[position]
	if !exists {
		return fmt.Errorf("stage was not created in this session")
	}
	entry, err := session.store.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	if entry.Status != "active" || entry.Plan.Action != "install" || position < 0 || position >= len(entry.Plan.Targets) {
		return fmt.Errorf("operation is not an active installation")
	}
	target := entry.Plan.Targets[position]
	if target.Kind != "canonical" || !target.StageReady || target.PublishIntent || target.Published {
		return fmt.Errorf("stage is not ready for canonical publication")
	}
	info, err := stage.VerifyHash(ctx, entry.Plan.ContentHash)
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	identity, err := objectid.FromFileInfo(info)
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if identity != target.StageIdentity {
		return session.stageFailure(ctx, id, fmt.Errorf("stage identity does not match operation journal"))
	}
	if err := session.store.BeginPublish(ctx, id, position, identity, entry.Plan.ContentHash); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := ctx.Err(); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	parent, name := filepath.Dir(target.Path), filepath.Base(target.Path)
	if err := publishDirectoryChecked(parent, target.StageName, name, info); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := stage.VerifyPublished(ctx, name, entry.Plan.ContentHash); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := session.store.MarkPublished(ctx, id, position, identity, entry.Plan.ContentHash); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	return nil
}

func (session *Session) CompleteInstallation(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil || session.active != id || id == "" {
		return fmt.Errorf("operation was not started in this session")
	}
	entry, err := session.store.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	if entry.Status != "active" || entry.Plan.Action != "install" {
		return fmt.Errorf("installation targets are not all supported and published")
	}
	canonicalPosition, err := canonicalTarget(entry)
	if err != nil {
		return err
	}
	for position, target := range entry.Plan.Targets {
		if !target.Published || target.Kind != "canonical" && target.Kind != "symlink" {
			return fmt.Errorf("installation targets are not all supported and published")
		}
		if target.Kind == "symlink" {
			projection, exists := session.projections[position]
			if !exists {
				return session.stageFailure(ctx, id, fmt.Errorf("projection was not created in this session"))
			}
			if err := projection.verify(target.Path, entry.Plan.Targets[canonicalPosition].Path); err != nil {
				return session.stageFailure(ctx, id, err)
			}
			identity, err := objectid.FromLinkInfo(projection.linkInfo)
			if err != nil || identity != target.ObjectIdentity {
				return session.stageFailure(ctx, id, fmt.Errorf("projection identity does not match operation journal"))
			}
		}
	}
	if err := session.verifyCanonical(ctx, entry, canonicalPosition); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := session.store.CompleteInstallation(ctx, id); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	session.active = ""
	if err := session.closeOperationHandles(); err != nil {
		return fmt.Errorf("installation completed but closing operation handles failed: %w", err)
	}
	return nil
}

func (session *Session) stageFailure(ctx context.Context, id string, cause error) error {
	session.active = ""
	if err := session.store.MarkOperationNeedsAttention(ctx, id); err != nil {
		return errors.Join(cause, fmt.Errorf("attention status was not recorded; reopen and reconcile the operation: %w", err))
	}
	return cause
}

func (session *Session) MarkOperationNeedsAttention(ctx context.Context, id string) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return fmt.Errorf("installation session is closed")
	}
	if err := session.store.MarkOperationNeedsAttention(ctx, id); err != nil {
		return err
	}
	if session.active == id {
		session.active = ""
	}
	return nil
}
