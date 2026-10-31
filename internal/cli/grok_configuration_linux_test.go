package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/sys/unix"
)

const grokTestKey = " synthetic-G2-private-key\t"
const grokTestDraft = `{"name":" Custom G2 ","grokbuildProvider":{"mode":"GROK_BUILD_MODE_CUSTOM_MODEL","model":{"profile":"profile-one","upstreamModel":"x-ai/model","endpoint":"https://example.invalid/v1","name":"Model title","apiBackend":"responses","contextWindow":"9007199254740991","envKey":"GROK_TEST_ENV"}}}`
const grokNativeDraft = `{"name":"Native G2","grokbuildProvider":{"mode":"GROK_BUILD_MODE_NATIVE_LOGIN"}}`

var grokActualBinary = sync.OnceValues(func() (string, error) {
	directory, err := os.MkdirTemp("", "grok-actual-main-")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path := filepath.Join(directory, "cognate")
	command := exec.CommandContext(ctx, "go", "build", "-o", path, "./cmd/cognate")
	command.Dir = "../.."
	if _, err := command.CombinedOutput(); err != nil {
		return "", errors.New("actual main build failed")
	}
	return path, nil
})

type grokCLIFixture struct {
	base          *mcpActualCLIFixture
	peer          *state.Store
	owner         string
	draft, key    string
	mutations     atomic.Int32
	requests      atomic.Int32
	business      atomic.Int32
	loss          atomic.Bool
	beforeApprove func()
}

