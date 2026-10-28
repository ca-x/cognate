//go:build unix

package maintenance_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func privateDirectory(test *testing.T) string {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	return directory
}

func TestJoinPrivateDirectory(test *testing.T) {
	directory := privateDirectory(test)
	first, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	defer first.Close()
	second, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	if err := first.Close(); err != nil {
		test.Fatal(err)
	}
	if err := first.Close(); err != nil {
		test.Fatal(err)
	}
	if err := second.Close(); err != nil {
		test.Fatal(err)
	}
	third, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	if err := third.Close(); err != nil {
		test.Fatal(err)
	}
}

func TestJoinRejectsUnsafeDirectory(test *testing.T) {
	parent := privateDirectory(test)
	public := filepath.Join(parent, "public")
	if err := os.Mkdir(public, 0o700); err != nil {
		test.Fatal(err)
	}
	if err := os.Chmod(public, 0o755); err != nil {
		test.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		test.Fatal(err)
	}
	for _, directory := range []string{"relative", public, alias, filepath.Join(parent, "missing")} {
		instance, err := maintenance.Join(test.Context(), directory)
		if instance != nil || err == nil {
			if instance != nil {
				_ = instance.Close()
			}
			test.Fatalf("unsafe directory accepted: %q", directory)
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if instance, err := maintenance.Join(ctx, parent); instance != nil || !errors.Is(err, context.Canceled) {
		test.Fatalf("canceled Join: instance=%v error=%v", instance != nil, err)
	}
}

func TestJoinSameFileAndReplacedPaths(test *testing.T) {
	parent := privateDirectory(test)
	directory := filepath.Join(parent, "original")
	if err := os.Mkdir(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	first, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	defer first.Close()
	renamed := filepath.Join(parent, "renamed")
	if err := os.Rename(directory, renamed); err != nil {
		test.Fatal(err)
	}
	second, err := maintenance.Join(test.Context(), renamed)
	if err != nil {
		test.Fatal(err)
	}
	defer second.Close()
	token, err := first.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer token.Release()
	if !token.ValidFor(second) {
		test.Fatal("same directory inode received an independent gate")
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	if replacement, err := maintenance.Join(test.Context(), directory); replacement != nil || !errors.Is(err, maintenance.ErrDirectory) {
		test.Fatalf("live registered path replacement accepted: %v", err)
	}
	token.Release()
	if err := first.Close(); err != nil {
		test.Fatal(err)
	}
	probeProcess(test, renamed, "BLOCKED")
	if err := second.Close(); err != nil {
		test.Fatal(err)
	}
	probeProcess(test, renamed, "ACQUIRED")
}

func TestJoinRejectsUnsafeLock(test *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions", "directory", "replaced"} {
		test.Run(kind, func(test *testing.T) {
			directory := privateDirectory(test)
			lock := filepath.Join(directory, ".instance.lock")
			target := filepath.Join(directory, "untouched")
			if err := os.WriteFile(target, []byte("synthetic content"), 0o600); err != nil {
				test.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink(target, lock); err != nil {
					test.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, lock); err != nil {
					test.Fatal(err)
				}
			case "permissions":
				if err := os.WriteFile(lock, nil, 0o600); err != nil {
					test.Fatal(err)
				}
				if err := os.Chmod(lock, 0o644); err != nil {
					test.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(lock, 0o700); err != nil {
					test.Fatal(err)
				}
			case "replaced":
				instance, err := maintenance.Join(test.Context(), directory)
				if err != nil {
					test.Fatal(err)
				}
				defer instance.Close()
				if err := os.Rename(lock, lock+".old"); err != nil {
					test.Fatal(err)
				}
				if err := os.WriteFile(lock, nil, 0o600); err != nil {
					test.Fatal(err)
				}
			}
			if instance, err := maintenance.Join(test.Context(), directory); instance != nil || !errors.Is(err, maintenance.ErrLock) {
				test.Fatalf("unsafe instance lock accepted: %v", err)
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "synthetic content" {
				test.Fatal("rejected lock modified unrelated synthetic data")
			}
		})
	}
}

func TestConcurrentJoinSharesInstance(test *testing.T) {
	directory := privateDirectory(test)
	results := make(chan *maintenance.Instance, 16)
	failures := make(chan error, 16)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-start
			instance, err := maintenance.Join(test.Context(), directory)
			results <- instance
			failures <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			test.Error(err)
		}
	}
	var instances []*maintenance.Instance
	for instance := range results {
		if instance != nil {
			instances = append(instances, instance)
			defer instance.Close()
		}
	}
	if len(instances) != 16 {
		test.Fatalf("concurrent Join lost handles: %d", len(instances))
	}
	token, err := instances[0].Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer token.Release()
	for _, instance := range instances {
		if !token.ValidFor(instance) {
			test.Fatal("concurrent Join split a directory into separate gates")
		}
	}
}

func TestInstanceProcessHelper(test *testing.T) {
	mode := os.Getenv("COGNATE_MAINTENANCE_CHILD")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(test.Context(), 150*time.Millisecond)
	if mode == "hold" {
		cancel()
		ctx, cancel = context.WithTimeout(test.Context(), 3*time.Second)
	}
	defer cancel()
	instance, err := maintenance.Join(ctx, os.Getenv("COGNATE_MAINTENANCE_DIRECTORY"))
	if mode == "probe" && errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(os.Stdout, "BLOCKED")
		return
	}
	if err != nil {
		test.Fatal(err)
	}
	defer instance.Close()
	if mode == "probe" {
		fmt.Fprintln(os.Stdout, "ACQUIRED")
		return
	}
	fmt.Fprintln(os.Stdout, "HELD")
	var control [1]byte
	if _, err := io.ReadFull(os.Stdin, control[:]); err != nil || control[0] != 'R' {
		test.Fatal("child did not receive its release control")
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "RELEASED")
}

func childCommand(test *testing.T, ctx context.Context, directory, mode string) *exec.Cmd {
	test.Helper()
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	command := exec.CommandContext(ctx, executable, "-test.run=^TestInstanceProcessHelper$", "-test.timeout=6s")
	command.Env = append(os.Environ(), "COGNATE_MAINTENANCE_CHILD="+mode, "COGNATE_MAINTENANCE_DIRECTORY="+directory)
	return command
}

func probeProcess(test *testing.T, directory, expected string) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	output, err := childCommand(test, ctx, directory, "probe").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(output), expected+"\n") {
		test.Fatalf("real child probe expected %s: error=%v output=%s", expected, err, output)
	}
}

