//go:build linux

package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type codexCLIFixture struct {
	reviewRows                                         []string
	promptHook                                         func()
	process                                            atomic.Pointer[os.Process]
	origin                                             string
	client                                             *nativeconfig.Client
	remote                                             *configclient.NativeClient
	store                                              *state.Store
	auth                                               *auth.Service
	domain                                             *configuration.Service
	request                                            nativeconfig.CodexApplyRequest
	session, home, stateRoot, database                 string
	certificate                                        string
	authorizes, consumes, statuses, requests, business atomic.Int32
	entered, drained                                   chan struct{}
}

func newCodexCLIFixture(test *testing.T, mode string) *codexCLIFixture {
	test.Helper()
	executable := os.Getenv("COGNATE_Q4_NATIVE")
	if executable == "" {
		test.Skip("explicit pinned ELF and private compiler overlay required; no native qualification claimed")
	}
	if _, err := configapply.QualifiedCodexNativeProfile(); err != nil {
		test.Fatal("dedicated native mode must not become Unsupported/skip")
	}
	root := test.TempDir()
	if os.Chmod(root, 0o700) != nil {
		test.Fatal("private root")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate, pair := seed.Certificate(), seed.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal("TLS key")
	}
	defer clear(key)
	master := bytes.Repeat([]byte{91}, 32)
	defer clear(master)
	for name, body := range map[string][]byte{"master.key": bytes.Clone(master), "tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if os.WriteFile(filepath.Join(root, name), body, 0o600) != nil {
			test.Fatal("private TLS fixture")
		}
		clear(body)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("loopback listener")
	}
	backend := "https://" + listener.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 10 * time.Second}
	fixture := &codexCLIFixture{entered: make(chan struct{}), drained: make(chan struct{})}
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		authorize := strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization")
		consume := strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization")
		if authorize {
			fixture.authorizes.Add(1)
		}
		if consume {
			fixture.consumes.Add(1)
		}
		if strings.HasSuffix(request.URL.Path, "/GetNativeMaterializationStatus") {
			fixture.statuses.Add(1)
		}
		forward := request.Clone(request.Context())
		forward.URL = request.URL.Clone()
		destination, _ := url.Parse(backend)
		forward.URL.Scheme = destination.Scheme
		forward.URL.Host = destination.Host
		forward.Host = destination.Host
		forward.RequestURI = ""
		response, err := transport.RoundTrip(forward)
		if err != nil {
			writer.WriteHeader(503)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		defer clear(body)
		if err != nil || len(body) > 2<<20 {
			writer.WriteHeader(503)
			return
		}
		lose := mode == "lost-authorize" && authorize || (mode == "lost-consume" || mode == "close-consume" || mode == "cancel-consume") && consume
		if lose {
			if response.StatusCode != 200 {
				test.Error("fault boundary requires actual successful server transaction")
			}
			close(fixture.entered)
			defer close(fixture.drained)
			if mode == "close-consume" || mode == "cancel-consume" {
				<-request.Context().Done()
			}
			writer.Header().Set("Content-Type", "application/proto")
			writer.WriteHeader(200)
			return
		}
		for name, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(body)
	}))
	proxy.StartTLS()
	ctx, cancel := context.WithCancel(test.Context())
	app, err := server.Open(ctx, server.Config{DataDirectory: filepath.Join(root, "data"), MasterKeyFile: filepath.Join(root, "master.key"), TLSCertificate: filepath.Join(root, "tls.pem"), TLSKey: filepath.Join(root, "tls.key"), PublicOrigin: proxy.URL, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		cancel()
		proxy.Close()
		listener.Close()
		test.Fatal("actual App open")
	}
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { done <- app.Serve(ctx, listener) })
	test.Cleanup(func() {
		cancel()
		if <-done != nil {
			test.Error("App serve join")
		}
		workers.Wait()
		if app.Close() != nil {
			test.Error("App close")
		}
		proxy.Close()
		transport.CloseIdleConnections()
	})
	fixture.database = filepath.Join(root, "data", "server.db")
	fixture.certificate = filepath.Join(root, "tls.pem")
	fixture.store, err = state.Open(test.Context(), fixture.database, master)
	if err != nil {
		test.Fatal("second physical Store")
	}
	test.Cleanup(func() {
		if fixture.store.Close() != nil {
			test.Error("Store close")
		}
	})
	fixture.auth, err = auth.NewService(fixture.store, master)
	if err != nil {
		test.Fatal("actual auth")
	}
	token, owner, err := fixture.auth.LoginOIDC(test.Context(), "https://synthetic.identity", "codex-q3-owner", "", "")
	if err != nil {
		test.Fatal("synthetic owner session")
	}
	fixture.session = token.Reveal()
	fixture.origin = proxy.URL
	fixture.domain = configuration.New(fixture.store)
	provider := &configuration.CodexProvider{AuthMode: "provider-key", Endpoint: "https://synthetic-provider.invalid/v1", Model: "synthetic-model", Catalog: &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", DisplayName: new("Synthetic model"), ContextWindow: new(uint64(32768)), SupportsParallelToolCalls: new(true), InputModalities: new([]agentconfig.CodexModality{"text"}), BaseInstructions: new("SYNTHETIC INSTRUCTIONS"), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"low", "high"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("low"))}}}}
	asset, err := fixture.domain.Create(test.Context(), owner, configuration.Draft{Name: "synthetic-q3", CodexProvider: provider}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("SYNTHETIC_Q3_KEY"))}})
	if err != nil {
		test.Fatal("real encrypted asset")
	}
	fixture.home, fixture.stateRoot = filepath.Join(root, "home"), filepath.Join(root, "state")
	workspace := filepath.Join(root, "workspace")
	for _, directory := range []string{fixture.home, fixture.stateRoot, workspace, filepath.Join(fixture.home, ".codex")} {
		if os.Mkdir(directory, 0o700) != nil {
			test.Fatal("synthetic directories")
		}
	}
	if clientkey.Initialize(test.Context(), fixture.home) != nil {
		test.Fatal("explicit fixture key init")
	}
	fixture.stateRoot = filepath.Join(fixture.home, ".local", "share", "skillhub")
	identity := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"synthetic@example.invalid"}`)) + ".synthetic"
	login, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]any{"id_token": identity, "access_token": "SYNTHETIC_ACCESS", "refresh_token": "SYNTHETIC_REFRESH", "account_id": "synthetic-account"}, "last_refresh": time.Now().Add(-8*24*time.Hour + 2*time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil || os.WriteFile(filepath.Join(fixture.home, ".codex", "auth.json"), login, 0o600) != nil {
		test.Fatal("fresh synthetic File OAuth")
	}
	clear(login)
	settings := proxy.Client().Transport.(*http.Transport).Clone()
	settings.TLSClientConfig = &tls.Config{RootCAs: settings.TLSClientConfig.RootCAs.Clone()}
	fixture.remote, err = configclient.NewNativeClient(proxy.URL, settings)
	if err != nil {
		test.Fatal("native TLS client")
	}
	test.Cleanup(fixture.remote.Close)
	localKey, err := clientkey.LoadExisting(test.Context(), fixture.home)
	if err != nil {
		test.Fatal("explicit fixture key load")
	}
	defer clear(localKey)
	fixture.client, err = nativeconfig.New(fixture.home, fixture.stateRoot, proxy.URL, localKey, fixture.remote)
	if err != nil {
		test.Fatal("native orchestration client")
	}
	test.Cleanup(func() {
		if fixture.client.Close() != nil {
			test.Error("client close")
		}
	})
	fixture.request = nativeconfig.CodexApplyRequest{OwnerID: owner, AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "synthetic-c0", Executable: executable, Directory: workspace}
	return fixture
}

func (fixture *codexCLIFixture) arguments() []string {
	return []string{"config", "apply", "--target", "codex.config", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--asset", fixture.request.AssetID, "--revision", fmt.Sprint(fixture.request.AssetRevision), "--profile", fixture.request.ProfileID, "--token-stdin", "--codex-executable", fixture.request.Executable, "--codex-cwd", fixture.request.Directory}
}
func (fixture *codexCLIFixture) environment() []string {
	return []string{"HOME=" + fixture.home, "TMPDIR=" + os.TempDir(), "XDG_CONFIG_HOME=" + filepath.Join(fixture.home, ".config"), "XDG_DATA_HOME=" + filepath.Join(fixture.home, ".local", "share"), "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "SSL_CERT_FILE=" + fixture.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.certificate)}
}
func (fixture *codexCLIFixture) consumed(test *testing.T) int {
	test.Helper()
	database, err := sql.Open("sqlite3", "file:"+fixture.database+"?mode=ro")
	if err != nil {
		test.Fatal("receipt database")
	}
	defer database.Close()
	var count int
	if database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_materialization_grants WHERE consumed_ms>0").Scan(&count) != nil {
		test.Fatal("consume receipt query")
	}
	return count
}
func codexCLIBinary(test *testing.T) string {
	test.Helper()
	if binary := os.Getenv("COGNATE_Q4_BINARY"); binary != "" {
		if !filepath.IsAbs(binary) {
			test.Fatal("dedicated CLI must be absolute")
		}
		return binary
	}
	return buildNativeCLI(test)
}
func codexRunCLI(test *testing.T, fixture *codexCLIFixture, arguments []string, answer string, extraEnv ...string) (string, string, int) {
	test.Helper()
	fixture.business.Add(1)
	ctx, cancel := context.WithTimeout(test.Context(), 50*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, codexCLIBinary(test), arguments...)
	command.Env = append(fixture.environment(), extraEnv...)
	command.Stdin = strings.NewReader(fixture.session)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	var master, slave *os.File
	if answer != "" {
		master, slave = nativeTestPTY(test)
		defer master.Close()
		defer slave.Close()
		command.ExtraFiles = []*os.File{slave}
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
	} else {
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if command.Start() != nil {
		test.Fatal("actual main start")
	}
	fixture.process.Store(command.Process)
	joined := false
	defer func() {
		if !joined {
			command.Process.Kill()
			command.Wait()
		}
	}()
	operation, pending, transcript := "", "", ""
	var sourceOperation string
	for index, argument := range arguments {
		if argument == "--source-operation" && index+1 < len(arguments) {
			sourceOperation = arguments[index+1]
		}
	}
	if master != nil {
		reviewed := false
		slotSeen := false
		hooked := false
		for {
			if master.SetReadDeadline(time.Now().Add(12*time.Second)) != nil {
				test.Fatal("PTY deadline")
			}
			var buffer [1024]byte
			count, readErr := master.Read(buffer[:])
			if readErr != nil {
				test.Fatal("actual full review/confirmation not reachable")
			}
			pending += string(buffer[:count])
			transcript += string(buffer[:count])
			if len(transcript) > 64<<10 {
				test.Fatal("PTY review output budget")
			}
			finished := false
			for strings.Contains(pending, "\n") {
				line, rest, _ := strings.Cut(pending, "\n")
				pending = rest
				line = strings.TrimSuffix(line, "\r")
				if strings.HasPrefix(line, "CODEX REVIEW BEGIN ") {
					reviewed = false
					_, operation, _ = strings.Cut(line, "operation=")
				}
				if strings.HasPrefix(line, "CODEX REVIEW END ") {
					if operation == "" || !strings.HasSuffix(line, "operation="+operation) || !strings.Contains(transcript, "auth Keep") || !strings.Contains(transcript, "C0 ") || !strings.Contains(transcript, "selection ") || !strings.Contains(transcript, "confirmation ") || !strings.Contains(transcript, "到期 ") || !strings.Contains(transcript, "NativeProfile ") {
						test.Fatal("incomplete review on actual TTY")
					}
					if sourceOperation != "" && (sourceOperation == operation || !strings.Contains(transcript, "source-operation="+sourceOperation) || !strings.Contains(transcript, "plan-operation="+operation)) {
						test.Fatal("restore review omitted distinct source/new-plan operations before confirmation")
					}
					if arguments[1] == "apply" {
						for _, row := range fixture.reviewRows {
							if !strings.Contains(transcript, row) {
								test.Fatal("actual TTY omitted an original safe change row")
							}
						}
					}
					reviewed = true
				}
				_, expected, prompt := strings.Cut(line, "确认请输入 ")
				if !prompt {
					continue
				}
				if !reviewed || !strings.HasSuffix(expected, " "+operation) {
					test.Fatal("prompt before complete same-operation TTY review")
				}
				if strings.HasPrefix(expected, "approve ") {
					if slotSeen || expected != "approve codex.provider-key "+operation {
						test.Fatal("missing/duplicate/wrong slot confirmation")
					}
					slotSeen = true
				} else if expected != "confirm "+operation || arguments[1] == "apply" && !slotSeen {
					test.Fatal("final confirmation bypassed required slot")
				}
				if !hooked && fixture.promptHook != nil {
					fixture.promptHook()
					hooked = true
				}
				response := expected
				if answer == "interrupt" {
					if command.Process.Signal(os.Interrupt) != nil {
						test.Fatal("interrupt actual human review")
					}
					finished = true
					break
				}
				if answer == "yes" {
					response = "yes"
				}
				if answer == "foreign" {
					response = "confirm 00000000000000000000000000000000"
				}
				if answer == "preloaded-yes" {
					response = "yes\nconfirm " + operation
				}
				if answer == "no-final" && strings.HasPrefix(expected, "confirm ") {
					response = "no"
				}
				if _, err := master.Write([]byte(response + "\n")); err != nil {
					test.Fatal("PTY response")
				}
				if response != expected || strings.HasPrefix(expected, "confirm ") {
					finished = true
					break
				}
			}
			if finished {
				break
			}
		}
	}
	err := command.Wait()
	joined = true
	code := 0
	if err != nil {
		failure, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			test.Fatal("main wait")
		}
		code = failure.ExitCode()
	}
	if ctx.Err() != nil {
		test.Fatal("actual main command exceeded bounded gate")
	}
	if sourceOperation != "" && master != nil {
		stdoutReview := codexCLIReviewBlock(output.String())
		if stdoutReview == "" || stdoutReview != codexCLIReviewBlock(transcript) {
			test.Fatal("stdout and controlling-TTY restore review diverged")
		}
	}
	for _, canary := range []string{fixture.session, "SYNTHETIC_Q3_KEY", "SYNTHETIC_ACCESS", "SYNTHETIC_REFRESH", fixture.request.Executable, fixture.request.Directory} {
		if strings.Contains(output.String(), canary) || strings.Contains(transcript, canary) {
			test.Fatal("sensitive body/path/credential in actual main output")
		}
	}
	return output.String(), operation, code
}

func codexCLIReviewBlock(value string) string {
	var block strings.Builder
	started := false
	for line := range strings.SplitSeq(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "CODEX REVIEW BEGIN ") {
			started = true
		}
		if started {
			block.WriteString(line)
			block.WriteByte('\n')
		}
		if started && strings.HasPrefix(line, "CODEX REVIEW END ") {
			return block.String()
		}
	}
	return ""
}

func TestCodexCLIActualPreviewApplyRestore(test *testing.T) {
	fixture := newCodexCLIFixture(test, "")
	binary := codexCLIBinary(test)
	_ = binary
	authPath := filepath.Join(fixture.home, ".codex", "auth.json")
	beforeAuth, _ := os.ReadFile(authPath)
	defer clear(beforeAuth)
	output, _, code := codexRunCLI(test, fixture, fixture.arguments(), "")
	if code != 0 || !strings.Contains(output, "CODEX REVIEW END") || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
		test.Fatal("actual CLI default was not zero-materialization preview")
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "角色 ") {
			fixture.reviewRows = append(fixture.reviewRows, line)
		}
	}
	if len(fixture.reviewRows) == 0 {
		test.Fatal("real default preview omitted all change rows")
	}
	output, operation, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
	if code != 0 || !strings.Contains(output, "Completed") || len(operation) != 32 || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 || fixture.business.Load() != 2 {
		test.Fatal("actual same-TTY apply did not publish once")
	}
	config, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
	defer clear(config)
	if err != nil || !bytes.Contains(config, []byte("SYNTHETIC_Q3_KEY")) {
		test.Fatal("actual main native config absent")
	}
	catalog, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "cognate-model-catalog.json"))
	defer clear(catalog)
	if err != nil || !bytes.Contains(catalog, []byte("SYNTHETIC INSTRUCTIONS")) {
		test.Fatal("actual main native catalog absent")
	}
	output, _, code = codexRunCLI(test, fixture, []string{"config", "status", "--target", "codex.config", "--operation", operation}, "")
	if code != 0 || !strings.Contains(output, "Completed") {
		test.Fatal("actual restarted main status")
	}
	output, _, code = codexRunCLI(test, fixture, []string{"config", "restore", "--target", "codex.config", "--source-operation", operation}, "")
	if code != 0 || !strings.Contains(output, "CODEX REVIEW END") {
		test.Fatal("actual restore default preview")
	}
	output, restoredOperation, code := codexRunCLI(test, fixture, []string{"config", "restore", "--target", "codex.config", "--source-operation", operation, "--apply"}, "exact")
	if code != 0 || restoredOperation == operation || !strings.Contains(output, "Restored") {
		test.Fatal("actual first RestoreMissing")
	}
	for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
		if _, err := os.Stat(filepath.Join(fixture.home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("restore did not recover Missing")
		}
	}
	afterAuth, _ := os.ReadFile(authPath)
	defer clear(afterAuth)
	if !bytes.Equal(beforeAuth, afterAuth) || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 {
		test.Fatal("auth Keep or local restore zero consume violated")
	}
}

func TestCodexCLIActualTTYRefusals(test *testing.T) {
	for _, answer := range []string{"", "yes", "preloaded-yes", "no-final", "foreign", "interrupt"} {
		test.Run(map[string]string{"": "no-tty", "yes": "yes", "preloaded-yes": "preloaded-yes", "no-final": "no-final", "foreign": "foreign-operation", "interrupt": "interrupt-review"}[answer], func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			_, _, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), answer)
			if code == 0 || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
				test.Fatal("refused actual PTY input materialized")
			}
		})
	}
}

func TestCodexCLIActualLateAdmission(test *testing.T) {
	for _, mode := range []string{"owner-revoked", "asset-CAS", "foreign-auth"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			fixture.promptHook = func() {
				switch mode {
				case "owner-revoked":
					if fixture.auth.Logout(test.Context(), fixture.session) != nil {
						test.Fatal("actual owner logout")
					}
				case "asset-CAS":
					view, err := fixture.domain.Edit(test.Context(), fixture.request.OwnerID, fixture.request.AssetID)
					if err != nil {
						test.Fatal("actual owner edit")
					}
					if _, err := fixture.domain.Update(test.Context(), fixture.request.OwnerID, fixture.request.AssetID, view.Metadata.Revision, configuration.Draft{Name: "changed-at-human-confirm", CodexProvider: view.CodexProvider}, nil); err != nil {
						test.Fatal("actual asset CAS mutation")
					}
				case "foreign-auth":
					if os.WriteFile(filepath.Join(fixture.home, ".codex", "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"SYNTHETIC_FOREIGN"}`), 0o600) != nil {
						test.Fatal("foreign auth writer")
					}
				}
			}
			_, _, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
			if code == 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 || fixture.statuses.Load() > 1 {
				test.Fatal("late owner/CAS/source mutation crossed consume boundary")
			}
			if mode == "foreign-auth" && fixture.authorizes.Load() != 0 {
				test.Fatal("changed auth observation authorized")
			}
		})
	}
}

