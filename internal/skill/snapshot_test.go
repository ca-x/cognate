package skill

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"
)

type cancelAfterRead struct {
	cancel context.CancelFunc
}

func (reader cancelAfterRead) Read(buffer []byte) (int, error) {
	buffer[0] = 'x'
	reader.cancel()
	return 1, nil
}

func TestContextReaderStopsBetweenReads(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	count, err := io.Copy(io.Discard, contextReader{ctx: ctx, reader: cancelAfterRead{cancel: cancel}})
	if count != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("copied %d bytes, error = %v", count, err)
	}
}

func TestCanceledCopyDoesNotWaitForStageLock(t *testing.T) {
	stage := &StagedSnapshot{}
	stage.mutex.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := make(chan error, 1)
	go func() { result <- stage.CopyFromVerifiedContext(ctx, "source", strings.Repeat("a", 64)) }()
	select {
	case err := <-result:
		stage.mutex.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		stage.mutex.Unlock()
		t.Fatal("canceled call waited for stage lock")
	}
}

type cancelingFilesystem struct {
	afero.Fs
	path   string
	cancel context.CancelFunc
}

func (filesystem cancelingFilesystem) Open(name string) (afero.File, error) {
	file, err := filesystem.Fs.Open(name)
	if err != nil || name != filesystem.path {
		return file, err
	}
	return cancelingFile{File: file, cancel: filesystem.cancel}, nil
}

type cancelingFile struct {
	afero.File
	cancel context.CancelFunc
}

func (file cancelingFile) Read(buffer []byte) (int, error) {
	count, err := file.File.Read(buffer)
	file.cancel()
	return count, err
}

