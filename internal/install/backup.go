package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"uuid"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/stagename"
	"github.com/ca-x/cognate/internal/state"
)

func (session *Session) BackupLocal(ctx context.Context, slug string) (result state.Backup, resultErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if runtime.GOOS != "linux" {
		return result, fmt.Errorf("local backup is unsupported on this platform")
	}
	if err := ValidateSlug(slug); err != nil {
		return result, err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.backupLocalLocked(ctx, slug)
}

func (session *Session) backupLocalLocked(ctx context.Context, slug string) (result state.Backup, resultErr error) {
	if session.store == nil {
		return result, fmt.Errorf("installation session is closed")
	}
	pending, err := session.store.PendingOperations(ctx)
	if err != nil {
		return result, err
	}
	if len(pending) != 0 {
		return result, fmt.Errorf("pending installation operations require recovery")
	}
	installed, err := session.store.Get(ctx, slug)
	if err != nil {
		return result, err
	}
	var canonical *state.Projection
	for index := range installed.Projections {
		if installed.Projections[index].Kind == "canonical" {
			if canonical != nil {
				return result, fmt.Errorf("installation has multiple canonical directories")
			}
			canonical = &installed.Projections[index]
		}
	}
	if canonical == nil {
		return result, fmt.Errorf("installation has no canonical directory")
	}
	entry, err := os.Lstat(canonical.Path)
	if err != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("canonical source is unavailable or changed")
	}
	source, err := os.OpenRoot(canonical.Path)
	if err != nil {
		return result, fmt.Errorf("open backup source: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	opened, err := source.Stat(".")
	if err != nil || !os.SameFile(entry, opened) {
		return result, fmt.Errorf("canonical source changed while opening")
	}
	sourceIdentity, err := objectid.FromFileInfo(opened)
	if err != nil {
		return result, err
	}
	if canonical.Identity != "" && canonical.Identity != sourceIdentity {
		return result, fmt.Errorf("canonical source identity differs from the installation record")
	}
	if err := validateTrustedRoot(ctx, source, true); err != nil {
		return result, fmt.Errorf("untrusted backup source: %w", err)
	}
	fingerprint, err := skill.BackupFingerprintRootContext(ctx, source)
	if err != nil {
		return result, err
	}
	stateInfo, err := os.Stat(session.directory)
	if err != nil {
		return result, fmt.Errorf("inspect state directory: %w", err)
	}
	if err := validatePrivateDirectory(stateInfo); err != nil {
		return result, err
	}
	if err := rejectSourceContainingDirectory(ctx, source, stateInfo); err != nil {
		return result, err
	}
	backupRoot, backupParent, err := session.openBackupRoot(ctx, stateInfo)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, backupRoot.Close()) }()
	if err := verifyBackupSource(source, canonical.Path, opened, fingerprint.RootMode); err != nil {
		return result, err
	}
	identifier := uuid.New().String()
	stageName, backupPath := stagename.Prefix+identifier, filepath.Join(backupParent, identifier)
	plan := state.OperationPlan{ID: identifier, Action: "backup", Slug: slug, ContentHash: fingerprint.ContentHash,
		Backup:  state.BackupPlan{Format: state.BackupFormatV1, ModeHash: fingerprint.ModeHash, RootMode: fingerprint.RootMode, SourceIdentity: sourceIdentity, InstallationGeneration: installed.Generation, InstallationHash: installed.ContentHash},
		Targets: []state.OperationTarget{{Path: canonical.Path, Kind: "canonical"}, {Path: backupPath, Kind: "backup", StageName: stageName}}}
	if err := session.store.BeginOperation(ctx, plan); err != nil {
		return result, fmt.Errorf("backup intent may need reconciliation: %w", err)
	}
	session.startedWork = true
	if err := session.store.StartOperation(ctx, identifier); err != nil {
		return result, fmt.Errorf("backup start may need reconciliation: %w", err)
	}
	session.active = identifier
	defer func() {
		if resultErr != nil && session.active == identifier {
			resultErr = session.stageFailure(ctx, identifier, resultErr)
		}
	}()
	stage, err := skill.CreateStageAt(backupParent, stageName)
	if err != nil {
		return result, err
	}
	if session.stages == nil {
		session.stages = make(map[int]*skill.StagedSnapshot)
	}
	session.stages[1] = stage
	stageInfo, err := stage.Info()
	if err != nil {
		return result, err
	}
	stageIdentity, err := objectid.FromFileInfo(stageInfo)
	if err != nil {
		return result, err
	}
	if err := session.store.RecordStageIdentity(ctx, identifier, 1, stageIdentity); err != nil {
		return result, err
	}
	if err := stage.CopyAllFromRootVerifiedContext(ctx, source, fingerprint.ContentHash); err != nil {
		return result, err
	}
	if err := verifyBackupSource(source, canonical.Path, opened, fingerprint.RootMode); err != nil {
		return result, err
	}
	current, err := skill.BackupFingerprintRootContext(ctx, source)
	if err != nil {
		return result, fmt.Errorf("recheck backup source: %w", err)
	}
	if current != fingerprint {
		return result, fmt.Errorf("backup source changed during copy")
	}
	stageInfo, err = stage.VerifyBackup(ctx, fingerprint)
	if err != nil {
		return result, err
	}
	if err := session.store.MarkStageReady(ctx, identifier, 1, stageIdentity, fingerprint.ContentHash); err != nil {
		return result, err
	}
	if err := session.store.BeginBackupPublish(ctx, identifier, 1, stageIdentity, fingerprint.ContentHash); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := verifyBackupParent(backupRoot, backupParent); err != nil {
		return result, err
	}
	if err := publishDirectoryChecked(backupParent, stageName, identifier, stageInfo); err != nil {
		return result, err
	}
	if err := stage.VerifyPublishedBackup(ctx, identifier, fingerprint); err != nil {
		return result, err
	}
	if err := verifyBackupSource(source, canonical.Path, opened, fingerprint.RootMode); err != nil {
		return result, err
	}
	current, err = skill.BackupFingerprintRootContext(ctx, source)
	if err != nil {
		return result, fmt.Errorf("recheck backup source: %w", err)
	}
	if current != fingerprint {
		return result, fmt.Errorf("backup source changed before completion")
	}
	if err := session.store.CompleteBackup(ctx, identifier, stageIdentity); err != nil {
		return result, err
	}
	session.active = ""
	result = state.Backup{ID: identifier, Slug: slug, Format: state.BackupFormatV1, SourcePath: canonical.Path, SourceIdentity: sourceIdentity, InstallationGeneration: installed.Generation, InstallationHash: installed.ContentHash, ContentHash: fingerprint.ContentHash, ModeHash: fingerprint.ModeHash, RootMode: fingerprint.RootMode, Path: backupPath, Identity: stageIdentity}
	if err := session.closeOperationHandles(); err != nil {
		return result, fmt.Errorf("backup completed but closing handles failed: %w", err)
	}
	return result, nil
}

