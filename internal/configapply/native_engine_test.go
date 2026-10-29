//go:build linux

package configapply

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/state"
)

func TestNativeOpaquePrivateCarriers(test *testing.T) {
	engine, home, _, _ := nativeTestFixture(test)
	base := []byte(`{"env":{"CUSTOM":"private-base-preimage"}}`)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), base, 0o600); err != nil {
		test.Fatal("private base fixture")
	}
	input := nativeTestSelection(test).private().SelectionInput
	input.Owner = "private-carrier-owner"
	selection, err := NewSelection(input)
	if err != nil {
		test.Fatal("private selection fixture")
	}
	plan, err := engine.Preview(test.Context(), selection)
	if err != nil {
		test.Fatal("private plan fixture")
	}
	digest := sha256.Sum256(base)
	markers := []string{input.Owner, engine.private().statePath, string(base), fmt.Sprintf("%v", digest), fmt.Sprintf("%#v", digest)}
	for _, value := range [][]byte{engine.private().keys.intent, engine.private().keys.journal, engine.private().keys.backup, digest[:]} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%x", "%X", "%s", "%q"} {
			markers = append(markers, fmt.Sprintf(verb, value))
		}
		markers = append(markers, hex.EncodeToString(value), base64.StdEncoding.EncodeToString(value), base64.RawStdEncoding.EncodeToString(value))
	}
	values := []any{*engine, engine, *plan, plan, selection, &selection,
		struct{ hidden Engine }{*engine}, struct{ hidden *Engine }{engine},
		struct{ hidden Plan }{*plan}, struct{ hidden *Plan }{plan},
		struct{ hidden Selection }{selection}, struct{ hidden *Selection }{&selection},
		struct{ hidden any }{struct{ hidden Plan }{*plan}},
		map[string]any{"value": struct{ hidden Engine }{*engine}},
		[]any{struct{ hidden Selection }{selection}},
	}
	check := func(test *testing.T, output string) {
		test.Helper()
		for _, marker := range markers {
			if strings.Contains(output, marker) {
				test.Fatal("opaque carrier exposed private data or reversible key/hash")
			}
		}
	}
	for index, value := range values {
		test.Run(fmt.Sprintf("carrier-%d", index), func(test *testing.T) {
			for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%x", "%X", "%s", "%q", "%b", "%o", "%f", "%e", "%g", "%t", "%c", "%U", "%p", "%#080.64v", "%#080.64s"} {
				check(test, fmt.Sprintf(verb, value))
			}
			for _, handler := range []func(*bytes.Buffer) slog.Handler{
				func(output *bytes.Buffer) slog.Handler { return slog.NewTextHandler(output, nil) },
				func(output *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(output, nil) },
			} {
				var output bytes.Buffer
				slog.New(handler(&output)).Info("safe-event", "value", value)
				check(test, output.String())
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				check(test, err.Error())
				if index < 6 {
					test.Fatal("public opaque JSON serialization failed")
				}
			}
			check(test, string(encoded))
		})
	}
}

func TestNativeSharedLockChild(test *testing.T) {
	stateRoot := os.Getenv("COGNATE_NATIVE_B_LOCK")
	if stateRoot == "" {
		return
	}
	session, err := install.OpenSession(test.Context(), stateRoot)
	if err != nil {
		test.Fatal(err)
	}
	defer session.Close()
	fmt.Println("LOCKED")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		test.Fatal("fixture release failed")
	}
}

