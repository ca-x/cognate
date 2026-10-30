package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/ca-x/cognate/internal/packattachments"
	"github.com/spf13/cobra"
)

var errPackConfigurations = errors.New("pack configurations refused; check bounded references file, exact revisions and current owner session")
var errPackConfigurationsUnknown = errors.New("pack configuration replacement outcome unknown; do not replay; use pack configurations list for read-only reconciliation")
var errPackConfigurationsDenied = errors.New("pack configurations owner session denied")
var errPackConfigurationsConflict = errors.New("Pack or configuration revision changed; list metadata and review a fresh replacement")
var errPackConfigurationsUnsupported = errors.New("Pack configuration target rejected as unsupported by server; metadata unchanged")

func packConfigurationsInvocation(args []string) bool {
	return len(args) >= 2 && args[0] == "pack" && args[1] == "configurations"
}

type packConfigurationsOutput struct {
	output *nativeOutput
	failed bool
}

func (output *packConfigurationsOutput) Write(data []byte) (int, error) {
	return output.WriteContext(output.output.ctx, data)
}

func (output *packConfigurationsOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	count, err := output.output.WriteContext(ctx, data)
	if count != len(data) && err == nil {
		err = io.ErrShortWrite
	}
	output.failed = output.failed || err != nil
	return count, err
}

func preparePackConfigurationsOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	if output, ok := writer.(*packConfigurationsOutput); ok {
		return &nativeOutput{ctx: ctx, writer: output, close: func() error { return nil }, remaining: 4 << 20}, nil
	}
	return prepareSelectionOutput(ctx, writer)
}

func executePackConfigurations(ctx context.Context, command *cobra.Command) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	prepared, err := prepareSelectionOutput(ctx, os.Stdout)
	if err != nil {
		return errPackConfigurations
	}
	output := &packConfigurationsOutput{output: prepared.(*nativeOutput)}
	command.SetOut(output)
	command.SetErr(output)
	err = command.ExecuteContext(ctx)
	closeErr := prepared.Close()
	if err != nil {
		return packConfigurationsError(err)
	}
	if output.failed || closeErr != nil || ctx.Err() != nil {
		return errPackConfigurationsUnknown
	}
	return nil
}

func packConfigurationsDiagnostic(ctx context.Context, err error) {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	output, prepareErr := prepareNativeOutput(ctx, os.Stderr)
	if prepareErr != nil {
		return
	}
	_, _ = io.WriteString(output, packConfigurationsError(err).Error()+"\n")
	_ = output.Close()
}

func packConfigurationsError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, packattachments.ErrUnknown) || errors.Is(err, errPackConfigurationsUnknown) {
		return errPackConfigurationsUnknown
	}
	if errors.Is(err, packattachments.ErrDenied) || errors.Is(err, errPackConfigurationsDenied) {
		return errPackConfigurationsDenied
	}
	if errors.Is(err, packattachments.ErrConflict) || errors.Is(err, errPackConfigurationsConflict) {
		return errPackConfigurationsConflict
	}
	if errors.Is(err, packattachments.ErrTargetUnsupported) || errors.Is(err, errPackConfigurationsUnsupported) {
		return errPackConfigurationsUnsupported
	}
	return errPackConfigurations
}

