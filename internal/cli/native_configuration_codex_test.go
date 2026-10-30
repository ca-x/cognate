package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
)

func TestCodexCLIDefaultUnqualified(test *testing.T) {
	if _, err := configapply.QualifiedCodexNativeProfile(); err == nil {
		test.Skip("private qualified compiler fixture; normal closed-registry gate runs separately")
	}
	root := test.TempDir()
	home := filepath.Join(root, "missing-home")
	test.Setenv("HOME", home)
	command := newNativeConfigurationCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetIn(bytes.NewBufferString("SYNTHETIC_SESSION"))
	command.SetArgs([]string{"apply", "--target", "codex.config", "--server", "https://127.0.0.1:1", "--owner", "synthetic-owner", "--asset", "synthetic-asset", "--revision", "1", "--profile", "synthetic-c0", "--token-stdin", "--codex-executable", "/not-read", "--codex-cwd", "/not-read"})
	if err := command.ExecuteContext(test.Context()); !errors.Is(err, configapply.ErrCodexUnsupported) {
		test.Fatal("empty qualification did not reject before local/session/native I/O")
	}
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("closed-registry CLI initialized local HOME")
	}
	if bytes.Contains(output.Bytes(), []byte("SYNTHETIC_SESSION")) {
		test.Fatal("session leaked")
	}
}

type codexTestWriter struct {
	bytes.Buffer
	short bool
}

func (writer *codexTestWriter) WriteContext(_ context.Context, data []byte) (int, error) {
	if writer.short {
		return len(data) - 1, nil
	}
	return writer.Write(data)
}

func TestCodexCLICompleteSafeReview(test *testing.T) {
	view := nativeconfig.CodexPreview{OperationID: strings.Repeat("a", 32), AssetID: "asset-metadata", AssetRevision: 1, C0ProfileID: "C0", NativeProfileID: "native", ExpiresAt: time.Now().Add(time.Minute), RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, AuthKept: true}
	for field := codexconfig.FieldModelProvider; field <= codexconfig.FieldDefaultReasoning; field++ {
		view.Changes = append(view.Changes, nativeconfig.CodexChange{Role: codexconfig.ConfigRole, Field: field, Action: codexconfig.Write, Count: uint32(field)})
	}
	writer := &codexTestWriter{}
	if codexWriteReview(test.Context(), writer, codexCLIOptions{action: "apply", server: "https://synthetic.invalid", owner: "owner-metadata"}, view) != nil {
		test.Fatal("safe review")
	}
	text := writer.String()
	for _, label := range []string{"model-provider", "model", "reasoning", "catalog-ref", "provider-name", "endpoint", "wire-api", "requires-auth", "bearer", "catalog-models", "display-name", "context-window", "parallel", "modalities", "instructions", "reasoning-levels", "default-reasoning"} {
		if !strings.Contains(text, "字段 "+label+"；") {
			test.Fatal("omitted explicit safe field from review: " + label)
		}
	}
	if strings.Count(text, "角色 ") != len(view.Changes) || !strings.Contains(text, "需批准槽 codex.provider-key") || !strings.Contains(text, "CODEX REVIEW END") {
		test.Fatal("incomplete full review")
	}
	if codexWriteReview(test.Context(), &codexTestWriter{short: true}, codexCLIOptions{action: "apply"}, view) == nil {
		test.Fatal("nil-error short review write accepted")
	}
	view.Changes[0].Field = 255
	if codexWriteReview(test.Context(), &codexTestWriter{}, codexCLIOptions{action: "apply"}, view) == nil {
		test.Fatal("unknown field rendered as trusted metadata")
	}
}

