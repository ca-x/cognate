package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"uuid"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/stagename"
	"github.com/ca-x/cognate/internal/state"
)

func (session *Session) RestoreLocal(ctx context.Context, backupID, parent, name string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("local restore is unsupported on this platform")
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || !filepath.IsLocal(name) {
		return fmt.Errorf("restore destination must be a single name")
	}
	parent, err := skill.ResolvePath(parent)
	if err != nil {
		return fmt.Errorf("resolve restore parent: %w", err)
	}
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
	backup, err := session.store.GetBackup(ctx, backupID)
	if err != nil {
		return err
	}
	if backup.Format != state.BackupFormatV1 || backup.ID == "" || filepath.Base(backup.ID) != backup.ID || !filepath.IsLocal(backup.ID) || backup.Path != filepath.Join(session.directory, "backups", backup.ID) {
		return fmt.Errorf("unsupported backup format or path")
	}
	stateInfo, err := os.Stat(session.directory)
	if err != nil {
		return fmt.Errorf("inspect state directory: %w", err)
	}
	if err := validatePrivateDirectory(stateInfo); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(session.directory, "backups")); err != nil {
		return fmt.Errorf("inspect backup directory: %w", err)
	}
	backupParent, backupPath, err := session.openBackupRoot(ctx, stateInfo)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, backupParent.Close()) }()
	entry, err := backupParent.Lstat(backup.ID)
	if err != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup snapshot is unavailable or changed")
	}
	source, err := backupParent.OpenRoot(backup.ID)
	if err != nil {
		return fmt.Errorf("open backup snapshot: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	opened, err := source.Stat(".")
	if err != nil || !os.SameFile(entry, opened) {
		return fmt.Errorf("backup snapshot changed while opening")
	}
	identity, err := objectid.FromFileInfo(opened)
	if err != nil || identity != backup.Identity {
		return fmt.Errorf("backup snapshot identity differs from record")
	}
	if err := validatePrivateDirectory(opened); err != nil {
		return err
	}
	observed, err := skill.BackupFingerprintRootContext(ctx, source)
	if err != nil {
		return fmt.Errorf("inspect backup snapshot contents: %w", err)
	}
	if observed.ContentHash != backup.ContentHash || observed.ModeHash != backup.ModeHash {
		return fmt.Errorf("backup snapshot content or permissions differ from record")
	}
	destinationParent, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("open restore parent: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, destinationParent.Close()) }()
	parentInfo, err := destinationParent.Stat(".")
	if err != nil {
		return err
	}
	if err := verifyRestoreParent(destinationParent, parent, parentInfo); err != nil {
		return err
	}
	if err := rejectSourceContainingDirectory(ctx, source, parentInfo); err != nil {
		return err
	}
	if _, err := destinationParent.Lstat(name); err == nil {
		return fmt.Errorf("restore destination already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect restore destination: %w", err)
	}
	identifier := uuid.New().String()
	stageName := stagename.Prefix + identifier
	plan := state.OperationPlan{ID: identifier, Action: "restore", Slug: backup.Slug, ContentHash: backup.ContentHash,
		Backup:  state.BackupPlan{BackupID: backup.ID, Format: backup.Format, ModeHash: backup.ModeHash, RootMode: backup.RootMode, SourceIdentity: backup.Identity},
		Targets: []state.OperationTarget{{Path: backup.Path, Kind: "backup_source"}, {Path: filepath.Join(parent, name), Kind: "restore_destination", StageName: stageName}}}
	if err := session.store.BeginOperation(ctx, plan); err != nil {
		return fmt.Errorf("restore operation %s intent may need reconciliation; inspect install-status: %w", identifier, err)
	}
	session.startedWork = true
	if err := session.store.StartOperation(ctx, identifier); err != nil {
		return fmt.Errorf("restore operation %s may need reconciliation; inspect install-status: %w", identifier, err)
	}
	session.active = identifier
	defer func() {
		if resultErr != nil && session.active == identifier {
			resultErr = fmt.Errorf("restore operation %s may have published %s; inspect install-status: %w", identifier, filepath.Join(parent, name), session.stageFailure(ctx, identifier, resultErr))
		}
	}()
	stage, err := skill.CreateStageAt(parent, stageName)
	if err != nil {
		return err
	}
	if session.stages == nil {
		session.stages = make(map[int]*skill.StagedSnapshot)
	}
	session.stages[1] = stage
	stageInfo, err := stage.Info()
	if err != nil {
		return err
	}
	stageIdentity, err := objectid.FromFileInfo(stageInfo)
	if err != nil {
		return err
	}
	if err := session.store.RecordStageIdentity(ctx, identifier, 1, stageIdentity); err != nil {
		return err
	}
	if err := stage.CopyAllFromRootVerifiedContext(ctx, source, backup.ContentHash); err != nil {
		return err
	}
	fingerprint := skill.BackupFingerprint{ContentHash: backup.ContentHash, ModeHash: backup.ModeHash, RootMode: backup.RootMode}
	stageInfo, err = stage.VerifyBackup(ctx, fingerprint)
	if err != nil {
		return err
	}
	if err := verifyRestoreSource(ctx, source, filepath.Join(backupPath, backup.ID), opened, observed); err != nil {
		return err
	}
	if err := session.store.MarkStageReady(ctx, identifier, 1, stageIdentity, backup.ContentHash); err != nil {
		return err
	}
	if err := session.store.BeginRestorePublish(ctx, identifier, 1, stageIdentity, backup.ContentHash); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyRestoreParent(destinationParent, parent, parentInfo); err != nil {
		return err
	}
	if err := publishDirectoryChecked(parent, stageName, name, stageInfo); err != nil {
		return err
	}
	if err := stage.SetPublishedBackupRootMode(ctx, name, fingerprint); err != nil {
		return err
	}
	if err := verifyRestoreSource(ctx, source, filepath.Join(backupPath, backup.ID), opened, observed); err != nil {
		return err
	}
	if err := session.store.CompleteRestore(ctx, identifier, stageIdentity); err != nil {
		return err
	}
	session.active = ""
	if err := session.closeOperationHandles(); err != nil {
		return fmt.Errorf("restore completed but closing handles failed: %w", err)
	}
	return nil
}

func verifyRestoreParent(root *os.Root, path string, expected os.FileInfo) error {
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		return fmt.Errorf("opened restore parent changed")
	}
	entry, err := os.Stat(path)
	if err != nil || !entry.IsDir() || !os.SameFile(expected, entry) {
		return fmt.Errorf("restore parent path changed")
	}
	return nil
}

func verifyRestoreSource(ctx context.Context, root *os.Root, path string, expected os.FileInfo, fingerprint skill.BackupFingerprint) error {
	if err := verifyBackupSource(root, path, expected, fingerprint.RootMode); err != nil {
		return err
	}
	current, err := skill.BackupFingerprintRootContext(ctx, root)
	if err != nil {
		return err
	}
	if current != fingerprint {
		return fmt.Errorf("backup snapshot changed during restore")
	}
	return nil
}
