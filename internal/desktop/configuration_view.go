package desktop

import (
	"fmt"
	"runtime"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/egoist/mygo/ui"
)

func (model *Model) configurationView(context *ui.Context) {
	state := model.configurationState()
	ui.Scroll(context.Key("configuration-scroll")).Grow(1).TrackScroll(&state.Scroll).Children(func() {
		ui.Column(context).FillWidth().Gap(10).Children(func() {
			ui.Text(context, "AI 配置 · 保存与本机应用分离").Bold()
			ui.Text(context, "本片仅支持 Claude provider schema1；其余资产仅元数据。配置不会自动应用。")
			if model.session.value() == "" {
				ui.Text(context, "未登录：远端资产需先登录；本机状态/恢复可离线操作。")
			}
			if model.pendingJobs.Load() > 0 && !model.Busy {
				ui.Text(context, "正在取消并等待实际 worker 排空；尚未释放本机资格。")
			}
			ui.Row(context).Gap(8).Children(func() {
				if ui.Button(context.Key("configuration-load"), "载入私有配置").Disabled(model.Busy || model.session.value() == "").Clicked() {
					model.LoadConfigurations()
				}
				if ui.Button(context.Key("configuration-new"), "新建 Claude provider").Disabled(model.Busy || state.Identity.private == nil).Clicked() {
					model.NewConfiguration()
				}
				if ui.Button(context.Key("configuration-account"), "账号 / 本机密钥设置").Clicked() {
					model.configurationNavigate(3)
				}
			})
			if len(state.Assets) > 0 {
				state.List.Selected = &state.ListIndex
				state.List.Key = func(index int) any { return state.Assets[index].ID }
				state.List.Label = func(index int) string { return state.Assets[index].Name }
				list := ui.List(context.Key("configuration-assets"), &state.List, len(state.Assets)).Label("配置资产").Height(160).Rows(func(row ui.ListRow) {
					asset := state.Assets[row.Index]
					label := fmt.Sprintf("%s · %s/%s · schema%d · revision%d", asset.Name, asset.AgentID, asset.Kind, asset.SchemaVersion, asset.Revision)
					if !asset.Editable {
						label += " · 仅元数据"
					}
					ui.Text(row.Context, label)
				})
				if list.Changed() && state.ListIndex >= 0 && state.ListIndex < len(state.Assets) {
					model.SelectConfiguration(state.Assets[state.ListIndex].ID)
				}
			}
			if state.Selected != "" {
				ui.Text(context, "已选资产："+state.Selected)
			}
			if state.Form != nil {
				model.configurationEditor(context, state.Form)
			}
			ui.Row(context).Gap(8).Children(func() {
				if ui.Button(context.Key("configuration-delete"), "删除所选服务端资产…").Disabled(model.Busy || state.Selected == "").Clicked() {
					state.ConfirmDelete = true
				}
			})
			ui.Divider(context)
			ui.Text(context, "本机操作 / 离线核实与恢复").Bold()
			if runtime.GOOS != "linux" {
				ui.Text(context, "当前本机应用、状态与恢复仅支持 Linux；跨平台构建不代表运行支持。")
			}
			if ui.TextInput(context.Key("configuration-profile"), &state.Profile).Label("本机 profile ID（不自动应用）").Disabled(model.Busy).FillWidth().Changed() {
				model.cancelJob()
			}
			ui.Text(context, "只作用于 claude.settings / user；不会读取本机 OAuth。需先在账号页显式初始化客户端密钥。")
			ui.Text(context, fmt.Sprintf("owner: %s · origin: %s · profile: %s", state.Identity.OwnerID, state.Identity.Origin, state.Profile))
			blocked := model.Busy || model.localWorkBlocked() || runtime.GOOS != "linux"
			applicable := state.Form != nil && !state.Form.Dirty && state.Form.Asset.ID != "" && state.Form.Asset.Editable && state.Identity.private != nil
			ui.Row(context).Gap(8).Children(func() {
				if ui.Button(context.Key("configuration-preview"), "仅预览本机计划").Disabled(blocked || !applicable).Clicked() {
					model.PreviewConfiguration()
				}
				if ui.PrimaryButton(context.Key("configuration-apply"), "预览并明确批准本次应用…").Disabled(blocked || !applicable).Clicked() {
					model.ApplyConfiguration()
				}
			})
			ui.TextInput(context.Key("configuration-operation"), &state.OperationID).Label("本机 operation ID（可跨重启输入）").FillWidth().Disabled(model.Busy)
			ui.Row(context).Gap(8).Children(func() {
				if ui.Button(context.Key("configuration-status"), "核实本机状态").Disabled(blocked || state.OperationID == "").Clicked() {
					model.ConfigurationStatus()
				}
				if ui.Button(context.Key("configuration-restore-preview"), "仅预览恢复").Disabled(blocked || state.OperationID == "").Clicked() {
					model.RestoreConfiguration(false)
				}
				if ui.Button(context.Key("configuration-restore"), "预览并明确批准恢复…").Disabled(blocked || state.OperationID == "").Clicked() {
					model.RestoreConfiguration(true)
				}
			})
			ui.Text(context, "恢复必须针对原记录重新预览批准；基线冲突不强制覆盖。Unknown 不重发授权/消费；Consumed 不等于 Applied。")
			if state.Outcome.Plan.OperationID != "" {
				plan := state.Outcome.Plan
				ui.Text(context, "本次安全操作预览（非文件全文差异）").Bold()
				ui.Text(context, fmt.Sprintf("owner: %s · origin: %s · profile: %s", state.Identity.OwnerID, state.Identity.Origin, state.Profile))
				ui.Text(context, fmt.Sprintf("operation: %s · asset: %s / revision %d · target: claude.settings/user", plan.OperationID, plan.AssetID, plan.AssetRevision))
				ui.Text(context, "adapter: "+plan.AdapterID+" / "+plan.AdapterVersion+" / "+plan.AdapterDigest)
				ui.Text(context, "readiness: "+plan.Readiness+" · source: "+plan.SourceOperation)
				for _, operation := range plan.Operations {
					ui.Text(context, string(operation.Slot)+" → "+string(operation.Action))
				}
			}
			for index, outcome := range state.History {
				id := outcome.Plan.OperationID
				if id == "" {
					id = outcome.Local.OperationID
				}
				if ui.Button(context.Key(fmt.Sprintf("configuration-history-%d", index)), id+" · "+configurationOutcomeMessage(outcome, nil)).FillWidth().Disabled(model.Busy).Clicked() {
					state.OperationID = id
				}
				ui.Text(context, fmt.Sprintf("remote: %s · unknown: %t · local: %s · needs attention: %t", configurationRemoteStatus(outcome), outcome.RemoteUnknown, configurationSafeStatus(outcome.Local.Status), outcome.Local.NeedsAttention))
			}
		})
	})
	model.configurationModals(context)
}

