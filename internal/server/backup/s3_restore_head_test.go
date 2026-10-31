package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func restoreHeadParent(test *testing.T) *os.File {
	test.Helper()
	name := test.TempDir()
	if err := os.Chmod(name, 0700); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(name)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = parent.Close() })
	return parent
}

func seedRestoreHeads(test *testing.T, fixture *s3Fixture, target *S3Target, completeSize int64) (Attempt, []byte, []byte) {
	test.Helper()
	attempt, err := NewAttempt([16]byte{1}, [16]byte{2}, [16]byte{3})
	if err != nil {
		test.Fatal(err)
	}
	bootstrap := bytes.Repeat([]byte{0x37}, 156)
	complete := bytes.Repeat([]byte{0x69}, int(completeSize))
	for index, kind := range []ObjectKind{Bootstrap, Complete} {
		payload := [][]byte{bootstrap, complete}[index]
		file, err := os.CreateTemp(fixture.dir, "synthetic-head-")
		if err != nil {
			test.Fatal(err)
		}
		if _, err := file.Write(payload); err != nil {
			test.Fatal(err)
		}
		if err := file.Close(); err != nil {
			test.Fatal(err)
		}
		ref, _ := NewObjectRef(attempt, kind, 0, max(1, int64(len(payload))), [32]byte{})
		key := objectKey(target.options.read(), ref.read())
		fixture.mutex.Lock()
		fixture.objects[key] = fixtureObject{file.Name(), `"opaque-untrusted-head"`, int64(len(payload))}
		fixture.mutex.Unlock()
	}
	return attempt, bootstrap, complete
}

func assertRestoreHeadParentEmpty(test *testing.T, parent *os.File) {
	test.Helper()
	entries, err := os.ReadDir(parent.Name())
	if err != nil || len(entries) != 0 {
		test.Fatalf("restore head stage not removed: entries=%d error=%v", len(entries), err)
	}
	if _, err := parent.Stat(); err != nil {
		test.Fatal("borrowed parent was closed")
	}
}

func TestS3RestoreHeadReadOnlyScope(test *testing.T) {
	for _, size := range []int64{1, 16 << 10} {
		test.Run(fmt.Sprint(size), func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			parent := restoreHeadParent(test)
			expected, bootstrap, complete := seedRestoreHeads(test, fixture, target, size)
			var borrowed []*os.File
			called := false
			err := target.WithRestoreHead(test.Context(), parent, expected, func(ctx context.Context, bootFile, completeFile *os.File) error {
				called = true
				borrowed = []*os.File{bootFile, completeFile}
				for index, file := range borrowed {
					payload, err := io.ReadAll(file)
					if err != nil || !bytes.Equal(payload, [][]byte{bootstrap, complete}[index]) {
						return fmt.Errorf("candidate bytes differ")
					}
					if filepath.Dir(filepath.Dir(file.Name())) != parent.Name() {
						return fmt.Errorf("candidate left explicit parent")
					}
					if _, err := file.WriteAt([]byte{1}, 0); err == nil {
						return fmt.Errorf("candidate is writable")
					}
				}
				return ctx.Err()
			})
			if err != nil || !called {
				test.Fatalf("read-only scope error=%v callback=%t", err, called)
			}
			for _, file := range borrowed {
				if _, err := file.Stat(); err == nil {
					test.Fatal("borrowed head escaped its scope")
				}
			}
			assertRestoreHeadParentEmpty(test, parent)
			calls := fixture.snapshot()
			if len(calls) != 4 {
				test.Fatalf("protocol requests=%d, want 4", len(calls))
			}
			for index, call := range calls {
				kind, length := Bootstrap, int64(156)
				if index >= 2 {
					kind, length = Complete, size
				}
				ref, _ := NewObjectRef(expected, kind, 0, length, [32]byte{})
				method, ranged := "HEAD", ""
				if index%2 == 1 {
					method, ranged = "GET", fmt.Sprintf("bytes=0-%d", length-1)
				}
				if call.method != method || call.rangeHeader != ranged || call.key != objectKey(target.options.read(), ref.read()) {
					test.Fatal("head protocol left canonical expected attempt or used extra operations")
				}
			}
			test.Logf("actual TLS bootstrap=156 complete=%d HEAD=2 ranged_GET=2 max_requests=4 read_only=true files_removed=2 parent_open=true", size)
		})
	}
}