func TestSnapshotCancellationRetainsPartialStage(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	for _, path := range []string{"/source", "/stage"} {
		if err := filesystem.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := afero.WriteFile(filesystem, "/source/SKILL.md", []byte(strings.Repeat("x", 100000)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := cancelingFilesystem{Fs: filesystem, path: "/source/SKILL.md", cancel: cancel}
	if err := copySnapshotContents(ctx, source, "/source", filesystem, "/stage", nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	info, err := filesystem.Stat("/stage/SKILL.md")
	if err != nil || info.Size() <= 0 || info.Size() >= 100000 {
		t.Fatalf("partial file = %v, error = %v", info, err)
	}
}

func TestHashCancellationStopsReading(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/source", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/source/SKILL.md", []byte(strings.Repeat("x", 100000)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := cancelingFilesystem{Fs: filesystem, path: "/source/SKILL.md", cancel: cancel}
	if hash, err := contentHash(ctx, source, "/source"); !errors.Is(err, context.Canceled) || hash != "" {
		t.Fatalf("hash = %q, error = %v", hash, err)
	}
}

type statOnlyFilesystem struct{ afero.Fs }

func (filesystem statOnlyFilesystem) LstatIfPossible(path string) (os.FileInfo, bool, error) {
	info, err := filesystem.Stat(path)
	return info, false, err
}

func TestCopySnapshotPreservesContentWithoutGitMetadata(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	for _, directory := range []string{"/source/.git", "/source/.hidden", "/snapshot"} {
		if err := filesystem.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		"/source/SKILL.md":         "skill",
		"/source/.hidden/data.bin": "\x00\x01\xff",
		"/source/.git/config":      "private git metadata",
	} {
		if err := afero.WriteFile(filesystem, path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := copySnapshotTo(filesystem, "/source", "/snapshot", nil); err != nil {
		t.Fatal(err)
	}
	first, err := ContentHash(filesystem, "/source")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ContentHash(filesystem, "/snapshot")
	if err != nil || first != second {
		t.Fatalf("snapshot hash %q, want %q: %v", second, first, err)
	}
	if exists, err := afero.Exists(filesystem, "/snapshot/.git"); err != nil || exists {
		t.Fatalf("snapshot contains Git metadata: %v, %v", exists, err)
	}
}

func TestCopySnapshotRejectsSymlinkDestination(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "destination")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copySnapshotTo(afero.NewOsFs(), source, link, nil); err == nil {
		t.Fatal("expected symlink destination to be rejected")
	}
}

func TestCopySnapshotRejectsNonemptyDestination(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	for _, directory := range []string{"/source", "/snapshot"} {
		if err := filesystem.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := afero.WriteFile(filesystem, "/snapshot/existing", []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copySnapshotTo(filesystem, "/source", "/snapshot", nil); err == nil {
		t.Fatal("expected destination conflict")
	}
	content, err := afero.ReadFile(filesystem, "/snapshot/existing")
	if err != nil || string(content) != "keep" {
		t.Fatalf("existing content changed: %q, %v", content, err)
	}
}

func TestCopySnapshotRejectsDestinationInsideSource(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/source/destination", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copySnapshotTo(filesystem, "/source", "/source/destination", nil); err == nil {
		t.Fatal("expected nested destination rejection")
	}
}

func TestCopySnapshotRejectsSourceSymlinks(t *testing.T) {
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "external")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copySnapshotTo(afero.NewOsFs(), source, t.TempDir(), nil); err == nil {
		t.Fatal("expected source symlink rejection")
	}
}

func TestCopySnapshotLeavesPartialFilesForStageOwner(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "a"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "z")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copySnapshotTo(afero.NewOsFs(), source, destination, nil); err == nil {
		t.Fatal("expected symlink rejection")
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 || entries[0].Name() != "a" {
		t.Fatalf("stage owner cannot identify partial snapshot: %v, %v", entries, err)
	}
}

func TestCopySnapshotRejectsAliasedDestinationInsideSource(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copySnapshotTo(afero.NewOsFs(), source, filepath.Join(alias, "child"), nil); err == nil {
		t.Fatal("expected aliased destination rejection")
	}
	entries, err := os.ReadDir(filepath.Join(source, "child"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v, %v", entries, err)
	}
}

func TestCopySnapshotRejectsDestinationInsideResolvedSource(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"base/source", "actual/sub", "actual/source/output"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "actual", "sub"), filepath.Join(root, "base", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "actual", "source", "a-link")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "base", "link") + "/../source"
	destination := filepath.Join(root, "actual", "source", "output")
	err := copySnapshotTo(afero.NewOsFs(), source, destination, nil)
	if err == nil || !strings.Contains(err.Error(), "destination cannot be inside source") {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v, %v", entries, err)
	}
}

func TestCopySnapshotRejectsAliasInsideSkippedGitDirectory(t *testing.T) {
	source := t.TempDir()
	child := filepath.Join(source, ".git", "cache")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copySnapshotTo(afero.NewOsFs(), source, filepath.Join(alias, ".git", "cache"), nil); err == nil {
		t.Fatal("expected aliased destination rejection")
	}
	if entries, err := os.ReadDir(child); err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v, %v", entries, err)
	}
}

func TestCopySnapshotRejectsUntrustedLstatFallback(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(destination, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	filesystem := statOnlyFilesystem{Fs: afero.NewOsFs()}
	if err := copySnapshotTo(filesystem, source, alias, nil); err == nil {
		t.Fatal("expected untrusted filesystem rejection")
	}
	if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
		t.Fatalf("destination changed: %v, %v", entries, err)
	}
}

func TestCopySnapshotCopiesReadOnlySourceDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions are not available")
	}
	source, destination := t.TempDir(), t.TempDir()
	child := filepath.Join(source, "readonly")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(child, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(child, 0o700) })
	if err := copySnapshotTo(afero.NewOsFs(), source, destination, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "readonly", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestCopySnapshotPreservesExecutableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions are not available")
	}
	source, destination := t.TempDir(), t.TempDir()
	path := filepath.Join(source, "script")
	if err := os.WriteFile(path, []byte("script"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copySnapshotTo(afero.NewOsFs(), source, destination, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destination, "script"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v, %v", info, err)
	}
	first, err := ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ContentHash(afero.NewOsFs(), destination)
	if err != nil || first != second {
		t.Fatalf("snapshot hash %q, want %q: %v", second, first, err)
	}
}

func TestCopySnapshotPreservesModeWithRestrictiveUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions are not available")
	}
	if os.Getenv("SKILLHUB_UMASK_TEST") == "1" {
		source, destination := os.Getenv("SKILLHUB_SOURCE"), os.Getenv("SKILLHUB_DESTINATION")
		if err := copySnapshotTo(afero.NewOsFs(), source, destination, nil); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(destination, "script"))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("executable mode not preserved: %v, %v", info, err)
		}
		return
	}
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "script"), []byte("script"), 0o700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", `umask 0777; exec "$1" -test.run '^TestCopySnapshotPreservesModeWithRestrictiveUmask$'`, "sh", binary)
	command.Env = append(os.Environ(), "SKILLHUB_UMASK_TEST=1", "SKILLHUB_SOURCE="+source, "SKILLHUB_DESTINATION="+destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restricted umask: %s: %v", output, err)
	}
}
