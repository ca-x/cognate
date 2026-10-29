package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/spf13/cobra"
)

var errNativeIO = errors.New("native configuration requires bounded private input/output")

type nativeTerminal interface {
	ReadContext(context.Context, []byte) (int, error)
	WriteContext(context.Context, []byte) (int, error)
	Close() error
}

type nativeOutput struct {
	ctx       context.Context
	writer    io.Writer
	close     func() error
	remaining int
}

func (output *nativeOutput) Write(data []byte) (int, error) {
	return output.WriteContext(output.ctx, data)
}

func (output *nativeOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := output.ctx.Err(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > output.remaining {
		return 0, errNativeIO
	}
	output.remaining -= len(data)
	if writer, ok := output.writer.(interface {
		WriteContext(context.Context, []byte) (int, error)
	}); ok {
		return writer.WriteContext(ctx, data)
	}
	return output.writer.Write(data)
}
func (output *nativeOutput) Close() error { return output.close() }

func prepareNativeOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	if existing, ok := writer.(*nativeOutput); ok {
		return &nativeOutput{ctx: ctx, writer: existing, close: func() error { return nil }, remaining: 64 << 10}, nil
	}
	close := func() error { return nil }
	switch value := writer.(type) {
	case *bytes.Buffer:
	case *os.File:
		owned, err := prepareNativeFileOutput(ctx, value)
		if err != nil {
			return nil, err
		}
		writer, close = owned, owned.Close
	default:
		return nil, errNativeIO
	}
	return &nativeOutput{ctx: ctx, writer: writer, close: close, remaining: 64 << 10}, nil
}

func readNativeSession(ctx context.Context, input io.Reader) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch value := input.(type) {
	case *bytes.Buffer, *bytes.Reader, *strings.Reader:
		return importclient.ReadSession(input)
	case *os.File:
		reader, err := prepareNativeFileInput(ctx, value)
		if err != nil {
			return "", err
		}
		session, readErr := importclient.ReadSession(reader)
		if err := errors.Join(readErr, reader.Close(), ctx.Err()); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errNativeIO
		}
		return session, nil
	default:
		return "", errNativeIO
	}
}

func nativeErrorMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "配置操作已取消或超时；已发生的消费或写入不会因此回滚。"
	case errors.Is(err, configclient.ErrNativeUnknown), errors.Is(err, configapply.ErrOutcomeUnknown):
		return "配置操作结果未知；请核实状态，不要重放消费。"
	case errors.Is(err, configapply.ErrConflict):
		return "配置已变化；本次计划不可继续。"
	case errors.Is(err, configapply.ErrUnsupportedPlatform):
		return "配置操作当前仅支持 Linux。"
	default:
		return "配置操作未完成；请核实参数、既有密钥、权限与本地状态。"
	}
}

func nativeInvocation(arguments []string) bool {
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		return argument == "config"
	}
	return false
}

func nativeExitDiagnostic(err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, prepareErr := prepareNativeOutput(ctx, os.Stderr)
	if prepareErr != nil {
		return
	}
	_, _ = fmt.Fprintln(output, nativeErrorMessage(err))
	_ = output.Close()
}

func newNativeConfigurationCommand() *cobra.Command {
	parent := &cobra.Command{
		Use: "config", Short: "预览并手动应用 Claude 本机配置（既有 key，仅 Linux）", SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return command.Help() },
	}
	for _, action := range []string{"apply", "restore", "status"} {
		var server, owner, asset, profile, target, operation string
		var revision int64
		var apply, tokenStdin bool
		var timeout time.Duration
		command := &cobra.Command{Use: action, Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) (returnedError error) {
			if timeout <= 0 || timeout > 5*time.Minute {
				return nativeconfig.ErrInvalid
			}
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			if err := nativeConfigurationPlatform(ctx); err != nil {
				return err
			}
			if action == "apply" && (!tokenStdin || server == "" || owner == "" || asset == "" || profile == "" || target != "claude.settings" || revision <= 0) || action != "apply" && operation == "" {
				return nativeconfig.ErrInvalid
			}
			output, err := prepareNativeOutput(ctx, command.OutOrStdout())
			if err != nil {
				return err
			}
			defer func() { returnedError = errors.Join(returnedError, output.Close()) }()
			var terminal nativeTerminal
			if apply {
				terminal, err = openNativeConfirmationTTY(ctx)
				if err != nil {
					return err
				}
				defer func() { returnedError = errors.Join(returnedError, terminal.Close()) }()
			}
			home, err := localHome()
			if err != nil {
				return nativeconfig.ErrInvalid
			}
			key, err := loadClientKey(home, false)
			if err != nil {
				return nativeconfig.ErrInvalid
			}
			defer clear(key)
			directory := filepath.Join(home, ".local", "share", "skillhub")
			if err := prepareStateDirectory(directory); err != nil {
				return nativeconfig.ErrInvalid
			}
			var native *configclient.NativeClient
			var session string
			if action == "apply" {
				session, err = readNativeSession(ctx, command.InOrStdin())
				if err != nil {
					return err
				}
				native, err = configclient.NewNativeClient(server, nil)
				if err != nil {
					return err
				}
				defer native.Close()
			}
			client, err := nativeconfig.New(home, directory, server, key, native)
			if err != nil {
				return err
			}
			defer func() { returnedError = errors.Join(returnedError, client.Close()) }()
			interaction := nativeInteraction(output, terminal, server, owner, profile)
			if action == "status" {
				status, err := client.Status(ctx, operation)
				if err != nil {
					return err
				}
				if err := nativeShowStatus(output, status); err != nil {
					return err
				}
				if status.Status == "OutcomeUnknown" {
					return configapply.ErrOutcomeUnknown
				}
				if status.NeedsAttention {
					return configapply.ErrPending
				}
				return nil
			}
			var result nativeconfig.Result
			if action == "restore" {
				result, err = client.Restore(ctx, operation, apply, interaction)
			} else {
				result, err = client.Apply(ctx, nativeconfig.ApplyRequest{OwnerID: owner, AssetID: asset, AssetRevision: revision, ProfileID: profile, Apply: apply}, session, interaction)
			}
			if result.RemoteUnknown {
				_, _ = fmt.Fprintf(output, "远端结果未知；已核实状态：%s\n", terminalText(string(result.RemoteState)))
			}
			if result.Local.OperationID != "" {
				err = errors.Join(err, nativeShowStatus(output, result.Local))
			}
			if result.Local.NeedsAttention {
				err = errors.Join(err, configapply.ErrPending)
			}
			return err
		}}
		command.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "本次操作总上限（大于0且不超过5m）")
		if action == "apply" {
			command.Flags().StringVar(&server, "server", "", "HTTPS origin")
			command.Flags().StringVar(&owner, "owner", "", "明确的资产 owner")
			command.Flags().StringVar(&asset, "asset", "", "明确的资产 ID")
			command.Flags().Int64Var(&revision, "revision", 0, "已选择的资产 revision")
			command.Flags().StringVar(&profile, "profile", "", "本机 profile")
			command.Flags().StringVar(&target, "target", "", "仅 claude.settings/user")
			command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "仅从有界非TTY stdin读取 session，不表示批准")
		} else if action == "restore" {
			command.Flags().StringVar(&operation, "source-operation", "", "已应用操作 ID；必须重新预览确认")
		} else {
			command.Flags().StringVar(&operation, "operation", "", "本机 durable 操作 ID")
		}
		if action != "status" {
			command.Flags().BoolVar(&apply, "apply", false, "显式逐项批准并确认本次计划（需要控制TTY）")
		}
		parent.AddCommand(command)
	}
	return parent
}

