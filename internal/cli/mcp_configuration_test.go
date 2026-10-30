//go:build linux

package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
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

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/server"
	"google.golang.org/protobuf/proto"
)

type mcpActualCLIFixture struct {
	origin, session, certificate, draft, home string
	database                                  *sql.DB
	httpMutations                             atomic.Int32
	stop                                      func()
}

func newMCPActualCLIFixture(test *testing.T, mode string) *mcpActualCLIFixture {
	test.Helper()
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private TLS fixture unavailable")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := seed.Certificate()
	pair := seed.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal("synthetic TLS key unavailable")
	}
	defer clear(key)
	for name, data := range map[string][]byte{"master.key": bytes.Repeat([]byte{85}, 32), "tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if os.WriteFile(filepath.Join(directory, name), data, 0600) != nil {
			test.Fatal("private TLS fixture write failed")
		}
		clear(data)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 40*time.Second)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		test.Fatal("actual listener unavailable")
	}
	backendOrigin := "https://" + listener.Addr().String()
	app, err := server.Open(ctx, server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: filepath.Join(directory, "master.key"), TLSCertificate: filepath.Join(directory, "tls.pem"), TLSKey: filepath.Join(directory, "tls.key"), PublicOrigin: backendOrigin, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		cancel()
		_ = listener.Close()
		test.Fatal("actual App open failed")
	}
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { done <- app.Serve(ctx, listener) })
	fixture := &mcpActualCLIFixture{home: test.TempDir(), certificate: filepath.Join(directory, "tls.pem")}
	var stopOnce sync.Once
	fixture.stop = func() {
		stopOnce.Do(func() {
			cancel()
			if <-done != nil {
				test.Error("actual App serve failed")
			}
			workers.Wait()
			if app.Close() != nil {
				test.Error("actual App close failed")
			}
		})
	}
	test.Cleanup(fixture.stop)
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, Proxy: nil, DisableCompression: true, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxConnsPerHost: 2}
	test.Cleanup(transport.CloseIdleConnections)
	backendURL, _ := url.Parse(backendOrigin)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutation := strings.HasSuffix(request.URL.Path, "/CreateAsset")
		if mutation {
			fixture.httpMutations.Add(1)
		}
		forward := request.Clone(request.Context())
		forward.URL = request.URL.Clone()
		forward.URL.Scheme = backendURL.Scheme
		forward.URL.Host = backendURL.Host
		forward.Host = backendURL.Host
		forward.RequestURI = ""
		response, err := transport.RoundTrip(forward)
		if err != nil {
			http.Error(writer, "synthetic transport interrupted", 503)
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil || len(data) > 2<<20 {
			http.Error(writer, "synthetic response refused", 503)
			return
		}
		if mutation && response.StatusCode == 200 && (mode == "lost-headers" || mode == "lost-body") {
			if mode == "lost-body" {
				writer.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				writer.Header().Set("Content-Length", fmt.Sprint(len(data)+128))
				writer.WriteHeader(200)
				_, _ = writer.Write(data[:len(data)/2])
				_ = http.NewResponseController(writer).Flush()
			}
			connection, _, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				test.Error("actual fault connection unavailable")
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
		_, _ = writer.Write(data)
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	proxy.StartTLS()
	test.Cleanup(proxy.Close)
	fixture.origin = proxy.URL
	authentication := authv1connect.NewAuthServiceClient(&http.Client{Transport: transport, Timeout: 5 * time.Second}, backendOrigin)
	if _, err := authentication.Register(ctx, connect.NewRequest(&authv1.RegisterRequest{Username: "mcp-cli-real", Password: "synthetic-mcp-private-password"})); err != nil {
		test.Fatal("actual registration failed")
	}
	login, err := authentication.Login(ctx, connect.NewRequest(&authv1.LoginRequest{Username: "mcp-cli-real", Password: "synthetic-mcp-private-password"}))
	if err != nil {
		test.Fatal("actual login failed")
	}
	fixture.session = login.Msg.SessionToken
	fixture.database, err = sql.Open("sqlite3", filepath.Join(directory, "data", "server.db"))
	if err != nil {
		test.Fatal("actual SQLite observer unavailable")
	}
	fixture.database.SetMaxOpenConns(1)
	test.Cleanup(func() {
		if fixture.database.Close() != nil {
			test.Error("actual observer close failed")
		}
	})
	for _, statement := range []string{`CREATE TABLE mcp_cli_test_commits(revision INTEGER NOT NULL)`, `CREATE TRIGGER mcp_cli_test_committed_create AFTER INSERT ON configuration_assets WHEN NEW.kind='mcp' BEGIN INSERT INTO mcp_cli_test_commits VALUES(NEW.revision); END`} {
		if _, err := fixture.database.ExecContext(ctx, statement); err != nil {
			test.Fatal("actual commit observer setup failed")
		}
	}
	fixture.draft = filepath.Join(directory, "declaration.json")
	if os.WriteFile(fixture.draft, []byte(`{"name":"tools","agentId":"claude","document":{"servers":[{"id":"stdio","name":"stdio","enabled":false,"stdio":{"command":"synthetic-review-command","args":{},"env":{}}},{"id":"http","name":"http","enabled":true,"http":{"url":"https://example.test/mcp","headers":{}}},{"id":"sse","name":"sse","enabled":false,"sse":{"url":"https://example.test/events"}}]}}`), 0600) != nil {
		test.Fatal("actual declaration unavailable")
	}
	return fixture
}

func TestMCPCLIActualProcess(test *testing.T) {
	if os.Getenv("COGNATE_MCP_REAL_PROCESS") != "1" {
		return
	}
	var arguments []string
	if json.Unmarshal([]byte(os.Getenv("COGNATE_MCP_REAL_ARGUMENTS")), &arguments) != nil {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 25*time.Second)
	defer cancel()
	if os.Getenv("COGNATE_MCP_REAL_SHORT") == "1" {
		var shortCancel context.CancelFunc
		ctx, shortCancel = context.WithTimeout(ctx, 300*time.Millisecond)
		defer shortCancel()
	}
	if err := selectionCommandRun(ctx, arguments, os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, mcpCommandError(err))
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMCPCLIActualTLSReviewAndUnknown(test *testing.T) {
	for _, mode := range []string{"default", "confirm", "no-tty", "stdin-yes", "wrong", "timeout", "kill", "closed-output", "lost-headers", "lost-body"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newMCPActualCLIFixture(test, mode)
			arguments := []string{"mcp-assets", "create", "--server", fixture.origin, "--token-stdin", "--declaration-file", fixture.draft}
			if mode != "default" {
				arguments = append(arguments, "--apply")
			}
			data, _ := json.Marshal(arguments)
			ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMCPCLIActualProcess$", "-test.count=1")
			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.home, "XDG_CONFIG_HOME=" + fixture.home, "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.certificate), "COGNATE_MCP_REAL_PROCESS=1", "COGNATE_MCP_REAL_ARGUMENTS=" + string(data)}
			if mode == "timeout" {
				command.Env = append(command.Env, "COGNATE_MCP_REAL_SHORT=1")
			}
			if os.Getenv("COGNATE_MCP_CLI_LAYER_PROBE") == "1" {
				command.Env = append(command.Env, "COGNATE_MCP_CLI_LAYER_PROBE=1")
			}
			command.Stdin = strings.NewReader(fixture.session)
			if mode == "stdin-yes" {
				command.Stdin = strings.NewReader(fixture.session + "\nyes\n")
			}
			var capture bytes.Buffer
			command.Stdout = &capture
			command.Stderr = &capture
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			command.WaitDelay = time.Second
			var master *os.File
			if mode != "default" && mode != "no-tty" && mode != "stdin-yes" && mode != "closed-output" {
				var slave *os.File
				master, slave = nativeTestPTY(test)
				command.ExtraFiles = []*os.File{slave}
				command.SysProcAttr.Setctty = true
				command.SysProcAttr.Ctty = 3
			}
			if mode == "closed-output" {
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("output pipe unavailable")
				}
				_ = reader.Close()
				defer writer.Close()
				command.Stdout = writer
				command.Stderr = writer
			}
			if err := command.Start(); err != nil {
				test.Fatal("actual CLI process start failed")
			}
			test.Cleanup(func() {
				cancel()
				if command.ProcessState == nil {
					_ = command.Wait()
				}
			})
			if master != nil {
				if master.SetReadDeadline(time.Now().Add(3*time.Second)) != nil {
					test.Fatal("actual PTY deadline unavailable")
				}
				var review strings.Builder
				for review.Len() < 4<<20 {
					buffer := make([]byte, 512)
					count, err := master.Read(buffer)
					review.Write(buffer[:count])
					if strings.Contains(review.String(), "确认请输入 mcp create") {
						break
					}
					if err != nil {
						cancel()
						_ = command.Wait()
						test.Fatal("actual full review prompt missing")
					}
				}
				for _, field := range []string{"synthetic-review-command", `"enabled":false`, `"args":{"values":[]}`, `"env":{"values":[]}`, `"sse"`, fixture.origin, "current authenticated session", "can_apply=false", "applied=false"} {
					if !strings.Contains(review.String(), field) {
						cancel()
						_ = command.Wait()
						test.Fatal("actual TTY declaration review incomplete")
					}
				}
				if fixture.httpMutations.Load() != 0 {
					test.Fatal("actual mutation preceded approval")
				}
				switch mode {
				case "confirm", "lost-headers", "lost-body":
					_, _ = fmt.Fprintln(master, "mcp create")
				case "wrong":
					_, _ = fmt.Fprintln(master, "yes")
				case "kill":
					cancel()
				}
			}
			err := command.Wait()
			if command.ProcessState == nil {
				test.Fatal("actual CLI child not joined")
			}
			expected := 0
			if mode == "confirm" || mode == "lost-headers" || mode == "lost-body" {
				expected = 1
			}
			var commits int
			if fixture.database.QueryRowContext(test.Context(), `SELECT count(*) FROM mcp_cli_test_commits`).Scan(&commits) != nil {
				test.Fatal("actual successful CAS audit unavailable")
			}
			if commits != expected || fixture.httpMutations.Load() != int32(expected) {
				test.Fatal("real HTTP/committed SQL mutation counts mismatch")
			}
			if (err == nil) != (mode == "default" || mode == "confirm") {
				test.Fatal("actual CLI terminal result mismatch")
			}
			if (mode == "lost-headers" || mode == "lost-body") && !strings.Contains(capture.String(), "unknown") {
				test.Fatal("committed response loss not classified Unknown")
			}
			for _, private := range []string{fixture.session, fixture.draft, "synthetic-review-command", `"servers"`, `"agentId"`} {
				if strings.Contains(capture.String(), private) {
					test.Fatal("real CLI stdout leaked token/path/declaration")
				}
			}
			test.Logf("application command=1 HTTP mutations=%d successful SQLite CAS=%d child joined=true", fixture.httpMutations.Load(), commits)
			if os.Getenv("COGNATE_MCP_CLI_LAYER_PROBE") == "1" {
				connectCalls := strings.Count(capture.String(), "MCP_PROBE_CONNECT /skillhub.configuration.mcp.v1.MCPConfigurationService/CreateAsset\n")
				if connectCalls != expected {
					test.Fatal("actual Connect invocation count mismatch")
				}
				test.Logf("compiled metadata probe: Connect=%d HTTP=%d successful SQLite CAS=%d", connectCalls, fixture.httpMutations.Load(), commits)
			}
		})
	}
}

