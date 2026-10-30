package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ca-x/cognate/internal/mcpclient"
	"github.com/spf13/cobra"
)

var errMCPCLI = errors.New("MCP asset command refused; check flags, bounded private files and current session")
var errMCPUnknown = errors.New("MCP mutation outcome is unknown; do not replay; reconcile manually with mcp-assets list/get/read")

func newMCPConfigurationCommand() *cobra.Command {
	group := &cobra.Command{Use: "mcp-assets COMMAND", Short: "管理当前认证 session 的私有 MCP 连接资产（不是网站 MCP token）", DisableFlagParsing: true, SilenceUsage: true, SilenceErrors: true}
	group.RunE = func(command *cobra.Command, args []string) error {
		if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			_, err := io.WriteString(command.OutOrStdout(), "MCP asset commands: list, get, read, preview, create, update, copy, delete\n--server ORIGIN --token-stdin; file inputs only; mutations default to preview. --apply requires a complete real controlling-TTY review.\nNo credential export, no native apply. Linux interactive first slice.\n")
			if err != nil {
				return errMCPCLI
			}
			return nil
		}
		action := args[0]
		switch action {
		case "list", "get", "read", "preview", "create", "update", "copy", "delete":
		default:
			return errMCPCLI
		}
		var server, id, declarationFile, secretsFile, name string
		var revision, sourceRevision int64
		var tokenStdin, apply bool
		inner := &cobra.Command{Use: action, SilenceUsage: true, SilenceErrors: true, Args: func(*cobra.Command, []string) error { return nil }}
		inner.Args = func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errMCPCLI
			}
			return nil
		}
		inner.SetOut(command.OutOrStdout())
		inner.SetErr(command.ErrOrStderr())
		inner.SetIn(command.InOrStdin())
		inner.SetFlagErrorFunc(func(*cobra.Command, error) error { return errMCPCLI })
		inner.Flags().StringVar(&server, "server", "", "HTTPS origin or explicit loopback HTTP")
		inner.Flags().BoolVar(&tokenStdin, "token-stdin", false, "bounded non-TTY stdin session; never approval")
		inner.Flags().StringVar(&id, "id", "", "owner asset ID")
		inner.Flags().Int64Var(&revision, "expected-revision", 0, "exact current asset revision")
		inner.Flags().Int64Var(&sourceRevision, "expected-source-revision", 0, "exact copy-source revision")
		inner.Flags().StringVar(&declarationFile, "declaration-file", "", "explicit regular Draft JSON file")
		inner.Flags().StringVar(&secretsFile, "secrets-file", "", "explicit private mutation-array/base64 JSON file")
		inner.Flags().StringVar(&name, "name", "", "explicit copy destination name")
		inner.Flags().BoolVar(&apply, "apply", false, "mutate only after complete controlling-TTY review and exact confirmation")
		inner.RunE = func(inner *cobra.Command, _ []string) error {
			if !tokenStdin || server == "" {
				return errMCPCLI
			}
			hasDraft := action == "preview" || action == "create" || action == "update"
			mutation := action == "create" || action == "update" || action == "copy" || action == "delete"
			if apply && !mutation || !hasDraft && (declarationFile != "" || secretsFile != "") || hasDraft && declarationFile == "" {
				return errMCPCLI
			}
			if action == "list" || action == "create" {
				if id != "" || revision != 0 || sourceRevision != 0 || name != "" {
					return errMCPCLI
				}
			}
			if action == "get" || action == "read" {
				if id == "" || revision != 0 || sourceRevision != 0 || name != "" {
					return errMCPCLI
				}
			}
			if action == "update" || action == "delete" {
				if id == "" || revision < 1 || sourceRevision != 0 || name != "" {
					return errMCPCLI
				}
			}
			if action == "copy" {
				if id == "" || sourceRevision < 1 || revision != 0 || name == "" {
					return errMCPCLI
				}
			}
			if action == "preview" {
				if sourceRevision != 0 || name != "" || id == "" && revision != 0 || id != "" && revision < 1 {
					return errMCPCLI
				}
			}
			ctx, cancel := context.WithTimeout(inner.Context(), 120*time.Second)
			defer cancel()
			output, err := prepareSelectionOutput(ctx, inner.OutOrStdout())
			if err != nil {
				return errMCPCLI
			}
			defer output.Close()
			session, err := readNativeSession(ctx, inner.InOrStdin())
			if err != nil {
				return errMCPCLI
			}
			client, err := mcpclient.New(server, session, nil)
			session = ""
			if err != nil {
				return errMCPCLI
			}
			defer client.Close()
			var input *mcpclient.Input
			if hasDraft {
				input, err = mcpclient.LoadInput(ctx, declarationFile, secretsFile)
				if err != nil {
					return errMCPCLI
				}
				defer input.Close()
			}
			switch action {
			case "list":
				items, err := client.List(ctx)
				if err != nil {
					return mcpCommandError(err)
				}
				for _, item := range items {
					if showMCPMetadata(output, item) != nil {
						return errMCPCLI
					}
				}
				return nil
			case "get":
				item, err := client.Get(ctx, id)
				if err != nil {
					return mcpCommandError(err)
				}
				return showMCPMetadata(output, item)
			case "read":
				view, err := client.Read(ctx, id)
				if err != nil {
					return mcpCommandError(err)
				}
				if view.WriteSummary(ctx, output) != nil {
					return errMCPCLI
				}
				return nil
			case "preview":
				preview, err := client.Preview(ctx, id, revision, input)
				if err != nil {
					return mcpCommandError(err)
				}
				if preview.WriteSummary(ctx, output) != nil {
					return errMCPCLI
				}
				return nil
			}
			expected := revision
			if action == "copy" {
				expected = sourceRevision
			}
			operation, err := client.Prepare(ctx, mcpclient.Change{Action: action, ID: id, Revision: expected, Name: name}, input)
			if err != nil {
				return mcpCommandError(err)
			}
			defer operation.Close()
			if _, err := fmt.Fprintf(output, "MCP %s reviewed by server; no mutation submitted. Current authenticated session. Native not applied.\n", action); err != nil {
				return errMCPCLI
			}
			if !apply {
				return nil
			}
			reviewCtx, reviewCancel := context.WithTimeout(ctx, 60*time.Second)
			defer reviewCancel()
			terminal, err := openNativeConfirmationTTY(reviewCtx)
			if err != nil {
				return errMCPCLI
			}
			defer terminal.Close()
			review := &nativeOutput{ctx: reviewCtx, writer: selectionTerminalWriter{ctx: reviewCtx, terminal: terminal}, close: func() error { return nil }, remaining: 4 << 20}
			if operation.WriteSummary(reviewCtx, review) != nil {
				return errMCPCLI
			}
			if nativeConfirm(reviewCtx, terminal, operation.Confirmation()) != nil {
				return errMCPCLI
			}
			item, err := operation.Submit(ctx)
			if err != nil {
				return mcpCommandError(err)
			}
			if showMCPMetadata(output, item) != nil {
				return errMCPUnknown
			}
			return nil
		}
		inner.SetArgs(args[1:])
		return mcpCommandError(inner.ExecuteContext(command.Context()))
	}
	return group
}

func mcpCommandError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, mcpclient.ErrUnknown) || errors.Is(err, errMCPUnknown) {
		return errMCPUnknown
	}
	if errors.Is(err, mcpclient.ErrConflict) {
		return errors.New("MCP asset revision changed; read and review the current asset before preparing a new operation")
	}
	if errors.Is(err, mcpclient.ErrDenied) {
		return errors.New("MCP session or operation denied")
	}
	return errMCPCLI
}

func showMCPMetadata(output io.Writer, item mcpclient.Metadata) error {
	if _, err := fmt.Fprintf(output, "MCP ASSET %s revision=%d name=%q target=%q schema=%d readonly=%t\n", item.ID, item.Revision, item.Name, item.Agent, item.Schema, !item.Editable()); err != nil {
		return errMCPCLI
	}
	return nil
}