func (model *Model) configurationEditor(context *ui.Context, form *configurationForm) {
	if ui.Button(context.Key("configuration-back-to-list"), "返回资产列表（保留草稿）").Clicked() {
		state := model.configurationState()
		state.Scroll.Y = 0
		state.List.Handle.Focus()
	}
	mark := func(changed bool) {
		if changed {
			form.Dirty = true
		}
	}
	ui.Text(context, fmt.Sprintf("编辑草稿 · revision %d（不含原秘密）", form.Asset.Revision)).Bold()
	mark(ui.TextInput(context.Key("configuration-name"), &form.Name).Label("名称（≤128 UTF-8 字节）").Disabled(model.Busy).FillWidth().Changed())
	ui.Text(context, "认证模式（保留字段 presence；不自动修剪输入）")
	ui.Row(context).Gap(6).Children(func() {
		for _, mode := range []agentconfig.ClaudeAuthMode{agentconfig.ClaudeNativeLogin, agentconfig.ClaudeAPIKeyAuth, agentconfig.ClaudeAuthTokenAuth} {
			label := string(mode)
			if form.Mode == mode {
				label = "✓ " + label
			}
			if ui.Button(context.Key("configuration-mode-"+string(mode)), label).Disabled(model.Busy).Clicked() {
				form.Mode = mode
				form.Dirty = true
			}
		}
	})
	for index, label := range []string{"Endpoint（≤2048 字节）", "默认模型（≤256 字节）", "Haiku 模型", "Sonnet 模型", "Opus 模型"} {
		ui.Row(context).Gap(6).Children(func() {
			mark(ui.Checkbox(context.Key(fmt.Sprintf("configuration-presence-%d", index)), &form.Present[index], "设置此字段").Disabled(model.Busy).Changed())
			mark(ui.TextInput(context.Key(fmt.Sprintf("configuration-value-%d", index)), &form.Values[index]).Label(label).Disabled(model.Busy || !form.Present[index]).Grow(1).Changed())
		})
	}
	for index, label := range []string{"claude.api-key", "claude.auth-token"} {
		ui.Text(context, fmt.Sprintf("%s · 服务端存在: %t · 不显示旧值", label, form.SecretPresent[index]))
		ui.Text(context, "CLEAR 仅删除服务端 slot；不会直接清除本机 env。")
		ui.Row(context).Gap(6).Children(func() {
			for _, action := range []struct {
				Value configurationSecretAction
				Label string
			}{{configurationKeep, "保留 KEEP"}, {configurationReplace, "替换 REPLACE"}, {configurationClear, "清除 CLEAR"}} {
				title := action.Label
				if form.Actions[index] == action.Value {
					title = "✓ " + title
				}
				if ui.Button(context.Key(fmt.Sprintf("configuration-secret-%d-%d", index, action.Value)), title).Disabled(model.Busy || form.Asset.ID == "" && action.Value == configurationKeep).Clicked() {
					form.Actions[index] = action.Value
					form.secret()[index] = ""
					form.Dirty = true
				}
			}
		})
		if form.Actions[index] == configurationReplace {
			mark(ui.TextInput(context.Key(fmt.Sprintf("configuration-replacement-%d", index)), &form.secret()[index]).Label("新凭据（仅内存）").Password().Disabled(model.Busy).FillWidth().Changed())
		}
	}
	if form.NeedsCredential {
		ui.Text(context, "此资产需要凭据；无秘密的副本不能直接应用。")
	}
	if form.PreservedExtras {
		ui.Text(context, "服务端报告存在保留扩展；不在本编辑器覆盖其 ownership。")
	}
	if form.Dirty {
		ui.Text(context, "未保存草稿；保存不会应用到本机。")
	}
	ui.Row(context).Gap(8).Children(func() {
		if ui.PrimaryButton(context.Key("configuration-save"), "仅保存服务端资产").Disabled(model.Busy || form.Name == "").Clicked() {
			model.SaveConfiguration()
		}
		if ui.Button(context.Key("configuration-copy"), "复制（不带秘密）").Disabled(model.Busy || form.Dirty || form.Asset.ID == "").Clicked() {
			model.CopyConfiguration()
		}
	})
	ui.Row(context).Gap(8).Children(func() {
		if ui.Button(context.Key("configuration-reload-draft"), "重新载入并放弃草稿…").Disabled(model.Busy || form.Asset.ID == "").Clicked() {
			model.configurationState().PendingAction = "select:" + form.Asset.ID
		}
		if ui.Button(context.Key("configuration-save-as-new"), "另存为新资产（不带秘密）").Disabled(model.Busy || form.Asset.ID == "").Clicked() {
			form.Asset.ID, form.Asset.Revision = "", 0
			form.SecretPresent = [2]bool{}
			form.clearSecrets()
			form.Actions = [2]configurationSecretAction{configurationClear, configurationClear}
			form.Dirty = true
			model.SaveConfiguration()
		}
	})
}

