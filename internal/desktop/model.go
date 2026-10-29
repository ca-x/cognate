package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/egoist/mygo/ui"
)

type Backend interface {
	InitializeClientKey(context.Context) error
	Login(context.Context, string, string, string) (string, error)
	Logout(context.Context, string, string) error
	Search(context.Context, string, string, string) (*catalogv1.ListSkillsResponse, error)
	Details(context.Context, string, string) (*catalogv1.PublicSkill, error)
	Install(context.Context, string, *catalogv1.PublicSkill) error
	Packs(context.Context, string, string, string, string) ([]state.Subscription, error)
	WatchCycle(context.Context) (subscription.SyncResult, []state.Subscription, error)
	Preview(context.Context, string, string, string) (*importclient.Client, *importclient.Preview, error)
}

func (*Model) String() string       { return "[redacted desktop model]" }
func (*Model) GoString() string     { return "[redacted desktop model]" }
func (*Model) LogValue() slog.Value { return slog.StringValue("[redacted desktop model]") }
func (*Model) Format(destination fmt.State, _ rune) {
	_, _ = io.WriteString(destination, "[redacted desktop model]")
}

type Model struct {
	backend                                                 Backend
	dispatch                                                func(func())
	life                                                    context.Context
	stop                                                    context.CancelFunc
	cancel                                                  context.CancelFunc
	workers                                                 sync.WaitGroup
	pendingJobs                                             atomic.Int32
	publication                                             sync.Mutex
	slots                                                   chan struct{}
	generation                                              uint64
	closed                                                  bool
	watch                                                   *watchRun
	watchOptions                                            subscription.WatchOptions
	WatchActive, WatchStopping                              bool
	WatchStatus                                             string
	origin, session                                         string
	Origin, Username, Password, Query, ShareToken, FilePath string
	Page                                                    int
	Status                                                  string
	Busy                                                    bool
	Skills                                                  []*catalogv1.PublicSkill
	Cursor                                                  string
	Selected                                                int
	Detail                                                  *catalogv1.PublicSkill
	Confirm                                                 bool
	Subscriptions                                           []state.Subscription
	PackSelected                                            int
	Candidates                                              []importclient.Candidate
	ImportSelected                                          []bool
	ImportResults                                           []string
	preview                                                 *importclient.Preview
	importer                                                *importclient.Client
	previewPath                                             string
	skillList, packList                                     ui.ListState
	originInput                                             string
	Logo                                                    *ui.Bitmap
}

func NewModel(backend Backend, dispatch func(func())) *Model {
	life, stop := context.WithCancel(context.Background())
	return &Model{backend: backend, dispatch: dispatch, life: life, stop: stop, slots: make(chan struct{}, 1), Selected: -1, PackSelected: -1, Status: "填写服务地址；公开目录无需登录"}
}

func (model *Model) Cancel() {
	model.StopWatch()
	model.cancelJob()
	model.Password, model.ShareToken, model.session = "", "", ""
}

func (model *Model) cancelJob() {
	model.generation++
	if model.cancel != nil {
		model.cut(model.cancel)
		model.cancel = nil
	}
	model.Busy = false
	model.Status = "已取消；若写入已开始，请刷新核实本地状态"
}

func (model *Model) clearOwner() {
	model.StopWatch()
	model.session = ""
	model.Password = ""
	model.ShareToken = ""
	model.Skills = nil
	model.Selected = -1
	model.Detail = nil
	model.Confirm = false
	model.Cursor = ""
	model.Subscriptions = nil
	model.PackSelected = -1
	model.preview = nil
	model.importer = nil
	model.Candidates = nil
	model.ImportSelected = nil
	model.ImportResults = nil
	model.previewPath = ""
}

func (model *Model) SetOrigin(raw string) {
	model.Cancel()
	model.clearOwner()
	model.Origin = raw
	model.originInput = raw
	origin, err := ValidateOrigin(raw)
	model.origin = origin
	if err != nil {
		model.Status = err.Error()
		return
	}
	model.Status = "地址已切换；匿名目录可用，请重新登录"
}

