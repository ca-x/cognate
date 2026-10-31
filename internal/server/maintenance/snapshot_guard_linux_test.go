//go:build linux

package maintenance

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSnapshotGuardProcesses(test *testing.T) {
	mode := os.Getenv("COGNATE_SNAPSHOT_GUARD_TEST")
	if mode == "hold" {
		input := os.NewFile(3, "guard control")
		output := os.NewFile(4, "guard ready")
		if _, err := output.Write([]byte("ready\n")); err != nil {
			os.Exit(2)
		}
		var end [1]byte
		_, _ = input.Read(end[:])
		os.Exit(0)
	}
	if mode == "parent" {
		directory := os.Getenv("COGNATE_SNAPSHOT_GUARD_ROOT")
		instance, err := Join(test.Context(), directory)
		if err != nil {
			os.Exit(2)
		}
		token, err := instance.Quiesce(test.Context())
		if err != nil {
			os.Exit(2)
		}
		guard, err := instance.AcquireSnapshotGuard(test.Context(), token)
		if err != nil {
			os.Exit(2)
		}
		control := os.NewFile(3, "outer control")
		ready := os.NewFile(4, "outer ready")
		child := exec.Command(os.Args[0], "-test.run=^TestSnapshotGuardProcesses$")
		child.Env = append(os.Environ(), "COGNATE_SNAPSHOT_GUARD_TEST=hold")
		child.ExtraFiles = []*os.File{control, ready, guard}
		if child.Start() != nil {
			os.Exit(2)
		}
		if _, err := fmt.Fprintf(ready, "parent-ready=%d\n", child.Process.Pid); err != nil {
			os.Exit(2)
		}
		select {}
	}
	if mode == "join" {
		ctx, cancel := context.WithTimeout(test.Context(), time.Second)
		defer cancel()
		instance, err := Join(ctx, os.Getenv("COGNATE_SNAPSHOT_GUARD_ROOT"))
		if err == nil {
			if instance.Close() != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
		if errors.Is(err, ErrLock) {
			os.Exit(7)
		}
		os.Exit(2)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		test.Fatal("private process-reaper setup refused")
	}
	defer unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer controlReader.Close()
	defer controlWriter.Close()
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer readyReader.Close()
	defer readyWriter.Close()
	parent := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestSnapshotGuardProcesses$")
	parent.Env = append(os.Environ(), "COGNATE_SNAPSHOT_GUARD_TEST=parent", "COGNATE_SNAPSHOT_GUARD_ROOT="+directory)
	parent.ExtraFiles = []*os.File{controlReader, readyWriter}
	if err = parent.Start(); err != nil {
		test.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	_ = controlReader.Close()
	_ = readyWriter.Close()
	if err = readyReader.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		test.Fatal(err)
	}
	reader := bufio.NewReader(readyReader)
	lines := map[string]bool{}
	childPID := 0
	for range 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			test.Fatal("guard barrier failed")
		}
		lines[line] = true
		if value, ok := strings.CutPrefix(line, "parent-ready="); ok {
			childPID, err = strconv.Atoi(strings.TrimSuffix(value, "\n"))
			if err != nil || childPID <= 0 {
				test.Fatal("invalid child receipt")
			}
		}
	}
	if !lines["ready\n"] || childPID == 0 {
		test.Fatal("guard barriers invalid")
	}
	childReaped := false
	defer func() {
		_ = parent.Process.Kill()
		_ = parent.Wait()
		_ = controlWriter.Close()
		if !childReaped {
			var status syscall.WaitStatus
			_, _ = syscall.Wait4(childPID, &status, 0, nil)
		}
	}()
	if err = parent.Process.Kill(); err != nil {
		test.Fatal(err)
	}
	if parent.Wait() == nil {
		test.Fatal("parent death was not induced")
	}
	join := func() error {
		ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSnapshotGuardProcesses$")
		command.Env = append(os.Environ(), "COGNATE_SNAPSHOT_GUARD_TEST=join", "COGNATE_SNAPSHOT_GUARD_ROOT="+directory)
		return command.Run()
	}
	if failure := join(); failure == nil {
		test.Fatal("new Join succeeded while inherited worker fence remained live")
	} else if exit, ok := failure.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
		test.Fatal("Join failure not the intended fence refusal")
	}
	if _, err = controlWriter.Write([]byte{1}); err != nil {
		test.Fatal(err)
	}
	_ = controlWriter.Close()
	var childStatus syscall.WaitStatus
	if _, err = syscall.Wait4(childPID, &childStatus, 0, nil); err != nil || !childStatus.Exited() || childStatus.ExitStatus() != 0 {
		test.Fatal("inherited worker was not actually reaped")
	}
	childReaped = true
	var eof [1]byte
	if _, err = reader.Read(eof[:]); err == nil {
		test.Fatal("guard child did not exit")
	}
	if err = join(); err != nil {
		test.Fatal("Join stayed blocked after actual guard child exit")
	}
	if _, err = os.Stat(filepath.Join(directory, ".snapshot-worker.lock")); err != nil {
		test.Fatal("stable fence inode was removed")
	}
}
