package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/mcpclient"
	"github.com/egoist/mygo/ui"
)

type mcpSlotForm struct {
	Ref                          string
	Present, Previous, Confirmed bool
	Action                       mcpclient.SecretAction
	Value                        modelCredential
	ValueSet                     bool
	Epoch                        uint64
}

func (slot *mcpSlotForm) clearValue() {
	slot.Value.clear()
	slot.ValueSet = false
	slot.Epoch++
}

type mcpForm struct {
	Metadata        mcpclient.Metadata
	Draft           mcpclient.Draft
	ReadOnly, Dirty bool
	Slots           []*mcpSlotForm
	Selected        int
}

func newMCPForm(view mcpclient.EditView) *mcpForm {
	form := &mcpForm{Metadata: view.Metadata, Draft: view.Draft, ReadOnly: view.ReadOnly, Selected: -1}
	if view.Metadata.ID == "" {
		form.Draft.AgentID = "claude"
	}
	for _, slot := range view.Secrets {
		form.Slots = append(form.Slots, &mcpSlotForm{Ref: slot.SlotID, Present: slot.Present, Previous: true, Action: mcpclient.Keep})
	}
	if len(form.Draft.Servers) > 0 {
		form.Selected = 0
	}
	return form
}

func (form *mcpForm) clearSecrets() {
	if form == nil {
		return
	}
	for _, slot := range form.Slots {
		slot.clearValue()
	}
}

func (form *mcpForm) syncSlots() ([]string, error) {
	var refs []string
	for serverIndex := range form.Draft.Servers {
		server := &form.Draft.Servers[serverIndex]
		if len(server.ID) > 64 {
			return nil, mcpclient.ErrInvalid
		}
		if server.Stdio != nil {
			if server.Stdio.Args != nil {
				for index := range server.Stdio.Args.Values {
					entry := &server.Stdio.Args.Values[index]
					if len(entry.ID) > 64 {
						return nil, mcpclient.ErrInvalid
					}
					entry.SlotRef = "arg/" + server.ID + "/" + entry.ID
					refs = append(refs, entry.SlotRef)
				}
			}
			if server.Stdio.Env != nil {
				for index := range server.Stdio.Env.Values {
					entry := &server.Stdio.Env.Values[index]
					if len(entry.Name) > 128 {
						return nil, mcpclient.ErrInvalid
					}
					entry.SlotRef = "env/" + server.ID + "/" + entry.Name
					refs = append(refs, entry.SlotRef)
				}
			}
		}
		for _, remote := range []*mcpclient.Remote{server.HTTP, server.SSE} {
			if remote == nil || remote.Headers == nil {
				continue
			}
			for index := range remote.Headers.Values {
				entry := &remote.Headers.Values[index]
				if len(entry.Name) > 128 {
					return nil, mcpclient.ErrInvalid
				}
				entry.SlotRef = "header/" + server.ID + "/" + entry.Name
				refs = append(refs, entry.SlotRef)
			}
		}
	}
	if len(refs) > 512 {
		return nil, mcpclient.ErrInvalid
	}
	var slots []*mcpSlotForm
	for _, slot := range form.Slots {
		if slot.Previous || slices.Contains(refs, slot.Ref) {
			if !slices.Contains(refs, slot.Ref) {
				slot.clearValue()
			}
			slots = append(slots, slot)
		} else {
			slot.clearValue()
		}
	}
	for _, ref := range refs {
		if !slices.ContainsFunc(slots, func(slot *mcpSlotForm) bool { return slot.Ref == ref }) {
			slots = append(slots, &mcpSlotForm{Ref: ref, Action: mcpclient.Keep})
		}
	}
	form.Slots = slots
	return refs, nil
}

func (form *mcpForm) input(ctx context.Context) (*mcpclient.Input, error) {
	if form == nil || form.ReadOnly {
		return nil, errMCPReadOnly
	}
	if _, err := form.syncSlots(); err != nil {
		return nil, err
	}
	var edits []mcpclient.SecretEdit
	defer func() {
		for index := range edits {
			clear(edits[index].Replacement)
		}
	}()
	total := 0
	for _, slot := range form.Slots {
		edit := mcpclient.SecretEdit{SlotID: slot.Ref, Action: slot.Action, Confirmed: slot.Confirmed}
		if slot.Action == mcpclient.Replace {
			if !slot.ValueSet {
				return nil, mcpclient.ErrInvalid
			}
			value := slot.Value.value()
			if len(value) > 8<<10 || len(value) > (128<<10)-total {
				return nil, mcpclient.ErrInvalid
			}
			total += len(value)
			edit.Replacement = []byte(value)
		}
		edits = append(edits, edit)
	}
	return mcpclient.NewInput(ctx, form.Draft, edits)
}

