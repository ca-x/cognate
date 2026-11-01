package desktop

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/mcpclient"
	"github.com/egoist/mygo/ui"
)

func mcpField(context *ui.Context, key any, value *string, label string, disabled, password bool) bool {
	ui.Text(context, label+"：")
	field := ui.TextInput(context.Key(key), value).Label(label).FillWidth().Disabled(disabled)
	if password {
		field.Password()
	}
	return field.Changed()
}

func (model *Model) mcpWorkspaceTabs(context *ui.Context) {
	ui.Row(context).Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("mcp-workspace-provider"), "Claude provider").Clicked() {
			model.mcpWorkspace(false)
		}
		if ui.Button(context.Key("mcp-workspace-assets"), "MCP 连接配置").Clicked() {
			model.mcpWorkspace(true)
		}
	})
}

func (model *Model) mcpView(context *ui.Context) {
	state := model.mcpState()
	ui.Row(context).Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("mcp-language"), model.mcpText("English", "中文")).Clicked() {
			configuration := model.configurationState()
			configuration.MCPEnglish = !configuration.MCPEnglish
		}
		if ui.Button(context.Key("mcp-load"), model.mcpText("载入 / 核实 MCP 资产", "Load / reconcile MCP assets")).Disabled(model.Busy || model.session.value() == "").Clicked() {
			model.mcpRequest("reload")
		}
		if ui.Button(context.Key("mcp-new"), model.mcpText("新建 MCP 连接配置", "New MCP configuration")).Disabled(model.Busy || state.Blocked || state.session() == nil).Clicked() {
			model.mcpRequest("new")
		}
	})
	ui.Scroll(context.Key("mcp-scroll")).Label("MCP editor area").Grow(1).MinHeight(0).TrackScroll(&state.Scroll).Children(func() {
		ui.Column(context).FillWidth().Gap(10).Children(func() {
			ui.Text(context, model.mcpText("MCP 连接配置 · 私有服务端资产", "MCP connections · private server assets")).Bold()
			ui.Text(context, model.mcpText("仅管理 Claude schema1。不会启动 MCP、执行命令、连接声明 URL 或应用本机。", "Claude schema1 only. Never starts MCP, executes commands, connects to declared URLs or applies locally."))
			if model.session.value() == "" {
				ui.Text(context, model.mcpText("请先在账号页登录。不会发现本机凭据。", "Sign in on the account page. No local credential discovery."))
			}
			if state.Blocked {
				ui.Text(context, model.mcpText("本次提交已消费，禁止重放。显式载入核实不会重发；名称相同不是原操作结果证明。", "Submission consumed; no replay. Explicit reconciliation never resends. A matching name is not an outcome receipt."))
			}
			if len(state.Assets) > 0 {
				state.List.Selected = &state.ListIndex
				state.List.Key = func(index int) any { return state.Assets[index].ID }
				state.List.Label = func(index int) string { return state.Assets[index].Name }
				list := ui.List(context.Key("mcp-assets"), &state.List, len(state.Assets)).Label(model.mcpText("MCP 资产列表", "MCP asset list")).Height(140).Rows(func(row ui.ListRow) {
					asset := state.Assets[row.Index]
					ui.Text(row.Context, fmt.Sprintf("%s · %s · schema %d · revision %d", asset.Name, asset.Agent, asset.Schema, asset.Revision))
				})
				if list.Changed() && !model.Busy && state.ListIndex >= 0 && state.ListIndex < len(state.Assets) {
					model.mcpRequest(state.Assets[state.ListIndex].ID)
				}
			} else if state.session() != nil {
				ui.Text(context, model.mcpText("当前所有者没有 MCP 连接资产。", "No MCP connection assets for the current owner."))
			}
			if state.Form != nil {
				model.mcpFormView(context, state, state.Form)
			}
			if state.Preview != "" {
				ui.Text(context, model.mcpText("只读逻辑预览 · can_apply=false / applied=false", "Read-only logical preview · can_apply=false / applied=false")).Bold()
				for line := range strings.SplitSeq(state.Preview, "\n") {
					ui.Text(context, line)
				}
			}
		})
	})
}

