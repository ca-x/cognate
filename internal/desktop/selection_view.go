package desktop

import (
	"fmt"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/egoist/mygo/ui"
)

func (model *Model) importView(context *ui.Context) {
	state := model.selectionState()
	ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("selection-mode"), "选择导入").Selected(!state.Legacy).Clicked() {
			model.cancelJob()
			state.Legacy = false
		}
		if ui.Button(context.Key("legacy-mode"), "旧版 Magpie（仅仓库）").Selected(state.Legacy).Clicked() {
			model.cancelJob()
			state.Legacy = true
		}
	})
	if state.Legacy {
		model.legacyImportView(context)
		return
	}
	ui.Scroll(context.Key("selection-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(context).FillWidth().Gap(context.Theme().Space(3)).Children(func() {
			ui.Text(context, "整个文件可能含凭据").Bold()
			ui.Text(context, "明确授权后才读取并上传；默认不选任何候选，不在本机安装或应用。")
			ui.Text(context, "上传到: "+model.origin)
			ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
				for _, source := range []struct {
					value importv1.ImportSelectionSource
					label string
				}{{1, "cc-switch schema24"}, {2, "Magpie JSON"}, {3, "npx skills v3"}} {
					if ui.Button(context.Key(fmt.Sprintf("selection-source-%d", source.value)), source.label).Selected(state.Source == source.value).Disabled(model.Busy).Clicked() {
						state.Source = source.value
						model.SelectionFileChanged()
					}
				}
			})
			ui.Text(context, selectionSourceLabel(state.Source)+" · 上限：cc-switch 16 MiB，其它来源 1 MiB；不扫描默认目录、不读取活动 SQLite/WAL。")
			if ui.TextInput(context.Key("selection-file"), &state.Path).Label("导出文件绝对路径").Placeholder("明确导出、自包含的私有 regular file").FillWidth().Disabled(model.Busy).Changed() {
				model.SelectionFileChanged()
			}
			ui.Checkbox(context.Key("selection-upload"), &state.UploadApproved, "授权整个文件上传到此 origin").Disabled(model.Busy)
			if ui.Button(context.Key("selection-preview"), "预览文件").Disabled(model.Busy || model.pendingJobs.Load() > 0 || !state.UploadApproved || model.session.value() == "").Clicked() {
				model.PreviewSelection()
			}
			if state.Hash != "" {
				ui.Text(context, "文件 SHA256: "+state.Hash)
			}
			if state.Owner != "" {
				ui.Text(context, "实际 GetSession owner: "+state.Owner)
			}
			if state.View != nil {
				model.selectionCandidatesView(context)
			}
			if state.Review != nil {
				ui.Column(context.Key("selection-final-review")).FillWidth().Gap(8).Padding(12).Background(context.Theme().Surface).Border(1, context.Theme().Border).Radius(context.Theme().Radius).Children(func() {
					ui.Text(context, "最终审阅").FontSize(context.Theme().Rem(1.2)).Bold()
					for _, line := range state.Review.Lines {
						ui.Text(context, line)
					}
					ui.Checkbox(context.Key("selection-final-approve"), &state.FinalApproved, "我确认此审阅，提交所选私有资产与内容引用").Disabled(model.Busy)
					if ui.PrimaryButton(context.Key("selection-commit"), "明确提交一次").Disabled(model.Busy || !state.FinalApproved).Clicked() {
						model.CommitSelection()
					}
				})
			}
			ui.Text(context, "操作状态与恢复").FontSize(context.Theme().Rem(1.2)).Bold()
			ui.Text(context, "提交后断网、取消或离开不等于回滚。结果不明只查原 ID，退出前请保存非秘密 operation ID；重启/重新登录不是幂等证明。")
			for _, barrier := range model.selectionBarriers {
				ui.Text(context, "待核实: "+barrier.Origin+" / "+barrier.Owner+" / "+barrier.ID)
				if barrier.Origin == model.origin && ui.Button(context.Key("selection-use-"+barrier.ID), "使用原操作 ID "+barrier.ID).Clicked() {
					state.OperationID = barrier.ID
				}
			}
			ui.TextInput(context.Key("selection-operation"), &state.OperationID).Label("非秘密 operation ID（32位小写hex）").FillWidth().Disabled(model.Busy)
			if ui.Button(context.Key("selection-status"), "只读查询状态（不上传文件）").Disabled(model.Busy || state.OperationID == "" || model.session.value() == "").Clicked() {
				model.QuerySelectionStatus()
			}
			if state.Result.OperationID != "" {
				for _, line := range selectionResultLines(state.Result) {
					ui.Text(context, line)
				}
			}
		})
	})
}

func (model *Model) selectionCheckbox(context *ui.Context, kind, index int, label string, status importv1.ImportCandidateStatus) {
	state := model.selectionState()
	checked := index < len(state.Checked[kind]) && state.Checked[kind][index]
	if ui.Checkbox(context.Key(fmt.Sprintf("selection-choice-%d-%d", kind, index)), &checked, label).Disabled(model.Busy || state.Preview == nil || status != 1 || kind == 1 && !state.bindingConfirmed(index)).Changed() {
		model.ChooseSelection(kind, index, checked)
	}
}

