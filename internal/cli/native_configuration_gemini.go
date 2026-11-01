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
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/geminiconfig"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/spf13/cobra"
)

type geminiCLIOptions struct {
	action, server, owner, asset, profile, operation, node, packageRoot, directory string
	revision                                                                       int64
	apply, tokenStdin, readRemote                                                  bool
	timeout                                                                        time.Duration
}

var geminiCLIFields = [...]geminiconfig.Field{geminiconfig.APIKeyField, geminiconfig.EndpointField, geminiconfig.TransportField, geminiconfig.EnvModelField, geminiconfig.SettingsModelField, geminiconfig.AuthModeField}

func geminiWriteReview(ctx context.Context, writer codexContextWriter, action string, view configapply.SafeGeminiPlan) error {
	if len(view.Changes) != 6 || len(view.RequiredSlots) > 1 || len(view.PlanID) != 32 || len(view.OperationID) != 32 || len(view.Origin)+len(view.OwnerID)+len(view.ProfileID)+len(view.AssetID)+len(view.NativeProfileID) > 4096 {
		return nativeconfig.ErrInvalid
	}
	var text bytes.Buffer
	fmt.Fprintf(&text, "GEMINI REVIEW BEGIN action=%s operation=%s plan=%s\n", terminalText(action), terminalText(view.OperationID), terminalText(view.PlanID))
	fmt.Fprintf(&text, "服务 %s；Owner %s；Profile %s；NativeProfile %s\n资产 %s revision %d；selection %d；confirmation %d；到期 %s\n", terminalText(view.Origin), terminalText(view.OwnerID), terminalText(view.ProfileID), terminalText(view.NativeProfileID), terminalText(view.AssetID), view.AssetRevision, view.SelectionRevision, view.ConfirmationRevision, view.ExpiresAt.UTC().Format(time.RFC3339Nano))
	if view.SourceOperation != "" {
		fmt.Fprintf(&text, "原操作 source-operation=%s；新审阅 plan=%s\n", terminalText(view.SourceOperation), terminalText(view.PlanID))
	}
	owners := []string{"", "Absent", "Managed", "Foreign", "ExplicitTakeover"}
	actions := []string{"", "Keep", "Write", "Remove"}
	for index, change := range view.Changes {
		role := geminiconfig.EnvRole
		name := ".env"
		if index >= 4 {
			role = geminiconfig.SettingsRole
			name = "settings.json"
		}
		if change.Field != geminiCLIFields[index] || change.Role != role || change.Ownership < 1 || int(change.Ownership) >= len(owners) || change.Action < 1 || int(change.Action) >= len(actions) {
			return nativeconfig.ErrInvalid
		}
		fmt.Fprintf(&text, "角色 %s；字段 %s；ownership %s；动作 %s；明确清理 %t\n", name, string(change.Field), owners[change.Ownership], actions[change.Action], change.Cleanup)
	}
	for _, slot := range view.RequiredSlots {
		if slot != agentconfig.GeminiAPIKeySecret {
			return nativeconfig.ErrInvalid
		}
		fmt.Fprintf(&text, "需独立批准槽 %s\n", slot)
	}
	if view.LocalMaterial {
		source := "durable-candidate"
		if action == "restore" {
			source = "local-backup"
		}
		fmt.Fprintf(&text, "本地材料来源 %s；不是新server消费。\n", source)
	}
	fmt.Fprintln(&text, "双rename非原子；请关闭正在运行或自动热加载的Gemini。Cognate锁不阻止外部读取/编辑；失败不保证回滚。")
	fmt.Fprintln(&text, "native-login仅selector，不迁移OAuth，不保证已登录、agent已加载或真实账户请求成功。排序/选择不代表agent已应用。")
	if action == "abandon" || action == "retire" {
		fmt.Fprintln(&text, "仅本地未发布退役；保留真实远端Consumed/Unknown和配额，不表示远端撤销。")
	}
	fmt.Fprintf(&text, "GEMINI REVIEW END action=%s operation=%s plan=%s final=%t\n", terminalText(action), terminalText(view.OperationID), terminalText(view.PlanID), view.FinalReview)
	return codexWrite(ctx, writer, text.String())
}

