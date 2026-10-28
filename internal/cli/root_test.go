package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/cli"
	"github.com/ca-x/cognate/internal/state"
)

func TestClientDoesNotExecuteImportPlugins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"import-preview", "import"} {
		command := cli.New()
		command.SetArgs([]string{name, "cc-switch"})
		if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("client exposed local plugin command %q: %v", name, err)
		}
	}
}

func TestScanCommand(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "skills", "demo", "SKILL.md"), []byte("---\nname: Demo\ndescription: Sample\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"scan", directory})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Demo", "Sample", "skills/demo"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output %q does not include %q", output.String(), want)
		}
	}
}

func TestInstallStatusReconcilesInterruptedOperations(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "operation", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(home, "demo"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"install-status"}, {"install-status", "--reconcile"}, {"install-status", "--reconcile"}} {
		var output bytes.Buffer
		command := cli.New()
		command.SetOut(&output)
		command.SetArgs(args)
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "operation") || !strings.Contains(output.String(), filepath.Join(home, "demo")) {
			t.Fatalf("missing details: %q", output.String())
		}
	}
	store, err = state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "needs_attention" {
		t.Fatalf("entry = %+v, error = %v", entry, err)
	}
}

func TestInstallStatusCancelsOnlyUnstartedPlans(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "planned", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(home, "demo"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-status", "--reconcile"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "cancelled" {
		t.Fatalf("entry = %+v, error = %v", entry, err)
	}
	plan.ID = "another"
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatalf("claims were not released: %v", err)
	}
}

func TestInstallLocalCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "skill" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
	output.Reset()
	command = cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "已匹配") {
		t.Fatalf("no-op not identified: %q", output.String())
	}
}

func TestBackupLocalCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.Mkdir(filepath.Join(canonical, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, ".git", "config"), []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	command = cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"backup-local", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(home, ".local", "share", "skillhub", "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("entries = %v, error = %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(backupDir, entries[0].Name(), ".git", "config"))
	if err != nil || string(data) != "edited" {
		t.Fatalf("data = %q, error = %v", data, err)
	}
	if !strings.Contains(output.String(), entries[0].Name()) {
		t.Fatalf("backup id missing from output: %q", output.String())
	}
	command = cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"backup-local", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(backupDir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, error = %v", entries, err)
	}
	output.Reset()
	command = cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"backup-list"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{entries[0].Name(), "demo", filepath.Join(backupDir, entries[0].Name())} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("backup list %q does not include %q", output.String(), want)
		}
	}
	if !strings.Contains(output.String(), entries[1].Name()) {
		t.Fatalf("second backup missing: %q", output.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := &cancelingWriter{cancel: cancel}
	command = cli.New()
	command.SetOut(writer)
	command.SetArgs([]string{"backup-list"})
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if writer.writes != 1 {
		t.Fatalf("writes after cancellation: %d", writer.writes)
	}
	parent := t.TempDir()
	output.Reset()
	command = cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"restore-local", entries[0].Name(), parent, "copy"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(parent, "copy", ".git", "config"))
	if err != nil || string(content) != "edited" || !strings.Contains(output.String(), filepath.Join(parent, "copy")) {
		t.Fatalf("restore content = %q, output = %q, error = %v", content, output.String(), err)
	}
	command = cli.New()
	command.SetArgs([]string{"restore-local", entries[0].Name(), parent, "copy"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("overwrote existing restore")
	}
}

type cancelingWriter struct {
	cancel context.CancelFunc
	writes int
}

func (writer *cancelingWriter) Write(data []byte) (int, error) {
	writer.writes++
	writer.cancel()
	return len(data), nil
}

func TestBackupListWithoutStateDoesNotCreateIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"backup-list"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "没有") {
		t.Fatalf("unexpected output: %q", output.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created state directory: %v", err)
	}
}

