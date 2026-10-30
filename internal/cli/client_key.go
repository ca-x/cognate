package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/spf13/cobra"
)

func loadClientKey(ctx context.Context, home string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, clientkey.ErrUnsupported
	}
	return clientkey.LoadExisting(ctx, home)
}

func admitClientHome(ctx context.Context) (string, error) {
	home, err := localHome()
	if err != nil {
		return "", clientkey.ErrUnavailable
	}
	key, err := loadClientKey(ctx, home)
	clear(key)
	if err != nil {
		return "", err
	}
	return home, nil
}

func newClientKeyCommand() *cobra.Command {
	parent := &cobra.Command{Use: "client-key", Short: "显式初始化本机客户端密钥", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error { return command.Help() }}
	var timeout time.Duration
	command := &cobra.Command{Use: "init", Short: "初始化或核实既有本机客户端密钥", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (returnedError error) {
			if timeout <= 0 || timeout > 2*time.Minute {
				return clientkey.ErrUnavailable
			}
			if runtime.GOOS != "linux" {
				return clientkey.ErrUnsupported
			}
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			output, err := prepareClientKeyOutput(ctx, command.OutOrStdout())
			if err != nil {
				return err
			}
			defer func() { returnedError = errors.Join(returnedError, output.Close()) }()
			home, err := localHome()
			if err != nil {
				return clientkey.ErrUnavailable
			}
			if err := clientkey.Initialize(ctx, home); err != nil {
				return err
			}
			_, err = fmt.Fprintln(output, "本机客户端密钥已就绪（未初始化服务端会话）")
			return err
		}}
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "初始化期限（大于0且不超过2m）")
	parent.AddCommand(command)
	return parent
}

func clientKeyInvocation(arguments []string) bool {
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "-") {
			return argument == "client-key"
		}
	}
	return false
}

func clientKeyFailure(err error) bool {
	return errors.Is(err, clientkey.ErrInitializationRequired) || errors.Is(err, clientkey.ErrRecoveryRequired) || errors.Is(err, clientkey.ErrUnavailable) || errors.Is(err, clientkey.ErrUnsupported)
}

func clientKeyErrorMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "操作未确认完成；写入可能已经发生，请核实后显式重试。"
	case errors.Is(err, clientkey.ErrInitializationRequired):
		return "请先显式初始化本机客户端密钥。"
	case errors.Is(err, clientkey.ErrRecoveryRequired):
		return "请恢复既有本机客户端密钥，勿重新生成。"
	case errors.Is(err, clientkey.ErrUnsupported):
		return "本机客户端密钥操作当前仅支持 Linux。"
	default:
		return "本机客户端密钥不可用；请核实参数、权限与本地状态。"
	}
}

type clientKeyOutput struct {
	io.WriteCloser
	failed bool
}

func (output *clientKeyOutput) Write(data []byte) (int, error) {
	return output.WriteContext(output.WriteCloser.(*nativeOutput).ctx, data)
}

func (output *clientKeyOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	written, err := output.WriteCloser.(*nativeOutput).WriteContext(ctx, data)
	if err != nil {
		output.failed = true
	}
	return written, err
}

func prepareClientKeyOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	if existing, ok := writer.(*clientKeyOutput); ok {
		return &clientKeyOutput{WriteCloser: &nativeOutput{ctx: ctx, writer: existing, close: func() error { return nil }, remaining: 4 << 10}}, nil
	}
	output, err := prepareNativeOutput(ctx, writer)
	if err != nil {
		return nil, err
	}
	output.(*nativeOutput).remaining = 4 << 10
	return &clientKeyOutput{WriteCloser: output}, nil
}

func executeClientKey(ctx context.Context, command *cobra.Command) int {
	output, err := prepareClientKeyOutput(ctx, command.OutOrStdout())
	if err != nil {
		clientKeyExitDiagnostic(err, 4<<10)
		return 1
	}
	command.SetOut(output)
	command.SetErr(output)
	err = command.ExecuteContext(ctx)
	closeErr := output.Close()
	if err == nil && closeErr == nil {
		return 0
	}
	tracked := output.(*clientKeyOutput)
	if !tracked.failed {
		clientKeyExitDiagnostic(errors.Join(err, closeErr), tracked.WriteCloser.(*nativeOutput).remaining)
	}
	return 1
}

func clientKeyExitDiagnostic(err error, remaining int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, prepareErr := prepareNativeOutput(ctx, os.Stderr)
	if prepareErr != nil {
		return
	}
	output.(*nativeOutput).remaining = remaining
	_, _ = fmt.Fprintln(output, clientKeyErrorMessage(err))
	_ = output.Close()
}
