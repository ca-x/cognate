package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type copyCandidateWriter struct {
	bytes.Buffer
	closed   bool
	onWrite  func([]byte) (int, error)
	maxWrite int
}

func (writer *copyCandidateWriter) Write(buffer []byte) (int, error) {
	writer.maxWrite = max(writer.maxWrite, len(buffer))
	if writer.onWrite != nil {
		return writer.onWrite(buffer)
	}
	return writer.Buffer.Write(buffer)
}

func (writer *copyCandidateWriter) Close() error {
	writer.closed = true
	return nil
}

func seedCopyObject(test *testing.T, fixture *s3Fixture, target *S3Target, size int64) (ObjectRef, []byte) {
	test.Helper()
	file, ref := sealedFile(test, size)
	ciphertext, err := os.ReadFile(file.Name())
	if err != nil {
		test.Fatal(err)
	}
	key := objectKey(target.options.read(), ref.read())
	fixture.mutex.Lock()
	fixture.objects[key] = fixtureObject{file.Name(), `"opaque-copy-candidate"`, size}
	fixture.mutex.Unlock()
	return ref, ciphertext
}

func assertCopyProtocol(test *testing.T, fixture *s3Fixture, target *S3Target, ref ObjectRef) {
	test.Helper()
	calls := fixture.snapshot()
	wantKey := objectKey(target.options.read(), ref.read())
	if len(calls) != 3 {
		test.Fatalf("copy protocol requests=%d, want probe+HEAD+GET", len(calls))
	}
	for index, call := range calls {
		if call.key != wantKey || call.query != "x-id=GetObject" && call.query != "" {
			test.Fatal("copy left the explicit object binding")
		}
		wantMethod, wantRange := "GET", "bytes=0-0"
		if index == 1 {
			wantMethod, wantRange = "HEAD", ""
		}
		if index == 2 {
			wantRange = fmt.Sprintf("bytes=0-%d", ref.read().size-1)
		}
		if call.method != wantMethod || call.rangeHeader != wantRange {
			test.Fatalf("request %d: method=%s range=%s", index, call.method, call.rangeHeader)
		}
	}
}

func TestS3CopyExactCopiesBoundCiphertext(test *testing.T) {
	for _, size := range []int64{1, 65537} {
		test.Run(fmt.Sprint(size), func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			ref, ciphertext := seedCopyObject(test, fixture, target, size)
			candidate := &copyCandidateWriter{}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			if result != Verified || err != nil {
				test.Fatalf("copy result=%v error=%v, want Verified", result, err)
			}
			if !bytes.Equal(candidate.Bytes(), ciphertext) || candidate.closed {
				test.Fatal("ciphertext changed or borrowed writer closed")
			}
			assertCopyProtocol(test, fixture, target, ref)
			if _, err := io.WriteString(candidate, "caller-still-owns-writer"); err != nil {
				test.Fatal(err)
			}
			test.Logf("actual TLS bytes=%d probe=1 HEAD=1 full_GET=1 mutations=0 borrowed_writer_open=true", size)
		})
	}
}

func copyActiveOperation(target *S3Target) *operation {
	target.mutex.Lock()
	defer target.mutex.Unlock()
	if len(target.active) != 1 {
		return nil
	}
	for operation := range target.active {
		return operation
	}
	return nil
}

func assertCopyBodiesClosed(test *testing.T, operation *operation, want int) {
	test.Helper()
	if operation == nil {
		test.Fatal("expected one captured copy lease")
	}
	operation.mutex.Lock()
	bodies := append([]*boundedBody(nil), operation.bodies...)
	operation.mutex.Unlock()
	if len(bodies) != want {
		test.Fatalf("registered response bodies=%d want=%d", len(bodies), want)
	}
	for _, body := range bodies {
		body.mutex.Lock()
		closed := body.closed
		body.mutex.Unlock()
		if !closed {
			test.Fatal("response body left open")
		}
	}
	operation.target.mutex.Lock()
	remaining := len(operation.target.active)
	operation.target.mutex.Unlock()
	if remaining != 0 {
		test.Fatal("copy lease remains after return")
	}
}