type mcpEditorState struct {
	mutex                  sync.Mutex
	assets                 *mcpAssetsModel
	closed, launching      bool
	Assets                 []mcpclient.Metadata
	Form                   *mcpForm
	Review                 *mcpAssetsReview
	Confirmation, CopyName string
	Authorized             []bool
	Preview                string
	Blocked                bool
	Pending                string
	PendingPage            int
	PendingWorkspace       int
	List                   ui.ListState
	ListIndex              int
	ServerList             ui.ListState
	ParameterLists         [3]ui.ListState
	ParameterIndices       [3]int
	SlotList               ui.ListState
	SlotIndex              int
	Scroll, ReviewScroll   ui.ScrollState
}

func (state *mcpEditorState) session() *mcpAssetsModel {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	return state.assets
}

func (state *mcpEditorState) attach(assets *mcpAssetsModel) bool {
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

func (state *mcpEditorState) close() {
	state.mutex.Lock()
	state.closed = true
	assets := state.assets
	state.assets = nil
	state.mutex.Unlock()
	if assets != nil {
		assets.Close()
	}
}

func (model *Model) mcpState() *mcpEditorState {
	configuration := model.configurationState()
	if configuration.MCP == nil {
		configuration.MCP = &mcpEditorState{PendingPage: -1, PendingWorkspace: -1, ListIndex: -1}
	}
	return configuration.MCP
}

func (model *Model) mcpText(chinese, english string) string {
	if model.configurationState().MCPEnglish {
		return english
	}
	return chinese
}

func (model *Model) mcpCancel(force bool) {
	if model.configuration == nil || model.configuration().MCP == nil {
		return
	}
	state := model.configuration().MCP
	if state.launching && !force {
		return
	}
	state.Form.clearSecrets()
	state.Review, state.Authorized, state.Preview = nil, nil, ""
	state.Confirmation = ""
	state.close()
	model.configuration().MCP = nil
}

func (model *Model) mcpMessage(err error) string {
	switch {
	case err == nil:
		return model.mcpText("服务端资产操作完成；未连接 MCP 或应用本机。", "Server asset operation completed; no MCP connection or native application.")
	case errors.Is(err, mcpclient.ErrUnknown):
		return model.mcpText("Unknown：结果未核实，禁止重放。只能显式载入核实，不能推断回滚或重试创建。", "Unknown: do not replay. Explicitly reconcile; never assume rollback or retry creation.")
	case errors.Is(err, mcpclient.ErrConflict):
		return model.mcpText("CAS 冲突：未覆盖。请显式载入并重新审阅。", "CAS conflict: not overwritten. Reload and review again.")
	case errors.Is(err, errMCPReadOnly):
		return model.mcpText("未知字段或版本：只读，禁止覆盖、复制或删除。", "Unknown fields or schema: read only; no overwrite, copy or delete.")
	case errors.Is(err, mcpclient.ErrDenied), errors.Is(err, errConfigurationIdentity):
		return model.mcpText("身份已失效，请重新登录。", "Identity expired. Sign in again.")
	case errors.Is(err, mcpclient.ErrInvalid):
		return model.mcpText("输入无效：检查标识、URL、集合存在位及旧引用的显式 CLEAR。", "Invalid input: check identifiers, URL, collection presence and explicit CLEAR of removed refs.")
	default:
		return model.mcpText("操作未完成或已取消；不保证回滚，不自动重发。", "Operation failed or canceled; rollback is not guaranteed. Never automatically replay.")
	}
}

func (model *Model) mcpJob(label string, work func(context.Context) (func(), error), cleanup ...func()) {
	state := model.mcpState()
	state.launching = true
	defer func() { state.launching = false }()
	model.jobWithTimeout(label, 60*time.Second, func(ctx context.Context) (func(), error) {
		commit, err := work(ctx)
		if ctx.Err() != nil {
			state.close()
		}
		return func() {
			model.Status = model.mcpMessage(err)
			if commit != nil {
				commit()
			}
			if errors.Is(err, mcpclient.ErrDenied) || errors.Is(err, errConfigurationIdentity) {
				model.mcpCancel(true)
			}
		}, nil
	}, cleanup...)
}

func (model *Model) LoadMCPAssets() {
	if model.closed || model.Busy || !model.ready() || model.session.value() == "" {
		return
	}
	backend, ok := model.backend.(interface {
		ConfigurationIdentity(context.Context, string, string) (configurationIdentity, error)
		openMCPAssets(context.Context, configurationIdentity) (*mcpAssetsModel, error)
	})
	if !ok {
		model.Status = model.mcpMessage(errConfigurationUnsupported)
		return
	}
	model.mcpCancel(true)
	state := model.mcpState()
	origin, session := model.origin, model.session.value()
	model.mcpJob(model.mcpText("载入 MCP 资产", "Load MCP assets"), func(ctx context.Context) (func(), error) {
		identity, err := backend.ConfigurationIdentity(ctx, origin, session)
		if err != nil {
			return nil, err
		}
		defer identity.clear()
		assets, err := backend.openMCPAssets(ctx, identity)
		if err != nil {
			return nil, err
		}
		if !state.attach(assets) {
			return nil, mcpclient.ErrClosed
		}
		list, err := assets.List(ctx)
		if err != nil {
			state.close()
			return nil, err
		}
		return func() { state.Assets = list }, nil
	})
}

func (model *Model) mcpRequest(action string) {
	if model.Busy {
		return
	}
	state := model.mcpState()
	state.Form.clearSecrets()
	if state.Form != nil && state.Form.Dirty {
		state.Pending = action
		return
	}
	model.mcpAction(action)
}

func (model *Model) mcpAction(action string) {
	state := model.mcpState()
	switch action {
	case "reload":
		model.LoadMCPAssets()
	case "new":
		if state.session() != nil && !state.Blocked {
			state.Form = newMCPForm(mcpclient.EditView{Draft: mcpclient.Draft{AgentID: "claude"}})
			state.CopyName, state.Preview = "", ""
		}
	default:
		assets := state.session()
		if assets == nil {
			return
		}
		model.mcpJob(model.mcpText("读取 MCP 详情", "Read MCP details"), func(ctx context.Context) (func(), error) {
			view, err := assets.Read(ctx, action)
			if err != nil {
				return nil, err
			}
			return func() {
				state.Form = newMCPForm(view)
				state.CopyName = view.Metadata.Name + model.mcpText(" 副本", " copy")
				state.Preview = ""
			}, nil
		})
	}
}

func (model *Model) PrepareMCP(action string) {
	state := model.mcpState()
	assets := state.session()
	if model.closed || model.Busy || state.Blocked || state.Review != nil || assets == nil || state.Form == nil || state.Form.ReadOnly || model.session.value() == "" {
		return
	}
	form := state.Form
	change := mcpclient.Change{Action: action, ID: form.Metadata.ID, Revision: form.Metadata.Revision}
	var input *mcpclient.Input
	if action == "create" || action == "update" || action == "preview" {
		var err error
		input, err = form.input(context.Background())
		form.clearSecrets()
		if err != nil {
			model.Status = model.mcpMessage(err)
			return
		}
	} else if form.Dirty {
		return
	}
	if action == "copy" {
		change.Name = state.CopyName
	}
	model.mcpJob(model.mcpText("MCP 逻辑预览与审阅", "MCP logical preview and review"), func(ctx context.Context) (func(), error) {
		if action == "preview" {
			summary, err := assets.Preview(ctx, change.ID, change.Revision, input)
			return func() { state.Preview = summary }, err
		}
		review, err := assets.Prepare(ctx, change, input)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Review = &review
			state.Confirmation = ""
			state.Authorized = make([]bool, len(review.Slots))
		}, nil
	}, func() {
		if input != nil {
			input.Close()
		}
	})
}