func (model *Model) Close() {
	if model.closed {
		return
	}
	model.closed = true
	model.cut(model.stop)
	model.Cancel()
	model.clearOwner()
}

func (model *Model) Wait() { model.workers.Wait() }

func (model *Model) job(label string, work func(context.Context) (func(), error)) {
	model.jobWithTimeout(label, 60*time.Second, work)
}

func (model *Model) localWorkBlocked() bool {
	return model.pendingJobs.Load() > 0 || model.watchLocksLocal()
}

func (model *Model) InitializeClientKey() {
	if model.closed {
		return
	}
	if model.localWorkBlocked() || model.Busy {
		model.Status = "请先停止后台同步并等待排空；当前任务尚未结束"
		return
	}
	backend := model.backend
	model.jobWithTimeout("正在初始化", 30*time.Second, func(ctx context.Context) (func(), error) {
		err := backend.InitializeClientKey(ctx)
		return func() { model.Status = "本机客户端密钥已就绪（未初始化服务端会话）" }, err
	})
}

func (model *Model) jobWithTimeout(label string, limit time.Duration, work func(context.Context) (func(), error)) {
	if model.closed {
		return
	}
	model.cancelJob()
	generation := model.generation
	ctx, cancel := context.WithTimeout(model.life, limit)
	model.cancel = cancel
	model.Busy = true
	model.Status = label + "…"
	model.pendingJobs.Add(1)
	model.workers.Go(func() {
		defer cancel()
		var commit func()
		var err error
		select {
		case model.slots <- struct{}{}:
			if ctx.Err() == nil {
				commit, err = work(ctx)
			} else {
				err = ctx.Err()
			}
			<-model.slots
		case <-ctx.Done():
			err = ctx.Err()
		}
		model.pendingJobs.Add(-1)
		contextErr := ctx.Err()
		if model.life.Err() != nil || errors.Is(contextErr, context.Canceled) {
			model.publish(model.life, func() {})
			return
		}
		delivery := ctx
		if errors.Is(contextErr, context.DeadlineExceeded) {
			delivery = model.life
		}
		model.publish(delivery, func() {
			if model.closed || model.generation != generation {
				return
			}
			model.Busy = false
			if err != nil {
				model.Status = safeTaskError(err)
				return
			}
			if contextErr != nil {
				model.Status = "操作已取消或超时"
				return
			}
			model.Status = "完成"
			if commit != nil {
				commit()
			}
		})
	})
}

func (model *Model) ready() bool {
	if model.origin == "" {
		model.Status = "请填写有效服务地址"
		return false
	}
	return true
}

func (model *Model) Login() {
	if !model.ready() {
		return
	}
	origin, username, password := model.origin, model.Username, model.Password
	model.clearOwner()
	model.job("登录", func(ctx context.Context) (func(), error) {
		token, err := model.backend.Login(ctx, origin, username, password)
		return func() { model.session = token; model.Status = "已登录（会话仅保存在内存）" }, err
	})
}

func (model *Model) Logout() {
	origin, token := model.origin, model.session
	model.Cancel()
	model.clearOwner()
	if token == "" {
		model.Status = "已退出本地会话"
		return
	}
	model.job("退出登录", func(ctx context.Context) (func(), error) {
		err := model.backend.Logout(ctx, origin, token)
		return func() { model.Status = "已退出登录" }, err
	})
}

func (model *Model) Search(next bool) {
	if !model.ready() {
		return
	}
	origin, query, cursor := model.origin, model.Query, ""
	if next {
		cursor = model.Cursor
	}
	model.Detail = nil
	model.Confirm = false
	model.Selected = -1
	model.job("查询公开目录", func(ctx context.Context) (func(), error) {
		response, err := model.backend.Search(ctx, origin, query, cursor)
		return func() {
			model.Skills = response.Skills
			model.Cursor = response.NextCursor
			model.Status = fmt.Sprintf("公开目录：%d 项", len(model.Skills))
		}, err
	})
}

func (model *Model) Select(index int) {
	model.cancelJob()
	model.Selected = index
	model.Detail = nil
	model.Confirm = false
	model.Status = "已选择；查看详情后确认安装"
}