func mcpActualCommand(test *testing.T, fixture *mcpActualCLIFixture, arguments []string, phrase string) string {
	test.Helper()
	arguments = append(arguments, "--server", fixture.origin, "--token-stdin")
	encoded, _ := json.Marshal(arguments)
	ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMCPCLIActualProcess$", "-test.count=1")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.home, "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.certificate), "COGNATE_MCP_REAL_PROCESS=1", "COGNATE_MCP_REAL_ARGUMENTS=" + string(encoded)}
	command.Stdin = strings.NewReader(fixture.session)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.WaitDelay = time.Second
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	var master *os.File
	if phrase != "" {
		var slave *os.File
		master, slave = nativeTestPTY(test)
		command.ExtraFiles = []*os.File{slave}
		command.SysProcAttr.Setctty = true
		command.SysProcAttr.Ctty = 3
	}
	if command.Start() != nil {
		test.Fatal("actual lifecycle child start failed")
	}
	test.Cleanup(func() {
		cancel()
		if command.ProcessState == nil {
			_ = command.Wait()
		}
	})
	if master != nil {
		if master.SetReadDeadline(time.Now().Add(3*time.Second)) != nil {
			test.Fatal("actual lifecycle PTY deadline unavailable")
		}
		var review strings.Builder
		for review.Len() < 4<<20 {
			buffer := make([]byte, 512)
			count, err := master.Read(buffer)
			review.Write(buffer[:count])
			if strings.Contains(review.String(), "确认请输入 "+phrase) {
				break
			}
			if err != nil {
				cancel()
				_ = command.Wait()
				test.Fatal("actual lifecycle TTY review missing")
			}
		}
		if !strings.Contains(review.String(), fixture.origin) || !strings.Contains(review.String(), "current authenticated session") {
			cancel()
			_ = command.Wait()
			test.Fatal("actual lifecycle review lacks identity/origin")
		}
		if strings.Contains(review.String(), "synthetic-cli-value-secret") || strings.Contains(review.String(), "c3ludGhldGljLWNsaS12YWx1ZS1zZWNyZXQ=") {
			cancel()
			_ = command.Wait()
			test.Fatal("actual lifecycle secret in TTY")
		}
		_, _ = fmt.Fprintln(master, phrase)
	}
	if command.Wait() != nil {
		test.Fatal("actual lifecycle command failed")
	}
	if command.ProcessState == nil {
		test.Fatal("actual lifecycle child not joined")
	}
	for _, private := range []string{fixture.session, fixture.draft, "synthetic-cli-value-secret", "c3ludGhldGljLWNsaS12YWx1ZS1zZWNyZXQ=", `"servers"`, `"agentId"`} {
		if strings.Contains(output.String(), private) {
			test.Fatal("actual lifecycle stdout privacy failed")
		}
	}
	return output.String()
}

