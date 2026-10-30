package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCodexObservedRestartRevalidatesSource(test *testing.T) {
	for _, changed := range []bool{false, true} {
		test.Run(strconv.FormatBool(changed), func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			var descriptor codexLaunchDescriptor
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				var err error
				descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
				return err
			}); err != nil {
				test.Fatal("restart observation fixture failed")
			}
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			operation.ProfileIdentity = codexNative0166Identity()
			operation.Observation = &descriptor
			codexStageLiteral(test, engine, operation, bodies)
			if err := engine.Close(); err != nil {
				test.Fatal("restart close failed")
			}
			if changed {
				if os.Mkdir(filepath.Join(launch.Directory, ".codex"), 0o700) != nil || os.WriteFile(filepath.Join(launch.Directory, ".codex", "config.toml"), []byte("model_context_window=4096\n"), 0o600) != nil {
					test.Fatal("restart source fixture failed")
				}
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("restart open failed")
			}
			defer reopened.Close()
			err = reopened.private().withLock(test.Context(), func(ctx context.Context) error {
				return reopened.private().validateCodexOperationObservation(ctx, reopened.private().inventory.Operations[operation.ID])
			})
			if changed {
				if !errors.Is(err, ErrCodexConflict) {
					test.Fatal("changed source accepted after restart")
				}
			} else if err != nil {
				test.Fatal("unchanged authenticated descriptor refused after restart")
			}
		})
	}
}

func TestCodexObservedPublicationCrashChild(test *testing.T) {
	if os.Getenv("COGNATE_Q2_CRASH") != "1" {
		return
	}
	engine, err := OpenCodex(test.Context(), os.Getenv("COGNATE_Q2_HOME"), os.Getenv("COGNATE_Q2_STATE"), bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("crash child open failed")
	}
	defer engine.Close()
	engine.private().fault = func(stage string) error {
		if stage == "directory-synced-2" {
			os.Exit(73)
		}
		return nil
	}
	err = engine.private().withLock(test.Context(), func(ctx context.Context) error {
		operation := engine.private().inventory.Operations[os.Getenv("COGNATE_Q2_OPERATION")]
		if err := engine.private().validateCodexOperationObservation(ctx, operation); err != nil {
			return err
		}
		_, err := engine.private().publishCodex(ctx, operation)
		return err
	})
	if err != nil {
		test.Fatal("crash child publication refused")
	}
	test.Fatal("real crash checkpoint not reached")
}

func TestCodexObservedCrashRecoveryAndRestore(test *testing.T) {
	for _, change := range []bool{false, true} {
		test.Run(strconv.FormatBool(change), func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			auth := []byte(`{"OPENAI_API_KEY":"SYNTHETIC_KEEP_API"}`)
			if os.WriteFile(filepath.Join(home, ".codex", "auth.json"), auth, 0o600) != nil {
				test.Fatal("auth fixture failed")
			}
			var descriptor codexLaunchDescriptor
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				var err error
				descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
				return err
			}); err != nil {
				test.Fatal("crash descriptor capture failed")
			}
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			operation.ProfileIdentity = codexNative0166Identity()
			operation.Observation = &descriptor
			codexStageLiteral(test, engine, operation, bodies)
			if engine.Close() != nil {
				test.Fatal("crash parent close failed")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexObservedPublicationCrashChild$", "-test.timeout=10s")
			child.Env = []string{"HOME=" + home, "TMPDIR=" + os.TempDir(), "PATH=/usr/bin:/bin", "COGNATE_Q2_CRASH=1", "COGNATE_Q2_HOME=" + home, "COGNATE_Q2_STATE=" + stateRoot, "COGNATE_Q2_OPERATION=" + operation.ID}
			err := child.Run()
			status, ok := errors.AsType[*exec.ExitError](err)
			if !ok || status.ExitCode() != 73 || ctx.Err() != nil {
				test.Fatal("child did not join at real publication crash")
			}
			if change {
				if os.Mkdir(filepath.Join(launch.Directory, ".codex"), 0o700) != nil || os.WriteFile(filepath.Join(launch.Directory, ".codex", "config.toml"), []byte("model_context_window=4096\n"), 0o600) != nil {
					test.Fatal("post-crash source mutation failed")
				}
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("post-crash open failed")
			}
			defer reopened.Close()
			plan, err := reopened.PreviewCodexRecovery(test.Context(), operation.ID)
			if change {
				if !errors.Is(err, ErrCodexConflict) {
					test.Fatal("restart source mutation did not fail closed")
				}
				if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("rejected recovery published config")
				}
				return
			}
			if err != nil {
				test.Fatal("legitimate pre/post role mix recovery refused")
			}
			defer plan.Destroy()
			plan, err = plan.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("recovery confirmation failed")
			}
			result, err := reopened.RecoverCodex(test.Context(), plan)
			if err != nil || result.State != CodexCompleted {
				test.Fatal("fresh authenticated recovery did not complete")
			}
			restore, err := reopened.PreviewCodexRestore(test.Context(), operation.ID)
			if err != nil {
				test.Fatal("restore preview failed")
			}
			defer restore.Destroy()
			restore, err = restore.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("restore confirmation failed")
			}
			result, err = reopened.RestoreCodex(test.Context(), restore)
			if err != nil || result.State != CodexRestored {
				test.Fatal("fresh authenticated restore failed")
			}
			for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
				if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("restore did not preserve original absence")
				}
			}
			actual, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
			if err != nil || !bytes.Equal(auth, actual) {
				test.Fatal("crash/recovery/restore changed Keep auth")
			}
		})
	}
}

