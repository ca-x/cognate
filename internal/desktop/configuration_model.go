package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/egoist/mygo/ui"
)

var (
	errConfigurationInvalid     = errors.New("配置输入或服务响应无效")
	errConfigurationUnsupported = errors.New("此配置尚不支持编辑或本机应用")
	errConfigurationIdentity    = errors.New("配置身份已失效，请重新登录")
	errConfigurationNotFound    = errors.New("配置不存在或当前身份无权访问")
	errConfigurationConflict    = errors.New("服务端配置已变化，未覆盖；请重新载入")
	errConfigurationLimit       = errors.New("配置请求或容量超出限制")
	errConfigurationUnavailable = errors.New("配置请求未完成，请核实连接或权限")
)

type configurationIdentity struct {
	Origin, OwnerID string
	private         func() *configurationIdentityData
}

type configurationIdentityData struct {
	mutex   sync.Mutex
	session string
}

func (identity configurationIdentity) clear() {
	if identity.private != nil {
		data := identity.private()
		data.mutex.Lock()
		defer data.mutex.Unlock()
		data.session = ""
	}
}

type configurationAsset struct {
	ID, AgentID, Kind, Name string
	SchemaVersion           uint32
	Revision                int64
	Editable                bool
}

type configurationDraft struct {
	Asset   configurationAsset
	private func() *configurationDraftData
}

type configurationDraftData struct {
	Provider                         agentconfig.ClaudeProviderDesired
	NeedsCredential, PreservedExtras bool
}

type configurationSecretAction uint8

const (
	configurationKeep configurationSecretAction = iota + 1
	configurationReplace
	configurationClear
)

type configurationSecretEdit struct {
	Action      configurationSecretAction
	Replacement []byte
}

type configurationEdit struct{ private func() *configurationEditData }

type configurationEditData struct {
	Asset    configurationAsset
	Name     string
	Provider agentconfig.ClaudeProviderDesired
	Secrets  [2]configurationSecretEdit
}

func cloneConfigurationProvider(provider agentconfig.ClaudeProviderDesired) agentconfig.ClaudeProviderDesired {
	clone := func(value *string) *string {
		if value == nil {
			return nil
		}
		return new(*value)
	}
	provider.Endpoint = clone(provider.Endpoint)
	provider.Models = agentconfig.ClaudeModelsDesired{Default: clone(provider.Models.Default), Haiku: clone(provider.Models.Haiku), Sonnet: clone(provider.Models.Sonnet), Opus: clone(provider.Models.Opus)}
	return provider
}

func newConfigurationEdit(asset configurationAsset, name string, provider agentconfig.ClaudeProviderDesired, secrets [2]configurationSecretEdit) configurationEdit {
	for index := range secrets {
		secrets[index].Replacement = bytes.Clone(secrets[index].Replacement)
	}
	data := &configurationEditData{Asset: asset, Name: name, Provider: cloneConfigurationProvider(provider), Secrets: secrets}
	return configurationEdit{private: func() *configurationEditData { return data }}
}

func (edit configurationEdit) clear() {
	if edit.private == nil {
		return
	}
	data := edit.private()
	for index := range data.Secrets {
		clear(data.Secrets[index].Replacement)
		data.Secrets[index] = configurationSecretEdit{}
	}
	data.Name = ""
	data.Provider = agentconfig.ClaudeProviderDesired{}
}

type configurationBackend interface {
	ConfigurationIdentity(context.Context, string, string) (configurationIdentity, error)
	ConfigurationAssets(context.Context, configurationIdentity) ([]configurationAsset, error)
	ConfigurationDraft(context.Context, configurationIdentity, string) (configurationDraft, error)
	SaveConfiguration(context.Context, configurationIdentity, configurationEdit) (configurationDraft, error)
	CopyConfiguration(context.Context, configurationIdentity, configurationAsset, string) (configurationDraft, error)
	DeleteConfiguration(context.Context, configurationIdentity, configurationAsset) error
	ApplyConfiguration(context.Context, configurationIdentity, configurationAsset, string, bool, nativeconfig.Interaction) (configurationOutcome, error)
	RestoreConfiguration(context.Context, string, bool, nativeconfig.Interaction) (configurationOutcome, error)
	ConfigurationStatus(context.Context, string) (configurationOutcome, error)
}

