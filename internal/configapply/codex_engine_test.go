package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/install"
)

func TestCodexLockCallbackReceivesLifetimeCancellation(test *testing.T) {
	engine, _, _, _ := codexFixture(test)
	ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
	defer cancel()
	reached := make(chan struct{})
	done := make(chan error, 1)
	var workers sync.WaitGroup
	defer workers.Wait()
	workers.Go(func() {
		done <- engine.private().withLock(ctx, func(operation context.Context) error {
			close(reached)
			<-operation.Done()
			return operation.Err()
		})
	})
	select {
	case <-reached:
	case <-ctx.Done():
		test.Fatal("callback not reached")
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		test.Fatal("callback received caller-only context instead of merged lifetime cancellation")
	}
}

func TestCodexCloseActivePhaseKeepsTruthfulState(test *testing.T) {
	for _, scenario := range []struct {
		name     string
		mode     string
		phase    string
		complete bool
	}{
		{"prepare-reserved", "prepare", "operation-reserved", false},
		{"prepare-candidate", "prepare", "candidates-durable", false},
		{"prepare-stage", "prepare", "stage-durable-2", false},
		{"publish-before-catalog", "publish", "before-publish-2", false},
		{"publish-between-targets", "publish", "directory-synced-2", false},
		{"publish-all-targets", "publish", "targets-durable", false},
		{"publish-completed", "publish", "completed-durable", true},
		{"recover-stages", "recover", "recovery-stages-start", false},
		{"restore-reserved", "restore", "operation-reserved", false},
		{"restore-before-catalog", "restore", "before-publish-2", false},
		{"restore-between-targets", "restore", "directory-synced-2", false},
		{"restore-completed", "restore", "completed-durable", true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			bodies := [3][]byte{[]byte("close-phase-config"), nil, []byte("close-phase-catalog")}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			if scenario.mode != "prepare" {
				codexStageLiteral(test, engine, operation, bodies)
			}
			var recovery CodexRecoveryPlan
			var restore CodexRestorePlan
			var err error
			if scenario.mode == "recover" {
				recovery, err = engine.PreviewCodexRecovery(test.Context(), operation.ID)
				if err == nil {
					recovery, err = recovery.Confirm(test.Context(), true)
				}
			} else if scenario.mode == "restore" {
				_, err = codexPublishLiteral(test.Context(), engine, operation.ID)
				if err == nil {
					restore, err = engine.PreviewCodexRestore(test.Context(), operation.ID)
				}
				if err == nil {
					restore, err = restore.Confirm(test.Context(), true)
				}
			}
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
			defer cancel()
			reached, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			engine.private().fault = func(label string) error {
				if label == scenario.phase {
					close(reached)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			type outcome struct {
				result CodexResult
				err    error
			}
			done, closed := make(chan outcome, 1), make(chan error, 1)
			var workers sync.WaitGroup
			defer func() { cancel(); resume(); workers.Wait() }()
			workers.Go(func() {
				var result CodexResult
				var err error
				switch scenario.mode {
				case "prepare":
					err = engine.private().withLock(ctx, func(ctx context.Context) error {
						return engine.private().prepareCodexOperation(ctx, operation, bodies)
					})
				case "publish":
					result, err = codexPublishLiteral(ctx, engine, operation.ID)
				case "recover":
					result, err = engine.RecoverCodex(ctx, recovery)
				case "restore":
					result, err = engine.RestoreCodex(ctx, restore)
				}
				done <- outcome{result, err}
			})
			select {
			case <-reached:
			case <-ctx.Done():
				test.Fatal("active operation did not reach phase barrier")
			}
			workers.Go(func() { closed <- engine.Close() })
			select {
			case <-engine.private().life.Done():
			case <-ctx.Done():
				test.Fatal("Close did not cancel lifetime")
			}
			select {
			case <-closed:
				test.Fatal("Close released resources before active worker drained")
			default:
			}
			resume()
			actual := <-done
			if err := <-closed; err != nil {
				test.Fatal(err)
			}
			completed := actual.result.State == CodexCompleted || actual.result.State == CodexRestored
			if completed != scenario.complete || (actual.err == nil) != scenario.complete {
				test.Fatal("Close phase lied about cancellation or durable completion")
			}
			config, configError := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
			wantConfig := scenario.mode == "restore" && !scenario.complete || scenario.mode == "publish" && (scenario.complete || scenario.phase == "targets-durable")
			if wantConfig {
				if configError != nil || !bytes.Equal(config, bodies[0]) {
					test.Fatal("Close erased already-published config or restore source")
				}
			} else if !errors.Is(configError, os.ErrNotExist) {
				test.Fatal("canceled phase advanced final config pointer")
			}
			catalog, catalogError := os.ReadFile(filepath.Join(home, ".codex", "cognate-model-catalog.json"))
			if scenario.name == "publish-between-targets" && (catalogError != nil || !bytes.Equal(catalog, bodies[2])) {
				test.Fatal("partial catalog publication was falsely rolled back")
			}
			if scenario.name == "restore-between-targets" && !errors.Is(catalogError, os.ErrNotExist) {
				test.Fatal("partial catalog restore was falsely rolled back")
			}
			fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("cancellation left unreopenable authenticated state", err)
			}
			defer fresh.Close()
			if _, err := fresh.ReconcileCodex(test.Context()); err != nil {
				test.Fatal("cancellation lost inspectable truthful phase", err)
			}
		})
	}
}

