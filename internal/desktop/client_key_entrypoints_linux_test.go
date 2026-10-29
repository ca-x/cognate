package desktop

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/state"
	"github.com/egoist/mygo/ui"
	"github.com/gofrs/flock"
)

type forbiddenKeyTransport struct{ calls atomic.Int32 }

func (transport *forbiddenKeyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("synthetic forbidden transport")
}

func assertNoLocalProductState(test *testing.T, home string) {
	test.Helper()
	for _, relative := range []string{".local/share/skillhub/client.db", ".agents", ".claude", ".config/skillhub/client.key"} {
		if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
			test.Error("unadmitted operation created key or product state")
		}
	}
}

func TestClientKeyEntrypointsService(test *testing.T) {
	for _, action := range []string{"install", "list", "sync", "remove", "watch"} {
		test.Run(action, func(test *testing.T) {
			home := test.TempDir()
			transport := &forbiddenKeyTransport{}
			service := &Service{Home: home, HTTP: &http.Client{Transport: transport}}
			var err error
			switch action {
			case "install":
				err = service.Install(test.Context(), "https://offline.invalid", &catalogv1.PublicSkill{Id: "synthetic", Slug: "synthetic"})
			case "watch":
				_, _, err = service.WatchCycle(test.Context())
			default:
				_, err = service.Packs(test.Context(), action, "", "", "synthetic")
			}
			if !errors.Is(err, clientkey.ErrInitializationRequired) || transport.calls.Load() != 0 {
				test.Error("local operation did not refuse before product network")
			}
			assertNoLocalProductState(test, home)
		})
	}
	test.Run("OfflineInit", func(test *testing.T) {
		home := test.TempDir()
		transport := &forbiddenKeyTransport{}
		service := &Service{Home: home, HTTP: &http.Client{Transport: transport}}
		if err := service.InitializeClientKey(test.Context()); err != nil {
			test.Fatal("offline core initialization failed")
		}
		key, err := clientkey.LoadExisting(test.Context(), home)
		defer clear(key)
		if err != nil || len(key) != 32 || transport.calls.Load() != 0 {
			test.Fatal("initialization did not use offline shared core")
		}
		if _, err := os.Lstat(filepath.Join(home, ".local/share/skillhub/client.db")); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("initialization opened business database")
		}
	})
	test.Run("Residue", func(test *testing.T) {
		for _, kind := range []string{"sqlite", "wal", "vault", "marker", "unknown"} {
			test.Run(kind, func(test *testing.T) {
				home := test.TempDir()
				directory := filepath.Join(home, ".local/share/skillhub")
				if err := os.MkdirAll(directory, 0700); err != nil {
					test.Fatal(err)
				}
				name := "client.db-wal"
				switch kind {
				case "sqlite":
					store, err := state.Open(test.Context(), filepath.Join(directory, "client.db"))
					if err != nil {
						test.Fatal(err)
					}
					if err := store.Close(); err != nil {
						test.Fatal(err)
					}
					name = "client.db"
				case "vault":
					name = "native-config.vault"
				case "unknown":
					name = "unknown-state"
				case "marker":
					directory = filepath.Join(home, ".config/skillhub")
					if err := os.MkdirAll(directory, 0700); err != nil {
						test.Fatal(err)
					}
					name = "client-key-use.v1"
				}
				path := filepath.Join(directory, name)
				if kind != "sqlite" {
					body := []byte("synthetic state residue")
					if kind == "marker" {
						body = []byte("{\"version\":1,\"root\":\"default-client-state\"}\n")
					}
					if err := os.WriteFile(path, body, 0600); err != nil {
						test.Fatal(err)
					}
				}
				before, err := os.ReadFile(path)
				if err != nil {
					test.Fatal(err)
				}
				service := &Service{Home: home}
				if err := service.InitializeClientKey(test.Context()); !errors.Is(err, clientkey.ErrRecoveryRequired) {
					test.Error("state residue permitted key regeneration")
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					test.Error("key admission modified existing state")
				}
				if _, err := os.Lstat(filepath.Join(home, ".config/skillhub/client.key")); !errors.Is(err, os.ErrNotExist) {
					test.Error("recovery refusal nevertheless created a key")
				}
			})
		}
	})
}

