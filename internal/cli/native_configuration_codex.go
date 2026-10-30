package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/spf13/cobra"
)

type codexCLIOptions struct {
	action, server, owner, asset, profile, operation, executable, directory string
	revision                                                                int64
	apply, tokenStdin                                                       bool
	timeout                                                                 time.Duration
}
type codexContextWriter interface {
	WriteContext(context.Context, []byte) (int, error)
}

func newCodexLocalCommand(action string) *cobra.Command {
	options := codexCLIOptions{action: action}
	var target string
	command := &cobra.Command{Use: action, Short: "显式管理Codex本机操作；不重放消费", Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true, RunE: func(command *cobra.Command, _ []string) error {
		if target != "codex.config" {
			return nativeconfig.ErrInvalid
		}
		return runCodexConfiguration(command, options)
	}}
	command.Flags().StringVar(&target, "target", "", "必须显式 codex.config")
	command.Flags().StringVar(&options.operation, "operation", "", "既有本机操作ID；重新预览确认")
	command.Flags().BoolVar(&options.apply, "apply", false, "控制TTY完整review及本次明确确认")
	command.Flags().DurationVar(&options.timeout, "timeout", 2*time.Minute, "外层预算>0且<=5m；Codex编排另有2m硬上限")
	return command
}

func runCodexConfiguration(command *cobra.Command, options codexCLIOptions) (returnedError error) {
	if options.timeout <= 0 || options.timeout > 5*time.Minute {
		return nativeconfig.ErrInvalid
	}
	if options.action == "apply" {
		if !options.tokenStdin || options.server == "" || options.owner == "" || options.asset == "" || options.profile == "" || options.revision <= 0 || !filepath.IsAbs(options.executable) || filepath.Clean(options.executable) != options.executable || !filepath.IsAbs(options.directory) || filepath.Clean(options.directory) != options.directory {
			return nativeconfig.ErrInvalid
		}
		if _, err := configapply.QualifiedCodexNativeProfile(); err != nil {
			return err
		}
	} else if options.operation == "" {
		return nativeconfig.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(command.Context(), options.timeout)
	defer cancel()
	if err := nativeConfigurationPlatform(ctx); err != nil {
		return err
	}
	output, err := prepareNativeOutput(ctx, command.OutOrStdout())
	if err != nil {
		return errNativeIO
	}
	writer, ok := output.(codexContextWriter)
	if !ok {
		if output.Close() != nil {
			return errNativeIO
		}
		return errNativeIO
	}
	possiblyChanged := false
	defer func() {
		if output.Close() != nil {
			if possiblyChanged {
				returnedError = errors.Join(returnedError, configapply.ErrCodexUnknown)
			} else {
				returnedError = errors.Join(returnedError, errNativeIO)
			}
		}
	}()
	var terminal nativeTerminal
	if options.apply {
		terminal, err = openNativeConfirmationTTY(ctx)
		if err != nil {
			return errNativeIO
		}
	}
	closeTTY := sync.OnceValue(func() error {
		if terminal != nil && terminal.Close() != nil {
			return errNativeIO
		}
		return nil
	})
	defer func() { returnedError = errors.Join(returnedError, closeTTY()) }()
	home, err := localHome()
	if err != nil {
		return nativeconfig.ErrInvalid
	}
	key, err := loadClientKey(ctx, home)
	if err != nil {
		return err
	}
	defer clear(key)
	stateRoot := filepath.Join(home, ".local", "share", "skillhub")
	if prepareStateDirectory(stateRoot) != nil {
		return nativeconfig.ErrInvalid
	}
	var native *configclient.NativeClient
	var session string
	if options.action == "apply" {
		session, err = readNativeSession(ctx, command.InOrStdin())
		if err != nil {
			return err
		}
		native, err = configclient.NewNativeClient(options.server, nil)
		if err != nil {
			return configclient.ErrNativeInvalid
		}
		defer native.Close()
	}
	client, err := nativeconfig.New(home, stateRoot, options.server, key, native)
	clear(key)
	if err != nil {
		return nativeconfig.ErrInvalid
	}
	defer func() {
		if client.Close() != nil {
			returnedError = errors.Join(returnedError, configapply.ErrCodexUnknown)
		}
	}()
	if options.action == "status" {
		status, err := client.StatusCodex(ctx, options.operation)
		if err != nil {
			return err
		}
		return codexShowStatus(ctx, writer, status)
	}
	interaction := nativeconfig.CodexInteraction{
		Preview: func(ctx context.Context, view nativeconfig.CodexPreview) error {
			return codexWriteReview(ctx, writer, options, view)
		},
		Approve: func(ctx context.Context, view nativeconfig.CodexPreview, slots []agentconfig.CodexSecretSlot) (nativeconfig.CodexApproval, error) {
			if terminal == nil {
				return nativeconfig.CodexApproval{}, nativeconfig.ErrDenied
			}
			deadline := min(time.Now().Add(time.Minute).UnixNano(), view.ExpiresAt.UnixNano())
			reviewCtx, reviewCancel := context.WithDeadline(ctx, time.Unix(0, deadline))
			defer reviewCancel()
			if err := codexWriteReview(reviewCtx, terminal, options, view); err != nil {
				return nativeconfig.CodexApproval{}, err
			}
			if options.action == "apply" && !slices.Equal(slots, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) {
				return nativeconfig.CodexApproval{}, nativeconfig.ErrDenied
			}
			for _, slot := range slots {
				if slot != agentconfig.CodexProviderKeySecret {
					return nativeconfig.CodexApproval{}, nativeconfig.ErrDenied
				}
				if err := nativeConfirm(reviewCtx, terminal, "approve "+string(slot)+" "+view.OperationID); err != nil {
					return nativeconfig.CodexApproval{}, err
				}
			}
			if err := nativeConfirm(reviewCtx, terminal, "confirm "+view.OperationID); err != nil {
				return nativeconfig.CodexApproval{}, err
			}
			if err := closeTTY(); err != nil {
				return nativeconfig.CodexApproval{}, err
			}
			return nativeconfig.CodexApproval{Confirmed: true, ApprovedSlots: slices.Clone(slots)}, nil
		},
	}
	var result nativeconfig.CodexResult
	switch options.action {
	case "apply":
		result, err = client.ApplyCodex(ctx, nativeconfig.CodexApplyRequest{OwnerID: options.owner, AssetID: options.asset, AssetRevision: options.revision, ProfileID: options.profile, Apply: options.apply, Executable: options.executable, Directory: options.directory}, session, interaction)
	case "recover":
		result, err = client.RecoverCodex(ctx, options.operation, options.apply, interaction)
	case "restore":
		result, err = client.RestoreCodex(ctx, options.operation, options.apply, interaction)
	case "abandon":
		result, err = client.AbandonCodex(ctx, options.operation, options.apply, interaction)
	case "retire-unpublished":
		result, err = client.RetireCodexUnpublished(ctx, options.operation, options.apply, interaction)
	default:
		return nativeconfig.ErrInvalid
	}
	possiblyChanged = result.RemoteUnknown || result.RemoteState == configclient.NativeConsumed || result.Local.OperationID != ""
	if result.RemoteUnknown || result.RemoteState != "" {
		label, valid := codexRemoteLabel(result.RemoteState)
		if !valid {
			return errors.Join(err, configapply.ErrCodexUnknown)
		}
		text := fmt.Sprintf("远端状态 %s；Unknown %t；不重放消费。\n", label, result.RemoteUnknown)
		if codexWrite(ctx, writer, text) != nil {
			return errors.Join(err, configapply.ErrCodexUnknown)
		}
	}
	if result.Local.OperationID != "" {
		err = errors.Join(err, codexShowStatus(ctx, writer, result.Local))
	}
	return err
}

func codexWrite(ctx context.Context, writer codexContextWriter, text string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(text) > 64<<10 {
		return errNativeIO
	}
	count, err := writer.WriteContext(ctx, []byte(text))
	if err != nil || count != len(text) {
		return errNativeIO
	}
	return ctx.Err()
}

func codexWriteReview(ctx context.Context, writer codexContextWriter, options codexCLIOptions, view nativeconfig.CodexPreview) error {
	if len(options.server)+len(options.owner)+len(options.operation)+len(view.OperationID)+len(view.AssetID)+len(view.C0ProfileID)+len(view.NativeProfileID) > 4096 || len(view.Changes) > 128 || len(view.RequiredSlots) > 1 || !view.AuthKept {
		return nativeconfig.ErrInvalid
	}
	if options.action == "apply" && view.AssetRevision == 0 {
		return nativeconfig.ErrInvalid
	}
	var text bytes.Buffer
	fmt.Fprintf(&text, "CODEX REVIEW BEGIN action=%s operation=%s\n", terminalText(options.action), terminalText(view.OperationID))
	if options.action == "apply" {
		fmt.Fprintf(&text, "服务 %s；Owner %s\n", terminalText(options.server), terminalText(options.owner))
	} else {
		fmt.Fprintf(&text, "原操作 source-operation=%s；本次计划 plan-operation=%s\n", terminalText(options.operation), terminalText(view.OperationID))
		fmt.Fprintln(&text, "范围：既有本机key认证的原operation绑定；此安全视图未提供owner/origin字符串。")
	}
	revision := "未提供（使用经认证原operation绑定，不推断当前资产版本）"
	if view.AssetRevision != 0 {
		revision = fmt.Sprint(view.AssetRevision)
	}
	fmt.Fprintf(&text, "资产 %s revision %s；C0 %s；NativeProfile %s\nselection %d；confirmation %d；到期 %s\n", terminalText(view.AssetID), revision, terminalText(view.C0ProfileID), terminalText(view.NativeProfileID), view.SelectionRevision, view.ConfirmationRevision, view.ExpiresAt.UTC().Format(time.RFC3339Nano))
	roles := []string{"", "config", "auth", "catalog"}
	fields := []string{"role-bundle", "model-provider", "model", "reasoning", "catalog-ref", "provider-name", "endpoint", "wire-api", "requires-auth", "bearer", "catalog-models", "display-name", "context-window", "parallel", "modalities", "instructions", "reasoning-levels", "default-reasoning"}
	for _, change := range view.Changes {
		if change.Role < codexconfig.ConfigRole || change.Role > codexconfig.CatalogRole || int(change.Field) >= len(fields) || change.Field == 0 && options.action == "apply" || change.Action != codexconfig.Keep && change.Action != codexconfig.Write {
			return nativeconfig.ErrInvalid
		}
		action := "Keep"
		if change.Action == codexconfig.Write {
			action = "Write"
		}
		fmt.Fprintf(&text, "角色 %s；字段 %s；动作 %s；count %d\n", roles[change.Role], fields[change.Field], action, change.Count)
	}
	for _, slot := range view.RequiredSlots {
		if slot != agentconfig.CodexProviderKeySecret {
			return nativeconfig.ErrInvalid
		}
		fmt.Fprintf(&text, "需批准槽 %s\n", string(slot))
	}
	fmt.Fprintf(&text, "auth Keep；权限收紧 %t；可恢复窗口 %t。多文件发布可能partial；失败/取消不表示回滚。\n", view.TightenPermissions, view.RecoverableWindow)
	if options.action == "abandon" {
		fmt.Fprintln(&text, "Abandon仅本机BeforeConsume；可能存在未用远端grant，不声称远端已retired。")
	}
	if options.action == "retire-unpublished" {
		fmt.Fprintln(&text, "远端可能已消费；材料无法取回；保留Unknown/reservation/quota；不是还原或删除。")
	}
	fmt.Fprintln(&text, "Preview可更新加密记录；未确认预览不保证可重开Status。Completed仅durable文件，不是agent已加载。")
	fmt.Fprintf(&text, "CODEX REVIEW END action=%s operation=%s\n", terminalText(options.action), terminalText(view.OperationID))
	return codexWrite(ctx, writer, text.String())
}

func codexRemoteLabel(state configclient.NativeGrantStatus) (string, bool) {
	switch state {
	case "":
		return "未观测", true
	case configclient.NativeNotIssued:
		return "not-issued", true
	case configclient.NativeIssued:
		return "issued", true
	case configclient.NativeConsumed:
		return "consumed", true
	case configclient.NativeExpired:
		return "expired", true
	case configclient.NativeRetired:
		return "retired", true
	default:
		return "", false
	}
}
func codexShowStatus(ctx context.Context, writer codexContextWriter, status nativeconfig.CodexLocalResult) error {
	labels := []string{"", "Previewed", "AuthorizeAttempted", "Authorized", "ConsumeAttempted", "CandidatesDurable", "PublishingCatalog", "PublishingConfig", "Completed", "ReconcileRequired", "NeedsAttention", "RemoteOutcomeUnknown", "RestorePrepared", "Restoring", "Restored", "AbandonedBeforeWrite", "RetiredUnpublished"}
	if status.State < configapply.CodexPreviewed || int(status.State) >= len(labels) || len(status.OperationID) > 128 {
		return configapply.ErrCodexUnknown
	}
	text := fmt.Sprintf("本机操作 %s：%s；selection %d；confirmation %d；需核实 %t；远端Unknown %t。不保证agent已加载。\n", terminalText(status.OperationID), labels[status.State], status.SelectionRevision, status.ConfirmationRevision, status.NeedsAttention, status.RemoteOutcomeUnknown)
	if err := codexWrite(ctx, writer, text); err != nil {
		return err
	}
	if status.RemoteOutcomeUnknown || status.State == configapply.CodexRemoteOutcomeUnknown || status.State == configapply.CodexRetiredUnpublished {
		return configapply.ErrCodexUnknown
	}
	if status.NeedsAttention || status.State == configapply.CodexNeedsAttention {
		return configapply.ErrCodexAttention
	}
	switch status.State {
	case configapply.CodexPreviewed, configapply.CodexCompleted, configapply.CodexRestored, configapply.CodexAbandonedBeforeWrite:
		return nil
	default:
		return configapply.ErrCodexUnknown
	}
}

func nativeCodexErrorMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, configapply.ErrCodexUnknown):
		return "Codex本机或远端结果仍未知；查看明确target的status，不重放消费，不保证回滚。", true
	case errors.Is(err, configapply.ErrCodexUnsupported):
		return "Codex本机资格或受支持来源尚未满足；本次未完成，不表示生产profile可用。", true
	case errors.Is(err, configapply.ErrCodexExpired):
		return "Codex原批准期限已过；必须重新预览确认，不能续期或重放。", true
	case errors.Is(err, configapply.ErrCodexConflict):
		return "Codex源/selection/文件已变化；拒绝覆盖，请核实状态。", true
	case errors.Is(err, configapply.ErrCodexAttention), errors.Is(err, configapply.ErrCodexCapacity), errors.Is(err, configapply.ErrCodexInvalid), errors.Is(err, configapply.ErrCodexClosed):
		return "Codex操作未完成；核实本机状态、权限和配额，不强制覆盖。", true
	default:
		return "", false
	}
}
