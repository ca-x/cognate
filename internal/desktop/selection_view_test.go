package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSelectionOrdinaryEntryAndExplicitLegacy(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 1000, 800)
	click(test, tester, "导入")
	if model.Page != 2 || !tester.HasText("选择导入") || !tester.HasText("整个文件可能含凭据") {
		test.Fatal("ordinary import front door or explicit file authorization missing")
	}
	click(test, tester, "旧版 Magpie（仅仓库）")
	if !tester.HasText("Magpie 导出路径") {
		test.Fatal("explicit legacy import is unreachable")
	}
}

func TestSelectionNativeKeyboardFrontDoor(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 920, 760)
	for range 16 {
		if tester.Focused("导入") {
			break
		}
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused("导入") {
		test.Fatal("ordinary Page2 front door not keyboard reachable")
	}
	tester.Key(0, ui.KeyEnter)
	if model.Page != 2 || !tester.HasText("选择导入") || model.selectionState().UploadApproved {
		test.Fatal("keyboard front door missing or preauthorized")
	}
}

func TestSelectionNativeControlsRealPreviewReviewAndCommit(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := modelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "导入")
	click(test, tester, "npx skills v3")
	click(test, tester, "导出文件绝对路径")
	tester.Type(selectionExport(test, 3))
	if fixture.count("PreviewImportSelection") != 0 || model.selectionState().UploadApproved {
		test.Fatal("path selection read or authorized a file")
	}
	click(test, tester, "授权整个文件上传到此 origin")
	click(test, tester, "预览文件")
	pump()
	tester.Frame()
	state := model.selectionState()
	if state.Preview == nil {
		test.Fatal("native real TLS preview failed", model.Status)
	}
	label := fmt.Sprintf("仓库 1 · %s @ %s · %s", state.View.Repositories[0].Hint.Repository, state.View.Repositories[0].Hint.Branch, selectionStatusLabel(1))
	scrollNativeShellControl(test, tester, label, nativePages[2].heading, 920)
	click(test, tester, label)
	for index := range state.Notices {
		label := fmt.Sprintf("确认提示 %d：%s", index+1, state.Notices[index].Label)
		scrollNativeShellControl(test, tester, label, nativePages[2].heading, 920)
		click(test, tester, label)
	}
	scrollNativeShellControl(test, tester, "审阅明确选择", nativePages[2].heading, 920)
	click(test, tester, "审阅明确选择")
	if state.Review == nil || state.FinalApproved {
		test.Fatal("native review missing or implicitly approved")
	}
	scrollNativeShellControl(test, tester, "我确认此审阅，提交所选私有资产与内容引用", nativePages[2].heading, 920)
	click(test, tester, "我确认此审阅，提交所选私有资产与内容引用")
	scrollNativeShellControl(test, tester, "明确提交一次", nativePages[2].heading, 920)
	click(test, tester, "明确提交一次")
	pump()
	tester.Frame()
	if state.Result.Phase != 3 || fixture.count("CommitImportSelection") != 1 || fixture.downloads.Load() != 1 {
		test.Fatal("native controls did not submit actual explicit repository", model.Status)
	}
}

func TestSelectionUnreadyConfigurationShowsCredentialPresence(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, _ := selectionLoadModel(test, fixture, 1)
	model.Page = 2
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("claude.api-key · 来源存在 true（不迁移）") {
		test.Fatal("needs-choice candidate hid credential presence bits")
	}
}

func TestSelectionNativeShortWindowThemesLocalesAndStatusReachable(test *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, locale := range []string{"zh-CN", "en-US"} {
			test.Run(fmt.Sprintf("dark%t-%s", dark, locale), func(test *testing.T) {
				model, _ := modelFixture(test, &Service{Home: test.TempDir()})
				model.Page = 2
				tester := ui.NewTester(model.View, 640, 540)
				tester.SetDark(dark)
				tester.SetLocale(locale)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true, HighContrast: dark, TextScale: 1.1})
				for _, label := range []string{"导出文件绝对路径", "授权整个文件上传到此 origin", "非秘密 operation ID（32位小写hex）", "只读查询状态（不上传文件）"} {
					scrollNativeShellControl(test, tester, label, nativePages[2].heading, 640)
				}
				if tester.FramePending() {
					test.Fatal("selection scheduled decorative motion")
				}
				if directory := os.Getenv("DESKTOP_EVIDENCE_DIR"); directory != "" {
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("selection-short-dark%t-%s.png", dark, locale)))
					if err != nil {
						test.Fatal(err)
					}
					encodeErr := png.Encode(file, tester.Image())
					closeErr := file.Close()
					if encodeErr != nil || closeErr != nil {
						test.Fatal("capture failed")
					}
				}
			})
		}
	}
}
