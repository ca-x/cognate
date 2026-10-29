//go:build linux

package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/search"
	"github.com/ca-x/cognate/internal/state"
)

func assertClientKeyHomeEmpty(test *testing.T, home string) {
	test.Helper()
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		test.Fatal("invalid operation changed home")
	}
}

type clientKeyRemoteTransport struct{ endpoint string }

func (transport clientKeyRemoteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.URL = request.URL.Clone()
	copy.URL.Scheme = "http"
	copy.URL.Host = strings.TrimPrefix(transport.endpoint, "http://")
	return http.DefaultTransport.RoundTrip(copy)
}

func TestClientKeyEntrypointsRemoteNoKey(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"skills":[]}`))
	}))
	defer server.Close()
	command := newSearchCommand(search.NewClient(clientKeyRemoteTransport{endpoint: server.URL}))
	command.SetArgs([]string{"synthetic"})
	command.SetOut(new(bytes.Buffer))
	if err := command.ExecuteContext(test.Context()); err != nil || calls.Load() != 1 {
		test.Fatal("pure remote search was gated on local key")
	}
	assertClientKeyHomeEmpty(test, home)
}

type clientKeyWatchWriter struct {
	buffer      bytes.Buffer
	closedCycle func(context.Context) error
	observed    bool
}

func (writer *clientKeyWatchWriter) Write(data []byte) (int, error) {
	return writer.WriteContext(context.Background(), data)
}
func (writer *clientKeyWatchWriter) WriteContext(ctx context.Context, data []byte) (int, error) {
	if !writer.observed && bytes.Contains(data, []byte("下次同步")) {
		writer.observed = true
		if err := writer.closedCycle(ctx); err != nil {
			return 0, err
		}
	}
	return writer.buffer.Write(data)
}

func TestClientKeyEntrypointsWatchAdmissionLateKeyLoss(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	if err := clientkey.Initialize(test.Context(), home); err != nil {
		test.Fatal(err)
	}
	writer := &clientKeyWatchWriter{closedCycle: func(ctx context.Context) error {
		lockCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		session, err := install.OpenSession(lockCtx, filepath.Join(home, ".local", "share", "skillhub"))
		if err != nil {
			return err
		}
		if err := session.Close(); err != nil {
			return err
		}
		return os.Remove(filepath.Join(home, ".config", "skillhub", "client.key"))
	}}
	ctx, cancel := context.WithTimeout(test.Context(), 4*time.Second)
	defer cancel()
	command := New()
	command.SetArgs([]string{"pack", "watch", "--interval=1s", "--attempt-timeout=1s"})
	command.SetOut(writer)
	if err := command.ExecuteContext(ctx); !errors.Is(err, clientkey.ErrRecoveryRequired) {
		test.Fatal("later key loss was retried or swallowed")
	}
	if !writer.observed || strings.Count(writer.buffer.String(), "同步完成") != 1 || strings.Contains(writer.buffer.String(), "连续失败") {
		test.Fatal("late loss did not follow exactly one successful closed cycle")
	}
}

func TestClientKeyEntrypointsActualMain(test *testing.T) {
	binary := buildNativeCLI(test)
	for _, arguments := range [][]string{{"client-key", "init"}, {"client-key", "init", "--unknown-private-value"}, {"client-key", "unknown-private-value"}, {"pack", "sync"}, {"pack", "watch", "--interval=1s", "--attempt-timeout=1s"}} {
		test.Run(strings.Join(arguments, " "), func(test *testing.T) {
			home := test.TempDir()
			ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, arguments...)
			command.Env = append(os.Environ(), "HOME="+home)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			err := command.Run()
			success := len(arguments) == 2 && arguments[0] == "client-key" && arguments[1] == "init"
			if ctx.Err() != nil || (err == nil) != success {
				test.Fatal("actual main exit classification incorrect")
			}
			if strings.Contains(output.String(), "private-value") || output.Len() > 4096 {
				test.Fatal("actual main leaked raw args or exceeded output budget")
			}
			if success {
				if key, err := clientkey.LoadExisting(test.Context(), home); err != nil {
					test.Fatal(err)
				} else {
					clear(key)
				}
			} else if arguments[0] == "pack" && output.String() != clientKeyErrorMessage(clientkey.ErrInitializationRequired)+"\n" {
				test.Fatal("local admission did not reach fixed root diagnostic")
			}
		})
	}
	test.Run("LockTimeout", func(test *testing.T) {
		home := test.TempDir()
		if err := clientkey.Initialize(test.Context(), home); err != nil {
			test.Fatal(err)
		}
		before, err := os.ReadFile(filepath.Join(home, ".config", "skillhub", "client.key"))
		if err != nil {
			test.Fatal(err)
		}
		defer clear(before)
		session, err := install.OpenSession(test.Context(), filepath.Join(home, ".local", "share", "skillhub"))
		if err != nil {
			test.Fatal(err)
		}
		defer session.Close()
		ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "client-key", "init", "--timeout=100ms")
		command.Env = append(os.Environ(), "HOME="+home)
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		if err := command.Run(); err == nil || ctx.Err() != nil {
			test.Fatal("lock-blocked init did not terminate on its own deadline")
		}
		if output.String() != clientKeyErrorMessage(context.DeadlineExceeded)+"\n" {
			test.Fatal("lock timeout lost fixed cancellation classification")
		}
		after, err := os.ReadFile(filepath.Join(home, ".config", "skillhub", "client.key"))
		defer clear(after)
		if err != nil || !bytes.Equal(before, after) {
			test.Fatal("canceled admission replaced or removed durable key")
		}
	})
	test.Run("FullOutput", func(test *testing.T) {
		_, writer, _ := fullWatchPipe(test)
		ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
		defer cancel()
		home := test.TempDir()
		if err := clientkey.Initialize(test.Context(), home); err != nil {
			test.Fatal(err)
		}
		command := exec.CommandContext(ctx, binary, "client-key", "init", "--timeout=1s")
		command.Env = append(os.Environ(), "HOME="+home)
		var diagnostic bytes.Buffer
		command.Stdout, command.Stderr = writer, &diagnostic
		started := time.Now()
		if err := command.Run(); err == nil || ctx.Err() != nil {
			test.Fatal("full output did not refuse and drain")
		}
		if time.Since(started) > 2*time.Second || diagnostic.Len() != 0 {
			test.Fatal("failed output lost operation context or retried diagnostic")
		}
		key, err := clientkey.LoadExisting(test.Context(), home)
		clear(key)
		if err != nil {
			test.Fatal("output failure removed durable initialized key")
		}
	})
	test.Run("FullError", func(test *testing.T) {
		reader, writer, _ := fullWatchPipe(test)
		var rescued atomic.Bool
		rescueDone := make(chan struct{})
		rescue := time.AfterFunc(6*time.Second, func() {
			rescued.Store(true)
			_, _ = reader.Read(make([]byte, 4096))
			close(rescueDone)
		})
		defer func() {
			if !rescue.Stop() {
				<-rescueDone
			}
		}()
		ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "client-key", "unknown-private-argument")
		command.Env = append(os.Environ(), "HOME="+test.TempDir())
		command.Stdout, command.Stderr = new(bytes.Buffer), writer
		started := time.Now()
		if err := command.Run(); err == nil || ctx.Err() != nil {
			test.Fatal("full error output did not self-exit nonzero")
		}
		if rescued.Load() || time.Since(started) > 6*time.Second {
			test.Fatal("final diagnostic exceeded five-second wait plus process cleanup allowance")
		}
	})
	test.Run("ClosedError", func(test *testing.T) {
		ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "sh", "-c", `exec "$1" client-key unknown-private-argument 2>&-`, "sh", binary)
		command.Env = append(os.Environ(), "HOME="+test.TempDir())
		command.Stdout = new(bytes.Buffer)
		if err := command.Run(); err == nil || ctx.Err() != nil {
			test.Fatal("closed error output did not self-exit nonzero")
		}
	})
	test.Run("WatchLateLoss", func(test *testing.T) {
		home := test.TempDir()
		if err := clientkey.Initialize(test.Context(), home); err != nil {
			test.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "pack", "watch", "--interval=1s", "--attempt-timeout=1s")
		command.Env = append(os.Environ(), "HOME="+home)
		output, err := command.StdoutPipe()
		if err != nil {
			test.Fatal(err)
		}
		var diagnostic bytes.Buffer
		command.Stderr = &diagnostic
		if err := command.Start(); err != nil {
			test.Fatal(err)
		}
		wait := sync.OnceValue(command.Wait)
		defer func() { cancel(); _ = wait() }()
		reader := bufio.NewReader(output)
		for range 2 {
			if _, err := reader.ReadString('\n'); err != nil {
				test.Fatal("missing successful watch cycle")
			}
		}
		session, err := install.OpenSession(ctx, filepath.Join(home, ".local", "share", "skillhub"))
		if err != nil {
			test.Fatal(err)
		}
		if err := session.Close(); err != nil {
			test.Fatal(err)
		}
		if err := os.Remove(filepath.Join(home, ".config", "skillhub", "client.key")); err != nil {
			test.Fatal(err)
		}
		if err := wait(); err == nil || ctx.Err() != nil {
			test.Fatal("actual watch did not terminate on late key loss")
		}
		if diagnostic.String() != clientKeyErrorMessage(clientkey.ErrRecoveryRequired)+"\n" {
			test.Fatal("actual watch recovery classification incorrect")
		}
	})
}

func TestClientKeyEntrypointsResidue(test *testing.T) {
	for _, name := range []string{".local/share/skillhub/client.db-wal", ".local/share/skillhub/native-configuration-v1/record", ".local/share/skillhub/backups/record", ".local/share/skillhub/unknown", ".config/skillhub/client-key-use.v1", ".config/skillhub/client.key"} {
		test.Run(name, func(test *testing.T) {
			home := test.TempDir()
			test.Setenv("HOME", home)
			path := filepath.Join(home, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				test.Fatal(err)
			}
			original := []byte("synthetic-incomplete-state")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				test.Fatal(err)
			}
			for _, arguments := range [][]string{{"pack", "sync"}, {"client-key", "init"}} {
				command := New()
				command.SetArgs(arguments)
				command.SetOut(new(bytes.Buffer))
				if !errors.Is(command.ExecuteContext(test.Context()), clientkey.ErrRecoveryRequired) {
					test.Fatal("residue silently permitted key creation")
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, after) {
				test.Fatal("refusal modified residue")
			}
			if name != ".config/skillhub/client.key" {
				if _, err := os.Lstat(filepath.Join(home, ".config", "skillhub", "client.key")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("residue generated a key")
				}
			}
		})
	}
	test.Run("MarkerAfterLostState", func(test *testing.T) {
		home := test.TempDir()
		test.Setenv("HOME", home)
		if err := clientkey.Initialize(test.Context(), home); err != nil {
			test.Fatal(err)
		}
		if err := os.Remove(filepath.Join(home, ".config", "skillhub", "client.key")); err != nil {
			test.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(home, ".local", "share", "skillhub")); err != nil {
			test.Fatal(err)
		}
		command := New()
		command.SetArgs([]string{"client-key", "init"})
		command.SetOut(new(bytes.Buffer))
		if !errors.Is(command.ExecuteContext(test.Context()), clientkey.ErrRecoveryRequired) {
			test.Fatal("surviving enrollment marker permitted new identity")
		}
	})
}

func TestClientKeyEntrypointsLegacyEnrollment(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	keyPath := filepath.Join(home, ".config", "skillhub", "client.key")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		test.Fatal(err)
	}
	original := bytes.Repeat([]byte{41}, 32)
	defer clear(original)
	if err := os.WriteFile(keyPath, original, 0o600); err != nil {
		test.Fatal(err)
	}
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	command := New()
	command.SetOut(new(bytes.Buffer))
	command.SetArgs([]string{"pack", "list"})
	if err := command.ExecuteContext(test.Context()); err != nil {
		test.Fatal("valid legacy key not enrolled")
	}
	key, err := clientkey.LoadExisting(test.Context(), home)
	defer clear(key)
	if err != nil || !bytes.Equal(key, original) {
		test.Fatal("legacy enrollment changed key")
	}
}

func TestClientKeyEntrypointsNoImplicitCreate(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	command := New()
	command.SetArgs([]string{"pack", "sync"})
	command.SetOut(new(bytes.Buffer))
	if err := command.ExecuteContext(test.Context()); !errors.Is(err, clientkey.ErrInitializationRequired) {
		test.Error("local command did not require explicit initialization")
	}
	for _, name := range []string{".config/skillhub/client.key", ".local/share/skillhub/client.db", ".agents"} {
		if _, err := os.Lstat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			test.Error("refused command created a business file")
		}
	}
}

func TestClientKeyEntrypointsExplicitInit(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	var first []byte
	for range 2 {
		command := New()
		command.SetArgs([]string{"client-key", "init"})
		output := new(bytes.Buffer)
		command.SetOut(output)
		if err := command.ExecuteContext(test.Context()); err != nil {
			test.Fatal("explicit initialization command failed")
		}
		key, err := clientkey.LoadExisting(test.Context(), home)
		if err != nil || len(key) != 32 {
			test.Fatal("initialized key unavailable")
		}
		if first != nil && !bytes.Equal(first, key) {
			test.Fatal("idempotent initialization replaced key")
		}
		clear(first)
		first = key
	}
	clear(first)
}

func TestClientKeyEntrypointsDefaultRootMatrix(test *testing.T) {
	for _, action := range []string{"install-local", "install-public", "uninstall-local", "install-status", "backup-local", "backup-list", "restore-local", "pack-sync", "pack-list", "pack-remove", "config-status", "config-restore"} {
		test.Run(action, func(test *testing.T) {
			home := test.TempDir()
			test.Setenv("HOME", home)
			directory := filepath.Join(home, ".local", "share", "skillhub")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			store, err := state.Open(test.Context(), filepath.Join(directory, "client.db"))
			if err != nil {
				test.Fatal(err)
			}
			if err := store.Close(); err != nil {
				test.Fatal(err)
			}
			database, err := os.ReadFile(filepath.Join(directory, "client.db"))
			if err != nil {
				test.Fatal(err)
			}
			beforeHash := sha256.Sum256(database)
			source := test.TempDir()
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			arguments := map[string][]string{
				"install-local":   {"install-local", source, "--slug=demo"},
				"install-public":  {"install-public", "--server=" + server.URL, "--skill=demo"},
				"uninstall-local": {"uninstall-local", "demo"},
				"install-status":  {"install-status"}, "backup-local": {"backup-local", "demo"}, "backup-list": {"backup-list"},
				"restore-local": {"restore-local", "backup", source, "restored"}, "pack-sync": {"pack", "sync"}, "pack-list": {"pack", "list"}, "pack-remove": {"pack", "remove", "subscription"},
				"config-status": {"config", "status", "--operation=operation"}, "config-restore": {"config", "restore", "--source-operation=operation"},
			}
			command := New()
			command.SetArgs(arguments[action])
			command.SetOut(new(bytes.Buffer))
			command.SetErr(new(bytes.Buffer))
			if err := command.ExecuteContext(test.Context()); !errors.Is(err, clientkey.ErrRecoveryRequired) {
				test.Error("default-root entry bypassed key admission")
			}
			afterDatabase, err := os.ReadFile(filepath.Join(directory, "client.db"))
			if err != nil || sha256.Sum256(afterDatabase) != beforeHash {
				test.Error("refused entry opened or modified existing database")
			}
			if requests.Load() != 0 {
				test.Error("refused operation performed network request")
			}
			for _, name := range []string{".config/skillhub/client.key", ".agents"} {
				if _, err := os.Lstat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
					test.Error("refusal modified business state")
				}
			}
		})
	}
}

func TestClientKeyEntrypointsWatchAdmissionPristine(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	ctx, cancel := context.WithTimeout(test.Context(), 1500*time.Millisecond)
	defer cancel()
	command := New()
	command.SetArgs([]string{"pack", "watch", "--interval=1s", "--attempt-timeout=1s"})
	output := new(bytes.Buffer)
	command.SetOut(output)
	if err := command.ExecuteContext(ctx); !errors.Is(err, clientkey.ErrInitializationRequired) {
		test.Error("watch swallowed key admission failure")
	}
	if output.Len() != 0 {
		test.Error("watch printed success or generic retry for key admission failure")
	}
}
