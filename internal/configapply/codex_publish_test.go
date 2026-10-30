package configapply

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func codexFixture(test *testing.T) (*CodexEngine, string, string, []byte) {
	test.Helper()
	if runtime.GOOS != "linux" {
		test.Skip("Linux publisher runtime gate")
	}
	root := test.TempDir()
	home, stateRoot := filepath.Join(root, "home"), filepath.Join(root, "state")
	for _, directory := range []string{home, stateRoot, filepath.Join(home, ".codex")} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			test.Fatal("fixture directory")
		}
	}
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	test.Setenv("XDG_STATE_HOME", stateRoot)
	key := bytes.Repeat([]byte{0x67}, 32)
	engine, err := OpenCodex(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = engine.Close() })
	return engine, home, stateRoot, key
}

func codexLiteral(test *testing.T, engine *CodexEngine, bodies [3][]byte, writes [3]bool) *codexOperation {
	test.Helper()
	state := engine.private()
	var operation *codexOperation
	err := state.withLock(test.Context(), func(ctx context.Context) error {
		target, images, originals, err := state.observeCodex()
		defer codexClearBodies(&originals)
		if err != nil {
			return err
		}
		defer target.close()
		base, ok := state.inventory.Selections[codexSelectionKey("https://synthetic.invalid", "fixture-owner", "fixture-profile")]
		if !ok {
			base = codexSelectionRecord{Origin: "https://synthetic.invalid", Owner: "fixture-owner", Profile: "fixture-profile", Device: state.inventory.Device}
		}
		operation = &codexOperation{ID: codexRandomID(), Owner: base.Owner, Origin: base.Origin, Profile: base.Profile, Asset: "fixture-asset", Binding: sha256.Sum256([]byte("private literal publisher fixture, NOT a qualified B plan")), Target: target.identity, Before: images, After: images, Write: writes, Base: base, State: CodexConsumeAttempted, Expires: time.Now().Add(2 * time.Minute).UnixNano()}
		for index := range writes {
			if writes[index] {
				operation.After[index] = nativeImage{Present: true, SHA256: sha256.Sum256(bodies[index])}
			}
		}
		return nil
	})
	if err != nil {
		test.Fatal(err)
	}
	return operation
}

func codexStageLiteral(test *testing.T, engine *CodexEngine, operation *codexOperation, bodies [3][]byte) {
	test.Helper()
	if err := engine.private().withLock(test.Context(), func(ctx context.Context) error { return engine.private().prepareCodexOperation(ctx, operation, bodies) }); err != nil {
		test.Fatal(err)
	}
}

func codexPublishLiteral(ctx context.Context, engine *CodexEngine, id string) (CodexResult, error) {
	var result CodexResult
	err := engine.private().withLock(ctx, func(ctx context.Context) error {
		operation := engine.private().inventory.Operations[id]
		if operation == nil {
			return ErrCodexInvalid
		}
		var err error
		result, err = engine.private().publishCodex(ctx, operation)
		return err
	})
	return result, err
}

func TestCodexPublishRealThreeRoleCAS(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	auth := []byte(`{"synthetic-login":"auth-must-stay-byte-identical"}`)
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), auth, 0o644); err != nil {
		test.Fatal(err)
	}
	bodies := [3][]byte{[]byte("provider_key = \"synthetic-private-key\"\n"), nil, []byte(`{"models":[{"slug":"fixture-model"}]}`)}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	result, err := codexPublishLiteral(test.Context(), engine, operation.ID)
	if err != nil || result.State != CodexCompleted {
		test.Fatalf("publisher did not complete: %v", err)
	}
	for index, name := range codexRoleNames {
		actual, err := os.ReadFile(filepath.Join(home, ".codex", name))
		if err != nil {
			test.Fatal("real target missing")
		}
		expected := bodies[index]
		if index == 1 {
			expected = auth
		}
		if !bytes.Equal(actual, expected) {
			test.Fatal("published bytes differ")
		}
		info, err := os.Stat(filepath.Join(home, ".codex", name))
		if err != nil {
			test.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if index == 1 {
			mode = 0o644
		}
		if info.Mode().Perm() != mode {
			test.Fatal("role permissions changed incorrectly")
		}
	}
}

