package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
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

	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

func authMainBinary(test *testing.T) string {
	test.Helper()
	if binary := os.Getenv("COGNATE_AUTH_BINARY"); binary != "" {
		return binary
	}
	return buildNativeCLI(test)
}

type authActualObservation struct {
	login, identity, logout, business atomic.Int32
	parent                            atomic.Int32
	childAtIdentity                   atomic.Bool
	childAtFirstIdentity              atomic.Bool
	token                             func() string
}

func authActualFront(test *testing.T, fixture *mcpActualCLIFixture, mode string) (string, *authActualObservation) {
	test.Helper()
	certificate, err := os.ReadFile(fixture.certificate)
	if err != nil {
		test.Fatal("fixture trust unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certificate) {
		test.Fatal("fixture trust invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, Proxy: nil, DisableCompression: true}
	test.Cleanup(transport.CloseIdleConnections)
	backend, _ := url.Parse(fixture.origin)
	observation := &authActualObservation{}
	var mutex sync.Mutex
	var session string
	release := make(chan struct{})
	observation.token = func() string { mutex.Lock(); defer mutex.Unlock(); return session }
	front := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		login := strings.HasSuffix(request.URL.Path, "/Login")
		identity := strings.HasSuffix(request.URL.Path, "/GetSession")
		logout := strings.HasSuffix(request.URL.Path, "/Logout")
		if login {
			observation.login.Add(1)
		}
		if identity {
			identityCount := observation.identity.Add(1)
			if parent := observation.parent.Load(); parent > 0 && len(authChildPIDs(test, int(parent))) > 0 {
				observation.childAtIdentity.Store(true)
				if identityCount == 1 {
					observation.childAtFirstIdentity.Store(true)
				}
			}
		}
		if logout {
			observation.logout.Add(1)
		}
		if strings.Contains(request.URL.Path, ".MCPConfigurationService/") {
			observation.business.Add(1)
		}
		if logout && mode == "logout-failure" {
			http.Error(writer, "private cleanup canary", 503)
			return
		}
		if identity && mode == "revoked" {
			if _, err := fixture.database.ExecContext(request.Context(), "DELETE FROM sessions WHERE id=(SELECT MAX(id) FROM sessions)"); err != nil {
				test.Error("revocation setup failed")
			}
		}
		if (login && mode == "cancel-login") || (identity && mode == "cancel-identity") {
			_, _ = io.Copy(io.Discard, request.Body)
			select {
			case <-request.Context().Done():
			case <-release:
			}
			return
		}
		forward := request.Clone(request.Context())
		forward.URL = request.URL.Clone()
		forward.URL.Scheme, forward.URL.Host = backend.Scheme, backend.Host
		forward.Host, forward.RequestURI = backend.Host, ""
		response, err := transport.RoundTrip(forward)
		if err != nil {
			http.Error(writer, "fixture unavailable", 503)
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil {
			test.Error("fixture body unavailable")
			return
		}
		if login && response.StatusCode == 200 {
			value := &authv1.LoginResponse{}
			if proto.Unmarshal(data, value) != nil {
				test.Error("fixture login shape changed")
				return
			}
			mutex.Lock()
			session = value.SessionToken
			mutex.Unlock()
			value.Reset()
		}
		if login && mode == "lost-login" {
			connection, _, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				test.Error("fixture response loss unavailable")
				return
			}
			_ = connection.Close()
			return
		}
		if strings.HasSuffix(request.URL.Path, "/CreateAsset") && response.StatusCode == 200 && (mode == "lost-headers" || mode == "lost-body") {
			if mode == "lost-body" {
				writer.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				writer.Header().Set("Content-Length", fmt.Sprint(len(data)+128))
				writer.WriteHeader(200)
				_, _ = writer.Write(data[:len(data)/2])
				_ = http.NewResponseController(writer).Flush()
			}
			connection, _, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				test.Error("mutation response fault unavailable")
				return
			}
			_ = connection.Close()
			return
		}
		if identity && mode == "bad-identity" {
			data, _ = proto.Marshal(&authv1.GetSessionResponse{})
		}
		for name, values := range response.Header {
			if name != "Content-Length" {
				for _, value := range values {
					writer.Header().Add(name, value)
				}
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(data)
		clear(data)
	}))
	test.Cleanup(front.Close)
	test.Cleanup(func() { close(release) })
	return front.URL, observation
}

func authChildPIDs(test *testing.T, parent int) []string {
	test.Helper()
	tasks, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", parent))
	if err != nil {
		test.Fatal("process task metadata unavailable")
	}
	var children []string
	for _, task := range tasks {
		data, err := os.ReadFile(task)
		if err == nil {
			children = append(children, strings.Fields(string(data))...)
		}
	}
	return children
}

func authTTYState(test *testing.T, slave *os.File) unix.Termios {
	test.Helper()
	var state *unix.Termios
	if authTermios(slave, func(descriptor int) error {
		var err error
		state, err = unix.IoctlGetTermios(descriptor, unix.TCGETS)
		return err
	}) != nil {
		test.Fatal("PTY termios unavailable")
	}
	return *state
}

func TestAuthRunActualMutationLifecycle(test *testing.T) {
	for _, mode := range []string{"preview", "confirm", "wrong", "queued", "lost-headers", "lost-body", "logout-failure", "wrong-password", "bad-identity", "revoked", "lost-login", "no-tty", "stdin-yes", "cancel-input", "cancel-login", "cancel-identity", "cancel-review", "kill-review", "closed-output"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newMCPActualCLIFixture(test, "default")
			origin, observation := authActualFront(test, fixture, mode)
			arguments := []string{"auth", "run", "--server", origin, "--", "mcp-assets", "create", "--declaration-file", fixture.draft}
			if mode != "preview" {
				arguments = append(arguments, "--apply")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, authMainBinary(test), arguments...)
			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.home, "XDG_CONFIG_HOME=" + fixture.home, "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.certificate), "AUTH_PRIVATE_ENV_CANARY=do-not-forward-731853"}
			command.Stdin = strings.NewReader("yes\nmcp create\n")
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			var master, slave *os.File
			var before unix.Termios
			if mode != "no-tty" && mode != "stdin-yes" {
				master, slave = nativeTestPTY(test)
				before = authTTYState(test, slave)
				command.ExtraFiles = []*os.File{slave}
				command.SysProcAttr.Setctty, command.SysProcAttr.Ctty = true, 3
			}
			if mode == "closed-output" {
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("output fixture unavailable")
				}
				_ = reader.Close()
				defer writer.Close()
				command.Stdout = writer
			}
			if command.Start() != nil {
				test.Fatal("actual main start failed")
			}
			observation.parent.Store(int32(command.Process.Pid))
			joined := false
			defer func() {
				if !joined {
					cancel()
					_ = command.Wait()
				}
			}()
			var transcript string
			var ownerChild string
			if master != nil {
				transcript = authReadPrompt(test, master, "Username: ")
				if mode == "cancel-input" {
					_ = command.Process.Signal(syscall.SIGTERM)
				} else {
					_, _ = fmt.Fprintln(master, "mcp-cli-real")
					transcript += authReadPrompt(test, master, "Password: ")
					password := "synthetic-mcp-private-password"
					if mode == "wrong-password" {
						password = "incorrect-synthetic-password"
					}
					if mode == "queued" {
						password += "\nmcp create"
					}
					_, _ = fmt.Fprintln(master, password)
					if mode == "cancel-login" || mode == "cancel-identity" {
						deadline := time.Now().Add(3 * time.Second)
						for time.Now().Before(deadline) {
							if mode == "cancel-login" && observation.login.Load() > 0 || mode == "cancel-identity" && observation.identity.Load() > 0 {
								break
							}
							time.Sleep(time.Millisecond)
						}
						_ = command.Process.Signal(syscall.SIGTERM)
					}
					if mode == "confirm" || mode == "wrong" || mode == "queued" || mode == "lost-headers" || mode == "lost-body" || mode == "logout-failure" || mode == "cancel-review" || mode == "kill-review" {
						transcript += authReadPrompt(test, master, "确认请输入 mcp create")
						for _, field := range []string{origin, "synthetic-review-command", `"enabled":false`, "current authenticated session"} {
							if !strings.Contains(transcript, field) {
								test.Fatal("same TTY review incomplete")
							}
						}
						if fixture.httpMutations.Load() != 0 {
							test.Fatal("mutation before terminal approval")
						}
						children := authChildPIDs(test, command.Process.Pid)
						if len(children) != 1 {
							test.Fatal("not exactly one live owner child")
						}
						child := children[0]
						ownerChild = child
						for _, leaf := range []string{"cmdline", "environ"} {
							data, err := os.ReadFile("/proc/" + child + "/" + leaf)
							if err != nil || bytes.Contains(data, []byte(observation.token())) || bytes.Contains(data, []byte("do-not-forward-731853")) || bytes.Contains(data, []byte("synthetic-mcp-private-password")) {
								test.Fatal("child argv/env contains private material")
							}
						}
						switch mode {
						case "wrong", "queued":
							_, _ = fmt.Fprintln(master, "yes")
						case "cancel-review":
							_ = command.Process.Signal(syscall.SIGTERM)
						case "kill-review":
							_ = command.Process.Kill()
						default:
							_, _ = fmt.Fprintln(master, "mcp create")
						}
					}
				}
			}
			err := command.Wait()
			joined = true
			if os.Getenv("COGNATE_AUTH_LIFECYCLE_PROBE") == "1" {
				starts := strings.Count(output.String(), "AUTH_PROBE_START\n")
				waits := strings.Count(output.String(), "AUTH_PROBE_WAIT\n")
				calls := strings.Count(output.String(), "AUTH_PROBE_CONNECT_CREATE\n")
				if starts != 1 || waits != 1 || calls != 1 {
					test.Fatalf("instrumented one-shot violated: Starts=%d Waits=%d ConnectCreate=%d", starts, waits, calls)
				}
			}
			if master != nil {
				_ = master.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
				remaining, _ := io.ReadAll(io.LimitReader(master, 4<<20))
				transcript += string(remaining)
			}
			if ownerChild != "" {
				deadline := time.Now().Add(time.Second)
				for {
					state, err := os.ReadFile("/proc/" + ownerChild + "/stat")
					_, rest, _ := strings.Cut(string(state), ") ")
					if os.IsNotExist(err) || strings.HasPrefix(rest, "Z ") {
						break
					}
					if time.Now().After(deadline) {
						test.Fatal("owner child still live after parent terminal result")
					}
					time.Sleep(time.Millisecond)
				}
			}
			wantSuccess := mode == "preview" || mode == "confirm"
			if (err == nil) != wantSuccess {
				test.Fatal("actual main outcome mismatch")
			}
			var commits, sessions int
			if fixture.database.QueryRowContext(test.Context(), "SELECT count(*) FROM mcp_cli_test_commits").Scan(&commits) != nil || fixture.database.QueryRowContext(test.Context(), "SELECT count(*) FROM sessions").Scan(&sessions) != nil {
				test.Fatal("SQLite observer unavailable")
			}
			wantCommits := 0
			if mode == "confirm" || mode == "lost-headers" || mode == "lost-body" || mode == "logout-failure" {
				wantCommits = 1
			}
			if commits != wantCommits || fixture.httpMutations.Load() != int32(wantCommits) {
				test.Fatal("business replay or write without approval")
			}
			if master != nil && authTTYState(test, slave) != before {
				test.Fatal("catchable input did not restore exact termios")
			}
			if mode == "lost-headers" || mode == "lost-body" {
				if !strings.Contains(output.String(), "unknown") {
					test.Fatal("committed response loss not Unknown")
				}
			}
			if mode == "logout-failure" && !strings.Contains(output.String(), "business result unchanged") {
				test.Fatal("cleanup failure hides business truth")
			}
			if mode != "kill-review" && mode != "lost-login" && mode != "logout-failure" && sessions != 1 {
				test.Fatal("own session not revoked")
			}
			if (mode == "kill-review" || mode == "lost-login" || mode == "logout-failure") && sessions != 2 {
				test.Fatal("unconfirmed logout truth changed")
			}
			if mode == "no-tty" || mode == "stdin-yes" || mode == "cancel-input" {
				if observation.login.Load() != 0 {
					test.Fatal("credentials accepted without private terminal")
				}
			} else if observation.login.Load() != 1 {
				test.Fatal("login replayed")
			}
			if mode == "bad-identity" || mode == "revoked" || mode == "lost-login" || mode == "wrong-password" || mode == "cancel-input" || mode == "cancel-login" || mode == "cancel-identity" {
				if observation.business.Load() != 0 || observation.childAtIdentity.Load() {
					test.Fatal("owner child ran without authenticated identity")
				}
			}
			for _, secret := range []string{"synthetic-mcp-private-password", "mcp-cli-real", observation.token()} {
				if secret != "" && strings.Contains(transcript+output.String(), secret) {
					test.Fatal("real terminal or output disclosed credentials")
				}
			}
			test.Logf("one wrapper; Login=%d GetSession=%d Logout=%d owner HTTP=%d mutation HTTP=%d successful SQLite CAS=%d sessions=%d joined=true", observation.login.Load(), observation.identity.Load(), observation.logout.Load(), observation.business.Load(), fixture.httpMutations.Load(), commits, sessions)
		})
	}
}

