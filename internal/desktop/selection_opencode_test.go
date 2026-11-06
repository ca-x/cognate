package desktop

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSelectionOpenCodeNativeCompleteReview(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := modelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	state := model.selectionState()
	state.Source, state.UploadApproved = 1, true
	state.Path = selectionExport(test, 1, `DELETE FROM providers; INSERT INTO providers VALUES('work','opencode','OpenCode imported','{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://opencode.example/v1","apiKey":"SYNTHETIC-PRIVATE-MARKER","headers":{"Authorization":"SYNTHETIC-PRIVATE-MARKER"}},"models":{"a":{"name":"Model A","limit":{"context":9007199254740991,"output":4096},"modalities":{"input":["text","image"],"output":["text"]}}}}','{}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`)
	model.PreviewSelection()
	pump()
	if state.Preview == nil || state.View == nil || len(state.View.Configurations) != 3 || state.View.Configurations[0].KnownAgentId != "opencode" {
		test.Fatal("native OpenCode source preview unavailable", model.Status)
	}
	fields := []string{"work", "@ai-sdk/openai-compatible", "https://opencode.example/v1", "Authorization", "opencode.value.01", "Model A", "9007199254740991", "4096", "image", "text"}
	check := func(lines []string) {
		test.Helper()
		text := strings.Join(lines, "\n")
		for _, field := range fields {
			if !strings.Contains(text, field) {
				test.Fatalf("native review omitted %s", field)
			}
		}
		if strings.Contains(text, "SYNTHETIC-PRIVATE-MARKER") {
			test.Fatal("source secret appeared in native review")
		}
	}
	check(selectionDraftSummary(state.View.Configurations[0]))
	model.ChooseSelection(2, 0, true)
	for index := range state.Notices {
		state.Notices[index].Checked = true
	}
	model.ReviewSelection()
	if state.Review == nil || state.FinalApproved || fixture.count("CommitImportSelection") != 0 {
		test.Fatal("native confirmation unavailable or preapproved")
	}
	check(state.Review.Lines)
	model.Page = 2
	tester := ui.NewTester(model.View, 1000, 900)
	if !tester.HasText("provider ID: work") || !tester.HasText("limit.context: 9007199254740991") {
		test.Fatal("native final review did not render provider fields")
	}
	for range 24 {
		bounds, found := tester.Find("最终审阅")
		if found && bounds.Y >= 210 && bounds.Y < 430 {
			break
		}
		delta := float32(180)
		if found && bounds.Y < 210 {
			delta = -180
		}
		tester.Scroll(500, 550, 0, delta)
		tester.Frame()
	}
	if directory := os.Getenv("DESKTOP_EVIDENCE_DIR"); directory != "" {
		file, err := os.OpenFile(filepath.Join(directory, "opencode-import-native-review.png"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			test.Fatal(err)
		}
		encodeErr := png.Encode(file, tester.Image())
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			test.Fatal("native screenshot failed")
		}
	}
	scrollNativeShellControl(test, tester, "我确认此审阅，提交所选私有资产与内容引用", nativePages[2].heading, 1000)
	click(test, tester, "我确认此审阅，提交所选私有资产与内容引用")
	scrollNativeShellControl(test, tester, "明确提交一次", nativePages[2].heading, 1000)
	click(test, tester, "明确提交一次")
	pump()
	if state.Result.Phase != 3 || state.Result.Configurations != 1 || fixture.count("CommitImportSelection") != 1 {
		test.Fatal("native single configuration commit failed", model.Status)
	}
}