func TestMCPCLIActualTLSEightCommands(test *testing.T) {
	fixture := newMCPActualCLIFixture(test, "")
	draft := `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"stdio","name":"stdio","enabled":true,"stdio":{"command":"synthetic-review-command","env":{"values":[{"name":"TOKEN","slotRef":"env/stdio/TOKEN"}]}}}]}}`
	if os.WriteFile(fixture.draft, []byte(draft), 0600) != nil {
		test.Fatal("actual lifecycle draft unavailable")
	}
	actions := filepath.Join(test.TempDir(), "actions.json")
	if os.WriteFile(actions, []byte(`[{"slotId":"env/stdio/TOKEN","replace":"c3ludGhldGljLWNsaS12YWx1ZS1zZWNyZXQ="}]`), 0600) != nil {
		test.Fatal("actual lifecycle actions unavailable")
	}
	mcpActualCommand(test, fixture, []string{"mcp-assets", "preview", "--declaration-file", fixture.draft, "--secrets-file", actions}, "")
	mcpActualCommand(test, fixture, []string{"mcp-assets", "create", "--declaration-file", fixture.draft, "--secrets-file", actions}, "")
	var commits int
	if fixture.database.QueryRowContext(test.Context(), `SELECT count(*) FROM mcp_cli_test_commits`).Scan(&commits) != nil || commits != 0 {
		test.Fatal("actual default/preview wrote")
	}
	mcpActualCommand(test, fixture, []string{"mcp-assets", "create", "--declaration-file", fixture.draft, "--secrets-file", actions, "--apply"}, "mcp create")
	var id string
	if fixture.database.QueryRowContext(test.Context(), `SELECT public_id FROM configuration_assets WHERE kind='mcp'`).Scan(&id) != nil {
		test.Fatal("actual created ID unavailable")
	}
	if !strings.Contains(mcpActualCommand(test, fixture, []string{"mcp-assets", "list"}, ""), id) || !strings.Contains(mcpActualCommand(test, fixture, []string{"mcp-assets", "get", "--id", id}, ""), id) {
		test.Fatal("actual metadata output incomplete")
	}
	if !strings.Contains(mcpActualCommand(test, fixture, []string{"mcp-assets", "read", "--id", id}, ""), `present=true`) {
		test.Fatal("actual refs/presence output incomplete")
	}
	if os.WriteFile(fixture.draft, []byte(strings.Replace(draft, `"enabled":true`, `"enabled":false`, 1)), 0600) != nil || os.WriteFile(actions, []byte(`[{"slotId":"env/stdio/TOKEN","keep":{}}]`), 0600) != nil {
		test.Fatal("actual update input unavailable")
	}
	mcpActualCommand(test, fixture, []string{"mcp-assets", "update", "--id", id, "--expected-revision", "1", "--declaration-file", fixture.draft, "--secrets-file", actions, "--apply"}, "mcp update "+id+" 1")
	mcpActualCommand(test, fixture, []string{"mcp-assets", "copy", "--id", id, "--expected-source-revision", "2", "--name", "copied", "--apply"}, "mcp copy "+id+" 2")
	var copied string
	if fixture.database.QueryRowContext(test.Context(), `SELECT public_id FROM configuration_assets WHERE name='copied'`).Scan(&copied) != nil {
		test.Fatal("actual copied ID unavailable")
	}
	if !strings.Contains(mcpActualCommand(test, fixture, []string{"mcp-assets", "read", "--id", copied}, ""), `present=false`) {
		test.Fatal("actual copy did not omit credential")
	}
	mcpActualCommand(test, fixture, []string{"mcp-assets", "delete", "--id", copied, "--expected-revision", "1", "--apply"}, "mcp delete "+copied+" 1")
	if os.WriteFile(fixture.draft, []byte(`{"name":"tools","agentId":"claude","document":{}}`), 0600) != nil || os.WriteFile(actions, []byte(`[{"slotId":"env/stdio/TOKEN","clear":{"confirmed":true}}]`), 0600) != nil {
		test.Fatal("actual old-only removal unavailable")
	}
	mcpActualCommand(test, fixture, []string{"mcp-assets", "update", "--id", id, "--expected-revision", "2", "--declaration-file", fixture.draft, "--secrets-file", actions, "--apply"}, "mcp update "+id+" 2")
	mcpActualCommand(test, fixture, []string{"mcp-assets", "delete", "--id", id, "--expected-revision", "3", "--apply"}, "mcp delete "+id+" 3")
	var remaining int
	if fixture.database.QueryRowContext(test.Context(), `SELECT count(*) FROM configuration_assets WHERE kind='mcp'`).Scan(&remaining) != nil || remaining != 0 {
		test.Fatal("actual lifecycle did not finish empty")
	}
	test.Log("actual CLI eight commands with real PTY, verified TLS App/auth/SQLite/moejs: default zero-write, KEEP/CLEAR/CAS, copy without credential, delete complete")
}