func (model *Model) mcpFormView(context *ui.Context, state *mcpEditorState, form *mcpForm) {
	ui.Divider(context)
	ui.Text(context, fmt.Sprintf("%s · %s · revision %d", form.Metadata.Name, form.Metadata.ID, form.Metadata.Revision)).Bold()
	if form.ReadOnly {
		ui.Text(context, model.mcpText("未知版本、扩展或详情未验证：仅只读元数据，禁止覆盖、复制、删除。", "Unknown schema, extensions or unverified details: read-only metadata; no overwrite, copy or delete."))
	}
	if form.Dirty {
		ui.Text(context, model.mcpText("未保存草稿。离开前需明确放弃；新秘密离开或预览后清除。", "Unsaved draft. Leaving requires explicit discard; new secrets clear on leaving or preview."))
	}
	disabled := model.Busy || state.Blocked || form.ReadOnly || state.Review != nil
	if mcpField(context, "mcp-name", &form.Draft.Name, model.mcpText("资产名称", "Asset name"), disabled, false) {
		form.Dirty = true
	}
	ui.Text(context, model.mcpText("服务声明有序，最多 32 个。所有参数、环境变量与 Header 的值均为凭据引用；命令和 URL 不是秘密容器。", "Ordered declarations, up to 32 servers. All argument, environment and header values are credential refs; commands and URLs are not secret containers."))
	state.ServerList.Selected = &form.Selected
	state.ServerList.Key = func(index int) any { return index }
	state.ServerList.Label = func(index int) string { return fmt.Sprintf("%d %s", index+1, form.Draft.Servers[index].Name) }
	ui.List(context.Key("mcp-servers"), &state.ServerList, len(form.Draft.Servers)).Label(model.mcpText("有序服务", "Ordered servers")).Height(100).Rows(func(row ui.ListRow) {
		server := form.Draft.Servers[row.Index]
		ui.Text(row.Context, fmt.Sprintf("%d · %s · %s · enabled=%t", row.Index+1, server.ID, server.Name, server.Enabled))
	})
	ui.Row(context).Wrap().Gap(6).Children(func() {
		if ui.Button(context.Key("mcp-server-add"), model.mcpText("添加 stdio 服务", "Add stdio server")).Disabled(disabled || len(form.Draft.Servers) >= 32).Clicked() {
			form.Draft.Servers = append(form.Draft.Servers, mcpclient.Server{Stdio: &mcpclient.Stdio{}})
			form.Selected, form.Dirty = len(form.Draft.Servers)-1, true
		}
		selected := form.Selected >= 0 && form.Selected < len(form.Draft.Servers)
		if ui.Button(context.Key("mcp-server-up"), model.mcpText("服务上移", "Move server up")).Disabled(disabled || !selected || form.Selected == 0).Clicked() {
			form.Draft.Servers[form.Selected], form.Draft.Servers[form.Selected-1] = form.Draft.Servers[form.Selected-1], form.Draft.Servers[form.Selected]
			form.Selected--
			form.Dirty = true
		}
		if ui.Button(context.Key("mcp-server-down"), model.mcpText("服务下移", "Move server down")).Disabled(disabled || !selected || form.Selected == len(form.Draft.Servers)-1).Clicked() {
			form.Draft.Servers[form.Selected], form.Draft.Servers[form.Selected+1] = form.Draft.Servers[form.Selected+1], form.Draft.Servers[form.Selected]
			form.Selected++
			form.Dirty = true
		}
		if ui.Button(context.Key("mcp-server-remove"), model.mcpText("移除服务声明", "Remove server declaration")).Disabled(disabled || !selected).Clicked() {
			form.Draft.Servers = slices.Delete(form.Draft.Servers, form.Selected, form.Selected+1)
			form.Selected, form.Dirty = min(form.Selected, len(form.Draft.Servers)-1), true
		}
	})
	if form.Selected >= 0 && form.Selected < len(form.Draft.Servers) {
		model.mcpServerView(context, state, form, &form.Draft.Servers[form.Selected], disabled)
	}
	model.mcpSecretsView(context, state, form, disabled)
	ui.Divider(context)
	ui.Row(context).Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("mcp-preview"), model.mcpText("仅逻辑预览", "Logical preview only")).Disabled(disabled).Clicked() {
			model.PrepareMCP("preview")
		}
		action := "update"
		if form.Metadata.ID == "" {
			action = "create"
		}
		if ui.PrimaryButton(context.Key("mcp-prepare"), model.mcpText("审阅服务端保存…", "Review server save…")).Disabled(disabled).Clicked() {
			model.PrepareMCP(action)
		}
	})
	mcpField(context, "mcp-copy-name", &state.CopyName, model.mcpText("无密钥副本名称", "Keyless copy name"), disabled || form.Metadata.ID == "", false)
	ui.Row(context).Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("mcp-copy"), model.mcpText("审阅无密钥复制…", "Review keyless copy…")).Disabled(disabled || form.Dirty || form.Metadata.ID == "").Clicked() {
			model.PrepareMCP("copy")
		}
		if ui.Button(context.Key("mcp-delete"), model.mcpText("审阅删除资产…", "Review asset deletion…")).Disabled(disabled || form.Dirty || form.Metadata.ID == "").Clicked() {
			model.PrepareMCP("delete")
		}
	})
}

