package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/egoist/mygo/ui"
)

func TestNativeConfigurationPasswordPixels(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 4
	state := model.configurationState()
	state.Form = &configurationForm{Name: "Pixel privacy fixture", Mode: agentconfig.ClaudeAPIKeyAuth, Asset: configurationAsset{Editable: true}, Actions: [2]configurationSecretAction{configurationReplace, configurationClear}}
	state.Form.secret()[0] = strings.Repeat("M", 32)
	tester := ui.NewTester(model.View, 920, 760)
	configurationClick(test, tester, "新凭据（仅内存）")
	before := bytes.Clone(tester.Image().Pix)
	state.Form.secret()[0] = strings.Repeat("Z", 32)
	tester.Frame()
	if !bytes.Equal(before, tester.Image().Pix) {
		test.Fatal("same-length replacement values changed visible pixels; password masking absent")
	}
}

func configurationClick(test *testing.T, tester *ui.Tester, label string) {
	test.Helper()
	for range 24 {
		box, found := tester.Find(label)
		if !found {
			test.Fatalf("native component not found: %s", label)
		}
		if box.Y >= 240 && box.Y+box.H < 690 {
			click(test, tester, label)
			return
		}
		delta := float32(220)
		if box.Y < 240 {
			delta = -220
		}
		tester.Scroll(460, 480, 0, delta)
		tester.Frame()
	}
	test.Fatalf("native component unreachable by scroll: %s", label)
}

func TestNativeConfigurationViewRemoteEvents(test *testing.T) {
	fixture := newConfigurationFixture(test)
	model, queue := configurationLoadModel(test, fixture)
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "新建 Claude provider")
	configurationClick(test, tester, "名称（≤128 UTF-8 字节）")
	tester.Type("Native event asset")
	configurationClick(test, tester, "仅保存服务端资产")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if model.configurationState().Form.Asset.ID == "" || fixture.count("CreateAsset") != 1 || fixture.count("BeginNativeConsumer") != 0 {
		test.Fatal("native save event did not remain remote-only")
	}
	model.configurationState().Form.Dirty = true
	tester.Frame()
	click(test, tester, "账号")
	tester.Frame()
	if model.Page != 4 || !tester.HasText("放弃未保存草稿？") {
		test.Fatal("native dirty-navigation modal missing")
	}
	click(test, tester, "保留草稿")
	if model.Page != 4 {
		test.Fatal("dirty dismiss discarded editor")
	}
	click(test, tester, "账号")
	click(test, tester, "放弃草稿并继续")
	if model.Page != 3 {
		test.Fatal("explicit discard did not navigate")
	}
	click(test, tester, "本机配置状态 / 离线恢复")
	if model.Page != 4 {
		test.Fatal("account offline restore link missing")
	}
}

func TestNativeConfigurationViewApprovalEvents(test *testing.T) {
	fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeAPIKeyAuth)
	model.ApplyConfiguration()
	configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("本次安全操作预览（非文件全文差异）") {
		test.Fatal("real native approval modal missing")
	}
	tester.Key(0, ui.KeyTab)
	if fixture.count("AuthorizeNativeMaterialization") != 0 {
		test.Fatal("keyboard approved unchecked slot")
	}
	configurationClick(test, tester, "仅授权本次敏感槽 claude.api-key")
	configurationClick(test, tester, "最终批准本次 exact 操作")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if model.configurationState().Outcome.Local.Status != "AppliedOnDisk" || fixture.count("ConsumeNativeMaterialization") != 1 {
		test.Fatal("native final approval did not apply once")
	}
}

func TestNativeConfigurationViewEscapeDenies(test *testing.T) {
	fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeNativeLogin)
	model.ApplyConfiguration()
	configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
	tester := ui.NewTester(model.View, 920, 760)
	tester.Key(0, ui.KeyEscape)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if fixture.count("AuthorizeNativeMaterialization") != 0 || fixture.count("ConsumeNativeMaterialization") != 0 || !strings.Contains(model.Status, "未批准") {
		test.Fatal("Escape failed to deny zero-slot final confirmation")
	}
}

