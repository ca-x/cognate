//go:build linux

package configapply

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"golang.org/x/sys/unix"
)

func TestNativeTargetRejectsLinksFIFOAndUnsafePermissions(test *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory", "writable", "oversize", "acl"} {
		test.Run(kind, func(test *testing.T) {
			engine, home, _, _ := nativeTestFixture(test)
			target := filepath.Join(home, ".claude", "settings.json")
			outside := filepath.Join(test.TempDir(), "outside")
			original := []byte(`{"external":true}`)
			if err := os.WriteFile(outside, original, 0o600); err != nil {
				test.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(outside, target)
			case "hardlink":
				err = os.Link(outside, target)
			case "fifo":
				err = unix.Mkfifo(target, 0o600)
			case "directory":
				err = os.Mkdir(target, 0o700)
			case "writable":
				err = os.WriteFile(target, []byte("{}"), 0o600)
				if err == nil {
					err = os.Chmod(target, 0o666)
				}
			case "oversize":
				err = os.WriteFile(target, bytes.Repeat([]byte("x"), agentconfig.MaxConfigBytes+1), 0o600)
			case "acl":
				err = os.WriteFile(target, []byte("{}"), 0o600)
				if err == nil {
					acl := binary.LittleEndian.AppendUint32(nil, 2)
					for _, entry := range []struct {
						tag, permissions uint16
						owner            uint32
					}{{1, 6, ^uint32(0)}, {2, 4, uint32(os.Geteuid() + 1)}, {4, 0, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)}} {
						acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
						acl = binary.LittleEndian.AppendUint16(acl, entry.permissions)
						acl = binary.LittleEndian.AppendUint32(acl, entry.owner)
					}
					err = unix.Setxattr(target, "system.posix_acl_access", acl, 0)
				}
			}
			if err != nil {
				test.Fatal(err)
			}
			if plan, err := engine.Preview(test.Context(), nativeTestSelection(test)); plan != nil || err == nil {
				test.Fatal("unsafe target admitted")
			}
			if actual, err := os.ReadFile(outside); err != nil || !bytes.Equal(actual, original) {
				test.Fatal("outside object changed")
			}
		})
	}
}

func TestNativeAbsentClaudeDirectoryNeverCreated(test *testing.T) {
	engine, home, _, _ := nativeTestFixture(test)
	if err := os.Remove(filepath.Join(home, ".claude")); err != nil {
		test.Fatal(err)
	}
	if _, err := engine.Preview(test.Context(), nativeTestSelection(test)); !errors.Is(err, ErrTargetAbsent) {
		test.Fatalf("missing target: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("agent directory created")
	}
}

func TestNativeCapturePreservesExternalEntriesAndSameInodeEdits(test *testing.T) {
	for _, scenario := range []string{"post-preview-edit", "pre-capture-edit", "holding-fd-edit", "new-entry-after-capture", "parent-replacement"} {
		test.Run(scenario, func(test *testing.T) {
			engine, home, _, _ := nativeTestFixture(test)
			parent := filepath.Join(home, ".claude")
			target := filepath.Join(parent, "settings.json")
			base := []byte(`{"original":"before"}`)
			external := []byte(`{"external":"preserve"}`)
			if err := os.WriteFile(target, base, 0o600); err != nil {
				test.Fatal(err)
			}
			writer, err := os.OpenFile(target, os.O_WRONLY, 0)
			if err != nil {
				test.Fatal(err)
			}
			defer writer.Close()
			plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
			if err != nil {
				test.Fatal(err)
			}
			if scenario == "post-preview-edit" {
				if err := os.WriteFile(target, external, 0o600); err != nil {
					test.Fatal(err)
				}
			}
			engine.private().fault = func(phase string) error {
				if scenario == "pre-capture-edit" && phase == "reserve-intent-durable" {
					return os.WriteFile(target, external, 0o600)
				}
				if scenario == "holding-fd-edit" && phase == "capture-synced" {
					if err := writer.Truncate(0); err != nil {
						return err
					}
					_, err := writer.WriteAt(external, 0)
					return err
				}
				if scenario == "new-entry-after-capture" && phase == "capture-synced" {
					return os.WriteFile(target, external, 0o600)
				}
				if scenario == "parent-replacement" && phase == "prepared-durable" {
					if err := os.Rename(parent, filepath.Join(home, "old-parent")); err != nil {
						return err
					}
					if err := os.Mkdir(parent, 0o700); err != nil {
						return err
					}
					return os.WriteFile(target, external, 0o600)
				}
				return nil
			}
			secret, _ := NewSecretMaterial([]byte("never-overwrite"))
			defer secret.Destroy()
			result, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret})
			if err == nil || result.Status == "AppliedOnDisk" {
				test.Fatal("conflict claimed successful application")
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, external) {
				test.Fatal("external entry/content lost")
			}
		})
	}
}

func TestNativeBeforeActionBackupFailureAndTamperedStageRetained(test *testing.T) {
	for _, scenario := range []string{"backup-permission", "stage-substitution"} {
		test.Run(scenario, func(test *testing.T) {
			engine, home, stateRoot, _ := nativeTestFixture(test)
			target := filepath.Join(home, ".claude", "settings.json")
			base := []byte(`{"original":"preserve-before-backup"}`)
			if err := os.WriteFile(target, base, 0o600); err != nil {
				test.Fatal(err)
			}
			plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
			if err != nil {
				test.Fatal(err)
			}
			var unknownPath string
			unknown := []byte(`{"unknown":"must-not-delete"}`)
			engine.private().fault = func(phase string) error {
				if scenario == "backup-permission" && phase == "before-backup" {
					return os.Chmod(filepath.Join(stateRoot, "native-configuration-v1"), 0o500)
				}
				if scenario == "stage-substitution" && phase == "prepared-durable" {
					entries, err := os.ReadDir(filepath.Join(home, ".claude"))
					if err != nil {
						return err
					}
					for _, entry := range entries {
						if entry.IsDir() {
							unknownPath = filepath.Join(home, ".claude", entry.Name(), "stage")
							return os.WriteFile(unknownPath, unknown, 0o600)
						}
					}
					return errors.New("stage fixture absent")
				}
				return nil
			}
			secret, _ := NewSecretMaterial([]byte("no-unsafe-publish"))
			defer secret.Destroy()
			result, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret})
			if err == nil || result.Status == "AppliedOnDisk" {
				test.Fatal("unsafe preparation reported applied")
			}
			engine.private().fault = nil
			if scenario == "backup-permission" {
				if actual, err := os.ReadFile(target); err != nil || !bytes.Equal(actual, base) {
					test.Fatal("failed backup moved target")
				}
				if err := os.Chmod(filepath.Join(stateRoot, "native-configuration-v1"), 0o700); err != nil {
					test.Fatal(err)
				}
				if _, err := engine.Reconcile(test.Context()); err != nil {
					test.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrOutcomeUnknown) {
					test.Fatal("possible reservation misreported")
				}
				if _, err := engine.Reconcile(test.Context()); err != nil {
					test.Fatal(err)
				}
				if actual, err := os.ReadFile(unknownPath); err != nil || !bytes.Equal(actual, unknown) {
					test.Fatal("unknown stage evidence deleted")
				}
				status, err := engine.Status(test.Context(), plan.OperationID())
				if err != nil || !status.NeedsAttention {
					test.Fatal("tampered residue not exposed safely")
				}
			}
		})
	}
}