func TestCodexAnchorRepairCrashChild(test *testing.T) {
	if os.Getenv("COGNATE_D1_ANCHOR_REPAIR") != "1" {
		return
	}
	state := &codexEngineState{}
	var err error
	state.home, err = nativeOpenDirectory(os.Getenv("COGNATE_D1_HOME"), false)
	if err != nil {
		test.Fatal("repair home")
	}
	defer state.home.close()
	state.state, err = nativeOpenDirectory(os.Getenv("COGNATE_D1_STATE"), false)
	if err != nil {
		test.Fatal("repair state")
	}
	defer state.state.close()
	state.keys, err = deriveCodexKeys(bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("repair key")
	}
	defer state.keys.destroy()
	guard, err := acquireCodexGuard(test.Context(), state.state)
	if err != nil {
		test.Fatal("repair guard")
	}
	defer guard.close()
	state.fault = func(label string) error {
		if label == "anchor-recovery-exchanged" {
			os.Exit(73)
		}
		return nil
	}
	if err := state.openCodexVault(); err != nil {
		test.Fatal("repair before crash", err)
	}
	defer state.vault.close()
	test.Fatal("repair crash not reached")
}

func TestCodexAnchorRepairCanItselfBeInterrupted(test *testing.T) {
	engine, home, stateRoot, key := codexFixture(test)
	bodies := [3][]byte{[]byte("repair-config"), nil, []byte("repair-catalog")}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	state := engine.private()
	state.fault = func(label string) error {
		if label == "anchor-staged" {
			return ErrCodexUnknown
		}
		return nil
	}
	if err := state.withLock(test.Context(), func(ctx context.Context) error { return state.saveCodexInventory() }); !errors.Is(err, ErrCodexUnknown) {
		test.Fatal("repair fixture did not retain authenticated pre-exchange pair")
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexAnchorRepairCrashChild$", "-test.count=1")
	child.Env = append(os.Environ(), "COGNATE_D1_ANCHOR_REPAIR=1", "COGNATE_D1_HOME="+home, "COGNATE_D1_STATE="+stateRoot)
	if exit, ok := errors.AsType[*exec.ExitError](child.Run()); !ok || exit.ExitCode() != 73 {
		test.Fatal("actual recovery exchange crash exit73 not observed")
	}
	fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal("interrupted repair did not retain reopenable post-exchange pair", err)
	}
	defer fresh.Close()
	preview, err := fresh.PreviewCodexRecovery(test.Context(), operation.ID)
	if err != nil {
		test.Fatal(err)
	}
	confirmed, err := preview.Confirm(test.Context(), true)
	if err != nil {
		test.Fatal(err)
	}
	if result, err := fresh.RecoverCodex(test.Context(), confirmed); err != nil || result.State != CodexCompleted {
		test.Fatal("freshly approved recovery after interrupted anchor repair failed", err)
	}
}

