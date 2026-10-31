package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/ca-x/cognate/internal/grokclient"
	"github.com/spf13/cobra"
)

var errGrokCommand = errors.New("Grok asset command refused; check exact flags, bounded private files and owner session")
var errGrokUnknown = errors.New("Grok mutation outcome unknown; do not replay; manually reconcile with grok-assets list/get/read/order get; no durable receipt")
var errGrokDenied = errors.New("Grok owner session denied")
var errGrokConflict = errors.New("Grok asset or order revision changed; read and review a fresh operation")
var errGrokInUse = errors.New("Grok asset is referenced by a Pack; remove the reference before deletion")
var errGrokUnsupported = errors.New("Grok asset operation unsupported; Linux bounded terminal/file input required; no native application")
var errGrokExpired = errors.New("Grok review expired; prepare and review again, never replay an unknown mutation")
var errGrokNotFound = errors.New("Grok owner asset not found")
var errGrokLimit = errors.New("Grok asset capacity exhausted")

func grokCommandError(err error) error {
	if err == nil {
		return nil
	}
	for _, pair := range [][2]error{{grokclient.ErrUnknown, errGrokUnknown}, {grokclient.ErrDenied, errGrokDenied}, {grokclient.ErrConflict, errGrokConflict}, {grokclient.ErrInUse, errGrokInUse}, {grokclient.ErrUnsupported, errGrokUnsupported}, {grokclient.ErrExpired, errGrokExpired}, {grokclient.ErrNotFound, errGrokNotFound}, {grokclient.ErrLimit, errGrokLimit}} {
		if errors.Is(err, pair[0]) || errors.Is(err, pair[1]) {
			return pair[1]
		}
	}
	return errGrokCommand
}

func grokInvocation(args []string) bool { return len(args) > 0 && args[0] == "grok-assets" }

type grokOutput struct {
	output    *nativeOutput
	failed    bool
	submitted bool
}

func (output *grokOutput) Write(data []byte) (int, error) {
	return output.WriteContext(output.output.ctx, data)
}

func (output *grokOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	count, err := output.output.WriteContext(ctx, data)
	if count != len(data) && err == nil {
		err = io.ErrShortWrite
	}
	output.failed = output.failed || err != nil
	return count, err
}

func prepareGrokOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	if output, ok := writer.(*grokOutput); ok {
		return &nativeOutput{ctx: ctx, writer: output, close: func() error { return nil }, remaining: 4 << 20}, nil
	}
	return prepareSelectionOutput(ctx, writer)
}

func executeGrok(ctx context.Context, command *cobra.Command) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	prepared, err := prepareSelectionOutput(ctx, os.Stdout)
	if err != nil {
		return errGrokCommand
	}
	output := &grokOutput{output: prepared.(*nativeOutput)}
	command.SetOut(output)
	command.SetErr(output)
	err = command.ExecuteContext(ctx)
	closeErr := prepared.Close()
	if err != nil {
		return grokCommandError(err)
	}
	if output.failed || closeErr != nil || ctx.Err() != nil {
		if output.submitted {
			return errGrokUnknown
		}
		return errGrokCommand
	}
	return nil
}

func grokDiagnostic(ctx context.Context, err error) {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	output, prepareErr := prepareNativeOutput(ctx, os.Stderr)
	if prepareErr != nil {
		return
	}
	_, _ = io.WriteString(output, grokCommandError(err).Error()+"\n")
	_ = output.Close()
}

const grokHelp = "grok-assets list|get|read|create|update|copy|delete|order get|order replace\n--server ORIGIN --token-stdin (owner session only, never share token)\ncreate/update --draft-file JSON [--key-action keep|replace|clear] [--key-file PRIVATE]\nget/read/update/delete/copy --id ID; update/delete --expected-revision N\ncopy --expected-source-revision N --name NAME\norder replace --expected-order-revision N --ids-file JSON (explicit zero allowed)\nMutations default to local review, NOT server-validated. --apply requires complete same controlling-TTY review, explicit key action confirmation and final exact phrase.\nAll targets NativeUnavailable; saved assets are not applied. Linux bounded terminal/file input; no automatic Unknown replay or durable receipt.\n"

