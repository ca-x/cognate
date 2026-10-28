package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ca-x/skillhub/internal/importclient"
	"github.com/spf13/cobra"
)

func newImportCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "import", Short: "上传明确授权的导出数据并预览仓库候选", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("请使用 import magpie 指定授权的导出文件")
		},
	}
	command.AddCommand(newImportMagpieCommand())
	return command
}

func newImportMagpieCommand() *cobra.Command {
	var server, file, expectedSHA256 string
	var tokenStdin, apply bool
	var selected []int
	command := &cobra.Command{
		Use: "magpie", Short: "预览 Magpie 导出；显式选择并固定原文 SHA256 后逐仓收录", Args: cobra.NoArgs,
		Long: "默认只上传预览。--apply --expected-sha256 HASH --select 3,1 按本次预览序号及参数顺序逐仓收录。收录整个公开仓库，已有条目仍校验缓存；部分成功保留，未知结果可能已提交，不自动重试或回滚。导出可含未选择的敏感配置，仅上传到可信服务端。",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Context().Err(); err != nil {
				return err
			}
			if !tokenStdin {
				return errors.New("必须使用 --token-stdin 从标准输入提供会话令牌")
			}
			if apply && (expectedSHA256 == "" || len(selected) == 0) {
				return errors.New("--apply 必须同时指定 --expected-sha256 和 --select")
			}
			if !apply && (command.Flags().Changed("select") || command.Flags().Changed("expected-sha256")) {
				return errors.New("选择和 SHA256 固定必须显式使用 --apply")
			}
			session, err := importclient.ReadSession(command.InOrStdin())
			if err != nil {
				return err
			}
			client, err := importclient.New(server, session, nil)
			if err != nil {
				return err
			}
			var preview *importclient.Preview
			if apply {
				preview, err = client.PreviewFileAtHash(command.Context(), file, expectedSHA256)
			} else {
				preview, err = client.PreviewFile(command.Context(), file)
			}
			if err != nil {
				return err
			}
			if err := showImportPreview(command.OutOrStdout(), preview); err != nil {
				return err
			}
			if !apply {
				return nil
			}
			result, operationErr := client.ApplyFile(command.Context(), file, expectedSHA256, preview, selected)
			return errors.Join(operationErr, showImportResult(command.OutOrStdout(), result))
		},
	}
	command.Flags().StringVar(&server, "server", "", "SkillHub HTTPS origin（显式 loopback 可使用 HTTP）")
	command.Flags().StringVar(&file, "file", "", "明确授权的非空 Magpie JSON regular 文件（≤1 MiB；不跟随链接）")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "从有界标准输入读取会话令牌；不保存凭据")
	command.Flags().BoolVar(&apply, "apply", false, "显式收录所选仓库（默认只预览）")
	command.Flags().StringVar(&expectedSHA256, "expected-sha256", "", "预览显示的原文 SHA256；收录前重读重验")
	command.Flags().IntSliceVar(&selected, "select", nil, "本次预览的 1-based 序号，按参数顺序处理，可重复指定（例如 3,1）")
	_ = command.MarkFlagRequired("server")
	_ = command.MarkFlagRequired("file")
	return command
}

func showImportPreview(output io.Writer, preview *importclient.Preview) error {
	candidates := preview.Candidates()
	if _, err := fmt.Fprintf(output, "SHA256 %s\nREPOSITORIES %d REFERENCES %d\n", preview.SHA256(), len(candidates), preview.ReferencedSkills()); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "INDEX\tREPOSITORY\tREF\tEXISTING"); err != nil {
		return err
	}
	for _, candidate := range candidates {
		branch := candidate.Branch
		if branch == "" {
			branch = "(default)"
		}
		if _, err := fmt.Fprintf(writer, "%d\t%s\t%s\t%t\n", candidate.Index, terminalText(candidate.Repository), terminalText(branch), candidate.Existing); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func showImportResult(output io.Writer, result importclient.Result) error {
	for _, outcome := range result.Outcomes {
		code := ""
		if outcome.Code != 0 {
			code = " " + outcome.Code.String()
		}
		if _, err := fmt.Fprintf(output, "%d %s%s\n", outcome.Candidate.Index, outcome.Status, code); err != nil {
			return err
		}
	}
	return nil
}
