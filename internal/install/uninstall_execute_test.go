package install_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/state"
)

func uninstallExecutionFixture(t *testing.T) (*install.Session, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only uninstall")
	}
	home, source, directory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".codex", "skills"), filepath.Join(source, ".hidden", "empty")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string][]byte{"SKILL.md": []byte("skill"), ".hidden/binary": {0, 255, 0, 128}, "run": []byte("#!/bin/sh\nexit 0\n")} {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), content, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	for _, slug := range []string{"demo", "other"} {
		if err := session.InstallLocal(t.Context(), home, slug, source); err != nil {
			t.Fatal(err)
		}
	}
	return session, home, directory
}

func uninstallExecutionDatabase(t *testing.T, directory string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestUninstallLocalRemovesOnlyManagedTargetsAndPreservesBackup(t *testing.T) {
	session, home, directory := uninstallExecutionFixture(t)
	backup, err := session.UninstallLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if backup.ID == "" || backup.Path == "" {
		t.Fatalf("missing recovery backup: %+v", backup)
	}
	for _, agent := range []string{".agents", ".claude", ".codex"} {
		parent := filepath.Join(home, agent, "skills")
		if _, err := os.Lstat(filepath.Join(parent, "demo")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("uninstalled target still exists in %s: %v", agent, err)
		}
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 1 || entries[0].Name() != "other" {
			t.Fatalf("unrelated target or quarantine changed in %s: %v, %v", agent, entries, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(backup.Path, ".hidden", "binary"))
	if err != nil || !bytes.Equal(content, []byte{0, 255, 0, 128}) {
		t.Fatalf("backup lost binary content: %v, %v", content, err)
	}
	for name, mode := range map[string]os.FileMode{"run": 0o700, ".hidden/empty": os.ModeDir | 0o700} {
		info, err := os.Stat(filepath.Join(backup.Path, filepath.FromSlash(name)))
		if err != nil || info.Mode() != mode {
			t.Fatalf("backup mode for %s: %v, %v", name, info, err)
		}
	}
	if _, err := session.Installation(t.Context(), "demo"); err == nil {
		t.Fatal("uninstalled skill remains managed")
	}
	if _, err := session.Installation(t.Context(), "other"); err != nil {
		t.Fatalf("unrelated installation lost: %v", err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("completed uninstall has pending work: %+v, %v", pending, err)
	}
	database := uninstallExecutionDatabase(t, directory)
	var status string
	if err := database.QueryRowContext(t.Context(), `SELECT status FROM operations WHERE action = 'uninstall' AND backup_id = ?`, backup.ID).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("uninstall status = %q, %v", status, err)
	}
	var cleaned int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM operation_targets JOIN operations ON operation_targets.operation_id = operations.id WHERE operations.action = 'uninstall' AND operation_targets.kind != 'backup_source' AND publish_intent AND published AND cleanup_intent AND cleaned AND expected_identity = object_identity`).Scan(&cleaned); err != nil || cleaned != 3 {
		t.Fatalf("cleaned targets = %d, %v", cleaned, err)
	}
}

func TestUninstallLocalRejectsChangedOrReplacedInstalledTargets(t *testing.T) {
	for _, change := range []string{"content", "extra", "canonical", "symlink"} {
		t.Run(change, func(t *testing.T) {
			session, home, _ := uninstallExecutionFixture(t)
			canonical := filepath.Join(home, ".agents", "skills", "demo")
			projection := filepath.Join(home, ".claude", "skills", "demo")
			switch change {
			case "content", "extra":
				name := "SKILL.md"
				if change == "extra" {
					name = "user.txt"
				}
				if err := os.WriteFile(filepath.Join(canonical, name), []byte("user content"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "canonical":
				if err := os.Rename(canonical, canonical+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(canonical, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(projection, projection+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(canonical, projection); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := session.UninstallLocal(t.Context(), "demo"); err == nil {
				t.Fatalf("accepted %s change", change)
			}
			for _, path := range []string{canonical, projection} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("changed target was removed: %v", err)
				}
			}
			backups, err := session.Backups(t.Context())
			if err != nil || len(backups) != 0 {
				t.Fatalf("unexpected backup before validation: %+v, %v", backups, err)
			}
			if _, err := session.Installation(t.Context(), "demo"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type uninstallExecutionObserver struct {
	context.Context
	mutex    sync.Mutex
	database *sql.DB
	when     string
	ready    func(string, string) bool
	change   func(string, string) error
	fired    bool
	failure  error
}

func (observer *uninstallExecutionObserver) Err() error {
	if err := observer.Context.Err(); err != nil {
		return err
	}
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	if observer.fired || observer.failure != nil {
		return observer.Context.Err()
	}
	var path, stage string
	query := `SELECT targets.path, targets.stage_name FROM operation_targets AS targets JOIN operations ON targets.operation_id = operations.id WHERE operations.action = 'uninstall' AND (` + observer.when + `) LIMIT 1`
	err := observer.database.QueryRowContext(observer.Context, query).Scan(&path, &stage)
	if errors.Is(err, sql.ErrNoRows) {
		return observer.Context.Err()
	}
	if err != nil {
		observer.failure = err
		return observer.Context.Err()
	}
	if observer.ready != nil && !observer.ready(path, stage) {
		return observer.Context.Err()
	}
	observer.fired = true
	observer.failure = observer.change(path, stage)
	return observer.Context.Err()
}

func (observer *uninstallExecutionObserver) assertChanged(t *testing.T) {
	t.Helper()
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	if !observer.fired || observer.failure != nil {
		t.Fatalf("fault injection fired = %v, error = %v", observer.fired, observer.failure)
	}
}

func assertUninstallExecutionRecovery(t *testing.T, session *install.Session, backup state.Backup, resultErr error) state.Operation {
	t.Helper()
	if resultErr == nil || backup.ID == "" || !strings.Contains(resultErr.Error(), backup.ID) {
		t.Fatalf("missing backup/recovery error: backup = %+v, error = %v", backup, resultErr)
	}
	content, err := os.ReadFile(filepath.Join(backup.Path, "SKILL.md"))
	if err != nil || string(content) != "skill" {
		t.Fatalf("recovery backup changed: %q, %v", content, err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || !strings.Contains(resultErr.Error(), pending[0].Plan.ID) {
		t.Fatalf("recovery journal = %+v, error = %v; uninstall error = %v", pending, err, resultErr)
	}
	if _, err := session.Installation(t.Context(), "demo"); err != nil {
		t.Fatalf("failed uninstall removed management record: %v", err)
	}
	return pending[0]
}

func TestUninstallLocalPreservesTargetsChangedAfterMoveIntent(t *testing.T) {
	for _, change := range []string{"content", "canonical", "symlink", "quarantine"} {
		t.Run(change, func(t *testing.T) {
			session, home, directory := uninstallExecutionFixture(t)
			kind := "canonical"
			if change == "symlink" || change == "quarantine" {
				kind = "symlink"
			}
			var preserved string
			observer := &uninstallExecutionObserver{Context: t.Context(), database: uninstallExecutionDatabase(t, directory), when: "targets.kind = '" + kind + "' AND targets.publish_intent"}
			observer.change = func(path, stage string) error {
				preserved = path
				switch change {
				case "content":
					preserved = filepath.Join(path, "SKILL.md")
					return os.WriteFile(preserved, []byte("user edit"), 0o600)
				case "canonical":
					if err := os.Rename(path, path+".saved"); err != nil {
						return err
					}
					return os.Mkdir(path, 0o700)
				case "symlink":
					if err := os.Rename(path, path+".saved"); err != nil {
						return err
					}
					return os.Symlink(filepath.Join(home, ".agents", "skills", "demo"), path)
				case "quarantine":
					preserved = filepath.Join(filepath.Dir(path), stage)
					return os.WriteFile(preserved, []byte("unmanaged quarantine"), 0o600)
				}
				return fmt.Errorf("unknown change")
			}
			backup, resultErr := session.UninstallLocal(observer, "demo")
			observer.assertChanged(t)
			operation := assertUninstallExecutionRecovery(t, session, backup, resultErr)
			if _, err := os.Lstat(preserved); err != nil {
				t.Fatalf("changed target removed: %v", err)
			}
			for _, target := range operation.Plan.Targets {
				if target.CleanupIntent || target.Cleaned {
					t.Fatalf("cleanup began after target change: %+v", target)
				}
			}
		})
	}
}

func TestUninstallLocalPreservesQuarantineChangedAfterCleanupIntent(t *testing.T) {
	for _, change := range []string{"content", "file", "directory", "canonical", "symlink", "parent", "backup"} {
		t.Run(change, func(t *testing.T) {
			session, home, directory := uninstallExecutionFixture(t)
			kind := "canonical"
			if change == "symlink" || change == "backup" {
				kind = "symlink"
			}
			var preserved string
			observer := &uninstallExecutionObserver{Context: t.Context(), database: uninstallExecutionDatabase(t, directory), when: "targets.kind = '" + kind + "' AND targets.cleanup_intent"}
			observer.change = func(path, stage string) error {
				quarantine := filepath.Join(filepath.Dir(path), stage)
				preserved = quarantine
				switch change {
				case "content":
					preserved = filepath.Join(quarantine, "SKILL.md")
					return os.WriteFile(preserved, []byte("user edit"), 0o600)
				case "file":
					preserved = filepath.Join(quarantine, "SKILL.md")
					if err := os.Rename(preserved, preserved+".saved"); err != nil {
						return err
					}
					return os.WriteFile(preserved, []byte("skill"), 0o700)
				case "directory":
					preserved = filepath.Join(quarantine, ".hidden", "empty")
					if err := os.Rename(preserved, preserved+".saved"); err != nil {
						return err
					}
					return os.Mkdir(preserved, 0o700)
				case "canonical":
					if err := os.Rename(quarantine, quarantine+".saved"); err != nil {
						return err
					}
					return os.Mkdir(quarantine, 0o700)
				case "symlink":
					if err := os.Rename(quarantine, quarantine+".saved"); err != nil {
						return err
					}
					return os.Symlink(filepath.Join(home, ".agents", "skills", "demo"), quarantine)
				case "parent":
					parent := filepath.Dir(path)
					if err := os.Rename(parent, parent+".saved"); err != nil {
						return err
					}
					preserved = filepath.Join(parent+".saved", stage, "SKILL.md")
					return os.Mkdir(parent, 0o700)
				case "backup":
					var backupPath string
					if err := observer.database.QueryRowContext(t.Context(), `SELECT backups.path FROM backups JOIN operations ON backups.backup_key = operations.backup_id WHERE operations.action = 'uninstall'`).Scan(&backupPath); err != nil {
						return err
					}
					return os.Chmod(backupPath, 0o755)
				}
				return fmt.Errorf("unknown change")
			}
			backup, resultErr := session.UninstallLocal(observer, "demo")
			observer.assertChanged(t)
			assertUninstallExecutionRecovery(t, session, backup, resultErr)
			if _, err := os.Lstat(preserved); err != nil {
				t.Fatalf("changed quarantine removed: %v", err)
			}
		})
	}
}

func TestUninstallLocalJournalFailuresRetainBackupAndRecoverableProgress(t *testing.T) {
	for _, boundary := range []string{"publish_intent", "published", "cleanup_intent", "cleaned", "completed"} {
		t.Run(boundary, func(t *testing.T) {
			session, _, directory := uninstallExecutionFixture(t)
			database := uninstallExecutionDatabase(t, directory)
			trigger := `CREATE TRIGGER fail_uninstall BEFORE UPDATE OF ` + boundary + ` ON operation_targets WHEN NEW.` + boundary + ` AND EXISTS(SELECT 1 FROM operations WHERE id = NEW.operation_id AND action = 'uninstall') BEGIN SELECT RAISE(ABORT, 'injected uninstall failure'); END`
			if boundary == "completed" {
				trigger = `CREATE TRIGGER fail_uninstall BEFORE UPDATE OF status ON operations WHEN NEW.action = 'uninstall' AND NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected uninstall failure'); END`
			}
			if _, err := database.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			backup, resultErr := session.UninstallLocal(t.Context(), "demo")
			operation := assertUninstallExecutionRecovery(t, session, backup, resultErr)
			if !strings.Contains(resultErr.Error(), "injected uninstall failure") {
				t.Fatalf("wrong failure: %v", resultErr)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := install.OpenSession(t.Context(), directory)
			if err != nil {
				t.Fatalf("cannot reopen interrupted %s uninstall: %v", boundary, err)
			}
			defer reopened.Close()
			recovered, err := reopened.RecoverInterruptedOperations(t.Context())
			if err != nil || len(recovered) != 1 || recovered[0].Plan.ID != operation.Plan.ID || recovered[0].Status != "needs_attention" {
				t.Fatalf("recovery changed failed operation: %+v, %v", recovered, err)
			}
		})
	}
}

