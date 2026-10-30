//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/packattachments"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"golang.org/x/sys/unix"
)

func TestPackConfigurationsActualMainOutput(test *testing.T) {
	binary := filepath.Join(test.TempDir(), "cognate")
	build := exec.CommandContext(test.Context(), "go", "build", "-o", binary, "./cmd/cognate")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		test.Fatalf("build actual main: %v %s", err, output)
	}
	for _, mode := range []string{"writable", "full", "closed", "help-cancel", "help-closed"} {
		test.Run(mode, func(test *testing.T) {
			args := []string{"pack", "configurations", "list", "--invalid-SYNTHETIC-NOT-A-SECRET"}
			if strings.HasPrefix(mode, "help-") {
				args = []string{"pack", "configurations", "list", "--help"}
			}
			command := exec.Command(binary, args...)
			command.Env = append(os.Environ(), "HOME="+test.TempDir(), "XDG_CONFIG_HOME="+test.TempDir())
			var diagnostic bytes.Buffer
			command.Stderr = &diagnostic
			var writer *os.File
			var flags int
			if mode == "full" || mode == "help-cancel" {
				_, writer, _ = fullWatchPipe(test)
				if err := unix.SetNonblock(int(writer.Fd()), false); err != nil {
					test.Fatal(err)
				}
				var err error
				flags, err = unix.FcntlInt(writer.Fd(), unix.F_GETFL, 0)
				if err != nil {
					test.Fatal(err)
				}
				if mode == "full" {
					command.Stderr = writer
				} else {
					command.Stdout = writer
				}
			}
			if mode == "closed" || mode == "help-closed" {
				closed, err := os.Open(os.DevNull)
				if err != nil {
					test.Fatal(err)
				}
				defer closed.Close()
				if mode == "closed" {
					command.Stderr = closed
				} else {
					command.Stdout = closed
				}
			}
			if err := command.Start(); err != nil {
				test.Fatal(err)
			}
			completed := make(chan error, 1)
			go func() { completed <- command.Wait() }()
			if mode == "help-cancel" {
				time.Sleep(100 * time.Millisecond)
				if err := command.Process.Signal(os.Interrupt); err != nil {
					_ = command.Process.Kill()
					<-completed
					test.Fatal(err)
				}
			}
			select {
			case err := <-completed:
				if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
					test.Fatalf("expected joined exit 1, got %v", err)
				}
			case <-time.After(2 * time.Second):
				_ = command.Process.Kill()
				<-completed
				test.Fatal("actual main blocked on borrowed output")
			}
			if writer != nil {
				after, err := unix.FcntlInt(writer.Fd(), unix.F_GETFL, 0)
				if err != nil || after != flags {
					test.Fatal("borrowed output flags changed")
				}
			}
			if mode == "writable" && (!strings.Contains(diagnostic.String(), "pack configurations refused") || strings.Contains(diagnostic.String(), "SYNTHETIC-NOT-A-SECRET")) {
				test.Fatal("diagnostic must be fixed safe text")
			}
		})
	}
}

