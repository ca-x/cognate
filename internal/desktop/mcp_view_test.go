package desktop

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/mcpclient"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/egoist/mygo/ui"
)

func TestMCPNativeWorkspaceEntry(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 4
	tester := ui.NewTester(model.View, 720, 540)
	if !tester.HasText("MCP 连接配置") {
		test.Fatal("AI configuration workspace has no native MCP connection editor entry")
	}
}

func TestMCPNativeWorkspaceKeyboardEntry(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 1280, 684)
	for _, target := range []string{"AI 配置", "MCP 连接配置"} {
		steps := 0
		for ; steps < 40 && !tester.Focused(target); steps++ {
			tester.Key(0, ui.KeyTab)
		}
		if !tester.Focused(target) {
			test.Fatal("workspace keyboard target unreachable", target)
		}
		test.Logf("keyboard target=%s tabs=%d", target, steps)
		tester.Key(0, ui.KeyEnter)
	}
	if model.Page != 4 || !model.configurationState().MCPActive {
		test.Fatal("keyboard entry did not reach MCP")
	}
}

func mcpClick(test *testing.T, tester *ui.Tester, areaLabel, label string) {
	test.Helper()
	for range 60 {
		area, found := tester.Find(areaLabel)
		if !found || area.H < 24 {
			test.Fatalf("scroll area missing or too short: %s %+v", areaLabel, area)
		}
		box, found := tester.Find(label)
		if !found {
			test.Fatalf("native MCP component missing: %s", label)
		}
		if box.W > 0 && box.H > 0 && box.Y >= area.Y && box.Y+box.H <= area.Y+area.H+0.5 {
			click(test, tester, label)
			return
		}
		delta := min(float32(120), area.H/2)
		if box.Y <= area.Y {
			delta = -delta
		}
		tester.Scroll(area.X+area.W-14, area.Y+area.H/2, 0, delta)
		tester.Frame()
	}
	mcpScreenshot(test, tester, "unreachable")
	area, _ := tester.Find(areaLabel)
	box, _ := tester.Find(label)
	test.Fatalf("native MCP component not scroll-reachable: %s area=%+v target=%+v", label, area, box)
}

func mcpScreenshot(test *testing.T, tester *ui.Tester, name string) {
	test.Helper()
	if root := os.Getenv("DESKTOP_EVIDENCE_DIR"); root != "" {
		file, err := os.Create(filepath.Join(root, "mcp-"+name+".png"))
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
}

func TestMCPNativeTypedCreateEvents(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	model, queue := mcpLoadController(test, fixture)
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "新建 MCP 连接配置")
	mcpClick(test, tester, "MCP editor area", "资产名称")
	tester.Type("Typed native MCP")
	mcpClick(test, tester, "MCP editor area", "添加 stdio 服务")
	for _, field := range []struct{ label, value string }{{"服务 ID（显式、唯一）", "tools"}, {"服务名称", "tools"}, {"命令（仅声明，不执行）", "never-executed"}} {
		mcpClick(test, tester, "MCP editor area", field.label)
		tester.Type(field.value)
	}
	mcpClick(test, tester, "MCP editor area", "启用此服务声明")
	mcpClick(test, tester, "MCP editor area", "设置 args 集合（含空集合）")
	mcpClick(test, tester, "MCP editor area", "args · 添加项")
	mcpClick(test, tester, "MCP editor area", "参数 ID（不是参数值）")
	tester.Type("first")
	mcpClick(test, tester, "MCP editor area", "args · 添加项")
	mcpClick(test, tester, "MCP editor area", "参数 ID（不是参数值）")
	tester.Type("second")
	mcpClick(test, tester, "MCP editor area", "args · 项上移")
	mcpClick(test, tester, "MCP editor area", "env · 设置集合（含空集合）")
	state := model.mcpState()
	if state.Form.Draft.Servers[0].Stdio.Args.Values[0].ID != "second" || state.Form.Draft.Servers[0].Stdio.Env == nil || !state.Form.Draft.Servers[0].Enabled {
		test.Fatal("typed order/presence/enabled events were lost")
	}
	mcpClick(test, tester, "MCP editor area", "arg/tools/first · present=false")
	mcpClick(test, tester, "MCP editor area", "REPLACE")
	mcpClick(test, tester, "MCP editor area", "新值（仅内存，可为空）")
	tester.Type("synthetic-native-secret")
	mcpClick(test, tester, "MCP editor area", "审阅服务端保存…")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Review == nil || len(state.Review.Slots) != 1 || fixture.commits(test, "create") != 0 {
		test.Fatal("native review missing or wrote", model.Status)
	}
	if tester.HasText("synthetic-native-secret") || !tester.HasText("never-executed") {
		test.Fatal("review omitted declaration or disclosed replacement")
	}
	mcpClick(test, tester, "MCP review content", "仅批准本次槽操作 "+state.Review.Slots[0])
	click(test, tester, state.Review.Confirmation)
	tester.Type(state.Review.Confirmation)
	click(test, tester, "确认且仅提交一次")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if fixture.commits(test, "create") != 1 || fixture.count("CreateAsset") != 1 || !state.Blocked {
		test.Fatal("native create did not submit once", model.Status)
	}
	view, err := state.session().List(test.Context())
	if err != nil || len(view) != 1 {
		test.Fatal(err)
	}
	read, err := state.session().Read(test.Context(), view[0].ID)
	if err != nil || read.Draft.Servers[0].Stdio.Args.Values[0].ID != "second" || read.Draft.Servers[0].Stdio.Env == nil || !read.Secrets[0].Present && !read.Secrets[1].Present {
		test.Fatal("native declaration/secret not stored", err)
	}
}

