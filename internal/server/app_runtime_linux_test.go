package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestAppRuntimeProcess(test *testing.T) {
	mode := os.Getenv("COGNATE_T3S_PROCESS")
	if mode == "" {
		return
	}
	budget := 350 * time.Millisecond
	if mode == "hold" {
		budget = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(test.Context(), budget)
	defer cancel()
	lease, err := runtimegen.LockOffline(ctx, os.Getenv("COGNATE_T3S_ROOT"))
	if errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("deadline")
		return
	}
	if err != nil {
		test.Fatal("offline helper could not acquire", err)
	}
	if mode == "hold" {
		fmt.Println("held")
		var signal [1]byte
		if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
			test.Fatal(err)
		}
	}
	if err := lease.Close(); err != nil {
		test.Fatal(err)
	}
	fmt.Println("acquired")
}

func runtimePeer(test *testing.T, root string, hold bool) func() string {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAppRuntimeProcess$", "-test.count=1")
	mode := "probe"
	if hold {
		mode = "hold"
	}
	command.Env = append(os.Environ(), "COGNATE_T3S_PROCESS="+mode, "COGNATE_T3S_ROOT="+root)
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		test.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		cancel()
		test.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		cancel()
		test.Fatal(err)
	}
	reader := bufio.NewReader(output)
	if hold {
		line, err := reader.ReadString('\n')
		if err != nil || line != "held\n" {
			cancel()
			_ = input.Close()
			_ = command.Wait()
			test.Fatal("holder failed before ready", line, err)
		}
	}
	finish := sync.OnceValue(func() string {
		if hold {
			_, _ = input.Write([]byte{1})
		}
		_ = input.Close()
		remaining, readError := io.ReadAll(reader)
		waitError := command.Wait()
		cancel()
		if readError != nil || waitError != nil {
			test.Fatal("helper not joined", readError, waitError, stderr.String())
		}
		return string(remaining)
	})
	test.Cleanup(func() { finish() })
	return finish
}

func runtimeConfig(test *testing.T) Config {
	test.Helper()
	base := test.TempDir()
	if err := os.Chmod(base, 0700); err != nil {
		test.Fatal(err)
	}
	key := filepath.Join(base, "master.key")
	if err := os.WriteFile(key, bytes.Repeat([]byte{83}, 32), 0600); err != nil {
		test.Fatal(err)
	}
	return Config{DataDirectory: filepath.Join(base, "data"), MasterKeyFile: key, SourceRefreshInterval: new(time.Duration(0))}
}

func TestAppRuntime(test *testing.T) {
	test.Run("StartupBeforeWrites", func(test *testing.T) {
		for _, scenario := range []struct{ initialize, enabled bool }{{}, {initialize: true}, {enabled: true}, {initialize: true, enabled: true}} {
			test.Run(fmt.Sprintf("backup=%t/init=%t", scenario.enabled, scenario.initialize), func(test *testing.T) {
				config := runtimeConfig(test)
				if scenario.enabled {
					config = backupAppConfig(test)
					config.SourceRefreshInterval = new(time.Duration(0))
				}
				if err := os.Mkdir(config.DataDirectory, 0700); err != nil {
					test.Fatal(err)
				}
				if scenario.initialize {
					config.InitializeMasterKey = true
					config.MasterKeyFile = filepath.Join(filepath.Dir(config.DataDirectory), "keys", "master.key")
				}
				stop := runtimePeer(test, config.DataDirectory, true)
				defer stop()
				ctx, cancel := context.WithTimeout(test.Context(), 200*time.Millisecond)
				defer cancel()
				app, err := Open(ctx, config)
				if app != nil {
					_ = app.Close()
				}
				if !errors.Is(err, context.DeadlineExceeded) || app != nil {
					test.Error("startup bypassed offline authority", err)
				}
				for _, name := range []string{"server.db", "server.db.schema.lock", "archives", "logs", "backup"} {
					if _, err := os.Lstat(filepath.Join(config.DataDirectory, name)); !errors.Is(err, os.ErrNotExist) {
						test.Error("startup wrote before SH", name)
					}
				}
				if scenario.initialize {
					if _, err := os.Lstat(filepath.Dir(config.MasterKeyFile)); !errors.Is(err, os.ErrNotExist) {
						test.Error("startup initialized key parent before SH")
					}
				}
			})
		}
	})
	runtimeLifecycleTests(test)
}

