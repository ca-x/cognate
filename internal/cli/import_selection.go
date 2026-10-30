package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

func newImportSelectionCommand() *cobra.Command {
	var server, path, source, expected, optionsPath string
	var tokenStdin, apply bool
	var repositories, skills, configurations []int
	var notices []string
	command := &cobra.Command{Use: "select", Short: "明确选择导入技能引用、仓库与配置资产（不安装、不迁移凭据）", Args: cobra.NoArgs,
		Long: "默认只上传授权快照并预览。--apply 必须固定原文 SHA256、显式选择序号和逐项排除提示，再在控制终端确认。提交结果未知时只使用 import status，不重新提交。配置仅存服务端，技能仅引用已收录内容。整个文件可能含秘密，仅上传到可信服务端。",
		RunE: func(command *cobra.Command, _ []string) error {
			if !tokenStdin {
				return errors.New("必须使用 --token-stdin 从标准输入提供会话令牌")
			}
			var sourceID importv1.ImportSelectionSource
			switch source {
			case "cc-switch":
				sourceID = importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24
			case "magpie":
				sourceID = importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON
			default:
				return errors.New("--source 必须是 cc-switch 或 magpie")
			}
			if apply && (expected == "" || len(repositories)+len(skills)+len(configurations) == 0) {
				return errors.New("--apply 必须固定 --expected-sha256 并显式选择候选")
			}
			if !apply && (expected != "" || len(repositories)+len(skills)+len(configurations)+len(notices) > 0) {
				return errors.New("选择、排除确认与 SHA256 固定仅用于显式 --apply")
			}
			ctx := command.Context()
			output, err := prepareSelectionOutput(ctx, command.OutOrStdout())
			if err != nil {
				return err
			}
			defer output.Close()
			session, err := readNativeSession(ctx, command.InOrStdin())
			if err != nil {
				return err
			}
			client, err := importclient.NewSelectionClient(server, session, nil)
			if err != nil {
				return err
			}
			defer client.Close()
			var options []*importv1.ImportCandidateOptions
			if optionsPath != "" {
				options, err = importclient.ReadSelectionOptions(ctx, optionsPath)
				if err != nil {
					return err
				}
			}
			preview, err := client.PreviewFile(ctx, sourceID, path, expected, options)
			if err != nil {
				return err
			}
			defer preview.Close()
			if err := showSelectionPreview(output, preview); err != nil {
				return err
			}
			if !apply {
				return nil
			}
			operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Repositories: repositories, Skills: skills, Configurations: configurations, AcknowledgedNotices: notices})
			if err != nil {
				return err
			}
			defer operation.Close()
			if _, err := fmt.Fprintf(output, "SELECTED repositories=%v skills=%v configurations=%v\nOPERATION %s\n", repositories, skills, configurations, operation.ID()); err != nil {
				return err
			}
			terminal, err := openNativeConfirmationTTY(ctx)
			if err != nil {
				return err
			}
			defer terminal.Close()
			approvalOutput := &nativeOutput{ctx: ctx, writer: selectionTerminalWriter{ctx: ctx, terminal: terminal}, close: func() error { return nil }, remaining: 4 << 20}
			if err := showSelectionPreview(approvalOutput, preview); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(approvalOutput, "SELECTED repositories=%v skills=%v configurations=%v\nOPERATION %s\n", repositories, skills, configurations, operation.ID()); err != nil {
				return err
			}
			if err := nativeConfirm(ctx, terminal, "import "+operation.ID()); err != nil {
				return err
			}
			result, commitErr := operation.CommitFile(ctx, path)
			return errors.Join(commitErr, showSelectionStatus(output, result))
		}}
	command.Flags().StringVar(&server, "server", "", "可信 HTTPS origin（显式 loopback HTTP 可用）")
	command.Flags().StringVar(&path, "file", "", "授权的独立快照 regular 文件；cc-switch ≤16 MiB，Magpie ≤1 MiB")
	command.Flags().StringVar(&source, "source", "", "cc-switch 或 magpie")
	command.Flags().StringVar(&optionsPath, "options-file", "", "≤64 KiB ProtoJSON {options:[...]}，仅候选名称、认证模式或完整 skill binding；不含密钥")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "有界非TTY stdin 会话令牌，不表示批准")
	command.Flags().BoolVar(&apply, "apply", false, "显式选择后在控制终端确认（默认只预览）")
	command.Flags().StringVar(&expected, "expected-sha256", "", "先前预览的原文 SHA256；提交重读校验")
	command.Flags().IntSliceVar(&repositories, "repositories", nil, "所选仓库的 1-based 序号，默认无")
	command.Flags().IntSliceVar(&skills, "skills", nil, "所选技能引用的 1-based 序号，默认无")
	command.Flags().IntSliceVar(&configurations, "configurations", nil, "所选服务端配置的 1-based 序号，默认无")
	command.Flags().StringSliceVar(&notices, "ack-notice", nil, "按预览顺序逐项确认的 NOTICE ID；可重复指定")
	for _, name := range []string{"server", "file", "source"} {
		_ = command.MarkFlagRequired(name)
	}
	return command
}

type selectionTerminalWriter struct {
	ctx      context.Context
	terminal nativeTerminal
}

