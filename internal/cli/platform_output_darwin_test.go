//go:build darwin

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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/configapply"
)

type darwinPTYCapture struct {
	mutex sync.Mutex
	data  []byte
	child atomic.Int64
}

type darwinPTYDiagnostic struct {
	Schema          int    `json:"schema"`
	TestName        string `json:"test_name"`
	ScriptTerminal  string `json:"script_terminal"`
	ScriptExitKnown bool   `json:"script_exit_known"`
	ScriptExit      int    `json:"script_exit"`
	ScriptSignal    string `json:"script_signal"`
	Capture         string `json:"capture"`
	ChildMarker     string `json:"child_marker"`
	TargetMarker    string `json:"target_marker"`
	EOTPrefix       string `json:"eot_prefix"`
}

func darwinPTYDetails(state *os.ProcessState, runError error, output string) darwinPTYDiagnostic {
	details := darwinPTYDiagnostic{Schema: 1, TestName: "TestDarwinPTYExecutionPrerequisites", ScriptTerminal: "unknown", ScriptSignal: "unknown", Capture: "empty", ChildMarker: "absent", TargetMarker: "absent", EOTPrefix: "absent"}
	if state != nil {
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			details.ScriptTerminal = "signaled"
			switch status.Signal() {
			case syscall.SIGHUP:
				details.ScriptSignal = "hup"
			case syscall.SIGINT:
				details.ScriptSignal = "int"
			case syscall.SIGQUIT:
				details.ScriptSignal = "quit"
			case syscall.SIGKILL:
				details.ScriptSignal = "kill"
			case syscall.SIGTERM:
				details.ScriptSignal = "term"
			default:
				details.ScriptSignal = "other"
			}
		} else if code := state.ExitCode(); code >= 0 && code <= 255 {
			details.ScriptTerminal = "exited"
			details.ScriptExitKnown = true
			details.ScriptExit = code
		}
	} else if runError != nil {
		details.ScriptTerminal = "launch-failed"
	}
	if output != "" {
		details.Capture = "nonempty"
	}
	if strings.HasPrefix(output, "^D") {
		details.EOTPrefix = "fixed-caret-D"
	} else if strings.HasPrefix(output, "\x04") {
		details.EOTPrefix = "unknown"
	}
	if strings.Contains(output, "COGNATE_PTY_CHILD:") {
		details.ChildMarker = "nonanchored-only"
	}
	if strings.Contains(output, "COGNATE_TARGET_EXIT:") {
		details.TargetMarker = "nonanchored-only"
	}
	targets := 0
	for line := range strings.SplitSeq(output, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "COGNATE_PTY_CHILD:"); found {
			if details.ChildMarker != "anchored-valid" {
				details.ChildMarker = "anchored-invalid"
			}
			if child, err := strconv.ParseInt(value, 10, 32); err == nil && child > 1 {
				details.ChildMarker = "anchored-valid"
			}
		}
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "COGNATE_TARGET_EXIT:"); found {
			targets++
			details.TargetMarker = "anchored-invalid"
			if code, err := strconv.Atoi(value); err == nil {
				details.TargetMarker = "anchored-nonnegative"
				if code < 0 {
					details.TargetMarker = "anchored-negative"
				}
			}
		}
	}
	if targets > 1 {
		details.TargetMarker = "duplicate"
	}
	return details
}

func darwinPTYLogDiagnostic(test *testing.T, command *exec.Cmd, runError error, capture *darwinPTYCapture) {
	if test.Name() != "TestDarwinPTYExecutionPrerequisites" {
		return
	}
	capture.mutex.Lock()
	details := darwinPTYDetails(command.ProcessState, runError, string(capture.data))
	capture.mutex.Unlock()
	encoded, _ := json.Marshal(details)
	test.Logf("DARWIN_PTY_DIAGNOSTIC %s", encoded)
}

func (capture *darwinPTYCapture) Write(data []byte) (int, error) {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	if len(capture.data)+len(data) > 32768 {
		return 0, errors.New("bounded synthetic PTY output exceeded")
	}
	capture.data = append(capture.data, data...)
	for _, line := range strings.Split(string(capture.data), "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "COGNATE_PTY_CHILD:"); found {
			if pid, err := strconv.ParseInt(value, 10, 32); err == nil && pid > 1 {
				capture.child.Store(pid)
			}
		}
	}
	return len(data), nil
}