func (model *Model) configurationModals(context *ui.Context) {
	state := model.configurationState()
	if state.PendingPage >= 0 || state.PendingAction != "" {
		open := true
		ui.Modal(context.Key("configuration-dirty-modal"), &open, func() {
			ui.Column(context).Padding(20).Gap(10).Children(func() {
				ui.Text(context, "放弃未保存草稿？").Bold()
				if ui.Button(context.Key("configuration-keep-draft"), "保留草稿").Clicked() {
					open = false
				}
				if ui.Button(context.Key("configuration-discard"), "放弃草稿并继续").Clicked() {
					page, action := state.PendingPage, state.PendingAction
					state.PendingPage, state.PendingAction = -1, ""
					if state.Form != nil {
						state.Form.clearSecrets()
						state.Form.Dirty = false
					}
					if page >= 0 {
						model.configurationNavigate(page)
					} else if action == "new" {
						model.NewConfiguration()
					} else if action == "reload" {
						model.LoadConfigurations()
					} else if len(action) > 7 && action[:7] == "select:" {
						model.SelectConfiguration(action[7:])
					}
				}
			})
		})
		if !open {
			state.PendingPage, state.PendingAction = -1, ""
		}
	}
	if state.ConfirmDelete {
		ui.Modal(context.Key("configuration-delete-modal"), &state.ConfirmDelete, func() {
			ui.Column(context).Padding(20).Gap(10).Children(func() {
				ui.Text(context, "仅删除所选服务端资产；本机文件不变。确认？")
				if ui.Button(context.Key("configuration-delete-cancel"), "取消").Clicked() {
					state.ConfirmDelete = false
				}
				if ui.Button(context.Key("configuration-delete-confirm"), "确认删除资产").Clicked() {
					model.DeleteConfiguration()
				}
			})
		})
	}
	prompt := state.Prompt
	if prompt == nil {
		return
	}
	context.After(time.Second)
	open := true
	ui.Modal(context.Key("configuration-approval-modal"), &open, func() {
		ui.Scroll(context.Key("configuration-approval-scroll")).MaxHeight(560).Children(func() {
			ui.Column(context).Padding(20).Gap(8).Children(func() {
				plan := prompt.Plan
				ui.Text(context, "本次安全操作预览（非文件全文差异）").Bold()
				if ui.Button(context.Key("configuration-deny"), "拒绝本次操作").Clicked() {
					model.ReplyConfigurationApproval(false)
				}
				if plan.Kind == "restore" {
					ui.Text(context, "离线恢复：原身份绑定由既有加密 journal 校验；当前登录身份不提供恢复授权。")
				}
				for _, line := range []string{"当前 origin: " + prompt.Origin, "当前 owner: " + prompt.Owner, "当前 profile: " + prompt.Profile, fmt.Sprintf("asset: %s / revision %d", plan.AssetID, plan.AssetRevision), "operation: " + plan.OperationID, "target: claude.settings / user", "adapter: " + plan.AdapterID + " / " + plan.AdapterVersion + " / " + plan.AdapterDigest, "readiness: " + plan.Readiness, "source operation: " + plan.SourceOperation, "批准截止: " + prompt.Deadline.UTC().Format(time.RFC3339), fmt.Sprintf("收紧权限: %t · 可恢复缺失窗口: %t", plan.TightenPermissions, plan.RecoverableAbsentWindow)} {
					ui.Text(context, line)
				}
				for _, operation := range plan.Operations {
					ui.Text(context, string(operation.Slot)+" → "+string(operation.Action))
				}
				for index, slot := range prompt.Slots {
					ui.Checkbox(context.Key(fmt.Sprintf("configuration-approve-slot-%d", index)), &prompt.Checked[index], "仅授权本次敏感槽 "+string(slot))
				}
				ui.Text(context, "即使零敏感槽也必须最终批准；已消费不等于本机应用成功。")
				all := time.Now().Before(prompt.Deadline)
				for _, checked := range prompt.Checked {
					all = all && checked
				}
				if ui.PrimaryButton(context.Key("configuration-final-approve"), "最终批准本次 exact 操作").Disabled(!all).Clicked() {
					model.ReplyConfigurationApproval(true)
				}
			})
		})
	})
	if !open {
		model.ReplyConfigurationApproval(false)
	}
}

func configurationRemoteStatus(outcome configurationOutcome) string {
	switch outcome.RemoteState {
	case "not-issued", "issued", "consumed", "expired", "retired":
		return string(outcome.RemoteState)
	default:
		return "未核实"
	}
}