func (writer selectionTerminalWriter) Write(data []byte) (int, error) {
	return writer.terminal.WriteContext(writer.ctx, data)
}

func newImportStatusCommand() *cobra.Command {
	var server, operation string
	var tokenStdin bool
	command := &cobra.Command{Use: "status", Short: "只查询同一导入操作，不上传、不重试提交", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if !tokenStdin {
			return errors.New("必须使用 --token-stdin")
		}
		output, err := prepareSelectionOutput(command.Context(), command.OutOrStdout())
		if err != nil {
			return err
		}
		defer output.Close()
		session, err := readNativeSession(command.Context(), command.InOrStdin())
		if err != nil {
			return err
		}
		client, err := importclient.NewSelectionClient(server, session, nil)
		if err != nil {
			return err
		}
		defer client.Close()
		result, err := client.Status(command.Context(), operation)
		if err != nil {
			return err
		}
		if err := showSelectionStatus(output, result); err != nil {
			return err
		}
		if result.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED {
			return importclient.ErrIncomplete
		}
		return nil
	}}
	command.Flags().StringVar(&server, "server", "", "原服务端 origin")
	command.Flags().StringVar(&operation, "operation-id", "", "原 OPERATION 的32位小写hex；NotObserved 不等于未来不会完成")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "stdin 提供当前 owner session")
	_ = command.MarkFlagRequired("server")
	_ = command.MarkFlagRequired("operation-id")
	return command
}

func prepareSelectionOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	closeOutput := func() error { return nil }
	switch value := writer.(type) {
	case *bytes.Buffer:
	case *os.File:
		owned, err := prepareNativeFileOutput(ctx, value)
		if err != nil {
			return nil, err
		}
		writer = owned
		closeOutput = owned.Close
	default:
		return nil, errNativeIO
	}
	return &nativeOutput{ctx: ctx, writer: writer, close: closeOutput, remaining: 4 << 20}, nil
}

func showSelectionPreview(output io.Writer, preview *importclient.SelectionPreview) error {
	view := preview.View()
	if view == nil {
		return errNativeIO
	}
	if _, err := fmt.Fprintf(output, "SHA256 %s\nEXPIRES_MS %d\n", preview.SHA256(), view.ExpiresMs); err != nil {
		return err
	}
	for index, entry := range view.Repositories {
		if _, err := fmt.Fprintf(output, "REPOSITORY %d id=%s %s %s %s\n", index+1, entry.Id, terminalText(entry.Hint.Repository), terminalText(entry.Hint.Branch), entry.Status); err != nil {
			return err
		}
	}
	for index, entry := range view.Skills {
		if _, err := fmt.Fprintf(output, "SKILL %d id=%s %s %s repository_candidate_id=%s\n", index+1, entry.Id, terminalText(entry.SafeName), entry.Status, entry.RepositoryCandidateId); err != nil {
			return err
		}
		if binding := entry.Binding; binding != nil {
			if _, err := fmt.Fprintf(output, "BINDING repository_id=%s archive_hash=%s skill_id=%s logical_path=%s content_hash=%s repository=%s branch=%s\n", terminalText(binding.RepositoryId), binding.ArchiveHash, terminalText(binding.SkillId), terminalText(binding.LogicalPath), binding.ContentHash, terminalText(binding.Repository), terminalText(binding.Branch)); err != nil {
				return err
			}
		}
	}
	for index, entry := range view.Configurations {
		if _, err := fmt.Fprintf(output, "CONFIGURATION %d id=%s %s %s %s\n", index+1, entry.Id, entry.KnownAgentId, terminalText(entry.SafeName), entry.Status); err != nil {
			return err
		}
		if entry.Draft != nil {
			data, err := protojson.Marshal(entry.Draft)
			if err != nil {
				return errNativeIO
			}
			_, writeErr := fmt.Fprintln(output, terminalText(string(data)))
			clear(data)
			if writeErr != nil {
				return writeErr
			}
		}
		for _, slot := range entry.CredentialSlots {
			if _, err := fmt.Fprintf(output, "CREDENTIAL %s present=%t omitted=true\n", slot.SlotId, slot.Present); err != nil {
				return err
			}
		}
	}
	notices := append([]*importv1.ImportNotice{}, view.Notices...)
	for _, entry := range view.Skills {
		notices = append(notices, entry.Notices...)
	}
	for _, entry := range view.Configurations {
		notices = append(notices, entry.Notices...)
	}
	for _, notice := range notices {
		if _, err := fmt.Fprintf(output, "NOTICE %s %s count=%d\n", notice.Id, notice.Code, notice.Count); err != nil {
			return err
		}
	}
	return nil
}

func showSelectionStatus(output io.Writer, result importclient.SelectionResult) error {
	_, err := fmt.Fprintf(output, "OPERATION %s\nOBSERVATION %s PHASE %s REVISION %d SELECTION_VERIFIED %t\nRECEIPT repositories=%d skills=%d configurations=%d\n仅服务端导入回执；不表示本机安装或应用。NotObserved 不是撤销证明。\n", result.OperationID, result.Observation, result.Phase, result.Revision, result.SelectionVerified, result.Repositories, result.Skills, result.Configurations)
	return err
}
