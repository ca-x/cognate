package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/importclient"
	"google.golang.org/protobuf/proto"
)

type selectionPrivate struct{}

func (selectionPrivate) Format(destination fmt.State, _ rune) {
	_, _ = io.WriteString(destination, "[private desktop import]")
}
func (selectionPrivate) LogValue() slog.Value { return slog.StringValue("[private desktop import]") }
func (selectionPrivate) MarshalJSON() ([]byte, error) {
	return []byte(`"[private desktop import]"`), nil
}

type selectionBarrier struct{ Origin, Owner, ID string }

type selectionState struct {
	selectionPrivate
	Legacy                      bool
	Source                      importv1.ImportSelectionSource
	Path                        string
	UploadApproved              bool
	Scope                       *selectionScope
	Owner                       string
	Preview                     *importclient.SelectionPreview
	View                        *importv1.PreviewImportSelectionResponse
	Choice                      importclient.SelectionChoice
	Hash, PreviewPath           string
	PreviewSource               importv1.ImportSelectionSource
	Configurations              []selectionConfiguration
	SkillOptions, FrozenOptions []*importv1.ImportCandidateOptions
	Checked                     [3][]bool
	Notices                     []selectionNotice
	Bindings                    []*importv1.SkillBinding
	BindingSkill                int
	VerifiedSkills              map[string]*importv1.SkillBinding
	Review                      *selectionReview
	FinalApproved               bool
	OperationID                 string
	Result                      importclient.SelectionResult
}

type selectionScope struct {
	selectionPrivate
	mutex      sync.Mutex
	closed     bool
	connection *selectionConnection
}

func (scope *selectionScope) attach(connection *selectionConnection) bool {
	scope.mutex.Lock()
	if scope.closed {
		scope.mutex.Unlock()
		connection.Close()
		return false
	}
	scope.connection = connection
	scope.mutex.Unlock()
	return true
}

func (scope *selectionScope) Close() {
	scope.mutex.Lock()
	scope.closed = true
	connection := scope.connection
	scope.mutex.Unlock()
	if connection != nil {
		connection.Close()
	}
}

func (scope *selectionScope) get() *selectionConnection {
	if scope == nil {
		return nil
	}
	scope.mutex.Lock()
	defer scope.mutex.Unlock()
	if scope.closed {
		return nil
	}
	return scope.connection
}

func (model *Model) selectionState() *selectionState {
	if model.selection == nil {
		state := &selectionState{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON}
		model.selection = func() *selectionState { return state }
	}
	return model.selection()
}

func (model *Model) selectionCancel() {
	if model.selection != nil {
		state := model.selection()
		state.UploadApproved = false
		state.Preview = nil
		state.View = nil
		state.Choice = importclient.SelectionChoice{}
		state.Owner = ""
		state.Configurations = nil
		state.SkillOptions = nil
		state.FrozenOptions = nil
		state.Bindings = nil
		state.VerifiedSkills = nil
		state.Checked = [3][]bool{}
		state.Notices = nil
		state.Review = nil
		state.FinalApproved = false
		state.Hash = ""
		if scope := state.Scope; scope != nil {
			state.Scope = nil
			scope.mutex.Lock()
			if scope.connection != nil {
				scope.connection.stop()
			}
			scope.mutex.Unlock()
			model.pendingJobs.Add(1)
			model.workers.Go(func() { defer model.pendingJobs.Add(-1); scope.Close() })
		}
	}
}

func (model *Model) selectionJob(label string, work func(context.Context) (func(), error)) {
	model.selectionLaunching = true
	defer func() { model.selectionLaunching = false }()
	model.jobWithTimeout(label, 125*time.Second, func(ctx context.Context) (func(), error) {
		commit, err := work(ctx)
		return func() {
			if commit != nil {
				commit()
			}
			if err != nil {
				model.Status = selectionMessage(err)
			}
		}, nil
	})
}

func selectionMessage(err error) string {
	if errors.Is(err, importclient.ErrSelectionUnknown) || errors.Is(err, errSelectionBlocked) {
		return errSelectionBlocked.Error()
	}
	if errors.Is(err, importclient.ErrIncomplete) {
		return "导入未全部完成；请核对三类回执，部分收录不等于回滚"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "已取消或超时；写入可能已发生，仅查询原操作，不自动重发"
	}
	if errors.Is(err, errConfigurationIdentity) {
		return "身份已失效，请重新登录；不重发导入"
	}
	return errSelectionInvalid.Error()
}