func assertCopyPrivate(test *testing.T, err error, fixture *s3Fixture) {
	test.Helper()
	var logs strings.Builder
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("copy candidate", "error", err)
	formatted := logs.String() + fmt.Sprintf("%v %+v %#v %q", err, err, err, err)
	for _, forbidden := range []string{fixture.server.URL, "https://", "synthetic-access", "SYNTHETIC-PRIVATE-SECRET", "SYNTHETIC-TOKEN", "PRIVATE-READER-DETAIL", "PRIVATE-WRITER-DETAIL", "PRIVATE-CLOSE-DETAIL", "PRIVATE-SDK-DETAIL"} {
		if strings.Contains(formatted, forbidden) {
			test.Fatal("public error leaked private transport or caller details")
		}
	}
}

func TestS3CopyExactInvalidAdmission(test *testing.T) {
	for _, variant := range []string{"nil-context", "nil-target", "zero-target", "zero-ref", "nil-writer", "typed-nil-writer", "foreign-instance", "pre-canceled", "closed"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			_, ref := sealedFile(test, 64)
			ctx := test.Context()
			candidate := &copyCandidateWriter{}
			var output io.Writer = candidate
			wantError := ErrInvalid
			switch variant {
			case "nil-context":
				ctx = nil
			case "nil-target":
				target = nil
			case "zero-target":
				target = &S3Target{}
			case "zero-ref":
				ref = ObjectRef{}
			case "nil-writer":
				output = nil
			case "typed-nil-writer":
				output = (*copyCandidateWriter)(nil)
			case "foreign-instance":
				attempt, _ := NewAttempt([16]byte{9}, [16]byte{2}, [16]byte{3})
				ref, _ = NewObjectRef(attempt, DataExtent, 0, 64, ref.read().digest)
			case "pre-canceled":
				linked, cancel := context.WithCancel(ctx)
				cancel()
				ctx, wantError = linked, context.Canceled
			case "closed":
				if err := target.Close(); err != nil {
					test.Fatal(err)
				}
				wantError = ErrClosed
			}
			result, err := target.CopyExact(ctx, ref, output)
			if result != VerifyUnknown || !errors.Is(err, wantError) || len(fixture.snapshot()) != 0 || candidate.Len() != 0 || candidate.closed {
				test.Fatalf("invalid admission result=%v error=%v requests=%d", result, err, len(fixture.snapshot()))
			}
			assertCopyPrivate(test, err, fixture)
		})
	}
}

func TestS3CopyExactMissingAndServiceErrors(test *testing.T) {
	for _, row := range []struct {
		name       string
		status     int
		code       string
		stage      int
		wantResult VerifyResult
		wantError  error
	}{
		{"typed-missing", 404, "NoSuchKey", 1, Missing, nil},
		{"generic-404", 404, "NotFound", 1, VerifyUnknown, ErrUnavailable},
		{"bucket-404", 404, "NoSuchBucket", 1, VerifyUnknown, ErrUnavailable},
		{"malformed-404", 404, "", 1, VerifyUnknown, ErrUnavailable},
		{"denied", 403, "AccessDenied", 1, VerifyUnknown, ErrDenied},
		{"bad-access", 403, "InvalidAccessKeyId", 1, VerifyUnknown, ErrDenied},
		{"bad-signature", 403, "SignatureDoesNotMatch", 1, VerifyUnknown, ErrDenied},
		{"no-retry", 503, "SlowDown", 1, VerifyUnknown, ErrUnavailable},
		{"error-body-cap", 500, "oversize", 1, VerifyUnknown, ErrUnavailable},
		{"head-missing-is-uncertain", 404, "NoSuchKey", 2, VerifyUnknown, ErrUnavailable},
		{"head-denied", 403, "AccessDenied", 2, VerifyUnknown, ErrUnavailable},
		{"full-missing-is-uncertain", 404, "NoSuchKey", 3, VerifyUnknown, ErrUnavailable},
		{"full-denied", 403, "AccessDenied", 3, VerifyUnknown, ErrUnavailable},
		{"full-no-retry", 503, "SlowDown", 3, VerifyUnknown, ErrUnavailable},
	} {
		test.Run(row.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			ref, _ := seedCopyObject(test, fixture, target, 65537)
			captured := make(chan *operation, 3)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				captured <- copyActiveOperation(target)
				if len(fixture.snapshot()) != row.stage {
					return false
				}
				writer.WriteHeader(row.status)
				if row.code == "oversize" {
					_, _ = io.WriteString(writer, strings.Repeat("x", 128<<10))
				} else if row.code == "" {
					_, _ = io.WriteString(writer, "PRIVATE-SDK-DETAIL malformed response")
				} else {
					_, _ = fmt.Fprintf(writer, "<Error><Code>%s</Code><Message>PRIVATE-SDK-DETAIL SYNTHETIC-PRIVATE-SECRET SYNTHETIC-TOKEN %s</Message></Error>", row.code, fixture.server.URL)
				}
				return true
			}
			candidate := &copyCandidateWriter{}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			if result != row.wantResult || !errors.Is(err, row.wantError) || candidate.Len() != 0 || candidate.closed {
				test.Fatalf("service result=%v error=%v candidate_bytes=%d", result, err, candidate.Len())
			}
			if len(fixture.snapshot()) != row.stage {
				test.Fatal("service error retried or used a fallback request")
			}
			assertCopyBodiesClosed(test, <-captured, row.stage)
			assertCopyPrivate(test, err, fixture)
		})
	}
}