func TestBackupListRejectsDatabaseSymlink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "other.db"), filepath.Join(directory, "client.db")); err != nil {
		t.Fatal(err)
	}
	command := cli.New()
	command.SetArgs([]string{"backup-list"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted symlinked database")
	}
	if _, err := os.Lstat(filepath.Join(home, "other.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("followed database symlink: %v", err)
	}
}

func TestBackupLocalRejectsUntrustedSourceParent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, ".agents", "skills"), 0o777); err != nil {
		t.Fatal(err)
	}
	command = cli.New()
	command.SetArgs([]string{"backup-local", "demo"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted untrusted source parent")
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "share", "skillhub", "backups")); !os.IsNotExist(err) {
		t.Fatalf("backup root created: %v", err)
	}
}

func TestBackupLocalReportsCommittedBackupWhenOutputFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected output failure")
	command = cli.New()
	command.SetOut(failingWriter{err: injected})
	command.SetArgs([]string{"backup-local", "demo"})
	err := command.ExecuteContext(t.Context())
	if !errors.Is(err, injected) {
		t.Fatalf("got %v, want output failure", err)
	}
	db, openErr := sql.Open("sqlite3", filepath.Join(home, ".local", "share", "skillhub", "client.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	var id string
	if queryErr := db.QueryRowContext(t.Context(), `SELECT backup_key FROM backups`).Scan(&id); queryErr != nil {
		t.Fatal(queryErr)
	}
	if !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "completed") {
		t.Fatalf("missing committed backup information: %v", err)
	}
}

type failingWriter struct{ err error }

func (writer failingWriter) Write(_ []byte) (int, error) { return 0, writer.err }

func TestInstallLocalCanceledBeforeBootstrap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	command := cli.New()
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	for _, path := range []string{".agents", ".local"} {
		if _, err := os.Lstat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Fatalf("created %s: %v", path, err)
		}
	}
}

func TestInstallLocalRejectsUntrustedStateDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := cli.New()
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted world-writable state directory")
	}
	if _, err := os.Lstat(filepath.Join(directory, "client.db")); !os.IsNotExist(err) {
		t.Fatalf("state database created: %v", err)
	}
}

func TestInstallLocalRejectsUntrustedStatePath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	for _, kind := range []string{"symlink", "writable-ancestor"} {
		t.Run(kind, func(t *testing.T) {
			home, source := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			local := filepath.Join(home, ".local")
			if kind == "symlink" {
				if err := os.Symlink(t.TempDir(), local); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(local, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(local, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
				t.Fatal(err)
			}
			command := cli.New()
			command.SetArgs([]string{"install-local", source, "--slug", "demo"})
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatalf("accepted %s state path", kind)
			}
			if _, err := os.Lstat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
				t.Fatalf("canonical directory created: %v", err)
			}
		})
	}
}

func TestInstallLocalRejectsInvalidSlugBeforeCreatingDirectories(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := cli.New()
	command.SetArgs([]string{"install-local", source, "--slug", "../escape"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted invalid slug")
	}
	for _, path := range []string{".agents", ".local"} {
		if _, err := os.Lstat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Fatalf("created %s: %v", path, err)
		}
	}
}

func TestInstallStatusRejectsDatabaseSymlink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, outside := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(outside, "unrelated.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE unrelated (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(database, filepath.Join(directory, "client.db")); err != nil {
		t.Fatal(err)
	}
	command := cli.New()
	command.SetArgs([]string{"install-status"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted symlinked database")
	}
	db, err = sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE name = 'installed_skills'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("migrated unrelated database")
	}
}

func TestInstallLocalRejectsWritableTargetDirectories(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	for _, kind := range []string{"canonical", "agent"} {
		t.Run(kind, func(t *testing.T) {
			home, source := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			canonical, agent := filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")
			for _, path := range []string{canonical, agent} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writable := canonical
			if kind == "agent" {
				writable = agent
			}
			if err := os.Chmod(writable, 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
				t.Fatal(err)
			}
			command := cli.New()
			command.SetArgs([]string{"install-local", source, "--slug", "demo"})
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatalf("accepted writable %s directory", kind)
			}
			if _, err := os.Lstat(filepath.Join(canonical, "demo")); !os.IsNotExist(err) {
				t.Fatalf("created canonical target: %v", err)
			}
		})
	}
}

