//go:build linux

package configapply

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/install"
	"golang.org/x/sys/unix"
)

func TestCurrentSelectionInputsAndLifecycle(test *testing.T) {
	engine, _, _, _ := nativeTestFixture(test)
	input := nativeTestSelection(test).private().SelectionInput
	for _, invalid := range []struct{ origin, owner, profile string }{
		{"http://example.invalid", input.Owner, input.Profile},
		{input.Origin + "/", input.Owner, input.Profile},
		{input.Origin + "?a=b", input.Owner, input.Profile},
		{"https://user@example.invalid", input.Owner, input.Profile},
		{input.Origin, "", input.Profile},
		{input.Origin, "owner/child", input.Profile},
		{input.Origin, input.Owner, "bad profile"},
	} {
		got, err := engine.CurrentSelection(test.Context(), invalid.origin, invalid.owner, invalid.profile)
		if !errors.Is(err, ErrSelection) || got != (SelectionState{}) {
			test.Fatal("invalid partition accepted")
		}
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	for _, closed := range []*Engine{nil, {}, engine} {
		got, err := closed.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
		if !errors.Is(err, ErrClosed) || got != (SelectionState{}) {
			test.Fatal("closed engine returned a selection")
		}
	}
	if err := engine.Close(); err != nil {
		test.Fatal("repeated close failed")
	}
}

func TestCurrentSelectionPartitionsAndRestore(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	input := nativeTestSelection(test).private().SelectionInput
	var stale *Plan
	for index, change := range []func(*SelectionInput){
		func(*SelectionInput) {},
		func(input *SelectionInput) { input.Origin = "https://other.invalid" },
		func(input *SelectionInput) { input.Owner = "other-owner" },
		func(input *SelectionInput) { input.Profile = "other-profile" },
	} {
		partition := cloneSelection(input)
		change(&partition)
		partition.OperationID = fmt.Sprintf("partition-%d", index)
		partition.ConfirmationRevision = uint64(index + 3)
		partition.PolicyRevision = uint64(index + 8)
		partition.DeviceID = fmt.Sprintf("device-%d", index)
		partitionPlan := currentSelectionPreview(test, engine, partition)
		if index == 0 {
			stale = partitionPlan
		}
		got, err := engine.CurrentSelection(test.Context(), partition.Origin, partition.Owner, partition.Profile)
		want := SelectionState{Exists: true, SelectionRevision: 1, ConfirmationRevision: partition.ConfirmationRevision, PolicyRevision: partition.PolicyRevision, DeviceID: partition.DeviceID}
		if err != nil || got != want {
			test.Fatal("partition value mismatch")
		}
	}
	input.SelectionRevision, input.ConfirmationRevision, input.PolicyRevision = 2, 5, 10
	input.DeviceID, input.OperationID = "replacement-device", "device-update"
	plan := currentSelectionPreview(test, engine, input)
	secret, err := NewSecretMaterial([]byte("synthetic-selection-material"))
	if err != nil {
		test.Fatal(err)
	}
	defer secret.Destroy()
	if _, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret}); err != nil {
		test.Fatal(err)
	}
	restore, err := engine.PreviewRestore(test.Context(), plan.OperationID())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := engine.Restore(test.Context(), restore.Confirm()); err != nil {
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
	before := currentSelectionBusinessFiles(test, home, stateRoot)
	got, err := reopened.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
	want := SelectionState{Exists: true, SelectionRevision: 3, ConfirmationRevision: 5, PolicyRevision: 11, DeviceID: "replacement-device"}
	if err != nil || got != want {
		test.Fatal("restore pause counters or same-partition device lost")
	}
	if !maps.Equal(before, currentSelectionBusinessFiles(test, home, stateRoot)) {
		test.Fatal("getter changed business files")
	}
	if _, err := reopened.Apply(test.Context(), stale.Confirm(), nil); !errors.Is(err, ErrConflict) {
		test.Fatal("getter bypassed original apply CAS")
	}
}

func currentSelectionBusinessFiles(test *testing.T, home, stateRoot string) map[string][32]byte {
	test.Helper()
	result := make(map[string][32]byte)
	for _, root := range []string{filepath.Join(home, ".claude"), filepath.Join(stateRoot, "native-configuration-root.v1"), filepath.Join(stateRoot, "native-configuration-v1")} {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			data := []byte(info.Mode().String())
			if !entry.IsDir() {
				contents, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				data = append(data, contents...)
			}
			result[path] = sha256.Sum256(data)
			clear(data)
			return nil
		}); err != nil {
			test.Fatal(err)
		}
	}
	return result
}

