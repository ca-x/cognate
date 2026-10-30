package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClosedTargetRejects(test *testing.T) {
	target := newFixture(test).target(test, false)
	file, ref := sealedFile(test, 1)
	if err := target.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := target.PutSealed(test.Context(), ref, file); err != ErrClosed {
		test.Fatalf("closed admission: %v", err)
	}
}

func TestTwoSlotsCloseCancelsAndJoins(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	_, ref := sealedFile(test, 64)
	entered := make(chan struct{}, 2)
	left := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		entered <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-release:
		}
		left <- struct{}{}
		return true
	}
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := target.VerifyExact(test.Context(), ref); done <- err }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			test.Fatal("request not started")
		}
	}
	before := len(fixture.snapshot())
	if _, err := target.VerifyExact(test.Context(), ref); !errors.Is(err, ErrBusy) {
		test.Fatalf("third admission %v", err)
	}
	if len(fixture.snapshot()) != before {
		test.Fatal("Busy dispatched")
	}
	closed := make(chan error, 2)
	for range 2 {
		go func() { closed <- target.Close() }()
	}
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				test.Fatal("cancel result", err)
			}
		case <-time.After(5 * time.Second):
			test.Fatal("operation did not join")
		}
	}
	for range 2 {
		select {
		case err := <-closed:
			if err != nil {
				test.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			test.Fatal("Close did not join")
		}
	}
	for range 2 {
		select {
		case <-left:
		case <-time.After(5 * time.Second):
			test.Fatal("fixture callback not reclaimed")
		}
	}
	target.mutex.Lock()
	active := len(target.active)
	target.mutex.Unlock()
	if active != 0 {
		test.Fatal("operation lease remains")
	}
	test.Log("actual TLS admitted=2 third=Busy operations_joined=2 fixture_callbacks_joined=2 remaining_leases=0")
}

func TestTargetCancellationIsolation(test *testing.T) {
	fixture := newFixture(test)
	first := fixture.target(test, true)
	second := fixture.target(test, true)
	_, ref := sealedFile(test, 64)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		entered <- struct{}{}
		select {
		case <-request.Context().Done():
			return true
		case <-release:
			return false
		}
	}
	firstDone := make(chan error, 1)
	secondDone := make(chan VerifyResult, 1)
	go func() { _, err := first.VerifyExact(test.Context(), ref); firstDone <- err }()
	go func() { result, _ := second.VerifyExact(test.Context(), ref); secondDone <- result }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			test.Fatal("isolation requests not started")
		}
	}
	if err := first.Close(); err != nil {
		test.Fatal(err)
	}
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		test.Fatal(err)
	}
	select {
	case <-secondDone:
		test.Fatal("one target canceled its peer")
	default:
	}
	once.Do(func() { close(release) })
	select {
	case result := <-secondDone:
		if result != Missing {
			test.Fatal("independent result", result)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("peer did not finish")
	}
}

func TestCompleteLostReceiptReadOnlyReconciliation(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 512)
	data := ref.read()
	ref, _ = NewObjectRef(Attempt{read: func() attemptData { return data.attempt }}, Complete, 0, data.size, data.digest)
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method != "PUT" {
			return false
		}
		stored, err := os.CreateTemp(fixture.dir, "lost-complete-")
		if err != nil {
			panic(err)
		}
		size, err := io.Copy(stored, request.Body)
		if err != nil {
			stored.Close()
			return true
		}
		stored.Close()
		fixture.mutex.Lock()
		fixture.objects[key] = fixtureObject{stored.Name(), `"not-a-sha"`, size}
		fixture.mutex.Unlock()
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		connection.Close()
		return true
	}
	outcome, _ := target.PutSealed(test.Context(), ref, file)
	if outcome != PutUnknown {
		test.Fatal("lost receipt became success", outcome)
	}
	target.Close()
	successor := fixture.target(test, true)
	if result, err := successor.VerifyExact(test.Context(), ref); result != Verified || err != nil {
		test.Fatal("restart read-only witness", result, err)
	}
	puts := 0
	for _, call := range fixture.snapshot() {
		if call.method == "PUT" {
			puts++
		}
	}
	if puts != 1 {
		test.Fatal("complete replayed")
	}
	test.Log("actual lost complete reply=Unknown successor=VerifyExact total_puts=1")
}

type countedReader struct {
	io.ReadCloser
	count int64
}

func (reader *countedReader) Read(buffer []byte) (int, error) {
	count, err := reader.ReadCloser.Read(buffer)
	reader.count += int64(count)
	return count, err
}

type closeOverlay struct {
	io.ReadCloser
	close func() error
}

func (body closeOverlay) Close() error {
	err := body.ReadCloser.Close()
	overlay := body.close()
	return errors.Join(err, overlay)
}

type responseCloseTransport struct {
	transport *http.Transport
	close     func() error
}

func (transport responseCloseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.transport.RoundTrip(request)
	if err == nil {
		response.Body = closeOverlay{response.Body, transport.close}
	}
	return response, err
}