func verifyBackupSource(root *os.Root, path string, expected os.FileInfo, rootMode int) error {
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(expected, opened) || int(opened.Mode().Perm()) != rootMode {
		return fmt.Errorf("opened backup source changed")
	}
	entry, err := os.Lstat(path)
	if err != nil || !entry.IsDir() || !os.SameFile(expected, entry) {
		return fmt.Errorf("backup source path changed")
	}
	return nil
}

func rejectSourceContainingDirectory(ctx context.Context, source *os.Root, directory os.FileInfo) error {
	return fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := source.Lstat(path)
		if err != nil {
			return err
		}
		if os.SameFile(info, directory) {
			return fmt.Errorf("state directory cannot be inside the backup source")
		}
		return nil
	})
}

func (session *Session) openBackupRoot(ctx context.Context, stateInfo os.FileInfo) (*os.Root, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	parent, err := os.OpenRoot(session.directory)
	if err != nil {
		return nil, "", err
	}
	defer parent.Close()
	opened, err := parent.Stat(".")
	if err != nil || !os.SameFile(stateInfo, opened) {
		return nil, "", fmt.Errorf("state directory changed")
	}
	info, err := parent.Lstat("backups")
	if errors.Is(err, fs.ErrNotExist) {
		if err := parent.Mkdir("backups", 0o700); err != nil {
			return nil, "", err
		}
		info, err = parent.Lstat("backups")
	}
	if err != nil {
		return nil, "", err
	}
	if err := validatePrivateDirectory(info); err != nil {
		return nil, "", err
	}
	root, err := parent.OpenRoot("backups")
	if err != nil {
		return nil, "", err
	}
	opened, err = root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, "", fmt.Errorf("backup directory changed while opening")
	}
	path := filepath.Join(session.directory, "backups")
	if err := verifyBackupParent(root, path); err != nil {
		_ = root.Close()
		return nil, "", err
	}
	return root, path, nil
}

func verifyBackupParent(root *os.Root, path string) error {
	opened, err := root.Stat(".")
	if err != nil {
		return err
	}
	entry, err := os.Lstat(path)
	if err != nil || !entry.IsDir() || !os.SameFile(opened, entry) {
		return fmt.Errorf("backup directory changed")
	}
	return validatePrivateDirectory(entry)
}
