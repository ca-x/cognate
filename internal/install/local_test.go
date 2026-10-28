package install_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/objectid"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/state"
	"github.com/spf13/afero"
)

func TestInstallLocalProjectsDiscoveredAgents(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	canonical := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	for _, path := range []string{canonical, claude, filepath.Join(home, ".gemini")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(claude, "demo", "SKILL.md"))
	if err != nil || string(data) != "skill" {
		t.Fatalf("data = %q, error = %v", data, err)
	}
	link, err := os.Readlink(filepath.Join(claude, "demo"))
	if err != nil || link != filepath.Join(canonical, "demo") {
		t.Fatalf("link = %q, error = %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "skills")); !os.IsNotExist(err) {
		t.Fatalf("missing agent skills created: %v", err)
	}
	store, err := state.Open(t.Context(), filepath.Join(stateDirectory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.Get(t.Context(), "demo")
	if err != nil || len(record.Projections) != 2 {
		t.Fatalf("record = %+v, error = %v", record, err)
	}
	for _, projection := range record.Projections {
		info, err := os.Lstat(projection.Path)
		if err != nil {
			t.Fatal(err)
		}
		var identity string
		switch projection.Kind {
		case "canonical":
			identity, err = objectid.FromFileInfo(info)
		case "symlink":
			identity, err = objectid.FromLinkInfo(info)
		default:
			t.Fatalf("unexpected projection kind: %q", projection.Kind)
		}
		if err != nil || identity != projection.Identity {
			t.Fatalf("stored identity = %q, actual = %q, error = %v", projection.Identity, identity, err)
		}
	}
}

func TestInstallSelectedAgentsAndRejectChangedPreview(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".codex", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHashContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.InstallLocalWithSelection(t.Context(), home, "demo", source, hash, []string{"claude"}); err == nil {
		t.Fatal("accepted changed source")
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.InstallLocalWithSelection(t.Context(), home, "demo", source, hash, []string{"unknown"}); err == nil {
		t.Fatal("accepted unknown selected agent")
	}
	if _, err := session.InstallLocalWithSelection(t.Context(), home, "demo", source, hash, []string{"claude", "claude"}); err == nil {
		t.Fatal("accepted duplicate selected agent")
	}
	if _, err := session.InstallLocalWithSelection(t.Context(), home, "demo", source, hash, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "demo")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("unselected agent changed: %v", err)
	}
}

func TestInstallSelectedAgentsRejectsUnselectedAlias(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".codex")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".codex", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHashContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.InstallLocalWithSelection(t.Context(), home, "demo", source, hash, []string{"claude"}); err == nil {
		t.Fatal("selected agent shares a directory with an unselected agent")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("unexpected installation: %v", err)
	}
}

func TestInstallLocalRejectsWritableSource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "SKILL.md")
	if err := os.WriteFile(path, []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.InstallLocal(t.Context(), home, "demo", source); err == nil {
		t.Fatal("accepted writable source")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestInstallLocalResolvesSourceBeforeHashAndCopy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, root, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
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
		t.Fatal(err)
	}
	source := filepath.Join(root, "base", "link") + "/../data"
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"))
	if err != nil || string(content) != "correct" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
}

func TestInstallLocalPreservesPartialFailureAcrossRestart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(stateDirectory, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_projection BEFORE UPDATE OF published ON operation_targets WHEN NEW.kind = 'symlink' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.InstallLocal(t.Context(), home, "demo", source); err == nil {
		t.Fatal("injected failure was ignored")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "skills", "demo")
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(home, ".agents", "skills", "demo") {
		t.Fatalf("target = %q, error = %v", target, err)
	}
	reopened, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for range 2 {
		pending, err := reopened.RecoverInterruptedOperations(t.Context())
		if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
			t.Fatalf("pending = %+v, error = %v", pending, err)
		}
	}
	if err := reopened.InstallLocal(t.Context(), home, "other", source); err == nil {
		t.Fatal("ignored pending operation")
	}
}

