package importer_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/importer"
)

func TestSelectionSnapshotProjection(test *testing.T) {
	raw := ccSwitchSnapshot(test, string(selectionFixture(test, "ccswitch-schema24.sql")))
	original := bytes.Clone(raw)
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, raw)
	if err != nil {
		test.Fatal("schema24 selection projection unavailable")
	}
	defer projection.Destroy()
	view, err := projection.View()
	if err != nil || len(view.Repositories) != 1 || len(view.Skills) != 1 || len(view.Configurations) != 5 {
		test.Fatal("snapshot three-column projection missing")
	}
	if view.Repositories[0].Repository != "example/toolkit" || view.Repositories[0].Branch != "" || view.Repositories[0].SourceEnabled == nil || !*view.Repositories[0].SourceEnabled || view.Skills[0].Status != importer.SelectionNeedsBinding || view.Skills[0].Path != nil {
		test.Fatal("source flags, HEAD or explicit skill binding semantics drifted")
	}
	encoded, err := json.Marshal(view)
	if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) || !bytes.Equal(raw, original) {
		test.Fatal("source data escaped or caller snapshot mutated")
	}
	clone := projection.Clone()
	defer clone.Destroy()
	view.Repositories[0].Repository = "mutated"
	projection.Destroy()
	if _, err := projection.View(); !errors.Is(err, importer.ErrSelectionInvalid) {
		test.Fatal("destroyed projection remained readable")
	}
	cloned, err := clone.View()
	if err != nil || cloned.Repositories[0].Repository != "example/toolkit" {
		test.Fatal("owned clone retained mutable aliases")
	}
	for _, rendered := range []string{fmt.Sprint(clone), fmt.Sprintf("%#v", clone), fmt.Sprintf("%+v", clone)} {
		if strings.Contains(rendered, "example") {
			test.Fatal("opaque projection formatting leaked input")
		}
	}
	opaque, err := json.Marshal(clone)
	if err != nil || bytes.Contains(opaque, []byte("example")) {
		test.Fatal("opaque projection JSON leaked input")
	}
}

func TestSelectionSnapshotIsolation(test *testing.T) {
	statements := string(selectionFixture(test, "ccswitch-schema24.sql"))
	for _, suffix := range []string{
		`PRAGMA user_version=23;`,
		`DROP TABLE skills; CREATE VIEW skills AS SELECT 'x' id;`,
		`DROP TABLE skill_repos; CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT GENERATED ALWAYS AS ('HEAD') VIRTUAL, enabled INTEGER);`,
		`CREATE TRIGGER unsafe AFTER INSERT ON skills BEGIN SELECT 1; END;`,
		`INSERT INTO providers SELECT 'second-'||id,app_type,name,settings_config,meta,NULL,NULL,0,0,NULL,NULL,NULL,0,0 FROM providers; WITH RECURSIVE records(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM records WHERE value<33) INSERT INTO providers SELECT 'overflow-'||value,'claude','Overflow','{}','{}',NULL,NULL,0,0,NULL,NULL,NULL,0,0 FROM records;`,
	} {
		raw := ccSwitchSnapshot(test, statements+suffix)
		projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, raw)
		if err == nil {
			projection.Destroy()
			test.Fatal("unsafe or unbounded snapshot was accepted")
		}
		if strings.Contains(err.Error(), "SYNTHETIC-PRIVATE-MARKER") {
			test.Fatal("raw parser error escaped")
		}
	}
	raw := ccSwitchSnapshot(test, statements)
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(test.Context())
		wanted := context.Canceled
		if deadline {
			ctx, cancel = context.WithDeadline(test.Context(), time.Unix(0, 0))
			wanted = context.DeadlineExceeded
		}
		cancel()
		if _, err := importer.ProjectSelection(ctx, importer.SelectionCCSwitch24, raw); !errors.Is(err, wanted) {
			test.Fatal("context sentinel was normalized away")
		}
	}
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, raw)
	if err != nil {
		test.Fatal("synthetic snapshot unexpectedly rejected")
	}
	projection.Destroy()
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		test.Fatal("preview created filesystem artifacts")
	}
}

func TestSelectionSnapshotUnknownProviderColumn(test *testing.T) {
	statements := string(selectionFixture(test, "ccswitch-schema24.sql")) + `ALTER TABLE providers ADD COLUMN future_control TEXT; UPDATE providers SET future_control='SYNTHETIC-PRIVATE-MARKER';`
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, ccSwitchSnapshot(test, statements))
	if err != nil {
		test.Fatal("safe unsupported metadata projection unavailable")
	}
	defer projection.Destroy()
	view, _ := projection.View()
	for _, candidate := range view.Configurations {
		if candidate.Kind == "provider" && (candidate.Status != importer.SelectionUnsupportedField || candidate.Draft != nil) {
			test.Fatal("unknown provider column was silently stripped")
		}
	}
}

func TestSelectionAgentCapabilityAndAuxiliaryExclusions(test *testing.T) {
	statements := string(selectionFixture(test, "ccswitch-schema24.sql")) + `DELETE FROM providers;`
	for _, agent := range []string{"claude-desktop", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"} {
		statements += `INSERT INTO providers VALUES('` + agent + `','` + agent + `','Known source','{}','{}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`
	}
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, ccSwitchSnapshot(test, statements))
	if err != nil {
		test.Fatal("known unsupported agents disappeared from source preview")
	}
	defer projection.Destroy()
	view, _ := projection.View()
	if len(view.Configurations) != 13 {
		test.Fatal("known agents or auxiliary kinds disappeared")
	}
	for _, candidate := range view.Configurations {
		if candidate.Kind == "provider" && (candidate.Status != importer.SelectionUnsupportedAgent || candidate.Draft != nil || candidate.AgentID == "") {
			test.Fatal("unsupported agent was silently converted")
		}
	}
	for _, suffix := range []string{`UPDATE providers SET meta='{"unknown":"SYNTHETIC-PRIVATE-MARKER"}';`, `INSERT INTO provider_endpoints VALUES(1,'codex-source','codex','SYNTHETIC-PRIVATE-MARKER');`} {
		projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, ccSwitchSnapshot(test, string(selectionFixture(test, "ccswitch-schema24.sql"))+suffix))
		if err != nil {
			test.Fatal("unsupported metadata preview failed")
		}
		view, _ := projection.View()
		projection.Destroy()
		for _, candidate := range view.Configurations {
			if candidate.AgentID == "codex" && (candidate.Status != importer.SelectionUnsupportedField || candidate.Draft != nil) {
				test.Fatal("functional metadata/endpoint fallback silently stripped")
			}
		}
	}
}

func TestSelectionSnapshotDuplicateIdentity(test *testing.T) {
	for _, suffix := range []string{
		`INSERT INTO skill_repos SELECT owner,name,branch,enabled FROM skill_repos;`,
		`ALTER TABLE providers RENAME TO old_providers; CREATE TABLE providers AS SELECT * FROM old_providers; INSERT INTO providers SELECT * FROM old_providers; DROP TABLE old_providers;`,
		`ALTER TABLE skills RENAME TO old_skills; CREATE TABLE skills AS SELECT * FROM old_skills; INSERT INTO skills SELECT * FROM old_skills; DROP TABLE old_skills;`,
	} {
		projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, ccSwitchSnapshot(test, string(selectionFixture(test, "ccswitch-schema24.sql"))+suffix))
		if err == nil {
			projection.Destroy()
			test.Fatal("duplicate source identity silently collapsed into candidate")
		}
	}
}