func TestS3RestoreHeadInvalidAdmission(test *testing.T) {
	for _, variant := range []string{"nil-context", "nil-target", "zero-target", "nil-parent", "closed-parent", "public-parent", "regular-parent", "symlink-parent", "replaced-parent", "zero-attempt", "invalid-attempt", "foreign-instance", "nil-consume", "pre-canceled", "closed-target"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			parent := restoreHeadParent(test)
			originalParent := parent
			expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
			ctx := test.Context()
			called := false
			consume := func(context.Context, *os.File, *os.File) error { called = true; return nil }
			wantError := ErrInvalid
			switch variant {
			case "nil-context":
				ctx = nil
			case "nil-target":
				target = nil
			case "zero-target":
				target = &S3Target{}
			case "nil-parent":
				parent = nil
			case "closed-parent":
				_ = parent.Close()
			case "public-parent":
				_ = os.Chmod(parent.Name(), 0755)
			case "regular-parent":
				name := filepath.Join(parent.Name(), "regular")
				_ = os.WriteFile(name, []byte{1}, 0600)
				parent, _ = os.Open(name)
				defer parent.Close()
			case "symlink-parent":
				link := filepath.Join(test.TempDir(), "link")
				if err := os.Symlink(parent.Name(), link); err != nil {
					test.Fatal(err)
				}
				parent, _ = os.Open(link)
				defer parent.Close()
			case "replaced-parent":
				if err := os.Rename(parent.Name(), parent.Name()+"-old"); err != nil {
					test.Fatal(err)
				}
				test.Cleanup(func() { _ = os.Remove(parent.Name() + "-old") })
				_ = os.Mkdir(parent.Name(), 0700)
			case "zero-attempt":
				expected = Attempt{}
			case "invalid-attempt":
				expected = Attempt{read: func() attemptData { return attemptData{instance: [16]byte{1}} }}
			case "foreign-instance":
				expected, _ = NewAttempt([16]byte{9}, [16]byte{2}, [16]byte{3})
			case "nil-consume":
				consume = nil
			case "pre-canceled":
				linked, cancel := context.WithCancel(ctx)
				cancel()
				ctx, wantError = linked, context.Canceled
			case "closed-target":
				_ = target.Close()
				wantError = ErrClosed
			}
			err := target.WithRestoreHead(ctx, parent, expected, consume)
			if !errors.Is(err, wantError) || called || len(fixture.snapshot()) != 0 {
				test.Fatalf("admission error=%v callback=%t requests=%d", err, called, len(fixture.snapshot()))
			}
			if variant != "closed-parent" {
				if _, err := originalParent.Stat(); err != nil {
					test.Fatal("invalid input closed borrowed parent")
				}
			}
			assertCopyPrivate(test, err, fixture)
		})
	}
}

func TestS3RestoreHeadRemoteFailures(test *testing.T) {
	for _, row := range []struct {
		name  string
		stage int
	}{
		{"missing-bootstrap", 1}, {"missing-complete", 3}, {"wrong-attempt", 1},
		{"bootstrap-short-length", 1}, {"bootstrap-long-length", 1},
		{"complete-zero-length", 3}, {"complete-oversize", 3},
		{"missing-length", 1}, {"head-bad-time", 1}, {"head-wrong-status", 1},
		{"short-bootstrap-body", 2}, {"short-complete-body", 4},
		{"get-wrong-range", 4}, {"get-long-length", 4}, {"get-encoding", 4},
		{"denied", 1}, {"no-retry", 3}, {"redirect", 1}, {"error-cap", 1},
	} {
		test.Run(row.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			parent := restoreHeadParent(test)
			expected, bootstrap, complete := seedRestoreHeads(test, fixture, target, 256)
			if row.name == "wrong-attempt" {
				expected, _ = NewAttempt([16]byte{1}, [16]byte{2}, [16]byte{4})
			}
			captured := make(chan *operation, 4)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				captured <- copyActiveOperation(target)
				if len(fixture.snapshot()) != row.stage || row.name == "wrong-attempt" {
					return false
				}
				if strings.HasPrefix(row.name, "missing-") && row.name != "missing-length" {
					fixtureError(writer, 404, "NoSuchKey")
					return true
				}
				if row.name == "denied" || row.name == "no-retry" || row.name == "error-cap" {
					writer.WriteHeader(503)
					if row.name == "error-cap" {
						_, _ = io.WriteString(writer, strings.Repeat("x", 128<<10))
					} else {
						_, _ = io.WriteString(writer, "<Error><Code>AccessDenied</Code><Message>PRIVATE-SDK-DETAIL SYNTHETIC-TOKEN</Message></Error>")
					}
					return true
				}
				if row.name == "redirect" {
					writer.Header().Set("Location", fixture.server.URL+"/forbidden")
					writer.WriteHeader(307)
					return true
				}
				size, payload := int64(156), bootstrap
				if row.stage >= 3 {
					size, payload = 256, complete
				}
				writer.Header().Set("Content-Length", fmt.Sprint(size))
				writer.Header().Set("Last-Modified", "Tue, 14 Nov 2023 22:13:20 GMT")
				writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", size-1, size))
				switch row.name {
				case "bootstrap-short-length":
					writer.Header().Set("Content-Length", "155")
				case "bootstrap-long-length":
					writer.Header().Set("Content-Length", "157")
				case "complete-zero-length":
					writer.Header().Set("Content-Length", "0")
				case "complete-oversize":
					writer.Header().Set("Content-Length", "16385")
				case "missing-length":
					writer.Header().Del("Content-Length")
				case "head-bad-time":
					writer.Header().Del("Last-Modified")
				case "head-wrong-status":
					writer.WriteHeader(206)
					return true
				case "get-wrong-range":
					writer.Header().Set("Content-Range", "bytes 1-256/257")
				case "get-long-length":
					writer.Header().Set("Content-Length", "257")
				case "get-encoding":
					writer.Header().Set("Content-Encoding", "identity")
				}
				if request.Method == http.MethodHead {
					writer.WriteHeader(200)
					return true
				}
				writer.WriteHeader(206)
				if strings.HasPrefix(row.name, "short-") {
					payload = payload[:16]
				}
				_, _ = writer.Write(payload)
				return true
			}
			called := false
			err := target.WithRestoreHead(test.Context(), parent, expected, func(context.Context, *os.File, *os.File) error { called = true; return nil })
			if !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrDenied) || called || len(fixture.snapshot()) != row.stage {
				test.Fatalf("remote failure error=%v callback=%t requests=%d", err, called, len(fixture.snapshot()))
			}
			assertCopyBodiesClosed(test, <-captured, row.stage)
			assertRestoreHeadParentEmpty(test, parent)
			assertCopyPrivate(test, err, fixture)
			test.Logf("actual TLS variant=%s callback=false requests=%d local_stage_removed=true", row.name, row.stage)
		})
	}
}