type copyReadOverlay struct {
	io.ReadCloser
	read func([]byte) (int, error)
}

func (reader copyReadOverlay) Read(buffer []byte) (int, error) {
	return reader.read(buffer)
}

func overlayCopyBody(target *S3Target, overlay func(*boundedBody)) (*operation, error) {
	operation := copyActiveOperation(target)
	if operation == nil {
		return nil, ErrInvalid
	}
	operation.mutex.Lock()
	bodies := append([]*boundedBody(nil), operation.bodies...)
	operation.mutex.Unlock()
	if len(bodies) != 3 {
		return nil, ErrInvalid
	}
	body := bodies[2]
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return nil, ErrClosed
	}
	overlay(body)
	return operation, nil
}

func TestS3CopyExactReaderAndCloseFailures(test *testing.T) {
	for _, variant := range []string{"clean", "reader-error", "reader-error-at-end", "overflow", "close-failure", "conflict-and-close-failure", "writer-and-close-failure"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newFixture(test)
			source := fixture.target(test, false)
			target, err := NewS3Target(source.options)
			if err != nil {
				test.Fatal(err)
			}
			target.roots = source.roots
			test.Cleanup(func() { _ = target.Close() })
			ref, ciphertext := seedCopyObject(test, fixture, target, 65537)
			if variant == "conflict-and-close-failure" {
				data := ref.read()
				attempt, _ := NewAttempt(data.attempt.instance, data.attempt.backup, data.attempt.attempt)
				ref, _ = NewObjectRef(attempt, data.kind, data.ordinal, data.size, sha256.Sum256([]byte("conflicting ciphertext")))
			}
			candidate := &copyCandidateWriter{}
			var captured *operation
			var overlayError error
			closeCount := 0
			candidate.onWrite = func(buffer []byte) (int, error) {
				if captured == nil {
					captured, overlayError = overlayCopyBody(target, func(body *boundedBody) {
						physical := body.body
						if variant == "reader-error" || variant == "reader-error-at-end" {
							seen := body.read
							body.body = copyReadOverlay{ReadCloser: physical, read: func(buffer []byte) (int, error) {
								count, err := physical.Read(buffer)
								seen += int64(count)
								if variant == "reader-error" || seen == ref.read().size {
									return count, errors.New("PRIVATE-READER-DETAIL " + fixture.server.URL)
								}
								return count, err
							}}
						}
						if variant == "overflow" {
							body.body = struct {
								io.Reader
								io.Closer
							}{io.MultiReader(physical, bytes.NewReader([]byte{1})), physical}
						}
						body.body = closeOverlay{body.body, func() error {
							closeCount++
							if strings.Contains(variant, "close-failure") {
								return errors.New("PRIVATE-CLOSE-DETAIL SYNTHETIC-PRIVATE-SECRET " + fixture.server.URL)
							}
							return nil
						}}
					})
					if overlayError != nil {
						return 0, overlayError
					}
				}
				count, err := candidate.Buffer.Write(buffer)
				if variant == "writer-and-close-failure" {
					return count, errors.New("PRIVATE-WRITER-DETAIL")
				}
				return count, err
			}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			if overlayError != nil || closeCount != 1 || candidate.closed {
				test.Fatalf("physical body injection=%v closes=%d writer_closed=%t", overlayError, closeCount, candidate.closed)
			}
			wantResult, wantError := VerifyUnknown, ErrUnavailable
			if variant == "clean" {
				wantResult, wantError = Verified, nil
			}
			if result != wantResult || !errors.Is(err, wantError) {
				test.Fatalf("reader/cleanup result=%v error=%v bytes=%d", result, err, candidate.Len())
			}
			if variant == "clean" || variant == "reader-error-at-end" || variant == "overflow" || variant == "close-failure" || variant == "conflict-and-close-failure" {
				if !bytes.Equal(candidate.Bytes(), ciphertext) {
					test.Fatal("test did not exercise a complete staged candidate")
				}
			}
			assertCopyBodiesClosed(test, captured, 3)
			assertCopyProtocol(test, fixture, target, ref)
			assertCopyPrivate(test, err, fixture)
			for range 2 {
				closeError := target.Close()
				if strings.Contains(variant, "close-failure") && !errors.Is(closeError, ErrUnavailable) || !strings.Contains(variant, "close-failure") && closeError != nil {
					test.Fatal("target Close lost the terminal physical close outcome")
				}
				assertCopyPrivate(test, closeError, fixture)
			}
			test.Logf("actual TLS variant=%s full_GET=1 bodies_closed=3 physical_full_close=1 candidate_bytes=%d result=%d", variant, candidate.Len(), result)
		})
	}
}

