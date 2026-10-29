package cli

import (
	"bytes"
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestNativeConfigurationCLI(test *testing.T) {
	if runtime.GOOS != "linux" {
		home := test.TempDir()
		test.Setenv("HOME", home)
		command := New()
		command.SetArgs([]string{"config", "status", "--operation", "operation"})
		if command.ExecuteContext(test.Context()) == nil {
			test.Fatal("unsupported platform claimed success")
		}
		entries, err := os.ReadDir(home)
		if err != nil || len(entries) != 0 {
			test.Fatal("unsupported changed filesystem")
		}
		return
	}
	binary := buildNativeCLI(test)
	fixture := nativeCLIBackend(test, configuration.APIKey)
	for _, invalid := range []bool{false, true} {
		arguments := fixture.arguments()
		if invalid {
			arguments = append(arguments, "--unknown-secret=synthetic-argv-secret")
		}
		ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Env = fixture.environment()
		command.Stdin = strings.NewReader(fixture.bearer)
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		err := command.Run()
		cancel()
		if (err != nil) != invalid {
			test.Fatalf("unexpected actual main exit: %v; %s", err, output.String())
		}
		if fixture.authorize.Load() != 0 || fixture.consume.Load() != 0 {
			test.Fatal("preview/preflight consumed material")
		}
		for _, secret := range []string{fixture.bearer, "synthetic-C1-cli-secret", "synthetic-argv-secret"} {
			if strings.Contains(output.String(), secret) {
				test.Fatal("actual main output exposed sensitive input")
			}
		}
	}
	if err := os.Remove(filepath.Join(fixture.home, ".config", "skillhub", "client.key")); err != nil {
		test.Fatal(err)
	}
	before := fixture.authorize.Load()
	beforeRequests := fixture.requests.Load()
	command := exec.CommandContext(test.Context(), binary, fixture.arguments()...)
	command.Env = fixture.environment()
	command.Stdin = strings.NewReader(fixture.bearer)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err == nil || fixture.authorize.Load() != before {
		test.Fatal("missing key silently initialized")
	}
	if fixture.requests.Load() != beforeRequests || output.String() != clientKeyErrorMessage(clientkey.ErrRecoveryRequired)+"\n" {
		test.Fatal("missing native key performed network work or lost recovery classification")
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".config", "skillhub", "client.key")); !os.IsNotExist(err) {
		test.Fatal("missing key was recreated")
	}
}

func TestNativeConfigurationExecuteHelper(test *testing.T) {
	if os.Getenv("COGNATE_NATIVE_EXIT_HELPER") != "closed-stderr" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator == 0 {
		os.Exit(2)
	}
	os.Args = append([]string{"cognate"}, os.Args[separator+1:]...)
	_ = os.Stderr.Close()
	Execute()
	os.Exit(0)
}

func buildNativeCLI(test *testing.T) string {
	test.Helper()
	name := filepath.Join(test.TempDir(), "cognate")
	arguments := []string{"build", "-mod=readonly", "-o", name}
	if overlay := os.Getenv("COGNATE_NATIVE_TEST_BUILD_OVERLAY"); overlay != "" {
		arguments = append(arguments, "-overlay", overlay)
	}
	arguments = append(arguments, "./cmd/cognate")
	command := exec.CommandContext(test.Context(), "go", arguments...)
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("actual main build failed: %v; %s", err, output)
	}
	return name
}

type nativeCLIFixture struct {
	home, origin, owner, asset, bearer, certificate string
	revision                                        int64
	authorize, consume                              atomic.Int32
	requests                                        atomic.Int32
}

func (fixture *nativeCLIFixture) arguments() []string {
	return []string{"config", "apply", "--server", fixture.origin, "--owner", fixture.owner, "--asset", fixture.asset, "--revision", strconv.FormatInt(fixture.revision, 10), "--profile", "default", "--target", "claude.settings", "--token-stdin"}
}
func (fixture *nativeCLIFixture) environment() []string {
	return append(os.Environ(), "HOME="+fixture.home, "CLAUDE_CONFIG_DIR=", "SSL_CERT_FILE="+fixture.certificate, "SSL_CERT_DIR="+filepath.Dir(fixture.certificate))
}

func nativeCLIBackend(test *testing.T, mode configuration.AuthMode) *nativeCLIFixture {
	test.Helper()
	master := bytes.Repeat([]byte{72}, 32)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), master)
	if err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	token, owner, err := authentication.LoginOIDC(test.Context(), "https://synthetic.identity", "cli-owner", "", "")
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	fixture := &nativeCLIFixture{home: test.TempDir(), origin: "https://" + server.Listener.Addr().String(), owner: owner, bearer: token.Reveal()}
	service, err := nativeconsumer.New(store, authentication, master, fixture.origin)
	if err != nil {
		test.Fatal(err)
	}
	handler := httpapi.NewWithOptions(authentication, httpapi.Options{PublicOrigin: fixture.origin, NativeConfiguration: service})
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
			fixture.authorize.Add(1)
		}
		if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
			fixture.consume.Add(1)
		}
		handler.ServeHTTP(writer, request)
	})
	server.StartTLS()
	if err := service.Start(test.Context()); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { service.Close(); server.Close(); _ = store.Close() })
	var mutations []configuration.SecretMutation
	if mode != configuration.NativeLogin {
		slot := configuration.APIKeySlot
		if mode == configuration.AuthToken {
			slot = configuration.AuthTokenSlot
		}
		mutations = []configuration.SecretMutation{{Slot: slot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-C1-cli-secret"))}}
	}
	asset, err := configuration.New(store).Create(test.Context(), owner, configuration.Draft{Name: "C1 cli", Provider: configuration.ClaudeProvider{AuthMode: mode}}, mutations)
	if err != nil {
		test.Fatal(err)
	}
	fixture.asset, fixture.revision = asset.Metadata.ID, asset.Metadata.Revision
	for _, directory := range []string{fixture.home, filepath.Join(fixture.home, ".config", "skillhub"), filepath.Join(fixture.home, ".claude")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			test.Fatal(err)
		}
		if err := os.Chmod(directory, 0700); err != nil {
			test.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.home, ".config", "skillhub", "client.key"), bytes.Repeat([]byte{39}, 32), 0600); err != nil {
		test.Fatal(err)
	}
	if err := clientkey.Initialize(test.Context(), fixture.home); err != nil {
		test.Fatal(err)
	}
	fixture.certificate = filepath.Join(directory, "test-ca.pem")
	if err := os.WriteFile(fixture.certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	return fixture
}