func TestMCPNativeReviewEscapeAndShortWindow(test *testing.T) {
	for _, english := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			test.Run(fmt.Sprintf("english=%t/dark=%t", english, dark), func(test *testing.T) {
				fixture := newMCPDesktopFixture(test)
				model, queue := mcpLoadController(test, fixture)
				model.configurationState().MCPEnglish = english
				mcpControllerForm(test, model)
				tester := ui.NewTester(model.View, 720, 540)
				tester.SetDark(dark)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true, HighContrast: dark})
				mcpClick(test, tester, "MCP editor area", model.mcpText("审阅服务端保存…", "Review server save…"))
				configurationPump(test, model, queue, func() bool { return !model.Busy })
				tester.Frame()
				state := model.mcpState()
				if state.Review == nil {
					test.Fatal("short window did not prepare", model.Status)
				}
				for _, label := range []string{state.Review.Confirmation, model.mcpText("取消本次审阅", "Cancel this review"), model.mcpText("确认且仅提交一次", "Confirm and submit once")} {
					box, found := tester.Find(label)
					if !found || box.Y < 0 || box.Y+box.H > 540 || box.X < 0 || box.X+box.W > 720 {
						test.Fatalf("short modal control unreachable: %s %+v", label, box)
					}
				}
				for range 12 {
					tester.Key(0, ui.KeyTab)
					if tester.Focused("服务地址") || tester.Focused("账号") || tester.Focused("English") {
						test.Fatal("review Tab escaped modal")
					}
				}
				mcpScreenshot(test, tester, fmt.Sprintf("review-en%t-dark%t-short", english, dark))
				tester.Key(0, ui.KeyEscape)
				if state.Review != nil || state.session().private().pending != nil || fixture.count("CreateAsset") != 0 {
					test.Fatal("Escape left actual submission capability")
				}
			})
		}
	}
}