func authReadPrompt(test *testing.T, master *os.File, want string) string {
	test.Helper()
	if master.SetReadDeadline(time.Now().Add(6*time.Second)) != nil {
		test.Fatal("PTY deadline unavailable")
	}
	var captured strings.Builder
	for captured.Len() < 4<<20 {
		buffer := make([]byte, 1024)
		count, err := master.Read(buffer)
		captured.Write(buffer[:count])
		if strings.Contains(captured.String(), want) {
			return captured.String()
		}
		if err != nil {
			test.Fatalf("actual main prompt missing: %s", want)
		}
	}
	test.Fatal("PTY output budget exceeded")
	return ""
}

func TestAuthRunActualDelayedLogin(test *testing.T) {
	fixture := newMCPActualCLIFixture(test, "default")
	home := filepath.Join(test.TempDir(), "absent-home")
	ctx, cancel := context.WithTimeout(test.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, authMainBinary(test), "auth", "run", "--server", fixture.origin, "--", "mcp-assets", "list")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.certificate)}
	master, slave := nativeTestPTY(test)
	command.ExtraFiles = []*os.File{slave}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if command.Start() != nil {
		test.Fatal("actual main start failed")
	}
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = command.Wait()
		}
	}()
	transcript := authReadPrompt(test, master, "Username: ")
	_, _ = fmt.Fprintln(master, "mcp-cli-real")
	transcript += authReadPrompt(test, master, "Password: ")
	time.Sleep(12500 * time.Millisecond)
	_, _ = fmt.Fprintln(master, "synthetic-mcp-private-password")
	err := command.Wait()
	joined = true
	if err != nil {
		test.Fatal("delayed real login did not complete owner command")
	}
	if strings.Contains(transcript+output.String(), "synthetic-mcp-private-password") || strings.Contains(transcript, "mcp-cli-real") {
		test.Fatal("account input echoed")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		test.Fatal("auth created HOME state")
	}
	test.Log("real main + TLS App/SQLite owner read after 12.5s password input; no HOME; joined")
}