type currentSelectionCheckContext struct {
	context.Context
	onCheck      func()
	afterRelease bool
	once         sync.Once
}

func (ctx *currentSelectionCheckContext) Err() error {
	var callers [24]uintptr
	frames := runtime.CallersFrames(callers[:runtime.Callers(2, callers[:])])
	target := "github.com/ca-x/cognate/internal/configapply.(*Engine).CurrentSelection.func1"
	if ctx.afterRelease {
		target = "github.com/ca-x/cognate/internal/configapply.(*Engine).CurrentSelection"
	}
	frame, _ := frames.Next()
	if frame.Function == target {
		ctx.once.Do(ctx.onCheck)
	}
	return ctx.Context.Err()
}

func TestCurrentSelectionReleaseFailure(test *testing.T) {
	engine, _, stateRoot, _ := nativeTestFixture(test)
	input := nativeTestSelection(test).private().SelectionInput
	currentSelectionPreview(test, engine, input)
	faulted := false
	ctx := &currentSelectionCheckContext{Context: test.Context(), onCheck: func() {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			test.Fatal(err)
		}
		for _, entry := range entries {
			path, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
			if err != nil || path != filepath.Join(stateRoot, "client.db.install.lock") {
				continue
			}
			descriptor, err := strconv.Atoi(entry.Name())
			if err != nil {
				test.Fatal(err)
			}
			replacement, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
			if err != nil {
				test.Fatal(err)
			}
			err = unix.Dup3(replacement, descriptor, unix.O_CLOEXEC)
			closeErr := unix.Close(replacement)
			if err != nil || closeErr != nil {
				test.Fatal("real release descriptor fixture failed")
			}
			if !errors.Is(unix.Flock(descriptor, unix.LOCK_UN), unix.EBADF) {
				test.Fatal("release fixture did not produce actual bad-descriptor failure")
			}
			faulted = true
			break
		}
	}}
	got, err := engine.CurrentSelection(ctx, input.Origin, input.Owner, input.Profile)
	if !faulted || !errors.Is(err, ErrOutcomeUnknown) || got != (SelectionState{}) {
		test.Fatal("real session release failure published candidate")
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
}