func darwinPTYRun(test *testing.T, environment []string, arguments ...string) (string, int) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	defer cancel()
	capture := new(darwinPTYCapture)
	shell := "printf '\nCOGNATE_PTY_CHILD:%s\n' \"$$\"; \"$@\"; result=$?; printf 'COGNATE_TARGET_EXIT:%s\n' \"$result\"; exit \"$result\""
	command := exec.CommandContext(ctx, "/usr/bin/script", append([]string{"-q", "/dev/null", "/bin/sh", "-c", shell, "cognate-fixture"}, arguments...)...)
	command.Env = environment
	command.Stdin = strings.NewReader("")
	command.Stdout = capture
	command.Stderr = capture
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		if pid := capture.child.Load(); pid > 1 {
			_ = syscall.Kill(-int(pid), syscall.SIGKILL)
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		return command.Process.Kill()
	}
	err := command.Run()
	darwinPTYLogDiagnostic(test, command, err, capture)
	if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
		test.Fatal("DARWIN_INFRA PTY child deadline or incomplete drain")
	}
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			test.Fatal("DARWIN_INFRA PTY process unavailable")
		}
	}
	capture.mutex.Lock()
	output := string(capture.data)
	capture.mutex.Unlock()
	targetExit := -1
	for _, line := range strings.Split(output, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "COGNATE_TARGET_EXIT:"); found {
			if targetExit != -1 {
				test.Fatal("DARWIN_INFRA duplicate PTY exit receipt")
			}
			targetExit, err = strconv.Atoi(value)
			if err != nil {
				test.Fatal("DARWIN_INFRA invalid PTY exit receipt")
			}
		}
	}
	if capture.child.Load() < 2 || targetExit < 0 {
		test.Fatal("DARWIN_INFRA missing actual PTY child/exit receipt")
	}
	return output, targetExit
}

func darwinPTYEnvironment(test *testing.T) ([]string, string) {
	test.Helper()
	home, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(home, 0700) != nil {
		test.Fatal("DARWIN_INFRA canonical private PTY home")
	}
	for _, relative := range []string{"config", "data", "cache", "tmp"} {
		if os.Mkdir(filepath.Join(home, relative), 0700) != nil {
			test.Fatal("DARWIN_INFRA isolated PTY environment")
		}
	}
	return []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "SHELL=/bin/sh", "TERM=dumb", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "TMPDIR=" + filepath.Join(home, "tmp")}, home
}

func TestDarwinPTYExecutionPrerequisites(test *testing.T) {
	environment, _ := darwinPTYEnvironment(test)
	output, code := darwinPTYRun(test, environment, "/bin/sh", "-c", "test -t 0 && test -t 1 && : </dev/tty && : >/dev/tty && printf 'COGNATE_PTY_CAPABLE\n'")
	if code != 0 || !strings.Contains(output, "COGNATE_PTY_CAPABLE") {
		test.Fatal("DARWIN_INFRA actual controlling PTY unavailable")
	}
	test.Log("DARWIN_CAPABILITY actual_control_tty=true child_exit=0 script_waited=true bounded_capture=true")
}

func TestDarwinExecutionCLIRealPTY(test *testing.T) {
	if os.Getenv("COGNATE_DARWIN_SINK_PROBE") == "1" {
		output, err := prepareClientKeyOutput(test.Context(), os.Stdout)
		if errors.Is(err, configapply.ErrUnsupportedPlatform) {
			fmt.Println("COGNATE_SINK_UNSUPPORTED")
			return
		}
		if err != nil || output == nil {
			test.Fatal("DARWIN_INFRA unexpected native sink classification")
		}
		if err := output.Close(); err != nil {
			test.Fatal("DARWIN_INFRA sink close failed")
		}
		fmt.Println("COGNATE_SINK_SUPPORTED")
		return
	}
	binary := os.Getenv("COGNATE_DARWIN_BINARY")
	if !filepath.IsAbs(binary) {
		test.Fatal("DARWIN_INFRA missing real cognate binary")
	}
	environment, home := darwinPTYEnvironment(test)
	probe, code := darwinPTYRun(test, append(append([]string{}, environment...), "COGNATE_DARWIN_SINK_PROBE=1"), os.Args[0], "-test.run=^TestDarwinExecutionCLIRealPTY$", "-test.count=1")
	if code != 0 {
		test.Fatal("DARWIN_INFRA native sink probe failed")
	}
	output, code := darwinPTYRun(test, environment, binary, "client-key", "init")
	keyPath := filepath.Join(home, ".config", "skillhub", "client.key")
	if code != 0 {
		if strings.Contains(probe, "COGNATE_SINK_UNSUPPORTED") && code == 1 {
			for _, relative := range []string{".config/skillhub/client.key", ".config/skillhub/client-key-use.v1", ".local/share/skillhub/client.db", ".agents"} {
				if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("failed PTY init left business/key state")
				}
			}
			test.Log("DARWIN_BASELINE_REJECTION code=pty-sink-unsupported child_exit=1 business_state=false")
		}
		test.Fatal("Darwin actual terminal client-key init must succeed")
	}
	if !strings.Contains(output, "本机客户端密钥已就绪") {
		test.Fatal("actual terminal initialization has no fixed completion")
	}
	first, err := os.ReadFile(keyPath)
	defer clear(first)
	if err != nil || len(first) != 32 {
		test.Fatal("actual terminal did not initialize key")
	}
	_, code = darwinPTYRun(test, environment, binary, "client-key", "init")
	second, err := os.ReadFile(keyPath)
	defer clear(second)
	if code != 0 || err != nil || !bytes.Equal(first, second) {
		test.Fatal("actual terminal reopen changed key")
	}
}