func requireRuntimeProbe(test *testing.T, root, expected string) {
	test.Helper()
	if output := runtimePeer(test, root, false)(); !strings.Contains(output, expected+"\n") {
		test.Fatal("unexpected real EX probe", output, expected)
	}
}

func runtimeServe(test *testing.T, app *App) (string, func()) {
	test.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, listener) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				test.Error("Serve did not drain", err)
			}
		case <-time.After(5 * time.Second):
			test.Error("Serve not joined")
		}
	})
	test.Cleanup(stop)
	endpoint := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(endpoint + "/healthz")
	if err != nil {
		test.Fatal(err)
	}
	_, readError := io.Copy(io.Discard, response.Body)
	closeError := response.Body.Close()
	if readError != nil || closeError != nil || response.StatusCode != http.StatusOK {
		test.Fatal("health not ready", readError, closeError)
	}
	return endpoint, stop
}

func runtimeWait(test *testing.T, done <-chan error) error {
	test.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		test.Fatal("runtime operation not joined")
		return nil
	}
}

func runtimeLifecycleTests(test *testing.T) {
	test.Run("ListenerCloseUnknown", func(test *testing.T) {
		config := runtimeConfig(test)
		app, err := Open(test.Context(), config)
		if err != nil {
			test.Fatal(err)
		}
		physical, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			test.Fatal(err)
		}
		listener := &runtimeFaultListener{Listener: physical}
		done := make(chan error, 1)
		go func() { done <- app.Serve(test.Context(), listener) }()
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + physical.Addr().String() + "/healthz")
		if err != nil {
			test.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if err := app.Close(); !errors.Is(err, runtimegen.ErrUncertain) {
			test.Fatal("unknown listener close lost", err)
		}
		if err := runtimeWait(test, done); err == nil || strings.Contains(fmt.Sprint(err), "PRIVATE") {
			test.Fatal("Serve hid or exposed unknown listener close", err)
		}
		if listener.calls.Load() != 1 {
			test.Fatal("unknown physical listener Close was retried", listener.calls.Load())
		}
		requireRuntimeProbe(test, config.DataDirectory, "deadline")
	})
	test.Run("ConcurrentClose", func(test *testing.T) {
		config := runtimeConfig(test)
		calls := 0
		app, err := openApp(test.Context(), config, appRuntimeHooks{close: func(stage string, original error) error {
			if stage == "logs" {
				calls++
			}
			return nil
		}})
		if err != nil {
			test.Fatal(err)
		}
		var joined sync.WaitGroup
		for range 16 {
			joined.Go(func() {
				if err := app.Close(); err != nil {
					test.Error(err)
				}
			})
		}
		joined.Wait()
		if calls != 1 {
			test.Fatal("concurrent Close did not finish once", calls)
		}
		requireRuntimeProbe(test, config.DataDirectory, "acquired")
	})
	test.Run("LiveServerBlocksOffline", func(test *testing.T) {
		for _, enabled := range []bool{false, true} {
			test.Run(fmt.Sprint(enabled), func(test *testing.T) {
				config := runtimeConfig(test)
				if enabled {
					config = backupAppConfig(test)
					config.SourceRefreshInterval = new(time.Duration(0))
				}
				app, err := Open(test.Context(), config)
				if err != nil {
					test.Fatal(err)
				}
				_, stop := runtimeServe(test, app)
				requireRuntimeProbe(test, config.DataDirectory, "deadline")
				if err := app.Close(); err != nil {
					test.Fatal(err)
				}
				stop()
				requireRuntimeProbe(test, config.DataDirectory, "acquired")
			})
		}
	})
	test.Run("StartupInProgress", func(test *testing.T) {
		config := runtimeConfig(test)
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			app, err := openApp(ctx, config, appRuntimeHooks{observe: func(stage string) {
				if stage == "store-opening" {
					close(entered)
					<-release
				}
			}})
			if app != nil {
				_ = app.Close()
			}
			done <- err
		}()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			test.Fatal("startup not held")
		}
		cancel()
		requireRuntimeProbe(test, config.DataDirectory, "deadline")
		unblock()
		if err := runtimeWait(test, done); !errors.Is(err, context.Canceled) {
			test.Fatal(err)
		}
		requireRuntimeProbe(test, config.DataDirectory, "acquired")
	})
	test.Run("OrdinarySHCoexist", func(test *testing.T) {
		config := runtimeConfig(test)
		first, err := Open(test.Context(), config)
		if err != nil {
			test.Fatal(err)
		}
		second, err := Open(test.Context(), config)
		if err != nil {
			_ = first.Close()
			test.Fatal(err)
		}
		_, stopFirst := runtimeServe(test, first)
		_, stopSecond := runtimeServe(test, second)
		requireRuntimeProbe(test, config.DataDirectory, "deadline")
		if err := first.Close(); err != nil {
			test.Fatal(err)
		}
		stopFirst()
		requireRuntimeProbe(test, config.DataDirectory, "deadline")
		if err := second.Close(); err != nil {
			test.Fatal(err)
		}
		stopSecond()
		requireRuntimeProbe(test, config.DataDirectory, "acquired")
	})
	test.Run("CloseLastLease", func(test *testing.T) {
		for _, target := range []string{"serve-tail", "handler", "library-closed", "cache-closed", "store-closed", "logs-closed"} {
			test.Run(target, func(test *testing.T) {
				config := runtimeConfig(test)
				entered, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				hold := sync.OnceFunc(func() { close(entered); <-release })
				app, err := openApp(test.Context(), config, appRuntimeHooks{observe: func(stage string) {
					if stage == target {
						hold()
					}
				}})
				if err != nil {
					test.Fatal(err)
				}
				if target == "handler" {
					original := app.handler
					app.handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						if request.URL.Path == "/held" {
							hold()
							writer.WriteHeader(http.StatusNoContent)
							return
						}
						original.ServeHTTP(writer, request)
					})
				}
				endpoint, stop := runtimeServe(test, app)
				requestDone := make(chan error, 1)
				if target == "handler" {
					go func() {
						response, err := http.Get(endpoint + "/held")
						if err == nil {
							_, err = io.Copy(io.Discard, response.Body)
							err = errors.Join(err, response.Body.Close())
						}
						requestDone <- err
					}()
					select {
					case <-entered:
					case <-time.After(3 * time.Second):
						test.Fatal("handler not held")
					}
				}
				closed := make(chan error, 1)
				go func() { closed <- app.Close() }()
				if target != "handler" {
					select {
					case <-entered:
					case <-time.After(3 * time.Second):
						test.Fatal("close did not reach physical tail", target)
					}
				}
				requireRuntimeProbe(test, config.DataDirectory, "deadline")
				select {
				case err := <-closed:
					test.Fatal("Close returned before resource drained", err)
				default:
				}
				unblock()
				if err := runtimeWait(test, closed); err != nil {
					test.Fatal(err)
				}
				if target == "handler" {
					_ = runtimeWait(test, requestDone)
				}
				stop()
				requireRuntimeProbe(test, config.DataDirectory, "acquired")
			})
		}
	})
	test.Run("FailedStartupRetainsLease", func(test *testing.T) {
		config := runtimeConfig(test)
		app, err := openApp(test.Context(), config, appRuntimeHooks{store: func(context.Context, string, []byte, state.Options) (*state.Store, error) {
			return nil, state.ErrLocalSnapshotUncertain
		}})
		if app != nil || !errors.Is(err, runtimegen.ErrUncertain) {
			test.Fatal("nil owning constructor uncertainty discarded", err)
		}
		runtime.GC()
		requireRuntimeProbe(test, config.DataDirectory, "deadline")
	})
	test.Run("CloseUncertainRetainsLease", func(test *testing.T) {
		for _, target := range []string{"cache", "store", "logs"} {
			test.Run(target, func(test *testing.T) {
				config := runtimeConfig(test)
				calls := 0
				app, err := openApp(test.Context(), config, appRuntimeHooks{close: func(stage string, original error) error {
					if stage == target {
						calls++
						if original != nil {
							test.Error("physical Close failed before injection", original)
						}
						return errors.New("PRIVATE close-after-success")
					}
					return nil
				}})
				if err != nil {
					test.Fatal(err)
				}
				first := app.Close()
				if !errors.Is(first, runtimegen.ErrUncertain) || strings.Contains(fmt.Sprint(first), "PRIVATE") {
					test.Fatal("uncertain close downgraded/leaked", first)
				}
				if second := app.Close(); first != second || calls != 1 {
					test.Fatal("Close retried or changed result", calls)
				}
				runtime.GC()
				requireRuntimeProbe(test, config.DataDirectory, "deadline")
			})
		}
	})
	test.Run("LegacyGate", func(test *testing.T) {
		for _, name := range []string{"runtime.current", "generations", "restore", "runtime.future", ".runtime.future", ".runtime-future", "restore.future", ".restore-future", "layout.intent", "switch.intent", "rollback.intent"} {
			test.Run(name, func(test *testing.T) {
				config := runtimeConfig(test)
				config.InitializeMasterKey = true
				if err := os.Remove(config.MasterKeyFile); err != nil {
					test.Fatal(err)
				}
				if err := os.Mkdir(config.DataDirectory, 0700); err != nil {
					test.Fatal(err)
				}
				path := filepath.Join(config.DataDirectory, name)
				if err := os.Symlink("untrusted-future", path); err != nil {
					test.Fatal(err)
				}
				app, err := Open(test.Context(), config)
				if app != nil || !errors.Is(err, runtimegen.ErrNeedsAttention) {
					test.Fatal("future layout opened", err)
				}
				if _, err := os.Lstat(config.MasterKeyFile); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("gate generated a key")
				}
				if _, err := os.Lstat(filepath.Join(config.DataDirectory, "server.db")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("gate opened SQL")
				}
				if target, err := os.Readlink(path); err != nil || target != "untrusted-future" {
					test.Fatal("gate changed evidence")
				}
			})
		}
	})
}

