package desktop

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/importclient"
	"google.golang.org/protobuf/proto"
)

type selectionConfiguration struct {
	selectionPrivate
	ID, Agent, Name string
	Rename          bool
	Claude          configurationv1.ClaudeAuthMode
	Gemini          configurationv1.GeminiAuthMode
	Transport       configurationv1.GeminiKeyTransport
}

type selectionNotice struct {
	ID, Label string
	Checked   bool
}

type selectionReview struct {
	selectionPrivate
	Lines   []string
	Choice  importclient.SelectionChoice
	Preview *importclient.SelectionPreview
}

func selectionSourceLabel(source importv1.ImportSelectionSource) string {
	switch source {
	case 1:
		return "cc-switch schema24 自包含 SQLite"
	case 2:
		return "Magpie library.json"
	case 3:
		return "npx skills v3 global lock"
	default:
		return "未支持的来源"
	}
}

func selectionStatusLabel(status importv1.ImportCandidateStatus) string {
	labels := []string{"无效", "READY · 可选择", "需明确配置选项", "需先收录仓库", "需显式绑定内容", "不支持的 agent", "不支持的种类", "不支持的字段", "名称冲突"}
	if int(status) < 0 || int(status) >= len(labels) {
		return "未知状态（不可选）"
	}
	return labels[status]
}

func selectionNoticeLabel(notice *importv1.ImportNotice) string {
	labels := []string{"无效提示", "来源凭据不迁移，仅显示存在位", "元数据排除", "其它来源数据排除", "来源本地内容未比较，保存服务器内容引用", "未知来源区段排除"}
	if notice == nil || notice.Code < 1 || int(notice.Code) >= len(labels) {
		return "未知提示（不能批准）"
	}
	return fmt.Sprintf("%s · %d 项", labels[notice.Code], notice.Count)
}

func selectionAllNotices(view *importv1.PreviewImportSelectionResponse) []*importv1.ImportNotice {
	if view == nil {
		return nil
	}
	notices := slices.Clone(view.Notices)
	for _, skill := range view.Skills {
		notices = append(notices, skill.Notices...)
	}
	for _, configuration := range view.Configurations {
		notices = append(notices, configuration.Notices...)
	}
	return notices
}

func cloneSelectionOptions(options []*importv1.ImportCandidateOptions) []*importv1.ImportCandidateOptions {
	var result []*importv1.ImportCandidateOptions
	for _, option := range options {
		result = append(result, proto.Clone(option).(*importv1.ImportCandidateOptions))
	}
	return result
}

func selectionOptionsEqual(first, second []*importv1.ImportCandidateOptions) bool {
	return slices.EqualFunc(first, second, func(left, right *importv1.ImportCandidateOptions) bool { return proto.Equal(left, right) })
}

func (state *selectionState) options() []*importv1.ImportCandidateOptions {
	options := cloneSelectionOptions(state.SkillOptions)
	for _, form := range state.Configurations {
		option := &importv1.ImportCandidateOptions{CandidateId: form.ID}
		if form.Rename {
			option.Name = new(form.Name)
		}
		if form.Claude != 0 {
			option.Auth = &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: form.Claude}
		}
		if form.Gemini != 0 {
			option.Auth = &importv1.ImportCandidateOptions_GeminiAuthMode{GeminiAuthMode: form.Gemini}
		}
		if form.Transport != 0 {
			option.GeminiKeyTransport = new(form.Transport)
		}
		if option.Name != nil || option.Auth != nil || option.GeminiKeyTransport != nil {
			options = append(options, option)
		}
	}
	return options
}

func (state *selectionState) invalidate() {
	if state.Preview != nil {
		state.Preview.Close()
		state.Preview = nil
	}
	state.Choice = importclient.SelectionChoice{}
	state.Checked = [3][]bool{}
	state.Notices = nil
	state.Review = nil
	state.FinalApproved = false
}