func newGrokConfigurationCommand() *cobra.Command {
	group := &cobra.Command{Use: "grok-assets COMMAND", Short: "管理当前所有者的加密Grok资产（保存不等于本机应用）", DisableFlagParsing: true, SilenceUsage: true, SilenceErrors: true}
	group.RunE = func(command *cobra.Command, args []string) error {
		if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			output, err := prepareGrokOutput(command.Context(), command.OutOrStdout())
			if err != nil {
				return errGrokCommand
			}
			count, writeErr := io.WriteString(output, grokHelp)
			closeErr := output.Close()
			if count != len(grokHelp) || writeErr != nil || closeErr != nil {
				return errGrokCommand
			}
			return nil
		}
		action := args[0]
		args = args[1:]
		if action == "order" {
			if len(args) == 0 || args[0] != "get" && args[0] != "replace" {
				return errGrokCommand
			}
			action = "order-" + args[0]
			args = args[1:]
		}
		switch action {
		case "list", "get", "read", "create", "update", "copy", "delete", "order-get", "order-replace":
		default:
			return errGrokCommand
		}
		var origin, id, draftFile, keyFile, keyAction, name, idsFile string
		var revision, sourceRevision, orderRevision int64
		var tokenStdin, apply bool
		mutation := action == "create" || action == "update" || action == "copy" || action == "delete" || action == "order-replace"
		hasDraft := action == "create" || action == "update"
		inner := &cobra.Command{Use: action, SilenceErrors: true, SilenceUsage: true, Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errGrokCommand
			}
			return nil
		}}
		inner.SetOut(command.OutOrStdout())
		inner.SetErr(command.ErrOrStderr())
		inner.SetIn(command.InOrStdin())
		inner.SetFlagErrorFunc(func(*cobra.Command, error) error { return errGrokCommand })
		inner.Flags().StringVar(&origin, "server", "", "HTTPS origin or explicit literal-loopback HTTP")
		inner.Flags().BoolVar(&tokenStdin, "token-stdin", false, "bounded non-TTY owner session stdin, never approval")
		if mutation {
			inner.Flags().BoolVar(&apply, "apply", false, "submit only after complete same controlling-TTY review")
		}
		if action == "get" || action == "read" || action == "update" || action == "copy" || action == "delete" {
			inner.Flags().StringVar(&id, "id", "", "owner asset or copy-source ID")
		}
		if action == "update" || action == "delete" {
			inner.Flags().Int64Var(&revision, "expected-revision", 0, "explicit positive current revision")
		}
		if action == "copy" {
			inner.Flags().Int64Var(&sourceRevision, "expected-source-revision", 0, "explicit positive source revision")
			inner.Flags().StringVar(&name, "name", "", "copy destination name")
		}
		if hasDraft {
			inner.Flags().StringVar(&draftFile, "draft-file", "", "owned regular bounded canonical Grok Draft ProtoJSON")
			inner.Flags().StringVar(&keyAction, "key-action", "", "explicit keep (update only), replace, or clear")
			inner.Flags().StringVar(&keyFile, "key-file", "", "replace only: private raw UTF8 key, no line ending")
		}
		if action == "order-replace" {
			inner.Flags().Int64Var(&orderRevision, "expected-order-revision", 0, "explicit nonnegative order revision; zero means absent partition")
			inner.Flags().StringVar(&idsFile, "ids-file", "", "owned regular bounded ordered unique ID JSON array")
		}
		inner.RunE = func(inner *cobra.Command, _ []string) (result error) {
			if origin == "" || !tokenStdin || inner.Flags().Lookup("id") != nil && id == "" || hasDraft && draftFile == "" || (action == "update" || action == "delete") && revision < 1 || action == "copy" && (sourceRevision < 1 || name == "") || action == "order-replace" && (!inner.Flags().Changed("expected-order-revision") || orderRevision < 0 || idsFile == "") {
				return errGrokCommand
			}
			key := grokclient.NoKeyMutation
			if hasDraft {
				switch keyAction {
				case "":
					if inner.Flags().Changed("key-action") {
						return errGrokCommand
					}
				case "keep":
					if action != "update" {
						return errGrokCommand
					}
					key = grokclient.KeepKey
				case "replace":
					key = grokclient.ReplaceKey
				case "clear":
					key = grokclient.ClearKey
				default:
					return errGrokCommand
				}
				if (key == grokclient.ReplaceKey) != (keyFile != "") || key != grokclient.ReplaceKey && inner.Flags().Changed("key-file") {
					return errGrokCommand
				}
			}
			if nativeConfigurationPlatform(inner.Context()) != nil {
				return errGrokUnsupported
			}
			ctx, cancel := context.WithTimeout(inner.Context(), 120*time.Second)
			defer cancel()
			output, err := prepareGrokOutput(ctx, inner.OutOrStdout())
			if err != nil {
				return errGrokCommand
			}
			submitted := false
			defer func() {
				if output.Close() != nil && result == nil {
					result = errGrokCommand
					if submitted {
						result = errGrokUnknown
					}
				}
			}()
			session, err := readNativeSession(ctx, inner.InOrStdin())
			if err != nil {
				return errGrokCommand
			}
			client, err := grokclient.New(origin, session, nil)
			session = ""
			if err != nil {
				return grokCommandError(err)
			}
			defer client.Close()
			switch action {
			case "list":
				items, err := client.List(ctx)
				if err != nil {
					return grokCommandError(err)
				}
				for _, item := range items {
					if showGrokMetadata(output, item) != nil {
						return errGrokCommand
					}
				}
				return nil
			case "get":
				item, err := client.Get(ctx, id)
				if err != nil {
					return grokCommandError(err)
				}
				return showGrokMetadata(output, item)
			case "read":
				view, err := client.Read(ctx, id)
				if err != nil {
					return grokCommandError(err)
				}
				defer view.Close()
				return grokCommandError(view.WriteSummary(ctx, output))
			case "order-get":
				order, err := client.GetOrder(ctx)
				if err != nil {
					return grokCommandError(err)
				}
				return showGrokOrder(output, order)
			}
			change := grokclient.Change{ID: id, ExpectedRevision: revision, Name: name, ExpectedOrderRevision: orderRevision}
			var input *grokclient.Input
			if hasDraft {
				input, err = grokclient.LoadInput(ctx, draftFile, key, keyFile)
				if err != nil {
					return grokCommandError(err)
				}
				defer input.Close()
			}
			switch action {
			case "create":
				change.Action = grokclient.Create
			case "update":
				change.Action = grokclient.Update
			case "copy":
				change.Action, change.ExpectedRevision = grokclient.Copy, sourceRevision
			case "delete":
				change.Action = grokclient.Delete
			case "order-replace":
				change.Action = grokclient.ReplaceOrder
				change.OrderIDs, err = grokclient.ReadOrderFile(ctx, idsFile)
				if err != nil {
					return grokCommandError(err)
				}
			}
			operation, err := client.Prepare(ctx, change, input)
			if err != nil {
				return grokCommandError(err)
			}
			defer operation.Close()
			if operation.WriteSummary(ctx, output) != nil {
				return errGrokCommand
			}
			if !apply {
				return nil
			}
			if err := confirmGrokOperation(ctx, operation); err != nil {
				return grokCommandError(err)
			}
			submitted = true
			if outer, ok := inner.OutOrStdout().(*grokOutput); ok {
				outer.submitted = true
			}
			acknowledged, err := operation.Submit(ctx)
			if err != nil {
				return grokCommandError(err)
			}
			if err := showGrokResult(output, acknowledged); err != nil {
				return errGrokUnknown
			}
			return nil
		}
		inner.SetArgs(args)
		return grokCommandError(inner.ExecuteContext(command.Context()))
	}
	return group
}