func (model *Model) PreviewSelection() {
	state := model.selectionState()
	if model.closed || model.Busy || model.pendingJobs.Load() > 0 {
		return
	}
	if !state.UploadApproved || !model.ready() || model.session.value() == "" {
		model.Status = "请先登录并明确授权整个文件上传到此 origin"
		return
	}
	backend, ok := model.backend.(selectionBackend)
	if !ok {
		model.Status = "当前后端不支持选择导入"
		return
	}
	origin, session, file, source := model.origin, model.session.value(), state.Path, state.Source
	barriers := slices.Clone(model.selectionBarriers)
	scope := state.Scope
	if scope == nil {
		scope = new(selectionScope)
		state.Scope = scope
	}
	options, hash := state.options(), state.Hash
	state.invalidate()
	model.selectionJob("授权文件预览", func(ctx context.Context) (func(), error) {
		connection := scope.get()
		if connection == nil {
			var err error
			connection, err = backend.OpenSelection(ctx, origin, session)
			if err != nil {
				return nil, err
			}
			if !scope.attach(connection) {
				return nil, context.Canceled
			}
		}
		if !connection.matches(origin, session) {
			return nil, errConfigurationIdentity
		}
		if index := slices.IndexFunc(barriers, func(barrier selectionBarrier) bool {
			return barrier.Origin == origin && barrier.Owner == connection.Owner
		}); index >= 0 {
			return func() { state.Owner = connection.Owner; state.OperationID = barriers[index].ID }, errSelectionBlocked
		}
		preview, err := connection.Preview(ctx, source, file, hash, options)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Owner = connection.Owner
			state.load(preview, source, file, options)
			model.Status = "预览完成；三类默认不选，不会在本机安装或应用"
		}, nil
	})
}

func (connection *selectionConnection) matches(origin, session string) bool {
	if connection == nil || connection.private == nil || connection.Origin != origin {
		return false
	}
	data := connection.private()
	identity := data.identity.private()
	identity.mutex.Lock()
	defer identity.mutex.Unlock()
	return session != "" && identity.session == session
}

func (model *Model) selectionCurrent() bool {
	state := model.selectionState()
	connection := state.Scope.get()
	return state.Preview != nil && state.View != nil && state.UploadApproved && state.Preview.SHA256() == state.Hash && state.PreviewPath == state.Path && state.PreviewSource == state.Source && state.View.ExpiresMs > time.Now().UnixMilli() && connection != nil && connection.Owner == state.Owner && connection.matches(model.origin, model.session.value()) && selectionOptionsEqual(state.options(), state.FrozenOptions)
}

func (model *Model) LoadSelectionBindings(index int) {
	state := model.selectionState()
	if model.closed || model.Busy || model.pendingJobs.Load() > 0 || !model.selectionCurrent() || index < 0 || index >= len(state.View.Skills) {
		return
	}
	skill := state.View.Skills[index]
	var name, branch string
	for _, repository := range state.View.Repositories {
		if repository.Id == skill.RepositoryCandidateId {
			name, branch = repository.Hint.Repository, repository.Hint.Branch
		}
	}
	if name == "" && skill.Binding != nil {
		name, branch = skill.Binding.Repository, skill.Binding.Branch
	}
	if name == "" {
		model.Status = "此候选没有可核对的仓库身份，不按名称猜测绑定"
		return
	}
	connection := state.Scope.get()
	state.Review = nil
	state.FinalApproved = false
	state.Bindings = nil
	state.BindingSkill = -1
	model.selectionJob("读取当前账号同仓库内容", func(ctx context.Context) (func(), error) {
		bindings, err := connection.Bindings(ctx, name, branch)
		return func() {
			state.Bindings = bindings
			state.BindingSkill = index
			model.Status = fmt.Sprintf("当前同仓库内容 %d 项；请明确选择完整身份，无内容时先收录仓库再重新预览", len(bindings))
		}, err
	})
}