type configurationOutcome struct {
	Plan                                    configapply.SafePlan
	Local                                   configapply.SafeResult
	RemoteState                             configclient.NativeGrantStatus
	RemoteUnknown                           bool
	LocalUnknown, CleanupIncomplete, Failed bool
}

type configurationForm struct {
	Asset                                   configurationAsset
	Name                                    string
	Mode                                    agentconfig.ClaudeAuthMode
	Present                                 [5]bool
	Values                                  [5]string
	Actions                                 [2]configurationSecretAction
	replacements                            func() *[2]string
	SecretPresent                           [2]bool
	NeedsCredential, PreservedExtras, Dirty bool
}

func (form *configurationForm) secret() *[2]string {
	if form.replacements == nil {
		data := new([2]string)
		form.replacements = func() *[2]string { return data }
	}
	return form.replacements()
}

func (form *configurationForm) clearSecrets() {
	if form.replacements != nil {
		*form.replacements() = [2]string{}
	}
}

func (configurationForm) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private configuration editor]")
}
func (configurationForm) LogValue() slog.Value {
	return slog.StringValue("[private configuration editor]")
}
func (configurationForm) MarshalJSON() ([]byte, error) {
	return []byte(`"[private configuration editor]"`), nil
}

type configurationPrompt struct {
	Plan                            configapply.SafePlan
	Slots                           []agentconfig.ClaudeSecretSlot
	Checked                         []bool
	Generation                      uint64
	Origin, Owner, AssetID, Profile string
	Revision                        int64
	Deadline                        time.Time
	Reply                           chan nativeconfig.Approval
}

type configurationState struct {
	Identity             configurationIdentity
	Assets               []configurationAsset
	Selected             string
	Form                 *configurationForm
	Profile, OperationID string
	History              []configurationOutcome
	Outcome              configurationOutcome
	Prompt               *configurationPrompt
	PendingPage          int
	PendingAction        string
	ConfirmDelete        bool
	List                 ui.ListState
	ListIndex            int
	Scroll               ui.ScrollState
}

func (model *Model) configurationState() *configurationState {
	if model.configuration == nil {
		data := &configurationState{Profile: "default", PendingPage: -1, ListIndex: -1}
		model.configuration = func() *configurationState { return data }
	}
	return model.configuration()
}

func (model *Model) configurationCancel(identityLost bool) {
	if model.configuration == nil {
		return
	}
	state := model.configuration()
	state.Prompt = nil
	if state.Form != nil {
		state.Form.clearSecrets()
	}
	state.ConfirmDelete = false
	if identityLost {
		state.Identity.clear()
		model.configuration = nil
	}
}

func (model *Model) configurationNavigate(page int) {
	if model.Page == page {
		return
	}
	if model.Page == 5 && page != 5 && model.grok != nil {
		state := model.grok()
		if state.Form != nil {
			state.Form.clearSecret()
		}
		if state.OrderDirty || state.Form != nil && state.Form.Dirty {
			state.PendingPage = page
			return
		}
		model.grokCancel()
	}
	state := model.configurationState()
	if state.Form != nil && state.Form.Dirty && model.Page == 4 && page != 4 {
		state.PendingPage = page
		return
	}
	model.cancelJob()
	model.Page = page
}

func (model *Model) configurationCapability() (configurationBackend, bool) {
	backend, ok := model.backend.(configurationBackend)
	if !ok {
		model.Status = errConfigurationUnsupported.Error()
	}
	return backend, ok
}