func nativeInteraction(output io.Writer, terminal nativeTerminal, origin, owner, profile string) nativeconfig.Interaction {
	return nativeconfig.Interaction{
		Preview: func(ctx context.Context, view configapply.SafePlan) error {
			if len(origin)+len(owner)+len(profile) > 2048 {
				return nativeconfig.ErrInvalid
			}
			_, err := fmt.Fprintf(output, "服务：%s\nOwner：%s\nProfile：%s\n计划：%s %s；资产 %s revision %d；目标 %s/%s；动作 %s；到期 %s\n", terminalText(origin), terminalText(owner), terminalText(profile), terminalText(view.OperationID), terminalText(view.Kind), terminalText(view.AssetID), view.AssetRevision, terminalText(view.Agent), terminalText(string(view.Target)), terminalText(view.Action), view.ExpiresAt.UTC().Format(time.RFC3339))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(output, "权限收紧：%t；可恢复的缺失窗口：%t；readiness仅凭据完整性。Preview可能更新加密记录，不代表目标已写入或agent已加载。\n", view.TightenPermissions, view.RecoverableAbsentWindow)
			if err != nil {
				return err
			}
			for _, operation := range view.Operations {
				if _, err := fmt.Fprintf(output, "字段 %s：%s\n", terminalText(string(operation.Slot)), terminalText(string(operation.Action))); err != nil {
					return err
				}
			}
			return nil
		},
		Approve: func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (nativeconfig.Approval, error) {
			if terminal == nil {
				return nativeconfig.Approval{}, nativeconfig.ErrDenied
			}
			deadline := min(time.Now().Add(time.Minute).UnixNano(), view.ExpiresAt.UnixNano())
			ctx, cancel := context.WithDeadline(ctx, time.Unix(0, deadline))
			defer cancel()
			for _, slot := range slots {
				if err := nativeConfirm(ctx, terminal, "approve "+string(slot)+" "+view.OperationID); err != nil {
					return nativeconfig.Approval{}, err
				}
			}
			if err := nativeConfirm(ctx, terminal, "confirm "+view.OperationID); err != nil {
				return nativeconfig.Approval{}, err
			}
			return nativeconfig.Approval{Confirmed: true, ApprovedSlots: slots}, nil
		},
	}
}

func nativeConfirm(ctx context.Context, terminal nativeTerminal, expected string) error {
	if len(expected) > 120 {
		return nativeconfig.ErrInvalid
	}
	prompt := "确认请输入 " + expected + "\n"
	count, err := terminal.WriteContext(ctx, []byte(prompt))
	if err != nil || count != len(prompt) {
		return errNativeIO
	}
	var line []byte
	for len(line) <= 128 {
		var character [1]byte
		count, err := terminal.ReadContext(ctx, character[:])
		if err != nil || count != 1 {
			return errNativeIO
		}
		if character[0] == '\n' {
			if strings.TrimSuffix(string(line), "\r") != expected {
				return nativeconfig.ErrDenied
			}
			return nil
		}
		line = append(line, character[0])
	}
	return nativeconfig.ErrDenied
}

func nativeShowStatus(output io.Writer, status configapply.SafeResult) error {
	_, err := fmt.Fprintf(output, "本机操作 %s：%s；需核实 %t；selection %d；policy %d。不保证agent已加载。\n", terminalText(status.OperationID), terminalText(status.Status), status.NeedsAttention, status.SelectionRevision, status.PolicyRevision)
	return err
}