func restoreHeadFaultTarget(test *testing.T, fixture *s3Fixture) *S3Target {
	test.Helper()
	source := fixture.target(test, false)
	target, err := NewS3Target(source.options)
	if err != nil {
		test.Fatal(err)
	}
	target.roots = source.roots
	test.Cleanup(func() { _ = target.Close() })
	return target
}

func assertRestoreHeadPrivate(test *testing.T, err error, fixture *s3Fixture, parent *os.File) {
	test.Helper()
	assertCopyPrivate(test, err, fixture)
	formatted := fmt.Sprintf("%v %+v %#v %q", err, err, err, err)
	if strings.Contains(formatted, parent.Name()) || strings.Contains(formatted, "PRIVATE-HEAD-") || errors.Unwrap(err) != nil {
		test.Fatal("restore scope leaked raw local/callback error details")
	}
}

func TestS3RestoreHeadResponseCloseFailures(test *testing.T) {
	for _, row := range []struct {
		name  string
		fault int
	}{
		{"clean", 0}, {"bootstrap-head", 1}, {"bootstrap-get", 2},
		{"complete-head", 3}, {"complete-get", 4}, {"missing-head", 1}, {"canceled-close", 4},
	} {
		test.Run(row.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := restoreHeadFaultTarget(test, fixture)
			parent := restoreHeadParent(test)
			expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			if row.name == "missing-head" {
				fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
					fixtureError(writer, 404, "NoSuchKey")
					return true
				}
			}
			options := target.options.read()
			var captured *operation
			closeCount := 0
			install := sync.OnceFunc(func() {
				captured = copyActiveOperation(target)
				if captured == nil {
					return
				}
				physical := captured.transport
				test.Cleanup(physical.CloseIdleConnections)
				captured.transport = &http.Transport{}
				captured.transport.RegisterProtocol("https", responseCloseTransport{physical, func() error {
					closeCount++
					if closeCount == row.fault {
						if row.name == "canceled-close" {
							cancel()
						}
						return errors.New("PRIVATE-CLOSE-DETAIL SYNTHETIC-TOKEN " + fixture.server.URL)
					}
					return nil
				}})
			})
			target.options = S3Options{read: func() optionData { install(); return options }}
			called := false
			err := target.WithRestoreHead(ctx, parent, expected, func(context.Context, *os.File, *os.File) error {
				called = true
				return nil
			})
			requests := row.fault
			if row.fault == 0 {
				requests = 4
				if err != nil || !called {
					test.Fatal("response close clean control failed")
				}
			} else if !errors.Is(err, ErrUnavailable) || called {
				test.Fatalf("physical response close failure error=%v callback=%t", err, called)
			}
			if closeCount != requests || len(fixture.snapshot()) != requests {
				test.Fatalf("response closes=%d requests=%d want=%d", closeCount, len(fixture.snapshot()), requests)
			}
			assertCopyBodiesClosed(test, captured, requests)
			assertRestoreHeadParentEmpty(test, parent)
			assertRestoreHeadPrivate(test, err, fixture, parent)
			for range 2 {
				closeError := target.Close()
				if row.fault != 0 && !errors.Is(closeError, ErrUnavailable) || row.fault == 0 && closeError != nil {
					test.Fatal("target Close lost physical response close outcome")
				}
			}
			test.Logf("actual TLS variant=%s physical_closes=%d callback=%t stage_removed=true", row.name, closeCount, called)
		})
	}
}