type configurationFocusFixture struct {
	fixture  *configurationFixture
	model    *Model
	queue    chan func()
	target   string
	before   []byte
	existed  bool
	original []byte
	requests int
}

func newConfigurationFocusFixture(test *testing.T, operation string) configurationFocusFixture {
	test.Helper()
	fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeNativeLogin)
	result := configurationFocusFixture{fixture: fixture, model: model, queue: queue, target: filepath.Join(fixture.service.Home, ".claude", "settings.json")}
	if operation == "restore" {
		result.original = []byte(`{"env":{"UNOWNED":"keep","ANTHROPIC_MODEL":"prior"}}`)
		if err := os.WriteFile(result.target, result.original, 0600); err != nil {
			test.Fatal("write synthetic original baseline")
		}
		model.ApplyConfiguration()
		configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
		if model.configurationState().Prompt == nil {
			test.Fatal("restore fixture missing actual apply approval")
		}
		model.ReplyConfigurationApproval(true)
		configurationPump(test, model, queue, func() bool { return !model.Busy })
		if model.configurationState().Outcome.Local.Status != "AppliedOnDisk" {
			test.Fatal("restore fixture did not actually apply")
		}
		model.RestoreConfiguration(true)
	} else {
		model.ApplyConfiguration()
	}
	configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
	prompt := model.configurationState().Prompt
	if prompt == nil || prompt.Plan.Kind != operation || len(prompt.Slots) != 0 {
		test.Fatal("missing actual zero-slot operation prompt")
	}
	data, err := os.ReadFile(result.target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		test.Fatal("read synthetic operation baseline")
	}
	result.before, result.existed = data, err == nil
	result.requests = fixture.count("")
	return result
}

func TestNativeConfigurationSafeLocalInitialFocus(test *testing.T) {
	for _, operation := range []string{"apply", "restore"} {
		for _, action := range []string{"initial-enter", "tab-enter"} {
			test.Run(operation+"/"+action, func(test *testing.T) {
				fixture := newConfigurationFocusFixture(test, operation)
				model := fixture.model
				tester := ui.NewTester(model.View, 920, 760)
				if !tester.Focused("拒绝本次操作") {
					test.Error("new local operation modal did not focus safe denial")
				}
				if action == "tab-enter" {
					tester.Key(0, ui.KeyTab)
					if !tester.Focused("最终批准本次 exact 操作") {
						test.Fatal("deliberate keyboard navigation did not reach final approval")
					}
				}
				tester.Key(0, ui.KeyEnter)
				configurationPump(test, model, fixture.queue, func() bool { return !model.Busy })
				data, err := os.ReadFile(fixture.target)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					test.Fatal("read actual post-operation target")
				}
				if action == "initial-enter" {
					if (err == nil) != fixture.existed || !bytes.Equal(data, fixture.before) || !strings.Contains(model.Status, "未批准") {
						test.Error("initial Enter changed target or failed to deny the exact operation")
					}
				} else if operation == "apply" {
					if err != nil || model.configurationState().Outcome.Local.Status != "AppliedOnDisk" {
						test.Fatal("deliberate Tab then Enter did not actually apply")
					}
				} else if !bytes.Equal(data, fixture.original) || model.configurationState().Outcome.Local.Status != "Restored" {
					test.Fatal("deliberate Tab then Enter did not restore original bytes")
				}
				if operation == "restore" && fixture.fixture.count("") != fixture.requests {
					test.Fatal("offline restore focus operation made a network request")
				}
			})
		}
	}
}