func TestCodexPublishRejectsChangedAuthKeep(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	bodies := [3][]byte{[]byte("synthetic-config"), nil, []byte("synthetic-catalog")}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("later-auth"), 0o600); err != nil {
		test.Fatal(err)
	}
	result, err := codexPublishLiteral(test.Context(), engine, operation.ID)
	if !errors.Is(err, ErrCodexConflict) || result.State == CodexCompleted {
		test.Fatal("auth Keep was excluded from CAS")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("failed auth CAS wrote config")
	}
}

func TestCodexNonCooperatingWriterChild(test *testing.T) {
	if os.Getenv("COGNATE_D1_FOREIGN_CHILD") == "" {
		return
	}
	root := filepath.Join(os.Getenv("COGNATE_D1_HOME"), ".codex")
	stage := filepath.Join(root, "foreign-child-stage")
	if err := os.WriteFile(stage, []byte("foreign-child-data-must-be-preserved"), 0o600); err != nil {
		test.Fatal("foreign stage")
	}
	if err := os.Rename(stage, filepath.Join(root, "cognate-model-catalog.json")); err != nil {
		test.Fatal("foreign rename")
	}
}

func TestCodexDisplacedImageRacePreservesForeignAndStopsPointer(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
		if err := os.WriteFile(filepath.Join(home, ".codex", name), []byte("original"), 0o600); err != nil {
			test.Fatal(err)
		}
	}
	bodies := [3][]byte{[]byte("new-private-config"), nil, []byte("new-private-catalog")}
	op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, op, bodies)
	called := false
	engine.private().fault = func(label string) error {
		if label != "before-publish-2" {
			return nil
		}
		called = true
		ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexNonCooperatingWriterChild$", "-test.count=1")
		command.Env = append(os.Environ(), "COGNATE_D1_FOREIGN_CHILD=1", "COGNATE_D1_HOME="+home)
		if err := command.Run(); err != nil {
			test.Fatal("real non-cooperating writer failed")
		}
		return nil
	}
	result, err := codexPublishLiteral(test.Context(), engine, op.ID)
	if !called || !errors.Is(err, ErrCodexUnknown) || result.State == CodexCompleted {
		test.Fatal("displaced-image race was accepted")
	}
	current, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil || !bytes.Equal(current, []byte("original")) {
		test.Fatal("pointer advanced after foreign displacement")
	}
	foreign, err := os.ReadFile(filepath.Join(home, ".codex", op.StageNames[2]))
	if err != nil || !bytes.Equal(foreign, []byte("foreign-child-data-must-be-preserved")) {
		test.Fatal("foreign displaced file lost")
	}
	engine.private().fault = nil
	status, err := engine.StatusCodex(test.Context(), op.ID)
	if err != nil || status.State != CodexNeedsAttention || !status.NeedsAttention {
		test.Fatal("third-state displacement offered automatic recovery")
	}
	if _, err := engine.PreviewCodexRecovery(test.Context(), op.ID); err == nil {
		test.Fatal("foreign displacement offered recovery approval")
	}
}

func TestCodexDurabilityFaultNeverReportsCompleted(test *testing.T) {
	for _, label := range []string{"entry-published-2", "directory-synced-2", "entry-published-0", "directory-synced-0", "targets-durable"} {
		test.Run(label, func(test *testing.T) {
			engine, _, _, _ := codexFixture(test)
			bodies := [3][]byte{[]byte("config"), nil, []byte("catalog")}
			op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, op, bodies)
			engine.private().fault = func(point string) error {
				if point == label {
					return errors.New("synthetic durability fault")
				}
				return nil
			}
			result, err := codexPublishLiteral(test.Context(), engine, op.ID)
			if !errors.Is(err, ErrCodexUnknown) || result.State == CodexCompleted {
				test.Fatal("durability failure reported completed")
			}
		})
	}
}
