package desktop

import (
	"fmt"
	"strings"

	"github.com/ca-x/cognate/internal/grokclient"
	"github.com/egoist/mygo/ui"
)

func (model *Model) grokView(context *ui.Context) {
	state := model.grokState()
	locked := model.Busy || state.Review != nil || state.Blocked
	ui.Scroll(context.Key("grok-scroll")).Grow(1).TrackScroll(&state.Scroll).Children(func() {
		ui.Column(context).FillWidth().Gap(10).Children(func() {
			ui.Text(context, "Grok Build · 仅管理服务端资产").Bold()
			ui.Text(context, "保存、复制、删除和排序均不应用本机；不执行客户端插件。不读取或显示旧密钥。")
			if model.session.value() == "" {
				ui.Text(context, "请在账号页登录后载入私有 Grok 资产。")
			}
			ui.Row(context).Gap(8).Children(func() {
				if ui.Button(context.Key("grok-load"), "载入 / 核实 Grok 资产").Disabled(model.Busy || state.Review != nil || model.session.value() == "").Clicked() {
					model.grokRequest("reload")
				}
				if ui.Button(context.Key("grok-new"), "新建 Grok provider").Disabled(locked || state.session() == nil).Clicked() {
					model.grokRequest("new")
				}
			})
			if state.Blocked {
				ui.Text(context, "本次提交资格已消耗：禁止再次提交。请显式重新载入核实（Unknown 不自动重放）。")
			}
			for _, asset := range state.Assets {
				if ui.Button(context.Key("grok-asset-"+asset.ID), grokRevision(asset)).Disabled(locked).Clicked() {
					model.grokRequest(asset.ID)
				}
			}
			if form := state.Form; form != nil {
				model.grokFormView(context, form, locked)
			}
			if len(state.Order.Assets) > 0 {
				ui.Text(context, fmt.Sprintf("Grok provider 完整顺序 · CAS revision %d（仅改顺序，不批准内容）", state.Order.Revision)).Bold()
				for index, asset := range state.Order.Assets {
					ui.Row(context.Key(fmt.Sprintf("grok-order-%d", index))).Gap(6).Children(func() {
						ui.Text(context, fmt.Sprintf("%d. %s · %s", index+1, asset.Name, asset.Agent)).Grow(1)
						if ui.Button(context.Key("up"), "上移").Disabled(locked || index == 0).Clicked() {
							state.move(index, -1)
						}
						if ui.Button(context.Key("down"), "下移").Disabled(locked || index == len(state.Order.Assets)-1).Clicked() {
							state.move(index, 1)
						}
					})
				}
				if ui.Button(context.Key("grok-review-order"), "审阅完整排序").Disabled(locked).Clicked() {
					model.PrepareGrok(grokclient.ReplaceOrder)
				}
			}
		})
	})
	model.grokModals(context)
}

func (model *Model) grokFormView(context *ui.Context, form *grokForm, locked bool) {
	locked = locked || form.ReadOnly
	mark := func(changed bool) {
		if changed {
			form.Dirty = true
		}
	}
	ui.Text(context, fmt.Sprintf("Grok 编辑 · revision %d · key-present=%t · needs-credential=%t", form.Metadata.Revision, form.KeyPresent, form.NeedsCredential)).Bold()
	if form.ReadOnly {
		ui.Text(context, "未知字段或版本：只读，不能保存、复制或删除；请使用支持该格式的客户端。")
	}
	mark(ui.TextInput(context.Key("grok-name"), &form.Name).Label("资产名称（≤128 UTF-8 字节）").Disabled(locked).FillWidth().Changed())
	ui.Row(context).Gap(8).Children(func() {
		for _, mode := range []grokclient.Mode{grokclient.NativeLogin, grokclient.CustomModel} {
			label := string(mode)
			if form.Mode == mode {
				label = "✓ " + label
			}
			if ui.Button(context.Key("grok-mode-"+string(mode)), label).Disabled(locked).Clicked() {
				form.setMode(mode)
			}
		}
	})
	if form.Mode == grokclient.CustomModel {
		for index, field := range []struct {
			label string
			value *string
		}{
			{"Profile", &form.Profile}, {"Upstream model", &form.UpstreamModel}, {"Endpoint", &form.Endpoint}, {"Model name", &form.ModelName}, {"API backend（当前仅 responses）", &form.APIBackend}, {"Context window（1–9007199254740991，精确十进制整数）", &form.ContextWindow},
		} {
			mark(ui.TextInput(context.Key(fmt.Sprintf("grok-model-%d", index)), field.value).Label(field.label).Disabled(locked).FillWidth().Changed())
		}
		mark(ui.Checkbox(context.Key("grok-env-present"), &form.EnvPresent, "设置 env key（不选表示 absent，非空字符串）").Disabled(locked).Changed())
		mark(ui.TextInput(context.Key("grok-env-key"), &form.EnvKey).Label("Env key（可选）").Disabled(locked || !form.EnvPresent).FillWidth().Changed())
	}
	ui.Text(context, "grokbuild.api-key：Keep 保留服务端值；Replace / Clear 必须在审阅中独立授权。切 native 不自动清除已有密钥。")
	ui.Row(context).Gap(6).Children(func() {
		for _, action := range []struct {
			value grokclient.KeyAction
			label string
		}{{grokclient.NoKeyMutation, "不携带密钥动作"}, {grokclient.KeepKey, "Keep"}, {grokclient.ReplaceKey, "Replace"}, {grokclient.ClearKey, "Clear"}} {
			label := action.label
			if form.Action == action.value {
				label = "✓ " + label
			}
			if ui.Button(context.Key(fmt.Sprintf("grok-key-%d", action.value)), label).Disabled(locked || action.value == grokclient.ReplaceKey && form.Mode != grokclient.CustomModel || action.value == grokclient.KeepKey && form.Metadata.ID == "").Clicked() {
				form.clearSecret()
				form.Action, form.Dirty = action.value, true
			}
		}
	})
	if form.Action == grokclient.ReplaceKey {
		mark(ui.TextInput(context.Key("grok-secret"), form.Secret.input()).Label("Grok 新密钥（仅内存）").Password().Disabled(locked).FillWidth().Changed())
	}
	if form.Dirty {
		ui.Text(context, "未保存草稿；取消、离开或模式切换清空新密钥。")
	}
	ui.Row(context).Gap(8).Children(func() {
		label, action := "审阅创建 Grok 资产", grokclient.Create
		if form.Metadata.ID != "" {
			label, action = "审阅更新 Grok 资产", grokclient.Update
		}
		if ui.PrimaryButton(context.Key("grok-save"), label).Disabled(locked).Clicked() {
			model.PrepareGrok(action)
		}
		if ui.Button(context.Key("grok-cancel-form"), "取消 Grok 草稿").Disabled(model.Busy).Clicked() {
			form.clearSecret()
			model.grokState().Form = nil
		}
	})
	if form.Metadata.ID != "" {
		ui.TextInput(context.Key("grok-copy-name"), &model.grokState().CopyName).Label("副本名称（不带秘密）").Disabled(locked).FillWidth()
		ui.Row(context).Gap(8).Children(func() {
			if ui.Button(context.Key("grok-copy"), "审阅复制（不带密钥）").Disabled(locked || form.Dirty).Clicked() {
				model.PrepareGrok(grokclient.Copy)
			}
			if ui.Button(context.Key("grok-delete"), "审阅删除服务端资产").Disabled(locked || form.Dirty).Clicked() {
				model.PrepareGrok(grokclient.Delete)
			}
		})
	}
}