func TestCurrentSelectionCancellationAndClose(test *testing.T) {
	for _, afterRelease := range []bool{false, true} {
		name := "after-candidate"
		if afterRelease {
			name = "after-release"
		}
		test.Run(name, func(test *testing.T) {
			engine, _, _, _ := nativeTestFixture(test)
			input := nativeTestSelection(test).private().SelectionInput
			currentSelectionPreview(test, engine, input)
			base, cancel := context.WithCancel(test.Context())
			defer cancel()
			checked := false
			ctx := &currentSelectionCheckContext{Context: base, afterRelease: afterRelease, onCheck: func() { checked = true; cancel() }}
			got, err := engine.CurrentSelection(ctx, input.Origin, input.Owner, input.Profile)
			if !checked || !errors.Is(err, context.Canceled) || got != (SelectionState{}) {
				test.Fatal("canceled query published candidate")
			}
		})
	}
	test.Run("mutex-wait", func(test *testing.T) {
		engine, _, _, _ := nativeTestFixture(test)
		input := nativeTestSelection(test).private().SelectionInput
		engine.private().mutex.Lock()
		ctx, cancel := context.WithTimeout(test.Context(), 30*time.Millisecond)
		defer cancel()
		got, err := engine.CurrentSelection(ctx, input.Origin, input.Owner, input.Profile)
		engine.private().mutex.Unlock()
		if !errors.Is(err, context.DeadlineExceeded) || got != (SelectionState{}) {
			test.Fatal("mutex wait ignored cancellation")
		}
	})
	test.Run("close-drain", func(test *testing.T) {
		engine, _, _, _ := nativeTestFixture(test)
		input := nativeTestSelection(test).private().SelectionInput
		currentSelectionPreview(test, engine, input)
		reached, release := make(chan struct{}), make(chan struct{})
		ctx := &currentSelectionCheckContext{Context: test.Context(), onCheck: func() { close(reached); <-release }}
		read := make(chan error, 1)
		go func() {
			got, err := engine.CurrentSelection(ctx, input.Origin, input.Owner, input.Profile)
			if err == nil && !got.Exists {
				err = errors.New("missing candidate")
			}
			read <- err
		}()
		select {
		case <-reached:
		case <-time.After(5 * time.Second):
			close(release)
			test.Fatal("getter barrier not reached")
		}
		closed := make(chan error, 1)
		go func() { closed <- engine.Close() }()
		select {
		case <-closed:
			close(release)
			test.Fatal("Close did not drain getter")
		case <-time.After(30 * time.Millisecond):
		}
		close(release)
		select {
		case err := <-read:
			if err != nil {
				test.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			test.Fatal("getter failed to drain")
		}
		select {
		case err := <-closed:
			if err != nil {
				test.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			test.Fatal("Close failed to drain")
		}
	})
	test.Run("caller-discards-on-close-error", func(test *testing.T) {
		engine, _, _, _ := nativeTestFixture(test)
		input := nativeTestSelection(test).private().SelectionInput
		currentSelectionPreview(test, engine, input)
		var delivered SelectionState
		candidate, err := engine.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
		if err != nil || !candidate.Exists {
			test.Fatal("missing staged result")
		}
		if err := engine.private().vault.file.Close(); err != nil {
			test.Fatal(err)
		}
		closeErr := engine.Close()
		if closeErr == nil {
			delivered = candidate
		}
		if !errors.Is(closeErr, ErrOutcomeUnknown) || delivered != (SelectionState{}) {
			test.Fatal("caller published a result after physical Close failed")
		}
	})
}

func TestCurrentSelectionProcessHelper(test *testing.T) {
	mode, stateRoot := os.Getenv("COGNATE_CURRENT_SELECTION_CHILD"), os.Getenv("COGNATE_CURRENT_SELECTION_STATE")
	if mode == "" {
		return
	}
	if mode == "lock" {
		session, err := install.OpenSession(test.Context(), stateRoot)
		if err != nil {
			test.Fatal(err)
		}
		fmt.Println("LOCKED")
		_, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		if err := errors.Join(readErr, session.Close()); err != nil {
			test.Fatal(err)
		}
		return
	}
	if mode != "update" {
		test.Fatal("unknown isolated helper")
	}
	engine, err := Open(test.Context(), os.Getenv("HOME"), stateRoot, bytes.Repeat([]byte{0x5c}, 32))
	if err != nil {
		test.Fatal(err)
	}
	input := nativeTestSelection(test).private().SelectionInput
	input.SelectionRevision, input.ConfirmationRevision, input.PolicyRevision = 2, 11, 13
	input.OperationID, input.DeviceID = "child-operation", "child-device"
	currentSelectionPreview(test, engine, input)
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
}

func TestCurrentSelectionCrossProcessFreshness(test *testing.T) {
	engine, _, stateRoot, _ := nativeTestFixture(test)
	input := nativeTestSelection(test).private().SelectionInput
	oldPlan := currentSelectionPreview(test, engine, input)
	first, err := engine.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
	if err != nil || first.SelectionRevision != 1 {
		test.Fatal("initial selection missing")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCurrentSelectionProcessHelper$")
	command.Env = append(os.Environ(), "COGNATE_CURRENT_SELECTION_CHILD=update", "COGNATE_CURRENT_SELECTION_STATE="+stateRoot)
	if _, err := command.CombinedOutput(); err != nil {
		test.Fatal("isolated updater failed")
	}
	got, err := engine.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
	want := SelectionState{Exists: true, SelectionRevision: 2, ConfirmationRevision: 11, PolicyRevision: 13, DeviceID: "child-device"}
	if err != nil || got != want {
		test.Fatal("same Engine cached stale cross-process state")
	}
	if _, err := engine.Apply(test.Context(), oldPlan.Confirm(), nil); !errors.Is(err, ErrConflict) {
		test.Fatal("read snapshot bypassed stale apply CAS")
	}
}

func TestCurrentSelectionCrossProcessLockCancellation(test *testing.T) {
	engine, _, stateRoot, _ := nativeTestFixture(test)
	input := nativeTestSelection(test).private().SelectionInput
	currentSelectionPreview(test, engine, input)
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCurrentSelectionProcessHelper$")
	command.Env = append(os.Environ(), "COGNATE_CURRENT_SELECTION_CHILD=lock", "COGNATE_CURRENT_SELECTION_STATE="+stateRoot)
	stdout, err := command.StdoutPipe()
	if err != nil {
		test.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		test.Fatal(err)
	}
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = stdin.Close(); cancel(); _ = command.Wait() })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "LOCKED\n" {
		test.Fatal("child never held actual install lock")
	}
	blocked, stop := context.WithTimeout(test.Context(), 50*time.Millisecond)
	defer stop()
	got, err := engine.CurrentSelection(blocked, input.Origin, input.Owner, input.Profile)
	if !errors.Is(err, context.DeadlineExceeded) || got != (SelectionState{}) {
		test.Fatal("locked query did not cancel with zero result")
	}
	if _, err := fmt.Fprintln(stdin, "release"); err != nil {
		test.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		test.Fatal("lock child did not drain")
	}
	got, err = engine.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
	if err != nil || !got.Exists {
		test.Fatal("canceled query leaked mutex or session")
	}
}

func TestCurrentSelectionRootAndInventoryValidation(test *testing.T) {
	for _, scenario := range []string{"root-missing", "root-ciphertext", "root-key-id", "root-binding", "root-permissions", "root-hardlink", "root-symlink", "root-fifo", "root-capacity", "vault-replaced", "inventory-unknown", "inventory-ciphertext", "inventory-chain", "inventory-pending"} {
		test.Run(scenario, func(test *testing.T) {
			engine, _, stateRoot, _ := nativeTestFixture(test)
			input := nativeTestSelection(test).private().SelectionInput
			currentSelectionPreview(test, engine, input)
			root := filepath.Join(stateRoot, "native-configuration-root.v1")
			vault := filepath.Join(stateRoot, "native-configuration-v1")
			must := func(err error) {
				test.Helper()
				if err != nil {
					test.Fatal(err)
				}
			}
			switch scenario {
			case "root-missing":
				must(os.Remove(root))
			case "root-ciphertext", "root-key-id":
				data, err := os.ReadFile(root)
				must(err)
				var record encryptedRecord
				must(json.Unmarshal(data, &record))
				if scenario == "root-key-id" {
					record.KeyID[0] ^= 1
				} else {
					record.Ciphertext[0] ^= 1
				}
				data, err = json.Marshal(record)
				must(err)
				must(os.WriteFile(root, data, 0o600))
			case "root-binding":
				_, _, otherRoot, _ := nativeTestFixture(test)
				data, err := os.ReadFile(filepath.Join(otherRoot, "native-configuration-root.v1"))
				must(err)
				must(os.WriteFile(root, data, 0o600))
			case "root-permissions":
				must(os.Chmod(root, 0o644))
			case "root-hardlink":
				must(os.Link(root, filepath.Join(stateRoot, "linked-root")))
			case "root-symlink":
				must(os.Rename(root, root+"-old"))
				must(os.Symlink(root+"-old", root))
			case "root-fifo":
				must(os.Remove(root))
				must(unix.Mkfifo(root, 0o600))
			case "root-capacity":
				must(os.Truncate(root, nativeJournalLimit+1))
			case "vault-replaced":
				must(os.Rename(vault, vault+"-old"))
				must(os.Mkdir(vault, 0o700))
			case "inventory-unknown", "inventory-pending":
				name := "unknown.v1"
				if scenario == "inventory-pending" {
					name = "pending-invalid"
				}
				must(os.WriteFile(filepath.Join(vault, name), []byte("invalid"), 0o600))
			case "inventory-ciphertext":
				must(os.WriteFile(filepath.Join(vault, "vault.v1"), []byte("invalid"), 0o600))
			case "inventory-chain":
				input.SelectionRevision = 2
				input.OperationID = "operation-two"
				currentSelectionPreview(test, engine, input)
				entries, err := os.ReadDir(vault)
				must(err)
				removed := false
				for _, entry := range entries {
					data, err := os.ReadFile(filepath.Join(vault, entry.Name()))
					must(err)
					record, err := engine.openRecord(entry.Name(), data)
					must(err)
					if record.Type == "selection" && record.Selection.SelectionRevision == 1 {
						must(os.Remove(filepath.Join(vault, entry.Name())))
						removed = true
					}
				}
				if !removed {
					test.Fatal("chain fixture did not remove revision")
				}
			}
			got, err := engine.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
			if err == nil || got != (SelectionState{}) {
				test.Fatal("untrusted state returned a selection or authenticated absence")
			}
			if scenario == "root-missing" {
				if _, err := os.Lstat(root); !os.IsNotExist(err) {
					test.Fatal("getter recreated missing root")
				}
			}
		})
	}
	test.Run("wrong-key", func(test *testing.T) {
		engine, home, stateRoot, _ := nativeTestFixture(test)
		currentSelectionPreview(test, engine, nativeTestSelection(test).private().SelectionInput)
		if err := engine.Close(); err != nil {
			test.Fatal(err)
		}
		wrong, err := Open(test.Context(), home, stateRoot, bytes.Repeat([]byte{3}, 32))
		if wrong != nil {
			defer wrong.Close()
		}
		if err == nil || wrong != nil {
			test.Fatal("wrong key opened encrypted inventory")
		}
	})
}

func currentSelectionPreview(test *testing.T, engine *Engine, input SelectionInput) *Plan {
	test.Helper()
	selection, err := NewSelection(input)
	if err != nil {
		test.Fatal(err)
	}
	plan, err := engine.Preview(test.Context(), selection)
	if err != nil {
		test.Fatal(err)
	}
	return plan
}

func TestCurrentSelectionRoundTrip(test *testing.T) {
	engine, home, stateRoot, key := nativeTestFixture(test)
	input := nativeTestSelection(test).private().SelectionInput
	input.ConfirmationRevision = 7
	input.PolicyRevision = 9
	selection, err := NewSelection(input)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := engine.Preview(test.Context(), selection); err != nil {
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
	want := SelectionState{Exists: true, SelectionRevision: 1, ConfirmationRevision: 7, PolicyRevision: 9, DeviceID: "device-one"}
	got, err := reopened.CurrentSelection(test.Context(), input.Origin, input.Owner, input.Profile)
	if err != nil || got != want {
		test.Fatal("authenticated selection did not survive restart")
	}
	got, err = reopened.CurrentSelection(test.Context(), input.Origin, input.Owner, "absent")
	if err != nil || got != (SelectionState{}) {
		test.Fatal("authenticated absent partition not distinguished")
	}
}

func TestNativeSelectionRejectsImplicitAndUnsupportedAuthorization(test *testing.T) {
	base := nativeTestSelection(test).private().SelectionInput
	for _, scenario := range []struct {
		name   string
		mutate func(*SelectionInput)
		want   error
	}{
		{"not-selected", func(input *SelectionInput) { input.Selected = false }, ErrNotSelected},
		{"project", func(input *SelectionInput) { input.Scope = "project" }, ErrSelection},
		{"override", func(input *SelectionInput) { input.ConfigRootOverride = true }, ErrSelection},
		{"auto-not-authorized", func(input *SelectionInput) { input.AutoApply = true }, ErrSelection},
		{"http", func(input *SelectionInput) { input.Origin = "http://example.invalid" }, ErrSelection},
		{"duplicate-capability", func(input *SelectionInput) {
			input.RequiredCapabilities = append(input.RequiredCapabilities, agentconfig.ClaudeProviderSwitchV1)
		}, ErrSelection},
		{"missing-server-deadline", func(input *SelectionInput) { input.PreparationExpiresAt = time.Time{} }, ErrSelection},
		{"unknown-capability", func(input *SelectionInput) { input.RequiredCapabilities = []agentconfig.Capability{"future"} }, ErrSelection},
		{"unknown-target", func(input *SelectionInput) { input.Patch.Target = "other" }, ErrSelection},
		{"duplicate-slot", func(input *SelectionInput) { input.Patch.Operations[1] = input.Patch.Operations[0] }, ErrSelection},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			input := cloneSelection(base)
			scenario.mutate(&input)
			if _, err := NewSelection(input); !errors.Is(err, scenario.want) {
				test.Fatalf("authorization boundary: %v", err)
			}
		})
	}
}

