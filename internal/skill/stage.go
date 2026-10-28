package skill

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ca-x/skillhub/internal/stagename"
	"github.com/spf13/afero"
)

type StagedSnapshot struct {
	mutex  sync.Mutex
	parent *os.Root
	root   *os.Root
	path   string
	name   string
}

func CreateStageAt(parent, name string) (*StagedSnapshot, error) {
	if err := stagename.Validate(name); err != nil {
		return nil, err
	}
	parentPath, err := ResolvePath(parent)
	if err != nil {
		return nil, fmt.Errorf("resolve stage parent: %w", err)
	}
	parentRoot, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open stage parent: %w", err)
	}
	if err := parentRoot.Mkdir(name, 0o700); err != nil {
		_ = parentRoot.Close()
		return nil, fmt.Errorf("create stage: %w", err)
	}
	stagePath := filepath.Join(parentPath, name)
	before, err := parentRoot.Lstat(name)
	if err != nil {
		_ = parentRoot.Close()
		return nil, fmt.Errorf("inspect stage at %s; retained for inspection: %w", stagePath, err)
	}
	if !before.IsDir() {
		_ = parentRoot.Close()
		return nil, fmt.Errorf("stage at %s is not a directory; retained for inspection", stagePath)
	}
	root, err := parentRoot.OpenRoot(name)
	if err != nil {
		_ = parentRoot.Close()
		return nil, fmt.Errorf("open stage at %s; retained for inspection: %w", stagePath, err)
	}
	stage := &StagedSnapshot{parent: parentRoot, root: root, path: stagePath, name: name}
	actual, err := stage.info()
	if err != nil {
		_ = stage.Close()
		return nil, fmt.Errorf("verify stage at %s; retained for inspection: %w", stagePath, err)
	}
	if !os.SameFile(before, actual) {
		_ = stage.Close()
		return nil, fmt.Errorf("stage changed while opening; retained at %s", stagePath)
	}
	return stage, nil
}

func (stage *StagedSnapshot) Path() string { return stage.path }

func (stage *StagedSnapshot) Info() (fs.FileInfo, error) {
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	return stage.info()
}

func (stage *StagedSnapshot) info() (fs.FileInfo, error) {
	if stage.root == nil {
		return nil, fmt.Errorf("stage is closed")
	}
	parentInfo, err := stage.parent.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("inspect open stage parent: %w", err)
	}
	parentEntry, err := os.Stat(filepath.Dir(stage.path))
	if err != nil || !os.SameFile(parentInfo, parentEntry) {
		return nil, fmt.Errorf("stage parent changed or disappeared")
	}
	opened, err := stage.root.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("inspect open stage: %w", err)
	}
	entry, err := stage.parent.Lstat(stage.name)
	if err != nil || !entry.IsDir() || !os.SameFile(opened, entry) {
		return nil, fmt.Errorf("stage entry changed or disappeared")
	}
	return opened, nil
}

func (stage *StagedSnapshot) CopyFrom(source string) error {
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	return stage.copyFrom(context.Background(), source, "", false)
}

func (stage *StagedSnapshot) CopyFromVerified(source, expectedHash string) error {
	return stage.CopyFromVerifiedContext(context.Background(), source, expectedHash)
}

func (stage *StagedSnapshot) CopyFromVerifiedContext(ctx context.Context, source, expectedHash string) error {
	return stage.copyVerified(ctx, source, expectedHash, false)
}

func (stage *StagedSnapshot) CopyAllFromVerifiedContext(ctx context.Context, source, expectedHash string) error {
	return stage.copyVerified(ctx, source, expectedHash, true)
}

