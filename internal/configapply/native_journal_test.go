//go:build linux

package configapply

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestNativeEncryptedJournalWrongKeyAADAndDefaultRedaction(test *testing.T) {
	engine, home, stateRoot, key, plan, _ := nativeApplyFixture(test, true)
	selection := nativeTestSelection(test)
	formatted := fmt.Sprintf("%v %#v %v %#v %v %#v", engine, engine, plan, plan, selection, selection)
	formatted += fmt.Sprintf("%v %#v", *engine, *engine)
	for _, value := range []string{"old-auth-sentinel", "selected-secret-sentinel", "unselected-sentinel", home, hex.EncodeToString(plan.private().data.Base.SHA256[:])} {
		if value != "" && bytes.Contains([]byte(formatted), []byte(value)) {
			test.Fatal("default wrapper disclosed private state")
		}
	}
	if err := filepath.WalkDir(stateRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, sentinel := range []string{"old-auth-sentinel", "selected-secret-sentinel", "unselected-sentinel"} {
			if bytes.Contains(data, []byte(sentinel)) {
				test.Fatal("plaintext in state/journal/backup")
			}
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	if reopened, err := Open(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x7f}, 32)); reopened != nil || !errors.Is(err, ErrStateInvalid) {
		if reopened != nil {
			_ = reopened.Close()
		}
		test.Fatal("wrong key replaced encrypted history")
	}
	vault := filepath.Join(stateRoot, "native-configuration-v1")
	marker := filepath.Join(vault, "vault.v1")
	original, err := os.ReadFile(marker)
	if err != nil {
		test.Fatal(err)
	}
	entries, _ := os.ReadDir(vault)
	var replacement []byte
	for _, entry := range entries {
		if entry.Name() != "vault.v1" {
			replacement, err = os.ReadFile(filepath.Join(vault, entry.Name()))
			break
		}
	}
	if err != nil || len(replacement) == 0 {
		test.Fatal("missing authenticated fixture")
	}
	if err := os.WriteFile(marker, replacement, 0o600); err != nil {
		test.Fatal(err)
	}
	if reopened, err := Open(test.Context(), home, stateRoot, key); reopened != nil || !errors.Is(err, ErrStateInvalid) {
		if reopened != nil {
			_ = reopened.Close()
		}
		test.Fatal("ciphertext identity swap accepted")
	}
	if err := os.WriteFile(marker, original, 0o600); err != nil {
		test.Fatal(err)
	}
	original[len(original)/2] ^= 1
	if err := os.WriteFile(marker, original, 0o600); err != nil {
		test.Fatal(err)
	}
	if reopened, err := Open(test.Context(), home, stateRoot, key); reopened != nil || !errors.Is(err, ErrStateInvalid) {
		if reopened != nil {
			_ = reopened.Close()
		}
		test.Fatal("tampered state accepted")
	}
}

func TestNativePreparedBackupTamperStopsBeforeReservation(test *testing.T) {
	engine, home, stateRoot, _ := nativeTestFixture(test)
	target := filepath.Join(home, ".claude", "settings.json")
	base := []byte(`{"before":"backup-must-be-durable"}`)
	if err := os.WriteFile(target, base, 0o600); err != nil {
		test.Fatal(err)
	}
	before, err := os.Stat(target)
	if err != nil {
		test.Fatal(err)
	}
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	engine.private().fault = func(phase string) error {
		if phase != "prepared-durable" {
			return nil
		}
		entries, err := os.ReadDir(filepath.Join(stateRoot, "native-configuration-v1"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if len(entry.Name()) > 7 && entry.Name()[:7] == "backup-" {
				return os.WriteFile(filepath.Join(stateRoot, "native-configuration-v1", entry.Name()), []byte("corrupt-backup-evidence"), 0o600)
			}
		}
		return errors.New("missing real backup fixture")
	}
	secret, _ := NewSecretMaterial([]byte("must-not-publish"))
	defer secret.Destroy()
	result, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret})
	if err == nil || result.Status == "AppliedOnDisk" {
		test.Fatal("tampered durable backup permitted publication")
	}
	after, statErr := os.Stat(target)
	actual, readErr := os.ReadFile(target)
	if statErr != nil || readErr != nil || !os.SameFile(before, after) || !bytes.Equal(actual, base) {
		test.Fatal("backup failure moved target before refusal")
	}
}