func TestMCPCLIHasEightOwnerCommands(test *testing.T) {
	command := New()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"mcp-assets", "--help"})
	if command.ExecuteContext(test.Context()) != nil {
		test.Fatal("MCP owner-management command missing")
	}
	for _, name := range []string{"list", "get", "read", "preview", "create", "update", "copy", "delete"} {
		if !strings.Contains(output.String(), name) {
			test.Fatal("ledger method missing from help")
		}
	}
}

func TestMCPCLIOutputFailureAndCancellationNeverMutate(test *testing.T) {
	for _, mode := range []string{"unsupported-writer", "closed-output", "full-output", "input-timeout", "request-cancel"} {
		test.Run(mode, func(test *testing.T) {
			var writes atomic.Int32
			entered, drained := make(chan struct{}), make(chan struct{})
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var response proto.Message
				switch request.URL.Path {
				case "/skillhub.auth.v1.AuthService/GetSession":
					response = &authv1.GetSessionResponse{UserId: "synthetic-owner"}
				case "/skillhub.configuration.mcp.v1.MCPConfigurationService/Preview":
					if mode == "request-cancel" {
						if _, err := io.Copy(io.Discard, io.LimitReader(request.Body, 2<<20)); err != nil {
							test.Error("request fixture body drain failed")
							return
						}
						close(entered)
						select {
						case <-request.Context().Done():
							close(drained)
						case <-stop:
						}
						return
					}
					response = &mcpv1.PreviewResponse{Readiness: mcpv1.Readiness_READINESS_COMPLETE, Warnings: []mcpv1.Warning{mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED}}
				default:
					writes.Add(1)
					http.Error(writer, "synthetic-private-upstream", 503)
					return
				}
				data, _ := proto.Marshal(response)
				writer.Header().Set("Content-Type", "application/proto")
				_, _ = writer.Write(data)
			}))
			defer func() { close(stop); server.Close() }()
			path := filepath.Join(test.TempDir(), "declaration.json")
			if os.WriteFile(path, []byte(`{"name":"tools","agentId":"claude","document":{}}`), 0600) != nil {
				test.Fatal("fixture write failed")
			}
			ctx, cancel := context.WithTimeout(test.Context(), time.Second)
			defer cancel()
			var output io.Writer = &bytes.Buffer{}
			var input io.Reader = strings.NewReader(selectionCommandSession)
			switch mode {
			case "unsupported-writer":
				output = io.Discard
			case "closed-output":
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("pipe unavailable")
				}
				_ = reader.Close()
				defer writer.Close()
				output = writer
			case "full-output":
				_, writer, _ := fullWatchPipe(test)
				output = writer
			case "input-timeout":
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("pipe unavailable")
				}
				defer reader.Close()
				defer writer.Close()
				input = reader
			}
			var workers sync.WaitGroup
			triggered := make(chan bool, 1)
			if mode == "request-cancel" {
				workers.Go(func() {
					select {
					case <-entered:
						triggered <- true
						cancel()
					case <-ctx.Done():
						triggered <- false
					}
				})
			}
			err := selectionCommandRun(ctx, []string{"mcp-assets", "create", "--server", server.URL, "--token-stdin", "--declaration-file", path, "--apply"}, input, output)
			cancel()
			workers.Wait()
			if err == nil || writes.Load() != 0 {
				test.Fatal("I/O failure or cancellation admitted mutation")
			}
			if mode == "request-cancel" {
				if !<-triggered {
					test.Fatal("cancellation never reached actual handler")
				}
				select {
				case <-drained:
				case <-time.After(time.Second):
					test.Fatal("canceled handler did not drain")
				}
			}
		})
	}
}