func TestCodexCLIActualKeyAndInputRefusals(test *testing.T) {
	for _, mode := range []string{"missing-key", "oversize-session", "replacement-key"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			keyFile := filepath.Join(fixture.home, ".config", "skillhub", "client.key")
			switch mode {
			case "missing-key":
				if os.Remove(keyFile) != nil {
					test.Fatal("missing explicit key fixture")
				}
			case "oversize-session":
				fixture.session = strings.Repeat("X", 4096)
			case "replacement-key":
				_, operation, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
				if code != 0 {
					test.Fatal("baseline publication")
				}
				if os.WriteFile(keyFile, bytes.Repeat([]byte{0x45}, 32), 0o600) != nil {
					test.Fatal("synthetic replacement key")
				}
				before := fixture.requests.Load()
				_, _, code = codexRunCLI(test, fixture, []string{"config", "status", "--target", "codex.config", "--operation", operation}, "")
				if code == 0 || fixture.requests.Load() != before || fixture.consumed(test) != 1 {
					test.Fatal("replacement key accessed authenticated journal or issued remote operation")
				}
				return
			}
			_, _, code := codexRunCLI(test, fixture, fixture.arguments(), "")
			if code == 0 || fixture.requests.Load() != 0 || fixture.consumes.Load() != 0 {
				test.Fatal("unadmitted key/session crossed remote boundary")
			}
			if mode == "missing-key" {
				if _, err := os.Stat(keyFile); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("CLI silently initialized missing key")
				}
			}
		})
	}
}

