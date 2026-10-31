package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/assets/branding"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/state"
	"github.com/egoist/mygo/ui"
)

func TestNativeShellLayout(test *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{420, 640, 920, 1280} {
			for page, definition := range nativePages {
				name := fmt.Sprintf("dark-%t-width-%d-page-%d", dark, width, page)
				test.Run(name, func(test *testing.T) {
					model, _ := modelFixture(test, &Service{Home: test.TempDir()})
					model.Page = page
					logo, err := ui.DecodeBitmap(clientlogoassets.ClientPNG)
					if err != nil {
						test.Fatal(err)
					}
					model.Logo = logo
					tester := ui.NewTester(model.View, width, 600)
					tester.SetDark(dark)
					for _, label := range []string{definition.heading, "服务地址", "未登录 · 公开访问", "无前台任务", "取消任务", model.Status} {
						assertNativeShellVisible(test, tester, label, width, 600)
					}
					for _, navigation := range nativePages {
						assertNativeShellVisible(test, tester, navigation.label, width, 600)
					}
					if directory := os.Getenv("DESKTOP_NATIVE_SHELL_EVIDENCE_DIR"); directory != "" {
						file, err := os.Create(filepath.Join(directory, name+".png"))
						if err != nil {
							test.Fatal(err)
						}
						if err := png.Encode(file, tester.Image()); err != nil {
							_ = file.Close()
							test.Fatal(err)
						}
						if err := file.Close(); err != nil {
							test.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func assertNativeShellVisible(test *testing.T, tester *ui.Tester, label string, width, height int) {
	test.Helper()
	bounds, found := tester.Find(label)
	if !found || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > float32(height) {
		test.Fatalf("%q is outside %dx%d: %+v, found %t", label, width, height, bounds, found)
	}
}

func TestNativeShellKeyboardNavigation(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	status := model.Status
	tester := ui.NewTester(model.View, 640, 600)
	for range 12 {
		if tester.Focused("Grok Build") {
			break
		}
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused("Grok Build") {
		test.Fatal("Grok navigation is not keyboard reachable")
	}
	tester.Key(0, ui.KeyEnter)
	if model.Page != 5 || model.Status != status || !tester.HasText("Grok Build 资产") {
		test.Fatal("keyboard navigation changed status or missed its page")
	}
	if tester.FramePending() {
		test.Fatal("keyboard navigation scheduled decorative animation")
	}
	tester.Key(0, ui.KeyTab)
	if !tester.HasText("Grok Build 资产") {
		test.Fatal("selected page disappeared after focus moved")
	}
	tester.SetSize(420, 600)
	assertNativeShellVisible(test, tester, "Grok Build", 420, 600)
	assertNativeShellVisible(test, tester, "取消任务", 420, 600)
}

func TestNativeShellNavigationKeepsDraftGuard(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 4
	state := model.configurationState()
	state.Form = &configurationForm{Dirty: true}
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "Grok Build")
	if model.Page != 4 || state.PendingPage != 5 || !state.Form.Dirty {
		test.Fatal("shell bypassed the existing dirty-draft navigation guard")
	}
	if !tester.HasText("放弃未保存草稿？") {
		test.Fatal("navigation did not present the existing discard prompt")
	}
}

func TestNativeShellIdentityAndStatus(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	model.Username = "synthetic-owner"
	if strings.Contains(model.nativeIdentity(), "synthetic-owner") {
		test.Fatal("editable username was presented as an authenticated identity")
	}
	model.session.set("synthetic-private-session")
	if model.nativeIdentity() != "已登录 · synthetic-owner" {
		test.Fatal("authenticated account identity missing")
	}
	model.Status = "Unknown：请显式重新载入核实，不自动重放"
	model.WatchStatus = "后台同步：等待排空"
	model.WatchActive = true
	model.WatchStopping = true
	watchContext, cancel := context.WithCancel(test.Context())
	model.watch = &watchRun{ctx: watchContext, cancel: cancel}
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText(model.Status) || !tester.HasText(model.WatchStatus) || !tester.HasText("后台同步停止中") || tester.HasText("synthetic-private-session") {
		test.Fatal("shell lost the authoritative status or exposed a session")
	}
	model.Busy = true
	tester.Frame()
	if !tester.HasText("任务进行中") || !tester.HasText(model.Status) {
		test.Fatal("foreground feedback replaced the authoritative result")
	}
	tester.Advance(200 * time.Millisecond)
	model.session.clear()
	tester.Frame()
	if tester.HasText("已登录 · synthetic-owner") || !tester.HasText("未登录 · 公开访问") {
		test.Fatal("cleared session retained the authenticated identity")
	}
}

func TestNativeShellPreferences(test *testing.T) {
	for _, dark := range []bool{false, true} {
		base := ui.LightTheme()
		if dark {
			base = ui.DarkTheme()
		}
		base.FontSize = 21
		base.Accent = ui.Hex("#7c3aed")
		original := *base
		theme, _ := nativeShellTheme(base, false)
		if *base != original || theme.FontSize != 21 || theme.Accent != base.Accent || theme.Focus != base.Focus || theme.Dark != dark {
			test.Fatal("shell mutated or discarded native preference tokens")
		}
		contrast, panel := nativeShellTheme(base, true)
		if contrast != *base || panel != base.Background {
			test.Fatal("high-contrast theme was overridden")
		}
	}
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 920, 760)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true, HighContrast: true, TextScale: 1.25})
	model.Busy = true
	tester.Frame()
	if tester.FramePending() || !tester.HasText("任务进行中") {
		test.Fatal("reduced-motion feedback was delayed or animated")
	}
}

func TestNativeShellThemeChangesDoNotAnimate(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	model.Busy = true
	model.Page = 5
	tester := ui.NewTester(model.View, 640, 600)
	for range 12 {
		if tester.Focused("取消任务") {
			break
		}
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused("取消任务") {
		test.Fatal("cancel is not keyboard reachable")
	}
	for _, dark := range []bool{true, false, true} {
		tester.SetDark(dark)
		if !tester.Focused("取消任务") {
			test.Fatal("appearance change lost the task cancellation focus")
		}
		if tester.FramePending() {
			test.Fatal("appearance change animated the status surface independently of its text")
		}
		pixel := tester.Image().RGBAAt(30, 570)
		if dark && pixel.R > 100 || !dark && pixel.R < 200 {
			test.Fatalf("appearance change left the wrong status surface: dark %t, pixel %+v", dark, pixel)
		}
	}
}

func TestNativeShellTaskFeedbackTransition(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 920, 760)
	status := model.Status
	model.Busy = true
	tester.Frame()
	if !tester.HasText("任务进行中") || !tester.HasText(status) || !tester.FramePending() {
		test.Fatal("task feedback did not update immediately with a native color transition")
	}
	tester.Advance(200 * time.Millisecond)
	if tester.FramePending() {
		test.Fatal("task feedback continued scheduling frames after its short transition")
	}
}

func TestNativeShellRestoreViewport(test *testing.T) {
	_, model, queue := configurationApplyModel(test, agentconfig.ClaudeNativeLogin)
	state := model.configurationState()
	model.ApplyConfiguration()
	configurationPump(test, model, queue, func() bool { return state.Prompt != nil || !model.Busy })
	model.ReplyConfigurationApproval(true)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester := ui.NewTester(model.View, 920, 760)
	configurationClick(test, tester, "预览并明确批准恢复…")
	configurationPump(test, model, queue, func() bool { return state.Prompt != nil || !model.Busy })
	tester.Frame()
	if !tester.Focused("拒绝本次操作") {
		test.Fatalf("restore was not reached: prompt %t, busy %t, status %q", state.Prompt != nil, model.Busy, model.Status)
	}
}

func TestNativeShellPackShortWindowControls(test *testing.T) {
	for _, dimensions := range []struct {
		width, height int
		scale         float32
	}{{640, 540, 1}, {920, 600, 1.25}, {640, 600, 1.25}, {420, 600, 1}} {
		for _, dark := range []bool{false, true} {
			for _, keyboard := range []bool{false, true} {
				name := fmt.Sprintf("%dx%d-scale-%.2f-dark-%t-keyboard-%t", dimensions.width, dimensions.height, dimensions.scale, dark, keyboard)
				test.Run(name, func(test *testing.T) {
					model, _ := modelFixture(test, &Service{Home: test.TempDir()})
					model.Page = 1
					watchContext, cancel := context.WithCancel(test.Context())
					model.watch = &watchRun{ctx: watchContext, cancel: cancel}
					model.WatchActive = true
					tester := ui.NewTester(model.View, dimensions.width, dimensions.height)
					tester.SetDark(dark)
					tester.SetPreferences(ui.Preferences{TextScale: dimensions.scale, ReduceMotion: true})
					writeNativeShellScrollEvidence(test, tester, name+"-initial")
					if keyboard {
						for range 24 {
							if tester.Focused("停止后台同步") {
								break
							}
							tester.Key(0, ui.KeyTab)
						}
						if !tester.Focused("停止后台同步") || !nativeShellControlVisible(tester, "停止后台同步", nativePages[1].heading) {
							test.Fatal("Tab did not reveal the stop control above the fixed footer")
						}
					} else {
						for _, label := range []string{"同步并应用安装变更", "启用后台同步", "停止后台同步"} {
							scrollNativeShellControl(test, tester, label, nativePages[1].heading, dimensions.width)
						}
					}
					assertNativeShellVisible(test, tester, "取消任务", dimensions.width, dimensions.height)
					writeNativeShellScrollEvidence(test, tester, name+"-stop-visible")
					if keyboard {
						tester.Key(0, ui.KeyEnter)
					} else {
						click(test, tester, "停止后台同步")
					}
					if watchContext.Err() == nil || !model.WatchStopping || model.WatchActive {
						test.Fatal("visible stop control did not invoke the existing StopWatch action")
					}
				})
			}
		}
	}
}

func nativeShellControlVisible(tester *ui.Tester, label, heading string) bool {
	bounds, found := tester.Find(label)
	footer, footerFound := tester.Find("取消任务")
	title, titleFound := tester.Find(heading)
	reference, referenceFound := tester.Find("账号")
	return found && footerFound && titleFound && referenceFound && bounds.W > 0 && bounds.H >= reference.H-0.5 && bounds.Y >= title.Y+title.H && bounds.Y+bounds.H <= footer.Y-8
}

func TestNativeShellPackShortWindowListNavigation(test *testing.T) {
	model, _ := modelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 1
	for index := range state.MaxSubscriptions {
		model.Subscriptions = append(model.Subscriptions, state.Subscription{ID: fmt.Sprintf("fixture-%d", index), Name: fmt.Sprintf("Fixture pack %03d", index), Server: "https://fixture.invalid"})
	}
	tester := ui.NewTester(model.View, 640, 540)
	scrollNativeShellControl(test, tester, "Fixture pack 000", nativePages[1].heading, 640)
	click(test, tester, "Fixture pack 000")
	if model.PackSelected != 0 {
		test.Fatal("scrolling the page lost native list pointer selection")
	}
	tester.Key(0, ui.KeyEnd)
	if model.PackSelected != len(model.Subscriptions)-1 {
		test.Fatal("native list End did not select the final subscription")
	}
	scrollNativeShellControl(test, tester, fmt.Sprintf("Fixture pack %03d", len(model.Subscriptions)-1), nativePages[1].heading, 640)
	writeNativeShellScrollEvidence(test, tester, "pack-short-window-list-end")
}

func scrollNativeShellControl(test *testing.T, tester *ui.Tester, label, heading string, width int) {
	test.Helper()
	for range 24 {
		if nativeShellControlVisible(tester, label, heading) {
			return
		}
		title, _ := tester.Find(heading)
		footer, _ := tester.Find("取消任务")
		bounds, _ := tester.Find(label)
		delta := float32(80)
		if bounds.Y < title.Y+title.H {
			delta = -80
		}
		tester.Scroll(float32(width)/2, (title.Y+title.H+footer.Y)/2, 0, delta)
		tester.Frame()
	}
	bounds, _ := tester.Find(label)
	footer, _ := tester.Find("取消任务")
	test.Fatalf("%q is not reachable above the fixed footer after scrolling: control %+v, footer %+v", label, bounds, footer)
}

func writeNativeShellScrollEvidence(test *testing.T, tester *ui.Tester, name string) {
	test.Helper()
	directory := os.Getenv("DESKTOP_NATIVE_SHELL_SCROLL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	file, err := os.Create(filepath.Join(directory, name+".png"))
	if err != nil {
		test.Fatal(err)
	}
	if err := png.Encode(file, tester.Image()); err != nil {
		_ = file.Close()
		test.Fatal(err)
	}
	if err := file.Close(); err != nil {
		test.Fatal(err)
	}
}
