package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"uuid"

	"github.com/ca-x/skillhub/internal/objectid"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/stagename"
	"github.com/ca-x/skillhub/internal/state"
	"github.com/spf13/afero"
)

func (session *Session) PrepareUninstallLocal(ctx context.Context, slug string) (backup state.Backup, operationID string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return backup, "", err
	}
	if runtime.GOOS != "linux" {
		return backup, "", fmt.Errorf("local uninstall is unsupported on this platform")
	}
	if err := rejectWSL(); err != nil {
		return backup, "", err
	}
	if err := ValidateSlug(slug); err != nil {
		return backup, "", err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return backup, "", fmt.Errorf("installation session is closed")
	}
	pending, err := session.store.PendingOperations(ctx)
	if err != nil {
		return backup, "", err
	}
	if len(pending) != 0 {
		return backup, "", fmt.Errorf("pending installation operations require recovery")
	}
	installed, err := session.store.Get(ctx, slug)
	if err != nil {
		return backup, "", err
	}
	if installed.Generation == "" {
		return backup, "", fmt.Errorf("installation has no managed generation")
	}
	var canonical state.Projection
	for _, projection := range installed.Projections {
		if err := objectid.Validate(projection.Identity); err != nil {
			return backup, "", fmt.Errorf("installation target has no identity")
		}
		if projection.Kind == "canonical" {
			if canonical.Path != "" {
				return backup, "", fmt.Errorf("installation has multiple canonical directories")
			}
			canonical = projection
		} else if projection.Kind != "symlink" {
			return backup, "", fmt.Errorf("unsupported uninstall projection")
		}
	}
	if canonical.Path == "" {
		return backup, "", fmt.Errorf("installation has no canonical directory")
	}
	root, err := os.OpenRoot(canonical.Path)
	if err != nil {
		return backup, "", fmt.Errorf("open canonical source: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	opened, err := root.Stat(".")
	if err != nil {
		return backup, "", err
	}
	identity, err := objectid.FromFileInfo(opened)
	if err != nil || identity != canonical.Identity {
		return backup, "", fmt.Errorf("canonical identity differs from installation")
	}
	if err := validateTrustedRoot(ctx, root, true); err != nil {
		return backup, "", fmt.Errorf("untrusted canonical source: %w", err)
	}
	fingerprint, err := skill.BackupFingerprintRootContext(ctx, root)
	if err != nil {
		return backup, "", err
	}
	if err := verifyUninstallCanonical(ctx, root, canonical.Path, opened, fingerprint, installed.ContentHash); err != nil {
		return backup, "", err
	}
	links := make([]*publishedProjection, 0, len(installed.Projections)-1)
	defer func() {
		for _, link := range links {
			resultErr = errors.Join(resultErr, link.link.Close(), link.root.Close())
		}
	}()
	for _, projection := range installed.Projections {
		if projection.Kind != "symlink" {
			continue
		}
		parent, err := os.OpenRoot(filepath.Dir(projection.Path))
		if err != nil {
			return backup, "", err
		}
		link, err := openProjectionLink(parent, filepath.Base(projection.Path))
		if err != nil {
			_ = parent.Close()
			return backup, "", err
		}
		item := &publishedProjection{root: parent, link: link}
		links = append(links, item)
		item.parentInfo, err = parent.Stat(".")
		if err != nil {
			return backup, "", err
		}
		item.linkInfo, err = link.Stat()
		if err != nil {
			return backup, "", err
		}
		identity, err := objectid.FromLinkInfo(item.linkInfo)
		if err != nil || identity != projection.Identity {
			return backup, "", fmt.Errorf("projection identity differs from installation")
		}
		if err := item.verify(projection.Path, canonical.Path); err != nil {
			return backup, "", err
		}
	}
	backup, err = session.backupLocalLocked(ctx, slug)
	if err != nil {
		return backup, "", err
	}
	if backup.SourceIdentity != canonical.Identity || backup.InstallationGeneration != installed.Generation || backup.InstallationHash != installed.ContentHash || backup.ContentHash != fingerprint.ContentHash || backup.ModeHash != fingerprint.ModeHash || backup.RootMode != fingerprint.RootMode {
		return backup, "", fmt.Errorf("backup %s completed but does not match the inspected source", backup.ID)
	}
	if err := verifyUninstallBackup(ctx, backup); err != nil {
		return backup, "", fmt.Errorf("backup %s completed but verification failed: %w", backup.ID, err)
	}
	if err := verifyUninstallCanonical(ctx, root, canonical.Path, opened, fingerprint, installed.ContentHash); err != nil {
		return backup, "", fmt.Errorf("backup %s completed but source changed: %w", backup.ID, err)
	}
	linkIndex := 0
	for _, projection := range installed.Projections {
		if projection.Kind != "symlink" {
			continue
		}
		if err := links[linkIndex].verify(projection.Path, canonical.Path); err != nil {
			return backup, "", fmt.Errorf("backup %s completed but projection changed: %w", backup.ID, err)
		}
		linkIndex++
	}
	identifier := uuid.New().String()
	plan := state.OperationPlan{ID: identifier, Action: "uninstall", Slug: slug, ContentHash: backup.ContentHash, Backup: state.BackupPlan{BackupID: backup.ID, Format: backup.Format, ModeHash: backup.ModeHash, RootMode: backup.RootMode, SourceIdentity: backup.SourceIdentity, InstallationGeneration: backup.InstallationGeneration, InstallationHash: backup.InstallationHash}}
	for index, projection := range installed.Projections {
		plan.Targets = append(plan.Targets, state.OperationTarget{Path: projection.Path, Kind: projection.Kind, ExpectedIdentity: projection.Identity, StageName: fmt.Sprintf("%s%s-%d", stagename.Prefix, identifier, index)})
	}
	plan.Targets = append(plan.Targets, state.OperationTarget{Path: backup.Path, Kind: "backup_source", ExpectedIdentity: backup.Identity})
	if err := session.store.BeginOperation(ctx, plan); err != nil {
		return backup, identifier, fmt.Errorf("backup %s completed; uninstall plan %s may need reconciliation: %w", backup.ID, identifier, err)
	}
	session.startedWork = true
	session.plan = identifier
	return backup, identifier, nil
}

func verifyUninstallBackup(ctx context.Context, backup state.Backup) (resultErr error) {
	entry, err := os.Lstat(backup.Path)
	if err != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup snapshot is unavailable or changed")
	}
	root, err := os.OpenRoot(backup.Path)
	if err != nil {
		return fmt.Errorf("open backup snapshot: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	opened, err := root.Stat(".")
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
	if err := validateTrustedRoot(ctx, root, true); err != nil {
		return err
	}
	fingerprint, err := skill.BackupFingerprintRootContext(ctx, root)
	if err != nil {
		return err
	}
	if fingerprint.ContentHash != backup.ContentHash || fingerprint.ModeHash != backup.ModeHash {
		return fmt.Errorf("backup snapshot differs from record")
	}
	return verifyBackupSource(root, backup.Path, opened, fingerprint.RootMode)
}

func verifyUninstallCanonical(ctx context.Context, root *os.Root, path string, expected os.FileInfo, fingerprint skill.BackupFingerprint, installHash string) error {
	if err := verifyBackupSource(root, path, expected, fingerprint.RootMode); err != nil {
		return err
	}
	current, err := skill.BackupFingerprintRootContext(ctx, root)
	if err != nil {
		return err
	}
	if current != fingerprint {
		return fmt.Errorf("canonical source changed")
	}
	hash, err := skill.ContentHashIncludingGitContext(ctx, afero.NewOsFs(), path)
	if err != nil {
		return err
	}
	if hash != installHash {
		return fmt.Errorf("canonical content differs from installation")
	}
	return nil
}
