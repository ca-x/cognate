package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"golang.org/x/sys/unix"
)

func TestNativeConfigurationInputCancellation(test *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	before := nativeTestFlags(test, reader)
	ctx, cancel := context.WithTimeout(test.Context(), 40*time.Millisecond)
	defer cancel()
	stopRescue, rescued := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(rescued)
		timer := time.NewTimer(600 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			_ = writer.Close()
		case <-stopRescue:
		}
	}()
	started := time.Now()
	_, readErr := readNativeSession(ctx, reader)
	close(stopRescue)
	<-rescued
	if readErr == nil {
		test.Fatal("blocked credential input accepted")
	}
	if time.Since(started) > 300*time.Millisecond || before != nativeTestFlags(test, reader) {
		test.Fatal("borrowed input flags changed or cancellation not bounded")
	}
}

func TestNativeConfigurationRealTTYApproval(test *testing.T) {
	binary := buildNativeCLI(test)
	for _, mode := range []configuration.AuthMode{configuration.NativeLogin, configuration.APIKey, configuration.AuthToken} {
		for _, approved := range []bool{false, true} {
			test.Run(fmt.Sprintf("%s-approved-%t", mode, approved), func(test *testing.T) {
				fixture := nativeCLIBackend(test, mode)
				master, slave := nativeTestPTY(test)
				ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, binary, append(fixture.arguments(), "--apply")...)
				command.Env = fixture.environment()
				command.Stdin = strings.NewReader(fixture.bearer)
				command.ExtraFiles = []*os.File{slave}
				command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
				var output bytes.Buffer
				command.Stdout, command.Stderr = &output, &output
				if err := command.Start(); err != nil {
					test.Fatal(err)
				}
				defer func() {
					if command.ProcessState == nil {
						_ = command.Process.Kill()
						_ = command.Wait()
					}
				}()
				var pending string
				finished := false
				for !finished {
					if err := master.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
						test.Fatal(err)
					}
					var buffer [512]byte
					count, err := master.Read(buffer[:])
					if err != nil {
						test.Fatalf("missing real confirmation prompt: %v", err)
					}
					pending += string(buffer[:count])
					for strings.Contains(pending, "\n") {
						line, rest, _ := strings.Cut(pending, "\n")
						pending = rest
						_, expected, ok := strings.Cut(line, "确认请输入 ")
						if !ok {
							continue
						}
						expected = strings.TrimSuffix(expected, "\r")
						answer := expected
						if !approved {
							answer = "no"
						}
						if _, err := master.Write([]byte(answer + "\n")); err != nil {
							test.Fatal(err)
						}
						if !approved || strings.HasPrefix(expected, "confirm ") {
							finished = true
							break
						}
					}
				}
				err := command.Wait()
				if (err == nil) != approved {
					test.Fatalf("real TTY outcome mismatch: %v; %s", err, output.String())
				}
				expectedAuthorizations, expectedConsumes := int32(0), int32(0)
				if approved {
					expectedAuthorizations = 1
					if mode != configuration.NativeLogin {
						expectedConsumes = 1
					}
				}
				if fixture.authorize.Load() != expectedAuthorizations || fixture.consume.Load() != expectedConsumes {
					test.Fatal("TTY did not bind exact approval/slot")
				}
				data, readErr := os.ReadFile(filepath.Join(fixture.home, ".claude", "settings.json"))
				if approved && readErr != nil || !approved && !os.IsNotExist(readErr) {
					test.Fatal("actual target write differs from approval")
				}
				if approved && mode != configuration.NativeLogin && !bytes.Contains(data, []byte("synthetic-C1-cli-secret")) {
					test.Fatal("actual secret not rendered")
				}
				if strings.Contains(output.String(), fixture.bearer) || strings.Contains(output.String(), "synthetic-C1-cli-secret") {
					test.Fatal("secret reached terminal output")
				}
			})
		}
	}
}

