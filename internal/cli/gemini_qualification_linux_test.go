//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"golang.org/x/sys/unix"
)

func geminiRunOrdinaryCLI(test *testing.T, fixture *codexCLIFixture, arguments []string, answer string) (string, int) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, codexCLIBinary(test), arguments...)
	command.Env = append(fixture.environment(), "GEMINI_CLI_NO_RELAUNCH=1", "NODE_USE_SYSTEM_CA=1", "TZ=UTC")
	command.Stdin = strings.NewReader(fixture.session)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if answer == "closed-output" {
		reader, writer, err := os.Pipe()
		if err != nil {
			test.Fatal("private closed output pipe")
		}
		defer writer.Close()
		if reader.Close() != nil {
			test.Fatal("private output reader close")
		}
		command.Stdout = writer
	}
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
		test.Fatal("ordinary Gemini CLI start")
	}
	joined := false
	defer func() {
		if !joined {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		clear(output.Bytes())
	}()
	operation, plan, pending, transcript := "", "", "", ""
	if master != nil && answer != "closed-output" && answer != "early-reject" {
		reviewed := false
		finished := false
		for !finished {
			if master.SetReadDeadline(time.Now().Add(20*time.Second)) != nil {
				test.Fatal("actual controlling PTY deadline")
			}
			var buffer [2048]byte
			count, err := master.Read(buffer[:])
			if err != nil {
				test.Fatal("actual Gemini review not reached")
			}
			pending += string(buffer[:count])
			transcript += string(buffer[:count])
			if len(transcript) > 64<<10 {
				test.Fatal("actual Gemini PTY budget")
			}
			for strings.Contains(pending, "\n") {
				line, rest, _ := strings.Cut(pending, "\n")
				pending = rest
				line = strings.TrimSuffix(line, "\r")
				if strings.HasPrefix(line, "GEMINI REVIEW BEGIN ") {
					reviewed = false
					for _, field := range strings.Fields(line) {
						if value, ok := strings.CutPrefix(field, "operation="); ok {
							operation = value
						}
						if value, ok := strings.CutPrefix(field, "plan="); ok {
							plan = value
						}
					}
				}
				if strings.HasPrefix(line, "GEMINI REVIEW END ") {
					if len(operation) != 32 || len(plan) != 32 || !strings.Contains(line, "plan="+plan) {
						test.Fatal("actual TTY review binding")
					}
					for _, field := range geminiCLIFields {
						if !strings.Contains(transcript, string(field)) {
							test.Fatal("actual TTY omitted provider field")
						}
					}
					for _, warning := range []string{"双rename非原子", "关闭正在运行", "不迁移OAuth", "selection ", "confirmation "} {
						if !strings.Contains(transcript, warning) {
							test.Fatal("actual TTY omitted lifecycle warning")
						}
					}
					reviewed = true
				}
				_, expected, prompt := strings.Cut(line, "确认请输入 ")
				if !prompt {
					continue
				}
				if !reviewed || !strings.HasSuffix(expected, " "+plan) {
					test.Fatal("prompt without complete same-plan review")
				}
				response := expected
				stopAnswer := false
				switch answer {
				case "empty":
					response = ""
					stopAnswer = true
				case "empty-slot":
					if strings.HasPrefix(expected, "approve ") {
						response = ""
						stopAnswer = true
					}
				case "empty-final":
					if strings.HasPrefix(expected, "confirm ") {
						response = ""
						stopAnswer = true
					}
				case "foreign-plan":
					response = "confirm " + strings.Repeat("0", 32)
					stopAnswer = true
				case "interrupt":
					_ = command.Process.Signal(os.Interrupt)
					finished = true
				}
				if !finished {
					if _, err := master.Write([]byte(response + "\n")); err != nil {
						test.Fatal("actual TTY answer")
					}
				}
				if stopAnswer || finished || strings.HasPrefix(expected, "confirm ") {
					finished = true
					break
				}
			}
		}
	}
	err := command.Wait()
	joined = true
	if ctx.Err() != nil {
		test.Fatal("ordinary Gemini CLI did not terminate+join")
	}
	code := 0
	if err != nil {
		failure, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			test.Fatal("ordinary CLI wait failure")
		}
		code = failure.ExitCode()
	}
	for _, canary := range []string{fixture.session, "synthetic-gemini-a#=$'\"key", "synthetic-gemini-b#=$'\"key"} {
		if bytes.Contains(output.Bytes(), []byte(canary)) || strings.Contains(transcript, canary) {
			test.Fatal("ordinary CLI propagated synthetic material")
		}
	}
	return operation, code
}