func TestS3RestoreHeadLocalFailures(test *testing.T) {
	for _, row := range []struct {
		name        string
		wantError   error
		called      bool
		quarantined bool
	}{
		{"clean", nil, true, false},
		{"open-write", ErrUnavailable, false, false},
		{"open-read", ErrUnavailable, false, false},
		{"write-sync", ErrUnavailable, false, false},
		{"write-close", ErrSealUncertain, false, true},
		{"write-close-open", ErrSealUncertain, false, true},
		{"read-close", ErrSealUncertain, true, true},
		{"read-close-open", ErrSealUncertain, true, true},
		{"pre-callback-directory-sync", ErrSealCleanup, false, false},
		{"post-callback-directory-sync", ErrSealCleanup, true, true},
		{"unlink", ErrSealCleanup, true, true},
		{"discard", ErrSealCleanup, true, true},
		{"discard-uncertain", ErrSealUncertain, true, true},
		{"callback-error", ErrUnavailable, true, false},
		{"callback-cleanup", ErrSealCleanup, true, false},
		{"callback-uncertain", ErrSealUncertain, true, false},
		{"cancel-callback-uncertain", ErrSealUncertain, true, false},
		{"callback-error-unlink", ErrSealCleanup, true, true},
		{"cancel-read-close", ErrSealUncertain, true, true},
	} {
		test.Run(row.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := restoreHeadFaultTarget(test, fixture)
			parent := restoreHeadParent(test)
			expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			ops := defaultRestoreHeadOps()
			writeHandles := make(map[*os.File]bool)
			writeCloses, readCloses, directorySyncs, unlinks := 0, 0, 0, 0
			faultReached := false
			var retained *os.File
			ops.open = func(directory *sealDirectory, name string, write bool) (*os.File, error) {
				if row.name == "open-write" && write || row.name == "open-read" && !write {
					faultReached = true
					return nil, errors.New("PRIVATE-HEAD-OPEN " + parent.Name())
				}
				file, err := sealOpen(directory, name, write)
				if err == nil {
					writeHandles[file] = write
				}
				return file, err
			}
			ops.sync = func(file *os.File) error {
				info, err := file.Stat()
				if err != nil {
					return err
				}
				if info.IsDir() {
					directorySyncs++
				}
				if row.name == "write-sync" && !info.IsDir() || row.name == "pre-callback-directory-sync" && directorySyncs == 1 && info.IsDir() || row.name == "post-callback-directory-sync" && directorySyncs == 2 && info.IsDir() {
					faultReached = true
					return errors.New("PRIVATE-HEAD-SYNC " + parent.Name())
				}
				return file.Sync()
			}
			ops.close = func(file *os.File) error {
				write := writeHandles[file]
				if write {
					writeCloses++
				} else {
					readCloses++
				}
				if !faultReached && (row.name == "write-close-open" && write || row.name == "read-close-open" && !write) {
					faultReached, retained = true, file
					return errors.New("PRIVATE-CLOSE-DETAIL before physical close")
				}
				err := file.Close()
				if !faultReached && (row.name == "write-close" && write || row.name == "read-close" && !write || row.name == "cancel-read-close" && !write) {
					faultReached = true
					if row.name == "cancel-read-close" {
						cancel()
					}
					return errors.New("PRIVATE-CLOSE-DETAIL " + parent.Name())
				}
				return err
			}
			ops.unlink = func(directory *sealDirectory, name string) error {
				unlinks++
				if strings.Contains(row.name, "unlink") && name == "bootstrap" {
					faultReached = true
					return errors.New("PRIVATE-HEAD-UNLINK " + parent.Name())
				}
				return sealUnlink(directory, name)
			}
			ops.discard = func(directory *sealDirectory) error {
				if strings.HasPrefix(row.name, "discard") {
					faultReached = true
					if row.name == "discard-uncertain" {
						return ErrSealUncertain
					}
					return errors.New("PRIVATE-HEAD-DISCARD " + parent.Name())
				}
				return discardSealDirectory(directory)
			}
			called := false
			err := target.withRestoreHead(ctx, parent, expected, func(callbackContext context.Context, bootstrap, complete *os.File) error {
				called = true
				if writeCloses != 2 || readCloses != 0 {
					return ErrInvalid
				}
				if row.name == "callback-cleanup" || row.name == "callback-uncertain" || row.name == "cancel-callback-uncertain" {
					faultReached = true
					controlled := ErrSealUncertain
					if row.name == "callback-cleanup" {
						controlled = ErrSealCleanup
					}
					if row.name == "cancel-callback-uncertain" {
						cancel()
					}
					return errors.Join(controlled, errors.New("PRIVATE-HEAD-CALLBACK"))
				}
				if strings.HasPrefix(row.name, "callback-error") {
					faultReached = true
					return errors.New("PRIVATE-HEAD-CALLBACK SYNTHETIC-TOKEN " + parent.Name())
				}
				return callbackContext.Err()
			}, ops)
			if !errors.Is(err, row.wantError) || called != row.called || row.name != "clean" && !faultReached {
				test.Fatalf("local boundary error=%v want=%v callback=%t fault=%t", err, row.wantError, called, faultReached)
			}
			if row.called && (writeCloses != 2 || readCloses != 2 || unlinks != 2) {
				test.Fatalf("local cleanup not joined: RW_closes=%d RO_closes=%d unlinks=%d", writeCloses, readCloses, unlinks)
			}
			if row.quarantined {
				before := len(fixture.snapshot())
				if err := target.WithRestoreHead(test.Context(), parent, expected, func(context.Context, *os.File, *os.File) error { return nil }); !errors.Is(err, ErrClosed) || len(fixture.snapshot()) != before {
					test.Fatal("uncertain cleanup admitted another scoped stage")
				}
				if err := target.Close(); !errors.Is(err, ErrUnavailable) {
					test.Fatal("quarantine lost terminal failure")
				}
			} else {
				assertRestoreHeadParentEmpty(test, parent)
			}
			if retained != nil {
				if _, err := retained.Stat(); err != nil {
					test.Fatal("uncertain FD ownership was lost or Close was retried")
				}
				test.Cleanup(func() { _ = retained.Close() })
			}
			assertRestoreHeadPrivate(test, err, fixture, parent)
			test.Logf("actual TLS local=%s callback=%t RW_close_attempts=%d RO_close_attempts=%d unlinks=%d quarantined=%t uncertain_live_FD_retained=%t", row.name, called, writeCloses, readCloses, unlinks, row.quarantined, retained != nil)
		})
	}
}