type copyOutcome struct {
	result VerifyResult
	err    error
}

func receiveCopyOutcome(test *testing.T, done <-chan copyOutcome) copyOutcome {
	test.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(5 * time.Second):
		test.Fatal("copy did not finish joining")
		return copyOutcome{}
	}
}

func awaitCopySignal(test *testing.T, signal <-chan struct{}) {
	test.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		test.Fatal("copy barrier not reached")
	}
}

func TestS3CopyExactCancellationStages(test *testing.T) {
	for _, stage := range []int{1, 2, 3} {
		for _, variant := range []string{"cancel", "deadline", "target-close"} {
			test.Run(fmt.Sprintf("stage=%d/%s", stage, variant), func(test *testing.T) {
				fixture := newFixture(test)
				target := fixture.target(test, false)
				ref, _ := seedCopyObject(test, fixture, target, 65537)
				entered, departed := make(chan struct{}), make(chan struct{})
				release := make(chan struct{})
				defer close(release)
				captured := make(chan *operation, 1)
				fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
					if len(fixture.snapshot()) != stage {
						return false
					}
					captured <- copyActiveOperation(target)
					if stage == 3 {
						writer.Header().Set("Content-Length", "65537")
						writer.Header().Set("Content-Range", "bytes 0-65536/65537")
						writer.WriteHeader(206)
						_, _ = writer.Write([]byte{7})
						writer.(http.Flusher).Flush()
					}
					if stage != 3 {
						close(entered)
					}
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
					deadlineContext, deadlineCancel := context.WithTimeout(ctx, 250*time.Millisecond)
					defer deadlineCancel()
					ctx = deadlineContext
				}
				candidate := &copyCandidateWriter{}
				if stage == 3 {
					firstWrite := sync.OnceFunc(func() { close(entered) })
					candidate.onWrite = func(buffer []byte) (int, error) {
						count, err := candidate.Buffer.Write(buffer)
						firstWrite()
						return count, err
					}
				}
				done := make(chan copyOutcome, 1)
				go func() {
					result, err := target.CopyExact(ctx, ref, candidate)
					done <- copyOutcome{result, err}
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
				if outcome.result != VerifyUnknown || !errors.Is(outcome.err, wantError) || candidate.closed {
					test.Fatalf("cancel result=%v error=%v", outcome.result, outcome.err)
				}
				awaitCopySignal(test, departed)
				registered := stage - 1
				if stage == 3 {
					registered = 3
				}
				assertCopyBodiesClosed(test, <-captured, registered)
				if len(fixture.snapshot()) != stage {
					test.Fatal("canceled copy dispatched more requests")
				}
				assertCopyPrivate(test, outcome.err, fixture)
				test.Logf("actual TLS stage=%d cancellation=%s requests=%d candidate_bytes=%d leases=0 callbacks_joined=1", stage, variant, stage, candidate.Len())
			})
		}
	}
}