func TestNativeConfigurationCLIRestoreRestart(test *testing.T) {
	binary := buildNativeCLI(test)
	fixture := nativeCLIBackend(test, configuration.AuthToken)
	output, operation := nativeRunApprovingTTY(test, binary, fixture, append(fixture.arguments(), "--apply"), true)
	if operation == "" || !strings.Contains(output, "AppliedOnDisk") {
		test.Fatal("actual CLI apply did not yield durable operation")
	}
	beforeAuthorize, beforeConsume := fixture.authorize.Load(), fixture.consume.Load()
	run := func(arguments ...string) string {
		ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Env = fixture.environment()
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		if err := command.Run(); err != nil {
			test.Fatalf("offline restarted command failed: %v; %s", err, output.String())
		}
		return output.String()
	}
	if !strings.Contains(run("config", "status", "--operation", operation), "AppliedOnDisk") {
		test.Fatal("restarted status lost durable state")
	}
	if !strings.Contains(run("config", "restore", "--source-operation", operation), "restore") {
		test.Fatal("restore default did not preview")
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".claude", "settings.json")); err != nil {
		test.Fatal("restore preview changed target")
	}
	output, _ = nativeRunApprovingTTY(test, binary, fixture, []string{"config", "restore", "--source-operation", operation, "--apply"}, true)
	if !strings.Contains(output, "Restored") {
		test.Fatal("actual CLI restore missing confirmed result")
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".claude", "settings.json")); !os.IsNotExist(err) {
		test.Fatal("offline CLI restore failed to restore absence")
	}
	if fixture.authorize.Load() != beforeAuthorize || fixture.consume.Load() != beforeConsume {
		test.Fatal("offline restore/status used remote authorization")
	}
}

func TestNativeConfigurationSlotApprovalNeedsFinalConfirmation(test *testing.T) {
	binary := buildNativeCLI(test)
	fixture := nativeCLIBackend(test, configuration.APIKey)
	nativeRunApprovingTTY(test, binary, fixture, append(fixture.arguments(), "--apply"), false)
	if fixture.authorize.Load() != 0 || fixture.consume.Load() != 0 {
		test.Fatal("slot approval replaced final plan consent")
	}
	command := exec.CommandContext(test.Context(), binary, append(fixture.arguments(), "--apply")...)
	command.Env = fixture.environment()
	command.Stdin = strings.NewReader(fixture.bearer)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if command.Run() == nil || fixture.authorize.Load() != 0 || fixture.consume.Load() != 0 {
		test.Fatal("missing controlling TTY was silently approved")
	}
}

func nativeRunApprovingTTY(test *testing.T, binary string, fixture *nativeCLIFixture, arguments []string, approveFinal bool) (string, string) {
	test.Helper()
	master, slave := nativeTestPTY(test)
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = fixture.environment()
	command.Stdin = strings.NewReader(fixture.bearer)
	command.ExtraFiles = []*os.File{slave}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	pending, operation := "", ""
	for operation == "" {
		if err := master.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
			test.Fatal(err)
		}
		var buffer [512]byte
		count, err := master.Read(buffer[:])
		if err != nil {
			test.Fatal("actual CLI confirmation was not reachable")
		}
		pending += string(buffer[:count])
		for strings.Contains(pending, "\n") {
			line, rest, _ := strings.Cut(pending, "\n")
			pending = rest
			_, expected, ok := strings.Cut(line, "确认请输入 ")
			if !ok {
				continue
			}
			expected = strings.TrimSuffix(expected, "\r")
			answer := expected
			if strings.HasPrefix(expected, "confirm ") && !approveFinal {
				answer = "no"
			}
			if _, err := master.Write([]byte(answer + "\n")); err != nil {
				test.Fatal(err)
			}
			if suffix, ok := strings.CutPrefix(expected, "confirm "); ok {
				operation = suffix
				break
			}
		}
	}
	if err := command.Wait(); (err == nil) != approveFinal {
		test.Fatalf("confirmed main failed: %v; %s", err, output.String())
	}
	if strings.Contains(output.String(), fixture.bearer) || strings.Contains(output.String(), "synthetic-C1-cli-secret") {
		test.Fatal("sensitive output from main")
	}
	return output.String(), operation
}