func TestS3RestoreHeadCloseJoinsCallback(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	parent := restoreHeadParent(test)
	expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseCallback := sync.OnceFunc(func() { close(release) })
	defer releaseCallback()
	var borrowed []*os.File
	done := make(chan copyOutcome, 1)
	go func() {
		err := target.WithRestoreHead(test.Context(), parent, expected, func(ctx context.Context, bootstrap, complete *os.File) error {
			borrowed = []*os.File{bootstrap, complete}
			close(entered)
			<-release
			for _, file := range borrowed {
				if _, err := file.Stat(); err != nil {
					return ErrInvalid
				}
			}
			return ctx.Err()
		})
		done <- copyOutcome{err: err}
	}()
	awaitCopySignal(test, entered)
	active := copyActiveOperation(target)
	if active == nil || active.maximum != 4 {
		test.Fatal("head scope did not retain its bounded operation lease")
	}
	deadline, ok := active.ctx.Deadline()
	if !ok || time.Until(deadline) > 120*time.Second || time.Until(deadline) < 110*time.Second {
		test.Fatal("head scope did not inherit the operation timeout")
	}
	active.mutex.Lock()
	bodies := append([]*boundedBody(nil), active.bodies...)
	active.mutex.Unlock()
	for _, body := range bodies {
		body.mutex.Lock()
		closed := body.closed
		body.mutex.Unlock()
		if !closed {
			test.Fatal("consume ran before a physical response body closed")
		}
	}
	closed := make(chan copyOutcome, 2)
	for range 2 {
		go func() { closed <- copyOutcome{err: target.Close()} }()
	}
	awaitCopySignal(test, active.ctx.Done())
	select {
	case <-closed:
		test.Fatal("target Close did not wait for consume")
	default:
	}
	for _, file := range borrowed {
		if _, err := file.Stat(); err != nil {
			test.Fatal("target Close reclaimed a borrowed head before consume returned")
		}
	}
	releaseCallback()
	outcome := receiveCopyOutcome(test, done)
	if !errors.Is(outcome.err, context.Canceled) {
		test.Fatal("callback cancellation was lost")
	}
	for range 2 {
		if outcome := receiveCopyOutcome(test, closed); outcome.err != nil {
			test.Fatal(outcome.err)
		}
	}
	assertCopyBodiesClosed(test, active, 4)
	assertRestoreHeadParentEmpty(test, parent)
	for _, file := range borrowed {
		if _, err := file.Stat(); err == nil {
			test.Fatal("scope retained borrowed FD after consume joined")
		}
	}
	test.Log("actual TLS responses_closed_before_callback=4 concurrent_Close_joined=2 callback_FDs_held_until_return=true leases=0")
}

func TestS3RestoreHeadCancellationStages(test *testing.T) {
	for _, stage := range []int{1, 2, 3, 4} {
		for _, variant := range []string{"cancel", "deadline", "target-close"} {
			test.Run(fmt.Sprintf("stage=%d/%s", stage, variant), func(test *testing.T) {
				fixture := newFixture(test)
				target := fixture.target(test, false)
				parent := restoreHeadParent(test)
				expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
				entered, departed := make(chan struct{}), make(chan struct{})
				release := make(chan struct{})
				defer close(release)
				captured := make(chan *operation, 1)
				fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
					if len(fixture.snapshot()) != stage {
						return false
					}
					captured <- copyActiveOperation(target)
					if request.Method == http.MethodGet {
						size := 156
						if stage == 4 {
							size = 256
						}
						writer.Header().Set("Content-Length", fmt.Sprint(size))
						writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", size-1, size))
						writer.WriteHeader(206)
						_, _ = writer.Write([]byte{1})
						writer.(http.Flusher).Flush()
					}
					close(entered)
					select {
					case <-request.Context().Done():
					case <-release:
					}
					close(departed)
					return true
				}
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				if variant == "deadline" {
					linked, deadlineCancel := context.WithTimeout(ctx, 250*time.Millisecond)
					defer deadlineCancel()
					ctx = linked
				}
				called := false
				done := make(chan copyOutcome, 1)
				go func() {
					err := target.WithRestoreHead(ctx, parent, expected, func(context.Context, *os.File, *os.File) error { called = true; return nil })
					done <- copyOutcome{err: err}
				}()
				awaitCopySignal(test, entered)
				if variant == "cancel" {
					cancel()
				} else if variant == "target-close" {
					if err := target.Close(); err != nil {
						test.Fatal(err)
					}
				}
				outcome := receiveCopyOutcome(test, done)
				wantError := context.Canceled
				if variant == "deadline" {
					wantError = context.DeadlineExceeded
				}
				if !errors.Is(outcome.err, wantError) || called || len(fixture.snapshot()) != stage {
					test.Fatalf("cancellation error=%v callback=%t requests=%d", outcome.err, called, len(fixture.snapshot()))
				}
				awaitCopySignal(test, departed)
				operation := <-captured
				operation.mutex.Lock()
				registered := len(operation.bodies)
				operation.mutex.Unlock()
				if registered < stage-1 || registered > stage {
					test.Fatal("unexpected response registration count")
				}
				assertCopyBodiesClosed(test, operation, registered)
				assertRestoreHeadParentEmpty(test, parent)
				assertRestoreHeadPrivate(test, outcome.err, fixture, parent)
				test.Logf("actual TLS stage=%d cancellation=%s callback=false registered_bodies_closed=%d callbacks_joined=1 stage_removed=true", stage, variant, registered)
			})
		}
	}
}