func TestClientKeyEntrypointsNativeGeneration(test *testing.T) {
	for _, action := range []string{"cancel", "origin", "account", "logout", "close"} {
		test.Run(action, func(test *testing.T) {
			home := test.TempDir()
			service := &Service{Home: home}
			durable, release := make(chan struct{}), make(chan struct{})
			backend := &initializationBackend{Backend: service, initialize: func(ctx context.Context) error {
				err := service.InitializeClientKey(ctx)
				close(durable)
				<-release
				return err
			}}
			model, pump := modelFixture(test, backend)
			model.SetOrigin("https://offline.invalid")
			model.Page = 3
			model.Username = "synthetic-owner"
			model.password.set("synthetic-password")
			model.shareToken.set("synthetic-share")
			tester := ui.NewTester(model.View, 920, 760)
			click(test, tester, initializeClientKeyLabel)
			<-durable
			path := filepath.Join(home, ".config/skillhub/client.key")
			before, err := os.ReadFile(path)
			if err != nil || len(before) != 32 {
				close(release)
				test.Fatal("backend did not reach durable barrier")
			}
			defer clear(before)
			switch action {
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://new.invalid")
			case "account":
				click(test, tester, "用户名")
				tester.Type("-changed")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			}
			if model.password.value() != "" || model.shareToken.value() != "" || model.session.value() != "" {
				test.Error("cancellation or identity change retained sensitive owner state")
			}
			status := model.Status
			close(release)
			pump()
			after, err := os.ReadFile(path)
			defer clear(after)
			if err != nil || !bytes.Equal(before, after) || model.Status != status || model.pendingJobs.Load() != 0 {
				test.Fatal("late durable receipt mutated key or old UI generation")
			}
			if err := service.InitializeClientKey(test.Context()); err != nil {
				test.Fatal("explicit retry did not reconcile durable key")
			}
		})
	}
}

func TestClientKeyEntrypointsNativeWatchKeyHint(test *testing.T) {
	for _, recovery := range []bool{false, true} {
		test.Run(fmt.Sprintf("recovery-%t", recovery), func(test *testing.T) {
			home := test.TempDir()
			service := &Service{Home: home}
			if recovery {
				if err := service.InitializeClientKey(test.Context()); err != nil {
					test.Fatal(err)
				}
				if err := os.Remove(filepath.Join(home, ".config/skillhub/client.key")); err != nil {
					test.Fatal(err)
				}
			}
			callbacks := make(chan func(), 16)
			model := NewModel(service, func(callback func()) { callbacks <- callback })
			defer func() { model.Close(); model.Wait() }()
			model.SetOrigin("https://offline.invalid")
			model.Page = 1
			tester := ui.NewTester(model.View, 920, 760)
			click(test, tester, "启用后台同步")
			nextWatchUpdate(test, callbacks)
			tester.Frame()
			word := "显式初始化客户端密钥"
			if recovery {
				word = "请恢复既有客户端密钥或状态"
			}
			if !strings.Contains(model.WatchStatus, word) || !strings.Contains(model.WatchStatus, "显式停止") {
				test.Error("native watch discarded shared core admission guidance")
			}
			model.Page = 3
			tester.Frame()
			tester.Click(initializeClientKeyLabel)
			if model.pendingJobs.Load() != 0 {
				test.Error("active watch admitted key initialization")
			}
			model.StopWatch()
			if !model.WatchStopping {
				test.Error("watch stop did not preserve draining state")
			}
			nextWatchUpdate(test, callbacks)
			tester.Frame()
			if model.watch != nil {
				test.Fatal("watch did not actually drain")
			}
			if !recovery {
				click(test, tester, initializeClientKeyLabel)
				model.Wait()
				nextWatchUpdate(test, callbacks)
				if !strings.Contains(model.Status, "已就绪") {
					test.Fatal("drained watch did not permit explicit initialization")
				}
				if _, err := service.Packs(test.Context(), "list", "", "", ""); err != nil {
					test.Fatal("explicit init did not enable manual Pack operation")
				}
			}
		})
	}
}

func TestClientKeyEntrypointsSessionChild(test *testing.T) {
	if os.Getenv("COGNATE_E2_SESSION_CHILD") != "1" {
		return
	}
	directory := os.Getenv("COGNATE_E2_STATE")
	session, err := install.OpenSession(test.Context(), directory)
	if err != nil {
		os.Exit(81)
	}
	fmt.Println("session-locked")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	if session.Close() != nil {
		os.Exit(82)
	}
}

func TestClientKeyEntrypointsLockedCancel(test *testing.T) {
	home := test.TempDir()
	directory := filepath.Join(home, ".local/share/skillhub")
	if err := os.MkdirAll(directory, 0700); err != nil {
		test.Fatal(err)
	}
	childContext, cancelChild := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancelChild()
	command := exec.CommandContext(childContext, os.Args[0], "-test.run=^TestClientKeyEntrypointsSessionChild$")
	command.Env = []string{"COGNATE_E2_SESSION_CHILD=1", "COGNATE_E2_STATE=" + directory, "HOME=" + home, "TMPDIR=" + test.TempDir()}
	input, err := command.StdinPipe()
	if err != nil {
		test.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		test.Fatal(err)
	}
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Process.Kill(); _ = command.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "session-locked\n" {
		test.Fatal("child did not acquire real install Session")
	}
	model, pump := modelFixture(test, &Service{Home: home})
	model.Page = 3
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, initializeClientKeyLabel)
	lockPath := filepath.Join(home, ".config/skillhub/client-key.init.lock")
	deadline := time.Now().Add(3 * time.Second)
	locked := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lockPath); err == nil {
			probe := flock.New(lockPath)
			acquired, err := probe.TryLock()
			_ = probe.Close()
			if err == nil && !acquired {
				locked = true
				break
			}
		}
		runtime.Gosched()
	}
	if !locked {
		model.Cancel()
		test.Fatal("initialization did not wait under the real key/install lock ordering")
	}
	if model.pendingJobs.Load() != 1 {
		test.Error("initialization did not retain actual task admission while locked")
	}
	tester.Key(0, ui.KeyTab)
	click(test, tester, "取消任务")
	pump()
	if model.pendingJobs.Load() != 0 || model.Busy {
		test.Error("canceled lock waiter did not actually join")
	}
	if err := input.Close(); err != nil {
		test.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		test.Fatal("real Session child failed to drain")
	}
	session, err := install.OpenSession(test.Context(), directory)
	if err != nil {
		test.Fatal("canceled key admission leaked physical install lock")
	}
	if err := session.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config/skillhub/client.key")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("canceled lock wait created key")
	}
}

