package importer

import (
	"os"
	"testing"
)

func TestCCSwitchAuthorizedLocalServicePreview(test *testing.T) {
	path := os.Getenv("COGNATE_TEST_CCSWITCH_DATABASE")
	if path == "" {
		test.Skip("explicitly authorized local database only")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		test.Fatal("authorized database unavailable")
	}
	defer clear(raw)
	fixture := newSelectionServiceFixture(test)
	call, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer)
	if err != nil {
		test.Fatal("preview admission unavailable")
	}
	defer call.Release()
	preview, err := fixture.service.Preview(call, SelectionCCSwitch24, raw, SelectionOptions{})
	if err != nil {
		test.Fatal("authorized database service preview rejected:", err)
	}
	defer preview.Destroy()
	view, err := preview.View()
	if err != nil || len(view.Repositories) == 0 {
		test.Fatal("authorized preview did not produce repository candidates")
	}
}
