package importer_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
	_ "github.com/lib-x/entsqlite"
)

func TestCCSwitchRepositoryPreview(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux database trust checks")
	}
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE skill_repos (owner TEXT NOT NULL, name TEXT NOT NULL, branch TEXT NOT NULL DEFAULT 'main', enabled BOOLEAN NOT NULL DEFAULT 1, PRIMARY KEY (owner,name)); INSERT INTO skill_repos(owner,name,branch,enabled) VALUES ('example','one','HEAD',1),('example','two','dev',0),('example','three','head',1),('example','zero','',1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := importer.PreviewCCSwitchRepos(t.Context(), path)
	if err != nil || !result.Found || len(result.Repositories) != 4 {
		t.Fatalf("preview = %+v, error = %v", result, err)
	}
	if result.Repositories[0].Owner != "example" || result.Repositories[0].Name != "one" || result.Repositories[0].Branch != "" || !result.Repositories[0].Enabled {
		t.Fatalf("HEAD repository = %+v", result.Repositories[0])
	}
	if result.Repositories[1].Name != "three" || result.Repositories[1].Branch != "" || result.Repositories[3].Name != "zero" || result.Repositories[3].Branch != "" {
		t.Fatalf("default branch repositories = %+v", result.Repositories)
	}
	if result.Repositories[2].Name != "two" || result.Repositories[2].Branch != "dev" || result.Repositories[2].Enabled {
		t.Fatalf("disabled repository = %+v", result.Repositories[2])
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("source changed: %v", err)
	}
}

func TestCCSwitchMissingDatabaseIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	result, err := importer.PreviewCCSwitchRepos(t.Context(), path)
	if err != nil || result.Found || len(result.Repositories) != 0 {
		t.Fatalf("preview = %+v, error = %v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing database was created: %v", err)
	}
}

func TestCCSwitchReadsWALWithoutCreatingDatabase(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux database trust checks")
	}
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `PRAGMA journal_mode=WAL; CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT, enabled INTEGER); INSERT INTO skill_repos VALUES ('wal-owner','skill','HEAD',1)`); err != nil {
		t.Fatal(err)
	}
	preview, err := importer.PreviewCCSwitchRepos(t.Context(), path)
	if err != nil || len(preview.Repositories) != 1 || preview.Repositories[0].Owner != "wal-owner" {
		t.Fatalf("WAL preview = %+v, %v", preview, err)
	}
}

func TestCCSwitchRejectsUnsupportedDatabaseAndLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE VIEW skill_repos AS SELECT 'owner' AS owner, 'repo' AS name, 'HEAD' AS branch, 1 AS enabled`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.PreviewCCSwitchRepos(t.Context(), path); err == nil || strings.Contains(err.Error(), path) {
		t.Fatalf("view error = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(filepath.Dir(path), "link.db")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.PreviewCCSwitchRepos(t.Context(), link); err == nil {
		t.Fatal("accepted database symlink")
	}
}

func TestCCSwitchRejectsOtherWritableParent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permission checks")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "untrusted")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT, enabled INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.PreviewCCSwitchRepos(t.Context(), path); err == nil {
		t.Fatal("accepted other-writable database parent")
	}
}

func TestCCSwitchRejectsUntrustedSidecars(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permission checks")
	}
	root := t.TempDir()
	path := filepath.Join(root, "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT, enabled INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, path+"-wal"); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.PreviewCCSwitchRepos(t.Context(), path); err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("sidecar error = %v", err)
	}
}