func (stage *StagedSnapshot) CopyAllFromRootVerifiedContext(ctx context.Context, source *os.Root, expectedHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || !validExpectedHash(expectedHash) {
		return fmt.Errorf("invalid backup source or expected hash")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceInfo, err := source.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect opened backup source: %w", err)
	}
	if !sourceInfo.IsDir() {
		return fmt.Errorf("opened backup source is not a directory")
	}
	destinationInfo, err := stage.info()
	if err != nil {
		return err
	}
	if os.SameFile(sourceInfo, destinationInfo) {
		return fmt.Errorf("backup source and destination are the same directory")
	}
	sourceFS := rootFS{root: source}
	before, err := contentHashAvoiding(ctx, sourceFS, ".", true, destinationInfo)
	if err != nil {
		return err
	}
	if before != expectedHash {
		return fmt.Errorf("backup source content does not match planned hash")
	}
	beforeModes, err := contentModeHash(ctx, sourceFS, ".")
	if err != nil {
		return err
	}
	copyErr := copySnapshotContents(ctx, sourceFS, ".", rootFS{root: stage.root}, ".", destinationInfo, true)
	_, identityErr := stage.info()
	if copyErr != nil || identityErr != nil {
		return errors.Join(copyErr, identityErr)
	}
	afterInfo, err := source.Stat(".")
	if err != nil || !os.SameFile(sourceInfo, afterInfo) {
		return fmt.Errorf("opened backup source changed or disappeared")
	}
	after, err := contentHashIncludingGit(ctx, sourceFS, ".", true)
	if err != nil {
		return err
	}
	if after != expectedHash {
		return fmt.Errorf("backup source content changed during copying")
	}
	afterModes, err := contentModeHash(ctx, sourceFS, ".")
	if err != nil {
		return err
	}
	if beforeModes != afterModes {
		return fmt.Errorf("backup source permissions changed during copying")
	}
	actual, err := contentHashIncludingGit(ctx, rootFS{root: stage.root}, ".", true)
	if err != nil {
		return err
	}
	if _, err := stage.info(); err != nil {
		return err
	}
	if actual != expectedHash {
		return fmt.Errorf("backup content does not match planned hash")
	}
	actualModes, err := contentModeHash(ctx, rootFS{root: stage.root}, ".")
	if err != nil {
		return err
	}
	if actualModes != beforeModes {
		return fmt.Errorf("backup permissions do not match the source")
	}
	if _, err := stage.info(); err != nil {
		return err
	}
	return ctx.Err()
}

func (stage *StagedSnapshot) VerifyBackup(ctx context.Context, expected BackupFingerprint) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validExpectedHash(expected.ContentHash) || !validExpectedHash(expected.ModeHash) {
		return nil, fmt.Errorf("invalid backup fingerprint")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	before, err := stage.info()
	if err != nil {
		return nil, err
	}
	if err := stage.verifyBackupContent(ctx, expected); err != nil {
		return nil, err
	}
	after, err := stage.info()
	if err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("backup stage changed during verification")
	}
	return after, nil
}

func (stage *StagedSnapshot) VerifyPublishedBackup(ctx context.Context, name string, expected BackupFingerprint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" || name == "." || name == ".." || name == stage.name || strings.ContainsAny(name, `/\`) || !filepath.IsLocal(name) || !validExpectedHash(expected.ContentHash) || !validExpectedHash(expected.ModeHash) {
		return fmt.Errorf("invalid published backup name or fingerprint")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	if err := stage.verifyPublishedEntry(name); err != nil {
		return err
	}
	if err := stage.verifyBackupContent(ctx, expected); err != nil {
		return err
	}
	return stage.verifyPublishedEntry(name)
}

func (stage *StagedSnapshot) SetPublishedBackupRootMode(ctx context.Context, name string, expected BackupFingerprint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" || name == "." || name == ".." || name == stage.name || strings.ContainsAny(name, `/\`) || !filepath.IsLocal(name) || !validExpectedHash(expected.ContentHash) || !validExpectedHash(expected.ModeHash) || expected.RootMode < 0 || expected.RootMode > 0o777 {
		return fmt.Errorf("invalid published backup name or fingerprint")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	if err := stage.verifyPublishedEntry(name); err != nil {
		return err
	}
	if err := stage.verifyBackupContent(ctx, expected); err != nil {
		return err
	}
	if err := stage.verifyPublishedEntry(name); err != nil {
		return err
	}
	directory, err := stage.root.Open(".")
	if err != nil {
		return fmt.Errorf("open published backup directory: %w", err)
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect published backup directory: %w", err)
	}
	entry, err := stage.parent.Lstat(name)
	if err != nil || !entry.IsDir() || !os.SameFile(opened, entry) {
		return fmt.Errorf("published backup changed before restoring permissions")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := directory.Chmod(os.FileMode(expected.RootMode)); err != nil {
		return fmt.Errorf("restore backup root permissions: %w", err)
	}
	after, err := directory.Stat()
	if err != nil || !os.SameFile(opened, after) || int(after.Mode().Perm()) != expected.RootMode {
		return fmt.Errorf("restored backup root permissions changed")
	}
	entry, err = stage.parent.Lstat(name)
	if err != nil || !entry.IsDir() || !os.SameFile(after, entry) {
		return fmt.Errorf("published backup changed after restoring permissions")
	}
	parent, err := stage.parent.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect published backup parent: %w", err)
	}
	parentEntry, err := os.Stat(filepath.Dir(stage.path))
	if err != nil || !os.SameFile(parent, parentEntry) {
		return fmt.Errorf("published backup parent changed")
	}
	if _, err := stage.parent.Lstat(stage.name); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stage path exists after publication")
	}
	return ctx.Err()
}