func (model *Model) BindSelection(index int) {
	state := model.selectionState()
	if model.closed || model.Busy || !model.selectionCurrent() || state.BindingSkill < 0 || state.BindingSkill >= len(state.View.Skills) || index < 0 || index >= len(state.Bindings) {
		return
	}
	id := state.View.Skills[state.BindingSkill].Id
	if state.Source == 2 && !proto.Equal(state.View.Skills[state.BindingSkill].Binding, state.Bindings[index]) {
		model.Status = "Magpie 仅支持来源固定目录的服务器绑定，不重写来源路径"
		return
	}
	if state.VerifiedSkills == nil {
		state.VerifiedSkills = make(map[string]*importv1.SkillBinding)
	}
	state.VerifiedSkills[id] = proto.Clone(state.Bindings[index]).(*importv1.SkillBinding)
	if state.Source == 2 {
		state.Review = nil
		state.FinalApproved = false
		model.Status = "完整同仓身份已明确核对；可选择此技能引用"
		return
	}
	option := &importv1.ImportCandidateOptions{CandidateId: id, SkillBinding: proto.Clone(state.Bindings[index]).(*importv1.SkillBinding)}
	position := slices.IndexFunc(state.SkillOptions, func(option *importv1.ImportCandidateOptions) bool { return option.CandidateId == id })
	if position < 0 {
		state.SkillOptions = append(state.SkillOptions, option)
	} else {
		state.SkillOptions[position] = option
	}
	model.SelectionOptionsChanged()
}

func (model *Model) CommitSelection() {
	state := model.selectionState()
	if model.closed || model.Busy || model.pendingJobs.Load() > 0 {
		return
	}
	if !state.FinalApproved || state.Review == nil || !model.selectionCurrent() || !state.skillsConfirmed() || state.Review.Preview != state.Preview || !selectionChoiceEqual(state.choice(), state.Review.Choice) {
		model.Status = "本次审阅未明确批准或已失效；请重新审阅"
		return
	}
	connection := state.Scope.get()
	if slices.ContainsFunc(model.selectionBarriers, func(barrier selectionBarrier) bool {
		return barrier.Origin == model.origin && barrier.Owner == state.Owner
	}) {
		model.Status = errSelectionBlocked.Error()
		return
	}
	operation, err := connection.Begin(state.Preview, state.Review.Choice)
	if err != nil {
		state.invalidate()
		model.Status = selectionMessage(err)
		return
	}
	id, origin, owner, file := operation.ID(), connection.Origin, connection.Owner, state.Path
	model.selectionBarriers = append(model.selectionBarriers, selectionBarrier{Origin: origin, Owner: owner, ID: id})
	state.OperationID = id
	state.invalidate()
	state.View = nil
	state.Configurations = nil
	state.SkillOptions = nil
	state.FrozenOptions = nil
	state.Bindings = nil
	state.VerifiedSkills = nil
	state.Hash = ""
	state.PreviewPath = ""
	state.PreviewSource = 0
	state.UploadApproved = false
	state.Result = importclient.SelectionResult{OperationID: id, Observation: importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_UNKNOWN}
	model.selectionJob("提交一次明确选择", func(ctx context.Context) (func(), error) {
		result, err := connection.Commit(ctx, operation, file)
		connection.Close()
		return func() {
			state.Scope = nil
			state.Result = result
			if result.Observation == importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL || !errors.Is(err, importclient.ErrSelectionUnknown) && result.Phase == 0 {
				model.selectionClearBarrier(origin, owner, id)
			}
			model.Status = "导入回执已收到；请核对三类计数及 Phase，不代表本机应用"
		}, err
	})
}

func (model *Model) selectionClearBarrier(origin, owner, id string) {
	model.selectionBarriers = slices.DeleteFunc(model.selectionBarriers, func(barrier selectionBarrier) bool {
		return barrier.Origin == origin && barrier.Owner == owner && barrier.ID == id
	})
}

func (model *Model) QuerySelectionStatus() {
	state := model.selectionState()
	if model.closed || model.Busy || model.pendingJobs.Load() > 0 || model.session.value() == "" || !model.ready() {
		return
	}
	backend, ok := model.backend.(selectionBackend)
	if !ok {
		return
	}
	id, origin, session := state.OperationID, model.origin, model.session.value()
	state.Result = importclient.SelectionResult{OperationID: id, Observation: importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_UNKNOWN}
	scope := state.Scope
	if scope == nil {
		scope = new(selectionScope)
		state.Scope = scope
	}
	model.selectionJob("只读查询原 operation ID", func(ctx context.Context) (func(), error) {
		connection := scope.get()
		if connection == nil {
			var err error
			connection, err = backend.OpenSelection(ctx, origin, session)
			if err != nil {
				return nil, err
			}
			if !scope.attach(connection) {
				return nil, context.Canceled
			}
		}
		if !connection.matches(origin, session) {
			return nil, errConfigurationIdentity
		}
		result, err := connection.Status(ctx, id)
		return func() {
			if err != nil {
				return
			}
			state.Owner = connection.Owner
			state.Result = result
			if result.Observation == importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL {
				model.selectionClearBarrier(origin, connection.Owner, id)
			}
			model.Status = "仅查询原操作；NotObserved 不证明在途提交未来不会完成"
		}, err
	})
}