func (state *selectionState) load(preview *importclient.SelectionPreview, source importv1.ImportSelectionSource, file string, options []*importv1.ImportCandidateOptions) {
	state.invalidate()
	state.Preview = preview
	state.Hash = preview.SHA256()
	state.PreviewPath, state.PreviewSource = file, source
	state.FrozenOptions = cloneSelectionOptions(options)
	state.View = preview.View()
	state.Configurations = nil
	for _, entry := range state.View.Configurations {
		form := selectionConfiguration{ID: entry.Id, Agent: entry.KnownAgentId, Name: entry.SafeName}
		for _, option := range options {
			if option.CandidateId != entry.Id {
				continue
			}
			if option.Name != nil {
				form.Rename = true
				form.Name = *option.Name
			}
			form.Claude, form.Gemini = option.GetClaudeAuthMode(), option.GetGeminiAuthMode()
			if option.GeminiKeyTransport != nil {
				form.Transport = *option.GeminiKeyTransport
			}
		}
		state.Configurations = append(state.Configurations, form)
	}
	state.Checked = [3][]bool{make([]bool, len(state.View.Repositories)), make([]bool, len(state.View.Skills)), make([]bool, len(state.View.Configurations))}
	for _, notice := range selectionAllNotices(state.View) {
		state.Notices = append(state.Notices, selectionNotice{ID: notice.Id, Label: selectionNoticeLabel(notice)})
	}
	state.Bindings = nil
	state.BindingSkill = -1
}

func (model *Model) SelectionOptionsChanged() {
	state := model.selectionState()
	if model.Busy || model.pendingJobs.Load() > 0 {
		return
	}
	state.invalidate()
	model.Status = "选项已变化，旧票据、选择与确认失效；请重新预览"
}

func (model *Model) SelectionFileChanged() {
	model.cancelJob()
	state := model.selectionState()
	state.Hash = ""
	state.Configurations = nil
	state.SkillOptions = nil
	state.Bindings = nil
	state.Review = nil
	state.FinalApproved = false
	model.Status = "文件或来源已变化；请重新授权上传，再预览"
}

func (model *Model) ChooseSelection(kind, index int, checked bool) {
	state := model.selectionState()
	if model.Busy || state.Preview == nil || kind < 0 || kind > 2 || index < 0 || index >= len(state.Checked[kind]) {
		return
	}
	status := importv1.ImportCandidateStatus(0)
	switch kind {
	case 0:
		status = state.View.Repositories[index].Status
	case 1:
		status = state.View.Skills[index].Status
	case 2:
		status = state.View.Configurations[index].Status
	}
	if status != 1 || kind == 1 && !state.bindingConfirmed(index) {
		state.Checked[kind][index] = false
		return
	}
	state.Checked[kind][index] = checked
	choice := make([]int, 0)
	for position, selected := range state.Checked[kind] {
		if selected {
			choice = append(choice, position+1)
		}
	}
	switch kind {
	case 0:
		state.Choice.Repositories = choice
	case 1:
		state.Choice.Skills = choice
	case 2:
		state.Choice.Configurations = choice
	}
	state.Review = nil
	state.FinalApproved = false
}

func (state *selectionState) choice() importclient.SelectionChoice {
	choice := importclient.SelectionChoice{Repositories: slices.Clone(state.Choice.Repositories), Skills: slices.Clone(state.Choice.Skills), Configurations: slices.Clone(state.Choice.Configurations)}
	for _, notice := range state.Notices {
		if notice.Checked {
			choice.AcknowledgedNotices = append(choice.AcknowledgedNotices, notice.ID)
		}
	}
	return choice
}

func selectionChoiceEqual(first, second importclient.SelectionChoice) bool {
	return slices.Equal(first.Repositories, second.Repositories) && slices.Equal(first.Skills, second.Skills) && slices.Equal(first.Configurations, second.Configurations) && slices.Equal(first.AcknowledgedNotices, second.AcknowledgedNotices)
}