func TestMCPNativeDirtyWorkspaceAndNavigation(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 4
	model.configurationState().MCPActive = true
	state := model.mcpState()
	state.Form = newMCPForm(mcpclient.EditView{Draft: mcpDesktopDraft()})
	state.Form.Dirty = true
	tester := ui.NewTester(model.View, 720, 540)
	click(test, tester, "Claude provider")
	if !tester.HasText("放弃未保存的连接草稿？") || !model.configurationState().MCPActive {
		test.Fatal("workspace switch discarded dirty draft")
	}
	tester.Key(0, ui.KeyEscape)
	if state.PendingWorkspace >= 0 || !model.configurationState().MCPActive {
		test.Fatal("Escape switched dirty workspace")
	}
	click(test, tester, "账号")
	click(test, tester, "保留草稿")
	if model.Page != 4 || !state.Form.Dirty {
		test.Fatal("keep draft lost content")
	}
	click(test, tester, "账号")
	click(test, tester, "放弃并继续")
	if model.Page != 3 {
		test.Fatal("explicit discard did not navigate")
	}
}

func TestMCPNativePasswordAndSlotUndoIsolation(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 4
	model.configurationState().MCPActive = true
	state := model.mcpState()
	state.Form = newMCPForm(mcpclient.EditView{Draft: mcpDesktopDraft()})
	if _, err := state.Form.syncSlots(); err != nil {
		test.Fatal(err)
	}
	for _, slot := range state.Form.Slots {
		slot.Action, slot.ValueSet = mcpclient.Replace, true
	}
	tester := ui.NewTester(model.View, 920, 760)
	mcpClick(test, tester, "MCP editor area", "新值（仅内存，可为空）")
	tester.Type(strings.Repeat("M", 24))
	before := bytes.Clone(tester.Image().Pix)
	state.Form.Slots[0].Value.set(strings.Repeat("Z", 24))
	tester.Frame()
	if !bytes.Equal(before, tester.Image().Pix) {
		test.Fatal("same-length credential altered password pixels")
	}
	tester.Key(ui.Ctrl, ui.KeyA)
	tester.Type("synthetic-another-secret")
	state.SlotIndex = 1
	tester.Frame()
	mcpClick(test, tester, "MCP editor area", "新值（仅内存，可为空）")
	tester.Key(ui.Ctrl, ui.KeyZ)
	if state.Form.Slots[1].Value.value() != "" {
		test.Fatal("a different credential slot recovered the previous input undo history")
	}
	tester.Type("synthetic-third-secret")
	tester.Key(ui.Ctrl, ui.KeyA)
	tester.Type("synthetic-fourth-secret")
	state.Form.clearSecrets()
	tester.Frame()
	mcpClick(test, tester, "MCP editor area", "新值（仅内存，可为空）")
	tester.Key(ui.Ctrl, ui.KeyZ)
	if state.Form.Slots[1].Value.value() != "" || state.Form.Slots[1].ValueSet {
		test.Fatal("clear allowed Undo to recover a credential")
	}
}