func TestCodexAnchorTransitionChild(test *testing.T) {
	phase := os.Getenv("COGNATE_D1_ANCHOR_PHASE")
	if phase == "" {
		return
	}
	ordinal, err := strconv.Atoi(os.Getenv("COGNATE_D1_ANCHOR_ORDINAL"))
	if err != nil || ordinal < 1 || ordinal > 8 {
		test.Fatal("invalid fixture ordinal")
	}
	engine, err := OpenCodex(test.Context(), os.Getenv("COGNATE_D1_HOME"), os.Getenv("COGNATE_D1_STATE"), bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("child open")
	}
	defer engine.Close()
	seen := 0
	engine.private().fault = func(label string) error {
		if label == phase {
			seen++
			if seen == ordinal {
				os.Exit(73)
			}
		}
		return nil
	}
	bodies := [3][]byte{[]byte("anchor-transition-config"), nil, []byte("anchor-transition-catalog")}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	if _, err := codexPublishLiteral(test.Context(), engine, operation.ID); err != nil {
		test.Fatal("child publish before crash")
	}
	test.Fatal("anchor crash ordinal not reached")
}

func TestCodexEveryAnchorPublicationBoundaryReopens(test *testing.T) {
	for _, phase := range []string{"anchor-staged", "anchor-exchanged", "anchor-durable"} {
		for ordinal := 1; ordinal <= 8; ordinal++ {
			test.Run(phase+"-"+strconv.Itoa(ordinal), func(test *testing.T) {
				engine, home, stateRoot, key := codexFixture(test)
				if err := engine.Close(); err != nil {
					test.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexAnchorTransitionChild$", "-test.count=1")
				child.Env = append(os.Environ(), "COGNATE_D1_ANCHOR_PHASE="+phase, "COGNATE_D1_ANCHOR_ORDINAL="+strconv.Itoa(ordinal), "COGNATE_D1_HOME="+home, "COGNATE_D1_STATE="+stateRoot)
				if exit, ok := errors.AsType[*exec.ExitError](child.Run()); !ok || exit.ExitCode() != 73 {
					test.Fatal("anchor transition did not reach actual child exit73")
				}
				fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
				if err != nil {
					test.Fatal("legitimate authenticated anchor transition refused", err)
				}
				defer fresh.Close()
				results, err := fresh.ReconcileCodex(test.Context())
				if err != nil || len(results) != 1 {
					test.Fatal("anchor transition lost operation", err)
				}
				id := results[0].OperationID
				if ordinal == 1 {
					if results[0].State != CodexRemoteOutcomeUnknown {
						test.Fatal("attempt-only anchor invented candidates or completion")
					}
					if _, err := fresh.PreviewCodexRecovery(test.Context(), id); err == nil {
						test.Fatal("attempt-only anchor reconstructed secret")
					}
					return
				}
				if results[0].State != CodexCompleted {
					if results[0].State != CodexReconcileRequired {
						test.Fatal("known transition did not retain recoverable phase")
					}
					preview, err := fresh.PreviewCodexRecovery(test.Context(), id)
					if err != nil {
						test.Fatal(err)
					}
					if _, err := fresh.RecoverCodex(test.Context(), preview); !errors.Is(err, ErrCodexInvalid) {
						test.Fatal("anchor recovery bypassed fresh approval")
					}
					confirmed, err := preview.Confirm(test.Context(), true)
					if err != nil {
						test.Fatal(err)
					}
					if result, err := fresh.RecoverCodex(test.Context(), confirmed); err != nil || result.State != CodexCompleted || result.SelectionRevision != 1 {
						test.Fatal("anchor recovery did not complete exactly once", err)
					}
				}
				preview, err := fresh.PreviewCodexRestore(test.Context(), id)
				if err != nil {
					test.Fatal(err)
				}
				confirmed, err := preview.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal(err)
				}
				if result, err := fresh.RestoreCodex(test.Context(), confirmed); err != nil || result.State != CodexRestored || result.SelectionRevision != 2 {
					test.Fatal("anchor transition restore failed", err)
				}
				for _, name := range codexRoleNames {
					if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
						test.Fatal("anchor transition restore changed original Missing semantics")
					}
				}
			})
		}
	}
}