func TestNativeDurablePendingRecordRecovered(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	vault := filepath.Join(stateRoot, "native-configuration-v1")
	entries, err := os.ReadDir(vault)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) > 10 && entry.Name()[:10] == "operation-" {
			if err := os.Rename(filepath.Join(vault, entry.Name()), filepath.Join(vault, "pending-durable-test")); err != nil {
				test.Fatal(err)
			}
		}
	}
	reopened, err := Open(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatalf("durable authenticated pending record must recover: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.Reconcile(test.Context()); err != nil {
		test.Fatal(err)
	}
	result, err := reopened.Status(test.Context(), plan.OperationID())
	if err != nil || result.Status != "Cancelled" {
		test.Fatalf("recovery lost operation: %v %v", result, err)
	}
}

func TestNativeRecordDeletionCannotResetSelection(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	if _, err := engine.Preview(test.Context(), nativeTestSelection(test)); err != nil {
		test.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	vault := filepath.Join(stateRoot, "native-configuration-v1")
	entries, err := os.ReadDir(vault)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "vault.v1" {
			if err := os.Remove(filepath.Join(vault, entry.Name())); err != nil {
				test.Fatal(err)
			}
		}
	}
	reopened, err := Open(test.Context(), home, stateRoot, key)
	if reopened != nil {
		_ = reopened.Close()
	}
	if !errors.Is(err, ErrStateInvalid) {
		test.Fatalf("record deletion reset durable revision: %v", err)
	}
}

func TestNativeCancelledOperationRemainsCancelledAfterRestart(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := engine.Reconcile(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	result, err := reopened.Status(test.Context(), plan.OperationID())
	if err != nil || result.Status != "Cancelled" || result.NeedsAttention {
		test.Fatalf("cancelled file-free operation acquired phantom recovery: %v %v", result, err)
	}
}

func TestNativeDeletedVaultCannotResetRootBinding(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	if _, err := engine.Preview(test.Context(), nativeTestSelection(test)); err != nil {
		test.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stateRoot, "native-configuration-v1")); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), home, stateRoot, key)
	if reopened != nil {
		_ = reopened.Close()
	}
	if !errors.Is(err, ErrStateInvalid) {
		test.Fatal("deleted vault silently reinitialized revisions")
	}
}

func TestNativeEncryptedJournalAndBackupCapacityBeforeTargetAction(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	if err := engine.run(test.Context(), func(inventory *nativeInventory) error {
		operation := inventory.operations[plan.OperationID()]
		reached := false
		for range 512 {
			err := engine.saveOperation(operation, inventory, "Planned")
			if errors.Is(err, ErrCapacity) {
				reached = true
				break
			}
			if err != nil {
				return err
			}
		}
		if !reached || inventory.opBytes[plan.OperationID()] > nativeJournalLimit {
			test.Fatal("cumulative encrypted journal bytes escaped 1MiB bound")
		}
		oversize := nativeRecord{Version: 1, Type: "backup", ID: "oversized", Home: directoryBinding(engine.private().home.identity), State: directoryBinding(engine.private().state.identity), Vault: directoryBinding(engine.private().vault.identity), Selection: plan.private().data.Selection, Backup: &backupRecord{Data: bytes.Repeat([]byte("x"), nativeBackupLimit)}}
		defer clearBackup(oversize)
		encoded, err := engine.sealRecord(oversize)
		clear(encoded)
		if !errors.Is(err, ErrCapacity) {
			test.Fatal("encrypted backup ciphertext overhead escaped 512KiB bound")
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("capacity test unexpectedly touched target")
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatalf("capacity refusal corrupted existing records: %v", err)
	}
	defer reopened.Close()
	status, err := reopened.Status(test.Context(), plan.OperationID())
	if err != nil || status.Status != "Planned" {
		test.Fatal("capacity refusal lost last durable operation")
	}
}
