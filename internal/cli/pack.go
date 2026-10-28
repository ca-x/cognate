package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/spf13/cobra"
)

func newPackCommand() *cobra.Command {
	command := &cobra.Command{Use: "pack", Short: "订阅、同步与移除服务端技能组合"}
	command.AddCommand(newPackSubscribeCommand(), newPackListCommand(), newPackSyncCommand(), newPackRemoveCommand())
	return command
}

func newPackSubscribeCommand() *cobra.Command {
	var server string
	var tokenStdin bool
	command := &cobra.Command{
		Use: "subscribe", Short: "从标准输入读取分享令牌，保存订阅并安装", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !tokenStdin {
				return fmt.Errorf("必须使用 --token-stdin 从标准输入提供分享令牌")
			}
			data, err := io.ReadAll(io.LimitReader(command.InOrStdin(), 2049))
			defer clear(data)
			if err != nil || len(data) > 2048 {
				return fmt.Errorf("无法读取有界分享凭据")
			}
			credential := string(bytes.TrimSpace(data))
			if _, err := packclient.New(server, credential, nil); err != nil {
				return err
			}
			manager, err := openSubscriptionManager(command.Context())
			if err != nil {
				return err
			}
			record, operationErr := manager.Subscribe(command.Context(), server, credential)
			if operationErr != nil {
				return errors.Join(operationErr, manager.Close())
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "已保存订阅 %s · %s\n", terminalText(record.ID), terminalText(record.Name)); err != nil {
				return errors.Join(err, manager.Close())
			}
			result, operationErr := manager.Sync(command.Context())
			operationErr = errors.Join(operationErr, manager.Close())
			if operationErr != nil {
				return fmt.Errorf("订阅已保存；同步未完成，可重试 pack sync 或检查 install-status：%w", operationErr)
			}
			return showSyncResult(command.OutOrStdout(), result)
		},
	}
	command.Flags().StringVar(&server, "server", "", "Cognate HTTPS origin（显式 loopback 可使用 HTTP）")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "从标准输入读取分享令牌；不支持命令行参数传入令牌")
	_ = command.MarkFlagRequired("server")
	return command
}

func newPackListCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "列出订阅及已应用/待应用版本，不显示凭据", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			home, err := localHome()
			if err != nil {
				return err
			}
			if _, err := os.Lstat(filepath.Join(home, ".local", "share", "skillhub")); errors.Is(err, os.ErrNotExist) {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有技能组合订阅")
				return err
			} else if err != nil {
				return err
			}
			manager, err := openSubscriptionManager(command.Context())
			if err != nil {
				return err
			}
			records, operationErr := manager.List(command.Context())
			if err := errors.Join(operationErr, manager.Close()); err != nil {
				return err
			}
			if len(records) == 0 {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有技能组合订阅")
				return err
			}
			output := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(output, "ID\tNAME\tSERVER\tAPPLIED\tDESIRED\tSTATUS"); err != nil {
				return err
			}
			for _, record := range records {
				status := "待同步"
				if !record.Enabled {
					status = "待移除"
				} else if record.AppliedRevision > 0 && record.AppliedManifestHash == record.DesiredManifestHash {
					status = "已应用"
				}
				if _, err := fmt.Fprintf(output, "%s\t%s\t%s\t%d\t%d\t%s\n", terminalText(record.ID), terminalText(record.Name), terminalText(record.Server), record.AppliedRevision, record.DesiredRevision, status); err != nil {
					return err
				}
			}
			return output.Flush()
		},
	}
}

func newPackSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use: "sync", Short: "同步全部组合，去重下载并备份更新或移除的 skill", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			manager, err := openSubscriptionManager(command.Context())
			if err != nil {
				return err
			}
			result, operationErr := manager.Sync(command.Context())
			if err := errors.Join(operationErr, manager.Close()); err != nil {
				return err
			}
			return showSyncResult(command.OutOrStdout(), result)
		},
	}
}

func newPackRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use: "remove SUBSCRIPTION_ID", Short: "移除订阅；只有无其他组合引用的自有安装才会备份卸载", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			manager, err := openSubscriptionManager(command.Context())
			if err != nil {
				return err
			}
			if err := manager.Unsubscribe(command.Context(), arguments[0]); err != nil {
				return errors.Join(err, manager.Close())
			}
			result, operationErr := manager.Sync(command.Context())
			if err := errors.Join(operationErr, manager.Close()); err != nil {
				return fmt.Errorf("移除请求已保存；清理未完成：%w", err)
			}
			return showSyncResult(command.OutOrStdout(), result)
		},
	}
}

func prepareClientHome(ctx context.Context) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if runtime.GOOS != "linux" {
		return "", "", fmt.Errorf("local installation is unsupported on this platform")
	}
	home, err := localHome()
	if err != nil {
		return "", "", err
	}
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := prepareStateDirectory(directory); err != nil {
		return "", "", err
	}
	if _, err := validateDatabasePath(directory); err != nil {
		return "", "", err
	}
	canonical := filepath.Join(home, ".agents", "skills")
	if err := createTrustedDirectory(canonical, false); err != nil {
		return "", "", err
	}
	if _, err := validateLocalDirectories(home, canonical); err != nil {
		return "", "", err
	}
	return home, directory, nil
}

func openSubscriptionManager(ctx context.Context) (*subscription.Manager, error) {
	home, directory, err := prepareClientHome(ctx)
	if err != nil {
		return nil, err
	}
	store, err := state.Open(ctx, filepath.Join(directory, "client.db"))
	if err != nil {
		return nil, err
	}
	records, readErr := store.Subscriptions(ctx)
	if err := errors.Join(readErr, store.Close()); err != nil {
		return nil, err
	}
	key, err := loadClientKey(home, len(records) == 0)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return subscription.Open(ctx, home, directory, key, nil)
}

func showSyncResult(output io.Writer, result subscription.SyncResult) error {
	if _, err := fmt.Fprintf(output, "同步完成：%d 个订阅，新增 %d，更新 %d，复用 %d，移除 %d\n", result.Subscriptions, result.Installed, result.Updated, result.Reused, result.Removed); err != nil {
		return err
	}
	for _, id := range result.Backups {
		if _, err := fmt.Fprintf(output, "已保留备份 %s\n", terminalText(id)); err != nil {
			return err
		}
	}
	return nil
}

func newUninstallLocalCommand() *cobra.Command {
	return &cobra.Command{
		Use: "uninstall-local SLUG", Short: "备份并卸载自己记录的本地安装", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if err := install.ValidateSlug(arguments[0]); err != nil {
				return err
			}
			_, directory, err := prepareClientHome(command.Context())
			if err != nil {
				return err
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			backup, operationErr := session.UninstallLocal(command.Context(), arguments[0])
			if err := errors.Join(operationErr, session.Close()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "已卸载 %s；备份 %s\n", terminalText(arguments[0]), terminalText(backup.ID))
			return err
		},
	}
}