func TestS3CopyExactTwoSlotsAndCloseDrain(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	ref, _ := seedCopyObject(test, fixture, target, 65537)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	releaseWriters := sync.OnceFunc(func() { close(release) })
	defer releaseWriters()
	done := make(chan copyOutcome, 2)
	for range 2 {
		go func() {
			candidate := &copyCandidateWriter{}
			candidate.onWrite = func(buffer []byte) (int, error) {
				if candidate.Len() == 0 {
					entered <- struct{}{}
					<-release
				}
				return candidate.Buffer.Write(buffer)
			}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			if candidate.closed {
				err = ErrInvalid
			}
			done <- copyOutcome{result, err}
		}()
	}
	for range 2 {
		awaitCopySignal(test, entered)
	}
	target.mutex.Lock()
	operations := make([]*operation, 0, 2)
	for operation := range target.active {
		operations = append(operations, operation)
	}
	target.mutex.Unlock()
	if len(operations) != 2 {
		test.Fatal("copy did not retain both leases while writing")
	}
	for _, operation := range operations {
		deadline, ok := operation.ctx.Deadline()
		if !ok || time.Until(deadline) > 120*time.Second || time.Until(deadline) < 110*time.Second || operation.maximum != 3 {
			test.Fatal("copy did not inherit the bounded operation lifetime/request budget")
		}
	}
	before := len(fixture.snapshot())
	if result, err := target.CopyExact(test.Context(), ref, io.Discard); result != VerifyUnknown || !errors.Is(err, ErrBusy) {
		test.Fatal("third copy was admitted")
	}
	if _, err := target.VerifyExact(test.Context(), ref); !errors.Is(err, ErrBusy) {
		test.Fatal("copy slots are not shared with existing verification")
	}
	if len(fixture.snapshot()) != before {
		test.Fatal("busy operation dispatched a request")
	}
	closed := make(chan copyOutcome, 2)
	for range 2 {
		go func() { closed <- copyOutcome{err: target.Close()} }()
	}
	for _, operation := range operations {
		awaitCopySignal(test, operation.ctx.Done())
	}
	select {
	case <-closed:
		test.Fatal("Close returned while a borrowed writer was still running")
	default:
	}
	if len(target.options.read().credentials.read().secret) == 0 {
		test.Fatal("credentials cleared before operation drain")
	}
	releaseWriters()
	for range 2 {
		outcome := receiveCopyOutcome(test, done)
		if outcome.result != VerifyUnknown || !errors.Is(outcome.err, context.Canceled) {
			test.Fatal("Close cancellation became a verified candidate")
		}
		if outcome := receiveCopyOutcome(test, closed); outcome.err != nil {
			test.Fatal("Close failed joining copies")
		}
	}
	for _, operation := range operations {
		assertCopyBodiesClosed(test, operation, 3)
	}
	if target.options.read != nil {
		test.Fatal("credentials survived complete Close drain")
	}
	if result, err := target.CopyExact(test.Context(), ref, io.Discard); result != VerifyUnknown || !errors.Is(err, ErrClosed) {
		test.Fatal("closed target admitted a copy")
	}
	test.Log("actual TLS admitted=2 copy_and_verify_third=Busy requests=6 concurrent_Close_joined=2 bodies_closed=6 leases=0 operation_timeout=120s maximum_requests=3")
}