func newPackConfigurationCommand() *cobra.Command {
	group := &cobra.Command{Use: "configurations COMMAND", Short: "管理所有者Pack配置附件元数据（非分享令牌、非本机应用）", DisableFlagParsing: true, SilenceUsage: true, SilenceErrors: true}
	group.RunE = func(command *cobra.Command, args []string) error {
		if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			output, err := preparePackConfigurationsOutput(command.Context(), command.OutOrStdout())
			if err != nil {
				return errPackConfigurations
			}
			text := "pack configurations list|replace --server ORIGIN --pack ID --token-stdin\nreplace --references-file JSON --expected-pack-revision N --expected-configuration-revision N [--apply]\nOwner metadata only. All targets NativeUnavailable; nothing applied. Default review does NOT server-validate new references. --apply requires full controlling-TTY review and exact confirmation.\n"
			count, writeErr := io.WriteString(output, text)
			if closeErr := output.Close(); writeErr != nil || closeErr != nil || count != len(text) {
				return errPackConfigurations
			}
			return nil
		}
		action := args[0]
		if action != "list" && action != "replace" {
			return errPackConfigurations
		}
		var origin, pack, file string
		var packRevision, configurationRevision int64
		var tokenStdin, apply bool
		inner := &cobra.Command{Use: action, SilenceUsage: true, SilenceErrors: true, Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errPackConfigurations
			}
			return nil
		}}
		inner.SetOut(command.OutOrStdout())
		inner.SetErr(command.ErrOrStderr())
		inner.SetIn(command.InOrStdin())
		inner.SetFlagErrorFunc(func(*cobra.Command, error) error { return errPackConfigurations })
		inner.Flags().StringVar(&origin, "server", "", "HTTPS origin; explicit loopback HTTP permitted")
		inner.Flags().StringVar(&pack, "pack", "", "owner Pack ID")
		inner.Flags().BoolVar(&tokenStdin, "token-stdin", false, "bounded non-TTY session stdin, not share token or approval")
		inner.Flags().StringVar(&file, "references-file", "", "owned regular nofollow ordered JSON array; [] clears")
		inner.Flags().Int64Var(&packRevision, "expected-pack-revision", 0, "exact Pack revision")
		inner.Flags().Int64Var(&configurationRevision, "expected-configuration-revision", 0, "exact attachment revision")
		inner.Flags().BoolVar(&apply, "apply", false, "replace only after complete real controlling-TTY review")
		inner.RunE = func(inner *cobra.Command, _ []string) (result error) {
			if !tokenStdin || origin == "" || pack == "" {
				return errPackConfigurations
			}
			if action == "list" && (inner.Flags().Changed("references-file") || inner.Flags().Changed("expected-pack-revision") || inner.Flags().Changed("expected-configuration-revision") || inner.Flags().Changed("apply")) {
				return errPackConfigurations
			}
			if action == "replace" && (file == "" || packRevision < 1 || configurationRevision < 1) {
				return errPackConfigurations
			}
			ctx, cancel := context.WithTimeout(inner.Context(), 120*time.Second)
			defer cancel()
			output, err := preparePackConfigurationsOutput(ctx, inner.OutOrStdout())
			if err != nil {
				return errPackConfigurations
			}
			applied := false
			defer func() {
				if output.Close() != nil && result == nil {
					if applied {
						result = errPackConfigurationsUnknown
					} else {
						result = errPackConfigurations
					}
				}
			}()
			session, err := readNativeSession(ctx, inner.InOrStdin())
			if err != nil {
				return errPackConfigurations
			}
			client, err := packattachments.New(origin, session, nil)
			session = ""
			if err != nil {
				return errPackConfigurations
			}
			defer client.Close()
			if action == "list" {
				snapshot, err := client.Get(ctx, pack)
				if err != nil {
					return packConfigurationsError(err)
				}
				return packConfigurationsError(packattachments.WriteSnapshot(ctx, output, snapshot))
			}
			refs, err := packattachments.ReadReferences(ctx, file)
			if err != nil {
				return errPackConfigurations
			}
			operation, err := client.Prepare(ctx, pack, packRevision, configurationRevision, refs)
			if err != nil {
				return packConfigurationsError(err)
			}
			defer operation.Close()
			if err := operation.WriteSummary(ctx, output); err != nil {
				return errPackConfigurations
			}
			if !apply {
				return nil
			}
			reviewCtx, reviewCancel := context.WithTimeout(ctx, time.Minute)
			defer reviewCancel()
			terminal, err := openNativeConfirmationTTY(reviewCtx)
			if err != nil {
				return errPackConfigurations
			}
			closed := false
			defer func() {
				if !closed {
					_ = terminal.Close()
				}
			}()
			review := &nativeOutput{ctx: reviewCtx, writer: selectionTerminalWriter{ctx: reviewCtx, terminal: terminal}, close: func() error { return nil }, remaining: 64 << 10}
			if err := packattachments.WriteReviewIdentity(reviewCtx, review, origin); err != nil {
				return errPackConfigurations
			}
			if err := operation.WriteSummary(reviewCtx, review); err != nil {
				return errPackConfigurations
			}
			phrase := operation.Confirmation()
			if phrase == "" || nativeConfirm(reviewCtx, terminal, phrase) != nil {
				return errPackConfigurations
			}
			closed = true
			if terminal.Close() != nil {
				return errPackConfigurations
			}
			snapshot, err := operation.Submit(ctx)
			if err != nil {
				return packConfigurationsError(err)
			}
			applied = true
			if packattachments.WriteSnapshot(ctx, output, snapshot) != nil {
				return errPackConfigurationsUnknown
			}
			acknowledged := "Attachment metadata replacement acknowledged; no native configuration applied.\n"
			if count, err := io.WriteString(output, acknowledged); err != nil || count != len(acknowledged) {
				return errPackConfigurationsUnknown
			}
			return nil
		}
		inner.SetArgs(args[1:])
		return packConfigurationsError(inner.ExecuteContext(command.Context()))
	}
	return group
}