func TestNativeConfigurationSafeSecretKeyboardGates(test *testing.T) {
	fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeAPIKeyAuth)
	model.ApplyConfiguration()
	configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.Focused("拒绝本次操作") {
		test.Fatal("secret-bearing modal did not start on safe denial")
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("仅授权本次敏感槽 claude.api-key") {
		test.Fatal("keyboard did not reach explicit secret slot")
	}
	tester.Key(0, ui.KeySpace)
	prompt := model.configurationState().Prompt
	if prompt == nil || len(prompt.Checked) != 1 || !prompt.Checked[0] || fixture.count("AuthorizeNativeMaterialization") != 0 || fixture.count("ConsumeNativeMaterialization") != 0 {
		test.Fatal("slot selection was skipped or substituted for final approval")
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("最终批准本次 exact 操作") {
		test.Fatal("checked slot did not permit deliberate final navigation")
	}
	tester.Key(0, ui.KeyEnter)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if model.configurationState().Outcome.Local.Status != "AppliedOnDisk" || fixture.count("ConsumeNativeMaterialization") != 1 {
		test.Fatal("explicit slot and final keyboard approval failed actual application")
	}
}

func TestNativeConfigurationSafeDestructiveInitialFocus(test *testing.T) {
	for _, operation := range []string{"delete", "discard"} {
		for _, action := range []string{"initial-enter", "tab-enter"} {
			test.Run(operation+"/"+action, func(test *testing.T) {
				fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeNativeLogin)
				state := model.configurationState()
				safe, dangerous := "取消", "确认删除资产"
				if operation == "delete" {
					state.ConfirmDelete = true
				} else {
					state.Form.Dirty = true
					state.Form.secret()[0] = "synthetic-discard-replacement"
					state.PendingPage = 3
					safe, dangerous = "保留草稿", "放弃草稿并继续"
				}
				tester := ui.NewTester(model.View, 920, 760)
				if !tester.Focused(safe) {
					test.Error("new destructive modal did not focus its safe action")
				}
				if action == "tab-enter" {
					tester.Key(0, ui.KeyTab)
					if !tester.Focused(dangerous) {
						test.Fatal("explicit Tab did not reach destructive action")
					}
				}
				tester.Key(0, ui.KeyEnter)
				configurationPump(test, model, queue, func() bool { return !model.Busy })
				if operation == "delete" {
					assets, err := fixture.domain.List(test.Context(), fixture.owner)
					expected := 1
					if action == "tab-enter" {
						expected = 0
					}
					if err != nil || len(assets) != expected || fixture.count("DeleteAsset") != 1-expected {
						test.Fatal("delete keyboard approval did not preserve actual remote asset boundary")
					}
				} else if action == "initial-enter" {
					if model.Page != 4 || state.Form == nil || !state.Form.Dirty || state.Form.secret()[0] != "synthetic-discard-replacement" || state.PendingPage != -1 {
						test.Fatal("initial Enter discarded a private unsaved draft")
					}
				} else if model.Page != 3 || state.Form.Dirty || state.Form.secret()[0] != "" {
					test.Fatal("deliberate draft discard did not navigate and clear replacement")
				}
			})
		}
	}
}

func configurationCheckModalTabCycle(test *testing.T, tester *ui.Tester, safe, dangerous string) {
	test.Helper()
	for _, modifiers := range []ui.Modifiers{0, ui.Shift} {
		for range 4 {
			tester.Key(modifiers, ui.KeyTab)
			if !tester.Focused(safe) && !tester.Focused(dangerous) {
				test.Fatal("Tab or ShiftTab escaped the native modal scope")
			}
		}
		if !tester.Focused(safe) {
			test.Fatal("native modal tab cycle did not return to its safe action")
		}
	}
}