type runtimeFaultListener struct {
	net.Listener
	calls atomic.Int32
}

func TestAppRuntimeClosedHandlerAndListener(test *testing.T) {
	app, err := Open(test.Context(), runtimeConfig(test))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = app.Close() })
	paths := []string{"/skillhub.import.v1.ImportService/PreviewLibraryExport", "/skillhub.import.v1.ImportService/PreviewCCSwitchSnapshot"}
	request := func(path string) *http.Request {
		message := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)).WithContext(test.Context())
		message.Host = "127.0.0.1"
		message.Header.Set("Content-Type", "application/json")
		message.Header.Set("Connect-Protocol-Version", "1")
		return message
	}
	for _, path := range paths {
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, request(path))
		if response.Code != http.StatusUnauthorized {
			test.Fatal("open handler did not exercise authentication", path, response.Code)
		}
	}
	endpoint, stop := runtimeServe(test, app)
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	stop()
	listener, err := net.Listen("tcp", strings.TrimPrefix(endpoint, "http://"))
	if err != nil {
		test.Fatal("closed App retained its HTTP listener", err)
	}
	if err := listener.Close(); err != nil {
		test.Fatal(err)
	}
	for _, path := range paths {
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, request(path))
		if response.Code != http.StatusServiceUnavailable {
			test.Fatal("closed import handler did not reject before authentication/store access", path, response.Code)
		}
	}
	delegated := 0
	app.handler = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		delegated++
		writer.WriteHeader(http.StatusOK)
	})
	response := httptest.NewRecorder()
	app.runtimeHandler().ServeHTTP(response, request(paths[0]))
	if response.Code != http.StatusServiceUnavailable || delegated != 0 {
		test.Fatal("closed runtime admitted its delegate", response.Code, delegated)
	}
}

func (listener *runtimeFaultListener) Close() error {
	listener.calls.Add(1)
	if err := listener.Listener.Close(); err != nil {
		return err
	}
	return errors.New("PRIVATE actual-listener-close")
}
