package configapply

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
)

func TestCodexProductionProfileAndAdmissionRemainClosed(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	selection := codexShapeSelection(test, engine)
	plan, err := engine.PreviewCodex(test.Context(), selection)
	if !errors.Is(err, ErrCodexUnsupported) || plan.View().OperationID != "" {
		test.Fatal("unqualified profile produced trusted plan")
	}
	if len(codexQualifiedProfiles) != 0 {
		test.Fatal("D1 registered profile qualification")
	}
	if err := os.Remove(filepath.Join(home, ".codex")); err != nil {
		test.Fatal(err)
	}
	if _, err := engine.PreviewCodex(test.Context(), selection); !errors.Is(err, ErrCodexUnsupported) {
		test.Fatal("closed qualification read target before refusing")
	}
	for _, handle := range []CodexPlan{{}, {value: new(any(&codexPlanData{engine: engine.private(), selection: selection.get[codexSelectionData](), profile: CodexProfileIdentity{ID: "forged-profile"}}))}} {
		admission, err := engine.ConfirmCodex(test.Context(), handle, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, true)
		if err == nil || admission.CopyPreparedBinding() != nil {
			test.Fatal("forged plan manufactured admission")
		}
	}
	if _, err := engine.ApplyCodex(test.Context(), CodexAdmission{}, codexconfig.ProviderCandidate{}); err == nil {
		test.Fatal("zero admission applied")
	}
	if _, err := engine.MaterializeCodex(test.Context(), CodexAdmission{}, nil); err == nil {
		test.Fatal("zero admission materialized")
	}
	if err := (CodexAdmission{}).Revalidate(test.Context()); err == nil {
		test.Fatal("zero admission revalidated")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("unsupported preview created target")
	}
}

func TestCodexPublishIdentityAndAuthKeepBaseline(test *testing.T) {
	for _, mode := range []string{"same-hash-new-inode", "parent-replaced", "auth-same-hash-new-inode"} {
		test.Run(mode, func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			for _, name := range []string{"config.toml", "auth.json"} {
				if err := os.WriteFile(filepath.Join(home, ".codex", name), []byte("original"), 0o600); err != nil {
					test.Fatal(err)
				}
			}
			bodies := [3][]byte{[]byte("new-config"), nil, []byte("new-catalog")}
			op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, op, bodies)
			if mode == "parent-replaced" {
				if err := os.Rename(filepath.Join(home, ".codex"), filepath.Join(home, "displaced")); err != nil {
					test.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
					test.Fatal(err)
				}
			} else {
				name := "config.toml"
				if mode == "auth-same-hash-new-inode" {
					name = "auth.json"
				}
				path := filepath.Join(home, ".codex", name)
				if err := os.Rename(path, path+".old"); err != nil {
					test.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
					test.Fatal(err)
				}
			}
			result, err := codexPublishLiteral(test.Context(), engine, op.ID)
			if !errors.Is(err, ErrCodexConflict) || result.State == CodexCompleted {
				test.Fatal("physical identity change accepted")
			}
			if mode != "parent-replaced" {
				actual, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
				if err != nil || string(actual) != "original" {
					test.Fatal("CAS failure advanced config")
				}
			}
		})
	}
}