func (model *Model) mcpServerView(context *ui.Context, state *mcpEditorState, form *mcpForm, server *mcpclient.Server, disabled bool) {
	mark := func(changed bool) { form.Dirty = form.Dirty || changed }
	mark(mcpField(context, "mcp-server-id", &server.ID, model.mcpText("服务 ID（显式、唯一）", "Server ID (explicit, unique)"), disabled, false))
	mark(mcpField(context, "mcp-server-name", &server.Name, model.mcpText("服务名称", "Server name"), disabled, false))
	mark(ui.Checkbox(context.Key("mcp-enabled"), &server.Enabled, model.mcpText("启用此服务声明", "Enable this server declaration")).Disabled(disabled).Changed())
	ui.Row(context).Wrap().Gap(6).Children(func() {
		for _, transport := range []string{"stdio", "HTTP", "SSE"} {
			active := transport == "stdio" && server.Stdio != nil || transport == "HTTP" && server.HTTP != nil || transport == "SSE" && server.SSE != nil
			label := transport
			if active {
				label = "✓ " + label
			}
			if ui.Button(context.Key("mcp-transport-"+transport), label).Disabled(disabled || active).Clicked() {
				server.Stdio, server.HTTP, server.SSE = nil, nil, nil
				switch transport {
				case "stdio":
					server.Stdio = &mcpclient.Stdio{}
				case "HTTP":
					server.HTTP = &mcpclient.Remote{}
				case "SSE":
					server.SSE = &mcpclient.Remote{}
				}
				form.Dirty = true
			}
		}
	})
	if server.Stdio != nil {
		mark(mcpField(context, "mcp-command", &server.Stdio.Command, model.mcpText("命令（仅声明，不执行）", "Command (declaration, never executed)"), disabled, false))
		present := server.Stdio.Args != nil
		if ui.Checkbox(context.Key("mcp-args-present"), &present, model.mcpText("设置 args 集合（含空集合）", "Set args collection (including empty)")).Disabled(disabled).Changed() {
			server.Stdio.Args = nil
			if present {
				server.Stdio.Args = &mcpclient.Arguments{}
			}
			form.Dirty = true
		}
		if server.Stdio.Args != nil {
			model.mcpArgumentsView(context, state, form, server.Stdio.Args, disabled)
		}
		model.mcpNamedView(context, state, form, &server.Stdio.Env, 1, "env", 64, disabled)
	}
	remote := server.HTTP
	if server.SSE != nil {
		remote = server.SSE
	}
	if remote != nil {
		mark(mcpField(context, "mcp-url", &remote.URL, model.mcpText("URL（不支持 query / 用户凭据 / fragment）", "URL (no query, userinfo or fragment)"), disabled, false))
		model.mcpNamedView(context, state, form, &remote.Headers, 2, "headers", 32, disabled)
	}
}