func (model *Model) selectionCandidatesView(context *ui.Context) {
	state := model.selectionState()
	ui.Text(context, "1 · 仓库").Bold()
	for index, entry := range state.View.Repositories {
		model.selectionCheckbox(context, 0, index, fmt.Sprintf("仓库 %d · %s @ %s · %s", index+1, entry.Hint.Repository, entry.Hint.Branch, selectionStatusLabel(entry.Status)), entry.Status)
	}
	ui.Text(context, "2 · 技能内容引用（不是本地安装）").Bold()
	for index, entry := range state.View.Skills {
		model.selectionCheckbox(context, 1, index, fmt.Sprintf("技能 %d · %s · %s", index+1, entry.SafeName, selectionStatusLabel(entry.Status)), entry.Status)
		if entry.Binding != nil {
			ui.Text(context, entry.Binding.Repository+" @ "+entry.Binding.Branch+" / "+entry.Binding.LogicalPath)
			ui.Text(context, "archive "+entry.Binding.ArchiveHash+" · content "+entry.Binding.ContentHash)
		}
		if ui.Button(context.Key(fmt.Sprintf("selection-bind-load-%d", index)), fmt.Sprintf("读取技能 %d 的同仓库内容", index+1)).Disabled(model.Busy || state.Preview == nil || entry.Status != 1 && entry.Status != 3 && entry.Status != 4).Clicked() {
			model.LoadSelectionBindings(index)
		}
	}
	if state.BindingSkill >= 0 {
		ui.Text(context, "核对完整身份后选择绑定；不会按名称自动匹配，Git tree SHA 不是内容 SHA256。")
		if len(state.Bindings) == 0 {
			ui.Text(context, "没有可绑定内容：先仅选择仓库并提交，再重新授权预览与绑定。")
		}
		for index, binding := range state.Bindings {
			ui.Column(context.Key(fmt.Sprintf("selection-bind-%d", index))).Gap(4).Children(func() {
				ui.Text(context, binding.Repository+" @ "+binding.Branch+" · "+binding.LogicalPath)
				ui.Text(context, "repository ID "+binding.RepositoryId+" · skill ID "+binding.SkillId)
				ui.Text(context, "archive "+binding.ArchiveHash)
				ui.Text(context, "content "+binding.ContentHash)
				label := fmt.Sprintf("明确绑定内容 %d 并使旧预览失效", index+1)
				if state.Source == 2 {
					label = fmt.Sprintf("明确核对 Magpie 固定目录内容 %d", index+1)
				}
				if ui.Button(context.Key(fmt.Sprintf("selection-bind-choose-%d", index)), label).Disabled(model.Busy || state.Preview == nil).Clicked() {
					model.BindSelection(index)
				}
			})
		}
	}
	ui.Text(context, "3 · 配置私有资产（不迁移来源秘密）").Bold()
	for index, entry := range state.View.Configurations {
		model.selectionCheckbox(context, 2, index, fmt.Sprintf("配置 %d · %s / %s · %s", index+1, entry.KnownAgentId, entry.SafeName, selectionStatusLabel(entry.Status)), entry.Status)
		for _, line := range selectionDraftSummary(entry) {
			ui.Text(context, line)
		}
		if index < len(state.Configurations) && (entry.Status == 1 || entry.Status == 2 || entry.Status == 8) {
			ui.Column(context.Key(fmt.Sprintf("selection-config-%d", index))).FillWidth().Gap(8).Children(func() { model.selectionConfigurationView(context, &state.Configurations[index]) })
		}
	}
	ui.Text(context, "排除项与未比较提示（每项都需确认）").Bold()
	for index := range state.Notices {
		notice := &state.Notices[index]
		if ui.Checkbox(context.Key("selection-notice-"+notice.ID), &notice.Checked, fmt.Sprintf("确认提示 %d：%s", index+1, notice.Label)).Disabled(model.Busy || state.Preview == nil).Changed() {
			state.Review = nil
			state.FinalApproved = false
		}
	}
	if ui.Button(context.Key("selection-review"), "审阅明确选择").Disabled(model.Busy || state.Preview == nil).Clicked() {
		model.ReviewSelection()
	}
}

func (model *Model) selectionConfigurationView(context *ui.Context, form *selectionConfiguration) {
	changed := ui.Checkbox(context.Key("rename"), &form.Rename, "明确配置目标改名").Disabled(model.Busy).Changed()
	if form.Rename {
		changed = ui.TextInput(context.Key("name"), &form.Name).Label("新配置名称").FillWidth().Disabled(model.Busy).Changed() || changed
	}
	ui.Row(context).FillWidth().Wrap().Gap(6).Children(func() {
		if form.Agent == "claude" {
			for mode := range 4 {
				value := configurationv1.ClaudeAuthMode(mode)
				label := []string{"Claude 保留来源解释", "Claude native-login", "Claude API key", "Claude auth token"}[mode]
				if ui.Button(context.Key(fmt.Sprintf("claude-%d", mode)), label).Selected(form.Claude == value).Disabled(model.Busy).Clicked() {
					form.Claude = value
					changed = true
				}
			}
		}
		if form.Agent == "gemini" {
			for mode := range 3 {
				value := configurationv1.GeminiAuthMode(mode)
				label := []string{"Gemini 保留来源解释", "Gemini native-login", "Gemini API key"}[mode]
				if ui.Button(context.Key(fmt.Sprintf("gemini-%d", mode)), label).Selected(form.Gemini == value).Disabled(model.Busy).Clicked() {
					form.Gemini = value
					changed = true
				}
			}
			for mode := range 3 {
				value := configurationv1.GeminiKeyTransport(mode)
				label := []string{"Gemini transport 未指定", "Gemini native-default", "Gemini bearer"}[mode]
				if ui.Button(context.Key(fmt.Sprintf("transport-%d", mode)), label).Selected(form.Transport == value).Disabled(model.Busy).Clicked() {
					form.Transport = value
					changed = true
				}
			}
		}
	})
	ui.Text(context, "缺失、native-login 与 API key 不等同；修改后必须重新预览。来源未支持的组合由服务端拒绝。")
	if changed {
		model.SelectionOptionsChanged()
	}
}
