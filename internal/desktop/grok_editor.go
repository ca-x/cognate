package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/grokclient"
	"github.com/egoist/mygo/ui"
)

type grokForm struct {
	grokclient.EditView
	Name, Profile, UpstreamModel, Endpoint, ModelName, APIBackend, ContextWindow, EnvKey string
	Mode                                                                                 grokclient.Mode
	EnvPresent, Dirty                                                                    bool
	Action                                                                               grokclient.KeyAction
	Secret                                                                               modelCredential
}

func newGrokForm(view grokclient.EditView) *grokForm {
	form := &grokForm{EditView: view, Name: view.Draft.Name, Mode: view.Draft.Mode, Action: grokclient.KeepKey, APIBackend: "responses"}
	if view.Metadata.ID == "" {
		form.Action = grokclient.NoKeyMutation
	}
	if model := view.Draft.Model; model != nil {
		form.Profile, form.UpstreamModel, form.Endpoint = model.Profile, model.UpstreamModel, model.Endpoint
		form.ModelName, form.APIBackend = model.Name, model.APIBackend
		form.ContextWindow = strconv.FormatInt(model.ContextWindow, 10)
		if model.EnvKey != nil {
			form.EnvPresent, form.EnvKey = true, *model.EnvKey
		}
	}
	return form
}

func (form *grokForm) clearSecret() {
	form.Secret.clear()
}

func (form *grokForm) setMode(mode grokclient.Mode) {
	form.clearSecret()
	form.Mode, form.Dirty = mode, true
	form.Action = grokclient.KeepKey
	if form.Metadata.ID == "" {
		form.Action = grokclient.NoKeyMutation
	}
}

func (form *grokForm) draft() (grokclient.Draft, error) {
	if form.ReadOnly || !utf8.ValidString(form.Name) || len(strings.TrimSpace(form.Name)) == 0 || len(strings.TrimSpace(form.Name)) > 128 {
		return grokclient.Draft{}, grokclient.ErrInvalid
	}
	draft := grokclient.Draft{Name: form.Name, Mode: form.Mode}
	if form.Mode == grokclient.CustomModel {
		window, err := strconv.ParseInt(form.ContextWindow, 10, 64)
		if err != nil || window < 1 || window > 9007199254740991 || strconv.FormatInt(window, 10) != form.ContextWindow {
			return grokclient.Draft{}, grokclient.ErrInvalid
		}
		draft.Model = &grokclient.Model{Profile: form.Profile, UpstreamModel: form.UpstreamModel, Endpoint: form.Endpoint, Name: form.ModelName, APIBackend: form.APIBackend, ContextWindow: window}
		if form.EnvPresent {
			draft.Model.EnvKey = new(form.EnvKey)
		}
	}
	return draft, nil
}

type grokEditorState struct {
	mutex                              sync.Mutex
	assets                             *grokAssetsModel
	closed                             bool
	Assets                             []grokclient.Metadata
	Order                              grokclient.OrderSnapshot
	OrderBaseline                      []grokclient.Metadata
	Form                               *grokForm
	Review                             *grokAssetsReview
	ReviewAction                       grokclient.KeyAction
	ReviewMutation                     grokclient.Action
	Confirmation                       string
	KeyAuthorized, Blocked, OrderDirty bool
	CopyName                           string
	Pending                            string
	PendingPage                        int
	Scroll                             ui.ScrollState
	ReviewScroll                       ui.ScrollState
}

func (state *grokEditorState) session() *grokAssetsModel {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	return state.assets
}

func (state *grokEditorState) attach(assets *grokAssetsModel) bool {
	state.mutex.Lock()
	if state.closed {
		state.mutex.Unlock()
		assets.Close()
		return false
	}
	state.assets = assets
	state.mutex.Unlock()
	return true
}

func (state *grokEditorState) close() {
	state.mutex.Lock()
	state.closed = true
	assets := state.assets
	state.assets = nil
	state.mutex.Unlock()
	if assets != nil {
		assets.Close()
	}
}

func (model *Model) grokState() *grokEditorState {
	if model.grok == nil {
		state := &grokEditorState{PendingPage: -1}
		model.grok = func() *grokEditorState { return state }
	}
	return model.grok()
}

