//go:build linux

package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/install"
	"golang.org/x/sys/unix"
)

func TestCodexRegressionD1AnchorCrashChild(test *testing.T) {
	phase := os.Getenv("COGNATE_D1_REGRESSION_CRASH_PHASE")
	if phase == "" {
		return
	}
	engine, err := OpenCodex(test.Context(), os.Getenv("COGNATE_D1_REGRESSION_HOME"), os.Getenv("COGNATE_D1_REGRESSION_STATE"), bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("child open failed")
	}
	defer engine.Close()
	engine.private().fault = func(label string) error {
		if label == phase {
			os.Exit(73)
		}
		return nil
	}
	_, err = codexPublishLiteral(test.Context(), engine, os.Getenv("COGNATE_D1_REGRESSION_OPERATION"))
	if err != nil {
		test.Fatal("child publish failed before crash")
	}
	test.Fatal("crash point not reached")
}

func TestCodexRegressionD1AnchorCrashReopen(test *testing.T) {
	for _, phase := range []string{"anchor-staged", "anchor-durable"} {
		test.Run(phase, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			bodies := [3][]byte{[]byte("review-synthetic-config"), nil, []byte("review-synthetic-catalog")}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, operation, bodies)
			if err := engine.Close(); err != nil {
				test.Fatal("parent close failed")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexRegressionD1AnchorCrashChild$", "-test.count=1")
			child.Env = append(os.Environ(), "COGNATE_D1_REGRESSION_CRASH_PHASE="+phase, "COGNATE_D1_REGRESSION_HOME="+home, "COGNATE_D1_REGRESSION_STATE="+stateRoot, "COGNATE_D1_REGRESSION_OPERATION="+operation.ID)
			if exit, ok := errors.AsType[*exec.ExitError](child.Run()); !ok || exit.ExitCode() != 73 {
				test.Fatal("real child exit73 not observed")
			}
			for _, name := range codexRoleNames {
				if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("unexpected target publication before crash")
				}
			}
			var err error
			inspect := &codexEngineState{}
			inspect.home, err = nativeOpenDirectory(home, false)
			if err != nil {
				test.Fatal("inspect home")
			}
			defer inspect.home.close()
			inspect.state, err = nativeOpenDirectory(stateRoot, true)
			if err != nil {
				test.Fatal("inspect state")
			}
			defer inspect.state.close()
			inspect.vault, err = inspect.state.child("codex-local-v1", false)
			if err != nil {
				test.Fatal("inspect vault")
			}
			defer inspect.vault.close()
			inspect.keys, err = deriveCodexKeys(key)
			if err != nil {
				test.Fatal("inspect key")
			}
			defer inspect.keys.destroy()
			var snapshots [2]codexInventory
			for index, name := range []string{codexAnchorName, codexNextAnchorName} {
				image, body, err := inspect.state.read(name, 2*codexMetadataLimit+1024)
				if err != nil || !image.Present {
					test.Fatal("journal pair absent")
				}
				err = inspect.unseal("anchor", nil, body, &snapshots[index], codexMetadataLimit)
				clear(body)
				if err != nil {
					test.Fatal("journal pair not authentic")
				}
			}
			if err := inspect.validateCodexInventory(&snapshots[0]); err != nil {
				test.Fatal("committed inventory has foreign/missing vault files")
			}
			test.Logf("SAFE_JOURNAL_PAIR committed_sequence=%d staged_sequence=%d both_authenticated=true targets_unchanged=true", snapshots[0].Sequence, snapshots[1].Sequence)
			fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("D1-01: authentic journal crash before exchange prevents reopening")
			}
			defer fresh.Close()
			status, err := fresh.StatusCodex(test.Context(), operation.ID)
			if err != nil || status.State != CodexReconcileRequired {
				test.Fatal("known all-before operation did not remain recoverable")
			}
			preview, err := fresh.PreviewCodexRecovery(test.Context(), operation.ID)
			if err != nil {
				test.Fatal("recovery preview refused")
			}
			confirmed, err := preview.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("fresh recovery confirmation refused")
			}
			result, err := fresh.RecoverCodex(test.Context(), confirmed)
			if err != nil || result.State != CodexCompleted {
				test.Fatal("confirmed recovery failed")
			}
		})
	}
}