func TestCodexOpenDoesNotRetainInstallLeaseAndCloseDrains(test *testing.T) {
	engine, _, stateRoot, _ := codexFixture(test)
	ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
	defer cancel()
	session, err := install.OpenSession(ctx, stateRoot)
	if err != nil {
		test.Fatal("Open retained physical lease")
	}
	defer session.Close()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := engine.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
		done <- err
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- engine.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		test.Fatal("Close failed to cancel lock waiter")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrCodexClosed) {
			test.Fatal("closing admitted operation")
		}
	case <-time.After(time.Second):
		test.Fatal("Close returned before operation drained")
	}
	if err := engine.Close(); err != nil {
		test.Fatal("repeated Close")
	}
	if _, err := engine.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile"); !errors.Is(err, ErrCodexClosed) {
		test.Fatal("closed engine admitted write")
	}
}

func TestCodexSelectionRaceChild(test *testing.T) {
	role := os.Getenv("COGNATE_D1_SELECTION_CHILD")
	if role == "" {
		return
	}
	engine, err := OpenCodex(test.Context(), os.Getenv("COGNATE_D1_HOME"), os.Getenv("COGNATE_D1_STATE"), bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("selection child open")
	}
	defer engine.Close()
	bodies := [3][]byte{[]byte("one-selection-config"), nil, []byte("one-selection-catalog")}
	op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	barrier := os.Getenv("COGNATE_D1_BARRIER")
	if err := os.WriteFile(filepath.Join(barrier, role+".ready"), nil, 0o600); err != nil {
		test.Fatal("ready signal")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(filepath.Join(barrier, "release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			test.Fatal("selection barrier timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}
	err = engine.private().withLock(ctx, func(ctx context.Context) error {
		if err := engine.private().prepareCodexOperation(ctx, op, bodies); err != nil {
			return err
		}
		_, err := engine.private().publishCodex(ctx, op)
		return err
	})
	if errors.Is(err, ErrCodexConflict) {
		os.Exit(74)
	}
	if err != nil {
		test.Fatal("selection child unexpected failure")
	}
}

func TestCodexRealProcessesSelectionCASExactlyOnce(test *testing.T) {
	engine, home, stateRoot, key := codexFixture(test)
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	barrier := test.TempDir()
	ctx, cancel := context.WithTimeout(test.Context(), 12*time.Second)
	defer cancel()
	var children []*exec.Cmd
	defer func() {
		for _, child := range children {
			if child.ProcessState == nil {
				_ = child.Process.Kill()
				_ = child.Wait()
			}
		}
	}()
	for _, role := range []string{"first", "second"} {
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexSelectionRaceChild$", "-test.count=1")
		child.Env = append(os.Environ(), "COGNATE_D1_SELECTION_CHILD="+role, "COGNATE_D1_HOME="+home, "COGNATE_D1_STATE="+stateRoot, "COGNATE_D1_BARRIER="+barrier)
		if err := child.Start(); err != nil {
			test.Fatal("start real selector")
		}
		children = append(children, child)
	}
	for {
		_, first := os.Stat(filepath.Join(barrier, "first.ready"))
		_, second := os.Stat(filepath.Join(barrier, "second.ready"))
		if first == nil && second == nil {
			break
		}
		select {
		case <-ctx.Done():
			test.Fatal("both real previews not reached")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := os.WriteFile(filepath.Join(barrier, "release"), nil, 0o600); err != nil {
		test.Fatal("release previews")
	}
	winners, conflicts := 0, 0
	for _, child := range children {
		err := child.Wait()
		if err == nil {
			winners++
		} else if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 74 {
			conflicts++
		} else {
			test.Fatal("unexpected selector exit")
		}
	}
	if winners != 1 || conflicts != 1 {
		test.Fatal("stale selection published more than once")
	}
	fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal(err)
	}
	defer fresh.Close()
	current, err := fresh.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil {
		test.Fatal(err)
	}
	if current.get[codexCurrent]().record.Revision != 1 || current.get[codexCurrent]().record.Confirmation != 1 {
		test.Fatal("physical selector counts guessed")
	}
}

func TestCodexOwnershipDoesNotCrossOwnerOriginOrC0Profile(test *testing.T) {
	for _, contextField := range []string{"owner", "origin", "profile"} {
		test.Run(contextField, func(test *testing.T) {
			engine, _, _, _ := codexFixture(test)
			bodies := [3][]byte{[]byte("owned-config"), nil, []byte("owned-catalog")}
			first := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, first, bodies)
			if _, err := codexPublishLiteral(test.Context(), engine, first.ID); err != nil {
				test.Fatal(err)
			}
			second := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			switch contextField {
			case "owner":
				second.Owner = "other-owner"
			case "origin":
				second.Origin = "https://other.invalid"
			case "profile":
				second.Profile = "other-c0-profile"
			}
			second.Base = codexSelectionRecord{Origin: second.Origin, Owner: second.Owner, Profile: second.Profile, Device: engine.private().inventory.Device}
			err := engine.private().withLock(test.Context(), func(ctx context.Context) error { return engine.private().prepareCodexOperation(ctx, second, bodies) })
			if !errors.Is(err, ErrCodexConflict) {
				test.Fatal("same-key context took ownership of foreign selection")
			}
		})
	}
}