func configurationMessage(err error) string {
	if err == nil {
		return "操作完成"
	}
	if hint := clientKeyHint(err); hint != "" {
		return hint
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "已取消或超时；消费或写入可能已发生，请核实本机状态"
	case errors.Is(err, nativeconfig.ErrDenied):
		return "未批准本次操作；未承诺回滚此前写入"
	case errors.Is(err, configapply.ErrPlanExpired):
		return "本次批准已过期，请重新预览"
	case errors.Is(err, configapply.ErrConflict), errors.Is(err, configclient.ErrNativeConflict), errors.Is(err, errConfigurationConflict):
		return "配置或本机基线已变化，未强制覆盖；请重新载入"
	case errors.Is(err, configclient.ErrNativeUnknown), errors.Is(err, configapply.ErrOutcomeUnknown):
		return "结果未知，不自动重发；请核实本机状态和待恢复记录"
	case errors.Is(err, errConfigurationIdentity):
		return errConfigurationIdentity.Error()
	case errors.Is(err, errConfigurationUnsupported):
		return errConfigurationUnsupported.Error()
	case errors.Is(err, errConfigurationNotFound):
		return errConfigurationNotFound.Error()
	case errors.Is(err, errConfigurationInvalid):
		return errConfigurationInvalid.Error()
	case errors.Is(err, errConfigurationLimit):
		return errConfigurationLimit.Error()
	default:
		return "操作未完成，请核实权限、连接与待恢复状态；不保证回滚"
	}
}