func (model *Model) mcpParameterActions(context *ui.Context, key string, count, limit int, selected *int, disabled bool, mutate func(string)) {
	ui.Row(context).Wrap().Gap(6).Children(func() {
		for _, action := range []struct{ id, chinese, english string }{{"add", "添加项", "Add item"}, {"up", "项上移", "Move item up"}, {"down", "项下移", "Move item down"}, {"remove", "移除项", "Remove item"}} {
			blocked := disabled
			switch action.id {
			case "add":
				blocked = blocked || count >= limit
			case "up":
				blocked = blocked || *selected <= 0 || *selected >= count
			case "down":
				blocked = blocked || *selected < 0 || *selected >= count-1
			case "remove":
				blocked = blocked || *selected < 0 || *selected >= count
			}
			if ui.Button(context.Key("mcp-"+key+"-"+action.id), key+" · "+model.mcpText(action.chinese, action.english)).Disabled(blocked).Clicked() {
				mutate(action.id)
			}
		}
	})
}

func (model *Model) mcpArgumentsView(context *ui.Context, state *mcpEditorState, form *mcpForm, args *mcpclient.Arguments, disabled bool) {
	selected := &state.ParameterIndices[0]
	listState := &state.ParameterLists[0]
	listState.Selected = selected
	listState.Key = func(index int) any { return index }
	listState.Label = func(index int) string { return args.Values[index].ID }
	ui.List(context.Key("mcp-args-list"), listState, len(args.Values)).Label(model.mcpText("有序 args", "Ordered args")).Height(88).Rows(func(row ui.ListRow) {
		ui.Text(row.Context, fmt.Sprintf("%d · %s", row.Index+1, args.Values[row.Index].ID))
	})
	model.mcpParameterActions(context, "args", len(args.Values), 64, selected, disabled, func(action string) {
		switch action {
		case "add":
			args.Values = append(args.Values, mcpclient.Argument{})
			*selected = len(args.Values) - 1
		case "remove":
			args.Values = slices.Delete(args.Values, *selected, *selected+1)
			*selected = min(*selected, len(args.Values)-1)
		case "up":
			args.Values[*selected], args.Values[*selected-1] = args.Values[*selected-1], args.Values[*selected]
			*selected--
		case "down":
			args.Values[*selected], args.Values[*selected+1] = args.Values[*selected+1], args.Values[*selected]
			*selected++
		}
		form.Dirty = true
	})
	if *selected >= 0 && *selected < len(args.Values) {
		if mcpField(context, "mcp-arg-id", &args.Values[*selected].ID, model.mcpText("参数 ID（不是参数值）", "Argument ID (not its value)"), disabled, false) {
			form.Dirty = true
		}
	}
}

func (model *Model) mcpNamedView(context *ui.Context, state *mcpEditorState, form *mcpForm, target **mcpclient.NamedSlots, index int, key string, limit int, disabled bool) {
	present := *target != nil
	if ui.Checkbox(context.Key("mcp-"+key+"-present"), &present, key+model.mcpText(" · 设置集合（含空集合）", " · Set collection (including empty)")).Disabled(disabled).Changed() {
		*target = nil
		if present {
			*target = &mcpclient.NamedSlots{}
		}
		form.Dirty = true
	}
	if *target == nil {
		return
	}
	values := *target
	selected := &state.ParameterIndices[index]
	listState := &state.ParameterLists[index]
	listState.Selected = selected
	listState.Key = func(index int) any { return index }
	listState.Label = func(index int) string { return values.Values[index].Name }
	ui.List(context.Key("mcp-"+key+"-list"), listState, len(values.Values)).Label(model.mcpText("有序 ", "Ordered ") + key).Height(88).Rows(func(row ui.ListRow) {
		ui.Text(row.Context, fmt.Sprintf("%d · %s", row.Index+1, values.Values[row.Index].Name))
	})
	model.mcpParameterActions(context, key, len(values.Values), limit, selected, disabled, func(action string) {
		switch action {
		case "add":
			values.Values = append(values.Values, mcpclient.NamedSlot{})
			*selected = len(values.Values) - 1
		case "remove":
			values.Values = slices.Delete(values.Values, *selected, *selected+1)
			*selected = min(*selected, len(values.Values)-1)
		case "up":
			values.Values[*selected], values.Values[*selected-1] = values.Values[*selected-1], values.Values[*selected]
			*selected--
		case "down":
			values.Values[*selected], values.Values[*selected+1] = values.Values[*selected+1], values.Values[*selected]
			*selected++
		}
		form.Dirty = true
	})
	if *selected >= 0 && *selected < len(values.Values) {
		if mcpField(context, "mcp-"+key+"-name", &values.Values[*selected].Name, key+model.mcpText(" 名称（不是值）", " name (not its value)"), disabled, false) {
			form.Dirty = true
		}
	}
}

