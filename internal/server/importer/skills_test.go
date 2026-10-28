package importer_test

import (
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
)

func TestCCSwitchSkillPreview(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux database trust checks")
	}
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE skills(id TEXT PRIMARY KEY, name TEXT NOT NULL, directory TEXT NOT NULL); INSERT INTO skills VALUES ('repo:one','One Skill','one'), ('repo:two','Two Skill','two')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := importer.PreviewCCSwitchSkills(t.Context(), path)
	if err != nil || !result.Found || len(result.Skills) != 2 || result.Skills[0].ID != "repo:one" || result.Skills[0].Directory != "one" || result.Skills[0].Name != "One Skill" {
		t.Fatalf("preview = %+v, error = %v", result, err)
	}
}

func TestCCSwitchSkillPreviewRejectsTraversal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux database trust checks")
	}
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE skills(id TEXT, name TEXT, directory TEXT); INSERT INTO skills VALUES ('id','Name','../user-data')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.PreviewCCSwitchSkills(t.Context(), path); err == nil {
		t.Fatal("accepted source directory traversal")
	}
}
