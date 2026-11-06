package githubproxysettings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticURL = "http://synthetic-proxy-user:synthetic-proxy-password@127.0.0.1:18080/"

func TestMain(main *testing.M) {
	guard := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	for name, value := range map[string]string{
		"HTTP_PROXY": guard.URL, "HTTPS_PROXY": guard.URL, "NO_PROXY": "",
		"http_proxy": "", "https_proxy": "", "no_proxy": "", "REQUEST_METHOD": "",
	} {
		if err := os.Setenv(name, value); err != nil {
			guard.Close()
			os.Exit(1)
		}
	}
	code := main.Run()
	guard.Close()
	os.Exit(code)
}

func testDirectory(test *testing.T) string {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	return directory
}

func testMaster() []byte { return bytes.Repeat([]byte{0x42}, 32) }

func openTestManager(test *testing.T, directory string) *Manager {
	test.Helper()
	manager, err := Open(test.Context(), directory, testMaster())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestEncryptedSettingsRestartAndReset(test *testing.T) {
	directory := testDirectory(test)
	master := testMaster()
	manager, err := Open(test.Context(), directory, master)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = manager.Close() })
	clear(master)
	initial, err := manager.Get(test.Context())
	if err != nil || initial != (Snapshot{Revision: 1}) {
		test.Fatalf("default settings: %+v, %v", initial, err)
	}
	snapshot, err := manager.Update(test.Context(), 1, syntheticURL)
	if err != nil || snapshot != (Snapshot{Revision: 2, Configured: true, Endpoint: "http://127.0.0.1:18080"}) {
		test.Fatalf("updated settings: %+v, %v", snapshot, err)
	}
	path := filepath.Join(directory, "network", settingsName)
	sealed, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	for _, secret := range []string{syntheticURL, "synthetic-proxy-user", "synthetic-proxy-password", `"proxy_url"`} {
		if bytes.Contains(sealed, []byte(secret)) {
			test.Fatal("settings file contains plaintext private configuration")
		}
	}
	for path, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			test.Fatal("settings path has unsafe permissions")
		}
	}
	assertPrivate(test, manager)
	assertPrivate(test, snapshot)
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		test.Fatal("close is not idempotent")
	}
	restarted := openTestManager(test, directory)
	restored, err := restarted.Get(test.Context())
	if err != nil || restored != snapshot {
		test.Fatalf("restored settings: %+v, %v", restored, err)
	}
	reset, err := restarted.Update(test.Context(), restored.Revision, "")
	if err != nil || reset != (Snapshot{Revision: 3}) {
		test.Fatalf("reset settings: %+v, %v", reset, err)
	}
	if err := restarted.Close(); err != nil {
		test.Fatal(err)
	}
	last := openTestManager(test, directory)
	if got, err := last.Get(test.Context()); err != nil || got != reset {
		test.Fatalf("reset did not survive restart: %+v, %v", got, err)
	}
}

func TestSettingsStrictRevisionAndInvalidInput(test *testing.T) {
	manager := openTestManager(test, testDirectory(test))
	for _, revision := range []int64{-1, 0, 2, math.MaxInt64} {
		got, err := manager.Update(test.Context(), revision, syntheticURL)
		if !errors.Is(err, ErrConflict) || got != (Snapshot{}) {
			test.Fatalf("stale revision was accepted: %+v, %v", got, err)
		}
	}
	for _, rawURL := range []string{"https://user:synthetic-proxy-password@127.0.0.1/path", "socks4://127.0.0.1:1", "http://127.0.0.1:0", "http://127.0.0.1?secret"} {
		got, err := manager.Update(test.Context(), 1, rawURL)
		if !errors.Is(err, ErrInvalid) || got != (Snapshot{}) {
			test.Fatalf("invalid URL was accepted: %+v, %v", got, err)
		}
		assertPrivate(test, err)
	}
	for revision := int64(1); revision <= 2; revision++ {
		got, err := manager.Update(test.Context(), revision, "")
		if err != nil || got.Revision != revision+1 {
			test.Fatalf("matching revision was not advanced: %+v, %v", got, err)
		}
	}
	if _, err := manager.Update(test.Context(), 2, ""); !errors.Is(err, ErrConflict) {
		test.Fatal("replaying an already committed update was accepted")
	}
	manager.mutex.Lock()
	manager.snapshot.Revision = math.MaxInt64
	manager.mutex.Unlock()
	if _, err := manager.Update(test.Context(), math.MaxInt64, ""); !errors.Is(err, ErrConflict) {
		test.Fatal("revision overflow was accepted")
	}
}