func TestMCPCLIDefaultPreviewAndNoApprovalShortcuts(test *testing.T) {
	var writes, previews atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var response proto.Message
		switch request.URL.Path {
		case "/skillhub.auth.v1.AuthService/GetSession":
			response = &authv1.GetSessionResponse{UserId: "synthetic-owner"}
		case "/skillhub.configuration.mcp.v1.MCPConfigurationService/Preview":
			previews.Add(1)
			data, _ := io.ReadAll(request.Body)
			var preview mcpv1.PreviewRequest
			if proto.Unmarshal(data, &preview) != nil || preview.Draft == nil || preview.Draft.Document == nil {
				test.Error("generated request missing required draft")
			}
			response = &mcpv1.PreviewResponse{Readiness: mcpv1.Readiness_READINESS_COMPLETE, Warnings: []mcpv1.Warning{mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED}}
		default:
			writes.Add(1)
			http.Error(writer, "synthetic refusal", 500)
			return
		}
		data, _ := proto.Marshal(response)
		writer.Header().Set("Content-Type", "application/proto")
		_, _ = writer.Write(data)
	}))
	defer server.Close()
	path := filepath.Join(test.TempDir(), "declaration.json")
	if os.WriteFile(path, []byte(`{"name":"tools","agentId":"claude","document":{"servers":[]}}`), 0600) != nil {
		test.Fatal("input setup failed")
	}
	command := New()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetIn(strings.NewReader(strings.Repeat("a", 43) + "." + strings.Repeat("b", 43)))
	command.SetArgs([]string{"mcp-assets", "create", "--server", server.URL, "--token-stdin", "--declaration-file", path})
	if err := command.ExecuteContext(test.Context()); err != nil {
		test.Fatal("default preview failed")
	}
	if writes.Load() != 0 || previews.Load() != 1 || strings.Contains(output.String(), path) || strings.Contains(output.String(), "synthetic-owner") {
		test.Fatal("default command wrote or exposed private identity/path")
	}
	for _, arguments := range [][]string{
		{"mcp-assets", "create", "--server", server.URL, "--token-stdin", "--declaration-file", path, "--apply", "--yes"},
		{"mcp-assets", "create", "--server", server.URL, "--token-stdin", "--declaration-file", path, "--secret", "synthetic-private-flag"},
		{"mcp-assets", "not-a-command-synthetic-private"},
	} {
		command := New()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetIn(strings.NewReader("yes"))
		command.SetArgs(arguments)
		err := command.ExecuteContext(test.Context())
		if !errors.Is(err, errMCPCLI) {
			test.Fatal("invalid flags not safely rejected")
		}
		if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "synthetic-private") || output.Len() != 0 {
			test.Fatal("unsafe flag diagnostics")
		}
	}
	if writes.Load() != 0 {
		test.Fatal("approval shortcut caused a mutation")
	}
}