func TestMCPNativeTransportAndCollectionEvents(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	assets := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, assets)
	model, queue := mcpLoadController(test, fixture)
	model.mcpAction(created.ID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state := model.mcpState()
	tester := ui.NewTester(model.View, 920, 760)
	mcpClick(test, tester, "MCP editor area", "服务下移")
	if state.Form.Selected != 1 || state.Form.Draft.Servers[0].ID != "remote" {
		test.Fatal("server order down event failed")
	}
	mcpClick(test, tester, "MCP editor area", "服务上移")
	mcpClick(test, tester, "MCP editor area", "env · 添加项")
	mcpClick(test, tester, "MCP editor area", "env 名称（不是值）")
	tester.Type("TOKEN")
	if !tester.Focused("env 名称（不是值）") || state.Form.Draft.Servers[0].Stdio.Env.Values[0].Name != "TOKEN" {
		mcpScreenshot(test, tester, "env-focus-diagnostic")
		test.Fatalf("env typed focus=%t server=%d values=%v", tester.Focused("env 名称（不是值）"), state.Form.Selected, state.Form.Draft.Servers[0].Stdio.Env.Values)
	}
	mcpClick(test, tester, "MCP editor area", "env · 添加项")
	mcpClick(test, tester, "MCP editor area", "env 名称（不是值）")
	tester.Type("SECOND")
	mcpClick(test, tester, "MCP editor area", "env · 项上移")
	if state.Form.Draft.Servers[0].Stdio.Env.Values[0].Name != "SECOND" {
		test.Fatalf("env ordering lost: names=%v selection=%d", state.Form.Draft.Servers[0].Stdio.Env.Values, state.ParameterIndices[1])
	}
	mcpClick(test, tester, "MCP editor area", "env · 移除项")
	mcpClick(test, tester, "MCP editor area", "HTTP")
	mcpClick(test, tester, "MCP editor area", "URL（不支持 query / 用户凭据 / fragment）")
	tester.Type("https://declaration.invalid/mcp")
	mcpClick(test, tester, "MCP editor area", "headers · 设置集合（含空集合）")
	mcpClick(test, tester, "MCP editor area", "headers · 添加项")
	mcpClick(test, tester, "MCP editor area", "headers 名称（不是值）")
	tester.Type("Authorization")
	mcpClick(test, tester, "MCP editor area", "headers · 添加项")
	mcpClick(test, tester, "MCP editor area", "headers 名称（不是值）")
	tester.Type("X-Trace")
	mcpClick(test, tester, "MCP editor area", "headers · 项上移")
	if state.Form.Draft.Servers[0].HTTP.Headers.Values[0].Name != "X-Trace" {
		test.Fatal("header ordering lost")
	}
	mcpClick(test, tester, "MCP editor area", "SSE")
	mcpClick(test, tester, "MCP editor area", "URL（不支持 query / 用户凭据 / fragment）")
	tester.Type("https://declaration.invalid/events")
	server := state.Form.Draft.Servers[0]
	if server.Stdio != nil || server.HTTP != nil || server.SSE == nil || server.SSE.Headers != nil {
		test.Fatal("explicit SSE transport or absent headers lost")
	}
	if !state.Form.Slots[0].Previous || state.Form.Slots[0].Ref != "arg/tools/first" {
		test.Fatal("transport switch forgot old ref requiring CLEAR")
	}
	mcpClick(test, tester, "MCP editor area", "arg/tools/first · present=true")
	mcpClick(test, tester, "MCP editor area", "CLEAR")
	mcpClick(test, tester, "MCP editor area", "明确清除此服务端凭据槽")
	mcpClick(test, tester, "MCP editor area", "审阅服务端保存…")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Review == nil || len(state.Review.Slots) != 1 {
		test.Fatal("typed transport update did not prepare confirmed old-ref CLEAR", model.Status)
	}
	mcpNativeSubmit(test, tester, model, queue)
	view, err := assets.Read(test.Context(), created.ID)
	if err != nil || view.Metadata.Revision != 2 || view.Draft.Servers[0].SSE == nil || view.Draft.Servers[0].SSE.URL != "https://declaration.invalid/events" || len(view.Secrets) != 1 || fixture.commits(test, "update") != 1 {
		test.Fatal("typed SSE/explicit CLEAR not stored", err)
	}
}

func mcpNativeSubmit(test *testing.T, tester *ui.Tester, model *Model, queue chan func()) {
	test.Helper()
	state := model.mcpState()
	if state.Review == nil {
		test.Fatal("missing actual native review", model.Status)
	}
	for _, slot := range state.Review.Slots {
		mcpClick(test, tester, "MCP review content", model.mcpText("仅批准本次槽操作 ", "Approve only this slot action ")+slot)
	}
	phrase := state.Review.Confirmation
	click(test, tester, phrase)
	tester.Type(phrase)
	click(test, tester, model.mcpText("确认且仅提交一次", "Confirm and submit once"))
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
}

func mcpNativeSelect(test *testing.T, tester *ui.Tester, model *Model, queue chan func(), asset mcpclient.Metadata) {
	test.Helper()
	click(test, tester, "载入 / 核实 MCP 资产")
	if model.mcpState().Pending == "reload" {
		click(test, tester, "放弃并继续")
	}
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	mcpClick(test, tester, "MCP editor area", fmt.Sprintf("%s · %s · schema %d · revision %d", asset.Name, asset.Agent, asset.Schema, asset.Revision))
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
}