func (model *Model) configurationJob(label string, local bool, work func(context.Context) (func(), error), cleanup ...func()) {
	if local && model.localWorkBlocked() {
		model.Status = "请先停止后台同步并等待实际任务排空"
		return
	}
	limit := 60 * time.Second
	if local {
		limit = 2 * time.Minute
	}
	model.jobWithTimeout(label, limit, func(ctx context.Context) (func(), error) {
		commit, err := work(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return func() {
			if commit != nil {
				commit()
			}
			if err != nil {
				model.Status = configurationMessage(err)
			}
		}, nil
	}, cleanup...)
}

func (model *Model) LoadConfigurations() {
	backend, ok := model.configurationCapability()
	if !ok || !model.ready() {
		return
	}
	if model.session.value() == "" {
		model.Status = "请先登录以管理私有配置"
		return
	}
	state := model.configurationState()
	if state.Form != nil && state.Form.Dirty {
		state.PendingAction = "reload"
		return
	}
	origin, session := model.origin, model.session.value()
	model.configurationJob("载入配置", false, func(ctx context.Context) (func(), error) {
		identity, err := backend.ConfigurationIdentity(ctx, origin, session)
		if err != nil {
			return nil, err
		}
		assets, err := backend.ConfigurationAssets(ctx, identity)
		if err != nil {
			identity.clear()
			return nil, err
		}
		if ctx.Err() != nil {
			identity.clear()
			return nil, ctx.Err()
		}
		return func() {
			state.Identity.clear()
			state.Identity = identity
			state.Assets = assets
			state.Selected = ""
			state.ListIndex = -1
			state.Form = nil
			model.Status = fmt.Sprintf("配置资产 %d 项；保存不会应用到本机", len(assets))
		}, nil
	})
}

func configurationDraftForm(draft configurationDraft) *configurationForm {
	form := &configurationForm{Asset: draft.Asset, Name: draft.Asset.Name, Mode: agentconfig.ClaudeNativeLogin, Actions: [2]configurationSecretAction{configurationKeep, configurationKeep}}
	if draft.private == nil {
		return form
	}
	data := draft.private()
	form.Mode = data.Provider.AuthMode
	form.NeedsCredential, form.PreservedExtras = data.NeedsCredential, data.PreservedExtras
	form.SecretPresent = [2]bool{data.Provider.APIKeyPresent, data.Provider.AuthTokenPresent}
	for index, field := range []*string{data.Provider.Endpoint, data.Provider.Models.Default, data.Provider.Models.Haiku, data.Provider.Models.Sonnet, data.Provider.Models.Opus} {
		if field != nil {
			form.Present[index] = true
			form.Values[index] = *field
		}
	}
	return form
}

func (form *configurationForm) edit() configurationEdit {
	values := [5]*string{}
	for index := range values {
		if form.Present[index] {
			values[index] = new(form.Values[index])
		}
	}
	provider := agentconfig.ClaudeProviderDesired{AuthMode: form.Mode, Endpoint: values[0], Models: agentconfig.ClaudeModelsDesired{Default: values[1], Haiku: values[2], Sonnet: values[3], Opus: values[4]}}
	secrets := [2]configurationSecretEdit{}
	for index := range secrets {
		secrets[index] = configurationSecretEdit{Action: form.Actions[index], Replacement: []byte(form.secret()[index])}
	}
	defer func() {
		for index := range secrets {
			clear(secrets[index].Replacement)
		}
	}()
	return newConfigurationEdit(form.Asset, form.Name, provider, secrets)
}

func (model *Model) NewConfiguration() {
	state := model.configurationState()
	if state.Identity.private == nil {
		model.Status = "请先载入配置以确认身份"
		return
	}
	if state.Form != nil && state.Form.Dirty {
		state.PendingAction = "new"
		return
	}
	model.cancelJob()
	state.Selected = ""
	state.ListIndex = -1
	state.Form = configurationDraftForm(configurationDraft{Asset: configurationAsset{AgentID: "claude", Kind: "provider", SchemaVersion: 1, Editable: true}})
	state.Form.Actions = [2]configurationSecretAction{configurationClear, configurationClear}
}

func (model *Model) SelectConfiguration(id string) {
	state := model.configurationState()
	if state.Form != nil && state.Form.Dirty {
		state.PendingAction = "select:" + id
		state.ListIndex = slices.IndexFunc(state.Assets, func(asset configurationAsset) bool { return asset.ID == state.Selected })
		return
	}
	backend, ok := model.configurationCapability()
	if !ok {
		return
	}
	index := slices.IndexFunc(state.Assets, func(asset configurationAsset) bool { return asset.ID == id })
	if index < 0 {
		return
	}
	model.cancelJob()
	state.Selected, state.Form = id, nil
	state.ListIndex = index
	if !state.Assets[index].Editable {
		model.Status = "此资产仅显示元数据；不支持编辑或应用"
		return
	}
	identity := state.Identity
	model.configurationJob("载入草稿", false, func(ctx context.Context) (func(), error) {
		draft, err := backend.ConfigurationDraft(ctx, identity, id)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Form = configurationDraftForm(draft)
			model.Status = "草稿已载入；原凭据不会回显"
		}, nil
	})
}

func (model *Model) SaveConfiguration() {
	state := model.configurationState()
	backend, ok := model.configurationCapability()
	if !ok || state.Form == nil || !state.Form.Asset.Editable || state.Identity.private == nil {
		return
	}
	edit, identity := state.Form.edit(), state.Identity
	state.Form.clearSecrets()
	for index, action := range state.Form.Actions {
		if action == configurationReplace {
			if state.Form.Asset.ID == "" {
				state.Form.Actions[index] = configurationClear
			} else {
				state.Form.Actions[index] = configurationKeep
			}
		}
	}
	model.configurationJob("保存服务端资产", false, func(ctx context.Context) (func(), error) {
		defer edit.clear()
		draft, err := backend.SaveConfiguration(ctx, identity, edit)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Form = configurationDraftForm(draft)
			state.Selected = draft.Asset.ID
			index := slices.IndexFunc(state.Assets, func(asset configurationAsset) bool { return asset.ID == draft.Asset.ID })
			if index < 0 {
				state.Assets = append(state.Assets, draft.Asset)
			} else {
				state.Assets[index] = draft.Asset
			}
			model.Status = "仅已保存到服务端；尚未应用到本机"
		}, nil
	}, edit.clear)
}