func TestCodexCLIActualUnknownStop(test *testing.T) {
	for _, mode := range []string{"lost-authorize", "lost-consume", "cancel-consume"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, mode)
			type outcome struct {
				output, operation string
				code              int
			}
			result := make(chan outcome, 1)
			var workers sync.WaitGroup
			workers.Go(func() {
				output, operation, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
				result <- outcome{output, operation, code}
			})
			defer workers.Wait()
			select {
			case <-fixture.entered:
			case <-time.After(20 * time.Second):
				test.Fatal("actual committed HTTP fault boundary")
			}
			if mode == "cancel-consume" {
				process := fixture.process.Load()
				if process == nil || process.Signal(os.Interrupt) != nil {
					test.Fatal("interrupt committed HTTP boundary")
				}
			}
			var returned outcome
			select {
			case returned = <-result:
			case <-time.After(15 * time.Second):
				test.Fatal("actual canceled main did not join")
			}
			if returned.code == 0 || len(returned.operation) != 32 {
				test.Fatal("actual Unknown missing operation or false success")
			}
			select {
			case <-fixture.drained:
			case <-time.After(time.Second):
				test.Fatal("actual server handler not drained")
			}
			wantConsume := int32(1)
			if mode == "lost-authorize" {
				wantConsume = 0
			}
			if fixture.authorizes.Load() != 1 || fixture.consumes.Load() != wantConsume || fixture.consumed(test) != int(wantConsume) || fixture.statuses.Load() > 1 || fixture.business.Load() != 1 {
				test.Fatal("Unknown attempt/replay/receipt counters")
			}
			beforeLocal := fixture.requests.Load()
			_, _, code := codexRunCLI(test, fixture, []string{"config", "status", "--target", "codex.config", "--operation", returned.operation}, "")
			if code == 0 {
				test.Fatal("unknown local status claimed success")
			}
			if mode == "lost-authorize" {
				_, _, code = codexRunCLI(test, fixture, []string{"config", "abandon", "--target", "codex.config", "--operation", returned.operation}, "")
				if code != 0 {
					test.Fatal("actual Abandon preview")
				}
				output, _, code := codexRunCLI(test, fixture, []string{"config", "abandon", "--target", "codex.config", "--operation", returned.operation, "--apply"}, "exact")
				if code != 0 || !strings.Contains(output, "AbandonedBeforeWrite") {
					test.Fatal("actual Abandon before ConsumeAttempted")
				}
			} else {
				_, _, code = codexRunCLI(test, fixture, []string{"config", "abandon", "--target", "codex.config", "--operation", returned.operation}, "")
				if code == 0 {
					test.Fatal("actual consumed operation falsely abandonable")
				}
				_, _, code = codexRunCLI(test, fixture, []string{"config", "retire-unpublished", "--target", "codex.config", "--operation", returned.operation}, "")
				if code != 0 {
					test.Fatal("actual conservative retire preview")
				}
				output, _, code := codexRunCLI(test, fixture, []string{"config", "retire-unpublished", "--target", "codex.config", "--operation", returned.operation, "--apply"}, "exact")
				if code == 0 || !strings.Contains(output, "RetiredUnpublished") || !strings.Contains(output, "Unknown") {
					test.Fatal("actual retirement erased unknown")
				}
			}
			if fixture.requests.Load() != beforeLocal || fixture.consumes.Load() != wantConsume || fixture.consumed(test) != int(wantConsume) {
				test.Fatal("local stop actions replayed remote operation")
			}
			key, err := clientkey.LoadExisting(test.Context(), fixture.home)
			clear(key)
			if err != nil {
				test.Fatal("real key/install lock left behind")
			}
		})
	}
}