func TestS3CopyExactUntrustedCandidates(test *testing.T) {
	for _, variant := range []string{"bad-hash", "short-body", "long-length", "short-length", "wrong-range", "encoding", "head-length", "head-time", "writer-error", "writer-full-error", "writer-short", "writer-invalid-count"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			ref, ciphertext := seedCopyObject(test, fixture, target, 65537)
			wantResult, wantError := VerifyUnknown, ErrUnavailable
			candidate := &copyCandidateWriter{}
			if variant == "bad-hash" {
				data := ref.read()
				attempt, _ := NewAttempt(data.attempt.instance, data.attempt.backup, data.attempt.attempt)
				ref, _ = NewObjectRef(attempt, data.kind, data.ordinal, data.size, sha256.Sum256([]byte("different ciphertext")))
				wantResult, wantError = VerifyConflict, nil
			}
			if strings.HasPrefix(variant, "writer-") {
				candidate.onWrite = func(buffer []byte) (int, error) {
					count := len(buffer)
					if variant != "writer-full-error" {
						count /= 2
					}
					_, _ = candidate.Buffer.Write(buffer[:count])
					if variant == "writer-invalid-count" {
						return 1 << 20, nil
					}
					if variant == "writer-short" {
						return count, nil
					}
					return count, errors.New("PRIVATE-WRITER-DETAIL SYNTHETIC-PRIVATE-SECRET " + fixture.server.URL)
				}
			}
			captured := make(chan *operation, 3)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				captured <- copyActiveOperation(target)
				if request.Method == http.MethodHead && strings.HasPrefix(variant, "head-") {
					writer.Header().Set("Content-Length", "65537")
					writer.Header().Set("Last-Modified", "Tue, 14 Nov 2023 22:13:20 GMT")
					if variant == "head-length" {
						writer.Header().Set("Content-Length", "65538")
					} else {
						writer.Header().Del("Last-Modified")
					}
					writer.WriteHeader(200)
					return true
				}
				if request.Method != http.MethodGet || request.Header.Get("Range") == "bytes=0-0" || variant == "bad-hash" || strings.HasPrefix(variant, "writer-") {
					return false
				}
				writer.Header().Set("Content-Length", "65537")
				writer.Header().Set("Content-Range", "bytes 0-65536/65537")
				switch variant {
				case "long-length":
					writer.Header().Set("Content-Length", "65538")
				case "short-length":
					writer.Header().Set("Content-Length", "65536")
				case "wrong-range":
					writer.Header().Set("Content-Range", "bytes 1-65537/65538")
				case "encoding":
					writer.Header().Set("Content-Encoding", "identity")
				}
				writer.WriteHeader(206)
				if variant == "short-body" {
					_, _ = writer.Write(ciphertext[:32768])
				} else {
					_, _ = writer.Write(ciphertext)
				}
				return true
			}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			if result != wantResult || !errors.Is(err, wantError) || candidate.closed {
				test.Fatalf("untrusted candidate result=%v error=%v bytes=%d", result, err, candidate.Len())
			}
			if variant == "bad-hash" && !bytes.Equal(candidate.Bytes(), ciphertext) {
				test.Fatal("bad hash test must leave a full untrusted candidate")
			}
			if (variant == "short-body" || strings.HasPrefix(variant, "writer-")) && (candidate.Len() == 0 || candidate.Len() >= len(ciphertext)) {
				test.Fatal("failure did not exercise partial irreversible output")
			}
			requests := 3
			if strings.HasPrefix(variant, "head-") {
				requests = 2
			}
			assertCopyBodiesClosed(test, <-captured, requests)
			if len(fixture.snapshot()) != requests {
				test.Fatal("failed copy retried or dispatched a mutation")
			}
			assertCopyPrivate(test, err, fixture)
			test.Logf("actual TLS variant=%s candidate_bytes=%d result=%d requests=%d writer_open=true", variant, candidate.Len(), result, requests)
		})
	}
}

