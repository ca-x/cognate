package importer_test

import (
	"os"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
)

func TestCCSwitchSchema23Selection(test *testing.T) {
	raw := ccSwitchSnapshot(test, string(selectionFixture(test, "ccswitch-schema24.sql"))+`; PRAGMA user_version=23;`)
	defer clear(raw)
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, raw)
	if err != nil {
		test.Fatal("compatible schema23 snapshot rejected")
	}
	defer projection.Destroy()
}

func TestCCSwitchAuthorizedLocalDatabase(test *testing.T) {
	path := os.Getenv("COGNATE_TEST_CCSWITCH_DATABASE")
	if path == "" {
		test.Skip("explicitly authorized local database only")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		test.Fatal("authorized database unavailable")
	}
	defer clear(raw)
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, raw)
	if err != nil {
		test.Fatal("authorized database projection rejected:", err)
	}
	defer projection.Destroy()
	view, err := projection.View()
	if err != nil || len(view.Repositories) == 0 {
		test.Fatal("authorized database repository projection unavailable")
	}
	candidates, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{})
	if err != nil {
		test.Fatal("authorized database plugin preview rejected:", err)
	}
	defer candidates.Destroy()
}