func (model *Model) grokCancel() {
	if model.grok == nil {
		return
	}
	state := model.grok()
	if state.Form != nil {
		state.Form.clearSecret()
	}
	state.Review, state.Confirmation, state.KeyAuthorized = nil, "", false
	state.close()
	model.grok = nil
}

func grokMessage(err error) string {
	switch {
	case err == nil:
		return "服务端资产操作完成；未应用本机或执行插件"
	case errors.Is(err, grokclient.ErrUnknown):
		return "结果 Unknown：禁止重放。请显式重新载入，核实资产与排序后重新审阅。"
	case errors.Is(err, grokclient.ErrConflict):
		return "CAS 冲突：没有覆盖；请重新载入并重新审阅。"
	case errors.Is(err, grokclient.ErrUnsupported):
		return "未知字段或版本：只读，禁止覆盖或复制。"
	case errors.Is(err, grokclient.ErrInvalid):
		return "输入无效：检查名称 UTF-8 字节、七字段、整数及密钥动作。"
	case errors.Is(err, grokclient.ErrExpired):
		return "审阅已过期；请重新预览和授权。"
	case errors.Is(err, grokclient.ErrDenied), errors.Is(err, errConfigurationIdentity):
		return "身份失效；请重新登录。"
	default:
		return "操作未完成或已取消；不保证回滚。请显式读取核实，不自动重发。"
	}
}

func (model *Model) grokJob(label string, work func(context.Context) (func(), error), cleanup ...func()) {
	state := model.grokState()
	model.jobWithTimeout(label, 60*time.Second, func(ctx context.Context) (func(), error) {
		commit, err := work(ctx)
		if ctx.Err() != nil {
			state.close()
		}
		return func() {
			model.Status = label + "完成；未应用本机或执行插件"
			if err != nil {
				model.Status = grokMessage(err)
			}
			if commit != nil {
				commit()
			}
			if errors.Is(err, grokclient.ErrDenied) || errors.Is(err, errConfigurationIdentity) {
				model.grokCancel()
			}
		}, nil
	}, cleanup...)
}

func (model *Model) LoadGrokAssets() {
	if model.closed || model.Busy || !model.ready() || model.session.value() == "" {
		return
	}
	backend, ok := model.backend.(interface {
		ConfigurationIdentity(context.Context, string, string) (configurationIdentity, error)
		openGrokAssets(context.Context, configurationIdentity) (*grokAssetsModel, error)
	})
	if !ok {
		model.Status = grokMessage(grokclient.ErrUnsupported)
		return
	}
	model.grokCancel()
	state := model.grokState()
	origin, session := model.origin, model.session.value()
	model.grokJob("载入 Grok 资产", func(ctx context.Context) (func(), error) {
		identity, err := backend.ConfigurationIdentity(ctx, origin, session)
		if err != nil {
			return nil, err
		}
		defer identity.clear()
		assets, err := backend.openGrokAssets(ctx, identity)
		if err != nil {
			return nil, err
		}
		if !state.attach(assets) {
			return nil, grokclient.ErrClosed
		}
		list, err := assets.List(ctx)
		if err != nil {
			state.close()
			return nil, err
		}
		order, err := assets.Order(ctx)
		if err != nil {
			state.close()
			return nil, err
		}
		return func() {
			state.Assets, state.Order = list, order
			state.OrderBaseline = slices.Clone(order.Assets)
		}, nil
	})
}

func (model *Model) grokRequest(action string) {
	state := model.grokState()
	if model.Busy {
		return
	}
	if state.Form != nil {
		state.Form.clearSecret()
	}
	if state.OrderDirty || state.Form != nil && state.Form.Dirty {
		state.Pending = action
		return
	}
	model.grokAction(action)
}

func (model *Model) grokAction(action string) {
	state := model.grokState()
	if action == "reload" {
		model.LoadGrokAssets()
		return
	}
	if action == "new" {
		state.Form = newGrokForm(grokclient.EditView{Draft: grokclient.Draft{Mode: grokclient.NativeLogin}})
		state.Form.Dirty = true
		return
	}
	assets := state.session()
	if assets == nil || model.Busy {
		return
	}
	model.grokJob("读取 Grok 资产", func(ctx context.Context) (func(), error) {
		view, err := assets.Editable(ctx, action)
		return func() {
			if err == nil {
				state.Form = newGrokForm(view)
				state.CopyName = view.Draft.Name + " 副本"
			}
		}, err
	})
}