func TestNativeSharedProcessLockPendingAndIdle(test *testing.T) {
	engine, _, stateRoot, _ := nativeTestFixture(test)
	idle, err := install.OpenSession(test.Context(), stateRoot)
	if err != nil {
		test.Fatal("idle engine retained installation lock")
	}
	if err := idle.Close(); err != nil {
		test.Fatal(err)
	}
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestNativeSharedLockChild$", "-test.count=1")
	command.Env = append(os.Environ(), "COGNATE_NATIVE_B_LOCK="+stateRoot)
	input, err := command.StdinPipe()
	if err != nil {
		test.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		test.Fatal(err)
	}
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		_ = input.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "LOCKED\n" {
		test.Fatal("child did not hold real canonical lock")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 80*time.Millisecond)
	defer cancel()
	if _, err := engine.Preview(ctx, nativeTestSelection(test)); !errors.Is(err, context.DeadlineExceeded) {
		test.Fatalf("shared process lock bypassed: %v", err)
	}
	if _, err := fmt.Fprintln(input, "release"); err != nil {
		test.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		test.Fatal(err)
	}
	session, err := install.OpenSession(test.Context(), stateRoot)
	if err != nil {
		test.Fatal(err)
	}
	operation := state.OperationPlan{ID: "pending-skill", Action: "install", Slug: "fixture", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(stateRoot, "skill-fixture"), Kind: "canonical"}}}
	if err := session.BeginOperation(test.Context(), operation); err != nil {
		test.Fatal(err)
	}
	if err := session.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := engine.Preview(test.Context(), nativeTestSelection(test)); !errors.Is(err, ErrPending) {
		test.Fatalf("pending skill operation ignored: %v", err)
	}
}

func TestNativeLoginExactSlotsIdempotencyAndSafeSummary(test *testing.T) {
	for _, mode := range []string{"native-login", "auth-token", "api-key"} {
		test.Run(mode, func(test *testing.T) {
			engine, home, _, _ := nativeTestFixture(test)
			base := []byte(`{"unrelated":9007199254740993,"env":{"ANTHROPIC_API_KEY":"old","CUSTOM":"kept"}}`)
			target := filepath.Join(home, ".claude", "settings.json")
			if err := os.WriteFile(target, base, 0o600); err != nil {
				test.Fatal(err)
			}
			input := cloneSelection(nativeTestSelection(test).private().SelectionInput)
			input.Patch.Operations[0].Action, input.Patch.Operations[0].Reference = agentconfig.ClaudeRemove, nil
			input.Patch.Operations[3].Action, input.Patch.Operations[3].Literal = agentconfig.ClaudeRemove, nil
			var material map[agentconfig.ClaudeSecretSlot]SecretMaterial
			if mode != "native-login" {
				index, slot := 0, agentconfig.ClaudeAPIKeySecret
				if mode == "auth-token" {
					index, slot = 1, agentconfig.ClaudeAuthTokenSecret
				}
				input.Patch.Operations[index].Action, input.Patch.Operations[index].Reference = agentconfig.ClaudeSetSelectedSecret, new(slot)
				secret, _ := NewSecretMaterial([]byte("exact-selected-sentinel"))
				defer secret.Destroy()
				material = map[agentconfig.ClaudeSecretSlot]SecretMaterial{slot: secret}
			}
			selection, err := NewSelection(input)
			if err != nil {
				test.Fatal(err)
			}
			plan, err := engine.Preview(test.Context(), selection)
			if err != nil {
				test.Fatal(err)
			}
			view := plan.View()
			if len(view.Operations) != 7 || view.Readiness == "" || !view.RecoverableAbsentWindow {
				test.Fatal("missing safe confirmation actions")
			}
			view.Operations[0].Action = "caller-mutation"
			if plan.View().Operations[0].Action == "caller-mutation" {
				test.Fatal("summary aliases immutable plan")
			}
			extra, _ := NewSecretMaterial([]byte("unrequested"))
			defer extra.Destroy()
			if _, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{"unrequested": extra}); !errors.Is(err, ErrInvalidSecret) {
				test.Fatal("non-exact slots accepted")
			}
			if actual, _ := os.ReadFile(target); !bytes.Equal(actual, base) {
				test.Fatal("invalid slots changed target")
			}
			result, err := engine.Apply(test.Context(), plan.Confirm(), material)
			if err != nil || result.Status != "AppliedOnDisk" {
				test.Fatalf("mode apply: %v %v", result, err)
			}
			before, err := os.Stat(target)
			if err != nil {
				test.Fatal(err)
			}
			result, err = engine.Apply(test.Context(), plan.Confirm(), nil)
			after, statErr := os.Stat(target)
			if err != nil || statErr != nil || result.Status != "AppliedOnDisk" || !os.SameFile(before, after) {
				test.Fatal("idempotent apply rewrote or required secrets again")
			}
			actual, _ := os.ReadFile(target)
			if !bytes.Contains(actual, []byte(`"CUSTOM":"kept"`)) || mode == "native-login" && bytes.Contains(actual, []byte("ANTHROPIC_")) {
				test.Fatal("mode projection changed unselected content or kept old managed credential")
			}
		})
	}
}