func packCLIAssetFixture(test *testing.T) (*mcpActualCLIFixture, string, string, string) {
	test.Helper()
	fixture := newMCPActualCLIFixture(test, "normal")
	var sequence int
	var name, path string
	if fixture.database.QueryRowContext(test.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path) != nil {
		test.Fatal("physical App database identity")
	}
	store, err := state.Open(test.Context(), path, bytes.Repeat([]byte{85}, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if store.Close() != nil {
			test.Error("peer Store close")
		}
	})
	authentication, err := auth.NewService(store, bytes.Repeat([]byte{85}, 32))
	if err != nil {
		test.Fatal(err)
	}
	owner, err := authentication.Authenticate(test.Context(), fixture.session)
	if err != nil {
		test.Fatal("owner session fixture")
	}
	ref, err := repository.ParseReference("org/pack-cli", "")
	if err != nil {
		test.Fatal(err)
	}
	repo, err := store.CollectRepository(test.Context(), owner, ref, strings.Repeat("a", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Synthetic", Description: "Fixture", Path: "skills/example"}, ContentHash: strings.Repeat("b", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), owner, repo.ID)
	if err != nil {
		test.Fatal(err)
	}
	pack, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "CLI pack", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	asset, err := configuration.New(store).Create(test.Context(), owner, configuration.Draft{Name: "Private name", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	file := filepath.Join(fixture.home, "references.json")
	if os.WriteFile(file, []byte(`[{"assetId":"`+asset.Metadata.ID+`","expectedAssetRevision":1,"targetId":"claude.provider.user"}]`), 0600) != nil {
		test.Fatal("references fixture")
	}
	return fixture, pack.ID, asset.Metadata.ID, file
}

func TestPackConfigurationsErrorClassesSurviveCommandBoundary(test *testing.T) {
	for _, item := range []struct{ source, errorClass error }{{packattachments.ErrDenied, errPackConfigurationsDenied}, {packattachments.ErrConflict, errPackConfigurationsConflict}, {packattachments.ErrTargetUnsupported, errPackConfigurationsUnsupported}, {packattachments.ErrUnknown, errPackConfigurationsUnknown}} {
		if !errors.Is(packConfigurationsError(packConfigurationsError(item.source)), item.errorClass) {
			test.Fatal("known error class lost through nested CLI command")
		}
	}
}

func TestPackConfigurationsDefaultMetadataOnly(test *testing.T) {
	fixture, pack, asset, file := packCLIAssetFixture(test)
	arguments := []string{"configurations", "replace", "--server", fixture.origin, "--pack", pack, "--token-stdin", "--references-file", file, "--expected-pack-revision", "1", "--expected-configuration-revision", "1"}
	command := newPackCommand()
	command.SetArgs(arguments)
	command.SetIn(strings.NewReader(fixture.session))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	test.Setenv("SSL_CERT_FILE", fixture.certificate)
	test.Setenv("SSL_CERT_DIR", filepath.Dir(fixture.certificate))
	if err := command.ExecuteContext(test.Context()); err != nil {
		test.Fatal("owner metadata preview unavailable")
	}
	if !strings.Contains(output.String(), asset) || !strings.Contains(output.String(), "NOT server-validated") || !strings.Contains(output.String(), "NativeUnavailable") || strings.Contains(output.String(), fixture.session) {
		test.Fatal("incomplete or private preview")
	}
	var revision, configurationRevision int64
	if fixture.database.QueryRowContext(test.Context(), "SELECT revision,configuration_revision FROM packs WHERE public_id=?", pack).Scan(&revision, &configurationRevision) != nil || revision != 1 || configurationRevision != 1 {
		test.Fatal("default metadata preview mutated Pack")
	}
}

func TestPackConfigurationsProcess(test *testing.T) {
	if os.Getenv("COGNATE_PACK_ATTACHMENTS_PROCESS") != "1" {
		return
	}
	var args []string
	if json.Unmarshal([]byte(os.Getenv("COGNATE_PACK_ATTACHMENTS_ARGS")), &args) != nil {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 20*time.Second)
	defer cancel()
	if err := selectionCommandRun(ctx, args, os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, packConfigurationsError(err))
		os.Exit(1)
	}
	os.Exit(0)
}

func packConfigurationProcess(test *testing.T, fixture *mcpActualCLIFixture, arguments []string, approval string, want int) string {
	test.Helper()
	arguments = append(arguments, "--server", fixture.origin, "--token-stdin")
	encoded, _ := json.Marshal(arguments)
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPackConfigurationsProcess$", "-test.count=1")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.home, "TMPDIR=" + test.TempDir(), "SSL_CERT_FILE=" + fixture.certificate, "SSL_CERT_DIR=" + filepath.Dir(fixture.certificate), "COGNATE_PACK_ATTACHMENTS_PROCESS=1", "COGNATE_PACK_ATTACHMENTS_ARGS=" + string(encoded)}
	command.Stdin = strings.NewReader(fixture.session)
	if approval == "stdin-yes" {
		command.Stdin = strings.NewReader(fixture.session + "\nyes\n")
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	command.WaitDelay = time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var master, slave *os.File
	if approval != "" && approval != "stdin-yes" {
		master, slave = nativeTestPTY(test)
		command.ExtraFiles = []*os.File{slave}
		command.SysProcAttr.Setctty = true
		command.SysProcAttr.Ctty = 3
	}
	if err := command.Start(); err != nil {
		test.Fatal("real CLI child unavailable")
	}
	defer func() {
		cancel()
		if command.ProcessState == nil {
			_ = command.Wait()
		}
	}()
	var review strings.Builder
	if master != nil {
		if master.SetReadDeadline(time.Now().Add(5*time.Second)) != nil {
			test.Fatal("PTY deadline")
		}
		pattern := regexp.MustCompile(`确认请输入 (REPLACE [A-Z2-7]{26} [a-f0-9]{32})`)
		for review.Len() < 64<<10 {
			data := make([]byte, 512)
			count, readErr := master.Read(data)
			review.Write(data[:count])
			if match := pattern.FindStringSubmatch(review.String()); match != nil {
				if !strings.Contains(review.String(), "NOT server-validated") || !strings.Contains(review.String(), "NativeUnavailable") || !strings.Contains(review.String(), fixture.origin) || !strings.Contains(review.String(), "REPLACE ENTIRE ORDERED SET") {
					test.Fatal("TTY did not show complete metadata review before prompt")
				}
				phrase := approval
				if approval == "exact" {
					phrase = match[1]
				}
				if _, err := fmt.Fprintln(master, phrase); err != nil {
					test.Fatal("PTY confirmation input")
				}
				break
			}
			if readErr != nil {
				test.Fatal("real controlling-TTY exact prompt missing")
			}
		}
	}
	err := command.Wait()
	if ctx.Err() != nil || command.ProcessState == nil {
		test.Fatal("real CLI child did not finish and join")
	}
	actual := 0
	if err != nil {
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			test.Fatal("unexpected child process failure")
		}
		actual = exit.ExitCode()
	}
	if actual != want {
		test.Fatalf("real CLI status %d expected %d", actual, want)
	}
	if strings.Contains(output.String(), fixture.session) || strings.Contains(review.String(), fixture.session) || strings.Contains(output.String(), "synthetic-mcp-private-password") {
		test.Fatal("session leaked from actual CLI")
	}
	return output.String()
}

func TestPackConfigurationsRealTTYReplaceClearAndRefusal(test *testing.T) {
	fixture, pack, asset, file := packCLIAssetFixture(test)
	arguments := []string{"pack", "configurations", "replace", "--pack", pack, "--references-file", file, "--expected-pack-revision", "1", "--expected-configuration-revision", "1", "--apply"}
	packConfigurationProcess(test, fixture, arguments, "", 1)
	packConfigurationProcess(test, fixture, arguments, "stdin-yes", 1)
	packConfigurationProcess(test, fixture, arguments, "yes", 1)
	output := packConfigurationProcess(test, fixture, arguments, "exact", 0)
	if !strings.Contains(output, asset) || !strings.Contains(output, "NativeUnavailable") {
		test.Fatal("committed metadata not shown")
	}
	var revision, configurationRevision int64
	var attachment string
	if fixture.database.QueryRowContext(test.Context(), `SELECT revision,configuration_revision FROM packs WHERE public_id=?`, pack).Scan(&revision, &configurationRevision) != nil || revision != 2 || configurationRevision != 2 {
		test.Fatal("PTY approval did not commit exact dual CAS")
	}
	if fixture.database.QueryRowContext(test.Context(), `SELECT public_id FROM pack_configuration_attachments`).Scan(&attachment) != nil || !strings.Contains(output, attachment) {
		test.Fatal("CLI attachment identity does not exist in actual database")
	}
	packConfigurationProcess(test, fixture, []string{"pack", "configurations", "list", "--pack", pack}, "", 0)
	if os.WriteFile(file, []byte(`[]`), 0600) != nil {
		test.Fatal("explicit clear fixture")
	}
	clearArgs := []string{"pack", "configurations", "replace", "--pack", pack, "--references-file", file, "--expected-pack-revision", "2", "--expected-configuration-revision", "2", "--apply"}
	packConfigurationProcess(test, fixture, clearArgs, "exact", 0)
	var count int
	if fixture.database.QueryRowContext(test.Context(), `SELECT count(*) FROM pack_configuration_attachments`).Scan(&count) != nil || count != 0 {
		test.Fatal("TTY explicit [] did not clear metadata")
	}
}

func TestPackConfigurationsBlockedOutputAndInputDoNotWrite(test *testing.T) {
	fixture, pack, _, file := packCLIAssetFixture(test)
	test.Setenv("SSL_CERT_FILE", fixture.certificate)
	test.Setenv("SSL_CERT_DIR", filepath.Dir(fixture.certificate))
	for _, mode := range []string{"output", "input"} {
		test.Run(mode, func(test *testing.T) {
			ctx, cancel := context.WithTimeout(test.Context(), 400*time.Millisecond)
			defer cancel()
			var input io.Reader = strings.NewReader(fixture.session)
			var output io.Writer = &bytes.Buffer{}
			if mode == "output" {
				_, writer, _ := fullWatchPipe(test)
				output = writer
			} else {
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal(err)
				}
				defer reader.Close()
				defer writer.Close()
				input = reader
			}
			err := selectionCommandRun(ctx, []string{"pack", "configurations", "replace", "--server", fixture.origin, "--pack", pack, "--token-stdin", "--references-file", file, "--expected-pack-revision", "1", "--expected-configuration-revision", "1", "--apply"}, input, output)
			if err == nil {
				test.Fatal("blocked I/O authorized mutation")
			}
			var revision int64
			if fixture.database.QueryRowContext(test.Context(), `SELECT revision FROM packs WHERE public_id=?`, pack).Scan(&revision) != nil || revision != 1 {
				test.Fatal("incomplete I/O mutated Pack")
			}
		})
	}
}