func TestCodexCLIActualPresentAndSelectionCAS(test *testing.T) {
	fixture := newCodexCLIFixture(test, "")
	_, first, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
	if code != 0 {
		test.Fatal("first native apply")
	}
	before, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
	if err != nil {
		test.Fatal("first native config")
	}
	defer clear(before)
	view, err := fixture.domain.Edit(test.Context(), fixture.request.OwnerID, fixture.request.AssetID)
	if err != nil {
		test.Fatal("owner edit")
	}
	view.CodexProvider.Model = "synthetic-model-second"
	view.CodexProvider.Catalog.Models[0].Model = "synthetic-model-second"
	updated, err := fixture.domain.Update(test.Context(), fixture.request.OwnerID, fixture.request.AssetID, view.Metadata.Revision, configuration.Draft{Name: "second-selection", CodexProvider: view.CodexProvider}, nil)
	if err != nil {
		test.Fatal("actual asset CAS")
	}
	fixture.request.AssetRevision = updated.Metadata.Revision
	_, second, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
	if code != 0 {
		test.Fatal("second selected native apply")
	}
	winning, _ := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
	defer clear(winning)
	if bytes.Equal(before, winning) {
		test.Fatal("second declaration not actually applied")
	}
	_, _, code = codexRunCLI(test, fixture, []string{"config", "restore", "--target", "codex.config", "--source-operation", first}, "")
	if code == 0 {
		test.Fatal("old N restore can overwrite N+1")
	}
	afterRefusal, _ := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
	defer clear(afterRefusal)
	if !bytes.Equal(winning, afterRefusal) {
		test.Fatal("stale restore changed winning files")
	}
	output, _, code := codexRunCLI(test, fixture, []string{"config", "restore", "--target", "codex.config", "--source-operation", second, "--apply"}, "exact")
	if code != 0 || !strings.Contains(output, "Restored") {
		test.Fatal("actual Present restore")
	}
	after, _ := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
	defer clear(after)
	if !bytes.Equal(before, after) || fixture.consumes.Load() != 2 || fixture.consumed(test) != 2 {
		test.Fatal("Present restore original bytes/consumption violated")
	}
}

