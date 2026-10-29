//go:build linux

package configapply

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestNativeRestoreEveryDurableCrash(test *testing.T) {
	for _, present := range []bool{true, false} {
		phases := []string{"before-stage", "stage-synced", "backup-durable", "prepared-durable", "reserve-intent-durable", "entry-captured", "capture-synced", "publish-intent-durable", "entry-published", "target-file-synced", "publication-synced", "result-durable", "cleanup-synced"}
		for _, boundary := range []string{"file-synced", "marker-file-synced", "marker-exchanged", "marker-directory-synced", "renamed", "directory-synced"} {
			phases = append(phases, "journal-operation-Restored-"+boundary)
		}
		for _, phase := range phases {
			test.Run(map[bool]string{true: "existing-", false: "absent-"}[present]+phase, func(test *testing.T) {
				root := test.TempDir()
				home, stateRoot := filepath.Join(root, "home"), filepath.Join(root, "state")
				for _, directory := range []string{home, stateRoot, filepath.Join(home, ".claude")} {
					if err := os.Mkdir(directory, 0o700); err != nil {
						test.Fatal(err)
					}
				}
				base := []byte(`{"restored":"original-private-preimage"}`)
				target := filepath.Join(home, ".claude", "settings.json")
				if present {
					if err := os.WriteFile(target, base, 0o600); err != nil {
						test.Fatal(err)
					}
				}
				command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestNativeCrashChild$", "-test.count=1")
				command.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR=", "XDG_CONFIG_HOME="+filepath.Join(home, "xdg"), "XDG_STATE_HOME="+stateRoot, "COGNATE_NATIVE_B_FIXTURE="+root, "COGNATE_NATIVE_B_RESTORE=1", "COGNATE_NATIVE_B_CRASH="+phase)
				output, err := command.CombinedOutput()
				exit, ok := errors.AsType[*exec.ExitError](err)
				if !ok || exit.ExitCode() != 73 {
					test.Fatalf("restore boundary not reached: %s %v", output, err)
				}
				identifier, err := os.ReadFile(filepath.Join(root, "restore-id"))
				if err != nil {
					test.Fatal(err)
				}
				engine, err := Open(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x5c}, 32))
				if err != nil {
					test.Fatal(err)
				}
				defer engine.Close()
				if _, err := engine.Reconcile(test.Context()); err != nil {
					test.Fatal(err)
				}
				result, err := engine.Status(test.Context(), string(identifier))
				if err != nil {
					test.Fatal(err)
				}
				published := phase == "entry-published" || phase == "target-file-synced" || phase == "publication-synced" || phase == "result-durable" || phase == "cleanup-synced" || strings.HasPrefix(phase, "journal-operation-Restored-")
				if !present && phase == "publish-intent-durable" {
					published = true
				}
				actual, readErr := os.ReadFile(target)
				if published {
					if result.Status != "Restored" || present && (readErr != nil || !bytes.Equal(actual, base)) || !present && !errors.Is(readErr, os.ErrNotExist) {
						test.Fatalf("restored publication misclassified: %v %v", result, readErr)
					}
				} else if result.Status != "Cancelled" && result.Status != "RolledBack" || !published && (readErr != nil || !bytes.Contains(actual, []byte("crash-secret-sentinel"))) {
					test.Fatalf("restore rollback changed applied postimage: %v %v", result, readErr)
				}
			})
		}
	}
}

func nativeApplyFixture(test *testing.T, present bool) (*Engine, string, string, []byte, *Plan, []byte) {
	test.Helper()
	engine, home, stateRoot, key := nativeTestFixture(test)
	base := []byte(`{"untouched":{"large":9007199254740993},"env":{"CUSTOM":"unselected-sentinel","ANTHROPIC_AUTH_TOKEN":"old-auth-sentinel"}}`)
	if present {
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), base, 0o644); err != nil {
			test.Fatal(err)
		}
	}
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	secret, _ := NewSecretMaterial([]byte("selected-secret-sentinel"))
	defer secret.Destroy()
	if result, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret}); err != nil || result.Status != "AppliedOnDisk" {
		test.Fatalf("apply fixture: %v %v", result, err)
	}
	return engine, home, stateRoot, key, plan, base
}

func TestNativeRestorePreviewConfirmationRestartAndAbsent(test *testing.T) {
	for _, present := range []bool{true, false} {
		test.Run(map[bool]string{true: "existing", false: "absent"}[present], func(test *testing.T) {
			engine, home, stateRoot, key, applied, base := nativeApplyFixture(test, present)
			if err := engine.Close(); err != nil {
				test.Fatal(err)
			}
			reopened, err := Open(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal(err)
			}
			defer reopened.Close()
			plan, err := reopened.PreviewRestore(test.Context(), applied.OperationID())
			if err != nil || plan.OperationID() == applied.OperationID() || plan.View().Kind != "restore" {
				test.Fatalf("restore preview: %v", err)
			}
			if _, err := reopened.Restore(test.Context(), plan); !errors.Is(err, ErrUnconfirmed) {
				test.Fatal("historical ID bypassed confirmation")
			}
			result, err := reopened.Restore(test.Context(), plan.Confirm())
			if err != nil || result.Status != "Restored" {
				test.Fatalf("restore: %v %v", result, err)
			}
			actual, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
			if present && (err != nil || !bytes.Equal(actual, base)) || !present && !errors.Is(err, os.ErrNotExist) {
				test.Fatal("restore did not preserve preimage/absence")
			}
			if present {
				info, _ := os.Stat(filepath.Join(home, ".claude", "settings.json"))
				if info.Mode().Perm() != 0o600 {
					test.Fatal("restore widened old permissions")
				}
			}
			if _, err := reopened.PreviewRestore(test.Context(), applied.OperationID()); !errors.Is(err, ErrConflict) {
				test.Fatal("restored source remained active")
			}
			status, err := reopened.Status(test.Context(), applied.OperationID())
			if err != nil || status.Status != "Restored" {
				test.Fatal("restored mapping absent")
			}
			if err := reopened.run(test.Context(), func(inventory *nativeInventory) error {
				current := inventory.selections[selectionKey(plan.private().data.Selection)]
				if current.SelectionRevision != plan.private().data.Selection.SelectionRevision+1 || current.PolicyRevision != plan.private().data.Selection.PolicyRevision+1 || current.AutoApply {
					test.Fatal("restore did not pause policy monotonically")
				}
				return nil
			}); err != nil {
				test.Fatal(err)
			}
		})
	}
}

func TestNativeRestoreRejectsExternalEditAndPlanChange(test *testing.T) {
	engine, home, _, _, applied, _ := nativeApplyFixture(test, true)
	plan, err := engine.PreviewRestore(test.Context(), applied.OperationID())
	if err != nil {
		test.Fatal(err)
	}
	target := filepath.Join(home, ".claude", "settings.json")
	outside := []byte(`{"external":"new-user-edit"}`)
	if err := os.WriteFile(target, outside, 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := engine.Restore(test.Context(), plan.Confirm()); !errors.Is(err, ErrConflict) {
		test.Fatal("restore overwrote a post-preview edit")
	}
	if actual, _ := os.ReadFile(target); !bytes.Equal(actual, outside) {
		test.Fatal("external edit lost")
	}
	if _, err := engine.PreviewRestore(test.Context(), applied.OperationID()); !errors.Is(err, ErrConflict) {
		test.Fatal("blind historical restore")
	}
}