func (stage *StagedSnapshot) verifyBackupContent(ctx context.Context, expected BackupFingerprint) error {
	content, err := contentHashIncludingGit(ctx, rootFS{root: stage.root}, ".", true)
	if err != nil {
		return err
	}
	modes, err := contentModeHash(ctx, rootFS{root: stage.root}, ".")
	if err != nil {
		return err
	}
	if content != expected.ContentHash || modes != expected.ModeHash {
		return fmt.Errorf("backup content or permissions changed")
	}
	return ctx.Err()
}

func (stage *StagedSnapshot) copyVerified(ctx context.Context, source, expectedHash string, includeGit bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validExpectedHash(expectedHash) {
		return fmt.Errorf("invalid expected content hash")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stage.copyFrom(ctx, source, expectedHash, includeGit); err != nil {
		return err
	}
	actualHash, err := contentHashIncludingGit(ctx, rootFS{root: stage.root}, ".", true)
	_, identityErr := stage.info()
	if err != nil || identityErr != nil {
		return errors.Join(err, identityErr)
	}
	if actualHash != expectedHash {
		return fmt.Errorf("snapshot content does not match planned hash")
	}
	return ctx.Err()
}

func (stage *StagedSnapshot) VerifyHash(ctx context.Context, expectedHash string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validExpectedHash(expectedHash) {
		return nil, fmt.Errorf("invalid expected content hash")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	before, err := stage.info()
	if err != nil {
		return nil, err
	}
	actual, err := contentHashIncludingGit(ctx, rootFS{root: stage.root}, ".", true)
	if err != nil {
		return nil, err
	}
	after, err := stage.info()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || actual != expectedHash {
		return nil, fmt.Errorf("stage identity or content changed")
	}
	return after, nil
}

func validExpectedHash(hash string) bool {
	if len(hash) != sha256.Size*2 || hash != strings.ToLower(hash) {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func (stage *StagedSnapshot) VerifyPublished(ctx context.Context, name, expectedHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" || name == "." || name == ".." || name == stage.name || strings.ContainsAny(name, `/\`) || !filepath.IsLocal(name) {
		return fmt.Errorf("invalid published directory name")
	}
	if !validExpectedHash(expectedHash) {
		return fmt.Errorf("invalid expected content hash")
	}
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	if err := stage.verifyPublishedEntry(name); err != nil {
		return err
	}
	actual, err := contentHashIncludingGit(ctx, rootFS{root: stage.root}, ".", true)
	if err != nil {
		return err
	}
	if actual != expectedHash {
		return fmt.Errorf("published content does not match planned hash")
	}
	return stage.verifyPublishedEntry(name)
}

func (stage *StagedSnapshot) verifyPublishedEntry(name string) error {
	if stage.root == nil {
		return fmt.Errorf("stage is closed")
	}
	parentInfo, err := stage.parent.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect stage parent: %w", err)
	}
	parentEntry, err := os.Stat(filepath.Dir(stage.path))
	if err != nil || !os.SameFile(parentInfo, parentEntry) {
		return fmt.Errorf("stage parent changed or disappeared")
	}
	opened, err := stage.root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect published stage: %w", err)
	}
	published, err := stage.parent.Lstat(name)
	if err != nil || !published.IsDir() || !os.SameFile(opened, published) {
		return fmt.Errorf("published entry does not match stage")
	}
	if _, err := stage.parent.Lstat(stage.name); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stage path still exists after publication")
	}
	return nil
}

