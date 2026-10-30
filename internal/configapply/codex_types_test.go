package configapply

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
)

func codexNativeLaunchFixture(test *testing.T, home string) CodexLaunch {
	test.Helper()
	executable := os.Getenv("COGNATE_Q2_NATIVE_FIXTURE")
	if executable == "" {
		test.Skip("explicit pinned native consumer fixture required; no runtime qualification from compilation")
	}
	workspace := filepath.Join(filepath.Dir(home), "workspace")
	if os.Mkdir(workspace, 0o700) != nil {
		test.Fatal("workspace fixture setup failed")
	}
	return CodexLaunch{Executable: executable, Directory: workspace, Arguments: []string{"app-server"}}
}

func codexShapeSelection(test *testing.T, engine *CodexEngine) CodexSelection {
	test.Helper()
	current, err := engine.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil {
		test.Fatal(err)
	}
	prepared := &nativev1.PreparationBinding{OwnerId: "fixture-owner", ServerOrigin: "https://synthetic.invalid", OperationId: bytes.Repeat([]byte{0x51}, 16), AssetId: "fixture-asset", AssetRevision: 1, AgentId: "codex", SchemaVersion: 1, Target: nativev1.Target(2), ProfileId: "fixture-profile", SelectionRevision: 1, RequiredSlots: []nativev1.SecretSlot{nativev1.SecretSlot(3)}, PrepareExpiresAtUnixMs: time.Now().Add(time.Minute).UnixMilli(), Codex: &nativev1.CodexPreparationV1{}}
	declaration := agentconfig.CodexProviderDeclarationV1{APIVersion: 1, Target: agentconfig.CodexConfig, ProviderID: agentconfig.CodexProviderID, WireAPI: agentconfig.CodexWireAPI, RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, CredentialReference: agentconfig.CodexProviderKeySecret, LocalAuthPolicy: agentconfig.CodexPreserveLocalLoginV1, Model: "synthetic-model", Endpoint: "https://synthetic.invalid/v1"}
	selection, err := NewCodexSelection(prepared, declaration, time.Now().Add(time.Minute), current)
	if err != nil {
		test.Fatal(err)
	}
	prepared.OwnerId = "changed-caller"
	declaration.RequiredSlots[0] = "changed-slot"
	if selection.get[codexSelectionData]().prepared.OwnerId != "fixture-owner" || selection.get[codexSelectionData]().declaration.RequiredSlots[0] != agentconfig.CodexProviderKeySecret {
		test.Fatal("shape aliases caller data")
	}
	return selection
}

func TestCodexOpaqueAndCounterBoundaries(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	selection := codexShapeSelection(test, engine)
	values := []any{engine, *engine, selection, CodexPlan{}, CodexAdmission{}, CodexRecoveryPlan{}, CodexRestorePlan{}, CodexSelectionState{}, CodexObservation{}, struct{ private CodexSelection }{selection}, map[string]any{"private": selection}}
	for _, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
			output := fmt.Sprintf(verb, value)
			if strings.Contains(output, "fixture-owner") || strings.Contains(output, home) || strings.Contains(output, "synthetic-model") {
				test.Fatal("opaque formatting leaked private data")
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil && len(encoded) != 0 {
			test.Fatal("failed opaque JSON produced bytes")
		}
		if bytes.Contains(encoded, []byte("fixture-owner")) || bytes.Contains(encoded, []byte(home)) {
			test.Fatal("opaque JSON leaked")
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("fixed", "private", value)
		if bytes.Contains(output.Bytes(), []byte("fixture-owner")) {
			test.Fatal("opaque slog leaked")
		}
	}
	if _, err := (CodexSelectionState{}).NextSelectionRevision(); err == nil {
		test.Fatal("zero current treated as observed")
	}
	current, err := engine.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil {
		test.Fatal(err)
	}
	current.get[codexCurrent]().record.Revision = math.MaxUint64
	current.get[codexCurrent]().record.Confirmation = math.MaxUint64
	if _, err := current.NextSelectionRevision(); err == nil {
		test.Fatal("selection overflow")
	}
	if _, err := current.NextConfirmationRevision(); err == nil {
		test.Fatal("confirmation overflow")
	}
	current.Destroy()
	if current.DeviceID() != "" {
		test.Fatal("destroy retained current")
	}
}