func nativeTestPTY(test *testing.T) (*os.File, *os.File) {
	test.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		test.Fatal(err)
	}
	connection, err := master.SyscallConn()
	if err != nil {
		test.Fatal(err)
	}
	var number int
	var ioctlErr error
	if err := connection.Control(func(descriptor uintptr) {
		ioctlErr = unix.IoctlSetPointerInt(int(descriptor), unix.TIOCSPTLCK, 0)
		if ioctlErr == nil {
			number, ioctlErr = unix.IoctlGetInt(int(descriptor), unix.TIOCGPTN)
		}
	}); err != nil || ioctlErr != nil {
		_ = master.Close()
		test.Fatal("PTY unlock failed")
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	return master, slave
}

func TestNativeConfigurationParentExit(test *testing.T) {
	binary := buildNativeCLI(test)
	for _, arguments := range [][]string{{"config", "not-a-command", "synthetic-private-parent"}, {"config", "synthetic-private-parent"}} {
		for _, sink := range []string{"writable", "full", "closed"} {
			test.Run(arguments[1]+"/"+sink, func(test *testing.T) {
				fixture := nativeCLIBackend(test, configuration.APIKey)
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal(err)
				}
				defer reader.Close()
				defer writer.Close()
				_ = writer.Fd()
				before := nativeTestFlags(test, writer)
				if sink == "full" {
					nativeFillOutputPipe(test, writer)
				}
				ctx, cancel := context.WithTimeout(test.Context(), 6500*time.Millisecond)
				defer cancel()
				command := exec.CommandContext(ctx, binary, arguments...)
				command.Env = fixture.environment()
				var output, diagnostic bytes.Buffer
				command.Stdout, command.Stderr = &output, &diagnostic
				if sink == "full" {
					command.Stderr = writer
				}
				if sink == "closed" {
					command = exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestNativeConfigurationExecuteHelper$", "--"}, arguments...)...)
					command.Env = append(fixture.environment(), "COGNATE_NATIVE_EXIT_HELPER=closed-stderr")
					command.Stdout, command.Stderr = &output, &diagnostic
				}
				err = command.Run()
				if err == nil || ctx.Err() != nil || command.ProcessState.ExitCode() != 1 {
					test.Fatal("invalid parent invocation did not independently exit1 within bound")
				}
				if sink == "writable" && diagnostic.String() != nativeErrorMessage(nativeconfig.ErrInvalid)+"\n" {
					test.Fatal("fixed diagnostic missing")
				}
				if output.Len() != 0 || strings.Contains(diagnostic.String(), "synthetic-private-parent") || before != nativeTestFlags(test, writer) {
					test.Fatal("parent invocation leaked input, printed help or mutated caller flags")
				}
				if fixture.requests.Load() != 0 || fixture.authorize.Load() != 0 || fixture.consume.Load() != 0 {
					test.Fatal("invalid parent invocation reached authority")
				}
				if _, err := os.Stat(filepath.Join(fixture.home, ".claude", "settings.json")); !os.IsNotExist(err) {
					test.Fatal("invalid parent invocation wrote target")
				}
			})
		}
	}
	for _, arguments := range [][]string{{"config"}, {"config", "--help"}} {
		ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Env = append(os.Environ(), "HOME="+test.TempDir())
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		err := command.Run()
		cancel()
		if err != nil || !strings.Contains(output.String(), "apply") {
			test.Fatal("intentional parent help failed")
		}
	}
}

func nativeFillOutputPipe(test *testing.T, writer *os.File) {
	test.Helper()
	connection, err := writer.SyscallConn()
	if err != nil {
		test.Fatal(err)
	}
	var capacity int
	var capacityErr error
	if err := connection.Control(func(descriptor uintptr) {
		capacity, capacityErr = unix.FcntlInt(descriptor, unix.F_GETPIPE_SZ, 0)
	}); err != nil || capacityErr != nil || capacity <= 0 {
		test.Fatal("pipe capacity unavailable")
	}
	if _, err := writer.Write(bytes.Repeat([]byte{'x'}, capacity)); err != nil {
		test.Fatal(err)
	}
}

