package desktop

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"

	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
)

type watchRun struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (model *Model) publish(ctx context.Context, update func()) {
	model.publication.Lock()
	defer model.publication.Unlock()
	if model.life.Err() == nil && ctx.Err() == nil {
		model.dispatch(update)
	}
}

func (model *Model) cut(cancel context.CancelFunc) {
	model.publication.Lock()
	defer model.publication.Unlock()
	cancel()
}

func safeTaskError(err error) string {
	switch {
	case errors.Is(err, errUnsupportedLocal):
		return "本地安装、Pack 同步和后台同步当前仅支持 Linux"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "操作已取消或超时；若写入已开始，请核实本地状态"
	default:
		return "操作未完成；请检查服务权限、本地修改或待恢复状态后重试"
	}
}

func (model *Model) watchLocksLocal() bool { return model.watch != nil }

func (model *Model) StartWatch() {
	if model.closed || !model.ready() {
		return
	}
	if runtime.GOOS != "linux" {
		model.WatchStatus = safeTaskError(errUnsupportedLocal)
		return
	}
	if model.watch != nil || model.Busy {
		model.WatchStatus = "请等待当前任务或后台同步排空"
		return
	}
	if model.watchOptions.Validate() != nil {
		model.WatchStatus = "后台同步周期或超时设置无效"
		return
	}
	ctx, cancel := context.WithCancel(model.life)
	run := &watchRun{ctx: ctx, cancel: cancel}
	model.watch = run
	model.WatchActive, model.WatchStopping = true, false
	model.WatchStatus = "后台同步已启用：正在执行首轮"
	options := model.watchOptions
	model.workers.Go(func() {
		defer cancel()
		var records []state.Subscription
		_ = subscription.Watch(ctx, options, func(attempt context.Context) (subscription.SyncResult, error) {
			select {
			case model.slots <- struct{}{}:
				defer func() { <-model.slots }()
			case <-attempt.Done():
				return subscription.SyncResult{}, attempt.Err()
			}
			if err := attempt.Err(); err != nil {
				return subscription.SyncResult{}, err
			}
			result, latest, err := model.backend.WatchCycle(attempt)
			records = latest
			return result, err
		}, func(ctx context.Context, report subscription.WatchReport) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			snapshot := slices.Clone(records)
			for index := range snapshot {
				snapshot[index].Skills = slices.Clone(snapshot[index].Skills)
			}
			model.publish(ctx, func() {
				if model.closed || model.watch != run || !model.WatchActive || run.ctx.Err() != nil {
					return
				}
				if !report.Succeeded {
					model.WatchStatus = fmt.Sprintf("后台同步未完成（连续 %d 轮）；%s 后重试。请核实权限、本地修改或待恢复状态", report.ConsecutiveFailures, report.NextAttemptIn)
					return
				}
				model.Subscriptions = snapshot
				model.PackSelected = -1
				model.WatchStatus = fmt.Sprintf("后台同步完成：安装 %d、更新 %d、复用 %d、备份移除 %d；%s 后下一轮", report.Result.Installed, report.Result.Updated, report.Result.Reused, report.Result.Removed, report.NextAttemptIn)
			})
			return nil
		})
		if model.life.Err() != nil {
			return
		}
		model.publish(model.life, func() {
			if model.closed || model.watch != run {
				return
			}
			model.watch = nil
			model.WatchActive, model.WatchStopping = false, false
			model.WatchStatus = "后台任务已排空；写入可能已发生，请核实本地状态"
		})
	})
}

func (model *Model) StopWatch() {
	if model.watch == nil {
		return
	}
	model.cut(model.watch.cancel)
	model.WatchActive, model.WatchStopping = false, true
	model.WatchStatus = "正在停止后台同步；等待实际任务结束并释放本地锁"
}
