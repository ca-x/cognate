package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestConfigurationOrderEntryAvailable(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("资产排序") {
		test.Fatal("native general asset-order entry is missing")
	}
	click(test, tester, "资产排序")
	if model.Page != 6 || !tester.HasText("配置资产完整分区排序") {
		test.Fatal("native general asset-order page is missing")
	}
}

func TestConfigurationOrderEntryKeyboard(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 920, 760)
	for tabs := range 16 {
		if tester.Focused("资产排序") {
			test.Logf("order-entry-tabs=%d", tabs)
			tester.Key(0, ui.KeyEnter)
			if model.Page != 6 {
				test.Fatal("keyboard failed to open general ordering")
			}
			return
		}
		tester.Key(0, ui.KeyTab)
	}
	test.Fatal("general ordering entry was not keyboard reachable")
}

func configurationOrderFocus(test *testing.T, tester *ui.Tester, label string) {
	test.Helper()
	for range 20 {
		if tester.Focused(label) {
			return
		}
		tester.Key(0, ui.KeyTab)
	}
	test.Fatal("order control was not keyboard reachable")
}

func writeConfigurationOrderEvidence(test *testing.T, tester *ui.Tester, name string) {
	test.Helper()
	directory := os.Getenv("DESKTOP_CONFIGURATION_ORDER_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	file, err := os.Create(filepath.Join(directory, name+".png"))
	if err != nil {
		test.Fatal("create private UI evidence")
	}
	defer file.Close()
	if err := png.Encode(file, tester.Image()); err != nil {
		test.Fatal("encode private UI evidence")
	}
}

func TestConfigurationOrderViewMouseTLSReview(test *testing.T) {
	fixture := newConfigurationFixture(test)
	first := configurationOrderSeed(test, fixture, fixture.owner, "claude", "mcp", "First metadata")
	second := configurationOrderSeed(test, fixture, fixture.owner, "claude", "mcp", "Second metadata")
	configurationOrderSeed(test, fixture, fixture.owner, "gemini", "prompt", "Other partition")
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	model.Page = 6
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "重新读取 / 核实排序")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	state := model.orderState()
	if !state.Loaded || len(state.Partitions) != 2 || state.Snapshot.Kind != "mcp" {
		test.Fatal("native metadata partition discovery failed")
	}
	click(test, tester, "1. First metadata")
	click(test, tester, "下移所选资产")
	if state.Selected != 1 || state.Draft[0].ID != second.ID || state.Draft[1].ID != first.ID {
		test.Fatal("mouse movement lost selection or complete order")
	}
	click(test, tester, "审阅完整排序")
	if state.Review == nil || !tester.HasText("完整排序审阅") || !tester.HasText("只提交本分区完整排列；不会保存内容草稿、切换 provider 或应用设备。") {
		test.Fatal("native complete order review missing")
	}
	writeConfigurationOrderEvidence(test, tester, "mouse-review")
	click(test, tester, "本次排序确认")
	tester.Type(state.Review.Confirmation)
	click(test, tester, "确认并提交一次排序")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Blocked || state.dirty() || fixture.count("ReplaceAssetOrder") != 1 {
		test.Fatal("native exact confirmation did not submit once")
	}
	click(test, tester, state.Partition)
	click(test, tester, "gemini · prompt")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Snapshot.AgentID != "gemini" || state.Snapshot.Kind != "prompt" || len(state.Draft) != 1 || fixture.count("ReplaceAssetOrder") != 1 {
		test.Fatal("native partition selector did not load independent full partition")
	}
	configurationOrderNoContentCalls(test, fixture)
	writeConfigurationOrderEvidence(test, tester, "mouse-partition")
}

func TestConfigurationOrderViewKeyboardComplete250(test *testing.T) {
	fixture := newConfigurationFixture(test)
	for index := range 250 {
		configurationOrderSeed(test, fixture, fixture.owner, "opencode", "provider", fmt.Sprintf("Asset %03d", index))
	}
	model, _ := configurationOrderLoadModel(test, fixture)
	state := model.orderState()
	last := state.Draft[249].ID
	for _, dark := range []bool{false, true} {
		tester := ui.NewTester(model.View, 640, 600)
		tester.SetDark(dark)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1.25})
		writeConfigurationOrderEvidence(test, tester, fmt.Sprintf("keyboard-250-initial-dark-%t", dark))
		configurationOrderFocus(test, tester, "完整资产顺序")
		tester.Key(0, ui.KeyEnd)
		if state.Selected != 249 {
			test.Fatalf("native list End could not reach full partition final member: selected=%d", state.Selected)
		}
		configurationOrderFocus(test, tester, "上移所选资产")
		tester.Key(0, ui.KeyEnter)
		if state.Selected != 248 || state.Draft[248].ID != last || !state.dirty() {
			test.Fatal("keyboard upward move lost selected asset")
		}
		configurationOrderFocus(test, tester, "审阅完整排序")
		tester.Key(0, ui.KeyEnter)
		if state.Review == nil || len(state.Review.IDs) != 250 {
			test.Fatal("keyboard review truncated the full 250 member permutation")
		}
		configurationOrderFocus(test, tester, "本次排序确认")
		assertNativeShellVisible(test, tester, "本次排序确认", 640, 600)
		configurationOrderFocus(test, tester, "取消排序审阅")
		assertNativeShellVisible(test, tester, "取消排序审阅", 640, 600)
		writeConfigurationOrderEvidence(test, tester, fmt.Sprintf("keyboard-250-dark-%t", dark))
		tester.Key(0, ui.KeyEscape)
		if state.Review != nil || !state.dirty() || fixture.count("ReplaceAssetOrder") != 0 {
			test.Fatal("Escape approved or discarded unsaved order")
		}
		state.Draft[248], state.Draft[249] = state.Draft[249], state.Draft[248]
		state.Selected = -1
	}
	configurationOrderNoContentCalls(test, fixture)
}

func TestConfigurationOrderViewPreferencesAndDirtyNavigation(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 4
	form := &configurationForm{Dirty: true}
	model.configurationState().Form = form
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "资产排序")
	if model.Page != 4 || !form.Dirty || !tester.HasText("放弃未保存草稿？") {
		test.Fatal("order navigation bypassed original content draft guard")
	}
	click(test, tester, "保留草稿")
	if model.Page != 4 || !form.Dirty {
		test.Fatal("order navigation cleared unsaved content")
	}
	model.Page = 6
	for _, dimensions := range []struct{ width, height int }{{420, 600}, {640, 600}, {920, 760}} {
		tester := ui.NewTester(model.View, dimensions.width, dimensions.height)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true, HighContrast: true})
		assertNativeShellVisible(test, tester, "资产排序", dimensions.width, dimensions.height)
		assertNativeShellVisible(test, tester, "取消任务", dimensions.width, dimensions.height)
		if tester.FramePending() || strings.Contains(model.Status, "synthetic-order-body-secret") {
			test.Fatal("order page added decorative animation or secret feedback")
		}
		writeConfigurationOrderEvidence(test, tester, fmt.Sprintf("empty-%dx%d", dimensions.width, dimensions.height))
	}
}
