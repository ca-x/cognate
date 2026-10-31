package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func grokAuthConfirmation(test *testing.T, terminal *os.File) (string, string) {
	test.Helper()
	if err := terminal.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		test.Fatal("Grok account PTY deadline unavailable")
	}
	pattern := regexp.MustCompile(`确认请输入 ([^\r\n]+)\r?\n`)
	var captured strings.Builder
	for captured.Len() < 128<<10 {
		buffer := make([]byte, 1024)
		count, err := terminal.Read(buffer)
		captured.Write(buffer[:count])
		if match := pattern.FindStringSubmatch(captured.String()); match != nil {
			return captured.String(), match[1]
		}
		if err != nil {
			test.Fatal("Grok account confirmation missing")
		}
	}
	test.Fatal("Grok account PTY output exceeded budget")
	return "", ""
}

func TestAuthRunGrokActualMutation(test *testing.T) {
	for _, mode := range []string{"preview", "confirm", "wrong-final", "lost-headers", "logout-failure"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newGrokCLIFixture(test)
			origin, observation := authActualFront(test, fixture.base, mode)
			arguments := []string{"auth", "run", "--server", origin, "--", "grok-assets", "create", "--draft-file", fixture.draft, "--key-action", "replace", "--key-file", fixture.key}
			if mode != "preview" {
				arguments = append(arguments, "--apply")
			}
			binary, err := grokActualBinary()
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, arguments...)
			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.base.home, "XDG_CONFIG_HOME=" + fixture.base.home, "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.base.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.base.certificate), "GROK_AUTH_ENV_CANARY=do-not-forward"}
			command.Stdin = strings.NewReader("yes\ngrok create\n")
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			master, slave := nativeTestPTY(test)
			before := authTTYState(test, slave)
			command.ExtraFiles = []*os.File{slave}
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
			if err := command.Start(); err != nil {
				test.Fatal("Grok account process start failed")
			}
			joined := false
			defer func() {
				if !joined {
					cancel()
					_ = command.Wait()
				}
			}()
			observation.parent.Store(int32(command.Process.Pid))
			transcript := authReadPrompt(test, master, "Username: ")
			if _, err := fmt.Fprintln(master, "mcp-cli-real"); err != nil {
				test.Fatal("Grok account username input unavailable")
			}
			transcript += authReadPrompt(test, master, "Password: ")
			if _, err := fmt.Fprintln(master, "synthetic-mcp-private-password"); err != nil {
				test.Fatal("Grok account password input unavailable")
			}
			if mode != "preview" {
				review, keyPhrase := grokAuthConfirmation(test, master)
				transcript += review
				for _, field := range []string{origin, "GROK REVIEW BEGIN", "GROK REVIEW END", "NOT been server-validated", "profile-one", "x-ai/model", "GROK_TEST_ENV"} {
					if !strings.Contains(review, field) {
						test.Fatal("Grok account same-terminal review incomplete")
					}
				}
				if !strings.HasPrefix(keyPhrase, "replace grokbuild.api-key ") || fixture.mutations.Load() != 0 {
					test.Fatal("Grok account mutation bypassed key approval")
				}
				children := authChildPIDs(test, command.Process.Pid)
				if len(children) != 1 {
					test.Fatal("Grok account did not start exactly one owner child")
				}
				for _, leaf := range []string{"cmdline", "environ"} {
					data, err := os.ReadFile("/proc/" + children[0] + "/" + leaf)
					if err != nil {
						test.Fatal("Grok account child metadata unavailable")
					}
					for _, secret := range []string{observation.token(), "synthetic-mcp-private-password", grokTestKey, "GROK_AUTH_ENV_CANARY"} {
						if secret == "" || bytes.Contains(data, []byte(secret)) {
							test.Fatal("Grok account private material escaped into child argv/environment")
						}
					}
				}
				if _, err := fmt.Fprintln(master, keyPhrase); err != nil {
					test.Fatal("Grok key confirmation unavailable")
				}
				finalReview, finalPhrase := grokAuthConfirmation(test, master)
				transcript += finalReview
				if finalPhrase != "grok create "+strings.TrimPrefix(keyPhrase, "replace grokbuild.api-key ") || fixture.mutations.Load() != 0 {
					test.Fatal("Grok final approval did not bind the reviewed operation")
				}
				if mode == "wrong-final" {
					finalPhrase = "yes"
				}
				if _, err := fmt.Fprintln(master, finalPhrase); err != nil {
					test.Fatal("Grok final confirmation unavailable")
				}
			}
			err = command.Wait()
			joined = true
			if ctx.Err() != nil || (err == nil) != (mode == "preview" || mode == "confirm") {
				test.Fatal("Grok account actual process outcome mismatch")
			}
			wantCommits := 1
			if mode == "preview" || mode == "wrong-final" {
				wantCommits = 0
			}
			var commits, sessions int
			if fixture.base.database.QueryRowContext(test.Context(), "SELECT count(*) FROM grok_cli_test_commits").Scan(&commits) != nil || fixture.base.database.QueryRowContext(test.Context(), "SELECT count(*) FROM sessions").Scan(&sessions) != nil {
				test.Fatal("Grok account durable observers unavailable")
			}
			wantIdentity := int32(2 + wantCommits)
			if commits != wantCommits || fixture.mutations.Load() != int32(wantCommits) || observation.login.Load() != 1 || observation.identity.Load() != wantIdentity || observation.logout.Load() != 1 || observation.childAtFirstIdentity.Load() || !observation.childAtIdentity.Load() {
				test.Fatalf("Grok account lifecycle mismatch: commits=%d mutations=%d login=%d identity=%d logout=%d child_at_first_identity=%t", commits, fixture.mutations.Load(), observation.login.Load(), observation.identity.Load(), observation.logout.Load(), observation.childAtFirstIdentity.Load())
			}
			wantSessions := 1
			if mode == "logout-failure" {
				wantSessions = 2
			}
			if sessions != wantSessions || authTTYState(test, slave) != before {
				test.Fatal("Grok account session or terminal cleanup mismatch")
			}
			if wantCommits == 1 {
				var id string
				if fixture.base.database.QueryRowContext(test.Context(), "SELECT public_id FROM configuration_assets WHERE agent_id='grokbuild'").Scan(&id) != nil {
					test.Fatal("Grok account committed identity unavailable")
				}
				assertGrokStored(test, fixture, id, 1, "Custom G2", grokTestKey, false, true)
			}
			if mode == "lost-headers" && !strings.Contains(output.String(), errGrokUnknown.Error()) || mode == "logout-failure" && !strings.Contains(output.String(), "business result unchanged") {
				test.Fatal("Grok account response or cleanup uncertainty hidden")
			}
			for _, secret := range []string{observation.token(), "synthetic-mcp-private-password", "mcp-cli-real", grokTestKey} {
				if secret == "" || strings.Contains(transcript+output.String(), secret) {
					test.Fatal("Grok account secret escaped captured output")
				}
			}
		})
	}
}