func TestUninstallLocalCancellationAfterCleanupIntentRecordsAttention(t *testing.T) {
	session, _, directory := uninstallExecutionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observer := &uninstallExecutionObserver{Context: ctx, database: uninstallExecutionDatabase(t, directory), when: "targets.cleanup_intent", change: func(_, _ string) error {
		cancel()
		return nil
	}}
	backup, resultErr := session.UninstallLocal(observer, "demo")
	observer.assertChanged(t)
	operation := assertUninstallExecutionRecovery(t, session, backup, resultErr)
	if !errors.Is(resultErr, context.Canceled) {
		t.Fatalf("cancellation was not preserved: %v", resultErr)
	}
	for _, target := range operation.Plan.Targets {
		if target.Kind == "backup_source" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(target.Path), target.StageName)); err != nil {
			t.Fatalf("canceled cleanup removed quarantine: %v", err)
		}
	}
}

func TestUninstallLocalDoesNotUnlinkFileReplacedDuringFinalRead(t *testing.T) {
	session, _, directory := uninstallExecutionFixture(t)
	observer := &uninstallExecutionObserver{Context: t.Context(), database: uninstallExecutionDatabase(t, directory), when: "targets.kind = 'canonical' AND targets.cleanup_intent"}
	checks := 0
	observer.ready = func(path, stage string) bool {
		quarantine := filepath.Join(filepath.Dir(path), stage)
		if _, err := os.Lstat(filepath.Join(quarantine, ".hidden")); !errors.Is(err, os.ErrNotExist) {
			return false
		}
		checks++
		return checks == 6
	}
	var replacement string
	observer.change = func(path, stage string) error {
		replacement = filepath.Join(filepath.Dir(path), stage, "SKILL.md")
		if err := os.Rename(replacement, replacement+".saved"); err != nil {
			return err
		}
		return os.WriteFile(replacement, []byte("user replacement"), 0o600)
	}
	backup, resultErr := session.UninstallLocal(observer, "demo")
	observer.assertChanged(t)
	assertUninstallExecutionRecovery(t, session, backup, resultErr)
	content, err := os.ReadFile(replacement)
	if err != nil || string(content) != "user replacement" {
		t.Fatalf("replacement was unlinked during final verification: %q, %v", content, err)
	}
}