func TestSettingsCancellationAndOwnership(test *testing.T) {
	directory := testDirectory(test)
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("synthetic-proxy-password"))
	if manager, err := Open(ctx, directory, testMaster()); manager != nil || !errors.Is(err, context.Canceled) {
		test.Fatalf("canceled open: %v", err)
	}
	manager := openTestManager(test, directory)
	if duplicate, err := Open(test.Context(), directory, testMaster()); duplicate != nil || !errors.Is(err, ErrOwned) {
		test.Fatalf("duplicate manager acquired ownership: %v", err)
	}
	if _, err := manager.Get(ctx); !errors.Is(err, context.Canceled) {
		test.Fatal("Get ignores cancellation")
	}
	if _, err := manager.Update(ctx, 1, syntheticURL); !errors.Is(err, context.Canceled) {
		test.Fatal("Update ignores cancellation")
	}
	if _, err := manager.Get(nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil Get context was accepted")
	}
	if _, err := manager.Update(nil, 1, ""); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil Update context was accepted")
	}
	if err := manager.acquire(test.Context()); err != nil {
		test.Fatal(err)
	}
	waiting, stop := context.WithCancel(test.Context())
	result := make(chan error, 1)
	go func() {
		_, err := manager.Update(waiting, 1, syntheticURL)
		result <- err
	}()
	stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		test.Fatalf("waiting update did not cancel: %v", err)
	}
	manager.release()
	if got, err := manager.Get(test.Context()); err != nil || got != (Snapshot{Revision: 1}) {
		test.Fatal("canceled update modified settings")
	}
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := manager.Get(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("closed manager returned a snapshot")
	}
	if _, err := manager.Update(test.Context(), 1, ""); !errors.Is(err, ErrClosed) {
		test.Fatal("closed manager accepted an update")
	}
	_ = openTestManager(test, directory)
}

func TestSettingsConcurrentUpdates(test *testing.T) {
	manager := openTestManager(test, testDirectory(test))
	var successful atomic.Int64
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			before, err := manager.Get(test.Context())
			if err != nil {
				test.Error(err)
				return
			}
			_, err = manager.Update(test.Context(), before.Revision, syntheticURL)
			if err == nil {
				successful.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				test.Errorf("concurrent update: %v", err)
			}
		})
	}
	workers.Wait()
	after, err := manager.Get(test.Context())
	if err != nil || after.Revision != successful.Load()+1 || !after.Configured {
		test.Fatalf("concurrent revision accounting: %+v, successful=%d, %v", after, successful.Load(), err)
	}
}

func TestSettingsOpaqueDefaultLifecycle(test *testing.T) {
	original := http.DefaultTransport
	opaque := new(settingsOpaqueTransport)
	http.DefaultTransport = opaque
	test.Cleanup(func() { http.DefaultTransport = original })
	manager := openTestManager(test, testDirectory(test))
	if snapshot, err := manager.Get(test.Context()); err != nil || snapshot != (Snapshot{Revision: 1}) {
		test.Fatalf("opaque default settings did not open: %v", err)
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.com/repos", nil)
	if err != nil {
		test.Fatal(err)
	}
	response, err := manager.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusOK || opaque.requests != 1 {
		test.Fatalf("opaque default did not receive the GitHub request: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		test.Fatal(err)
	}
	if snapshot, err := manager.Update(test.Context(), 1, ""); err != nil || snapshot != (Snapshot{Revision: 2}) {
		test.Fatalf("empty update did not preserve the opaque default: %v", err)
	}
	if snapshot, err := manager.Update(test.Context(), 2, syntheticURL); snapshot != (Snapshot{}) || !errors.Is(err, ErrUnavailable) {
		test.Fatalf("explicit proxy silently bypassed its setting: %v", err)
	}
	if snapshot, err := manager.Get(test.Context()); err != nil || snapshot != (Snapshot{Revision: 2}) {
		test.Fatal("rejected explicit proxy changed the active settings")
	}
	manager.CloseIdleConnections()
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	if http.DefaultTransport != opaque || opaque.idleCloses != 0 || opaque.closes != 0 {
		test.Fatal("manager modified or closed the caller-owned default")
	}
}

type settingsOpaqueTransport struct {
	requests   int
	idleCloses int
	closes     int
}

func (transport *settingsOpaqueTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests++
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
}

func (transport *settingsOpaqueTransport) CloseIdleConnections() { transport.idleCloses++ }

func (transport *settingsOpaqueTransport) Close() error {
	transport.closes++
	return nil
}

func TestSettingsGitHubAllowlist(test *testing.T) {
	manager := openTestManager(test, testDirectory(test))
	var forwarded atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		forwarded.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
	}))
	test.Cleanup(proxy.Close)
	if _, err := manager.Update(test.Context(), 1, proxy.URL); err != nil {
		test.Fatal(err)
	}
	for _, host := range []string{"api.github.com", "codeload.github.com", "github.com", "API.GITHUB.COM:443"} {
		request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://"+host+"/repos", nil)
		if err != nil || !allowedRequest(request) {
			test.Fatal("permitted GitHub HTTPS destination was rejected")
		}
	}
	for _, target := range []string{
		"http://api.github.com/repos", "https://api.github.com:444/repos", "https://api.github.com:/repos",
		"https://api.github.com./repos", "https://api.github.com.evil.invalid/repos", "https://user:password@github.com/repos",
		"https://127.0.0.1/repos", "https://storage.example.invalid/repos", "https://github.com@evil.invalid/repos",
	} {
		address, err := url.Parse(target)
		if err != nil {
			test.Fatal(err)
		}
		request := (&http.Request{Method: http.MethodGet, URL: address, Host: address.Host}).WithContext(test.Context())
		body := &trackingBody{Reader: strings.NewReader("synthetic body")}
		request.Body = body
		response, err := manager.RoundTrip(request)
		if response != nil || !errors.Is(err, ErrInvalid) || !body.closed {
			test.Fatalf("unexpected destination was not rejected and closed: target=%s error=%v", target, err)
		}
	}
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.com/repos", nil)
	request.Host = "github.com"
	if _, err := manager.RoundTrip(request); !errors.Is(err, ErrInvalid) {
		test.Fatal("mismatched Host override was accepted")
	}
	request.Host = "api.github.com"
	request.URL.Opaque = "//evil.invalid"
	if _, err := manager.RoundTrip(request); !errors.Is(err, ErrInvalid) {
		test.Fatal("opaque URL was accepted")
	}
	if _, err := manager.RoundTrip(nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil request was accepted")
	}
	if _, err := manager.RoundTrip(&http.Request{}); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil request URL was accepted")
	}
	request.URL = &url.URL{Scheme: "https", Host: "api.github.com"}
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := manager.RoundTrip(request); !errors.Is(err, ErrClosed) {
		test.Fatal("closed manager sent a request")
	}
	if forwarded.Load() != 0 {
		test.Fatal("rejected requests reached the local proxy")
	}
}

