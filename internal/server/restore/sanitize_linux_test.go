//go:build linux && amd64

package restore

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"golang.org/x/sys/unix"
)

func sanitizeFixture(test *testing.T) (*runtimegen.OfflineRoot, *Candidate, SanitizeRequest, string) {
	test.Helper()
	source, key := candidateTestSource(test, 2)
	rootPath := candidateParent(test).Name()
	request := SanitizeRequest{OperationID: [16]byte{5}, RevokedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	operation := filepath.Join(rootPath, ".restore-"+hex.EncodeToString(request.OperationID[:]))
	if err := os.Mkdir(operation, 0700); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(operation)
	if err != nil {
		test.Fatal(err)
	}
	defer parent.Close()
	candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = candidate.Close() })
	root, err := runtimegen.LockOffline(test.Context(), rootPath)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = root.Close() })
	return root, candidate, request, rootPath
}

func TestSanitizeRetainedLeaseProbe(test *testing.T) {
	path := os.Getenv("T4_RETAINED_PROBE")
	if path == "" {
		return
	}
	for _, exclusive := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(test.Context(), 150*time.Millisecond)
		var err error
		if exclusive {
			var other *runtimegen.OfflineRoot
			other, err = runtimegen.LockOffline(ctx, path)
			if other != nil {
				_ = other.Close()
			}
		} else {
			var other *runtimegen.Runtime
			other, err = runtimegen.OpenRuntime(ctx, path)
			if other != nil {
				_ = other.Close()
			}
		}
		cancel()
		test.Logf("exclusive=%t admitted=%t blocked=%t", exclusive, err == nil, errors.Is(err, context.DeadlineExceeded))
		if !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("fresh process admitted or wrong rejection class")
		}
	}
}

func TestSanitizeRecoveryExhaustionFailClosed(test *testing.T) {
	if os.Getenv("T4_RETENTION_CHILD") != "1" {
		command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestSanitizeRecoveryExhaustionFailClosed$", "-test.v")
		command.Env = []string{"LANG=C", "LC_ALL=C", "T4_RETENTION_CHILD=1", "TMPDIR=" + os.Getenv("TMPDIR")}
		output, err := command.CombinedOutput()
		test.Log(string(output))
		if err != nil {
			test.Fatal("complete-chain EMFILE failclosed gate")
		}
		return
	}
	root, source, request, path := sanitizeFixture(test)
	if unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: 64, Max: 64}) != nil {
		test.Fatal("resource gate class")
	}
	var fill []int
	exhausted, auxiliaryClosed := false, false
	closeFile := source.value.files.ops.close
	source.value.files.ops.close = func(file *os.File) error {
		if file.Name() != "restore coordinator guard" {
			return closeFile(file)
		}
		if closeFile(file) != nil {
			return ErrCandidateUncertain
		}
		auxiliaryClosed = true
		for {
			descriptor, err := unix.Open(os.DevNull, unix.O_RDWR|unix.O_CLOEXEC, 0)
			if errors.Is(err, unix.EMFILE) {
				exhausted = true
				break
			}
			if err != nil {
				return ErrCandidateUncertain
			}
			fill = append(fill, descriptor)
		}
		return ErrCandidateUncertain
	}
	owner, err := SanitizeCandidate(test.Context(), root, source, request)
	for _, descriptor := range fill {
		if unix.Close(descriptor) != nil {
			test.Fatal("filler close class")
		}
	}
	closeError := root.Close()
	if !exhausted || !auxiliaryClosed || owner != nil || !errors.Is(err, ErrCandidateUncertain) {
		test.Fatal("actual EMFILE stage or capability class")
	}
	fence, fenceError := os.ReadFile(filepath.Join(source.value.files.parentName, candidateFence))
	if fenceError != nil || string(fence) != candidateFenceText {
		test.Fatal("candidate fence lost")
	}
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestSanitizeRetainedLeaseProbe$", "-test.v")
	command.Env = []string{"LANG=C", "LC_ALL=C", "T4_RETAINED_PROBE=" + path}
	probeOutput, probeError := command.CombinedOutput()
	quarantined := errors.Is(closeError, runtimegen.ErrUncertain)
	test.Logf("real_emfile=%t auxiliary_physically_closed=%t no_sanitized=%t candidate_fence_retained=%t root_close_uncertain=%t fresh_process_blocked=%t new_authority_admitted=%t", exhausted, auxiliaryClosed, owner == nil, fenceError == nil, quarantined, probeError == nil, strings.Contains(string(probeOutput), "admitted=true"))
	if !quarantined || probeError != nil {
		test.Fatal("owning-root quarantine failed")
	}
	if !errors.Is(root.Close(), runtimegen.ErrUncertain) {
		test.Fatal("quarantine not idempotent")
	}
}