func (model *Model) SubmitMCP() {
	state := model.mcpState()
	if model.closed || model.Busy || state.Blocked || state.Review == nil || state.session() == nil || model.session.value() == "" || state.Confirmation != state.Review.Confirmation || len(state.Authorized) != len(state.Review.Slots) {
		return
	}
	for _, authorized := range state.Authorized {
		if !authorized {
			return
		}
	}
	assets, confirmation := state.session(), state.Confirmation
	state.Review, state.Authorized, state.Confirmation = nil, nil, ""
	state.Blocked = true
	model.mcpJob(model.mcpText("提交一次 MCP 资产操作", "Submit MCP asset operation once"), func(ctx context.Context) (func(), error) {
		metadata, err := assets.Submit(ctx, confirmation)
		return func() {
			if err == nil {
				model.Status = model.mcpText("服务端资产操作完成，请显式载入核实；未连接 MCP 或应用本机。", "Server asset operation completed. Explicitly reload to inspect; no MCP connection or native application.") + fmt.Sprintf(" %s / revision %d", metadata.ID, metadata.Revision)
			}
		}, err
	})
}

func (model *Model) CancelMCPReview() {
	if model.Busy {
		return
	}
	state := model.mcpState()
	state.Review, state.Authorized, state.Confirmation = nil, nil, ""
	if assets := state.session(); assets != nil {
		_ = assets.CancelReview()
	}
}

func (model *Model) mcpWorkspace(mcp bool) {
	configuration := model.configurationState()
	if configuration.MCPActive == mcp || model.Busy {
		return
	}
	state := model.mcpState()
	if configuration.MCPActive && state.Form != nil && state.Form.Dirty || !configuration.MCPActive && configuration.Form != nil && configuration.Form.Dirty {
		state.Form.clearSecrets()
		if configuration.Form != nil {
			configuration.Form.clearSecrets()
		}
		state.PendingWorkspace = 0
		if mcp {
			state.PendingWorkspace = 1
		}
		return
	}
	model.cancelJob()
	configuration.MCPActive = mcp
}