func TestSettingsOpenInvalidArguments(test *testing.T) {
	for _, fixture := range []struct {
		ctx       context.Context
		directory string
		master    []byte
	}{
		{directory: testDirectory(test), master: testMaster()},
		{ctx: test.Context(), directory: testDirectory(test), master: make([]byte, 31)},
		{ctx: test.Context(), directory: "relative", master: testMaster()},
		{ctx: test.Context(), directory: testDirectory(test) + "/../invalid", master: testMaster()},
	} {
		if manager, err := Open(fixture.ctx, fixture.directory, fixture.master); manager != nil || !errors.Is(err, ErrInvalid) {
			test.Fatalf("invalid Open argument was accepted: %v", err)
		}
	}
}

func TestSettingsStableErrorCategories(test *testing.T) {
	for _, failure := range []error{ErrUnavailable, ErrUncertain, ErrClosed, ErrOwned} {
		if !errors.Is(failure, ErrUnavailable) || errors.Is(failure, ErrInvalid) || errors.Is(failure, ErrConflict) {
			test.Errorf("unavailable error classification: %v", failure)
		}
	}
	for _, failure := range []error{ErrInvalid, ErrConflict} {
		if errors.Is(failure, ErrUnavailable) {
			test.Errorf("invalid or conflicting input classified as unavailable: %v", failure)
		}
	}

	manager := openTestManager(test, testDirectory(test))
	canceled, cancel := context.WithCancelCause(test.Context())
	cancel(syntheticFailure)
	expired, stop := context.WithDeadlineCause(test.Context(), time.Now().Add(-time.Second), syntheticFailure)
	defer stop()
	for _, fixture := range []struct {
		ctx     context.Context
		failure error
	}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
		ctx := fixture.ctx
		for _, operation := range []func() error{
			func() error { _, err := manager.Get(ctx); return err },
			func() error { _, err := manager.Update(ctx, 1, ""); return err },
			func() error {
				request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos", nil)
				_, err := manager.RoundTrip(request)
				return err
			},
		} {
			err := operation()
			if !errors.Is(err, ErrUnavailable) || !errors.Is(err, fixture.failure) {
				test.Errorf("cancellation classification: %v", err)
			}
			assertPrivate(test, err)
		}
	}

	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	test.Cleanup(proxy.Close)
	if _, err := manager.Update(test.Context(), 1, proxy.URL); err != nil {
		test.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.com/repos", nil)
	if _, err := manager.RoundTrip(request); !errors.Is(err, ErrUnavailable) {
		test.Fatalf("transport failure classification: %v", err)
	}
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := manager.Get(test.Context()); !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrClosed) {
		test.Fatalf("closed manager classification: %v", err)
	}
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (body *trackingBody) Close() error {
	body.closed = true
	return nil
}

func assertPrivate(test *testing.T, value any) {
	test.Helper()
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		printed := fmt.Sprintf(format, value)
		for _, secret := range []string{syntheticURL, "synthetic-proxy-user", "synthetic-proxy-password"} {
			if strings.Contains(printed, secret) {
				test.Fatalf("format %s reveals proxy credentials", format)
			}
		}
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logger.Info("synthetic", "value", value)
	if strings.Contains(output.String(), "synthetic-proxy-user") || strings.Contains(output.String(), "synthetic-proxy-password") {
		test.Fatal("structured formatting reveals proxy credentials")
	}
}