func newGrokCLIFixture(test *testing.T) *grokCLIFixture {
	test.Helper()
	base := newMCPActualCLIFixture(test, "normal")
	var sequence int
	var name, database string
	if base.database.QueryRowContext(test.Context(), "PRAGMA database_list").Scan(&sequence, &name, &database) != nil {
		test.Fatal("actual physical SQLite identity")
	}
	peer, err := state.Open(test.Context(), database, bytes.Repeat([]byte{85}, 32))
	if err != nil {
		test.Fatal("second actual Store")
	}
	test.Cleanup(func() {
		if peer.Close() != nil {
			test.Error("second Store close")
		}
	})
	authentication, err := auth.NewService(peer, bytes.Repeat([]byte{85}, 32))
	if err != nil {
		test.Fatal("actual auth service")
	}
	owner, err := authentication.Authenticate(test.Context(), base.session)
	if err != nil {
		test.Fatal("owner authentication")
	}
	fixture := &grokCLIFixture{base: base, peer: peer, owner: owner, draft: filepath.Join(base.home, "draft.json"), key: filepath.Join(base.home, "key")}
	for path, data := range map[string]string{fixture.draft: grokTestDraft, fixture.key: grokTestKey} {
		if os.WriteFile(path, []byte(data), 0600) != nil {
			test.Fatal("private input fixture")
		}
	}
	for _, statement := range []string{
		`CREATE TABLE grok_cli_test_commits(action TEXT NOT NULL)`,
		`CREATE TRIGGER grok_cli_test_insert AFTER INSERT ON configuration_assets WHEN NEW.agent_id='grokbuild' BEGIN INSERT INTO grok_cli_test_commits VALUES('create'); END`,
		`CREATE TRIGGER grok_cli_test_update AFTER UPDATE ON configuration_assets WHEN NEW.agent_id='grokbuild' BEGIN INSERT INTO grok_cli_test_commits VALUES('update'); END`,
		`CREATE TRIGGER grok_cli_test_delete AFTER DELETE ON configuration_assets WHEN OLD.agent_id='grokbuild' BEGIN INSERT INTO grok_cli_test_commits VALUES('delete'); END`,
	} {
		if _, err := base.database.ExecContext(test.Context(), statement); err != nil {
			test.Fatal("real commit observer", err)
		}
	}
	public, err := os.ReadFile(base.certificate)
	if err != nil {
		test.Fatal("synthetic public certificate")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(public) {
		test.Fatal("synthetic CA")
	}
	pair, err := tls.LoadX509KeyPair(base.certificate, filepath.Join(filepath.Dir(base.certificate), "tls.key"))
	if err != nil {
		test.Fatal("synthetic TLS identity")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 5 * time.Second, DisableCompression: true}
	test.Cleanup(transport.CloseIdleConnections)
	backend, _ := url.Parse(base.origin)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		mutation := false
		for _, method := range []string{"CreateAsset", "UpdateAsset", "CopyAsset", "DeleteAsset", "ReplaceAssetOrder"} {
			mutation = mutation || strings.HasSuffix(request.URL.Path, "/ConfigurationService/"+method) || request.URL.Path == "/skillhub.configuration.v1.ConfigurationService/"+method
		}
		if mutation {
			fixture.mutations.Add(1)
		}
		forward := request.Clone(request.Context())
		forward.URL = request.URL.Clone()
		forward.URL.Scheme, forward.URL.Host, forward.Host, forward.RequestURI = backend.Scheme, backend.Host, backend.Host, ""
		response, err := transport.RoundTrip(forward)
		if err != nil {
			writer.WriteHeader(503)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil || len(body) > 2<<20 {
			writer.WriteHeader(503)
			return
		}
		if mutation && response.StatusCode == 200 && fixture.loss.Load() {
			connection, _, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				test.Error("synthetic loss setup")
				return
			}
			_ = connection.Close()
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
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	proxy.StartTLS()
	test.Cleanup(proxy.Close)
	base.origin = proxy.URL
	return fixture
}

func grokRun(test *testing.T, fixture *grokCLIFixture, args []string, approval string, want int) (string, string) {
	test.Helper()
	binary, err := grokActualBinary()
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 9*time.Second)
	defer cancel()
	arguments := append(append([]string{"grok-assets"}, args...), "--server", fixture.base.origin, "--token-stdin")
	command := exec.CommandContext(ctx, binary, arguments...)
	home := filepath.Join(test.TempDir(), "absent-home")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + home + "/config", "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.base.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.base.certificate)}
	command.Stdin = strings.NewReader(fixture.base.session)
	if approval == "stdin-yes" {
		command.Stdin = strings.NewReader(fixture.base.session + "\nyes\n")
	}
	var stdout, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &diagnostic
	command.WaitDelay = time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var master, slave *os.File
	if approval != "" && approval != "stdin-yes" && approval != "full-output" && approval != "blocked-input" {
		master, slave = nativeTestPTY(test)
		command.ExtraFiles = []*os.File{slave}
		command.SysProcAttr.Setctty, command.SysProcAttr.Ctty = true, 3
	}
	if approval == "full-tty" {
		descriptor := int(slave.Fd())
		flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
		if err != nil || unix.SetNonblock(descriptor, true) != nil {
			test.Fatal("bounded full PTY setup")
		}
		data := bytes.Repeat([]byte("x"), 4096)
		full := false
		for range 1024 {
			_, err := unix.Write(descriptor, data)
			if errors.Is(err, unix.EAGAIN) {
				full = true
				break
			}
			if err != nil {
				test.Fatal("PTY fill")
			}
		}
		if !full {
			test.Fatal("PTY not actually full")
		}
		if _, err := unix.FcntlInt(uintptr(descriptor), unix.F_SETFL, flags); err != nil {
			test.Fatal("PTY flags restore")
		}
	}
	var flags int
	var borrowed *os.File
	var postData chan string
	if approval == "post-output-close" {
		reader, writer, err := os.Pipe()
		if err != nil {
			test.Fatal("post-output pipe")
		}
		defer reader.Close()
		defer writer.Close()
		command.Stdout = writer
		postData = make(chan string, 1)
		go func() {
			var captured strings.Builder
			defer func() { _ = reader.Close(); postData <- captured.String() }()
			for captured.Len() < 64<<10 {
				data := make([]byte, 4096)
				count, err := reader.Read(data)
				captured.Write(data[:count])
				if err != nil || strings.Contains(captured.String(), "GROK REVIEW END") {
					return
				}
			}
		}()
	}
	if approval == "full-output" {
		_, borrowed, _ = fullWatchPipe(test)
		if unix.SetNonblock(int(borrowed.Fd()), false) != nil {
			test.Fatal("borrowed pipe")
		}
		flags, err = unix.FcntlInt(borrowed.Fd(), unix.F_GETFL, 0)
		if err != nil {
			test.Fatal("borrowed flags")
		}
		command.Stdout = borrowed
	}
	if approval == "blocked-input" {
		reader, writer, err := os.Pipe()
		if err != nil {
			test.Fatal("blocked input")
		}
		defer reader.Close()
		defer writer.Close()
		command.Stdin = reader
	}
	if err := command.Start(); err != nil {
		test.Fatal("actual main start")
	}
	joined := false
	defer func() {
		if !joined {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if approval == "full-output" || approval == "blocked-input" || approval == "full-tty" {
		time.Sleep(100 * time.Millisecond)
		_ = command.Process.Signal(os.Interrupt)
	}
	var review strings.Builder
	prompts := 0
	if master != nil && approval != "full-tty" {
		if master.SetReadDeadline(time.Now().Add(6*time.Second)) != nil {
			test.Fatal("PTY deadline")
		}
		pattern := regexp.MustCompile(`确认请输入 ([^\r\n]+)\r?\n`)
		cursor := 0
		for review.Len() < 128<<10 {
			data := make([]byte, 1024)
			count, readErr := master.Read(data)
			review.Write(data[:count])
			if location := pattern.FindStringSubmatchIndex(review.String()[cursor:]); location != nil {
				phrase := review.String()[cursor+location[2] : cursor+location[3]]
				cursor += location[1]
				prompts++
				if postData != nil {
					select {
					case captured := <-postData:
						stdout.WriteString(captured)
						postData = nil
					case <-ctx.Done():
						test.Fatal("post-output worker not drained")
					}
				}
				if !strings.Contains(review.String(), "GROK REVIEW BEGIN") || !strings.Contains(review.String(), "GROK REVIEW END") || !strings.Contains(review.String(), "NOT been server-validated") || !strings.Contains(review.String(), fixture.base.origin) {
					test.Fatal("complete same-TTY review missing before prompt")
				}
				if fixture.beforeApprove != nil {
					fixture.beforeApprove()
					fixture.beforeApprove = nil
				}
				if approval == "cancel-prompt" {
					_ = command.Process.Signal(os.Interrupt)
					break
				}
				final := strings.HasPrefix(phrase, "grok ")
				answer := phrase
				if approval == "bad-final" && final || approval == "bad-key" && !final || approval == "yes" {
					answer = "yes"
				}
				if _, err := fmt.Fprintln(master, answer); err != nil {
					test.Fatal("PTY answer")
				}
				if final || answer == "yes" {
					if final && answer == phrase {
						fixture.business.Add(1)
					}
					break
				}
			}
			if readErr != nil {
				test.Fatal("real prompt missing")
			}
		}
	}
	err = command.Wait()
	joined = true
	if ctx.Err() != nil {
		test.Fatal("actual main deadline exceeded")
	}
	actual := 0
	if err != nil {
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			test.Fatal("actual main non-exit failure")
		}
		actual = exit.ExitCode()
	}
	if actual != want {
		test.Fatalf("actual main exit%d want%d safe diagnostic=%s", actual, want, diagnostic.String())
	}
	if borrowed != nil {
		after, err := unix.FcntlInt(borrowed.Fd(), unix.F_GETFL, 0)
		if err != nil || flags != after {
			test.Fatal("borrowed file flags changed")
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		test.Fatal("owner CLI created or read HOME state")
	}
	all := stdout.String() + diagnostic.String() + review.String()
	for _, secret := range []string{grokTestKey, fixture.base.session, "synthetic-mcp-private-password"} {
		if strings.Contains(all, secret) {
			test.Fatal("secret escaped actual command")
		}
	}
	if master != nil && approval != "full-tty" {
		pattern := regexp.MustCompile(`(?s)GROK REVIEW BEGIN.*?GROK REVIEW END[^\r\n]*`)
		stdoutReview := pattern.FindString(stdout.String())
		ttyReview := pattern.FindString(strings.ReplaceAll(review.String(), "\r\n", "\n"))
		if stdoutReview == "" || stdoutReview != ttyReview || prompts == 0 {
			test.Fatal("stdout and same-TTY full reviews differ")
		}
		if want == 0 {
			keyPhrase := slices.Contains(args, "replace") && slices.Contains(args, "--key-action") || slices.Contains(args, "clear") && slices.Contains(args, "--key-action")
			if keyPhrase && prompts != 2 || !keyPhrase && prompts != 1 {
				test.Fatal("explicit key action and final confirmation count differ")
			}
		}
	}
	return stdout.String(), diagnostic.String()
}

func grokSavedID(test *testing.T, output string) string {
	test.Helper()
	match := regexp.MustCompile(`ASSET id="([A-Z2-7]+)"`).FindStringSubmatch(output)
	if match == nil {
		test.Fatal("real receipt identity missing")
	}
	return match[1]
}

func assertGrokStored(test *testing.T, fixture *grokCLIFixture, id string, revision int64, name, secret string, native, env bool) {
	test.Helper()
	metadata, payload, err := fixture.peer.ReadConfigurationAssetVersion(test.Context(), fixture.owner, id)
	if err != nil || metadata.ID != id || metadata.Name != name || metadata.Revision != revision || metadata.AgentID != "grokbuild" {
		test.Fatal("receipt identity/revision/name does not resolve to approved owner DB")
	}
	data := payload.Reveal()
	defer clear(data)
	var envelope struct {
		Secrets struct {
			APIKey string `json:"api_key"`
		} `json:"secrets"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Secrets.APIKey != secret {
		test.Fatal("durable key differs from approved action")
	}
	view, err := configuration.New(fixture.peer).Edit(test.Context(), fixture.owner, id)
	if err != nil || view.GrokBuildProvider == nil {
		test.Fatal("real domain edit")
	}
	provider := view.GrokBuildProvider
	if native {
		if string(provider.Mode) != "native-login" || provider.Model != nil {
			test.Fatal("approved native shape differs")
		}
		return
	}
	model := provider.Model
	if string(provider.Mode) != "custom-model" || model == nil || model.Profile != "profile-one" || model.UpstreamModel != "x-ai/model" || model.Endpoint != "https://example.invalid/v1" || model.Name != "Model title" || model.APIBackend != "responses" || model.ContextWindow != 9007199254740991 || (model.EnvKey != nil) != env || env && *model.EnvKey != "GROK_TEST_ENV" {
		test.Fatal("complete seven model fields/presence differ from approved draft")
	}
}

func TestGrokActualMainNineRPCs(test *testing.T) {
	fixture := newGrokCLIFixture(test)
	file := filepath.Join(fixture.base.home, "order.json")
	if os.WriteFile(file, []byte(`[]`), 0600) != nil {
		test.Fatal("empty order")
	}
	output, _ := grokRun(test, fixture, []string{"order", "get"}, "", 0)
	if !strings.Contains(output, "revision=0 count=0") {
		test.Fatal("explicit empty order partition")
	}
	grokRun(test, fixture, []string{"order", "replace", "--expected-order-revision", "0", "--ids-file", file, "--apply"}, "exact", 0)
	args := []string{"create", "--draft-file", fixture.draft, "--key-action", "replace", "--key-file", fixture.key}
	output, _ = grokRun(test, fixture, args, "", 0)
	if fixture.mutations.Load() != 1 || !strings.Contains(output, "key-action=replace") {
		test.Fatal("default review mutated server or incomplete key action")
	}
	output, _ = grokRun(test, fixture, append(args, "--apply"), "exact", 0)
	id := grokSavedID(test, output)
	assertGrokStored(test, fixture, id, 1, "Custom G2", grokTestKey, false, true)
	for _, action := range []string{"list", "get", "read"} {
		args := []string{action}
		if action != "list" {
			args = append(args, "--id", id)
		}
		output, _ := grokRun(test, fixture, args, "", 0)
		if !strings.Contains(output, id) {
			test.Fatal("actual owner read did not resolve created ID")
		}
	}
	if os.WriteFile(fixture.draft, []byte(strings.ReplaceAll(grokTestDraft, `,"envKey":"GROK_TEST_ENV"`, "")), 0600) != nil {
		test.Fatal("absent env fixture")
	}
	grokRun(test, fixture, []string{"update", "--id", id, "--expected-revision", "1", "--draft-file", fixture.draft, "--key-action", "keep", "--apply"}, "exact", 0)
	assertGrokStored(test, fixture, id, 2, "Custom G2", grokTestKey, false, false)
	output, _ = grokRun(test, fixture, []string{"copy", "--id", id, "--expected-source-revision", "2", "--name", "Copy G2", "--apply"}, "exact", 0)
	copyID := grokSavedID(test, output)
	if copyID == id {
		test.Fatal("copy aliased source")
	}
	assertGrokStored(test, fixture, copyID, 1, "Copy G2", "", false, false)
	order, err := fixture.peer.ConfigurationAssetOrder(test.Context(), fixture.owner, "grokbuild", state.ConfigurationKindProvider)
	if err != nil {
		test.Fatal("actual order")
	}
	data, _ := json.Marshal([]string{copyID, id})
	if os.WriteFile(file, data, 0600) != nil {
		test.Fatal("new order")
	}
	grokRun(test, fixture, []string{"order", "replace", "--expected-order-revision", strconv.FormatInt(order.Revision, 10), "--ids-file", file, "--apply"}, "exact", 0)
	order, err = fixture.peer.ConfigurationAssetOrder(test.Context(), fixture.owner, "grokbuild", state.ConfigurationKindProvider)
	if err != nil || len(order.Assets) != 2 || order.Assets[0].ID != copyID || order.Assets[1].ID != id {
		test.Fatal("actual complete order differs from approved permutation")
	}
	if os.WriteFile(fixture.draft, []byte(grokNativeDraft), 0600) != nil {
		test.Fatal("native fixture")
	}
	before := fixture.mutations.Load()
	grokRun(test, fixture, []string{"update", "--id", id, "--expected-revision", "2", "--draft-file", fixture.draft, "--apply"}, "", 1)
	if fixture.mutations.Load() != before {
		test.Fatal("retained-key native switch mutated")
	}
	grokRun(test, fixture, []string{"update", "--id", id, "--expected-revision", "2", "--draft-file", fixture.draft, "--key-action", "clear", "--apply"}, "exact", 0)
	assertGrokStored(test, fixture, id, 3, "Native G2", "", true, false)
	for _, item := range []struct{ id, revision string }{{id, "3"}, {copyID, "1"}} {
		grokRun(test, fixture, []string{"delete", "--id", item.id, "--expected-revision", item.revision, "--apply"}, "exact", 0)
		if _, _, err := fixture.peer.ReadConfigurationAssetVersion(test.Context(), fixture.owner, item.id); !errors.Is(err, state.ErrConfigurationNotFound) {
			test.Fatal("approved delete not durable")
		}
	}
	if fixture.business.Load() != 8 || fixture.mutations.Load() != 8 {
		test.Fatal("business approval and mutation HTTP counts differ")
	}
}

func TestGrokActualMainReviewRefusals(test *testing.T) {
	fixture := newGrokCLIFixture(test)
	args := []string{"create", "--draft-file", fixture.draft, "--key-action", "replace", "--key-file", fixture.key, "--apply"}
	for _, mode := range []string{"", "stdin-yes", "yes", "bad-key", "bad-final", "cancel-prompt", "full-output", "blocked-input", "full-tty"} {
		test.Run(mode, func(test *testing.T) {
			grokRun(test, fixture, args, mode, 1)
		})
	}
	var count int
	if fixture.base.database.QueryRowContext(test.Context(), "SELECT count(*) FROM grok_cli_test_commits").Scan(&count) != nil || count != 0 || fixture.mutations.Load() != 0 {
		test.Fatal("refused/blocked review reached mutation or committed")
	}
}

func TestGrokActualMainLostReceiptNoReplay(test *testing.T) {
	fixture := newGrokCLIFixture(test)
	fixture.loss.Store(true)
	_, diagnostic := grokRun(test, fixture, []string{"create", "--draft-file", fixture.draft, "--key-action", "replace", "--key-file", fixture.key, "--apply"}, "exact", 1)
	var count int
	if fixture.base.database.QueryRowContext(test.Context(), "SELECT count(*) FROM grok_cli_test_commits WHERE action='create'").Scan(&count) != nil || count != 1 || fixture.business.Load() != 1 || fixture.mutations.Load() != 1 || !strings.Contains(diagnostic, "outcome unknown; do not replay") {
		test.Fatal("committed lost receipt replayed or falsely reported success")
	}
	assets, err := fixture.peer.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 1 {
		test.Fatal("lost receipt durable source identity")
	}
	assertGrokStored(test, fixture, assets[0].ID, 1, "Custom G2", grokTestKey, false, true)
	fixture.loss.Store(false)
	output, _ := grokRun(test, fixture, []string{"list"}, "", 0)
	if !strings.Contains(output, assets[0].ID) || fixture.mutations.Load() != 1 {
		test.Fatal("read reconciliation replayed write")
	}
	test.Logf("ATTEMPTS business=%d mutationHTTP=%d committedCreate=%d (reads/auth separate)", fixture.business.Load(), fixture.mutations.Load(), count)
}

func TestGrokActualMainCASAfterReview(test *testing.T) {
	fixture := newGrokCLIFixture(test)
	output, _ := grokRun(test, fixture, []string{"create", "--draft-file", fixture.draft, "--apply"}, "exact", 0)
	id := grokSavedID(test, output)
	fixture.beforeApprove = func() {
		view, err := configuration.New(fixture.peer).Edit(test.Context(), fixture.owner, id)
		if err != nil {
			test.Fatal("peer edit")
		}
		_, err = configuration.New(fixture.peer).Update(test.Context(), fixture.owner, id, 1, configuration.Draft{Name: "Peer wins", GrokBuildProvider: view.GrokBuildProvider}, nil)
		if err != nil {
			test.Fatal("real second Store CAS race")
		}
	}
	_, diagnostic := grokRun(test, fixture, []string{"update", "--id", id, "--expected-revision", "1", "--draft-file", fixture.draft, "--apply"}, "exact", 1)
	metadata, _, err := fixture.peer.ReadConfigurationAssetVersion(test.Context(), fixture.owner, id)
	if err != nil || metadata.Revision != 2 || metadata.Name != "Peer wins" || !strings.Contains(diagnostic, "revision changed") {
		test.Fatal("CLI silently rebased or hid CAS conflict")
	}
}

func TestGrokActualMainFinalOutputUnknown(test *testing.T) {
	fixture := newGrokCLIFixture(test)
	_, diagnostic := grokRun(test, fixture, []string{"create", "--draft-file", fixture.draft, "--apply"}, "post-output-close", 1)
	var count int
	if fixture.base.database.QueryRowContext(test.Context(), "SELECT count(*) FROM grok_cli_test_commits WHERE action='create'").Scan(&count) != nil || count != 1 || fixture.mutations.Load() != 1 || !strings.Contains(diagnostic, "outcome unknown; do not replay") {
		test.Fatal("committed write plus failed final output not Unknown")
	}
}

func TestGrokActualMainBadFlagsSafe(test *testing.T) {
	fixture := newGrokCLIFixture(test)
	for _, args := range [][]string{{"--token", "private-canary"}, {"list", "--bad-private-canary"}, {"order", "replace", "--expected-order-revision", "private-canary"}} {
		output, diagnostic := grokRun(test, fixture, args, "", 1)
		if strings.Contains(output+diagnostic, "private-canary") || fixture.requests.Load() != 0 {
			test.Fatal("invalid actual argv leaked or reached network")
		}
	}
}

func TestGrokCloseAndOutputHelpers(test *testing.T) {
	for _, text := range []string{"", "short"} {
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		var buffer bytes.Buffer
		output, err := prepareGrokOutput(ctx, &buffer)
		if err != nil {
			test.Fatal("bounded buffer")
		}
		if _, err := output.Write([]byte(text)); err == nil {
			test.Fatal("canceled output allowed")
		}
		_ = output.Close()
	}
	if slices.Contains([]string{errGrokCommand.Error(), errGrokUnknown.Error()}, "private-canary") {
		test.Fatal("unexpected raw error")
	}
}