func TestInstallLocalRejectsConflictAndKeepsExisting(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.WriteFile(conflict, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.InstallLocal(t.Context(), home, "demo", source); err == nil {
		t.Fatal("installation overwrote a conflict")
	}
	content, err := os.ReadFile(conflict)
	if err != nil || string(content) != "keep" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestInstallLocalDeduplicatesVerifiedInstallation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	var canonicalInfo, projectionInfo os.FileInfo
	for iteration := range 2 {
		session, err := install.OpenSession(t.Context(), stateDirectory)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
			t.Fatal(err)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		canonical, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo"))
		if err != nil {
			t.Fatal(err)
		}
		projection, err := os.Lstat(filepath.Join(home, ".claude", "skills", "demo"))
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			canonicalInfo, projectionInfo = canonical, projection
		} else if !os.SameFile(canonicalInfo, canonical) || !os.SameFile(projectionInfo, projection) {
			t.Fatal("deduplication replaced an existing object")
		}
	}
	db, err := sql.Open("sqlite3", filepath.Join(stateDirectory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var operations, claims, mappings int
	if err := db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM operations), (SELECT count(*) FROM operation_claims), (SELECT count(*) FROM projections)`).Scan(&operations, &claims, &mappings); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || claims != 0 || mappings != 2 {
		t.Fatalf("operations = %d, claims = %d, mappings = %d", operations, claims, mappings)
	}
}

func TestInstallLocalDoesNotAdoptMatchingUnrecordedObjects(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	canonical := filepath.Join(home, ".agents", "skills", "demo")
	projection := filepath.Join(home, ".claude", "skills", "demo")
	for _, path := range []string{canonical, filepath.Dir(projection)} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{source, canonical} {
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("skill"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(canonical, projection); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.InstallLocalWithResult(t.Context(), home, "demo", source); err == nil {
		t.Fatal("adopted unrecorded matching objects")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if content, err := os.ReadFile(filepath.Join(projection, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
}

func TestInstallLocalDedupDoesNotAssignLegacyIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(stateDirectory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE projections SET identity = ''`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	session, err = install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := session.InstallLocalWithResult(t.Context(), home, "demo", source)
	if err != nil || !matched {
		t.Fatalf("matched = %t, error = %v", matched, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(stateDirectory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.Get(t.Context(), "demo")
	if err != nil || len(record.Projections) != 1 || record.Projections[0].Identity != "" {
		t.Fatalf("record = %+v, error = %v", record, err)
	}
}

func TestInstallLocalDedupDoesNotIgnorePendingOperation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "pending", Action: "install", Slug: "other", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(home, ".agents", "skills", "other"), Kind: "canonical"}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := session.InstallLocalWithResult(t.Context(), home, "demo", source); err == nil {
		t.Fatal("matched while another operation was pending")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Plan.ID != plan.ID {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestInstallLocalDedupRejectsConflictingState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	for _, change := range []string{"source", "canonical", "canonical-symlink", "canonical-replaced", "writable-canonical", "executable", "empty-directory", "special-file", "git", "projection", "projection-replaced", "projection-file", "missing-projection", "new-agent", "removed-agent"} {
		t.Run(change, func(t *testing.T) {
			home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
			for _, path := range []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
				t.Fatal(err)
			}
			session, err := install.OpenSession(t.Context(), stateDirectory)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
				t.Fatal(err)
			}
			canonical := filepath.Join(home, ".agents", "skills", "demo")
			projection := filepath.Join(home, ".claude", "skills", "demo")
			switch change {
			case "source":
				if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("new"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "canonical":
				if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("edited"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "canonical-symlink":
				if err := os.Rename(canonical, canonical+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(canonical+"-old", canonical); err != nil {
					t.Fatal(err)
				}
			case "canonical-replaced":
				if err := os.Rename(canonical, canonical+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(canonical, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("skill"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Chmod(filepath.Join(canonical, "SKILL.md"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "writable-canonical":
				if err := os.Chmod(filepath.Join(canonical, "SKILL.md"), 0o666); err != nil {
					t.Fatal(err)
				}
			case "empty-directory":
				if err := os.Mkdir(filepath.Join(canonical, "empty"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "special-file":
				if err := os.Symlink(source, filepath.Join(canonical, "alias")); err != nil {
					t.Fatal(err)
				}
			case "git":
				if err := os.Mkdir(filepath.Join(canonical, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "projection":
				if err := os.Remove(projection); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(source, projection); err != nil {
					t.Fatal(err)
				}
			case "projection-replaced":
				if err := os.Rename(projection, projection+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(canonical, projection); err != nil {
					t.Fatal(err)
				}
			case "missing-projection":
				if err := os.Remove(projection); err != nil {
					t.Fatal(err)
				}
			case "projection-file":
				if err := os.Remove(projection); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(projection, []byte(canonical), 0o600); err != nil {
					t.Fatal(err)
				}
			case "new-agent":
				if err := os.MkdirAll(filepath.Join(home, ".codex", "skills"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "removed-agent":
				if err := os.Remove(projection); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Dir(projection)); err != nil {
					t.Fatal(err)
				}
			}
			if err := session.InstallLocal(t.Context(), home, "demo", source); err == nil {
				t.Fatal("accepted conflicting installation")
			}
			pending, err := session.PendingOperations(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatalf("pending = %+v, error = %v", pending, err)
			}
		})
	}
}