func TestS3RestoreHeadTwoSlotsAndIsolation(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	parent := restoreHeadParent(test)
	expected, _, _ := seedRestoreHeads(test, fixture, target, 16<<10)
	entered := make(chan struct{}, 2)
	releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
	finishFirst := sync.OnceFunc(func() { close(releaseFirst) })
	finishSecond := sync.OnceFunc(func() { close(releaseSecond) })
	defer finishFirst()
	defer finishSecond()
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	firstDone, secondDone := make(chan copyOutcome, 1), make(chan copyOutcome, 1)
	for index := range 2 {
		go func() {
			jobContext, release, done := test.Context(), releaseSecond, secondDone
			if index == 0 {
				jobContext, release, done = ctx, releaseFirst, firstDone
			}
			err := target.WithRestoreHead(jobContext, parent, expected, func(callbackContext context.Context, bootstrap, complete *os.File) error {
				entered <- struct{}{}
				<-release
				return callbackContext.Err()
			})
			done <- copyOutcome{err: err}
		}()
	}
	for range 2 {
		awaitCopySignal(test, entered)
	}
	entries, err := os.ReadDir(parent.Name())
	if err != nil || len(entries) != 2 {
		test.Fatal("two admitted head scopes did not have separate private directories")
	}
	var diskBytes int64
	for _, directory := range entries {
		files, err := os.ReadDir(filepath.Join(parent.Name(), directory.Name()))
		if err != nil || len(files) != 2 {
			test.Fatal("head scope staging was not bounded to two files")
		}
		for _, file := range files {
			info, err := file.Info()
			if err != nil {
				test.Fatal(err)
			}
			diskBytes += info.Size()
		}
	}
	if diskBytes != 2*(156+16<<10) {
		test.Fatalf("two-scope disk bound=%d", diskBytes)
	}
	before := len(fixture.snapshot())
	if err := target.WithRestoreHead(test.Context(), parent, expected, func(context.Context, *os.File, *os.File) error { return nil }); !errors.Is(err, ErrBusy) {
		test.Fatal("third head scope admitted")
	}
	ref, _ := NewObjectRef(expected, Bootstrap, 0, 156, [32]byte{})
	if _, err := target.CopyExact(test.Context(), ref, io.Discard); !errors.Is(err, ErrBusy) || len(fixture.snapshot()) != before {
		test.Fatal("head scopes did not share S3 target admission with CopyExact")
	}
	cancel()
	select {
	case <-firstDone:
		test.Fatal("caller cancellation returned before its callback")
	default:
	}
	finishFirst()
	if outcome := receiveCopyOutcome(test, firstDone); !errors.Is(outcome.err, context.Canceled) {
		test.Fatal("caller cancellation lost")
	}
	select {
	case <-secondDone:
		test.Fatal("one scope canceled its peer")
	default:
	}
	finishSecond()
	if outcome := receiveCopyOutcome(test, secondDone); outcome.err != nil {
		test.Fatal("independent head scope failed")
	}
	assertRestoreHeadParentEmpty(test, parent)
	test.Logf("actual TLS admitted=2 third_head_and_CopyExact=Busy disk_bytes=%d per_scope_max=16540 canceled_scope_joined=true peer_succeeded=true requests=8", diskBytes)
}

func TestS3RestoreHeadPinnedAttemptAndRejectedPayload(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	parent := restoreHeadParent(test)
	expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
	changing := expected.read()
	var identityMutex sync.Mutex
	mutable := Attempt{read: func() attemptData {
		identityMutex.Lock()
		defer identityMutex.Unlock()
		return changing
	}}
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		identityMutex.Lock()
		changing.attempt = [16]byte{99}
		identityMutex.Unlock()
		return false
	}
	engine, _, _ := sealFixture(test)
	called := false
	err := target.WithRestoreHead(test.Context(), parent, mutable, func(ctx context.Context, bootstrap, complete *os.File) error {
		called = true
		session, err := engine.BeginOpen(ctx, bootstrap, complete, expected)
		if err == nil {
			_ = session.Close()
			return nil
		}
		return err
	})
	if !called || err == nil || len(fixture.snapshot()) != 4 {
		test.Fatal("untrusted head transport was mistaken for authenticated restore capability")
	}
	for index, call := range fixture.snapshot() {
		kind, size := Bootstrap, int64(156)
		if index >= 2 {
			kind, size = Complete, 256
		}
		ref, _ := NewObjectRef(expected, kind, 0, size, [32]byte{})
		if call.key != objectKey(target.options.read(), ref.read()) {
			test.Fatal("mutable expected attempt changed a canonical request binding")
		}
	}
	assertRestoreHeadParentEmpty(test, parent)
	assertRestoreHeadPrivate(test, err, fixture, parent)
	test.Log("actual TLS expected_attempt_frozen=true arbitrary_head_bytes_rejected_by_BeginOpen=true trusted_capability_issued=false")
}