func TestS3CopyExactPhysicalCloseDrain(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	ref, _ := seedCopyObject(test, fixture, target, 65537)
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseClose := sync.OnceFunc(func() { close(release) })
	defer releaseClose()
	var captured *operation
	var overlayError error
	closeCount := 0
	candidate := &copyCandidateWriter{}
	candidate.onWrite = func(buffer []byte) (int, error) {
		if captured == nil {
			captured, overlayError = overlayCopyBody(target, func(body *boundedBody) {
				body.body = closeOverlay{body.body, func() error {
					closeCount++
					close(entered)
					<-release
					return nil
				}}
			})
			if overlayError != nil {
				return 0, overlayError
			}
		}
		return candidate.Buffer.Write(buffer)
	}
	done := make(chan copyOutcome, 1)
	go func() {
		result, err := target.CopyExact(test.Context(), ref, candidate)
		done <- copyOutcome{result, err}
	}()
	awaitCopySignal(test, entered)
	closed := make(chan copyOutcome, 1)
	go func() { closed <- copyOutcome{err: target.Close()} }()
	awaitCopySignal(test, captured.ctx.Done())
	select {
	case <-closed:
		test.Fatal("target Close returned before physical response Close")
	default:
	}
	target.mutex.Lock()
	active := len(target.active)
	target.mutex.Unlock()
	if active != 1 || target.options.read == nil {
		test.Fatal("physical Close released the lease or credentials early")
	}
	releaseClose()
	outcome := receiveCopyOutcome(test, done)
	if outcome.result != VerifyUnknown || !errors.Is(outcome.err, context.Canceled) || overlayError != nil || closeCount != 1 || candidate.closed {
		test.Fatalf("physical close drain result=%v error=%v closes=%d", outcome.result, outcome.err, closeCount)
	}
	if outcome := receiveCopyOutcome(test, closed); outcome.err != nil {
		test.Fatal(outcome.err)
	}
	assertCopyBodiesClosed(test, captured, 3)
	test.Log("actual TLS physical_Close_barrier=1 lease_held=true Close_waited=true body_closed_once=true result=Unknown leases=0")
}

func TestS3CopyExactWriterCancellationAndIsolation(test *testing.T) {
	fixture := newFixture(test)
	first := fixture.target(test, false)
	second := fixture.target(test, false)
	ref, ciphertext := seedCopyObject(test, fixture, first, 65537)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	firstCandidate := &copyCandidateWriter{}
	firstCandidate.onWrite = func(buffer []byte) (int, error) {
		count, err := firstCandidate.Buffer.Write(buffer)
		if firstCandidate.Len() == len(ciphertext) {
			cancel()
		}
		return count, err
	}
	result, err := first.CopyExact(ctx, ref, firstCandidate)
	if result != VerifyUnknown || !errors.Is(err, context.Canceled) || !bytes.Equal(firstCandidate.Bytes(), ciphertext) || firstCandidate.closed {
		test.Fatal("cancellation during the final write became a trusted candidate")
	}
	if err := first.Close(); err != nil {
		test.Fatal(err)
	}
	secondCandidate := &copyCandidateWriter{}
	result, err = second.CopyExact(test.Context(), ref, secondCandidate)
	if result != Verified || err != nil || !bytes.Equal(secondCandidate.Bytes(), ciphertext) || secondCandidate.closed {
		test.Fatal("cancellation or Close corrupted an independent target")
	}
	if len(fixture.snapshot()) != 6 {
		test.Fatal("isolated copies exceeded their protocol request budgets")
	}
	test.Log("actual TLS canceled_final_write_full_candidate=Unknown independent_target_after_Close=Verified total_requests=6")
}

type copyDigestWriter struct {
	writer   io.Writer
	size     int64
	maxWrite int
}

func (writer *copyDigestWriter) Write(buffer []byte) (int, error) {
	writer.maxWrite = max(writer.maxWrite, len(buffer))
	count, err := writer.writer.Write(buffer)
	writer.size += int64(count)
	return count, err
}