func (model *Model) CopyConfiguration() {
	state := model.configurationState()
	backend, ok := model.configurationCapability()
	if !ok || state.Form == nil || state.Form.Dirty {
		return
	}
	asset, identity := state.Form.Asset, state.Identity
	model.configurationJob("复制配置（不复制秘密）", false, func(ctx context.Context) (func(), error) {
		draft, err := backend.CopyConfiguration(ctx, identity, asset, asset.Name+" 副本")
		if err != nil {
			return nil, err
		}
		return func() {
			state.Assets = append(state.Assets, draft.Asset)
			state.Selected = draft.Asset.ID
			state.Form = configurationDraftForm(draft)
			model.Status = "副本已保存；凭据需重新提供"
		}, nil
	})
}

func (model *Model) DeleteConfiguration() {
	state := model.configurationState()
	backend, ok := model.configurationCapability()
	if !ok || !state.ConfirmDelete {
		return
	}
	index := slices.IndexFunc(state.Assets, func(asset configurationAsset) bool { return asset.ID == state.Selected })
	if index < 0 {
		return
	}
	asset, identity := state.Assets[index], state.Identity
	state.ConfirmDelete = false
	model.configurationJob("删除服务端资产", false, func(ctx context.Context) (func(), error) {
		err := backend.DeleteConfiguration(ctx, identity, asset)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Assets = slices.Delete(state.Assets, index, index+1)
			state.Selected = ""
			state.Form = nil
			model.Status = "资产已删除；本机配置未改变"
		}, nil
	})
}

func configurationPlanValid(plan configapply.SafePlan) bool {
	if !configurationText(plan.OperationID, 128) || plan.Target != agentconfig.ClaudeSettings || plan.Agent != "claude" || !slices.Contains([]string{"apply", "restore"}, plan.Kind) || !time.Now().Before(plan.ExpiresAt) {
		return false
	}
	seen := map[agentconfig.ClaudeEnvSlot]bool{}
	for _, operation := range plan.Operations {
		if seen[operation.Slot] || !slices.Contains([]agentconfig.ClaudeEnvSlot{agentconfig.ClaudeEnvAPIKey, agentconfig.ClaudeEnvAuthToken, agentconfig.ClaudeEnvBaseURL, agentconfig.ClaudeEnvModel, agentconfig.ClaudeEnvHaikuModel, agentconfig.ClaudeEnvSonnetModel, agentconfig.ClaudeEnvOpusModel}, operation.Slot) || !slices.Contains([]agentconfig.ClaudePatchAction{agentconfig.ClaudeRemove, agentconfig.ClaudeSetLiteral, agentconfig.ClaudeSetSelectedSecret}, operation.Action) {
			return false
		}
		seen[operation.Slot] = true
	}
	if plan.Kind == "apply" && len(seen) != 7 {
		return false
	}
	for index, slot := range plan.Slots {
		if !slices.Contains([]agentconfig.ClaudeSecretSlot{agentconfig.ClaudeAPIKeySecret, agentconfig.ClaudeAuthTokenSecret}, slot) || slices.Contains(plan.Slots[:index], slot) {
			return false
		}
	}
	return true
}

func cloneConfigurationPlan(plan configapply.SafePlan) configapply.SafePlan {
	plan.Slots, plan.Operations = slices.Clone(plan.Slots), slices.Clone(plan.Operations)
	return plan
}