func TestS3RestoreHeadIdentityChanges(test *testing.T) {
	for _, variant := range []string{"closed-borrow", "changed-head", "replaced-head", "replaced-stage", "replaced-parent", "public-parent"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newFixture(test)
			target := restoreHeadFaultTarget(test, fixture)
			parent := restoreHeadParent(test)
			expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
			foreignName := ""
			err := target.WithRestoreHead(test.Context(), parent, expected, func(ctx context.Context, bootstrap, complete *os.File) error {
				switch variant {
				case "closed-borrow":
					return bootstrap.Close()
				case "changed-head":
					writer, err := os.OpenFile(bootstrap.Name(), os.O_WRONLY, 0)
					if err != nil {
						return err
					}
					_, writeError := writer.WriteAt([]byte{99}, 0)
					return errors.Join(writeError, writer.Close())
				case "replaced-head":
					if err := os.Rename(bootstrap.Name(), bootstrap.Name()+"-saved"); err != nil {
						return err
					}
					foreignName = bootstrap.Name()
				case "replaced-stage", "replaced-parent":
					name := filepath.Dir(bootstrap.Name())
					if variant == "replaced-parent" {
						name = parent.Name()
					}
					if err := os.Rename(name, name+"-saved"); err != nil {
						return err
					}
					if err := os.Mkdir(name, 0700); err != nil {
						return err
					}
					foreignName = filepath.Join(name, "foreign")
				case "public-parent":
					return parent.Chmod(0755)
				}
				if foreignName != "" {
					return os.WriteFile(foreignName, []byte("foreign-entry-must-survive"), 0600)
				}
				return ctx.Err()
			})
			wantError := ErrSealCleanup
			if variant == "closed-borrow" {
				wantError = ErrSealUncertain
			}
			if !errors.Is(err, wantError) {
				test.Fatalf("identity mutation error=%v want=%v", err, wantError)
			}
			if foreignName != "" {
				payload, err := os.ReadFile(foreignName)
				if err != nil || string(payload) != "foreign-entry-must-survive" {
					test.Fatal("cleanup removed or changed a foreign directory entry")
				}
			}
			if _, err := parent.Stat(); err != nil {
				test.Fatal("identity mutation closed caller parent FD")
			}
			assertRestoreHeadPrivate(test, err, fixture, parent)
			test.Logf("actual TLS identity=%s final_scope_failed=true foreign_entries_preserved=true", variant)
		})
	}
}

func TestS3RestoreHeadPhysicalCloseBarrier(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	parent := restoreHeadParent(test)
	expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
	entered, release := make(chan struct{}), make(chan struct{})
	releaseClose := sync.OnceFunc(func() { close(release) })
	defer releaseClose()
	options := target.options.read()
	var captured *operation
	closeCount := 0
	install := sync.OnceFunc(func() {
		captured = copyActiveOperation(target)
		physical := captured.transport
		test.Cleanup(physical.CloseIdleConnections)
		captured.transport = &http.Transport{}
		captured.transport.RegisterProtocol("https", responseCloseTransport{physical, func() error {
			closeCount++
			if closeCount == 4 {
				close(entered)
				<-release
			}
			return nil
		}})
	})
	target.options = S3Options{read: func() optionData { install(); return options }}
	called := false
	done := make(chan copyOutcome, 1)
	go func() {
		err := target.WithRestoreHead(test.Context(), parent, expected, func(context.Context, *os.File, *os.File) error { called = true; return nil })
		done <- copyOutcome{err: err}
	}()
	awaitCopySignal(test, entered)
	closed := make(chan copyOutcome, 1)
	go func() { closed <- copyOutcome{err: target.Close()} }()
	awaitCopySignal(test, captured.ctx.Done())
	select {
	case <-closed:
		test.Fatal("target Close returned before response Close completed")
	default:
	}
	releaseClose()
	outcome := receiveCopyOutcome(test, done)
	if !errors.Is(outcome.err, context.Canceled) || called || closeCount != 4 {
		test.Fatalf("response Close barrier error=%v callback=%t closes=%d", outcome.err, called, closeCount)
	}
	if outcome := receiveCopyOutcome(test, closed); outcome.err != nil {
		test.Fatal(outcome.err)
	}
	assertCopyBodiesClosed(test, captured, 4)
	assertRestoreHeadParentEmpty(test, parent)
	test.Log("actual TLS physical_response_Close_barrier=1 consume=false target_Close_waited=true physical_closes=4 leases=0")
}

func TestS3RestoreHeadEmptyStageIdentity(test *testing.T) {
	fixture := newFixture(test)
	target := restoreHeadFaultTarget(test, fixture)
	parent := restoreHeadParent(test)
	expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
	foreign := make(chan string, 1)
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		entries, err := os.ReadDir(parent.Name())
		if err != nil || len(entries) != 1 {
			fixtureError(writer, 500, "InternalError")
			return true
		}
		name := filepath.Join(parent.Name(), entries[0].Name())
		if os.Rename(name, name+"-saved") != nil || os.Mkdir(name, 0700) != nil {
			fixtureError(writer, 500, "InternalError")
			return true
		}
		foreign <- name
		fixtureError(writer, 404, "NoSuchKey")
		return true
	}
	err := target.WithRestoreHead(test.Context(), parent, expected, func(context.Context, *os.File, *os.File) error { return ErrInvalid })
	if !errors.Is(err, ErrSealCleanup) {
		test.Fatalf("empty stage replacement error=%v want cleanup failure", err)
	}
	select {
	case name := <-foreign:
		if info, err := os.Stat(name); err != nil || !info.IsDir() {
			test.Fatal("cleanup removed a foreign empty replacement directory")
		}
	default:
		test.Fatal("empty directory replacement fault not exercised")
	}
	test.Log("actual TLS empty_stage_replacement_detected=true foreign_empty_directory_preserved=true callback=false")
}