func confirmGrokOperation(ctx context.Context, operation *grokclient.Operation) (result error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	terminal, err := openNativeConfirmationTTY(ctx)
	if err != nil {
		return errGrokCommand
	}
	defer func() {
		if terminal.Close() != nil && result == nil {
			result = errGrokCommand
		}
	}()
	review := &nativeOutput{ctx: ctx, writer: selectionTerminalWriter{ctx: ctx, terminal: terminal}, close: func() error { return nil }, remaining: 64 << 10}
	if operation.WriteSummary(ctx, review) != nil {
		return errGrokCommand
	}
	switch operation.KeyAction() {
	case grokclient.ReplaceKey:
		if nativeConfirm(ctx, terminal, "replace grokbuild.api-key "+operation.ReviewID()) != nil {
			return errGrokCommand
		}
	case grokclient.ClearKey:
		if nativeConfirm(ctx, terminal, "clear grokbuild.api-key "+operation.ReviewID()) != nil {
			return errGrokCommand
		}
	}
	phrase := operation.Confirmation()
	if phrase == "" || nativeConfirm(ctx, terminal, phrase) != nil {
		return errGrokCommand
	}
	return nil
}

func showGrokMetadata(output io.Writer, metadata grokclient.Metadata) error {
	_, err := fmt.Fprintf(output, "ASSET id=%s agent=%s kind=%d schema=%d revision=%d name=%s created=%d updated=%d schema-known=%t (extras require typed read; metadata is not write approval) NativeUnavailable; not applied\n", strconv.QuoteToASCII(metadata.ID), strconv.QuoteToASCII(metadata.Agent), metadata.Kind, metadata.Schema, metadata.Revision, strconv.QuoteToASCII(metadata.Name), metadata.CreatedAt, metadata.UpdatedAt, metadata.Schema == 1)
	if err != nil {
		return errGrokCommand
	}
	return nil
}

func showGrokOrder(output io.Writer, order grokclient.OrderSnapshot) error {
	if _, err := fmt.Fprintf(output, "ORDER revision=%d count=%d NativeUnavailable; not applied\n", order.Revision, len(order.Assets)); err != nil {
		return errGrokCommand
	}
	for _, asset := range order.Assets {
		if err := showGrokMetadata(output, asset); err != nil {
			return err
		}
	}
	return nil
}

func showGrokResult(output io.Writer, result grokclient.Result) error {
	if _, err := io.WriteString(output, "Owner asset metadata saved/deleted/ordered; NativeUnavailable; no native application.\n"); err != nil {
		return errGrokUnknown
	}
	if result.Metadata != nil {
		return showGrokMetadata(output, *result.Metadata)
	}
	if result.Order != nil {
		return showGrokOrder(output, *result.Order)
	}
	if !result.Deleted {
		return errGrokUnknown
	}
	return nil
}