func TestCodexCLIClosedOutcomesAndErrors(test *testing.T) {
	for state := configapply.CodexPreviewed; state <= configapply.CodexRetiredUnpublished; state++ {
		writer := &codexTestWriter{}
		err := codexShowStatus(test.Context(), writer, nativeconfig.CodexLocalResult{OperationID: "metadata", State: state})
		success := state == configapply.CodexPreviewed || state == configapply.CodexCompleted || state == configapply.CodexRestored || state == configapply.CodexAbandonedBeforeWrite
		if (err == nil) != success {
			test.Fatal("unknown/intermediate local state claimed successful completion")
		}
	}
	if codexShowStatus(test.Context(), &codexTestWriter{}, nativeconfig.CodexLocalResult{State: 255}) == nil {
		test.Fatal("unknown local enum accepted")
	}
	if _, ok := codexRemoteLabel(configclient.NativeGrantStatus("CANARY_PRIVATE")); ok {
		test.Fatal("unknown remote enum accepted")
	}
	for _, err := range []error{errors.New("CANARY_PRIVATE"), fmt.Errorf("CANARY_PRIVATE: %w", configapply.ErrCodexUnknown), configapply.ErrCodexExpired, configapply.ErrCodexUnsupported} {
		if strings.Contains(nativeErrorMessage(err), "CANARY_PRIVATE") {
			test.Fatal("raw diagnostic escaped")
		}
	}
}

func TestCodexCLILocalReviewOperationBinding(test *testing.T) {
	sourceOperation := strings.Repeat("b", 32)
	planOperation := strings.Repeat("a", 32)
	view := nativeconfig.CodexPreview{OperationID: planOperation, AuthKept: true}
	for _, action := range []string{"restore", "recover", "abandon", "retire-unpublished"} {
		test.Run(action, func(test *testing.T) {
			options := codexCLIOptions{action: action, operation: sourceOperation}
			stdout, terminal := &codexTestWriter{}, &codexTestWriter{}
			for _, writer := range []*codexTestWriter{stdout, terminal} {
				if err := codexWriteReview(test.Context(), writer, options, view); err != nil {
					test.Fatal(err)
				}
			}
			if stdout.String() != terminal.String() || !strings.Contains(terminal.String(), "source-operation="+sourceOperation) || !strings.Contains(terminal.String(), "plan-operation="+planOperation) || !strings.Contains(terminal.String(), "CODEX REVIEW END action="+action+" operation="+planOperation) {
				test.Fatal("local review omitted source/new-plan binding or diverged between outputs")
			}
			options.operation = "source\x1b[31m\n\u202e"
			escaped := &codexTestWriter{}
			if err := codexWriteReview(test.Context(), escaped, options, view); err != nil || strings.ContainsAny(escaped.String(), "\x1b\u202e") || strings.Contains(escaped.String(), "source\x1b[31m\n") {
				test.Fatal("source operation was not safely rendered")
			}
		})
	}
}

func TestCodexCLIArguments(test *testing.T) {
	for _, arguments := range [][]string{
		{"recover", "--operation", "metadata"},
		{"abandon", "--target", "claude.settings", "--operation", "metadata", "--apply"},
		{"retire-unpublished", "--target", "codex.config"},
		{"status", "--target", "codex.config", "--operation", "metadata", "--apply"},
		{"apply", "--target", "codex.config", "--timeout", "0"},
		{"apply", "--target", "codex.config", "--timeout", "6m"},
		{"apply", "--target", "foreign"},
		{"apply", "--target", "claude.settings", "--codex-executable", ""},
	} {
		command := newNativeConfigurationCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs(arguments)
		if command.ExecuteContext(test.Context()) == nil {
			test.Fatal("invalid flags/target admitted")
		}
	}
}

func TestCodexCLISixCommandRegistration(test *testing.T) {
	command := newNativeConfigurationCommand()
	for _, name := range []string{"apply", "status", "recover", "restore", "abandon", "retire-unpublished"} {
		found, _, err := command.Find([]string{name})
		if err != nil || found == command || found.Name() != name || found.Flags().Lookup("target") == nil {
			test.Fatal("missing explicit Codex target route: " + name)
		}
	}
}
