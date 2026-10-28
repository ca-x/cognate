package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/ca-x/cognate/internal/subscription"
	"github.com/spf13/cobra"
)

func newPackWatchCommand() *cobra.Command {
	options := subscription.WatchOptions{}
	command := &cobra.Command{
		Use: "watch", Short: "前台持续同步组合；Ctrl-C 停止，空闲期间释放安装锁", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (resultErr error) {
			if options.Interval == 0 || options.AttemptTimeout == 0 {
				return subscription.ErrInvalidWatchOptions
			}
			if err := options.Validate(); err != nil {
				return err
			}
			if command.Context().Err() != nil {
				return nil
			}
			output, closeOutput, err := prepareWatchOutput(command.Context(), command.OutOrStdout())
			if err != nil {
				return err
			}
			defer func() { resultErr = errors.Join(resultErr, closeOutput()) }()
			err = subscription.Watch(command.Context(), options, func(ctx context.Context) (subscription.SyncResult, error) {
				manager, err := openSubscriptionManager(ctx)
				if err != nil {
					return subscription.SyncResult{}, err
				}
				result, operationErr := manager.Sync(ctx)
				return result, errors.Join(operationErr, manager.Close())
			}, func(_ context.Context, report subscription.WatchReport) error {
				if report.Succeeded {
					if err := showSyncResult(output, report.Result); err != nil {
						return err
					}
					_, err := fmt.Fprintf(output, "下次同步：%s 后；Ctrl-C 停止\n", report.NextAttemptIn)
					return err
				}
				_, err := fmt.Fprintf(output, "同步未完成（连续失败 %d 次）；%s 后重试；可用 pack sync 和 install-status 检查\n", report.ConsecutiveFailures, report.NextAttemptIn)
				return err
			})
			if errors.Is(err, context.Canceled) && command.Context().Err() != nil {
				return nil
			}
			return err
		},
	}
	command.Flags().DurationVar(&options.Interval, "interval", subscription.DefaultWatchInterval, "成功后同步间隔（1s–24h，完成一轮后计时）")
	command.Flags().DurationVar(&options.AttemptTimeout, "attempt-timeout", subscription.DefaultWatchAttemptTimeout, "单次同步超时（1s–10m）")
	return command
}