func TestCodexCrashChild(test *testing.T) {
	phase := os.Getenv("COGNATE_D1_CRASH_PHASE")
	if phase == "" {
		return
	}
	home, stateRoot := os.Getenv("COGNATE_D1_HOME"), os.Getenv("COGNATE_D1_STATE")
	engine, err := OpenCodex(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("child open")
	}
	defer engine.Close()
	engine.private().fault = func(label string) error {
		if label == phase {
			os.Exit(73)
		}
		return nil
	}
	if id := os.Getenv("COGNATE_D1_RESTORE_SOURCE"); id != "" {
		plan, err := engine.PreviewCodexRestore(test.Context(), id)
		if err != nil {
			test.Fatal("child restore preview")
		}
		approved, err := plan.Confirm(test.Context(), true)
		if err != nil {
			test.Fatal("child restore confirmation")
		}
		if _, err := engine.RestoreCodex(test.Context(), approved); err != nil {
			test.Fatal("restore before crash")
		}
		test.Fatal("restore crash never reached")
	}
	bodies := [3][]byte{[]byte("new-private-config"), nil, []byte("new-private-catalog")}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	if _, err := codexPublishLiteral(test.Context(), engine, operation.ID); err != nil {
		test.Fatal("child publish before crash")
	}
	test.Fatal("crash phase never reached")
}

func TestCodexRestoreCrashAndMissingAreReal(test *testing.T) {
	for _, phase := range []string{"entry-published-2", "entry-published-0", "targets-durable"} {
		test.Run(phase, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			bodies := [3][]byte{[]byte("config-to-remove"), nil, []byte("catalog-to-remove")}
			source := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, source, bodies)
			if _, err := codexPublishLiteral(test.Context(), engine, source.ID); err != nil {
				test.Fatal(err)
			}
			if err := engine.Close(); err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexCrashChild$", "-test.count=1")
			child.Env = append(os.Environ(), "COGNATE_D1_CRASH_PHASE="+phase, "COGNATE_D1_RESTORE_SOURCE="+source.ID, "COGNATE_D1_HOME="+home, "COGNATE_D1_STATE="+stateRoot)
			err := child.Run()
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != 73 {
				test.Fatal("real restore crash not reached")
			}
			fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal(err)
			}
			defer fresh.Close()
			results, err := fresh.ReconcileCodex(test.Context())
			if err != nil || len(results) != 2 {
				test.Fatal("restore operation lost", err)
			}
			var restoration CodexResult
			for _, result := range results {
				if result.OperationID != source.ID {
					restoration = result
				}
			}
			if restoration.State != CodexRestored {
				plan, err := fresh.PreviewCodexRecovery(test.Context(), restoration.OperationID)
				if err != nil {
					test.Fatal(err)
				}
				approved, err := plan.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal(err)
				}
				restoration, err = fresh.RecoverCodex(test.Context(), approved)
				if err != nil {
					test.Fatal(err)
				}
			}
			if restoration.State != CodexRestored || restoration.SelectionRevision != 2 || restoration.ConfirmationRevision != 2 {
				test.Fatal("restore counts changed")
			}
			for _, name := range codexRoleNames {
				if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("Missing restored as empty/present")
				}
			}
			for range 3 {
				if _, err := fresh.StatusCodex(test.Context(), source.ID); err != nil {
					test.Fatal(err)
				}
			}
			current, err := fresh.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil {
				test.Fatal(err)
			}
			if current.get[codexCurrent]().record.Revision != 2 || current.get[codexCurrent]().record.Asset != "" {
				test.Fatal("historical reconcile overwrote newer selection")
			}
		})
	}
}

func TestCodexAttemptCrashHasNoSecretNoReplay(test *testing.T) {
	engine, home, stateRoot, key := codexFixture(test)
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexCrashChild$", "-test.count=1")
	child.Env = append(os.Environ(), "COGNATE_D1_CRASH_PHASE=operation-reserved", "COGNATE_D1_HOME="+home, "COGNATE_D1_STATE="+stateRoot)
	err := child.Run()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 73 {
		test.Fatal("real attempted crash not reached")
	}
	fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal(err)
	}
	defer fresh.Close()
	results, err := fresh.ReconcileCodex(test.Context())
	if err != nil || len(results) != 1 || results[0].State != CodexRemoteOutcomeUnknown {
		test.Fatal("missing candidates became local success")
	}
	if _, err := fresh.PreviewCodexRecovery(test.Context(), results[0].OperationID); err == nil {
		test.Fatal("missing secret reconstructed from record")
	}
	for _, name := range codexRoleNames {
		if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("attempted unknown wrote target")
		}
	}
}