func TestSanitizeFinalGuardCloseRace(test *testing.T) {
	for _, fault := range []string{"callback-error", "post-fence"} {
		test.Run(fault, func(test *testing.T) {
			root, source, request, path := sanitizeFixture(test)
			entered, release := make(chan struct{}), make(chan struct{})
			closeFile := source.value.files.ops.close
			source.value.files.ops.close = func(file *os.File) error {
				if file.Name() != "restore coordinator guard" {
					return closeFile(file)
				}
				if closeFile(file) != nil {
					return ErrCandidateUncertain
				}
				close(entered)
				<-release
				if fault == "callback-error" {
					return ErrCandidateUncertain
				}
				return os.WriteFile(filepath.Join(path, ".runtime-fences", "foreign"), []byte("CGRLS001"), 0600)
			}
			done, closed := make(chan error, 1), make(chan error, 1)
			go func() {
				owner, err := SanitizeCandidate(test.Context(), root, source, request)
				if owner != nil {
					done <- ErrCandidateInvalid
					return
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				test.Fatal("final guard close not reached")
			}
			go func() { closed <- root.Close() }()
			select {
			case <-closed:
				test.Fatal("Close bypassed final guard scope")
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			if err := <-done; !errors.Is(err, ErrCandidateUncertain) {
				test.Fatal("final guard uncertainty lost")
			}
			if err := <-closed; !errors.Is(err, runtimegen.ErrUncertain) {
				test.Fatal("owning root quarantine lost Close race")
			}
			if _, err := os.Stat(filepath.Join(source.value.files.parentName, candidateFence)); err != nil {
				test.Fatal("candidate fence lost")
			}
			if fault == "post-fence" {
				data, err := os.ReadFile(filepath.Join(path, ".runtime-fences", "foreign"))
				if err != nil || string(data) != "CGRLS001" {
					test.Fatal("foreign fence lost")
				}
			}
			command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestSanitizeRetainedLeaseProbe$")
			command.Env = []string{"LANG=C", "LC_ALL=C", "T4_RETAINED_PROBE=" + path}
			if _, err := command.CombinedOutput(); err != nil {
				test.Fatal("fresh process quarantine probe class")
			}
		})
	}
}

func TestSanitizeCompleteChain(test *testing.T) {
	root, source, request, _ := sanitizeFixture(test)
	metadata := source.value.metadata
	owner, err := SanitizeCandidate(test.Context(), root, source, request)
	if err != nil || owner == nil {
		test.Fatal("sanitize", err)
	}
	defer owner.Close()
	if source.value.files != nil || !reflect.DeepEqual(source.value.metadata, metadata) {
		test.Fatal("source deletion rights or identity not preserved")
	}
	if err := source.Close(); err != nil {
		test.Fatal(err)
	}
	var escaped SanitizedCandidate
	err = owner.WithCandidate(test.Context(), root, func(ctx context.Context, candidate SanitizedCandidate) error {
		escaped = candidate
		if !candidate.Valid() {
			test.Fatal("invalid sanitized scope")
		}
		return ctx.Err()
	})
	if err != nil || escaped.Valid() {
		test.Fatal("sanitized scope lifetime", err)
	}
	if err := owner.WithCandidate(test.Context(), root, func(context.Context, SanitizedCandidate) error { test.Fatal("replay"); return nil }); err == nil {
		test.Fatal("replay admitted")
	}
}

func TestSanitizePublicationMovesDeletionRights(test *testing.T) {
	root, source, request, rootPath := sanitizeFixture(test)
	owner, err := SanitizeCandidate(test.Context(), root, source, request)
	if err != nil {
		test.Fatal(err)
	}
	var moved *publicationOwnedFiles
	err = owner.WithCandidate(test.Context(), root, func(ctx context.Context, view SanitizedCandidate) error {
		var err error
		moved, err = takeForPublication(view)
		if err != nil {
			return err
		}
		if view.Valid() {
			test.Fatal("taken view still valid")
		}
		if moved.readyForPublication(root) {
			test.Fatal("publication became ready inside callback")
		}
		return ctx.Err()
	})
	if err != nil || moved == nil || !moved.readyForPublication(root) {
		test.Fatal("take/postadmit readiness", err)
	}
	err = root.WithWorkerFiles(test.Context(), func(rootFile, _ *os.File) error {
		generation := filepath.Join(rootPath, "test-generation")
		if !moved.readyForPublication(root) {
			test.Fatal("publication not ready in fresh EX loan")
		}
		if err := unix.Renameat(int(moved.files.parent.Fd()), "candidate", int(rootFile.Fd()), "test-generation"); err != nil {
			return err
		}
		for _, entry := range []struct {
			target **os.File
			name   string
		}{{&moved.files.root, generation}, {&moved.files.archives, filepath.Join(generation, "archives")}} {
			descriptor, err := unix.FcntlInt((*entry.target).Fd(), unix.F_DUPFD_CLOEXEC, 0)
			if err != nil {
				return err
			}
			if err := (*entry.target).Close(); err != nil {
				return err
			}
			*entry.target = os.NewFile(uintptr(descriptor), entry.name)
		}
		return nil
	})
	if err != nil || moved == nil {
		test.Fatal("take/real rename", err)
	}
	closeError := errors.Join(source.Close(), owner.Close())
	if _, err := os.Stat(filepath.Join(rootPath, "test-generation", "server.db")); err != nil {
		test.Fatal("old Close deleted renamed content")
	}
	if closeError != nil {
		test.Fatal(closeError)
	}
	for _, file := range []*os.File{moved.files.archives, moved.files.root, moved.files.parent} {
		if err := file.Close(); err != nil {
			test.Fatal(err)
		}
	}
	clear(moved.key[:])
}

func TestSanitizeTakePostadmitFailureNeverReady(test *testing.T) {
	root, source, request, rootPath := sanitizeFixture(test)
	owner, err := SanitizeCandidate(test.Context(), root, source, request)
	if err != nil {
		test.Fatal(err)
	}
	var moved *publicationOwnedFiles
	err = owner.WithCandidate(test.Context(), root, func(_ context.Context, view SanitizedCandidate) error {
		var err error
		moved, err = takeForPublication(view)
		if err != nil {
			return err
		}
		if moved.readyForPublication(root) {
			test.Fatal("pending owner is already ready")
		}
		return os.WriteFile(filepath.Join(rootPath, ".runtime-fences", "pending-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), []byte("CGRLS001"), 0600)
	})
	if err == nil || moved == nil || moved.readyForPublication(root) {
		test.Fatal("failed root postadmit produced ready owner", err)
	}
	if moved.phase.Load() != publicationQuarantined {
		test.Fatal("failed take was not quarantined")
	}
	if err := errors.Join(source.Close(), owner.Close()); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved.files.root.Name(), "server.db")); err != nil {
		test.Fatal("old owner deleted pending tree")
	}
	for _, file := range []*os.File{moved.files.archives, moved.files.root, moved.files.parent} {
		if err := file.Close(); err != nil {
			test.Fatal(err)
		}
	}
	clear(moved.key[:])
}

func TestSanitizeCloseJoinsHandoffAndPreservesUnknown(test *testing.T) {
	root, source, request, rootPath := sanitizeFixture(test)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	syncFile := source.value.files.ops.sync
	source.value.files.ops.sync = func(file *os.File) error {
		if filepath.Base(file.Name()) == "server.db" {
			once.Do(func() { close(entered); <-release })
		}
		return syncFile(file)
	}
	type outcome struct {
		owner *Sanitized
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		owner, err := SanitizeCandidate(test.Context(), root, source, request)
		done <- outcome{owner, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("mutation did not reach physical post-sync boundary")
	}
	closed := make(chan error, 1)
	go func() { closed <- source.Close() }()
	select {
	case <-closed:
		test.Fatal("Close crossed active handoff")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	result := <-done
	if result.owner != nil || !errors.Is(result.err, ErrCandidateUncertain) {
		test.Fatal("Close race minted capability or masked quarantine", result.err)
	}
	if err := <-closed; !errors.Is(err, ErrCandidateUncertain) {
		test.Fatal("unknown source cleanup", err)
	}
	if _, err := os.Stat(filepath.Join(source.value.files.parentName, candidateFence)); err != nil {
		test.Fatal("candidate fence lost")
	}
	if err := root.Close(); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 60*time.Millisecond)
	defer cancel()
	other, err := runtimegen.LockOffline(ctx, rootPath)
	if other != nil {
		_ = other.Close()
		test.Fatal("unknown coordinator guard released")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal(err)
	}
}

func TestSanitizePanicRetainsActualGuard(test *testing.T) {
	root, source, request, rootPath := sanitizeFixture(test)
	syncFile := source.value.files.ops.sync
	source.value.files.ops.sync = func(file *os.File) error {
		if filepath.Base(file.Name()) == "server.db" {
			panic("synthetic post-mutation panic")
		}
		return syncFile(file)
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, _ = SanitizeCandidate(test.Context(), root, source, request)
	}()
	if !panicked {
		test.Fatal("panic boundary not reached")
	}
	if err := source.Close(); !errors.Is(err, ErrCandidateUncertain) {
		test.Fatal("panic lost unknown fence", err)
	}
	if err := root.Close(); err != nil {
		test.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	runtime.Gosched()
	ctx, cancel := context.WithTimeout(test.Context(), 100*time.Millisecond)
	defer cancel()
	other, err := runtimegen.LockOffline(ctx, rootPath)
	if other != nil {
		_ = other.Close()
		test.Fatal("panic lost actual guard after original EX Close")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal(err)
	}
}

func TestSanitizePreVerificationCloseUnknownRetainsGuard(test *testing.T) {
	root, source, request, rootPath := sanitizeFixture(test)
	closeFile := source.value.files.ops.close
	source.value.files.ops.close = func(file *os.File) error {
		err := closeFile(file)
		if err != nil {
			return err
		}
		if filepath.Base(file.Name()) == "server.db" {
			return ErrCandidateUncertain
		}
		return nil
	}
	owner, err := SanitizeCandidate(test.Context(), root, source, request)
	if owner != nil || !errors.Is(err, ErrCandidateUncertain) {
		test.Fatal("pre-verification unknown was not retained", err)
	}
	if err := source.Close(); !errors.Is(err, ErrCandidateUncertain) {
		test.Fatal("candidate fence not preserved", err)
	}
	if err := root.Close(); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 60*time.Millisecond)
	defer cancel()
	other, err := runtimegen.LockOffline(ctx, rootPath)
	if other != nil {
		_ = other.Close()
		test.Fatal("pre-verification unknown released EX")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal(err)
	}
}

func TestSanitizeAdmissionAndBudgets(test *testing.T) {
	for _, scenario := range []string{"zero-operation", "nonutc", "already-used", "wrong-root", "staging", "stale-view"} {
		test.Run(scenario, func(test *testing.T) {
			root, source, request, _ := sanitizeFixture(test)
			if scenario == "zero-operation" {
				request.OperationID = [16]byte{}
			}
			if scenario == "nonutc" {
				request.RevokedAt = request.RevokedAt.In(time.FixedZone("not-canonical", 0))
			}
			if scenario == "already-used" {
				if err := source.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { return nil }); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "wrong-root" {
				var err error
				root, err = runtimegen.LockOffline(test.Context(), candidateParent(test).Name())
				if err != nil {
					test.Fatal(err)
				}
				defer root.Close()
			}
			if scenario == "staging" {
				source.value.limits.MaxStagingBytes = source.value.limits.Database.MaxDBBytes*3 + 65535
			}
			if scenario == "stale-view" {
				owner, err := SanitizeCandidate(test.Context(), root, source, request)
				if err != nil {
					test.Fatal(err)
				}
				defer owner.Close()
				var escaped SanitizedCandidate
				if err := owner.WithCandidate(test.Context(), root, func(_ context.Context, view SanitizedCandidate) error { escaped = view; return nil }); err != nil {
					test.Fatal(err)
				}
				if _, err := takeForPublication(escaped); err == nil {
					test.Fatal("escaped scope transferred deletion rights")
				}
				return
			}
			owner, err := SanitizeCandidate(test.Context(), root, source, request)
			if err == nil || owner != nil {
				test.Fatal("inadmissible source produced Sanitized", scenario)
			}
		})
	}
}
