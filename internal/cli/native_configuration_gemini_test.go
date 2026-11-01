package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/geminiconfig"
)

func TestGeminiCLICompleteSafeReview(test *testing.T) {
	view := configapply.SafeGeminiPlan{OperationID: strings.Repeat("a", 32), PlanID: strings.Repeat("b", 32), Origin: "https://synthetic.invalid", OwnerID: "synthetic-owner", ProfileID: "synthetic-profile", AssetID: "metadata", AssetRevision: 1, SelectionRevision: 2, ConfirmationRevision: 3, NativeProfileID: "fixed-profile", ExpiresAt: time.Now().Add(time.Minute), RequiredSlots: []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}, FinalReview: true}
	fields := []geminiconfig.Field{geminiconfig.APIKeyField, geminiconfig.EndpointField, geminiconfig.TransportField, geminiconfig.EnvModelField, geminiconfig.SettingsModelField, geminiconfig.AuthModeField}
	for index, field := range fields {
		role := geminiconfig.EnvRole
		if index >= 4 {
			role = geminiconfig.SettingsRole
		}
		view.Changes = append(view.Changes, configapply.GeminiChange{Role: role, Field: field, Ownership: geminiconfig.Foreign, Action: geminiconfig.Write})
	}
	writer := &codexTestWriter{}
	if geminiWriteReview(test.Context(), writer, "apply", view) != nil {
		test.Fatal("full Gemini review unavailable")
	}
	for _, field := range fields {
		if !strings.Contains(writer.String(), string(field)) {
			test.Fatal("safe field omitted")
		}
	}
	for _, label := range []string{".env", "settings.json", "selection 2", "confirmation 3", "双rename非原子", "关闭正在运行", "不迁移OAuth", "gemini.api-key", view.PlanID} {
		if !strings.Contains(writer.String(), label) {
			test.Fatal("review warning or binding omitted")
		}
	}
	if geminiWriteReview(test.Context(), &codexTestWriter{short: true}, "apply", view) == nil {
		test.Fatal("short output accepted")
	}
	view.Changes[0].Field = "UNKNOWN_PRIVATE_FIELD"
	if geminiWriteReview(test.Context(), &codexTestWriter{}, "apply", view) == nil {
		test.Fatal("unknown field rendered")
	}
}

func TestGeminiAuthRunExplicitTarget(test *testing.T) {
	_, _, native, err := authArguments([]string{"run", "--server", "https://synthetic.invalid", "--", "config", "apply", "--target", "gemini.settings"})
	if err != nil || !native {
		test.Fatal("explicit Gemini native-purpose route rejected")
	}
	for _, route := range [][]string{{"config", "apply", "--target", "gemini.settings", "--target", "codex.config"}, {"config", "apply", "--target", "gemini.env"}, {"config", "apply", "--target", "gemini.settings", "--owner", "injected-owner"}} {
		if _, _, _, err := authArguments(append([]string{"run", "--server", "https://synthetic.invalid", "--"}, route...)); err == nil {
			test.Fatal("auth whitelist ambiguity admitted")
		}
	}
}