func TestChildLockCancellationAndCleanup(test *testing.T) {
	for _, mode := range []string{"release", "kill"} {
		test.Run(mode, func(test *testing.T) {
			directory := privateDirectory(test)
			ctx, cancel := context.WithTimeout(test.Context(), 6*time.Second)
			defer cancel()
			command := childCommand(test, ctx, directory, "hold")
			control, err := command.StdinPipe()
			if err != nil {
				test.Fatal(err)
			}
			defer control.Close()
			output, err := command.StdoutPipe()
			if err != nil {
				test.Fatal(err)
			}
			if err := command.Start(); err != nil {
				test.Fatal(err)
			}
			joined := false
			defer func() {
				if !joined {
					cancel()
					_ = command.Wait()
				}
			}()
			reader := bufio.NewReader(output)
			if message, err := reader.ReadString('\n'); err != nil || message != "HELD\n" {
				test.Fatalf("child did not hold the real process lock: %v", err)
			}
			waiting, stop := context.WithTimeout(test.Context(), 75*time.Millisecond)
			instance, err := maintenance.Join(waiting, directory)
			stop()
			if instance != nil || !errors.Is(err, context.DeadlineExceeded) {
				test.Fatalf("held process lock did not honor caller deadline: %v", err)
			}
			if mode == "release" {
				started := time.Now()
				instance, err := maintenance.Join(context.Background(), directory)
				if instance != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
					test.Fatalf("default Join was not bounded: %v", err)
				}
				if _, err := control.Write([]byte{'R'}); err != nil {
					test.Fatal(err)
				}
				if message, err := reader.ReadString('\n'); err != nil || message != "RELEASED\n" {
					test.Fatalf("child did not finish release: %v", err)
				}
			} else {
				cancel()
			}
			_, readErr := io.Copy(io.Discard, reader)
			waitErr := command.Wait()
			joined = true
			if readErr != nil || mode == "release" && waitErr != nil || mode == "kill" && waitErr == nil {
				test.Fatalf("child cleanup read=%v wait=%v", readErr, waitErr)
			}
			if command.ProcessState == nil {
				test.Fatal("child was not reaped")
			}
			instance, err = maintenance.Join(test.Context(), directory)
			if err != nil {
				test.Fatal(err)
			}
			if err := instance.Close(); err != nil {
				test.Fatal(err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 || entries[0].Name() != ".instance.lock" {
				test.Fatal("child left unexpected files or removed the lock inode")
			}
		})
	}
}