func TestMCPCLIGeneratedMappingOnlyRealTTY(test *testing.T) {
	for _, mode := range []string{"confirm", "no-tty", "stdin-yes", "wrong", "timeout", "kill", "unknown"} {
		test.Run(mode, func(test *testing.T) {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var response proto.Message
				switch request.URL.Path {
				case "/skillhub.auth.v1.AuthService/GetSession":
					response = &authv1.GetSessionResponse{UserId: "synthetic-owner"}
				case "/skillhub.configuration.mcp.v1.MCPConfigurationService/Preview":
					response = &mcpv1.PreviewResponse{Readiness: mcpv1.Readiness_READINESS_COMPLETE, Warnings: []mcpv1.Warning{mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED}}
				case "/skillhub.configuration.mcp.v1.MCPConfigurationService/CreateAsset":
					writes.Add(1)
					if mode == "unknown" {
						http.Error(writer, "synthetic-private-upstream-detail", 503)
						return
					}
					response = &mcpv1.CreateAssetResponse{View: &mcpv1.View{Asset: &configurationv1.Asset{Id: strings.Repeat("A", 26), AgentId: "claude", Kind: configurationv1.AssetKind_ASSET_KIND_MCP, Name: "tools", SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}, Draft: &mcpv1.Draft{Name: "tools", AgentId: "claude", Document: &mcpv1.Document{}}, Readiness: mcpv1.Readiness_READINESS_COMPLETE}}
				default:
					http.Error(writer, "synthetic refusal", 500)
					return
				}
				data, _ := proto.Marshal(response)
				writer.Header().Set("Content-Type", "application/proto")
				_, _ = writer.Write(data)
			}))
			defer server.Close()
			path := filepath.Join(test.TempDir(), "declaration.json")
			if os.WriteFile(path, []byte(`{"name":"tools","agentId":"claude","document":{}}`), 0600) != nil {
				test.Fatal("fixture write failed")
			}
			arguments := []string{"mcp-assets", "create", "--server", server.URL, "--token-stdin", "--declaration-file", path, "--apply"}
			var master, slave *os.File
			if mode != "no-tty" && mode != "stdin-yes" {
				master, slave = nativeTestPTY(test)
			}
			command, capture, cancel := selectionCommandChild(test, &selectionCommandRPC{home: test.TempDir()}, arguments, slave, mode == "timeout", mode == "stdin-yes")
			defer cancel()
			if master != nil {
				if master.SetReadDeadline(time.Now().Add(2*time.Second)) != nil {
					test.Fatal("PTY read deadline failed")
				}
				var review strings.Builder
				for review.Len() < 64<<10 {
					buffer := make([]byte, 512)
					count, err := master.Read(buffer)
					if count > 0 {
						review.Write(buffer[:count])
					}
					if strings.Contains(review.String(), "确认请输入 mcp create") {
						break
					}
					if err != nil {
						cancel()
						_ = command.Wait()
						test.Fatal("complete controlling-TTY prompt missing")
					}
				}
				for _, field := range []string{`"name":"tools"`, `"agentId":"claude"`, `"servers":[]`, "current authenticated session", "can_apply=false", "applied=false", "确认请输入 mcp create"} {
					if !strings.Contains(review.String(), field) {
						cancel()
						_ = command.Wait()
						test.Fatal("TTY review incomplete")
					}
				}
				if writes.Load() != 0 {
					cancel()
					_ = command.Wait()
					test.Fatal("mutation preceded complete review/confirmation")
				}
				switch mode {
				case "confirm", "unknown":
					_, _ = fmt.Fprintln(master, "mcp create")
				case "wrong":
					_, _ = fmt.Fprintln(master, "yes")
				case "kill":
					cancel()
				}
			}
			err := command.Wait()
			expected := int32(0)
			if mode == "confirm" || mode == "unknown" {
				expected = 1
			}
			if writes.Load() != expected {
				test.Fatal("PTY approval or no-replay boundary violated")
			}
			if (err == nil) != (mode == "confirm") {
				test.Fatal("wrong process terminal outcome")
			}
			if command.ProcessState == nil {
				test.Fatal("child not joined")
			}
			for _, private := range []string{path, selectionCommandSession, "synthetic-private-upstream-detail", "synthetic-owner", `"servers"`, `"agentId"`} {
				if strings.Contains(capture.text(), private) {
					test.Fatal("stdout/stderr leaked private review/token/upstream detail")
				}
			}
		})
	}
}