func nativeTestSelection(test *testing.T) Selection {
	test.Helper()
	operations := make([]agentconfig.ClaudeProviderPatchOperation, 0, len(claudeSlots))
	for _, slot := range claudeSlots {
		operations = append(operations, agentconfig.ClaudeProviderPatchOperation{Slot: slot, Action: agentconfig.ClaudeRemove})
	}
	operations[0].Action = agentconfig.ClaudeSetSelectedSecret
	operations[0].Reference = new(agentconfig.ClaudeAPIKeySecret)
	operations[3].Action = agentconfig.ClaudeSetLiteral
	operations[3].Literal = new("new-model")
	selection, err := NewSelection(SelectionInput{Selected: true, Origin: "https://example.invalid", Owner: "owner-one", Profile: "default", DeviceID: "device-one", AssetID: "asset-one", SchemaVersion: 1, AssetRevision: 1, Adapter: agentconfig.Descriptor{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", AgentID: "claude", Target: agentconfig.ClaudeSettings, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1}}, DefinitionDigest: strings.Repeat("a", 64)}, RequiredCapabilities: []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1}, Patch: agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: operations}, SelectionRevision: 1, ConfirmationRevision: 1, OperationID: "operation-one", PreparationID: "preparation-one", PreparationPreparedAt: time.Now().UTC().Add(-time.Second), PreparationExpiresAt: time.Now().UTC().Add(4 * time.Minute), Scope: "user"})
	if err != nil {
		test.Fatal(err)
	}
	selection.private().PreparationPreparedAt = time.Now().UTC().Add(-time.Second)
	selection.private().PreparationExpiresAt = time.Now().UTC().Add(4 * time.Minute)
	return selection
}

func nativeTestFixture(test *testing.T) (*Engine, string, string, []byte) {
	test.Helper()
	root := test.TempDir()
	home, stateRoot := filepath.Join(root, "home"), filepath.Join(root, "state")
	for _, directory := range []string{home, stateRoot, filepath.Join(home, ".claude")} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			test.Fatal(err)
		}
	}
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	test.Setenv("XDG_STATE_HOME", stateRoot)
	test.Setenv("CLAUDE_CONFIG_DIR", "")
	key := bytes.Repeat([]byte{0x5c}, 32)
	engine, err := Open(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = engine.Close() })
	return engine, home, stateRoot, key
}

func TestNativeVerticalApplyRequiresExactConfirmation(test *testing.T) {
	engine, home, _, _ := nativeTestFixture(test)
	target := filepath.Join(home, ".claude", "settings.json")
	base := []byte("{\n  \"unrelated\":9007199254740993, \"env\":{\"ANTHROPIC_AUTH_TOKEN\":\"old-private\",\"CUSTOM\":\"keep\"}\n}\n")
	if err := os.WriteFile(target, base, 0o644); err != nil {
		test.Fatal(err)
	}
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatalf("selected safe preview: %v", err)
	}
	if !plan.View().TightenPermissions || !plan.View().RecoverableAbsentWindow {
		test.Fatal("missing permission/absent-window disclosure")
	}
	material, _ := NewSecretMaterial([]byte("new-private-sentinel"))
	defer material.Destroy()
	credentials := map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: material}
	if _, err := engine.Apply(test.Context(), plan, credentials); !errors.Is(err, ErrUnconfirmed) {
		test.Fatal("unconfirmed plan accepted")
	}
	if actual, _ := os.ReadFile(target); !bytes.Equal(actual, base) {
		test.Fatal("unconfirmed write")
	}
	result, err := engine.Apply(test.Context(), plan.Confirm(), credentials)
	if err != nil || result.Status != "AppliedOnDisk" {
		test.Fatalf("confirmed apply: %v %v", result, err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(actual, []byte("9007199254740993")) || !bytes.Contains(actual, []byte("new-private-sentinel")) || bytes.Contains(actual, []byte("old-private")) || !bytes.Contains(actual, []byte(`"CUSTOM":"keep"`)) {
		test.Fatal("incorrect native roundtrip")
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o600 {
		test.Fatal("published settings not private")
	}
}
