//go:build linux

package state

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

type snapshotWaitingWriter struct {
	ctx      context.Context
	entered  chan struct{}
	fallback <-chan time.Time
	once     sync.Once
	canceled bool
}

type snapshotShortWriter struct{}

func (snapshotShortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestLocalSnapshotSwallowedCopyFailure(test *testing.T) {
	store, _ := snapshotTestStore(test)
	result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		if count, err := view.CopyDatabase(ctx, snapshotShortWriter{}); err == nil || count != 0 {
			return ErrLocalSnapshotUncertain
		}
		return nil
	})
	if err == nil || result.get != nil {
		test.Fatal("swallowed admitted copy failure minted summary")
	}
}

func TestLocalSnapshotStrongHolderProcessProbe(test *testing.T) {
	directory := os.Getenv("COGNATE_SNAPSHOT_HOLDER_PROBE")
	if directory == "" {
		test.Skip("bounded child-process probe")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 250*time.Millisecond)
	defer cancel()
	instance, err := maintenance.Join(ctx, directory)
	if os.Getenv("COGNATE_SNAPSHOT_HOLDER_EXPECT") == "available" {
		if err != nil || instance == nil {
			test.Fatal("reaped closed root was not available")
		}
		if err = instance.Close(); err != nil {
			test.Fatal("probe could not close joined root")
		}
		return
	}
	if instance != nil {
		_ = instance.Close()
	}
	if instance != nil || !errors.Is(err, maintenance.ErrLock) {
		test.Fatal("cross-process root was not held")
	}
}

func snapshotAssertRootHeld(test *testing.T, path string) {
	test.Helper()
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestLocalSnapshotStrongHolderProcessProbe$")
	command.Env = append(os.Environ(), "COGNATE_SNAPSHOT_HOLDER_PROBE="+filepath.Dir(path))
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("actual strong holder process failed: %s", output)
	}
}

func (writer *snapshotWaitingWriter) Write(data []byte) (int, error) {
	writer.once.Do(func() { close(writer.entered) })
	select {
	case <-writer.ctx.Done():
		writer.canceled = true
		return 0, writer.ctx.Err()
	case <-writer.fallback:
		return 0, io.ErrShortWrite
	}
}

func TestLocalSnapshotRevokesAndJoinsReader(test *testing.T) {
	store, _ := snapshotTestStore(test)
	var writer *snapshotWaitingWriter
	finished := make(chan error, 1)
	result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		writer = &snapshotWaitingWriter{ctx: ctx, entered: make(chan struct{}), fallback: time.After(500 * time.Millisecond)}
		go func() { _, err := view.CopyDatabase(ctx, writer); finished <- err }()
		<-writer.entered
		return nil
	})
	childError := <-finished
	if !writer.canceled || !errors.Is(childError, context.Canceled) {
		test.Fatal("scope returned without canceling and joining its admitted reader", childError)
	}
	if err == nil || result.get != nil {
		test.Fatal("unjoined failed reader produced successful summary", err)
	}
}

func TestLocalSnapshotClosePinsSQLAndCut(test *testing.T) {
	store, _ := snapshotTestStore(test)
	closed := make(chan error, 1)
	_, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		go func() { closed <- store.Close() }()
		managed := store.state.maintenance
		for {
			managed.mutex.Lock()
			closing := managed.closing
			managed.mutex.Unlock()
			if closing {
				break
			}
			runtime.Gosched()
		}
		select {
		case <-closed:
			return ErrLocalSnapshotInvalid
		default:
		}
		if _, err := store.state.client.User.Query().Count(ctx); err != nil {
			return err
		}
		if _, err := view.CopyDatabase(ctx, io.Discard); err != nil {
			return err
		}
		if _, err := store.WithMaintenanceWriter(ctx, func(context.Context) (bool, error) { return true, nil }); !errors.Is(err, maintenance.ErrQuiescing) && !errors.Is(err, maintenance.ErrClosed) {
			return ErrLocalSnapshotInvalid
		}
		return nil
	})
	if err != nil {
		test.Fatal("Close did not preserve the active SQL/cut", err)
	}
	if err = <-closed; err != nil {
		test.Fatal(err)
	}
}

func TestLocalSnapshotCloseCancelsQueued(test *testing.T) {
	store, _ := snapshotTestStore(test)
	entered, release := make(chan struct{}), make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		_, err := store.WithMaintenanceWriter(test.Context(), func(context.Context) (bool, error) { close(entered); <-release; return true, nil })
		writerDone <- err
	}()
	<-entered
	snapshotDone := make(chan error, 1)
	go func() {
		_, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return errors.New("queued callback ran") })
		snapshotDone <- err
	}()
	managed := store.state.maintenance
	for {
		managed.mutex.Lock()
		queued := len(managed.snapshotOps) == 1
		managed.mutex.Unlock()
		if queued {
			break
		}
		runtime.Gosched()
	}
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	err := <-snapshotDone
	close(release)
	if writerErr := <-writerDone; writerErr != nil {
		test.Fatal(writerErr)
	}
	if closeErr := <-closed; closeErr != nil {
		test.Fatal(closeErr)
	}
	if !errors.Is(err, context.Canceled) {
		test.Fatal("queued request not canceled", err)
	}
}

func TestLocalSnapshotCleanupOutranksCancel(test *testing.T) {
	if os.Geteuid() == 0 {
		test.Skip("requires real non-root unlink permission enforcement")
	}
	store, _ := snapshotTestStore(test)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	var stage *localSnapshotStage
	result, err := store.WithLocalSnapshot(ctx, snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		stage = view.get().stage
		if err := stage.root.Chmod(0500); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if stage != nil {
		defer func() { _ = stage.root.Chmod(0700); _ = stage.cleanup() }()
	}
	if !errors.Is(err, ErrLocalSnapshotCleanup) || result.get != nil {
		test.Fatal("cleanup lost priority to cancellation", err)
	}
	if store.state.maintenance.snapshotPoison {
		test.Fatal("known closed deletion failure falsely poisoned SQL")
	}
	if result, err = store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil }); !errors.Is(err, ErrLocalSnapshotCleanup) || result.get != nil {
		test.Fatal("cleanup-pending request admitted", err)
	}
}

func TestLocalSnapshotConsumerFailureAndPanicCleanup(test *testing.T) {
	store, _ := snapshotTestStore(test)
	result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error {
		return errors.New("private diagnostic must not escape")
	})
	if !errors.Is(err, ErrLocalSnapshotInvalid) || result.get != nil {
		test.Fatal("consumer failure produced summary", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				test.Fatal("consumer panic swallowed")
			}
		}()
		_, _ = store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { panic("synthetic panic") })
	}()
	result, err = store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
	if err != nil || result.get == nil {
		test.Fatal("panic leaked pin or cut", err)
	}
}