func (model *Model) configurationInteraction(generation uint64, identity configurationIdentity, asset configurationAsset, profile string) nativeconfig.Interaction {
	show := func(ctx context.Context, plan configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot, approve bool) (nativeconfig.Approval, error) {
		if !configurationPlanValid(plan) || !slices.Equal(slots, plan.Slots) {
			return nativeconfig.Approval{}, errConfigurationInvalid
		}
		if plan.Kind == "apply" && (plan.AssetID != asset.ID || plan.AssetRevision != uint64(asset.Revision)) {
			return nativeconfig.Approval{}, errConfigurationInvalid
		}
		deadline := plan.ExpiresAt
		if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
			deadline = parent
		}
		wait, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		plan = cloneConfigurationPlan(plan)
		if !approve {
			model.publish(wait, func() {
				if model.closed || model.generation != generation {
					return
				}
				model.configurationRemember(configurationOutcome{Plan: plan})
			})
			return nativeconfig.Approval{}, wait.Err()
		}
		reply := make(chan nativeconfig.Approval, 1)
		prompt := &configurationPrompt{Plan: plan, Slots: slices.Clone(slots), Checked: make([]bool, len(slots)), Generation: generation, Origin: identity.Origin, Owner: identity.OwnerID, AssetID: asset.ID, Revision: asset.Revision, Profile: profile, Deadline: deadline, Reply: reply}
		model.publish(wait, func() {
			if model.closed || model.generation != generation || wait.Err() != nil {
				return
			}
			model.configurationState().Prompt = prompt
		})
		select {
		case answer := <-reply:
			return answer, wait.Err()
		case <-wait.Done():
			return nativeconfig.Approval{}, wait.Err()
		}
	}
	return nativeconfig.Interaction{
		Preview: func(ctx context.Context, plan configapply.SafePlan) error {
			_, err := show(ctx, plan, plan.Slots, false)
			return err
		},
		Approve: func(ctx context.Context, plan configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (nativeconfig.Approval, error) {
			return show(ctx, plan, slots, true)
		},
	}
}

func (model *Model) ReplyConfigurationApproval(confirmed bool) {
	state := model.configurationState()
	prompt := state.Prompt
	if prompt == nil {
		return
	}
	if confirmed {
		if model.closed || model.generation != prompt.Generation || !time.Now().Before(prompt.Deadline) || state.Profile != prompt.Profile || state.Identity.OwnerID != prompt.Owner || state.Identity.Origin != prompt.Origin {
			confirmed = false
		}
		for _, checked := range prompt.Checked {
			if !checked {
				return
			}
		}
		if prompt.Plan.Kind == "apply" && (state.Form == nil || state.Form.Dirty || state.Form.Asset.ID != prompt.AssetID || state.Form.Asset.Revision != prompt.Revision) {
			confirmed = false
		}
	}
	answer := nativeconfig.Approval{Confirmed: confirmed}
	if confirmed {
		answer.ApprovedSlots = slices.Clone(prompt.Slots)
	}
	select {
	case prompt.Reply <- answer:
	default:
	}
	state.Prompt = nil
}

func (model *Model) configurationRemember(outcome configurationOutcome) {
	state := model.configurationState()
	id := outcome.Plan.OperationID
	if id == "" {
		id = outcome.Local.OperationID
	}
	if id == "" {
		return
	}
	outcome.Plan = cloneConfigurationPlan(outcome.Plan)
	state.OperationID, state.Outcome = id, outcome
	index := slices.IndexFunc(state.History, func(previous configurationOutcome) bool {
		return previous.Plan.OperationID == id || previous.Local.OperationID == id
	})
	if index >= 0 {
		previous := state.History[index]
		outcome.RemoteUnknown = outcome.RemoteUnknown || previous.RemoteUnknown
		outcome.LocalUnknown = outcome.LocalUnknown || previous.LocalUnknown
		outcome.CleanupIncomplete = outcome.CleanupIncomplete || previous.CleanupIncomplete
		if outcome.RemoteState == "" {
			outcome.RemoteState = previous.RemoteState
		}
		if outcome.Plan.OperationID == "" {
			outcome.Plan = previous.Plan
		}
		state.History = slices.Delete(state.History, index, index+1)
	}
	state.Outcome = outcome
	state.History = append([]configurationOutcome{outcome}, state.History...)
	if len(state.History) > 20 {
		state.History = state.History[:20]
	}
}