func geminiCLIInteraction(writer codexContextWriter, terminal nativeTerminal, action string, closeTTY func() error) nativeconfig.GeminiInteraction {
	return nativeconfig.GeminiInteraction{
		Preview: func(ctx context.Context, view configapply.SafeGeminiPlan) error {
			return geminiWriteReview(ctx, writer, action, view)
		},
		Approve: func(ctx context.Context, view configapply.SafeGeminiPlan) (nativeconfig.GeminiApproval, error) {
			if terminal == nil {
				return nativeconfig.GeminiApproval{}, nativeconfig.ErrDenied
			}
			review, cancel := context.WithDeadline(ctx, minTimeCLI(time.Now().Add(time.Minute), view.ExpiresAt))
			defer cancel()
			if err := geminiWriteReview(review, terminal, action, view); err != nil {
				return nativeconfig.GeminiApproval{}, err
			}
			approval := nativeconfig.GeminiApproval{PlanID: view.PlanID, Confirmed: true}
			if !view.FinalReview {
				for _, change := range view.Changes {
					field := configapply.GeminiFieldApproval{Field: change.Field, Takeover: change.Ownership == geminiconfig.Foreign, Clear: change.Cleanup}
					if field.Takeover {
						if err := nativeConfirm(review, terminal, "takeover "+string(field.Field)+" "+view.PlanID); err != nil {
							return nativeconfig.GeminiApproval{}, err
						}
					}
					if field.Clear {
						if err := nativeConfirm(review, terminal, "clear "+string(field.Field)+" "+view.PlanID); err != nil {
							return nativeconfig.GeminiApproval{}, err
						}
					}
					approval.Fields = append(approval.Fields, field)
				}
				if err := nativeConfirm(review, terminal, "review-fields "+view.PlanID); err != nil {
					return nativeconfig.GeminiApproval{}, err
				}
				return approval, nil
			}
			if view.SourceOperation == "" {
				for _, change := range view.Changes {
					approval.Fields = append(approval.Fields, configapply.GeminiFieldApproval{Field: change.Field, Takeover: change.Ownership == geminiconfig.Foreign, Clear: change.Cleanup})
				}
			}
			for _, slot := range view.RequiredSlots {
				if err := nativeConfirm(review, terminal, "approve "+string(slot)+" "+view.PlanID); err != nil {
					return nativeconfig.GeminiApproval{}, err
				}
			}
			if err := nativeConfirm(review, terminal, "confirm "+view.PlanID); err != nil {
				return nativeconfig.GeminiApproval{}, err
			}
			if err := closeTTY(); err != nil {
				return nativeconfig.GeminiApproval{}, err
			}
			approval.ApprovedSlots = slices.Clone(view.RequiredSlots)
			return approval, nil
		},
	}
}
func minTimeCLI(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

func geminiShowStatus(ctx context.Context, writer codexContextWriter, result nativeconfig.GeminiResult) error {
	labels := []string{"", "Previewed", "AuthorizeAttempted", "AuthorizedNoSecret", "AuthorizedGrant", "ConsumeAttempted", "CandidatesDurable", "PublishingEnv", "PublishingSettings", "Completed", "RemoteOutcomeUnknown", "ReconcileRequired", "NeedsAttention", "RestorePrepared", "Restoring", "Restored", "AbandonedBeforeWrite", "RetiredUnpublished"}
	local := result.Local
	if local.State < 1 || int(local.State) >= len(labels) || len(local.OperationID) > 32 {
		return configapply.ErrGeminiUnknown
	}
	remote, valid := codexRemoteLabel(result.RemoteState)
	if !valid {
		return configapply.ErrGeminiUnknown
	}
	if err := codexWrite(ctx, writer, fmt.Sprintf("本机操作 %s：%s；Consumed %t；Applied %t；远端 %s；Unknown %t。不重放消费，不保证agent已加载。\n", terminalText(local.OperationID), labels[local.State], local.Consumed, local.Applied, remote, local.RemoteOutcomeUnknown || result.RemoteUnknown)); err != nil {
		return err
	}
	if local.State == configapply.GeminiNeedsAttention {
		return configapply.ErrGeminiAttention
	}
	if local.RemoteOutcomeUnknown || result.RemoteUnknown {
		return configapply.ErrGeminiUnknown
	}
	switch local.State {
	case configapply.GeminiPreviewed, configapply.GeminiCompleted, configapply.GeminiRestored, configapply.GeminiAbandonedBeforeWrite:
		return nil
	default:
		return configapply.ErrGeminiUnknown
	}
}

func runGeminiConfiguration(command *cobra.Command, options geminiCLIOptions) (returnedError error) {
	if options.timeout <= 0 || options.timeout > 5*time.Minute || options.server == "" || options.owner == "" || options.profile == "" || options.action == "status" && options.apply || options.action != "status" && options.readRemote {
		return nativeconfig.ErrInvalid
	}
	if _, err := configapply.QualifiedGeminiNativeProfile(); err != nil {
		return err
	}
	if options.action == "apply" {
		if !options.tokenStdin || options.asset == "" || options.revision <= 0 {
			return nativeconfig.ErrInvalid
		}
	} else if options.operation == "" || options.tokenStdin {
		return nativeconfig.ErrInvalid
	}
	if options.action != "status" {
		for _, path := range []string{options.node, options.packageRoot, options.directory} {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return nativeconfig.ErrInvalid
			}
		}
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
	defer func() {
		if output.Close() != nil {
			returnedError = errors.Join(returnedError, configapply.ErrGeminiUnknown)
		}
	}()
	writer, ok := output.(codexContextWriter)
	if !ok {
		return errNativeIO
	}
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
	var remote *configclient.NativeClient
	var session string
	if options.action == "apply" {
		session, err = readNativeSession(ctx, command.InOrStdin())
		if err != nil {
			return err
		}
	}
	if options.action == "apply" || options.readRemote {
		remote, err = configclient.NewNativeClient(options.server, nil)
		if err != nil {
			return configclient.ErrNativeInvalid
		}
		defer remote.Close()
	}
	client, err := nativeconfig.New(home, stateRoot, options.server, key, remote)
	clear(key)
	if err != nil {
		return nativeconfig.ErrInvalid
	}
	defer func() { returnedError = errors.Join(returnedError, client.Close()) }()
	launch := configapply.GeminiLaunch{NodeExecutable: options.node, PackageRoot: options.packageRoot, Directory: options.directory}
	ui := geminiCLIInteraction(writer, terminal, options.action, closeTTY)
	var result nativeconfig.GeminiResult
	if options.action == "apply" {
		result, err = client.ApplyGemini(ctx, nativeconfig.GeminiApplyRequest{OwnerID: options.owner, AssetID: options.asset, AssetRevision: options.revision, ProfileID: options.profile, Apply: options.apply, Launch: launch}, session, ui)
	} else {
		result, err = client.LocalGemini(ctx, nativeconfig.GeminiLocalRequest{OperationID: options.operation, OwnerID: options.owner, ProfileID: options.profile, Action: options.action, Launch: launch, Apply: options.apply, ReadRemote: options.readRemote}, ui)
	}
	if result.Local.OperationID != "" {
		err = errors.Join(err, geminiShowStatus(ctx, writer, result))
	}
	return err
}

func newGeminiAwareLocalCommand(action string) *cobra.Command {
	command := newCodexLocalCommand(action)
	original := command.RunE
	var options geminiCLIOptions
	options.action = action
	if action == "retire-unpublished" {
		options.action = "retire"
	}
	geminiExtraFlags(command, &options)
	command.RunE = func(command *cobra.Command, args []string) error {
		target, _ := command.Flags().GetString("target")
		if target != "gemini.settings" {
			if changedGeminiFlags(command) || command.Flags().Changed("server") || command.Flags().Changed("owner") || command.Flags().Changed("profile") {
				return nativeconfig.ErrInvalid
			}
			return original(command, args)
		}
		options.operation, _ = command.Flags().GetString("operation")
		options.apply, _ = command.Flags().GetBool("apply")
		options.timeout, _ = command.Flags().GetDuration("timeout")
		return runGeminiConfiguration(command, options)
	}
	return command
}
func geminiExtraFlags(command *cobra.Command, options *geminiCLIOptions) {
	for _, entry := range []struct {
		name, help string
		value      *string
	}{{"server", "Gemini绑定的HTTPS origin", &options.server}, {"owner", "Gemini明确owner", &options.owner}, {"profile", "Gemini本机profile", &options.profile}} {
		if command.Flags().Lookup(entry.name) == nil {
			command.Flags().StringVar(entry.value, entry.name, "", entry.help)
		}
	}
	command.Flags().StringVar(&options.node, "gemini-node", "", "固定Node绝对路径，不搜索PATH")
	command.Flags().StringVar(&options.packageRoot, "gemini-package", "", "完整固定Gemini npm package绝对路径")
	command.Flags().StringVar(&options.directory, "gemini-cwd", "", "既有trusted工作目录绝对路径")
	if options.action == "status" {
		command.Flags().BoolVar(&options.readRemote, "check-remote", false, "显式只读远端核实；不重放消费")
	}
}
func changedGeminiFlags(command *cobra.Command) bool {
	for _, name := range []string{"gemini-node", "gemini-package", "gemini-cwd", "check-remote"} {
		if command.Flags().Changed(name) {
			return true
		}
	}
	return false
}
func nativeGeminiErrorMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, configapply.ErrGeminiUnsupported):
		return "Gemini固定本机资格或有效来源不支持；未宣称可用。", true
	case errors.Is(err, configapply.ErrGeminiUnknown):
		return "Gemini结果未知；显式核实status，不重放消费；原failure/半写不会自动回滚。", true
	case errors.Is(err, configapply.ErrGeminiConflict):
		return "Gemini来源、文件或selection已变化；拒绝覆盖，须新审阅。", true
	case errors.Is(err, configapply.ErrGeminiExpired):
		return "Gemini本次审阅已过期；不能续用旧确认。", true
	case errors.Is(err, configapply.ErrGeminiInvalid), errors.Is(err, configapply.ErrGeminiAttention), errors.Is(err, configapply.ErrGeminiCapacity), errors.Is(err, configapply.ErrGeminiDenied), errors.Is(err, configapply.ErrGeminiClosed):
		return "Gemini操作未完成；核实绑定、权限、批准及本机状态。", true
	default:
		return "", false
	}
}