func (stage *StagedSnapshot) copyFrom(ctx context.Context, source, expectedHash string, includeGit bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destinationInfo, err := stage.info()
	if err != nil {
		return err
	}
	sourcePath, err := ResolvePath(source)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	relative, err := filepath.Rel(sourcePath, stage.path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("destination cannot be inside source")
	}
	sourceFS, sourceRoot, closeSource, err := confinedSource(afero.NewOsFs(), sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer closeSource()
	if expectedHash != "" {
		sourceHash, err := contentHashIncludingGit(ctx, sourceFS, sourceRoot, includeGit)
		if err != nil {
			return err
		}
		if sourceHash != expectedHash {
			return fmt.Errorf("source content does not match planned hash")
		}
		if _, err := stage.info(); err != nil {
			return err
		}
	}
	copyErr := copySnapshotContents(ctx, sourceFS, sourceRoot, rootFS{root: stage.root}, ".", destinationInfo, includeGit)
	_, identityErr := stage.info()
	return errors.Join(copyErr, identityErr)
}

func (stage *StagedSnapshot) Close() error {
	stage.mutex.Lock()
	defer stage.mutex.Unlock()
	if stage.root == nil {
		return nil
	}
	err := errors.Join(stage.root.Close(), stage.parent.Close())
	stage.root, stage.parent = nil, nil
	return err
}

func StageSnapshot(filesystem afero.Fs, source, parent string) (string, error) {
	return StageSnapshotAt(filesystem, source, parent, stagename.Prefix+rand.Text())
}

func StageSnapshotAt(filesystem afero.Fs, source, parent, name string) (string, error) {
	if err := stagename.Validate(name); err != nil {
		return "", err
	}
	var sourcePath, parentPath string
	var err error
	switch filesystem.(type) {
	case *afero.OsFs:
		sourcePath, err = ResolvePath(source)
		if err != nil {
			return "", fmt.Errorf("resolve source: %w", err)
		}
		parentPath, err = ResolvePath(parent)
		if err != nil {
			return "", fmt.Errorf("resolve stage parent: %w", err)
		}
	case *afero.MemMapFs:
		sourcePath, err = filepath.Abs(source)
		if err != nil {
			return "", err
		}
		parentPath, err = filepath.Abs(parent)
		if err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("filesystem does not support secure snapshot access")
	}
	relative, err := filepath.Rel(sourcePath, parentPath)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("stage parent cannot be inside source")
	}
	if _, ok := filesystem.(*afero.MemMapFs); ok {
		info, err := filesystem.Stat(parentPath)
		if err != nil {
			return "", fmt.Errorf("inspect stage parent: %w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("stage parent is not a directory: %s", parentPath)
		}
		stage := filepath.Join(parentPath, name)
		if err := filesystem.Mkdir(stage, 0o700); err != nil {
			return "", fmt.Errorf("create stage: %w", err)
		}
		if err := copySnapshotTo(filesystem, sourcePath, stage, nil); err != nil {
			return stage, fmt.Errorf("snapshot incomplete; stage retained for inspection: %w", err)
		}
		return stage, nil
	}
	return stageOSSnapshot(filesystem, sourcePath, parentPath, name)
}

func stageOSSnapshot(filesystem afero.Fs, source, parent, name string) (string, error) {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", fmt.Errorf("open stage parent: %w", err)
	}
	defer root.Close()
	if err := root.Mkdir(name, 0o700); err != nil {
		return "", fmt.Errorf("create stage: %w", err)
	}
	stage := filepath.Join(parent, name)
	info, err := root.Lstat(name)
	if err != nil {
		return stage, fmt.Errorf("inspect stage; stage retained for inspection: %w", err)
	}
	if err := copySnapshotTo(filesystem, source, stage, info); err != nil {
		return stage, fmt.Errorf("snapshot incomplete; stage retained for inspection: %w", err)
	}
	return stage, nil
}