func (model *Model) configurationLocal(action string, apply bool) {
	backend, ok := model.configurationCapability()
	if !ok || model.localWorkBlocked() {
		model.Status = "请等待实际任务排空，再开始本机操作"
		return
	}
	state := model.configurationState()
	identity, profile, operation := state.Identity, state.Profile, state.OperationID
	var asset configurationAsset
	if action == "apply" {
		if state.Form == nil || state.Form.Dirty || state.Form.Asset.ID == "" || identity.private == nil {
			model.Status = "请先保存并载入已授权资产；脏草稿不能应用"
			return
		}
		asset = state.Form.Asset
	}
	generation := model.generation + 1
	model.configurationJob("本机配置操作（不保证回滚）", true, func(ctx context.Context) (func(), error) {
		interaction := model.configurationInteraction(generation, identity, asset, profile)
		var outcome configurationOutcome
		var err error
		switch action {
		case "apply":
			outcome, err = backend.ApplyConfiguration(ctx, identity, asset, profile, apply, interaction)
		case "restore":
			outcome, err = backend.RestoreConfiguration(ctx, operation, apply, interaction)
		case "status":
			outcome, err = backend.ConfigurationStatus(ctx, operation)
		}
		return func() {
			model.configurationState().Prompt = nil
			outcome.Failed = err != nil || outcome.Failed
			outcome.LocalUnknown = outcome.LocalUnknown || errors.Is(err, configapply.ErrOutcomeUnknown)
			model.configurationRemember(outcome)
			model.Status = configurationOutcomeMessage(outcome, err)
		}, nil
	})
}

func configurationOutcomeMessage(outcome configurationOutcome, err error) string {
	if outcome.RemoteUnknown || errors.Is(err, configclient.ErrNativeUnknown) {
		return "远端结果未知；不重发授权或秘密消费，请核实本机状态"
	}
	if outcome.LocalUnknown || errors.Is(err, configapply.ErrOutcomeUnknown) {
		return "本机结果未知；不重发应用，请核实状态和待恢复记录"
	}
	if outcome.CleanupIncomplete {
		return "操作未完整收尾；写入或消费可能已发生，请核实本机状态"
	}
	if outcome.Local.NeedsAttention {
		return "本机需要恢复/人工处理；不要强制覆盖"
	}
	if err != nil {
		return configurationMessage(err)
	}
	if outcome.Failed {
		return "该操作未完整完成；请核实本机状态，不保证回滚"
	}
	if outcome.Local.Status == "AppliedOnDisk" {
		return "已应用到磁盘；未声称 Agent 已重载"
	}
	if outcome.Local.Status != "" {
		return "本机状态：" + configurationSafeStatus(outcome.Local.Status)
	}
	if outcome.RemoteState == configclient.NativeConsumed {
		return "秘密已消费，但尚未证明本机应用成功；请核实状态"
	}
	return "仅已预览；未声称本机应用成功"
}

func configurationSafeStatus(status string) string {
	if slices.Contains([]string{"Planned", "AppliedOnDisk", "Cancelled", "Restored", "OutcomeUnknown", "NeedsAttention", "Expired", "Pending"}, status) {
		return status
	}
	return "请核实"
}

func (model *Model) PreviewConfiguration()           { model.configurationLocal("apply", false) }
func (model *Model) ApplyConfiguration()             { model.configurationLocal("apply", true) }
func (model *Model) RestoreConfiguration(apply bool) { model.configurationLocal("restore", apply) }
func (model *Model) ConfigurationStatus()            { model.configurationLocal("status", false) }

var _ configurationBackend = (*Service)(nil)