func selectionChoiceValid(view *importv1.PreviewImportSelectionResponse, choice importclient.SelectionChoice) bool {
	if view == nil || len(choice.Repositories)+len(choice.Skills)+len(choice.Configurations) == 0 || len(choice.Repositories) > 16 || len(choice.Skills) > 128 || len(choice.Configurations) > 32 {
		return false
	}
	valid := func(indices []int, count int, status func(int) importv1.ImportCandidateStatus) bool {
		seen := make(map[int]bool)
		for _, index := range indices {
			if index < 1 || index > count || seen[index] || status(index-1) != 1 {
				return false
			}
			seen[index] = true
		}
		return true
	}
	if !valid(choice.Repositories, len(view.Repositories), func(index int) importv1.ImportCandidateStatus { return view.Repositories[index].Status }) || !valid(choice.Skills, len(view.Skills), func(index int) importv1.ImportCandidateStatus { return view.Skills[index].Status }) || !valid(choice.Configurations, len(view.Configurations), func(index int) importv1.ImportCandidateStatus { return view.Configurations[index].Status }) {
		return false
	}
	notices := selectionAllNotices(view)
	if len(choice.AcknowledgedNotices) != len(notices) {
		return false
	}
	for index, notice := range notices {
		if choice.AcknowledgedNotices[index] != notice.Id {
			return false
		}
	}
	return true
}

func selectionDraftSummary(candidate *importv1.ImportConfigurationCandidate) []string {
	var lines []string
	for _, slot := range candidate.CredentialSlots {
		lines = append(lines, fmt.Sprintf("%s · 来源存在 %t（不迁移）", slot.SlotId, slot.Present))
	}
	if candidate.Draft == nil {
		return lines
	}
	draft := candidate.Draft
	if provider := draft.GetClaudeProvider(); provider != nil {
		lines = append(lines, "Claude auth: "+provider.AuthMode.String())
		if provider.Endpoint != nil {
			lines = append(lines, "endpoint: "+*provider.Endpoint)
		}
		if provider.Models != nil {
			for index, value := range []*string{provider.Models.DefaultModel, provider.Models.Haiku, provider.Models.Sonnet, provider.Models.Opus} {
				if value != nil {
					lines = append(lines, fmt.Sprintf("model[%d]: %s", index, *value))
				}
			}
		}
	}
	if provider := draft.GetGeminiProvider(); provider != nil {
		lines = append(lines, "Gemini auth: "+provider.AuthMode.String())
		if provider.KeyTransport != nil {
			lines = append(lines, "key transport: "+provider.KeyTransport.String())
		}
		for _, value := range []*string{provider.Endpoint, provider.EnvModel, provider.SettingsModel} {
			if value != nil {
				lines = append(lines, *value)
			}
		}
	}
	if provider := draft.GetCodexProvider(); provider != nil {
		lines = append(lines, "Codex provider-key · "+provider.Endpoint, "model: "+provider.Model)
	}
	if provider := draft.GetGrokbuildProvider(); provider != nil {
		lines = append(lines, "Grok Build · "+provider.Mode.String())
	}
	if provider := draft.GetOpencodeProvider(); provider != nil {
		lines = append(lines, selectionOpenCodeSummary(provider)...)
	}
	return lines
}

func selectionOpenCodeSummary(provider *configurationv1.OpenCodeProvider) []string {
	lines := []string{"OpenCode · v1", "provider ID: " + provider.ProviderId}
	optional := func(label string, value *string) {
		text := "未设置"
		if value != nil {
			text = *value
		}
		lines = append(lines, label+": "+text)
	}
	input := provider.GetV1()
	if input == nil {
		return append(lines, "未支持的来源格式")
	}
	optional("npm", input.Npm)
	optional("name", input.Name)
	lines = append(lines, fmt.Sprintf("options present: %t", input.Options != nil))
	if options := input.Options; options != nil {
		optional("options.baseURL", options.BaseUrl)
		optional("options.apiKey slot", options.ApiKey)
		lines = append(lines, fmt.Sprintf("options.headers present: %t", options.Headers != nil))
		if options.Headers != nil {
			for _, header := range slices.Sorted(maps.Keys(options.Headers.Entries)) {
				lines = append(lines, "header "+header+" → "+options.Headers.Entries[header])
			}
		}
	}
	lines = append(lines, fmt.Sprintf("models present: %t", input.Models != nil))
	if input.Models != nil {
		for _, id := range slices.Sorted(maps.Keys(input.Models.Entries)) {
			model := input.Models.Entries[id]
			lines = append(lines, "model ID: "+id)
			optional("model.name", model.Name)
			lines = append(lines, fmt.Sprintf("model.limit present: %t", model.Limit != nil))
			if model.Limit != nil {
				optional("limit.context", model.Limit.Context)
				optional("limit.output", model.Limit.Output)
			}
			lines = append(lines, fmt.Sprintf("model.modalities present: %t", model.Modalities != nil))
			if model.Modalities != nil {
				for _, field := range []struct {
					label string
					value *configurationv1.OpenCodeStrings
				}{{"modalities.input", model.Modalities.Input}, {"modalities.output", model.Modalities.Output}} {
					text := "未设置"
					if field.value != nil {
						text = fmt.Sprintf("%q", field.value.Values)
					}
					lines = append(lines, field.label+": "+text)
				}
			}
		}
	}
	return lines
}