func TestClientKeyEntrypointsServiceNoImplicitCreate(test *testing.T) {
	home := test.TempDir()
	service := &Service{Home: home}
	_, err := service.Packs(test.Context(), "list", "", "", "")
	if !errors.Is(err, clientkey.ErrInitializationRequired) {
		test.Error("ordinary Pack operation did not require explicit initialization")
	}
	for _, relative := range []string{".config/skillhub/client.key", ".local/share/skillhub/client.db", ".agents"} {
		if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
			test.Error("ordinary Pack operation created key or product state before admission")
		}
	}
}

func TestClientKeyEntrypointsInvalidInput(test *testing.T) {
	for _, action := range []string{"unknown", "subscribe"} {
		test.Run(action, func(test *testing.T) {
			home := test.TempDir()
			service := &Service{Home: home}
			_, err := service.Packs(test.Context(), action, "invalid origin", "", "")
			if err == nil || errors.Is(err, clientkey.ErrInitializationRequired) {
				test.Error("invalid request reached creating key admission")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				test.Error("invalid request created admission metadata")
			}
		})
	}
}

func TestClientKeyEntrypointsCoreFaults(test *testing.T) {
	if os.Getenv("COGNATE_E2_FAULT_OVERLAY") != "1" {
		test.Skip("requires separately recorded real core fault overlay, no production hook")
	}
	var logs bytes.Buffer
	oldWriter, oldLogger := log.Writer(), slog.Default()
	log.SetOutput(&logs)
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer func() { slog.SetDefault(oldLogger); log.SetOutput(oldWriter) }()
	for _, fault := range []string{"key-sync", "marker-sync", "key-parent", "marker-parent", "root-close", "release", "late-cancel"} {
		test.Run(fault, func(test *testing.T) {
			home := test.TempDir()
			test.Setenv("COGNATE_KEY_FAULT", fault)
			service := &Service{Home: home, HTTP: &http.Client{Transport: &forbiddenKeyTransport{}}}
			model, pump := modelFixture(test, service)
			model.Page = 3
			tester := ui.NewTester(model.View, 920, 760)
			click(test, tester, initializeClientKeyLabel)
			pump()
			if model.Busy || strings.Contains(model.Status, "已就绪") || model.pendingJobs.Load() != 0 {
				test.Error("real core durability/release failure was published as success")
			}
			key, err := clientkey.LoadExisting(test.Context(), home)
			if err == nil || key != nil {
				clear(key)
				test.Error("failed core release delivered a key")
			}
			path := filepath.Join(home, ".config/skillhub/client.key")
			before, readErr := os.ReadFile(path)
			defer clear(before)
			if readErr != nil || len(before) != 32 {
				test.Fatal("fault did not reach durable-key recovery boundary")
			}
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%t", "%d", "%f", "%p"} {
				text := fmt.Sprintf(verb, model)
				container := fmt.Sprintf(verb, struct{ private *Model }{model})
				if strings.Contains(text+container, home) || strings.Contains(text+container, string(before)) {
					test.Error("model formatting exposed private key context")
				}
			}
			if strings.Contains(logs.String(), home) || bytes.Contains(logs.Bytes(), before) {
				test.Error("real fault reached standard logger with private material")
			}
			test.Setenv("COGNATE_KEY_FAULT", "")
			click(test, tester, initializeClientKeyLabel)
			pump()
			after, err := clientkey.LoadExisting(test.Context(), home)
			defer clear(after)
			if err != nil || !bytes.Equal(before, after) || !strings.Contains(model.Status, "已就绪") {
				test.Error("explicit fault retry replaced key or did not reconcile durability")
			}
			if _, err := os.Lstat(filepath.Join(home, ".agents")); !errors.Is(err, os.ErrNotExist) {
				test.Error("fault recovery prepared install targets")
			}
		})
	}
}