func TestCodexRealChildCrashRecoveryAndRestore(test *testing.T) {
	for _, phase := range []string{"candidates-durable", "stage-durable-2", "stage-durable-0", "before-publish-2", "entry-published-2", "directory-synced-2", "before-publish-0", "entry-published-0", "directory-synced-0", "targets-durable", "completed-durable"} {
		test.Run(phase, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			originals := [3][]byte{[]byte("original-private-config"), []byte("original-private-auth"), []byte("original-private-catalog")}
			for index, name := range codexRoleNames {
				if err := os.WriteFile(filepath.Join(home, ".codex", name), originals[index], 0o600); err != nil {
					test.Fatal(err)
				}
			}
			if err := engine.Close(); err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexCrashChild$", "-test.count=1")
			command.Env = append(os.Environ(), "COGNATE_D1_CRASH_PHASE="+phase, "COGNATE_D1_HOME="+home, "COGNATE_D1_STATE="+stateRoot)
			err := command.Run()
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != 73 {
				test.Fatal("did not observe real crash exit73")
			}
			fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("fresh process-state open", err)
			}
			defer fresh.Close()
			results, err := fresh.ReconcileCodex(test.Context())
			if err != nil || len(results) != 1 {
				test.Fatal("reconcile lost operation", err)
			}
			id := results[0].OperationID
			if results[0].State != CodexCompleted {
				if results[0].State != CodexReconcileRequired {
					test.Fatal("known partial did not require confirmation")
				}
				plan, err := fresh.PreviewCodexRecovery(test.Context(), id)
				if err != nil {
					test.Fatal(err)
				}
				if _, err := fresh.RecoverCodex(test.Context(), plan); !errors.Is(err, ErrCodexInvalid) {
					test.Fatal("recovery accepted absent approval")
				}
				approved, err := plan.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal(err)
				}
				result, err := fresh.RecoverCodex(test.Context(), approved)
				if err != nil || result.State != CodexCompleted {
					test.Fatal("confirmed recovery failed", err)
				}
			}
			if err := fresh.Close(); err != nil {
				test.Fatal(err)
			}
			fresh, err = OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal(err)
			}
			defer fresh.Close()
			plan, err := fresh.PreviewCodexRestore(test.Context(), id)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := fresh.RestoreCodex(test.Context(), plan); !errors.Is(err, ErrCodexInvalid) {
				test.Fatal("restore accepted absent approval")
			}
			approved, err := plan.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal(err)
			}
			result, err := fresh.RestoreCodex(test.Context(), approved)
			if err != nil || result.State != CodexRestored || result.SelectionRevision != 2 || result.ConfirmationRevision != 2 {
				test.Fatal("restore did not commit exactly once", err)
			}
			if _, err := fresh.RestoreCodex(test.Context(), approved); !errors.Is(err, ErrCodexInvalid) {
				test.Fatal("restore handle replayed")
			}
			for index, name := range codexRoleNames {
				actual, err := os.ReadFile(filepath.Join(home, ".codex", name))
				if err != nil || !bytes.Equal(actual, originals[index]) {
					test.Fatal("real restored bytes differ")
				}
			}
			current, err := fresh.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil {
				test.Fatal(err)
			}
			if current.get[codexCurrent]().record.Asset != "" {
				test.Fatal("restore left an active cloud selection")
			}
		})
	}
}

func TestCodexRestoreRefusesLaterAuthAndForeignIdentity(test *testing.T) {
	for _, role := range []int{0, 1, 2} {
		test.Run(codexRoleNames[role], func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			bodies := [3][]byte{[]byte("private-config"), nil, []byte("private-catalog")}
			op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, op, bodies)
			if _, err := codexPublishLiteral(test.Context(), engine, op.ID); err != nil {
				test.Fatal(err)
			}
			plan, err := engine.PreviewCodexRestore(test.Context(), op.ID)
			if err != nil {
				test.Fatal(err)
			}
			approved, err := plan.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(home, ".codex", codexRoleNames[role])
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				test.Fatal(err)
			}
			foreign := []byte("later-foreign-or-login")
			if err := os.WriteFile(path, foreign, 0o600); err != nil {
				test.Fatal(err)
			}
			if _, err := engine.RestoreCodex(test.Context(), approved); !errors.Is(err, ErrCodexConflict) {
				test.Fatal("restore overwrote later file")
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, foreign) {
				test.Fatal("later file lost")
			}
		})
	}
}