func TestS3RestoreHeadFeedsExistingBeginOpen(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = bootstrap.Close() })
	digest := sha256.Sum256([]byte("synthetic bounded plaintext"))
	hash := hex.EncodeToString(digest[:])
	var records []SealExtentRecord
	for ordinal, kind := range []string{"database", "inventory"} {
		artifact, err := engine.SealExtent(test.Context(), attempt, uint32(ordinal), source)
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { _ = artifact.Close() })
		ref := artifact.Ref().read()
		records = append(records, SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: kind, FileID: kind, PlaintextBytes: 27, PlaintextSHA256: hash, CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
	}
	page, err := engine.SealPage(test.Context(), attempt, 1, 2, func(yield func(SealExtentRecord, error) bool) {
		for _, record := range records {
			if !yield(record, nil) {
				return
			}
		}
	})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = page.Close() })
	root, err := engine.SealRoot(test.Context(), attempt, SealMetadata{AppVersion: "test", SchemaSHA256: hash, CutID: "01000000000000000000000000000000", MasterKeyID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Database: SealFileDescription{Bytes: 27, SHA256: hash, Extents: 1}, Inventory: SealInventoryDescription{Bytes: 27, SHA256: hash, Extents: 1, Records: 1, Owners: 1}, ClosureSHA256: hash, ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = root.Close() })
	complete, err := engine.SealComplete(test.Context(), attempt)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = complete.Close() })
	fixture := newFixture(test)
	target := fixture.target(test, false)
	parent := restoreHeadParent(test)
	for _, artifact := range []SealedFile{bootstrap, complete} {
		err := artifact.WithFile(test.Context(), func(input *os.File) error {
			file, err := os.CreateTemp(fixture.dir, "authenticated-head-")
			if err != nil {
				return err
			}
			size, copyError := io.Copy(file, input)
			closeError := file.Close()
			if copyError != nil || closeError != nil {
				return errors.Join(copyError, closeError)
			}
			key := objectKey(target.options.read(), artifact.Ref().read())
			fixture.mutex.Lock()
			fixture.objects[key] = fixtureObject{file.Name(), `"opaque-authenticated-head"`, size}
			fixture.mutex.Unlock()
			return nil
		})
		if err != nil {
			test.Fatal(err)
		}
	}
	identity := attempt.get().identity
	expected, _ := NewAttempt(identity.instance, identity.backup, identity.attempt)
	called := false
	err = target.WithRestoreHead(test.Context(), parent, expected, func(ctx context.Context, bootstrap, complete *os.File) error {
		session, err := engine.BeginOpen(ctx, bootstrap, complete, expected)
		if err != nil {
			return err
		}
		called = true
		return session.Close()
	})
	if err != nil || !called || len(fixture.snapshot()) != 4 {
		test.Fatalf("existing BeginOpen compatibility error=%v called=%t", err, called)
	}
	assertRestoreHeadParentEmpty(test, parent)
	test.Log("actual TLS original_seal_bootstrap_and_complete=true BeginOpen_authenticated=true requests=4 no_bundle_or_activation_issued=true")
}

type restoreHeadEOFTransport struct{ physical *http.Transport }

func (transport restoreHeadEOFTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.physical.RoundTrip(request)
	if err == nil && request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/bootstrap/") {
		response.Body = copyReadOverlay{ReadCloser: response.Body, read: func([]byte) (int, error) { return 0, io.EOF }}
	}
	return response, err
}

func TestS3RestoreHeadNoAferoReadRetry(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	parent := restoreHeadParent(test)
	expected, _, _ := seedRestoreHeads(test, fixture, target, 256)
	options := target.options.read()
	var captured *operation
	install := sync.OnceFunc(func() {
		captured = copyActiveOperation(target)
		physical := captured.transport
		test.Cleanup(physical.CloseIdleConnections)
		captured.transport = &http.Transport{}
		captured.transport.RegisterProtocol("https", restoreHeadEOFTransport{physical})
	})
	target.options = S3Options{read: func() optionData { install(); return options }}
	called := false
	err := target.WithRestoreHead(test.Context(), parent, expected, func(context.Context, *os.File, *os.File) error { called = true; return nil })
	if !errors.Is(err, ErrUnavailable) || called || len(fixture.snapshot()) != 2 {
		test.Fatalf("early EOF retried: error=%v callback=%t requests=%d want=2", err, called, len(fixture.snapshot()))
	}
	assertCopyBodiesClosed(test, captured, 2)
	assertRestoreHeadParentEmpty(test, parent)
	test.Log("actual TLS early_body_EOF=true Afero_reopen_dispatched=false HEAD=1 GET=1 callback=false stage_removed=true")
}