func (model *Model) mcpSecretsView(context *ui.Context, state *mcpEditorState, form *mcpForm, disabled bool) {
	refs, err := form.syncSlots()
	if err != nil {
		ui.Text(context, model.mcpText("引用预算或标识过长；请修正声明后继续。", "Reference budget or identifier length exceeded; correct the declaration."))
		return
	}
	ui.Divider(context)
	ui.Text(context, model.mcpText("凭据引用 · 不读取旧值", "Credential refs · never reads old values")).Bold()
	ui.Text(context, model.mcpText("重命名、删项或换传输后，原引用必须明确 CLEAR。REPLACE 空值仍为存在，不等于 CLEAR。", "Renaming, removing or changing transport requires explicit CLEAR of old refs. An empty REPLACE is present, not CLEAR."))
	state.SlotList.Selected = &state.SlotIndex
	state.SlotList.Key = func(index int) any { return form.Slots[index].Ref }
	state.SlotList.Label = func(index int) string { return form.Slots[index].Ref }
	ui.List(context.Key("mcp-slots"), &state.SlotList, len(form.Slots)).Label(model.mcpText("凭据引用列表", "Credential ref list")).Height(110).Rows(func(row ui.ListRow) {
		slot := form.Slots[row.Index]
		ui.Text(row.Context, fmt.Sprintf("%s · present=%t", slot.Ref, slot.Present))
	})
	if state.SlotIndex < 0 || state.SlotIndex >= len(form.Slots) {
		ui.Text(context, model.mcpText("选择一个引用以编辑 KEEP / REPLACE / CLEAR。", "Select a ref to edit KEEP / REPLACE / CLEAR."))
		return
	}
	slot := form.Slots[state.SlotIndex]
	current := slices.Contains(refs, slot.Ref)
	ui.Text(context, slot.Ref)
	if !current {
		ui.Text(context, model.mcpText("已移除的原引用：保存前必须明确 CLEAR 并确认。", "Removed original ref: explicit confirmed CLEAR required before saving."))
	}
	ui.Row(context).Wrap().Gap(6).Children(func() {
		for _, action := range []struct {
			value mcpclient.SecretAction
			label string
		}{{mcpclient.Keep, "KEEP"}, {mcpclient.Replace, "REPLACE"}, {mcpclient.Clear, "CLEAR"}} {
			label := action.label
			if slot.Action == action.value {
				label = "✓ " + label
			}
			if ui.Button(context.Key("mcp-slot-"+action.label), label).Disabled(disabled || !current && action.value != mcpclient.Clear).Clicked() {
				slot.clearValue()
				slot.Action, slot.Confirmed = action.value, false
				slot.ValueSet = action.value == mcpclient.Replace
				form.Dirty = true
			}
		}
	})
	if slot.Action == mcpclient.Replace && current {
		if !slot.ValueSet {
			ui.Text(context, model.mcpText("新值已清除。重新选择 REPLACE 明确允许空值，或重新输入。", "New value cleared. Select REPLACE again to explicitly allow empty, or enter a value."))
		}
		key := struct {
			slot  *mcpSlotForm
			epoch uint64
		}{slot, slot.Epoch}
		if mcpField(context, key, slot.Value.input(), model.mcpText("新值（仅内存，可为空）", "New value (memory only, may be empty)"), disabled, true) {
			slot.ValueSet = true
			form.Dirty = true
		}
	}
	if slot.Action == mcpclient.Clear {
		if ui.Checkbox(context.Key("mcp-clear-confirm"), &slot.Confirmed, model.mcpText("明确清除此服务端凭据槽", "Explicitly clear this server credential slot")).Disabled(disabled).Changed() {
			form.Dirty = true
		}
	}
}

