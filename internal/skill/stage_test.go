package skill_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/skill"
	"github.com/spf13/afero"
)

func TestPublishedBackupRestoresRootMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions")
	}
	for _, mode := range []os.FileMode{0, 0o500, 0o755} {
		t.Run(mode.String(), func(t *testing.T) {
			stage, fingerprint, parent := publishedBackupFixture(t)
			fingerprint.RootMode = int(mode)
			if err := stage.SetPublishedBackupRootMode(t.Context(), "restored", fingerprint); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(parent, "restored"))
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("restored mode = %v, error = %v", info, err)
			}
			if err := os.Chmod(filepath.Join(parent, "restored"), 0o700); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(parent, "restored", ".git", "config"))
			if err != nil || string(content) != "data" {
				t.Fatalf("restored data = %q, error = %v", content, err)
			}
		})
	}
}

func TestPublishedBackupRootModeRejectsChanges(t *testing.T) {
	for _, change := range []string{"content", "replacement", "invalid-mode", "canceled"} {
		t.Run(change, func(t *testing.T) {
			stage, fingerprint, parent := publishedBackupFixture(t)
			destination := filepath.Join(parent, "restored")
			ctx := t.Context()
			switch change {
			case "content":
				if err := os.WriteFile(filepath.Join(destination, ".git", "config"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := os.Rename(destination, destination+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			case "invalid-mode":
				fingerprint.RootMode = 0o1000
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := stage.SetPublishedBackupRootMode(ctx, "restored", fingerprint); err == nil {
				t.Fatal("accepted changed backup or invalid request")
			}
			info, err := os.Stat(destination)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("destination mode = %v, error = %v", info, err)
			}
		})
	}
}

func publishedBackupFixture(t *testing.T) (*skill.StagedSnapshot, skill.BackupFingerprint, string) {
	t.Helper()
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	fingerprint, err := skill.BackupFingerprintRootContext(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	stage, err := skill.CreateStageAt(parent, ".skillhub-stage-restore")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if err := stage.CopyAllFromRootVerifiedContext(t.Context(), root, fingerprint.ContentHash); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stage.Path(), filepath.Join(parent, "restored")); err != nil {
		t.Fatal(err)
	}
	return stage, fingerprint, parent
}

func TestStageVerifiedCopyHonorsCanceledContext(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-cancel")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := stage.CopyFromVerifiedContext(ctx, source, hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if entries, err := os.ReadDir(stage.Path()); err != nil || len(entries) != 0 {
		t.Fatalf("canceled stage changed: %v, %v", entries, err)
	}
}

func TestStageVerifiedBackupIncludesGit(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(source, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(source, "SKILL.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("local edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-backup")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.CopyAllFromVerifiedContext(t.Context(), source, hash); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(stage.Path(), ".git", "config"))
	if err != nil || string(content) != "local edits" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
	info, err := os.Stat(filepath.Join(stage.Path(), "SKILL.md"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("info = %v, error = %v", info, err)
	}
	gitInfo, err := os.Stat(filepath.Join(stage.Path(), ".git"))
	if err != nil || gitInfo.Mode().Perm() != 0o755 {
		t.Fatalf("git mode = %v, error = %v", gitInfo, err)
	}
	actual, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), stage.Path())
	if err != nil || actual != hash {
		t.Fatalf("hash = %q, error = %v", actual, err)
	}
}

func TestBackupFingerprintIncludesPermissionsAndGit(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, ".git", "config")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := skill.BackupFingerprintRootContext(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := skill.BackupFingerprintRootContext(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if before.ContentHash != after.ContentHash || before.ModeHash == after.ModeHash {
		t.Fatalf("before = %+v, after = %+v", before, after)
	}
}

func TestStageVerifiedBackupRejectsClosedSourceAndCancellation(t *testing.T) {
	source := t.TempDir()
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-closed-source")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	hash := strings.Repeat("a", 64)
	if err := stage.CopyAllFromRootVerifiedContext(t.Context(), root, hash); err == nil {
		t.Fatal("accepted closed source")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := stage.CopyAllFromRootVerifiedContext(ctx, root, hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want canceled", err)
	}
	entries, err := os.ReadDir(stage.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage = %v, error = %v", entries, err)
	}
}

func TestStageVerifiedBackupRejectsChangedGit(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, ".git", "config")
	if err := os.WriteFile(file, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-backup-changed")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.CopyAllFromVerifiedContext(t.Context(), source, hash); err == nil {
		t.Fatal("accepted changed backup source")
	}
	entries, err := os.ReadDir(stage.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage = %v, error = %v", entries, err)
	}
}

func TestStageVerifiedBackupRejectsSpecialFiles(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(source, ".git", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), source); err == nil {
		t.Fatal("accepted symlink in backup source")
	}
}

func TestStageVerifiedBackupCopiesOpenedSourceNotReplacement(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(source, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(source, source+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-open-source")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.CopyAllFromRootVerifiedContext(t.Context(), root, hash); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(stage.Path(), "SKILL.md"))
	if err != nil || string(content) != "original" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
	info, err := os.Stat(filepath.Join(stage.Path(), "assets"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("info = %v, error = %v", info, err)
	}
}

func TestStageVerifiedBackupRejectsStageInsideOpenedSource(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(source, ".skillhub-stage-nested-backup")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	hash, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.CopyAllFromRootVerifiedContext(t.Context(), root, hash); err == nil {
		t.Fatal("copied a source containing the destination")
	}
	entries, err := os.ReadDir(stage.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage = %v, error = %v", entries, err)
	}
	after, err := skill.ContentHashIncludingGitContext(t.Context(), afero.NewOsFs(), source)
	if err != nil || after != hash {
		t.Fatalf("source hash = %q, expected %q, error = %v", after, hash, err)
	}
}

func TestStageVerifiedCopyResolvesDotDotAfterSymlink(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"base/data", "other/sub", "other/data"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "base", "data", "SKILL.md"), []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other", "data", "SKILL.md"), []byte("correct"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "other", "sub"), filepath.Join(root, "base", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := filepath.Join(root, "base", "link") + "/../data"
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-source")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.CopyFromVerified(source, hash); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(stage.Path(), "SKILL.md"))
	if err != nil || string(content) != "correct" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
}

func TestStageSnapshotResolvesDotDotAfterSymlink(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"base/data", "other/sub", "other/data"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "base", "data", "SKILL.md"), []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other", "data", "SKILL.md"), []byte("correct"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "other", "sub"), filepath.Join(root, "base", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := filepath.Join(root, "base", "link") + "/../data"
	stage, err := skill.StageSnapshot(afero.NewOsFs(), source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if err != nil || string(content) != "correct" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
}

func TestCreateStageResolvesParentDotDotAfterSymlink(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"base/data", "other/sub", "other/data"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "other", "sub"), filepath.Join(root, "base", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	parent := filepath.Join(root, "base", "link") + "/../data"
	stage, err := skill.CreateStageAt(parent, ".skillhub-stage-parent")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if stage.Path() != filepath.Join(root, "other", "data", ".skillhub-stage-parent") {
		t.Fatalf("unexpected stage path: %q", stage.Path())
	}
}

func TestStageSnapshotPreservesPartialDirectoryAfterFailure(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "a"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "z")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	stage, err := skill.StageSnapshot(afero.NewOsFs(), source, parent)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(stage) != resolvedParent {
		t.Fatalf("expected the partial stage path, got %q", stage)
	}
	if content, err := os.ReadFile(filepath.Join(stage, "a")); err != nil || string(content) != "partial" {
		t.Fatalf("partial stage not retained: %q, %v", content, err)
	}
}

func TestStageSnapshotPreservesUnrelatedFiles(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(parent, "keep")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.StageSnapshot(afero.NewOsFs(), source, parent)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(stage, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("bad snapshot: %q, %v", content, err)
	}
	if content, err := os.ReadFile(unrelated); err != nil || string(content) != "keep" {
		t.Fatalf("unrelated file changed: %q, %v", content, err)
	}
}

func TestStageSnapshotReturnsResolvedParentPath(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	stage, err := skill.StageSnapshot(afero.NewOsFs(), source, alias)
	if err != nil {
		t.Fatal(err)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(stage) != resolvedParent {
		t.Fatalf("stage path %q does not use resolved parent %q", stage, resolvedParent)
	}
}

func TestStageSnapshotAtUsesPreselectedNameAndDoesNotOverwrite(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := ".skillhub-stage-known"
	stage, err := skill.StageSnapshotAt(afero.NewOsFs(), source, parent, name)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(stage) != name {
		t.Fatalf("got stage %q, want name %q", stage, name)
	}
	if _, err := skill.StageSnapshotAt(afero.NewOsFs(), source, parent, name); err == nil {
		t.Fatal("expected existing stage to be rejected")
	}
	if content, err := os.ReadFile(filepath.Join(stage, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("existing stage was modified: %q, %v", content, err)
	}
}

func TestStageSnapshotAtRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\name`, ".skillhub-stage-\x00bad", "other"} {
		if _, err := skill.StageSnapshotAt(afero.NewOsFs(), t.TempDir(), t.TempDir(), name); err == nil {
			t.Errorf("accepted stage name %q", name)
		}
	}
}

func TestStageSnapshotAtWorksWithMemoryFilesystem(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	root := filepath.Join(os.TempDir(), "skillhub-stage-test")
	source, parent := filepath.Join(root, "source"), filepath.Join(root, "parent")
	for _, directory := range []string{source, parent} {
		if err := filesystem.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := afero.WriteFile(filesystem, filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.StageSnapshotAt(filesystem, source, parent, ".skillhub-stage-memory")
	if err != nil {
		t.Fatal(err)
	}
	if content, err := afero.ReadFile(filesystem, filepath.Join(stage, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("bad snapshot: %q, %v", content, err)
	}
	if _, err := skill.StageSnapshotAt(filesystem, source, parent, ".skillhub-stage-memory"); err == nil {
		t.Fatal("expected existing stage to be rejected")
	}
}

func TestStageSnapshotAtMemoryRequiresExistingDirectoryParent(t *testing.T) {
	for _, parentKind := range []string{"missing", "file"} {
		t.Run(parentKind, func(t *testing.T) {
			filesystem := afero.NewMemMapFs()
			root := filepath.Join(os.TempDir(), "skillhub-stage-parent-test")
			source, parent := filepath.Join(root, "source"), filepath.Join(root, "parent")
			if err := filesystem.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if parentKind == "file" {
				if err := afero.WriteFile(filesystem, parent, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stage, err := skill.StageSnapshotAt(filesystem, source, parent, ".skillhub-stage-memory")
			if err == nil || stage != "" {
				t.Fatalf("got stage %q and error %v; expected rejection", stage, err)
			}
			if parentKind == "missing" {
				if _, err := filesystem.Stat(parent); !os.IsNotExist(err) {
					t.Fatalf("missing parent was created or inaccessible: %v", err)
				}
			} else if content, err := afero.ReadFile(filesystem, parent); err != nil || string(content) != "keep" {
				t.Fatalf("parent file changed: %q, %v", content, err)
			}
		})
	}
}

func TestStageCanBeRecordedBeforeCopy(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(parent, ".skillhub-stage-separate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	info, err := stage.Info()
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.Lstat(stage.Path())
	if err != nil || !os.SameFile(info, onDisk) {
		t.Fatalf("stage identity not available before copy: %v", err)
	}
	entries, err := os.ReadDir(stage.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage before copy = %v, error = %v", entries, err)
	}
	if err := stage.CopyFrom(source); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(stage.Path(), "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("snapshot content = %q, error = %v", content, err)
	}
	if err := stage.CopyFrom(source); err == nil {
		t.Fatal("copy into a nonempty stage succeeded")
	}
}

func TestStageRejectsReplacedDirectory(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(parent, ".skillhub-stage-replaced")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if err := os.Rename(stage.Path(), filepath.Join(parent, "moved")); err != nil {
		t.Skipf("moving an open directory is unavailable: %v", err)
	}
	if err := os.Mkdir(stage.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stage.CopyFrom(source); err == nil {
		t.Fatal("copied after stage was replaced")
	}
	for _, directory := range []string{stage.Path(), filepath.Join(parent, "moved")} {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatalf("directory %s changed: %v, %v", directory, entries, err)
		}
	}
}

func TestStageRejectsReplacedParent(t *testing.T) {
	base, source := t.TempDir(), t.TempDir()
	parent := filepath.Join(base, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(parent, ".skillhub-stage-parent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if err := os.Rename(parent, filepath.Join(base, "moved")); err != nil {
		t.Skipf("moving an open directory is unavailable: %v", err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stage.CopyFrom(source); err == nil {
		t.Fatal("copied after stage parent was replaced")
	}
	entries, err := os.ReadDir(filepath.Join(base, "moved", ".skillhub-stage-parent"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("moved stage changed: %v, %v", entries, err)
	}
}

func TestStageRejectsNestedSourceAndClosedHandle(t *testing.T) {
	source := t.TempDir()
	stage, err := skill.CreateStageAt(source, ".skillhub-stage-nested")
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.CopyFrom(source); err == nil {
		t.Fatal("accepted stage nested in source")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Info(); err == nil {
		t.Fatal("closed stage returned identity")
	}
	if err := stage.CopyFrom(source); err == nil {
		t.Fatal("copied into closed stage")
	}
}

func TestCreateStageAtRejectsExistingAndInvalidNames(t *testing.T) {
	parent := t.TempDir()
	existing := filepath.Join(parent, ".skillhub-stage-existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".skillhub-stage-existing", "../escape", "other", ""} {
		stage, err := skill.CreateStageAt(parent, name)
		if err == nil {
			_ = stage.Close()
			t.Fatalf("accepted name %q", name)
		}
	}
	if content, err := os.ReadFile(filepath.Join(existing, "keep")); err != nil || string(content) != "keep" {
		t.Fatalf("existing stage changed: %q, %v", content, err)
	}
}

func TestStageVerifiedCopyChecksPlannedContent(t *testing.T) {
	source := t.TempDir()
	file := filepath.Join(source, "SKILL.md")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-verified")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if err := stage.CopyFromVerified(source, expected); err != nil {
		t.Fatalf("matching snapshot rejected: %v", err)
	}
	if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedStage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-changed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = changedStage.Close() })
	if err := changedStage.CopyFromVerified(source, expected); err == nil {
		t.Fatal("accepted snapshot different from planned content")
	}
	if entries, err := os.ReadDir(changedStage.Path()); err != nil || len(entries) != 0 {
		t.Fatalf("unexpected source was copied: %v, %v", entries, err)
	}
}

func TestStageVerifiedCopyRejectsInvalidHashBeforeCopy(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, err := skill.CreateStageAt(t.TempDir(), ".skillhub-stage-invalid-hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	for _, expected := range []string{"", "not-a-hash", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if err := stage.CopyFromVerified(source, expected); err == nil {
			t.Fatalf("accepted invalid hash %q", expected)
		}
	}
	entries, err := os.ReadDir(stage.Path())
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage changed for invalid hash: %v, %v", entries, err)
	}
}