func TestMCPNativeUpdateCopyDeleteEvents(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	assets := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, assets)
	model, queue := mcpLoadController(test, fixture)
	tester := ui.NewTester(model.View, 920, 760)
	mcpNativeSelect(test, tester, model, queue, created)
	mcpClick(test, tester, "MCP editor area", "资产名称")
	tester.Key(ui.Ctrl, ui.KeyA)
	tester.Type("Updated native MCP")
	mcpClick(test, tester, "MCP editor area", "审阅服务端保存…")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	mcpNativeSubmit(test, tester, model, queue)
	list, err := assets.List(test.Context())
	if err != nil || len(list) != 1 || list[0].Revision != 2 || list[0].Name != "Updated native MCP" {
		test.Fatal("native update failed", err)
	}
	mcpNativeSelect(test, tester, model, queue, list[0])
	mcpClick(test, tester, "MCP editor area", "无密钥副本名称")
	tester.Key(ui.Ctrl, ui.KeyA)
	tester.Type("Keyless native copy")
	mcpClick(test, tester, "MCP editor area", "审阅无密钥复制…")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	mcpNativeSubmit(test, tester, model, queue)
	list, err = assets.List(test.Context())
	if err != nil || len(list) != 2 {
		test.Fatal("native copy failed", err)
	}
	var copied mcpclient.Metadata
	for _, asset := range list {
		if asset.ID != created.ID {
			copied = asset
		}
	}
	view, err := assets.Read(test.Context(), copied.ID)
	if err != nil || !view.NeedsCredential || view.Secrets[0].Present || view.Secrets[1].Present {
		test.Fatal("native keyless copy carried credentials", err)
	}
	mcpNativeSelect(test, tester, model, queue, copied)
	mcpClick(test, tester, "MCP editor area", "审阅删除资产…")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	mcpNativeSubmit(test, tester, model, queue)
	if fixture.commits(test, "create") != 2 || fixture.commits(test, "update") != 1 || fixture.commits(test, "delete") != 1 || fixture.count("CopyAsset") != 1 || fixture.count("DeleteAsset") != 1 {
		test.Fatal("native CRUD physical commit counts wrong")
	}
}

func TestMCPNativeThemeAndReadOnly(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	assets := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, assets)
	model, queue := mcpLoadController(test, fixture)
	model.mcpAction(created.ID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester := ui.NewTester(model.View, 920, 760)
	for _, english := range []bool{false, true} {
		model.configurationState().MCPEnglish = english
		for _, dark := range []bool{false, true} {
			tester.SetDark(dark)
			tester.Frame()
			mcpClick(test, tester, "MCP editor area", model.mcpText("命令（仅声明，不执行）", "Command (declaration, never executed)"))
			mcpScreenshot(test, tester, fmt.Sprintf("editor-en%t-dark%t", english, dark))
		}
	}
	model.configurationState().MCPEnglish = false
	unknown, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, serverstate.ConfigurationAssetDraft{AgentID: "claude", Kind: serverstate.ConfigurationKindMCP, Name: "readonly future envelope", SchemaVersion: 1, Payload: serverstate.NewConfigurationPayload([]byte(`{"version":1,"document":{},"slots":{},"future":false}`))})
	if err != nil {
		test.Fatal(err)
	}
	model.mcpAction(unknown.ID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if !model.mcpState().Form.ReadOnly || !tester.HasText("禁止覆盖、复制、删除") {
		test.Fatal("unknown details are not visibly read only")
	}
	mcpClick(test, tester, "MCP editor area", "审阅删除资产…")
	mcpClick(test, tester, "MCP editor area", "审阅无密钥复制…")
	mcpClick(test, tester, "MCP editor area", "审阅服务端保存…")
	if model.Busy || model.mcpState().Review != nil || fixture.count("DeleteAsset") != 0 || fixture.count("CopyAsset") != 0 || fixture.count("UpdateAsset") != 0 {
		test.Fatal("read-only native controls prepared a mutation")
	}
}