func TestS3CopyExactObjectKindsAndMaximum(test *testing.T) {
	for _, row := range []struct {
		kind ObjectKind
		size int64
	}{{DataExtent, MaxCiphertextBytes}, {ManifestPage, 1 << 20}, {Bootstrap, 16 << 10}, {Complete, 16 << 10}} {
		test.Run(fmt.Sprintf("kind=%d/bytes=%d", row.kind, row.size), func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			file, original := sealedFile(test, row.size)
			data := original.read()
			attempt, _ := NewAttempt(data.attempt.instance, data.attempt.backup, data.attempt.attempt)
			ref, err := NewObjectRef(attempt, row.kind, 0, row.size, data.digest)
			if err != nil {
				test.Fatal(err)
			}
			key := objectKey(target.options.read(), ref.read())
			fixture.mutex.Lock()
			fixture.objects[key] = fixtureObject{file.Name(), `"opaque-maximum"`, row.size}
			fixture.mutex.Unlock()
			digest := sha256.New()
			candidate := &copyDigestWriter{writer: digest}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			if result != Verified || err != nil || candidate.size != row.size || candidate.maxWrite > 32<<10 || !bytes.Equal(digest.Sum(nil), data.digest[:]) {
				test.Fatalf("maximum copy result=%v error=%v bytes=%d max_write=%d", result, err, candidate.size, candidate.maxWrite)
			}
			assertCopyProtocol(test, fixture, target, ref)
			if _, err := NewObjectRef(attempt, row.kind, 0, row.size+1, data.digest); !errors.Is(err, ErrInvalid) {
				test.Fatal("out-of-bound ref was constructible")
			}
			test.Logf("actual TLS kind=%d ciphertext_bytes=%d max_output_write=%d full_GET=1 request_count=3", row.kind, row.size, candidate.maxWrite)
		})
	}
}

func TestS3CopyExactEarlyResponseCloseFailures(test *testing.T) {
	for _, row := range []struct {
		name     string
		fault    int
		requests int
	}{
		{"clean-control", 0, 3},
		{"probe-close", 1, 1},
		{"missing-close", 1, 1},
		{"rejected-head-close", 2, 2},
		{"rejected-full-close", 3, 3},
	} {
		test.Run(row.name, func(test *testing.T) {
			fixture := newFixture(test)
			source := fixture.target(test, false)
			target, err := NewS3Target(source.options)
			if err != nil {
				test.Fatal(err)
			}
			target.roots = source.roots
			test.Cleanup(func() { _ = target.Close() })
			ref, _ := seedCopyObject(test, fixture, target, 65537)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				switch {
				case row.name == "missing-close":
					fixtureError(writer, 404, "NoSuchKey")
					return true
				case row.name == "rejected-head-close" && request.Method == http.MethodHead:
					writer.Header().Set("Content-Length", "65538")
					writer.WriteHeader(200)
					return true
				case row.name == "rejected-full-close" && request.Header.Get("Range") == "bytes=0-65536":
					writer.Header().Set("Content-Length", "65537")
					writer.Header().Set("Content-Range", "bytes 1-65537/65538")
					writer.WriteHeader(206)
					return true
				}
				return false
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
						return errors.New("PRIVATE-CLOSE-DETAIL SYNTHETIC-TOKEN " + fixture.server.URL)
					}
					return nil
				}})
			})
			target.options = S3Options{read: func() optionData {
				install()
				return options
			}}
			candidate := &copyCandidateWriter{}
			result, err := target.CopyExact(test.Context(), ref, candidate)
			wantResult, wantError := VerifyUnknown, ErrUnavailable
			if row.fault == 0 {
				wantResult, wantError = Verified, nil
			}
			if result != wantResult || !errors.Is(err, wantError) || candidate.closed || closeCount != row.requests || len(fixture.snapshot()) != row.requests {
				test.Fatalf("early cleanup result=%v error=%v physical_closes=%d requests=%d", result, err, closeCount, len(fixture.snapshot()))
			}
			if row.fault != 0 && candidate.Len() != 0 {
				test.Fatal("rejected early response reached the borrowed writer")
			}
			assertCopyBodiesClosed(test, captured, row.requests)
			assertCopyPrivate(test, err, fixture)
			for range 2 {
				closeError := target.Close()
				if row.fault != 0 && !errors.Is(closeError, ErrUnavailable) || row.fault == 0 && closeError != nil {
					test.Fatal("early response close failure lost from target Close")
				}
				assertCopyPrivate(test, closeError, fixture)
			}
			test.Logf("actual TLS variant=%s physical_response_closes=%d registered_bodies_closed=%d result=%d", row.name, closeCount, row.requests, result)
		})
	}
}
