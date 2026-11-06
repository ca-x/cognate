//go:build linux

package servercli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestRuntimeCrashWriterProcess(test *testing.T) {
	root := os.Getenv("COGNATE_R2_CRASH_ROOT")
	if root == "" {
		test.Skip("subprocess helper")
	}
	key, err := os.ReadFile(os.Getenv("COGNATE_R2_CRASH_KEY"))
	if err != nil {
		test.Fatal("synthetic key unavailable")
	}
	defer clear(key)
	owner, err := runtimegen.OpenRuntime(test.Context(), root)
	if err != nil {
		test.Fatal(err)
	}
	defer owner.Close()
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(root, "server.db"), key, state.Options{Runtime: owner})
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "r2-crash-owner"); err != nil {
		test.Fatal(err)
	}
	asset, err := store.CreateConfigurationAsset(test.Context(), "r2-crash-owner", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "r2-committed-before-crash", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{"synthetic":"r2-wal"}`))})
	if err != nil {
		test.Fatal(err)
	}
	fmt.Println(asset.ID)
	var signal [1]byte
	_, _ = io.ReadFull(os.Stdin, signal[:])
}

func TestRuntimeStartupProcess(test *testing.T) {
	root := os.Getenv("COGNATE_T3S_CLI_ROOT")
	if root == "" {
		return
	}
	probe := os.Getenv("COGNATE_T3S_CLI_PROBE") == "1"
	budget := 3 * time.Second
	if probe {
		budget = 350 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(test.Context(), budget)
	defer cancel()
	lease, err := runtimegen.LockOffline(ctx, root)
	if probe && errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("deadline")
		return
	}
	if err != nil {
		test.Fatal(err)
	}
	if probe {
		if err := lease.Close(); err != nil {
			test.Fatal(err)
		}
		fmt.Println("acquired")
		return
	}
	fmt.Println("held")
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		test.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		test.Fatal(err)
	}
}

func runtimeCLIHolder(test *testing.T, root string) func() {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeStartupProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "COGNATE_T3S_CLI_ROOT="+root)
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		test.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		cancel()
		_ = input.Close()
		test.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		cancel()
		_ = input.Close()
		_ = output.Close()
		test.Fatal(err)
	}
	reader := bufio.NewReader(output)
	finish := sync.OnceFunc(func() {
		_, _ = input.Write([]byte{1})
		_ = input.Close()
		_, readError := io.Copy(io.Discard, reader)
		waitError := command.Wait()
		cancel()
		if readError != nil || waitError != nil {
			test.Error("offline holder not joined", readError, waitError, stderr.String())
		}
	})
	test.Cleanup(finish)
	line, err := reader.ReadString('\n')
	if err != nil || line != "held\n" {
		cancel()
		finish()
		test.Fatal("offline holder not ready", line, err)
	}
	return finish
}

func requireRuntimeCLIProbe(test *testing.T, root, expected string) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeStartupProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "COGNATE_T3S_CLI_ROOT="+root, "COGNATE_T3S_CLI_PROBE=1")
	output, err := command.CombinedOutput()
	if err != nil {
		test.Fatal("offline probe not joined", err, string(output))
	}
	if !strings.Contains(string(output), expected+"\n") {
		test.Fatal("unexpected real CLI EX probe", string(output), expected)
	}
}

type runtimeCLIProcess struct {
	command  *exec.Cmd
	done     chan error
	output   bytes.Buffer
	stop     func() error
	endpoint string
}

func startRuntimeCLI(test *testing.T, binary, data, key string) *runtimeCLIProcess {
	test.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	process := &runtimeCLIProcess{done: make(chan error, 1), endpoint: "http://" + address}
	process.command = exec.CommandContext(ctx, binary, "--listen="+address, "--data-dir="+data, "--key-file="+key, "--init-key-if-missing", "--list-sync-interval=0", "--source-refresh-interval=0", "--github-client-id=", "--oidc-issuer=", "--oidc-client-id=", "--oidc-client-secret-file=")
	process.command.Stdout, process.command.Stderr = &process.output, &process.output
	if err := process.command.Start(); err != nil {
		cancel()
		test.Fatal(err)
	}
	go func() { process.done <- process.command.Wait() }()
	process.stop = sync.OnceValue(func() error {
		_ = process.command.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-process.done:
			cancel()
			return err
		case <-time.After(5 * time.Second):
			_ = process.command.Process.Kill()
			err := <-process.done
			cancel()
			return errors.Join(errors.New("CLI did not drain"), err)
		}
	})
	test.Cleanup(func() { _ = process.stop() })
	return process
}

func runtimeCLIHealth(test *testing.T, process *runtimeCLIProcess) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, process.endpoint+"/healthz", nil)
		if err != nil {
			test.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			content, readError := io.ReadAll(response.Body)
			closeError := response.Body.Close()
			if response.StatusCode == http.StatusOK && string(content) == "ok\n" && readError == nil && closeError == nil {
				return
			}
		}
		select {
		case err := <-process.done:
			process.done <- err
			test.Fatal("CLI exited before health", err)
		case <-ctx.Done():
			test.Fatal("CLI health not ready")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestRuntimeStartup(test *testing.T) {
	binary := filepath.Join(test.TempDir(), "cognate-server")
	arguments := []string{"build", "-mod=readonly", "-o", binary}
	if overlay := os.Getenv("COGNATE_T3S_CLI_BUILD_OVERLAY"); overlay != "" {
		arguments = append(arguments, "-overlay="+overlay)
	}
	arguments = append(arguments, "./cmd/cognate-server")
	buildContext, cancelBuild := context.WithTimeout(test.Context(), 30*time.Second)
	defer cancelBuild()
	command := exec.CommandContext(buildContext, "go", arguments...)
	command.Dir = filepath.Join("..", "..")
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatal("actual CLI build failed", err, string(output))
	}
	test.Run("InitializeRestartLostKey", func(test *testing.T) {
		base := test.TempDir()
		if err := os.Chmod(base, 0700); err != nil {
			test.Fatal(err)
		}
		data, key := filepath.Join(base, "data"), filepath.Join(base, "keys", "master.key")
		first := startRuntimeCLI(test, binary, data, key)
		runtimeCLIHealth(test, first)
		if err := first.stop(); err != nil {
			test.Fatal("first CLI did not exit cleanly", err)
		}
		before, err := os.Lstat(key)
		if err != nil {
			test.Fatal(err)
		}
		original, err := os.ReadFile(key)
		if err != nil || len(original) != 32 {
			test.Fatal("CLI did not generate exactly 32 bytes", err)
		}
		defer clear(original)
		second := startRuntimeCLI(test, binary, data, key)
		runtimeCLIHealth(test, second)
		if err := second.stop(); err != nil {
			test.Fatal("restart did not exit cleanly", err)
		}
		after, err := os.Lstat(key)
		if err != nil || !os.SameFile(before, after) {
			test.Fatal("CLI restart replaced original key")
		}
		current, err := os.ReadFile(key)
		if err != nil || !bytes.Equal(original, current) {
			test.Fatal("CLI restart changed key")
		}
		clear(current)
		if err := os.Remove(key); err != nil {
			test.Fatal(err)
		}
		third := startRuntimeCLI(test, binary, data, key)
		select {
		case err := <-third.done:
			third.done <- err
			if err == nil {
				test.Fatal("lost key startup succeeded")
			}
		case <-time.After(5 * time.Second):
			test.Fatal("lost key startup did not reject")
		}
		if third.stop() == nil {
			test.Fatal("lost key exited successfully")
		}
		if _, err := os.Lstat(key); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("CLI replaced lost key")
		}
	})
	test.Run("SingleManagedOwner", func(test *testing.T) {
		base := test.TempDir()
		if err := os.Chmod(base, 0700); err != nil {
			test.Fatal(err)
		}
		data, key := filepath.Join(base, "data"), filepath.Join(base, "keys", "master.key")
		first := startRuntimeCLI(test, binary, data, key)
		runtimeCLIHealth(test, first)
		original, err := os.ReadFile(key)
		if err != nil {
			test.Fatal(err)
		}
		defer clear(original)
		before, err := os.Lstat(key)
		if err != nil {
			test.Fatal(err)
		}
		second := startRuntimeCLI(test, binary, data, key)
		select {
		case err := <-second.done:
			second.done <- err
			if err == nil || !strings.Contains(second.output.String(), "another server is using this data directory") {
				test.Fatal("second CLI did not clearly reject shared managed ownership", err, second.output.String())
			}
		case <-time.After(3 * time.Second):
			test.Fatal("second CLI did not promptly reject shared managed ownership")
		}
		runtimeCLIHealth(test, first)
		requireRuntimeCLIProbe(test, data, "deadline")
		if err := first.stop(); err != nil {
			test.Fatal("first CLI did not drain", err)
		}
		requireRuntimeCLIProbe(test, data, "acquired")
		replacement := startRuntimeCLI(test, binary, data, key)
		runtimeCLIHealth(test, replacement)
		requireRuntimeCLIProbe(test, data, "deadline")
		if err := replacement.stop(); err != nil {
			test.Fatal("replacement CLI did not drain", err)
		}
		requireRuntimeCLIProbe(test, data, "acquired")
		after, err := os.Lstat(key)
		if err != nil || !os.SameFile(before, after) {
			test.Fatal("managed handoff replaced original master key", err)
		}
		current, err := os.ReadFile(key)
		defer clear(current)
		if err != nil || !bytes.Equal(original, current) {
			test.Fatal("managed handoff changed original master key", err)
		}
	})
	test.Run("OfflineBlocksInitialization", func(test *testing.T) {
		base := test.TempDir()
		if err := os.Chmod(base, 0700); err != nil {
			test.Fatal(err)
		}
		data, key := filepath.Join(base, "data"), filepath.Join(base, "keys", "master.key")
		if err := os.Mkdir(data, 0700); err != nil {
			test.Fatal(err)
		}
		stopHolder := runtimeCLIHolder(test, data)
		defer stopHolder()
		process := startRuntimeCLI(test, binary, data, key)
		select {
		case err := <-process.done:
			process.done <- err
			test.Error("CLI did not wait behind real offline authority", err)
		case <-time.After(500 * time.Millisecond):
		}
		if process.stop() == nil {
			test.Fatal("canceled blocked CLI succeeded")
		}
		if _, err := os.Lstat(filepath.Dir(key)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("blocked CLI initialized key parent")
		}
		for _, name := range []string{"server.db", "server.db.schema.lock", "archives", "logs", "backup"} {
			if _, err := os.Lstat(filepath.Join(data, name)); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("blocked CLI wrote business files", name)
			}
		}
		if strings.Contains(process.output.String(), key) {
			test.Fatal("blocked CLI exposed key path")
		}
	})
	test.Run("CommittedWALCrashRecovery", func(test *testing.T) {
		base := test.TempDir()
		if err := os.Chmod(base, 0700); err != nil {
			test.Fatal(err)
		}
		data, keyPath := filepath.Join(base, "data"), filepath.Join(base, "keys", "master.key")
		first := startRuntimeCLI(test, binary, data, keyPath)
		runtimeCLIHealth(test, first)
		before, err := os.Lstat(keyPath)
		if err != nil {
			test.Fatal(err)
		}
		original, err := os.ReadFile(keyPath)
		if err != nil {
			test.Fatal("synthetic key unavailable")
		}
		defer clear(original)
		ctx, cancel := context.WithTimeout(test.Context(), 12*time.Second)
		defer cancel()
		writer := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeCrashWriterProcess$", "-test.timeout=10s")
		writer.Env = append(os.Environ(), "COGNATE_R2_CRASH_ROOT="+data, "COGNATE_R2_CRASH_KEY="+keyPath)
		input, err := writer.StdinPipe()
		if err != nil {
			test.Fatal(err)
		}
		defer input.Close()
		output, err := writer.StdoutPipe()
		if err != nil {
			test.Fatal(err)
		}
		var stderr bytes.Buffer
		writer.Stderr = &stderr
		if err := writer.Start(); err != nil {
			test.Fatal(err)
		}
		defer func() { _ = writer.Process.Kill(); _ = writer.Wait() }()
		reader := bufio.NewReader(output)
		id, err := reader.ReadString('\n')
		if err != nil || len(strings.TrimSpace(id)) != 26 {
			test.Fatal("real committed fixture not ready", err, stderr.String())
		}
		if err := first.command.Process.Kill(); err != nil {
			test.Fatal(err)
		}
		if err := first.stop(); err == nil {
			test.Fatal("SIGKILL CLI was not observed")
		}
		if err := writer.Process.Kill(); err != nil {
			test.Fatal(err)
		}
		if err := writer.Wait(); err == nil {
			test.Fatal("SIGKILL writer was not reaped")
		}
		wal, err := os.Lstat(filepath.Join(data, "server.db-wal"))
		if err != nil || wal.Size() <= 32 {
			test.Fatal("nonempty committed WAL not preserved", err)
		}
		requireRuntimeCLIProbe(test, data, "acquired")
		second := startRuntimeCLI(test, binary, data, keyPath)
		runtimeCLIHealth(test, second)
		store, err := state.Open(test.Context(), filepath.Join(data, "server.db"), original)
		if err != nil {
			test.Fatal("SQLite WAL recovery failed", err)
		}
		asset, err := store.ConfigurationAsset(test.Context(), "r2-crash-owner", strings.TrimSpace(id))
		if err != nil || asset.Name != "r2-committed-before-crash" {
			test.Fatal("committed business row not recovered", err)
		}
		if err := store.Close(); err != nil {
			test.Fatal(err)
		}
		if err := second.stop(); err != nil {
			test.Fatal(err)
		}
		after, err := os.Lstat(keyPath)
		if err != nil || !os.SameFile(before, after) {
			test.Fatal("crash recovery replaced key")
		}
		current, err := os.ReadFile(keyPath)
		if err != nil || !bytes.Equal(original, current) {
			test.Fatal("crash recovery changed key")
		}
		clear(current)
		requireRuntimeCLIProbe(test, data, "acquired")
	})
}