func geminiConsumerRequest(test *testing.T, fixture *codexCLIFixture, model, key, transport, label string) {
	test.Helper()
	harness, evidence, node, pack := os.Getenv("COGNATE_GEMINI_CONSUMER"), os.Getenv("COGNATE_GEMINI_EVIDENCE"), os.Getenv("COGNATE_GEMINI_NODE"), os.Getenv("COGNATE_GEMINI_PACKAGE")
	if harness == "" || evidence == "" || node == "" || pack == "" {
		test.Fatal("actual complete pinned consumer harness not supplied")
	}
	forbidden := []string{"synthetic-gemini-unselected-key"}
	for _, candidate := range []string{"synthetic-gemini-a#=$'\"key", "synthetic-gemini-b#=$'\"key"} {
		if candidate != key {
			forbidden = append(forbidden, candidate)
		}
	}
	oracle, _ := json.Marshal(map[string]any{"Model": model, "Transport": transport, "Key": key, "AuthBoundary": key == "", "AllowRouter": label == "no-models", "Forbidden": forbidden, "TLSCertificate": fixture.certificate, "TLSKey": filepath.Join(filepath.Dir(fixture.certificate), "tls.key"), "Manifest": filepath.Join("internal", "geminiconfig", "testdata", "native-profile-v0.61.0", "package-files.sha256")})
	defer clear(oracle)
	ctx, cancel := context.WithTimeout(test.Context(), 50*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", harness, fixture.home, fixture.request.Directory, node, pack, filepath.Join(evidence, label+".json"))
	command.Env = append(fixture.environment(), "TZ=UTC")
	command.Stdin = bytes.NewReader(oracle)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	defer clear(output.Bytes())
	if err != nil || ctx.Err() != nil {
		test.Fatal("unmodified official Gemini request/transport/role preservation failed; inspect safe private receipt")
	}
}

func TestGeminiOrdinaryCLIActualConsumer(test *testing.T) {
	if os.Getenv("COGNATE_GEMINI_CONSUMER") == "" {
		test.Skip("explicit unmodified complete pinned official consumer required")
	}
	for _, transport := range []agentconfig.GeminiKeyTransport{agentconfig.GeminiNativeDefault, agentconfig.GeminiBearer} {
		test.Run(string(transport), func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			if os.Mkdir(filepath.Join(fixture.home, ".gemini"), 0o700) != nil {
				test.Fatal("existing private Gemini root")
			}
			trust, _ := json.Marshal(map[string]string{fixture.request.Directory: "TRUST_FOLDER"})
			if os.WriteFile(filepath.Join(fixture.home, ".gemini", "trustedFolders.json"), trust, 0o600) != nil {
				test.Fatal("preexisting trusted cwd")
			}
			var operations []string
			for _, variant := range []string{"a", "b"} {
				key := "synthetic-gemini-" + variant + "#=$'\"key"
				model := "gemini-native-" + variant
				selectedTransport := transport
				if variant == "b" {
					selectedTransport = agentconfig.GeminiBearer
					if transport == agentconfig.GeminiBearer {
						selectedTransport = agentconfig.GeminiNativeDefault
					}
				}
				asset, err := fixture.domain.Create(test.Context(), fixture.request.OwnerID, configuration.Draft{Name: "Gemini ordinary " + variant, GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://127.0.0.1:18444"), KeyTransport: new(selectedTransport), EnvModel: new(model), SettingsModel: new("settings-shadow-" + variant)}}, []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(key))}})
				if err != nil {
					test.Fatal("ordinary encrypted Gemini asset")
				}
				arguments := []string{"config", "apply", "--target", "gemini.settings", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--asset", asset.Metadata.ID, "--revision", fmt.Sprint(asset.Metadata.Revision), "--profile", "gemini-profile", "--token-stdin", "--gemini-node", os.Getenv("COGNATE_GEMINI_NODE"), "--gemini-package", os.Getenv("COGNATE_GEMINI_PACKAGE"), "--gemini-cwd", fixture.request.Directory, "--apply"}
				operation, code := geminiRunOrdinaryCLI(test, fixture, arguments, "exact")
				if code != 0 || len(operation) != 32 {
					test.Fatal("ordinary controlling-PTY Gemini apply failed")
				}
				operations = append(operations, operation)
				geminiConsumerRequest(test, fixture, model, key, string(selectedTransport), string(transport)+"-"+variant)
			}
			arguments := []string{"config", "restore", "--target", "gemini.settings", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--profile", "gemini-profile", "--source-operation", operations[1], "--gemini-node", os.Getenv("COGNATE_GEMINI_NODE"), "--gemini-package", os.Getenv("COGNATE_GEMINI_PACKAGE"), "--gemini-cwd", fixture.request.Directory, "--apply"}
			operation, code := geminiRunOrdinaryCLI(test, fixture, arguments, "exact")
			if code != 0 || operation == operations[1] {
				test.Fatal("ordinary new-plan restore failed")
			}
			geminiConsumerRequest(test, fixture, "gemini-native-a", "synthetic-gemini-a#=$'\"key", string(transport), string(transport)+"-restore-a")
			if fixture.authorizes.Load() != 2 || fixture.consumes.Load() != 2 || fixture.statuses.Load() != 0 || fixture.consumed(test) != 2 {
				test.Fatal("ordinary local restore replayed remote material")
			}
		})
	}
}

func TestGeminiOrdinaryCLIModelAbsenceAndNativeLoginBoundary(test *testing.T) {
	if os.Getenv("COGNATE_GEMINI_CONSUMER") == "" {
		test.Skip("explicit complete official consumer inputs required")
	}
	for _, variant := range []string{"settings-only", "env-only", "same-models", "benign-crlf", "foreign-six", "no-models", "native-login"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			if os.Mkdir(filepath.Join(fixture.home, ".gemini"), 0o700) != nil {
				test.Fatal("existing private Gemini root")
			}
			trust, _ := json.Marshal(map[string]string{fixture.request.Directory: "TRUST_FOLDER"})
			if os.WriteFile(filepath.Join(fixture.home, ".gemini", "trustedFolders.json"), trust, 0o600) != nil {
				test.Fatal("existing workspace trust")
			}
			key := "synthetic-gemini-a#=$'\"key"
			model := "gemini-3.8-flash"
			provider := &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://127.0.0.1:18444"), KeyTransport: new(agentconfig.GeminiNativeDefault)}
			switch variant {
			case "settings-only":
				model = "gemini-native-settings"
				provider.SettingsModel = new(model)
			case "env-only":
				model = "gemini-native-env"
				provider.EnvModel = new(model)
			case "same-models", "benign-crlf", "foreign-six":
				model = "gemini-native-same"
				provider.EnvModel, provider.SettingsModel = new(model), new(model)
			case "native-login":
				key = ""
				model = ""
				provider = &configuration.GeminiProvider{AuthMode: agentconfig.GeminiNativeLogin}
			}
			var preserved map[string][]byte
			if variant == "benign-crlf" {
				preserved = map[string][]byte{".env": []byte("# retained CRLF\r\nFOREIGN=value\r\n"), "settings.json": []byte("{\r\n  // retained JSONC\r\n  \"foreign\": {\"value\": 7}\r\n}\r\n")}
			}
			if variant == "native-login" {
				preserved = map[string][]byte{"oauth_creds.json": []byte("synthetic-opaque-OAuth-marker-not-credentials\n")}
			}
			if variant == "foreign-six" {
				for name, body := range map[string][]byte{".env": []byte("# foreign retained\r\nFOREIGN=keep\r\nGEMINI_API_KEY='synthetic-old-foreign'\r\nGOOGLE_GEMINI_BASE_URL='https://old.invalid'\r\nGEMINI_API_KEY_AUTH_MECHANISM='bearer'\r\nGEMINI_MODEL='foreign-env'\r\n"), "settings.json": []byte(`{"foreign":{"value":7},"model":{"name":"foreign-settings"},"security":{"auth":{"selectedType":"gemini-api-key"}}}`)} {
					if os.WriteFile(filepath.Join(fixture.home, ".gemini", name), body, 0o600) != nil {
						test.Fatal("actual six-field foreign fixture")
					}
				}
			}
			for name, body := range preserved {
				if os.WriteFile(filepath.Join(fixture.home, ".gemini", name), body, 0o600) != nil {
					test.Fatal("private benign/native-login marker fixture")
				}
			}
			watch := -1
			defer func() {
				if watch >= 0 {
					_ = unix.Close(watch)
				}
			}()
			if variant == "native-login" {
				var err error
				watch, err = unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
				if err != nil {
					test.Fatal("synthetic OAuth marker watch setup")
				}
				if _, err := unix.InotifyAddWatch(watch, filepath.Join(fixture.home, ".gemini", "oauth_creds.json"), unix.IN_ACCESS|unix.IN_OPEN|unix.IN_MODIFY|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF); err != nil {
					test.Fatal("synthetic OAuth marker watch registration")
				}
			}
			var mutations []configuration.SecretMutation
			if key != "" {
				mutations = []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(key))}}
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.request.OwnerID, configuration.Draft{Name: "Gemini ordinary " + variant, GeminiProvider: provider}, mutations)
			if err != nil {
				test.Fatal("real optional-model/native-login asset")
			}
			arguments := []string{"config", "apply", "--target", "gemini.settings", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--asset", asset.Metadata.ID, "--revision", fmt.Sprint(asset.Metadata.Revision), "--profile", "gemini-profile", "--token-stdin", "--gemini-node", os.Getenv("COGNATE_GEMINI_NODE"), "--gemini-package", os.Getenv("COGNATE_GEMINI_PACKAGE"), "--gemini-cwd", fixture.request.Directory, "--apply"}
			if _, code := geminiRunOrdinaryCLI(test, fixture, arguments, "exact"); code != 0 {
				test.Fatal("ordinary optional model/native-login CLI apply")
			}
			if watch >= 0 {
				var events [4096]byte
				if _, err := unix.Read(watch, events[:]); !errors.Is(err, unix.EAGAIN) {
					test.Fatal("Cognate opened/read/changed synthetic OAuth marker")
				}
				if unix.Close(watch) != nil {
					test.Fatal("synthetic OAuth watch close")
				}
				watch = -1
			}
			geminiConsumerRequest(test, fixture, model, key, string(agentconfig.GeminiNativeDefault), variant)
			if variant == "foreign-six" {
				for name, fragment := range map[string][]byte{".env": []byte("# foreign retained\r\nFOREIGN=keep\r\n"), "settings.json": []byte(`"foreign":{"value":7}`)} {
					body, err := os.ReadFile(filepath.Join(fixture.home, ".gemini", name))
					defer clear(body)
					if err != nil || !bytes.Contains(body, fragment) {
						test.Fatal("six-field takeover lost unrelated bytes")
					}
				}
			}
			for name, body := range preserved {
				actual, err := os.ReadFile(filepath.Join(fixture.home, ".gemini", name))
				if err != nil || !bytes.Contains(actual, body) && (name != "settings.json" || !bytes.Contains(actual, []byte("\"foreign\": {\"value\": 7}")) || !bytes.Contains(actual, []byte("// retained JSONC\r\n"))) {
					test.Fatal("consumer changed benign original bytes or OAuth marker")
				}
			}
			consumed := int32(1)
			if variant == "native-login" {
				consumed = 0
			}
			if fixture.authorizes.Load() != 1 || fixture.consumes.Load() != consumed || fixture.statuses.Load() != 0 {
				test.Fatal("optional model/no-secret path changed remote lifecycle")
			}
		})
	}
}