func TestRejectedResponseRetainsPhysicalCloseOutcome(test *testing.T) {
	for _, variant := range []string{"head", "range", "encoding", "registered"} {
		for _, fault := range []bool{false, true} {
			test.Run(fmt.Sprintf("%s/fault=%t", variant, fault), func(test *testing.T) {
				fixture := newFixture(test)
				source := fixture.target(test, false)
				target, err := NewS3Target(source.options)
				if err != nil {
					test.Fatal("target unavailable")
				}
				target.roots = source.roots
				test.Cleanup(func() { _ = target.Close() })
				file, ref := sealedFile(test, 1)
				if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
					test.Fatal("setup publish failed")
				}
				fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
					writer.Header().Set("Content-Length", "1")
					if request.Method == http.MethodHead {
						writer.WriteHeader(http.StatusOK)
						return true
					}
					if variant != "range" {
						writer.Header().Set("Content-Range", "bytes 0-0/1")
					}
					if variant == "encoding" {
						writer.Header().Set("Content-Encoding", "identity")
					}
					writer.WriteHeader(http.StatusPartialContent)
					_, _ = writer.Write([]byte{7})
					return true
				}
				operation, err := target.begin(test.Context(), "verify", 1)
				if err != nil {
					test.Fatal("operation unavailable")
				}
				var ended sync.Once
				test.Cleanup(func() { ended.Do(operation.end) })
				if _, err := operation.bind(ref); err != nil {
					test.Fatal("binding failed")
				}
				physical := operation.transport
				defer physical.CloseIdleConnections()
				var closeCount int
				operation.transport = &http.Transport{}
				operation.transport.RegisterProtocol("https", responseCloseTransport{physical, func() error {
					closeCount++
					if fault {
						return errors.New("synthetic physical close failure")
					}
					return nil
				}})
				method := http.MethodGet
				if variant == "head" {
					method = http.MethodHead
				}
				request, err := http.NewRequestWithContext(test.Context(), method, fixture.server.URL+"/synthetic/"+operation.key, nil)
				if err != nil {
					test.Fatal("request unavailable")
				}
				request.Header.Set("Range", "bytes=0-0")
				response, err := operation.RoundTrip(request)
				if variant == "registered" {
					if err != nil {
						test.Fatal("valid response rejected")
					}
					if _, err := io.Copy(io.Discard, response.Body); err != nil {
						test.Fatal("valid response read failed")
					}
					if err := response.Body.Close(); (err != nil) != fault {
						test.Fatal("registered close outcome incorrect")
					}
				} else if response != nil || !errors.Is(err, ErrUnavailable) || operation.healthy() {
					test.Fatal("malformed response was not rejected")
				}
				ended.Do(operation.end)
				if closeCount != 1 {
					test.Fatal("physical close was not exercised exactly once")
				}
				for range 2 {
					err := target.Close()
					if fault && !errors.Is(err, ErrUnavailable) || !fault && err != nil {
						test.Fatal("physical close outcome lost at repeated target Close")
					}
				}
			})
		}
	}
}

func TestPreDecodeLimitsAndCheckedClose(test *testing.T) {
	for _, row := range []struct {
		mode   string
		limit  int64
		status int
	}{{"verify", 64 << 10, 500}, {"list", 256 << 10, 200}} {
		test.Run(row.mode, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, true)
			_, ref := sealedFile(test, 8)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				writer.WriteHeader(row.status)
				io.WriteString(writer, strings.Repeat("x", 512<<10))
				return true
			}
			operation, err := target.begin(test.Context(), row.mode, 1)
			if err != nil {
				test.Fatal(err)
			}
			defer operation.end()
			operation.bind(ref)
			endpoint := fixture.server.URL + "/synthetic/" + operation.key
			if row.mode == "list" {
				operation.key = ""
				operation.prefix = "prefix/"
				endpoint = fixture.server.URL + "/synthetic?list-type=2&max-keys=50&prefix=prefix%2F"
			}
			request, _ := http.NewRequestWithContext(test.Context(), "GET", endpoint, nil)
			response, err := operation.RoundTrip(request)
			if err != nil {
				test.Fatal(err)
			}
			bounded := response.Body.(*boundedBody)
			counted := &countedReader{ReadCloser: bounded.body}
			bounded.body = counted
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if err == nil || counted.count != row.limit+1 || operation.healthy() {
				test.Fatalf("predecode bound: count=%d error=%v healthy=%v", counted.count, err, operation.healthy())
			}
		})
	}
	fixture := newFixture(test)
	original := fixture.target(test, true)
	target, err := NewS3Target(original.options)
	if err != nil {
		test.Fatal(err)
	}
	target.roots = original.roots
	test.Cleanup(func() { target.Close() })
	file, ref := sealedFile(test, 1)
	if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
		test.Fatal(result, err)
	}
	operation, err := target.begin(test.Context(), "verify", 1)
	if err != nil {
		test.Fatal(err)
	}
	defer operation.end()
	operation.bind(ref)
	request, _ := http.NewRequestWithContext(test.Context(), "GET", fixture.server.URL+"/synthetic/"+operation.key, nil)
	request.Header.Set("Range", "bytes=0-0")
	response, err := operation.RoundTrip(request)
	if err != nil {
		test.Fatal(err)
	}
	bounded := response.Body.(*boundedBody)
	bounded.body = closeOverlay{bounded.body, func() error { return fmt.Errorf("SYNTHETIC-SECRET-CLOSE") }}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		test.Fatal(err)
	}
	if err := response.Body.Close(); err == nil || operation.healthy() {
		test.Fatal("underlying Close failure lost")
	}
}