func (model *Model) grokModals(context *ui.Context) {
	state := model.grokState()
	if state.Pending != "" || state.PendingPage >= 0 {
		open := true
		ui.Modal(context.Key("grok-discard-modal"), &open, func() {
			ui.Column(context).Padding(20).Gap(10).Children(func() {
				ui.Text(context, "放弃 Grok 未保存草稿？新密钥已清空。")
				if ui.Button(context.Key("grok-keep-draft"), "保留 Grok 草稿").Clicked() {
					open = false
				}
				if ui.Button(context.Key("grok-discard"), "放弃 Grok 草稿并继续").Clicked() {
					page, action := state.PendingPage, state.Pending
					state.PendingPage, state.Pending = -1, ""
					state.discardOrder()
					if state.Form != nil {
						state.Form.clearSecret()
						state.Form.Dirty = false
					}
					if page >= 0 {
						model.configurationNavigate(page)
					} else {
						model.grokAction(action)
					}
				}
			})
		})
		if !open {
			state.PendingPage, state.Pending = -1, ""
		}
	}
	if state.Review == nil {
		return
	}
	open := true
	ui.Modal(context.Key("grok-review-modal"), &open, func() {
		ui.Scroll(context.Key("grok-review-scroll")).MaxHeight(580).TrackScroll(&state.ReviewScroll).Children(func() {
			ui.Column(context).Padding(20).Gap(8).Children(func() {
				ui.Text(context, "完整 Grok 操作审阅 · 不含密钥值").Bold()
				for line := range strings.SplitSeq(state.Review.Summary, "\n") {
					ui.Text(context, line).FillWidth()
				}
				ui.Text(context, "此操作只改变服务端资产，不执行本机应用；取消不能撤销已经提交的操作。")
				if state.ReviewAction == grokclient.ReplaceKey {
					ui.Checkbox(context.Key("grok-authorize-replace"), &state.KeyAuthorized, "仅授权本次 Replace grokbuild.api-key").Disabled(model.Busy)
				}
				if state.ReviewAction == grokclient.ClearKey {
					ui.Checkbox(context.Key("grok-authorize-clear"), &state.KeyAuthorized, "仅授权本次 Clear grokbuild.api-key").Disabled(model.Busy)
				}
				ui.Text(context, "精确输入以下最终确认："+state.Review.Confirmation)
				ui.TextInput(context.Key("grok-confirmation"), &state.Confirmation).Label("本次 exact 确认").Disabled(model.Busy).FillWidth()
				allowed := state.Confirmation == state.Review.Confirmation && (state.ReviewAction != grokclient.ReplaceKey && state.ReviewAction != grokclient.ClearKey || state.KeyAuthorized)
				if ui.PrimaryButton(context.Key("grok-submit"), "最终确认并提交一次").Disabled(model.Busy || !allowed).Clicked() {
					model.SubmitGrok()
				}
				if ui.Button(context.Key("grok-deny"), "取消 Grok 审阅").Disabled(model.Busy).Clicked() {
					model.CancelGrokReview()
				}
			})
		})
	})
	if !open {
		model.CancelGrokReview()
	}
}