func TestNativeConfigurationModalContainmentAndFocusReturn(test *testing.T) {
	for _, operation := range []string{"apply", "restore", "delete", "discard", "identity"} {
		test.Run(operation, func(test *testing.T) {
			fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeNativeLogin)
			state := model.configurationState()
			if operation == "restore" {
				model.ApplyConfiguration()
				configurationPump(test, model, queue, func() bool { return state.Prompt != nil || !model.Busy })
				if state.Prompt == nil {
					test.Fatal("restore opener fixture missing apply prompt")
				}
				model.ReplyConfigurationApproval(true)
				configurationPump(test, model, queue, func() bool { return !model.Busy })
				if state.Outcome.Local.Status != "AppliedOnDisk" {
					test.Fatal("restore opener fixture did not apply")
				}
			}
			if operation == "discard" {
				state.Form.Dirty = true
			}
			tester := ui.NewTester(model.View, 920, 760)
			opener, safe, dangerous := "预览并明确批准本次应用…", "拒绝本次操作", "最终批准本次 exact 操作"
			switch operation {
			case "apply", "identity":
				configurationClick(test, tester, opener)
				configurationPump(test, model, queue, func() bool { return state.Prompt != nil || !model.Busy })
			case "restore":
				opener = "预览并明确批准恢复…"
				configurationClick(test, tester, opener)
				configurationPump(test, model, queue, func() bool { return state.Prompt != nil || !model.Busy })
			case "delete":
				opener, safe, dangerous = "删除所选服务端资产…", "取消", "确认删除资产"
				configurationClick(test, tester, opener)
			case "discard":
				opener, safe, dangerous = "账号", "保留草稿", "放弃草稿并继续"
				click(test, tester, opener)
			}
			tester.Frame()
			if !tester.Focused(safe) {
				test.Fatal("actual opener did not yield safe initial modal focus")
			}
			configurationCheckModalTabCycle(test, tester, safe, dangerous)
			if operation == "identity" {
				model.Logout()
				configurationPump(test, model, queue, func() bool { return !model.Busy })
			} else {
				tester.Key(0, ui.KeyEscape)
				configurationPump(test, model, queue, func() bool { return !model.Busy })
			}
			tester.Frame()
			if tester.Focused(dangerous) || state.Prompt != nil || state.ConfirmDelete || state.PendingPage >= 0 {
				test.Fatal("closing modal retained a dangerous scope or pending approval")
			}
			if operation != "identity" && !tester.Focused(opener) {
				test.Fatal("native modal did not return focus to its actual opener")
			}
			before := fixture.count("")
			if operation == "identity" {
				tester.Key(0, ui.KeyEnter)
				if fixture.count("") != before || fixture.count("AuthorizeNativeMaterialization") != 0 || fixture.count("ConsumeNativeMaterialization") != 0 {
					test.Fatal("Enter after identity invalidation invoked a sensitive action")
				}
			}
		})
	}
}

func TestNativeConfigurationViewThemesScaleAndPrivacy(test *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{920, 640} {
			for _, zoom := range []float32{1, 2} {
				test.Run(fmt.Sprintf("dark%t-width%d-zoom%.0f", dark, width, zoom), func(test *testing.T) {
					model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
					model.Page = 4
					state := model.configurationState()
					state.Form = &configurationForm{Name: "Safe display", Mode: agentconfig.ClaudeAPIKeyAuth, Asset: configurationAsset{Editable: true}, Actions: [2]configurationSecretAction{configurationReplace, configurationClear}}
					state.Form.secret()[0] = "synthetic-hidden-native-editor-secret"
					tester := ui.NewTester(model.View, width, 760)
					tester.SetDark(dark)
					tester.SetZoom(zoom)
					tester.Frame()
					if !tester.HasText("保存与本机应用分离") || !tester.HasText("核实本机状态") || !tester.HasText("仅保存服务端资产") {
						test.Fatal("native configuration controls absent")
					}
					if strings.Contains(strings.Join(tester.Texts(), "\n"), "synthetic-hidden-native-editor-secret") {
						test.Fatal("password text exposed to native label/screenshot tree")
					}
					if directory := os.Getenv("DESKTOP_EVIDENCE_DIR"); directory != "" {
						file, err := os.Create(filepath.Join(directory, fmt.Sprintf("configuration-headless-dark%t-width%d-zoom%.0f.png", dark, width, zoom)))
						if err != nil {
							test.Fatal(err)
						}
						if err := png.Encode(file, tester.Image()); err != nil {
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