func TestGeminiOrdinaryCLIDenialsDrainWithoutAuthority(test *testing.T) {
	if os.Getenv("COGNATE_GEMINI_CONSUMER") == "" {
		test.Skip("explicit isolated ordinary CLI fixture required")
	}
	for _, answer := range []string{"empty", "empty-slot", "empty-final", "foreign-plan", "interrupt", "", "closed-output"} {
		label := answer
		if label == "" {
			label = "non-tty"
		}
		test.Run(label, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			if os.Mkdir(filepath.Join(fixture.home, ".gemini"), 0o700) != nil {
				test.Fatal("private preexisting Gemini root")
			}
			trust, _ := json.Marshal(map[string]string{fixture.request.Directory: "TRUST_FOLDER"})
			if os.WriteFile(filepath.Join(fixture.home, ".gemini", "trustedFolders.json"), trust, 0o600) != nil {
				test.Fatal("private trusted cwd")
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.request.OwnerID, configuration.Draft{Name: "Gemini denied", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://127.0.0.1:18444"), KeyTransport: new(agentconfig.GeminiNativeDefault)}}, []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-gemini-a#=$'\"key"))}})
			if err != nil {
				test.Fatal("real denied-case encrypted Gemini asset")
			}
			arguments := []string{"config", "apply", "--target", "gemini.settings", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--asset", asset.Metadata.ID, "--revision", fmt.Sprint(asset.Metadata.Revision), "--profile", "gemini-profile", "--token-stdin", "--gemini-node", os.Getenv("COGNATE_GEMINI_NODE"), "--gemini-package", os.Getenv("COGNATE_GEMINI_PACKAGE"), "--gemini-cwd", fixture.request.Directory, "--apply"}
			if _, code := geminiRunOrdinaryCLI(test, fixture, arguments, answer); code == 0 {
				test.Fatal("unapproved ordinary CLI accepted")
			}
			if fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.statuses.Load() != 0 || fixture.consumed(test) != 0 {
				test.Fatal("denied CLI acquired authority or replayed")
			}
			for _, name := range []string{".env", "settings.json"} {
				if _, err := os.Lstat(filepath.Join(fixture.home, ".gemini", name)); !os.IsNotExist(err) {
					test.Fatal("denied CLI changed target")
				}
			}
		})
	}
}

func TestGeminiOrdinaryCLIUnknownExplicitStatusNoReplay(test *testing.T) {
	if os.Getenv("COGNATE_GEMINI_CONSUMER") == "" {
		test.Skip("explicit isolated ordinary CLI fixture required")
	}
	for _, phase := range []string{"lost-authorize", "lost-consume"} {
		test.Run(phase, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, phase)
			if os.Mkdir(filepath.Join(fixture.home, ".gemini"), 0o700) != nil {
				test.Fatal("private preexisting Gemini root")
			}
			trust, _ := json.Marshal(map[string]string{fixture.request.Directory: "TRUST_FOLDER"})
			if os.WriteFile(filepath.Join(fixture.home, ".gemini", "trustedFolders.json"), trust, 0o600) != nil {
				test.Fatal("private existing trust")
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.request.OwnerID, configuration.Draft{Name: "Gemini Unknown", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://127.0.0.1:18444"), KeyTransport: new(agentconfig.GeminiNativeDefault)}}, []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-gemini-a#=$'\"key"))}})
			if err != nil {
				test.Fatal("actual Unknown encrypted asset")
			}
			arguments := []string{"config", "apply", "--target", "gemini.settings", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--asset", asset.Metadata.ID, "--revision", fmt.Sprint(asset.Metadata.Revision), "--profile", "gemini-profile", "--token-stdin", "--gemini-node", os.Getenv("COGNATE_GEMINI_NODE"), "--gemini-package", os.Getenv("COGNATE_GEMINI_PACKAGE"), "--gemini-cwd", fixture.request.Directory, "--apply"}
			operation, code := geminiRunOrdinaryCLI(test, fixture, arguments, "exact")
			if code == 0 || len(operation) != 32 {
				test.Fatal("actual Unknown missing operation or false success")
			}
			select {
			case <-fixture.drained:
			case <-time.After(time.Second):
				test.Fatal("committed HTTP receipt-loss handler not joined")
			}
			consumed := int32(1)
			if phase == "lost-authorize" {
				consumed = 0
			}
			if fixture.authorizes.Load() != 1 || fixture.consumes.Load() != consumed || fixture.statuses.Load() != 0 || fixture.consumed(test) != int(consumed) {
				test.Fatal("Unknown automatically replayed or checked status")
			}
			before := fixture.requests.Load()
			if _, code := geminiRunOrdinaryCLI(test, fixture, arguments, "early-reject"); code == 0 || fixture.requests.Load() != before {
				test.Fatal("pending Unknown allocated new remote authority")
			}
			status := []string{"config", "status", "--target", "gemini.settings", "--server", fixture.origin, "--owner", fixture.request.OwnerID, "--profile", "gemini-profile", "--operation", operation}
			if _, code := geminiRunOrdinaryCLI(test, fixture, status, ""); code == 0 || fixture.requests.Load() != before {
				test.Fatal("readonly local status replayed or claimed Applied")
			}
			if _, code := geminiRunOrdinaryCLI(test, fixture, append(status, "--check-remote"), ""); code == 0 || fixture.statuses.Load() != 1 || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != consumed {
				test.Fatal("explicit restarted Status replayed or falsely applied")
			}
			for _, name := range []string{".env", "settings.json"} {
				if _, err := os.Lstat(filepath.Join(fixture.home, ".gemini", name)); !os.IsNotExist(err) {
					test.Fatal("Unknown or Status published target")
				}
			}
		})
	}
}