func (state *selectionState) bindingConfirmed(index int) bool {
	if state.View == nil || index < 0 || index >= len(state.View.Skills) {
		return false
	}
	skill := state.View.Skills[index]
	return skill.Binding != nil && proto.Equal(state.VerifiedSkills[skill.Id], skill.Binding)
}

func (state *selectionState) skillsConfirmed() bool {
	for _, index := range state.Choice.Skills {
		if !state.bindingConfirmed(index - 1) {
			return false
		}
	}
	return true
}

func (model *Model) ReviewSelection() {
	state := model.selectionState()
	state.Review = nil
	state.FinalApproved = false
	if model.Busy || !model.selectionCurrent() || !state.skillsConfirmed() || !selectionChoiceValid(state.View, state.choice()) {
		model.Status = "请选择非空 READY 组合，并逐项确认所有排除提示"
		return
	}
	choice := state.choice()
	lines := []string{"Origin: " + model.origin, "Owner: " + state.Owner, "来源: " + selectionSourceLabel(state.Source), "SHA256: " + state.Hash, "票据期限: " + time.UnixMilli(state.View.ExpiresMs).UTC().Format(time.RFC3339)}
	for _, index := range choice.Repositories {
		entry := state.View.Repositories[index-1]
		lines = append(lines, "仓库: "+entry.Hint.Repository+" @ "+entry.Hint.Branch)
	}
	for _, index := range choice.Skills {
		entry := state.View.Skills[index-1]
		binding := entry.Binding
		lines = append(lines, "技能: "+entry.SafeName, binding.Repository+" @ "+binding.Branch, "repository ID: "+binding.RepositoryId, "archive SHA256: "+binding.ArchiveHash, "skill ID: "+binding.SkillId, "path: "+binding.LogicalPath, "content SHA256: "+binding.ContentHash)
	}
	for _, index := range choice.Configurations {
		entry := state.View.Configurations[index-1]
		lines = append(lines, "配置目标: "+entry.KnownAgentId+" / "+entry.SafeName)
		lines = append(lines, selectionDraftSummary(entry)...)
	}
	for _, notice := range state.Notices {
		lines = append(lines, "明确排除/边界: "+notice.Label)
	}
	lines = append(lines, "仅保存服务器私有资产及内容引用；不迁移来源秘密，不安装或应用设备。", fmt.Sprintf("本次明确选择：仓库 %d / 技能 %d / 配置 %d", len(choice.Repositories), len(choice.Skills), len(choice.Configurations)))
	state.Review = &selectionReview{Lines: lines, Choice: choice, Preview: state.Preview}
	model.Status = "请核对最终审阅，并另一次明确确认提交"
}

func selectionResultLines(result importclient.SelectionResult) []string {
	observation := strings.TrimPrefix(result.Observation.String(), "IMPORT_COMMIT_OBSERVATION_")
	phase := strings.TrimPrefix(result.Phase.String(), "IMPORT_RECEIPT_PHASE_")
	return []string{"operation ID: " + result.OperationID, "Observation: " + observation + " / Phase: " + phase, fmt.Sprintf("revision %d · 仓库回执 %d / 技能引用 %d / 配置资产 %d", result.Revision, result.Repositories, result.Skills, result.Configurations), fmt.Sprintf("与本次选择关联核验 %t；状态查询的回执不代替原选择证明", result.SelectionVerified), "Completed 不表示本机安装；Partial 不是回滚。NotObserved 不证明在途提交未来不会完成。"}
}