func (model *Model) Details() {
	if model.Selected < 0 || model.Selected >= len(model.Skills) || !model.ready() {
		return
	}
	origin, id := model.origin, model.Skills[model.Selected].Id
	model.Detail = nil
	model.Confirm = false
	model.job("获取详情", func(ctx context.Context) (func(), error) {
		entry, err := model.backend.Details(ctx, origin, id)
		return func() {
			model.Detail = entry
			model.Confirm = false
			model.Status = "详情已核实；安装前请确认内容与目标"
		}, err
	})
}

func (model *Model) Install() {
	if model.localWorkBlocked() {
		model.Status = "请先停止后台同步并等待排空，再手动安装"
		return
	}
	if model.Detail == nil || !model.Confirm || !model.ready() {
		model.Status = "请先获取详情并明确确认安装"
		return
	}
	origin, entry := model.origin, model.Detail
	model.Confirm = false
	model.job("安装技能", func(ctx context.Context) (func(), error) {
		err := model.backend.Install(ctx, origin, entry)
		return func() { model.Status = "安装完成：" + entry.Slug }, err
	})
}

func (model *Model) Packs(action string) {
	if model.localWorkBlocked() {
		model.Status = "请先停止后台同步并等待排空，再操作 Pack"
		return
	}
	if !model.ready() {
		return
	}
	origin, token, id := model.origin, model.ShareToken, ""
	model.ShareToken = ""
	if action == "remove" {
		if model.PackSelected < 0 || model.PackSelected >= len(model.Subscriptions) {
			return
		}
		id = model.Subscriptions[model.PackSelected].ID
	}
	model.job("Pack "+action, func(ctx context.Context) (func(), error) {
		records, err := model.backend.Packs(ctx, action, origin, token, id)
		return func() {
			model.Subscriptions = records
			model.PackSelected = -1
			model.Status = "Pack 操作完成；订阅/移除后请显式同步应用安装变更"
		}, err
	})
}

func (model *Model) Preview() {
	model.ImportResults = nil
	if !model.ready() {
		return
	}
	if model.session == "" {
		model.Status = "导入需要先登录本地账号"
		return
	}
	origin, session, path := model.origin, model.session, model.FilePath
	model.preview = nil
	model.Candidates = nil
	model.ImportSelected = nil
	model.job("预览 Magpie 导出文件", func(ctx context.Context) (func(), error) {
		client, preview, err := model.backend.Preview(ctx, origin, session, path)
		return func() {
			model.importer = client
			model.preview = preview
			model.previewPath = path
			model.Candidates = preview.Candidates()
			model.ImportSelected = make([]bool, len(model.Candidates))
			model.Status = "预览完成；仅应用明确勾选的仓库，不会安装本地技能"
		}, err
	})
}

func (model *Model) Apply() {
	if model.preview == nil || model.importer == nil || model.previewPath != model.FilePath {
		model.Status = "文件路径已变化，请重新预览"
		return
	}
	selected := []int{}
	for index, checked := range model.ImportSelected {
		if checked {
			selected = append(selected, model.Candidates[index].Index)
		}
	}
	if len(selected) == 0 {
		model.Status = "请明确选择需要导入的仓库"
		return
	}
	client, preview, path := model.importer, model.preview, model.previewPath
	selected = slices.Clone(selected)
	model.job("应用已选择仓库", func(ctx context.Context) (func(), error) {
		result, err := client.ApplyFile(ctx, path, preview.SHA256(), preview, selected)
		if err != nil && len(result.Outcomes) == 0 {
			return nil, networkError(err)
		}
		return func() {
			model.ImportResults = nil
			for _, outcome := range result.Outcomes {
				model.ImportResults = append(model.ImportResults, fmt.Sprintf("%s: %s (%s)", outcome.Candidate.Repository, outcome.Status, outcome.Code))
			}
			model.preview = nil
			model.Candidates = nil
			model.ImportSelected = nil
			model.Status = fmt.Sprintf("导入处理完成：%d 项；逐项结果如下", len(result.Outcomes))
			if err != nil {
				model.Status = "导入未全部完成；请核对逐项结果，重新预览后重试"
			}
		}, nil
	})
}

var _ Backend = (*Service)(nil)