func (model *Model) PrepareGrok(action grokclient.Action) {
	state := model.grokState()
	assets := state.session()
	if model.Busy || state.Blocked || assets == nil || state.Review != nil {
		return
	}
	change := grokclient.Change{Action: action}
	keyAction := grokclient.NoKeyMutation
	var input *grokclient.Input
	if action == grokclient.ReplaceOrder {
		change.ExpectedOrderRevision = state.Order.Revision
		change.OrderIDs = make([]string, len(state.Order.Assets))
		for index, asset := range state.Order.Assets {
			change.OrderIDs[index] = asset.ID
		}
	} else {
		form := state.Form
		if form == nil || form.ReadOnly {
			return
		}
		change.ID, change.ExpectedRevision = form.Metadata.ID, form.Metadata.Revision
		if action == grokclient.Copy {
			change.Name = state.CopyName
		}
		if action == grokclient.Create || action == grokclient.Update {
			draft, err := form.draft()
			if err != nil {
				model.Status = grokMessage(err)
				return
			}
			keyAction = form.Action
			key := []byte(form.Secret.value())
			input, err = grokclient.NewInput(model.life, draft, keyAction, key)
			clear(key)
			form.clearSecret()
			if err != nil {
				model.Status = grokMessage(err)
				return
			}
		}
	}
	model.grokJob("准备完整 Grok 审阅", func(ctx context.Context) (func(), error) {
		review, err := assets.prepare(ctx, change, input)
		return func() {
			if err == nil {
				state.Review, state.ReviewAction = &review, keyAction
				state.ReviewMutation = action
				state.Confirmation, state.KeyAuthorized = "", false
				state.ReviewScroll = ui.ScrollState{}
			}
		}, err
	}, func() {
		if input != nil {
			input.Close()
		}
	})
}

func (model *Model) CancelGrokReview() {
	state := model.grokState()
	if model.Busy {
		return
	}
	state.Review, state.Confirmation, state.KeyAuthorized = nil, "", false
	if state.Form != nil {
		state.Form.clearSecret()
	}
	assets := state.session()
	if assets != nil {
		model.grokJob("取消 Grok 审阅", func(ctx context.Context) (func(), error) { return nil, assets.CancelReview() })
	}
}

func (model *Model) SubmitGrok() {
	state := model.grokState()
	if model.Busy || state.Review == nil || state.Blocked || state.Confirmation != state.Review.Confirmation {
		return
	}
	if (state.ReviewAction == grokclient.ReplaceKey || state.ReviewAction == grokclient.ClearKey) && !state.KeyAuthorized {
		return
	}
	assets, confirmation, action := state.session(), state.Confirmation, state.ReviewMutation
	if assets == nil {
		return
	}
	state.Review, state.Confirmation, state.KeyAuthorized = nil, "", false
	state.Blocked = true
	if state.Form != nil {
		state.Form.clearSecret()
	}
	model.grokJob("提交一次 Grok 操作", func(ctx context.Context) (func(), error) {
		_, err := assets.Submit(ctx, confirmation)
		return func() {
			if err == nil && (action == grokclient.Create || action == grokclient.Update) && state.Form != nil {
				state.Form.Dirty = false
			}
			if err == nil {
				model.Status = "已提交一次；请重新载入核实。未应用本机。"
			}
		}, err
	})
}

func (state *grokEditorState) move(index, offset int) {
	other := index + offset
	if index < 0 || other < 0 || index >= len(state.Order.Assets) || other >= len(state.Order.Assets) {
		return
	}
	state.Order.Assets = slices.Clone(state.Order.Assets)
	state.Order.Assets[index], state.Order.Assets[other] = state.Order.Assets[other], state.Order.Assets[index]
	state.OrderDirty = true
}

func (state *grokEditorState) discardOrder() {
	state.Order.Assets = slices.Clone(state.OrderBaseline)
	state.OrderDirty = false
}

func grokRevision(metadata grokclient.Metadata) string {
	return fmt.Sprintf("%s · revision %d", metadata.Name, metadata.Revision)
}
