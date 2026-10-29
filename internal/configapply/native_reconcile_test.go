//go:build linux

package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestNativeCrashChild(test *testing.T) {
	root := os.Getenv("COGNATE_NATIVE_B_FIXTURE")
	if root == "" {
		return
	}
	engine, err := Open(test.Context(), filepath.Join(root, "home"), filepath.Join(root, "state"), bytes.Repeat([]byte{0x5c}, 32))
	if err != nil {
		test.Fatal(err)
	}
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	secret, _ := NewSecretMaterial([]byte("crash-secret-sentinel"))
	if os.Getenv("COGNATE_NATIVE_B_RESTORE") == "1" {
		if _, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret}); err != nil {
			test.Fatal(err)
		}
		plan, err = engine.PreviewRestore(test.Context(), plan.OperationID())
		if err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "restore-id"), []byte(plan.OperationID()), 0o600); err != nil {
			test.Fatal(err)
		}
		engine.private().fault = func(phase string) error {
			if phase == os.Getenv("COGNATE_NATIVE_B_CRASH") {
				os.Exit(73)
			}
			return nil
		}
		_, err = engine.Restore(test.Context(), plan.Confirm())
		test.Fatalf("restore crash boundary not reached: %v", err)
	}
	engine.private().fault = func(phase string) error {
		if phase == os.Getenv("COGNATE_NATIVE_B_CRASH") {
			os.Exit(73)
		}
		return nil
	}
	_, err = engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret})
	test.Fatalf("crash boundary not reached: %v", err)
}

func TestNativeEveryDurableCrashRestarts(test *testing.T) {
	for _, present := range []bool{true, false} {
		phases := []string{"before-stage", "stage-synced", "before-backup", "backup-durable", "prepared-durable", "publish-intent-durable", "entry-published", "target-file-synced", "publication-synced", "result-durable", "cleanup-synced"}
		for _, phase := range []string{"Staging", "Prepared", "PublishIntent", "AppliedOnDisk"} {
			for _, boundary := range []string{"file-synced", "marker-file-synced", "marker-exchanged", "marker-directory-synced", "renamed", "directory-synced"} {
				phases = append(phases, "journal-operation-"+phase+"-"+boundary)
			}
		}
		if present {
			phases = append(phases, "reserve-intent-durable", "entry-captured", "capture-synced")
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
				base := []byte(`{"before":"old-state-sentinel"}`)
				target := filepath.Join(home, ".claude", "settings.json")
				if present {
					if err := os.WriteFile(target, base, 0o600); err != nil {
						test.Fatal(err)
					}
				}
				command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestNativeCrashChild$", "-test.count=1")
				command.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "xdg"), "XDG_STATE_HOME="+stateRoot, "CLAUDE_CONFIG_DIR=", "COGNATE_NATIVE_B_FIXTURE="+root, "COGNATE_NATIVE_B_CRASH="+phase)
				output, err := command.CombinedOutput()
				exit, ok := errors.AsType[*exec.ExitError](err)
				if !ok || exit.ExitCode() != 73 {
					test.Fatalf("not a real crash boundary: %s %v", output, err)
				}
				engine, err := Open(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x5c}, 32))
				if err != nil {
					test.Fatalf("restart: %v", err)
				}
				defer engine.Close()
				if _, err := engine.Reconcile(test.Context()); err != nil {
					test.Fatal(err)
				}
				status, err := engine.Status(test.Context(), "operation-one")
				if err != nil {
					test.Fatal(err)
				}
				actual, readErr := os.ReadFile(target)
				published := phase == "entry-published" || phase == "target-file-synced" || phase == "publication-synced" || phase == "result-durable" || phase == "cleanup-synced" || strings.HasPrefix(phase, "journal-operation-AppliedOnDisk-")
				if published {
					if status.Status != "AppliedOnDisk" || readErr != nil || !bytes.Contains(actual, []byte("crash-secret-sentinel")) {
						test.Fatalf("published result misclassified: %v %v", status, readErr)
					}
				} else if present {
					if readErr != nil || !bytes.Equal(actual, base) || status.Status != "Cancelled" && status.Status != "RolledBack" {
						test.Fatalf("base not recovered: %v %v", status, readErr)
					}
				} else if !errors.Is(readErr, os.ErrNotExist) || status.Status != "Cancelled" {
					test.Fatalf("absent base not preserved: %v %v", status, readErr)
				}
				entries, err := os.ReadDir(filepath.Join(home, ".claude"))
				if err != nil {
					test.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "cognate-") {
						test.Fatal("owned stage/holding left after successful recovery")
					}
				}
			})
		}
	}
}

func TestNativeCancellationBeforeAndAfterReservation(test *testing.T) {
	for _, phase := range []string{"before-backup", "reserve-intent-durable", "entry-captured", "entry-published", "cleanup-synced"} {
		test.Run(phase, func(test *testing.T) {
			engine, home, _, _ := nativeTestFixture(test)
			target := filepath.Join(home, ".claude", "settings.json")
			base := []byte(`{"before":true}`)
			if err := os.WriteFile(target, base, 0o600); err != nil {
				test.Fatal(err)
			}
			plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			engine.private().fault = func(name string) error {
				if name == phase {
					cancel()
				}
				return nil
			}
			secret, _ := NewSecretMaterial([]byte("cancel-secret"))
			defer secret.Destroy()
			result, err := engine.Apply(ctx, plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret})
			if phase == "before-backup" {
				if !errors.Is(err, context.Canceled) || result.Status == "AppliedOnDisk" {
					test.Fatal("pre-action cancel misclassified")
				}
			} else if !errors.Is(err, ErrOutcomeUnknown) || result.Status != "OutcomeUnknown" || result.OperationID != plan.OperationID() {
				test.Fatalf("post-intent cancel misclassified: %v %v", result, err)
			}
			engine.private().fault = nil
			if _, err := engine.Reconcile(test.Context()); err != nil {
				test.Fatal(err)
			}
		})
	}
}

func TestNativeCrashBeforeIdentityRecordPreservesUnknownStaging(test *testing.T) {
	for _, phase := range []string{"transaction-created", "stage-file-synced", "capacity-file-synced", "vault-capacity-file-synced"} {
		test.Run(phase, func(test *testing.T) {
			root := test.TempDir()
			home, stateRoot := filepath.Join(root, "home"), filepath.Join(root, "state")
			for _, directory := range []string{home, stateRoot, filepath.Join(home, ".claude")} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					test.Fatal(err)
				}
			}
			base := []byte(`{"before":"unmodified"}`)
			target := filepath.Join(home, ".claude", "settings.json")
			if err := os.WriteFile(target, base, 0o600); err != nil {
				test.Fatal(err)
			}
			command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestNativeCrashChild$", "-test.count=1")
			command.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR=", "XDG_STATE_HOME="+stateRoot, "XDG_CONFIG_HOME="+filepath.Join(home, "xdg"), "COGNATE_NATIVE_B_FIXTURE="+root, "COGNATE_NATIVE_B_CRASH="+phase)
			output, err := command.CombinedOutput()
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != 73 {
				test.Fatalf("staging boundary not reached: %s %v", output, err)
			}
			engine, err := Open(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x5c}, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer engine.Close()
			result, err := engine.Status(test.Context(), "operation-one")
			if err != nil || !result.NeedsAttention {
				test.Fatal("unrecorded identity was guessed or discarded")
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, base) {
				test.Fatal("pre-reservation crash changed target")
			}
		})
	}
}