func TestCodexRegressionD1CloseCancelsPublisherBeforeCompletion(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	bodies := [3][]byte{[]byte("review-close-config"), nil, []byte("review-close-catalog")}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	reached, release := make(chan struct{}), make(chan struct{})
	engine.private().fault = func(label string) error {
		if label == "before-publish-2" {
			close(reached)
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				return context.DeadlineExceeded
			}
		}
		return nil
	}
	type outcome struct {
		result CodexResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := codexPublishLiteral(ctx, engine, operation.ID)
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		test.Fatal("publisher barrier not reached")
	}
	if os.Getenv("COGNATE_D1_REGRESSION_CANCEL_CALLER") == "1" {
		cancel()
	}
	closed := make(chan error, 1)
	go func() { closed <- engine.Close() }()
	deadline := time.Now().Add(3 * time.Second)
	for !engine.private().closing.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	closing := engine.private().closing.Load()
	close(release)
	var actual outcome
	select {
	case actual = <-done:
	case <-time.After(5 * time.Second):
		test.Fatal("publisher did not join")
	}
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal("Close failed")
		}
	case <-time.After(5 * time.Second):
		test.Fatal("Close did not join")
	}
	if !closing {
		test.Fatal("Close cancellation barrier not reached")
	}
	test.Logf("SAFE_CLOSE_OUTCOME engine_closing=true completed=%t error_nil=%t", actual.result.State == CodexCompleted, actual.err == nil)
	if actual.result.State == CodexCompleted || actual.err == nil {
		test.Fatal("D1-02: Close-canceled publisher still completes both target writes and selection")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("Close-canceled publisher advanced final config pointer")
	}
}

func TestCodexInstallLockChild(test *testing.T) {
	mode := os.Getenv("COGNATE_D1_LOCK_CHILD")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(test.Context(), 250*time.Millisecond)
	defer cancel()
	root := os.Getenv("COGNATE_D1_STATE")
	var err error
	if mode == "install" {
		var session *install.Session
		session, err = install.OpenSession(ctx, root)
		if session != nil {
			_ = session.Close()
		}
	} else {
		directory, openErr := nativeOpenDirectory(root, true)
		if openErr != nil {
			test.Fatal("open child root")
		}
		defer directory.close()
		var guard *codexGuard
		guard, err = acquireCodexGuard(ctx, directory)
		if guard != nil {
			_ = guard.close()
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("physical peer lock did not block child")
	}
}

func TestCodexGuardRejectsUnsafeObjectsAndReplacedInode(test *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "world-writable", "replaced-inode"} {
		test.Run(kind, func(test *testing.T) {
			root := filepath.Join(test.TempDir(), "state")
			if err := os.Mkdir(root, 0o700); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(root, "client.db.install.lock")
			switch kind {
			case "symlink":
				if err := os.Symlink("missing", path); err != nil {
					test.Fatal(err)
				}
			case "hardlink":
				if err := os.WriteFile(filepath.Join(root, "original"), nil, 0o600); err != nil {
					test.Fatal(err)
				}
				if err := os.Link(filepath.Join(root, "original"), path); err != nil {
					test.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0o600); err != nil {
					test.Fatal(err)
				}
			case "world-writable":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					test.Fatal(err)
				}
				if err := os.Chmod(path, 0o666); err != nil {
					test.Fatal(err)
				}
			}
			directory, err := nativeOpenDirectory(root, true)
			if err != nil {
				test.Fatal(err)
			}
			defer directory.close()
			ctx, cancel := context.WithTimeout(test.Context(), time.Second)
			defer cancel()
			guard, err := acquireCodexGuard(ctx, directory)
			if kind != "replaced-inode" {
				if guard != nil {
					_ = guard.close()
				}
				if err == nil {
					test.Fatal("unsafe physical guard accepted")
				}
				return
			}
			if err != nil {
				test.Fatal(err)
			}
			defer guard.close()
			if err := os.Rename(path, path+".old"); err != nil {
				test.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				test.Fatal(err)
			}
			if err := guard.check(); err == nil {
				test.Fatal("replaced lock inode accepted")
			}
		})
	}
}

func codexLockChild(test *testing.T, mode, root string) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexInstallLockChild$", "-test.count=1")
	command.Env = append(os.Environ(), "COGNATE_D1_LOCK_CHILD="+mode, "COGNATE_D1_STATE="+root)
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("child lock oracle failed: %s", output)
	}
}

func TestCodexPhysicalInstallLockBothDirections(test *testing.T) {
	root := filepath.Join(test.TempDir(), "state")
	if err := os.Mkdir(root, 0o700); err != nil {
		test.Fatal(err)
	}
	directory, err := nativeOpenDirectory(root, true)
	if err != nil {
		test.Fatal(err)
	}
	defer directory.close()
	guard, err := acquireCodexGuard(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	codexLockChild(test, "install", root)
	if err := guard.close(); err != nil {
		test.Fatal(err)
	}
	session, err := install.OpenSession(test.Context(), root)
	if err != nil {
		test.Fatal("actual SQLite session open")
	}
	codexLockChild(test, "codex", root)
	if err := session.Close(); err != nil {
		test.Fatal("session close")
	}
	guard, err = acquireCodexGuard(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	if err := guard.close(); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "client.db")); err != nil {
		test.Fatal("no real install database")
	}
}

func TestCodexPlatformDoesNotEnableWSL(test *testing.T) {
	if !codexLinuxKernelSupported("6.8.0-generic", "native Linux") {
		test.Fatal("normal Linux rejected")
	}
	for _, release := range []string{"", "5.15.153.1-microsoft-standard-WSL2", "4.4.0-Microsoft", "6.0-WSL"} {
		if codexLinuxKernelSupported(release, "Linux") {
			test.Fatal("unsupported kernel admitted")
		}
	}
}