func TestInstallLocalAcceptsTrustedDirectoryAliases(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	canonical := filepath.Join(home, "canonical")
	for _, path := range []string{canonical, filepath.Join(home, ".agents"), filepath.Join(home, ".claude"), filepath.Join(home, ".codex", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
		if err := os.Symlink(canonical, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(home, ".codex", "skills", "demo"))
	if err != nil || link != filepath.Join(canonical, "demo") {
		t.Fatalf("link = %q, error = %v", link, err)
	}
	info, err := os.Lstat(filepath.Join(home, ".claude", "skills", "demo"))
	if err != nil || !info.IsDir() {
		t.Fatalf("canonical alias is not a directory: %v", err)
	}
}

func TestInstallLocalRejectsUntrustedIntermediateSymlink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude"), filepath.Join(home, "trusted"), filepath.Join(home, "unsafe")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(home, "unsafe"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "trusted"), filepath.Join(home, "unsafe", "bridge")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "unsafe", "bridge"), filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := cli.New()
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted untrusted intermediate symlink path")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("canonical target created: %v", err)
	}
}

func TestInstallLocalRejectsUntrustedInputDotDot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	for _, path := range []string{"base/data", "unsafe/sub", "unsafe/data"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "unsafe"), 0o777); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"base/data", "unsafe/data"} {
		if err := os.WriteFile(filepath.Join(root, path, "SKILL.md"), []byte("skill"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "unsafe", "sub"), filepath.Join(root, "base", "link")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "base", "link") + "/../data"
	command := cli.New()
	command.SetArgs([]string{"install-local", source, "--slug", "demo"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted untrusted source path")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("canonical target created: %v", err)
	}
}

func TestInstallLocalRejectsWritableSourceContent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only local installation")
	}
	for _, kind := range []string{"skill", "nested-file", "nested-directory"} {
		t.Run(kind, func(t *testing.T) {
			home, source := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			if err := os.Mkdir(filepath.Join(source, "assets"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(source, "SKILL.md"), filepath.Join(source, "assets", "file")} {
				if err := os.WriteFile(path, []byte("skill"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path, mode := filepath.Join(source, "SKILL.md"), os.FileMode(0o666)
			if kind == "nested-file" {
				path = filepath.Join(source, "assets", "file")
			}
			if kind == "nested-directory" {
				path, mode = filepath.Join(source, "assets"), 0o777
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			command := cli.New()
			command.SetArgs([]string{"install-local", source, "--slug", "demo"})
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatal("accepted writable source content")
			}
			if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
				t.Fatalf("canonical target created: %v", err)
			}
		})
	}
}

func TestBrowseCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	directory := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = directory
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	runGit("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: Remote\ndescription: Cloned\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "SKILL.md")
	runGit("commit", "--quiet", "-m", "fixture")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+directory+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/fixture/demo.git")
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"browse", "fixture/demo", "--branch", "main"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Remote") || !strings.Contains(output.String(), "Cloned") {
		t.Fatalf("skill missing from output: %q", output.String())
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("No frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "SKILL.md")
	runGit("commit", "--quiet", "-m", "fallback fixture")
	output.Reset()
	command = cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"browse", "fixture/demo", "--branch", "main"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "demo  .     demo") {
		t.Fatalf("root skill should fall back to repository name: %q", output.String())
	}
}

func TestScanCommandAcceptsSymlinkDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: Linked\ndescription: Example\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(directory, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"scan", link})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Linked") {
		t.Fatalf("skill missing from output: %q", output.String())
	}
}

func TestScanCommandSanitizesTerminalControls(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "evil\x1b[2J")
	if runtime.GOOS == "windows" {
		path = filepath.Join(directory, "evil")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: \"\\e]52;c;SGk=\\a\"\ndescription: \"hello\\tworld\"\n---\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"scan", directory})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\x07") {
		t.Fatalf("terminal controls in output: %q", output.String())
	}
	if !strings.Contains(output.String(), "hello world") {
		t.Fatalf("description should remain readable: %q", output.String())
	}
}

func TestScanCommandPreservesUnicodeText(t *testing.T) {
	directory := t.TempDir()
	content := "---\nname: می‌روم\ndescription: 👩‍💻\n---\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"scan", directory})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "می‌روم") || !strings.Contains(output.String(), "👩‍💻") {
		t.Fatalf("Unicode text changed: %q", output.String())
	}
}