func TestNativeSelectionAndPlanDeepCopy(test *testing.T) {
	engine, _, _, _ := nativeTestFixture(test)
	input := cloneSelection(nativeTestSelection(test).private().SelectionInput)
	input.Pack = &PackBinding{ID: "pack-one", Revision: 2, AttachmentRevision: 3}
	deadline := time.Now().Add(time.Minute)
	input.NativeSessionExpiresAt = &deadline
	selection, err := NewSelection(input)
	if err != nil {
		test.Fatal(err)
	}
	*input.Patch.Operations[3].Literal = "caller-mutation"
	input.Adapter.Manifest.Capabilities[0] = "caller-mutation"
	input.RequiredCapabilities[0] = "caller-mutation"
	input.Pack.Revision = 99
	*input.NativeSessionExpiresAt = time.Time{}
	plan, err := engine.Preview(test.Context(), selection)
	if err != nil {
		test.Fatal(err)
	}
	if *plan.private().data.Selection.Patch.Operations[3].Literal != "new-model" || plan.private().data.Selection.Pack.Revision != 2 || plan.private().data.Selection.NativeSessionExpiresAt.IsZero() {
		test.Fatal("caller mutated opaque selection")
	}
	confirmed := plan.Confirm()
	*confirmed.private().data.Selection.Patch.Operations[3].Literal = "changed"
	if *plan.private().data.Selection.Patch.Operations[3].Literal != "new-model" {
		test.Fatal("confirmation aliases original")
	}
	slots := plan.RequiredSlots()
	slots[0] = "changed"
	if plan.RequiredSlots()[0] != agentconfig.ClaudeAPIKeySecret {
		test.Fatal("getter aliases private plan")
	}
}