func TestCodexCLIActualCrashRecovery(test *testing.T) {
	if os.Getenv("COGNATE_Q4_CRASH_OVERLAY") != "1" {
		test.Skip("explicit private durable crash compiler overlay")
	}
	for _, boundary := range []string{"candidates-durable", "entry-published-2"} {
		test.Run(boundary, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			_, operation, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact", "COGNATE_Q4_CRASH="+boundary)
			if code != 73 || len(operation) != 32 {
				test.Fatal("actual main did not crash at compiled durable boundary")
			}
			output, _, code := codexRunCLI(test, fixture, []string{"config", "status", "--target", "codex.config", "--operation", operation}, "")
			if code == 0 || strings.Contains(output, ":Completed") || strings.Contains(output, "：Completed") {
				test.Fatal("real partial/crash falsely Completed")
			}
			if boundary == "entry-published-2" {
				if _, err := os.Stat(filepath.Join(fixture.home, ".codex", "cognate-model-catalog.json")); err != nil {
					test.Fatal("actual catalog partial absent")
				}
				if _, err := os.Stat(filepath.Join(fixture.home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("actual config prematurely published")
				}
			}
			_, _, code = codexRunCLI(test, fixture, []string{"config", "recover", "--target", "codex.config", "--operation", operation}, "")
			if code != 0 {
				test.Fatal("actual fresh recovery preview")
			}
			_, _, code = codexRunCLI(test, fixture, []string{"config", "recover", "--target", "codex.config", "--operation", operation, "--apply"}, "no-final")
			if code == 0 {
				test.Fatal("recovery ignored fresh final refusal")
			}
			output, _, code = codexRunCLI(test, fixture, []string{"config", "recover", "--target", "codex.config", "--operation", operation, "--apply"}, "exact")
			if code != 0 || !strings.Contains(output, "Completed") || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 {
				test.Fatal("actual restarted recovery failed or replayed")
			}
		})
	}
}