func TestNativeConfigurationNestedOutputDeadline(test *testing.T) {
	binary := buildNativeCLI(test)
	fixture := nativeCLIBackend(test, configuration.APIKey)
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	nativeFillOutputPipe(test, writer)
	_ = writer.Fd()
	before := nativeTestFlags(test, writer)
	ctx, cancel := context.WithTimeout(test.Context(), 13*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append(fixture.arguments(), "--timeout", "1s")...)
	command.Env = fixture.environment()
	command.Stdin = strings.NewReader(fixture.bearer)
	command.Stdout, command.Stderr = writer, writer
	started := time.Now()
	err = command.Run()
	elapsed := time.Since(started)
	test.Logf("actual main self-exit=%t elapsed=%s", ctx.Err() == nil, elapsed)
	if err == nil || ctx.Err() != nil || command.ProcessState.ExitCode() != 1 || elapsed > 6500*time.Millisecond {
		test.Fatal("nested output ignored command deadline plus final diagnostic bound")
	}
	if fixture.requests.Load() == 0 || fixture.authorize.Load() != 0 || fixture.consume.Load() != 0 || nativeTestFlags(test, writer) != before {
		test.Fatal("output failure changed authority or borrowed flags")
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".claude", "settings.json")); !os.IsNotExist(err) {
		test.Fatal("output failure wrote target")
	}
}

func TestNativeConfigurationNestedOutputCancellation(test *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		test.Run(fmt.Sprintf("parent-%t", cancelParent), func(test *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			nativeFillOutputPipe(test, writer)
			before := nativeTestFlags(test, writer)
			parent := test.Context()
			if cancelParent {
				owner, cancel := context.WithTimeout(parent, 40*time.Millisecond)
				defer cancel()
				parent = owner
			}
			output, err := prepareNativeOutput(parent, writer)
			if err != nil {
				test.Fatal(err)
			}
			defer output.Close()
			ctx, cancel := context.WithTimeout(parent, 40*time.Millisecond)
			defer cancel()
			nested, err := prepareNativeOutput(ctx, output)
			if err != nil {
				test.Fatal(err)
			}
			started := time.Now()
			if _, err := nested.Write([]byte("bounded")); err == nil {
				test.Fatal("full nested sink accepted output")
			}
			if time.Since(started) > 300*time.Millisecond || nativeTestFlags(test, writer) != before {
				test.Fatal("nested cancellation not bounded or borrowed flags changed")
			}
			if err := nested.Close(); err != nil {
				test.Fatal(err)
			}
			if _, err := reader.Stat(); err != nil {
				test.Fatal("nested Close closed borrowed pipe")
			}
		})
	}
}

func TestNativeConfigurationExecuteExit(test *testing.T) {
	binary := buildNativeCLI(test)
	for _, failure := range []string{"flag", "leading-flag", "args", "timeout", "cancel", "closed", "writable"} {
		test.Run(failure, func(test *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			_ = writer.Fd()
			before := nativeTestFlags(test, writer)
			if failure != "closed" && failure != "writable" {
				connection, _ := writer.SyscallConn()
				var capacity int
				_ = connection.Control(func(descriptor uintptr) { capacity, err = unix.FcntlInt(descriptor, unix.F_GETPIPE_SZ, 0) })
				if capacity <= 0 || err != nil {
					test.Fatal("pipe capacity unavailable")
				}
				if _, err := writer.Write(bytes.Repeat([]byte{'x'}, capacity)); err != nil {
					test.Fatal(err)
				}
			}
			fixture := nativeCLIBackend(test, configuration.APIKey)
			arguments := []string{"config", "apply", "--unknown=synthetic-arg-private"}
			if failure == "leading-flag" {
				arguments = []string{"--unknown=synthetic-arg-private", "config", "apply"}
			}
			if failure == "args" {
				arguments = []string{"config", "status", "extra"}
			}
			input, inputWriter, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer input.Close()
			defer inputWriter.Close()
			if failure == "cancel" || failure == "timeout" {
				arguments = append(fixture.arguments(), "--timeout", "100ms")
			}
			if failure == "cancel" {
				arguments = append(fixture.arguments(), "--timeout", "3s")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 9*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, arguments...)
			command.Env = fixture.environment()
			var output, diagnostic bytes.Buffer
			command.Stdout, command.Stderr, command.Stdin = &output, writer, input
			if failure == "writable" {
				command.Stderr = &diagnostic
			}
			if failure == "closed" {
				command = exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestNativeConfigurationExecuteHelper$", "--"}, arguments...)...)
				command.Env = append(fixture.environment(), "COGNATE_NATIVE_EXIT_HELPER=closed-stderr")
				command.Stdout, command.Stderr = &output, &diagnostic
			}
			started := time.Now()
			if err := command.Start(); err != nil {
				test.Fatal(err)
			}
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			if failure == "cancel" {
				nativeWaitForCredentialRead(test, command, input)
				if err := command.Process.Signal(os.Interrupt); err != nil {
					test.Fatal(err)
				}
			}
			err = command.Wait()
			if err == nil || command.ProcessState.ExitCode() != 1 || ctx.Err() != nil || time.Since(started) > 7*time.Second {
				test.Fatal("actual Execute did not independently exit1 within bounded cleanup")
			}
			if before != nativeTestFlags(test, writer) {
				test.Fatal("borrowed stderr flags changed")
			}
			if fixture.authorize.Load() != 0 || fixture.consume.Load() != 0 {
				test.Fatal("preflight failure consumed material")
			}
			if failure == "writable" && diagnostic.Len() == 0 {
				test.Fatal("writable stderr missing fixed diagnostic")
			}
			if strings.Contains(diagnostic.String(), "synthetic-arg-private") {
				test.Fatal("preflight diagnostic echoed raw argument")
			}
		})
	}
}