func TestUninstallLocalCanceledOrClosedSessionDoesNotCreateWork(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed=%v", closed), func(t *testing.T) {
			session, home, directory := uninstallExecutionFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if closed {
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			backup, resultErr := session.UninstallLocal(ctx, "demo")
			if resultErr == nil || backup.ID != "" {
				t.Fatalf("created uninstall work: backup = %+v, error = %v", backup, resultErr)
			}
			if !closed && !errors.Is(resultErr, context.Canceled) {
				t.Fatalf("cancellation lost: %v", resultErr)
			}
			if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			var count int
			database := uninstallExecutionDatabase(t, directory)
			if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM operations WHERE action IN ('backup', 'uninstall')`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("created journal entries: %d, %v", count, err)
			}
		})
	}
}

func TestUninstallLocalProcessExitRetainsBackupAndPendingJournal(t *testing.T) {
	if directory := os.Getenv("SKILLHUB_UNINSTALL_CHILD_STATE"); directory != "" {
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		boundary := os.Getenv("SKILLHUB_UNINSTALL_CHILD_BOUNDARY")
		field := boundary
		if boundary == "renamed" {
			field = "publish_intent"
		} else if boundary == "unlinked" {
			field = "cleanup_intent"
		}
		observer := &uninstallExecutionObserver{Context: t.Context(), database: uninstallExecutionDatabase(t, directory), when: "targets." + field, change: func(_, _ string) error {
			os.Exit(73)
			return nil
		}}
		if boundary == "renamed" || boundary == "unlinked" {
			observer.ready = func(path, stage string) bool {
				_, err := os.Lstat(filepath.Join(filepath.Dir(path), stage))
				return boundary == "renamed" && err == nil || boundary == "unlinked" && errors.Is(err, os.ErrNotExist)
			}
		}
		if _, err := session.UninstallLocal(observer, "demo"); err != nil {
			t.Fatal(err)
		}
		t.Fatal("child did not exit at the requested journal boundary")
	}
	for _, boundary := range []string{"publish_intent", "renamed", "published", "cleanup_intent", "unlinked", "cleaned"} {
		t.Run(boundary, func(t *testing.T) {
			session, _, directory := uninstallExecutionFixture(t)
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestUninstallLocalProcessExitRetainsBackupAndPendingJournal$")
			command.Env = append(os.Environ(), "SKILLHUB_UNINSTALL_CHILD_STATE="+directory, "SKILLHUB_UNINSTALL_CHILD_BOUNDARY="+boundary)
			output, err := command.CombinedOutput()
			exit, matches := errors.AsType[*exec.ExitError](err)
			if !matches || exit.ExitCode() != 73 {
				t.Fatalf("child exit = %v, output = %s", err, output)
			}
			reopened, err := install.OpenSession(t.Context(), directory)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			pending, err := reopened.PendingOperations(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Status != "active" {
				t.Fatalf("interrupted journal = %+v, %v", pending, err)
			}
			backups, err := reopened.Backups(t.Context())
			if err != nil || len(backups) != 1 || backups[0].ID != pending[0].Plan.Backup.BackupID {
				t.Fatalf("interrupted backup = %+v, %v", backups, err)
			}
			var paths []string
			var identities []os.FileInfo
			for _, target := range pending[0].Plan.Targets {
				paths = append(paths, target.Path)
				if target.StageName != "" {
					paths = append(paths, filepath.Join(filepath.Dir(target.Path), target.StageName))
				}
			}
			for _, path := range paths {
				info, err := os.Lstat(path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				identities = append(identities, info)
			}
			for range 2 {
				operations, err := reopened.RecoverInterruptedOperations(t.Context())
				if err != nil || len(operations) != 1 || operations[0].Status != "needs_attention" || operations[0].Plan.ID != pending[0].Plan.ID {
					t.Fatalf("conservative recovery = %+v, %v", operations, err)
				}
			}
			for index, path := range paths {
				info, err := os.Lstat(path)
				if identities[index] == nil {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("recovery created %s: %v", path, err)
					}
				} else if err != nil || !os.SameFile(identities[index], info) {
					t.Fatalf("recovery changed %s: %v", path, err)
				}
			}
			content, err := os.ReadFile(filepath.Join(backups[0].Path, "SKILL.md"))
			if err != nil || string(content) != "skill" {
				t.Fatalf("process exit lost backup: %q, %v", content, err)
			}
		})
	}
}

func TestUninstallLocalDoesNotCleanProjectionsWhenMovedCanonicalChanges(t *testing.T) {
	session, _, directory := uninstallExecutionFixture(t)
	database := uninstallExecutionDatabase(t, directory)
	observer := &uninstallExecutionObserver{Context: t.Context(), database: database, when: "targets.kind = 'symlink' AND targets.cleanup_intent"}
	observer.change = func(_, _ string) error {
		var path, stage string
		if err := database.QueryRowContext(t.Context(), `SELECT targets.path, targets.stage_name FROM operation_targets AS targets JOIN operations ON targets.operation_id = operations.id WHERE operations.action = 'uninstall' AND targets.kind = 'canonical'`).Scan(&path, &stage); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(filepath.Dir(path), stage, "SKILL.md"), []byte("user edit"), 0o600)
	}
	backup, resultErr := session.UninstallLocal(observer, "demo")
	observer.assertChanged(t)
	operation := assertUninstallExecutionRecovery(t, session, backup, resultErr)
	for _, target := range operation.Plan.Targets {
		if target.Kind == "backup_source" {
			continue
		}
		if target.Cleaned {
			t.Fatalf("cleaned a projection after canonical changed: %+v", target)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(target.Path), target.StageName)); err != nil {
			t.Fatalf("changed installation lost quarantine: %v", err)
		}
	}
}

func TestUninstallLocalReportsFailureToRecordAttention(t *testing.T) {
	session, home, directory := uninstallExecutionFixture(t)
	database := uninstallExecutionDatabase(t, directory)
	for _, statement := range []string{
		`CREATE TRIGGER fail_uninstall_move BEFORE UPDATE OF publish_intent ON operation_targets WHEN NEW.publish_intent AND EXISTS(SELECT 1 FROM operations WHERE id = NEW.operation_id AND action = 'uninstall') BEGIN SELECT RAISE(ABORT, 'injected move failure'); END`,
		`CREATE TRIGGER fail_uninstall_attention BEFORE UPDATE OF status ON operations WHEN NEW.action = 'uninstall' AND NEW.status = 'needs_attention' BEGIN SELECT RAISE(ABORT, 'injected attention failure'); END`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	backup, resultErr := session.UninstallLocal(t.Context(), "demo")
	if resultErr == nil || backup.ID == "" || !strings.Contains(resultErr.Error(), "attention status was not recorded") || !strings.Contains(resultErr.Error(), backup.ID) {
		t.Fatalf("missing uncertain recovery status: backup = %+v, error = %v", backup, resultErr)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "active" || !strings.Contains(resultErr.Error(), pending[0].Plan.ID) {
		t.Fatalf("lost active recovery operation: %+v, %v", pending, err)
	}
	for _, path := range []string{filepath.Join(backup.Path, "SKILL.md"), filepath.Join(home, ".agents", "skills", "demo", "SKILL.md")} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "skill" {
			t.Fatalf("failure to record attention removed content: %q, %v", content, err)
		}
	}
}

func TestUninstallLocalStartFailureKeepsPlannedOperationAndBackup(t *testing.T) {
	session, home, directory := uninstallExecutionFixture(t)
	database := uninstallExecutionDatabase(t, directory)
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER fail_uninstall_start BEFORE UPDATE OF status ON operations WHEN NEW.action = 'uninstall' AND NEW.status = 'active' BEGIN SELECT RAISE(ABORT, 'injected start failure'); END`); err != nil {
		t.Fatal(err)
	}
	backup, resultErr := session.UninstallLocal(t.Context(), "demo")
	if resultErr == nil || backup.ID == "" || !strings.Contains(resultErr.Error(), "injected start failure") {
		t.Fatalf("start failure lost backup or cause: backup = %+v, error = %v", backup, resultErr)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "planned" || !strings.Contains(resultErr.Error(), pending[0].Plan.ID) {
		t.Fatalf("lost planned recovery operation: %+v, %v", pending, err)
	}
	for _, path := range []string{filepath.Join(backup.Path, "SKILL.md"), filepath.Join(home, ".agents", "skills", "demo", "SKILL.md")} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "skill" {
			t.Fatalf("start failure removed content: %q, %v", content, err)
		}
	}
	if err := session.CancelPlannedOperation(t.Context(), pending[0].Plan.ID); err != nil {
		t.Fatalf("cannot safely cancel unstarted uninstall: %v", err)
	}
}