func TestCodexCLIActualBlockedReview(test *testing.T) {
	for _, mode := range []string{"stdout", "tty"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			master, slave := nativeTestPTY(test)
			defer master.Close()
			defer slave.Close()
			ctx, cancel := context.WithTimeout(test.Context(), 20*time.Second)
			defer cancel()
			arguments := append(fixture.arguments(), "--apply", "--timeout", "8s")
			command := exec.CommandContext(ctx, codexCLIBinary(test), arguments...)
			command.Env = fixture.environment()
			command.Stdin = strings.NewReader(fixture.session)
			command.ExtraFiles = []*os.File{slave}
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			var reader, writer *os.File
			if mode == "stdout" {
				var err error
				reader, writer, err = os.Pipe()
				if err != nil {
					test.Fatal("real output pipe")
				}
				defer reader.Close()
				defer writer.Close()
				nativeFillOutputPipe(test, writer)
				command.Stdout = writer
			} else {
				connection, err := slave.SyscallConn()
				if err != nil {
					test.Fatal("real TTY raw connection")
				}
				filled := false
				var fillErr error
				if connection.Control(func(descriptor uintptr) {
					if err := syscall.SetNonblock(int(descriptor), true); err != nil {
						fillErr = err
						return
					}
					defer func() {
						if err := syscall.SetNonblock(int(descriptor), false); err != nil {
							fillErr = err
						}
					}()
					body := bytes.Repeat([]byte("X"), 4096)
					for range 256 {
						_, err := syscall.Write(int(descriptor), body)
						if errors.Is(err, syscall.EAGAIN) {
							filled = true
							return
						}
						if err != nil {
							fillErr = err
							return
						}
					}
				}) != nil || fillErr != nil || !filled {
					test.Fatal("could not establish actual full controlling TTY")
				}
			}
			err := command.Run()
			if err == nil || ctx.Err() != nil || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
				test.Fatal("blocked complete review authorized or failed to drain")
			}
		})
	}
}

func TestCodexCLIActualUnqualifiedMain(test *testing.T) {
	if _, err := configapply.QualifiedCodexNativeProfile(); err == nil {
		test.Skip("normal empty registry actual-main gate is separate")
	}
	home := filepath.Join(test.TempDir(), "not-created")
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, codexCLIBinary(test), "config", "apply", "--target", "codex.config", "--server", "https://127.0.0.1:1", "--owner", "synthetic-owner", "--asset", "synthetic-asset", "--revision", "1", "--profile", "synthetic-c0", "--token-stdin", "--codex-executable", "/not-read", "--codex-cwd", "/not-read", "--apply")
	command.Env = []string{"HOME=" + home, "TMPDIR=" + os.TempDir(), "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	command.Stdin = strings.NewReader("SYNTHETIC_SESSION")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || !bytes.Contains(output, []byte("Codex本机资格")) || bytes.Contains(output, []byte("SYNTHETIC_SESSION")) {
		test.Fatal("normal actual main did not safely reject unqualified profile")
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("default empty registry created client home/key")
	}
}