func nativeWaitForCredentialRead(test *testing.T, command *exec.Cmd, input *os.File) {
	test.Helper()
	connection, err := input.SyscallConn()
	if err != nil {
		test.Fatal(err)
	}
	var identity string
	var inspectErr error
	if err := connection.Control(func(descriptor uintptr) {
		identity, inspectErr = os.Readlink(fmt.Sprintf("/proc/self/fd/%d", descriptor))
	}); err != nil || inspectErr != nil {
		test.Fatal("credential pipe identity unavailable")
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", command.Process.Pid))
		if err != nil {
			test.Fatal("child exited before credential wait")
		}
		matched := 0
		for _, entry := range entries {
			link, _ := os.Readlink(filepath.Join(fmt.Sprintf("/proc/%d/fd", command.Process.Pid), entry.Name()))
			if link == identity {
				matched++
			}
		}
		if matched >= 2 {
			return
		}
		select {
		case <-deadline.C:
			test.Fatal("child never opened its private credential pipe")
		case <-poll.C:
		case <-test.Context().Done():
			test.Fatal("fixture canceled before credential wait")
		}
	}
}

func TestNativeConfigurationOutputCancellation(test *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	before := nativeTestFlags(test, writer)
	connection, err := writer.SyscallConn()
	if err != nil {
		test.Fatal(err)
	}
	var capacity int
	if err := connection.Control(func(descriptor uintptr) { capacity, err = unix.FcntlInt(descriptor, unix.F_GETPIPE_SZ, 0) }); err != nil || capacity == 0 {
		test.Fatal("pipe capacity unavailable")
	}
	if _, err := writer.Write([]byte(strings.Repeat("x", capacity))); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 40*time.Millisecond)
	defer cancel()
	output, err := prepareNativeOutput(ctx, writer)
	if err != nil {
		test.Fatal(err)
	}
	defer output.Close()
	started := time.Now()
	if _, err := output.Write([]byte("bounded")); err == nil {
		test.Fatal("full sink accepted output")
	}
	if time.Since(started) > time.Second || before != nativeTestFlags(test, writer) {
		test.Fatal("output did not cancel or changed borrowed flags")
	}
}

func nativeTestFlags(test *testing.T, file *os.File) int {
	test.Helper()
	connection, err := file.SyscallConn()
	if err != nil {
		test.Fatal(err)
	}
	var flags int
	if err := connection.Control(func(descriptor uintptr) { flags, err = unix.FcntlInt(descriptor, unix.F_GETFL, 0) }); err != nil {
		test.Fatal(err)
	}
	return flags
}