func (model *Model) mcpModals(context *ui.Context) {
	state := model.mcpState()
	if state.Pending != "" || state.PendingPage >= 0 || state.PendingWorkspace >= 0 {
		open := true
		ui.Modal(context.Key("mcp-discard-modal"), &open, func() {
			ui.Column(context).Padding(16).Gap(8).Children(func() {
				ui.Text(context, model.mcpText("放弃未保存的连接草稿？", "Discard the unsaved configuration draft?")).Bold()
				ui.Text(context, model.mcpText("新秘密已清除；保留草稿也需要重新输入 REPLACE。", "New secrets are cleared; retaining the draft requires re-entering REPLACE values."))
				if ui.Button(context.Key("mcp-keep"), model.mcpText("保留草稿", "Keep draft")).Clicked() {
					open = false
				}
				if ui.Button(context.Key("mcp-discard"), model.mcpText("放弃并继续", "Discard and continue")).Clicked() {
					action, page, workspace := state.Pending, state.PendingPage, state.PendingWorkspace
					state.Pending, state.PendingPage, state.PendingWorkspace = "", -1, -1
					if state.Form != nil {
						state.Form.clearSecrets()
						state.Form.Dirty = false
					}
					configuration := model.configurationState()
					if workspace >= 0 {
						if configuration.Form != nil {
							configuration.Form.clearSecrets()
							configuration.Form.Dirty = false
						}
						model.mcpWorkspace(workspace == 1)
					} else if page >= 0 {
						model.configurationNavigate(page)
					} else {
						model.mcpAction(action)
					}
				}
			})
		})
		if !open {
			state.Pending, state.PendingPage, state.PendingWorkspace = "", -1, -1
		}
	}
	if state.Review == nil {
		return
	}
	open := true
	ui.Modal(context.Key("mcp-review-modal"), &open, func() {
		ui.Column(context).Padding(16).Gap(8).Children(func() {
			ui.Text(context, model.mcpText("本次 exact MCP 资产审阅", "Review this exact MCP asset operation")).Bold()
			_, height := context.Size()
			ui.Scroll(context.Key("mcp-review-scroll")).Label("MCP review content").Height(min(float32(300), max(float32(70), height-context.Theme().Rem(25)))).TrackScroll(&state.ReviewScroll).Children(func() {
				ui.Column(context).Gap(6).Children(func() {
					for line := range strings.SplitSeq(state.Review.Summary, "\n") {
						ui.Text(context, line)
					}
					for index, slot := range state.Review.Slots {
						ui.Checkbox(context.Key(fmt.Sprintf("mcp-authorize-%d", index)), &state.Authorized[index], model.mcpText("仅批准本次槽操作 ", "Approve only this slot action ")+slot)
					}
				})
			})
			ui.Text(context, model.mcpText("完整非秘密声明在上方。不会执行或应用；无密钥复制不复制凭据。", "Full nonsecret declaration above. Never executes or applies; keyless copies omit all credentials."))
			mcpField(context, "mcp-confirmation", &state.Confirmation, state.Review.Confirmation, false, false)
			all := state.Confirmation == state.Review.Confirmation
			for _, authorized := range state.Authorized {
				all = all && authorized
			}
			ui.Row(context).Wrap().Gap(8).Children(func() {
				if ui.Button(context.Key("mcp-review-cancel"), model.mcpText("取消本次审阅", "Cancel this review")).Clicked() {
					model.CancelMCPReview()
				}
				if ui.PrimaryButton(context.Key("mcp-submit"), model.mcpText("确认且仅提交一次", "Confirm and submit once")).Disabled(!all || model.Busy).Clicked() {
					model.SubmitMCP()
				}
			})
		})
	})
	if !open {
		model.CancelMCPReview()
	}
}