func TestLeaseHeldThroughPhysicalBodyClose(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 1)
	if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
		test.Fatal(result, err)
	}
	operation, err := target.begin(test.Context(), "verify", 1)
	if err != nil {
		test.Fatal(err)
	}
	operation.bind(ref)
	request, _ := http.NewRequestWithContext(test.Context(), "GET", fixture.server.URL+"/synthetic/"+operation.key, nil)
	request.Header.Set("Range", "bytes=0-0")
	response, err := operation.RoundTrip(request)
	if err != nil {
		operation.end()
		test.Fatal(err)
	}
	bounded := response.Body.(*boundedBody)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	bounded.body = closeOverlay{bounded.body, func() error { close(entered); <-release; return nil }}
	done := make(chan struct{})
	go func() { operation.end(); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("Close overlay not entered")
	}
	target.mutex.Lock()
	active := len(target.active)
	target.mutex.Unlock()
	if active != 1 {
		test.Fatal("lease released before body Close")
	}
	select {
	case <-done:
		test.Fatal("end returned before body Close")
	default:
	}
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		test.Fatal("end did not reclaim resources")
	}
}

func TestCloseFailureIsTerminalAndPrivate(test *testing.T) {
	fixture := newFixture(test)
	source := fixture.target(test, true)
	options := source.options
	target, err := NewS3Target(options)
	if err != nil {
		test.Fatal(err)
	}
	target.roots = source.roots
	file, ref := sealedFile(test, 1)
	if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
		test.Fatal(result, err)
	}
	operation, err := target.begin(test.Context(), "verify", 1)
	if err != nil {
		test.Fatal(err)
	}
	operation.bind(ref)
	request, _ := http.NewRequestWithContext(test.Context(), "GET", fixture.server.URL+"/synthetic/"+operation.key, nil)
	request.Header.Set("Range", "bytes=0-0")
	response, err := operation.RoundTrip(request)
	if err != nil {
		operation.end()
		test.Fatal(err)
	}
	bounded := response.Body.(*boundedBody)
	bounded.body = closeOverlay{bounded.body, func() error { return errors.New("PRIVATE-CLOSE-DETAIL") }}
	operation.end()
	for range 2 {
		if err := target.Close(); !errors.Is(err, ErrUnavailable) || strings.Contains(fmt.Sprint(err), "PRIVATE-") {
			test.Fatalf("Close lost failure: %v", err)
		}
	}
}

func TestCallerCancellationDuringPutCannotBecomeVerified(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 1024)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method == "PUT" {
			cancel()
		}
		return false
	}
	result, err := target.PutSealed(ctx, ref, file)
	if result != PutUnknown || !errors.Is(err, context.Canceled) {
		test.Fatalf("canceled dispatch upgraded: %v %v", result, err)
	}
}

type borrowedBody struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (body *borrowedBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() { close(body.entered) })
	<-body.release
	return 0, io.EOF
}
func (body *borrowedBody) Close() error { return nil }

func TestRequestBodyBorrowJoinedBeforeReturn(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	_, ref := sealedFile(test, 8)
	replied := make(chan struct{})
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		writer.Header().Set("Connection", "close")
		fixtureError(writer, 403, "AccessDenied")
		writer.(http.Flusher).Flush()
		close(replied)
		return true
	}
	operation, err := target.begin(test.Context(), "put", 1)
	if err != nil {
		test.Fatal(err)
	}
	operation.bind(ref)
	body := &borrowedBody{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	defer once.Do(func() { close(body.release) })
	request, _ := http.NewRequestWithContext(test.Context(), "PUT", fixture.server.URL+"/synthetic/"+operation.key, body)
	request.ContentLength = 8
	done := make(chan struct{})
	go func() {
		response, _ := operation.RoundTrip(request)
		if response != nil {
			response.Body.Close()
		}
		operation.end()
		close(done)
	}()
	select {
	case <-body.entered:
	case <-time.After(5 * time.Second):
		test.Fatal("actual HTTP writer did not borrow source")
	}
	select {
	case <-replied:
	case <-time.After(5 * time.Second):
		test.Fatal("actual early response not sent")
	}
	early := false
	select {
	case <-done:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	once.Do(func() { close(body.release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		test.Fatal("request owner did not join after release")
	}
	if early {
		test.Fatal("returned while HTTP writer still borrowed source")
	}
}
