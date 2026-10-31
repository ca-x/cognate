//go:build linux

package runtimegen

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func privateRoot(testingCase *testing.T) string {
	testingCase.Helper()
	root := testingCase.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		testingCase.Fatal("chmod root")
	}
	return root
}

func TestLeaseProcess(testingCase *testing.T) {
	mode := os.Getenv("T3_LEASE_CHILD")
	if mode == "" {
		return
	}
	budget := 350 * time.Millisecond
	if strings.Contains(mode, "hold") {
		budget = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(testingCase.Context(), budget)
	defer cancel()
	var lease interface{ Close() error }
	var err error
	if strings.HasPrefix(mode, "shared") {
		lease, err = OpenRuntime(ctx, os.Getenv("T3_LEASE_ROOT"))
	} else {
		lease, err = LockOffline(ctx, os.Getenv("T3_LEASE_ROOT"))
	}
	switch {
	case errors.Is(err, ErrNeedsAttention):
		fmt.Println("fenced")
		return
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Println("deadline")
		return
	case err != nil:
		testingCase.Fatal("child acquisition rejected")
	}
	if strings.Contains(mode, "hold") {
		fmt.Println("held")
		var signal [1]byte
		if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
			testingCase.Fatal("child release signal")
		}
	}
	if strings.Contains(mode, "fault") {
		physicalLease := lease.(*Runtime).lease
		physicalLease.ops.close = func(file *os.File) error {
			err := file.Close()
			if file == physicalLease.lock {
				return errors.New("PRIVATE child fault")
			}
			return err
		}
		if !errors.Is(lease.Close(), ErrUncertain) {
			testingCase.Fatal("child fault missing")
		}
		fmt.Println("uncertain")
		return
	}
	if err := lease.Close(); err != nil {
		testingCase.Fatal("child release failed")
	}
	fmt.Println("acquired")
}

func holdProcess(testingCase *testing.T, root, mode string) func() {
	testingCase.Helper()
	ctx, cancel := context.WithTimeout(testingCase.Context(), 10*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLeaseProcess$")
	command.Env = append(os.Environ(), "T3_LEASE_CHILD="+mode, "T3_LEASE_ROOT="+root)
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		testingCase.Fatal("child stdin")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		cancel()
		testingCase.Fatal("child stdout")
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if command.Start() != nil {
		input.Close()
		output.Close()
		cancel()
		testingCase.Fatal("child start")
	}
	reader := bufio.NewReader(output)
	stop := sync.OnceValue(func() error {
		_, writeError := input.Write([]byte{1})
		input.Close()
		remaining, readError := io.ReadAll(reader)
		waitError := command.Wait()
		cancel()
		if writeError != nil || readError != nil || waitError != nil {
			return errors.New("child failed or was not joined")
		}
		expected := "acquired\n"
		if strings.Contains(mode, "fault") {
			expected = "uncertain\n"
		}
		if !bytes.Contains(remaining, []byte(expected)) {
			return errors.New("child release result missing")
		}
		return nil
	})
	finish := func() {
		testingCase.Helper()
		if err := stop(); err != nil {
			testingCase.Error(err)
		}
	}
	testingCase.Cleanup(finish)
	line, err := reader.ReadString('\n')
	if err != nil || line != "held\n" {
		finish()
		testingCase.Fatalf("child did not hold lease; protocol=%q", strings.TrimSpace(line))
	}
	return finish
}

func TestLeaseProcesses(testingCase *testing.T) {
	for _, scenario := range []struct{ name, holder, probe, expected string }{
		{"SH_SH", "shared-hold", "shared", "acquired"},
		{"SH_EX", "shared-hold", "offline", "deadline"},
		{"EX_SH", "offline-hold", "shared", "deadline"},
		{"EX_EX", "offline-hold", "offline", "deadline"},
	} {
		testingCase.Run(scenario.name, func(testingCase *testing.T) {
			root := privateRoot(testingCase)
			finish := holdProcess(testingCase, root, scenario.holder)
			processProbe(testingCase, root, scenario.probe, scenario.expected)
			finish()
			processProbe(testingCase, root, "offline", "acquired")
		})
	}
}

func TestLeaseConcurrentSharedCloseDoesNotClearPeerFence(testingCase *testing.T) {
	root := privateRoot(testingCase)
	finish := holdProcess(testingCase, root, "shared-hold-fault")
	runtime, err := OpenRuntime(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatal(err)
	}
	armed := make(chan struct{})
	proceed := make(chan struct{})
	release := sync.OnceFunc(func() { close(proceed) })
	var physical atomic.Int32
	runtime.lease.ops.close = func(file *os.File) error {
		if file == runtime.lease.lock {
			physical.Add(1)
			close(armed)
			<-proceed
		}
		return file.Close()
	}
	var group sync.WaitGroup
	testingCase.Cleanup(func() { release(); group.Wait() })
	results := make(chan error, 12)
	for range 12 {
		group.Go(func() { results <- runtime.Close() })
	}
	<-armed
	finish()
	release()
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			testingCase.Fatal(err)
		}
	}
	if physical.Load() != 1 {
		testingCase.Fatal("concurrent close repeated physical release")
	}
	entries, err := os.ReadDir(filepath.Join(root, fenceName))
	if err != nil || len(entries) != 1 {
		testingCase.Fatal("successful shared peer must only remove its own fence")
	}
	processProbe(testingCase, root, "offline", "fenced")
	processProbe(testingCase, root, "shared", "fenced")
}

func TestLeaseFinalCloseBarrier(testingCase *testing.T) {
	root := privateRoot(testingCase)
	runtime, err := OpenRuntime(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatal(err)
	}
	armed, proceed := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(proceed) })
	runtime.lease.ops.close = func(file *os.File) error {
		if file == runtime.lease.lock {
			close(armed)
			<-proceed
		}
		return file.Close()
	}
	var group sync.WaitGroup
	testingCase.Cleanup(func() { release(); group.Wait() })
	result := make(chan error, 1)
	group.Go(func() { result <- runtime.Close() })
	<-armed
	entries, readError := os.ReadDir(filepath.Join(root, fenceName))
	processProbe(testingCase, root, "offline", "deadline")
	processProbe(testingCase, root, "shared", "fenced")
	release()
	group.Wait()
	if err := <-result; err != nil {
		testingCase.Fatal(err)
	}
	if readError != nil || len(entries) != 1 {
		testingCase.Fatal("fence must precede final physical release")
	}
	processProbe(testingCase, root, "offline", "acquired")
}

func processProbe(testingCase *testing.T, root, mode, expected string) {
	testingCase.Helper()
	ctx, cancel := context.WithTimeout(testingCase.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLeaseProcess$")
	command.Env = append(os.Environ(), "T3_LEASE_CHILD="+mode, "T3_LEASE_ROOT="+root)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		testingCase.Fatalf("probe failed: %s", output.String())
	}
	if !strings.Contains(output.String(), expected+"\n") {
		testingCase.Fatalf("probe expected %s, got %s", expected, output.String())
	}
}

func TestLeaseLateCloseFence(testingCase *testing.T) {
	root := privateRoot(testingCase)
	runtime, err := OpenRuntime(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatal(err)
	}
	physical := runtime.lease.ops.close
	calls := 0
	runtime.lease.ops.close = func(file *os.File) error {
		if file == runtime.lease.lock {
			calls++
			if err := physical(file); err != nil {
				return err
			}
			return errors.New("PRIVATE close fault /secret/root")
		}
		return physical(file)
	}
	if err := runtime.Close(); !errors.Is(err, ErrUncertain) || strings.Contains(err.Error(), "PRIVATE") {
		testingCase.Fatalf("late close: %v", err)
	}
	file, err := os.OpenFile(filepath.Join(root, lockName), os.O_RDWR, 0)
	if err != nil {
		testingCase.Fatal("raw lock open")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		testingCase.Fatal("fault must really release the physical lock")
	}
	if err := file.Close(); err != nil {
		testingCase.Fatal("raw lock close")
	}
	testingCase.Log("NEGATIVE CONTROL: actual-close-then-error really freed flock; fresh cooperating processes must reject the durable fence")
	processProbe(testingCase, root, "offline", "fenced")
	processProbe(testingCase, root, "shared", "fenced")
	if !errors.Is(runtime.Close(), ErrUncertain) || calls != 1 {
		testingCase.Fatal("uncertain Close must never retry physical close")
	}
	entries, err := os.ReadDir(filepath.Join(root, fenceName))
	if err != nil || len(entries) != 1 {
		testingCase.Fatal("must retain exactly one own persistent fence")
	}
	marker, err := os.ReadFile(filepath.Join(root, fenceName, entries[0].Name()))
	if err != nil || string(marker) != "CGRLS001" {
		testingCase.Fatal("wrong persistent fence format")
	}
}

func TestLeaseSharedAndExclusive(testingCase *testing.T) {
	root := privateRoot(testingCase)
	first, err := OpenRuntime(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatalf("first shared: %v", err)
	}
	testingCase.Cleanup(func() { first.Close() })
	second, err := OpenRuntime(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatalf("second shared: %v", err)
	}
	testingCase.Cleanup(func() { second.Close() })
	ctx, cancel := context.WithTimeout(testingCase.Context(), 40*time.Millisecond)
	defer cancel()
	if offline, err := LockOffline(ctx, root); offline != nil || !errors.Is(err, context.DeadlineExceeded) {
		testingCase.Fatalf("exclusive must wait for both shared holders: %v", err)
	}
	if err := first.Close(); err != nil {
		testingCase.Fatal(err)
	}
	if err := second.Close(); err != nil {
		testingCase.Fatal(err)
	}
	offline, err := LockOffline(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatalf("exclusive after shared release: %v", err)
	}
	if err := offline.Close(); err != nil {
		testingCase.Fatal(err)
	}
}

func TestLeaseInputValidation(testingCase *testing.T) {
	root := privateRoot(testingCase)
	for _, test := range []struct {
		name string
		ctx  context.Context
		root string
	}{
		{"nil-context", nil, root}, {"empty", testingCase.Context(), ""},
		{"missing", testingCase.Context(), filepath.Join(root, "missing")},
	} {
		testingCase.Run(test.name, func(testingCase *testing.T) {
			runtime, err := OpenRuntime(test.ctx, test.root)
			if runtime != nil || !errors.Is(err, ErrInvalid) {
				testingCase.Fatalf("invalid runtime input: %v", err)
			}
			offline, err := LockOffline(test.ctx, test.root)
			if offline != nil || !errors.Is(err, ErrInvalid) {
				testingCase.Fatalf("invalid offline input: %v", err)
			}
		})
	}
	var runtime *Runtime
	var offline *OfflineRoot
	for _, err := range []error{runtime.Close(), offline.Close(), new(Runtime).Close(), new(OfflineRoot).Close()} {
		if !errors.Is(err, ErrInvalid) {
			testingCase.Fatal("nil and zero value must reject")
		}
	}
}

func TestLeaseUnsafeNamespace(testingCase *testing.T) {
	for _, kind := range []string{"root-mode", "root-file", "root-symlink", "ancestor-symlink", "dotdot-symlink", "ancestor-writable", "lock-mode", "lock-symlink", "lock-hardlink", "lock-directory", "lock-fifo", "lock-content", "fence-mode", "fence-symlink", "fence-file"} {
		testingCase.Run(kind, func(testingCase *testing.T) {
			base := privateRoot(testingCase)
			root := filepath.Join(base, "root")
			if os.Mkdir(root, 0700) != nil {
				testingCase.Fatal("root fixture")
			}
			lock := filepath.Join(root, lockName)
			fences := filepath.Join(root, fenceName)
			var err error
			switch kind {
			case "root-mode":
				err = os.Chmod(root, 0750)
			case "root-file":
				err = os.Remove(root)
				if err == nil {
					err = os.WriteFile(root, nil, 0600)
				}
			case "root-symlink":
				err = os.Symlink(root, filepath.Join(base, "link"))
				root = filepath.Join(base, "link")
			case "ancestor-symlink":
				err = os.Symlink(base, filepath.Join(base, "link"))
				root = filepath.Join(base, "link", "root")
			case "dotdot-symlink":
				err = os.Symlink(base, filepath.Join(base, "link"))
				root = base + "/link/../root"
			case "ancestor-writable":
				err = os.Chmod(base, 0770)
			case "lock-mode":
				err = os.WriteFile(lock, nil, 0640)
			case "lock-symlink":
				err = os.Symlink(filepath.Join(base, "foreign"), lock)
			case "lock-hardlink":
				err = os.WriteFile(lock, nil, 0600)
				if err == nil {
					err = os.Link(lock, filepath.Join(base, "foreign"))
				}
			case "lock-directory":
				err = os.Mkdir(lock, 0700)
			case "lock-fifo":
				err = unix.Mkfifo(lock, 0600)
			case "lock-content":
				err = os.WriteFile(lock, []byte("foreign"), 0600)
			case "fence-mode":
				err = os.Mkdir(fences, 0750)
			case "fence-symlink":
				err = os.Symlink(base, fences)
			case "fence-file":
				err = os.WriteFile(fences, nil, 0600)
			}
			if err != nil {
				testingCase.Fatal("unsafe fixture creation")
			}
			before, _ := os.Lstat(lock)
			runtime, err := OpenRuntime(testingCase.Context(), root)
			if runtime != nil {
				runtime.Close()
			}
			if runtime != nil || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), base) {
				testingCase.Fatalf("unsafe namespace accepted or exposed: %v", err)
			}
			if before != nil {
				after, err := os.Lstat(lock)
				if err != nil || !os.SameFile(before, after) {
					testingCase.Fatal("foreign lock removed or replaced")
				}
			}
		})
	}
}

func TestLeaseExistingLockReopened(testingCase *testing.T) {
	root := privateRoot(testingCase)
	lock := filepath.Join(root, lockName)
	if os.WriteFile(lock, nil, 0600) != nil {
		testingCase.Fatal("existing lock fixture")
	}
	before, err := os.Stat(lock)
	if err != nil {
		testingCase.Fatal("existing lock stat")
	}
	for range 3 {
		offline, err := LockOffline(testingCase.Context(), root)
		if err != nil {
			testingCase.Fatal(err)
		}
		if err := offline.Close(); err != nil {
			testingCase.Fatal(err)
		}
		after, err := os.Stat(lock)
		if err != nil || !os.SameFile(before, after) {
			testingCase.Fatal("API replaced or removed coordination lock")
		}
	}
}

func TestLeaseCancellation(testingCase *testing.T) {
	root := privateRoot(testingCase)
	offline, err := LockOffline(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatal(err)
	}
	testingCase.Cleanup(func() { offline.Close() })
	ctx, cancel := context.WithCancel(testingCase.Context())
	var group sync.WaitGroup
	result := make(chan error, 1)
	testingCase.Cleanup(func() { cancel(); group.Wait() })
	group.Go(func() {
		runtime, err := OpenRuntime(ctx, root)
		if runtime != nil {
			runtime.Close()
		}
		result <- err
	})
	select {
	case <-result:
		testingCase.Fatal("startup lease did not wait for exclusive holder")
	case <-time.After(25 * time.Millisecond):
	}
	started := time.Now()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		testingCase.Fatalf("cancellation: %v", err)
	}
	group.Wait()
	if time.Since(started) > 250*time.Millisecond {
		testingCase.Fatal("cancellation not bounded")
	}
	if err := offline.Close(); err != nil {
		testingCase.Fatal(err)
	}
	if runtime, err := OpenRuntime(ctx, root); runtime != nil || !errors.Is(err, context.Canceled) {
		testingCase.Fatal("pre-canceled admission")
	}
	processProbe(testingCase, root, "offline", "acquired")
}

func TestLeaseContextDoesNotReleaseReturnedLease(testingCase *testing.T) {
	root := privateRoot(testingCase)
	ctx, cancel := context.WithCancel(testingCase.Context())
	runtime, err := OpenRuntime(ctx, root)
	if err != nil {
		cancel()
		testingCase.Fatal(err)
	}
	testingCase.Cleanup(func() { runtime.Close() })
	cancel()
	processProbe(testingCase, root, "offline", "deadline")
	if err := runtime.Close(); err != nil {
		testingCase.Fatal(err)
	}
	processProbe(testingCase, root, "offline", "acquired")
}

func TestLeaseReleaseFaults(testingCase *testing.T) {
	for _, stage := range []string{"short-write", "write", "marker-sync", "marker-close", "directory-sync", "root-sync", "directory-close", "root-close", "lock-close", "lock-close-without-release", "unlink", "unlink-after-effect"} {
		testingCase.Run(stage, func(testingCase *testing.T) {
			root := privateRoot(testingCase)
			runtime, err := OpenRuntime(testingCase.Context(), root)
			if err != nil {
				testingCase.Fatal(err)
			}
			lease := runtime.lease
			testingCase.Cleanup(func() { lease.fences.Close(); lease.root.Close(); lease.lock.Close() })
			fault := errors.New("PRIVATE injected failure /secret/root credential=synthetic")
			var injections atomic.Int32
			isMarker := func(file *os.File) bool { return file != lease.root && file != lease.lock && file != lease.fences }
			lease.ops.write = func(file *os.File, data []byte) (int, error) {
				if stage == "short-write" {
					injections.Add(1)
					return file.Write(data[:3])
				}
				written, err := file.Write(data)
				if stage == "write" {
					injections.Add(1)
					return written, fault
				}
				return written, err
			}
			lease.ops.sync = func(file *os.File) error {
				err := file.Sync()
				if stage == "marker-sync" && isMarker(file) || stage == "directory-sync" && file == lease.fences || stage == "root-sync" && file == lease.root {
					injections.Add(1)
					return fault
				}
				return err
			}
			lease.ops.close = func(file *os.File) error {
				if stage == "lock-close-without-release" && file == lease.lock {
					injections.Add(1)
					return fault
				}
				err := file.Close()
				if stage == "marker-close" && isMarker(file) || stage == "directory-close" && file == lease.fences || stage == "root-close" && file == lease.root || stage == "lock-close" && file == lease.lock {
					injections.Add(1)
					return fault
				}
				return err
			}
			lease.ops.remove = func(path string) error {
				if stage == "unlink" {
					injections.Add(1)
					return fault
				}
				err := unix.Unlink(path)
				if stage == "unlink-after-effect" {
					injections.Add(1)
					return fault
				}
				return err
			}
			var group sync.WaitGroup
			results := make(chan error, 8)
			for range 8 {
				group.Go(func() { results <- runtime.Close() })
			}
			group.Wait()
			close(results)
			for err := range results {
				if !errors.Is(err, ErrUncertain) || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), root) {
					testingCase.Fatalf("fault or sanitized terminal state: %v", err)
				}
			}
			if injections.Load() != 1 {
				testingCase.Fatal("unknown operation was retried or fault was not exercised")
			}
			entries, err := os.ReadDir(filepath.Join(root, fenceName))
			if err != nil {
				testingCase.Fatal("fence read")
			}
			if stage == "unlink-after-effect" {
				if len(entries) != 0 {
					testingCase.Fatal("unlink negative control did not take effect")
				}
				processProbe(testingCase, root, "offline", "acquired")
				testingCase.Log("BOUNDARY: unlink took effect after all owned FDs were confirmed closed; returned error does not mean still fenced")
				return
			}
			if len(entries) != 1 {
				testingCase.Fatal("fault lost own fence or created multiple fences")
			}
			if stage != "lock-close" && stage != "unlink" {
				probe, err := os.OpenFile(filepath.Join(root, lockName), os.O_RDWR, 0)
				if err != nil {
					testingCase.Fatal("raw lock open")
				}
				flockError := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
				probe.Close()
				if !errors.Is(flockError, unix.EWOULDBLOCK) {
					testingCase.Fatal("early release failure must retain authority")
				}
			}
			lease.lock.Close()
			processProbe(testingCase, root, "offline", "fenced")
		})
	}
}

func TestLeaseForeignFenceRejectedAndPreserved(testingCase *testing.T) {
	for _, kind := range []string{"empty", "partial", "unknown", "symlink", "directory"} {
		testingCase.Run(kind, func(testingCase *testing.T) {
			root := privateRoot(testingCase)
			directory := filepath.Join(root, fenceName)
			if os.Mkdir(directory, 0700) != nil {
				testingCase.Fatal("foreign directory")
			}
			marker := filepath.Join(directory, "foreign")
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(marker, nil, 0600)
			case "partial":
				err = os.WriteFile(marker, []byte("CGR"), 0600)
			case "unknown":
				err = os.WriteFile(marker, []byte("UNKNOWN!"), 0600)
			case "symlink":
				err = os.Symlink("missing", marker)
			case "directory":
				err = os.Mkdir(marker, 0700)
			}
			if err != nil {
				testingCase.Fatal("foreign marker fixture")
			}
			before, err := os.Lstat(marker)
			if err != nil {
				testingCase.Fatal("foreign stat")
			}
			processProbe(testingCase, root, "shared", "fenced")
			processProbe(testingCase, root, "offline", "fenced")
			after, err := os.Lstat(marker)
			if err != nil || !os.SameFile(before, after) {
				testingCase.Fatal("foreign fence was touched")
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 {
				testingCase.Fatal("rejected admissions left extra markers")
			}
		})
	}
}

func TestLeaseReplacementDetectedWithoutForeignDeletion(testingCase *testing.T) {
	for _, kind := range []string{"root", "lock", "fence-directory"} {
		testingCase.Run(kind, func(testingCase *testing.T) {
			base := privateRoot(testingCase)
			root := filepath.Join(base, "root")
			if os.Mkdir(root, 0700) != nil {
				testingCase.Fatal("replacement root fixture")
			}
			runtime, err := OpenRuntime(testingCase.Context(), root)
			if err != nil {
				testingCase.Fatal(err)
			}
			lease := runtime.lease
			testingCase.Cleanup(func() { lease.fences.Close(); lease.root.Close(); lease.lock.Close() })
			entry := root
			if kind == "lock" {
				entry = filepath.Join(root, lockName)
			}
			if kind == "fence-directory" {
				entry = filepath.Join(root, fenceName)
			}
			old := entry + "-old"
			if os.Rename(entry, old) != nil {
				testingCase.Fatal("unsupported replacement fixture rename")
			}
			if kind == "lock" {
				err = os.WriteFile(entry, nil, 0600)
			} else {
				err = os.Mkdir(entry, 0700)
			}
			if err != nil {
				testingCase.Fatal("unsupported replacement fixture create")
			}
			foreign, err := os.Lstat(entry)
			if err != nil {
				testingCase.Fatal("replacement stat")
			}
			if !errors.Is(runtime.Close(), ErrUncertain) || !errors.Is(runtime.Close(), ErrUncertain) {
				testingCase.Fatal("detected replacement must terminate uncertain")
			}
			after, err := os.Lstat(entry)
			if err != nil || !os.SameFile(foreign, after) {
				testingCase.Fatal("foreign replacement deleted")
			}
			pinnedFences := filepath.Join(root, fenceName)
			if kind == "root" {
				pinnedFences = filepath.Join(old, fenceName)
			}
			if kind == "fence-directory" {
				pinnedFences = old
			}
			entries, err := os.ReadDir(pinnedFences)
			if err != nil || len(entries) != 1 {
				testingCase.Fatal("detected replacement must fence pinned original authority")
			}
			testingCase.Log("UNSUPPORTED namespace replacement detected; original pinned tree fenced, no same-path cross-inode exclusion claim")
		})
	}
}

func TestLeaseArmFailureRetainsAuthorityThroughGC(testingCase *testing.T) {
	root := privateRoot(testingCase)
	var descriptors []struct {
		descriptor int
		identity   unix.Stat_t
	}
	func() {
		holder, err := OpenRuntime(testingCase.Context(), root)
		if err != nil {
			testingCase.Fatal(err)
		}
		for _, file := range []*os.File{holder.lease.root, holder.lease.fences, holder.lease.lock} {
			var identity unix.Stat_t
			if unix.Fstat(int(file.Fd()), &identity) != nil {
				testingCase.Fatal("GC fixture stat")
			}
			descriptors = append(descriptors, struct {
				descriptor int
				identity   unix.Stat_t
			}{int(file.Fd()), identity})
		}
		holder.lease.ops.createMarker = func(*os.File, string) (*os.File, error) { return nil, errors.New("PRIVATE disk-full simulation") }
		if !errors.Is(holder.Close(), ErrUncertain) {
			testingCase.Fatal("arming failure must not report closed")
		}
	}()
	for range 3 {
		runtime.GC()
	}
	probe, err := os.OpenFile(filepath.Join(root, lockName), os.O_RDWR, 0)
	if err != nil {
		testingCase.Fatal("raw GC probe")
	}
	flockError := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	probe.Close()
	for _, entry := range descriptors {
		var current unix.Stat_t
		if unix.Fstat(entry.descriptor, &current) == nil && sameInode(current, entry.identity) {
			unix.Close(entry.descriptor)
		}
	}
	if !errors.Is(flockError, unix.EWOULDBLOCK) {
		testingCase.Fatal("arming failure lost physical authority to finalizers when caller discarded the terminal handle")
	}
	testingCase.Log("BOUNDARY: cannot persist a fence when storage refuses creation; retain physical authority, do not attempt final release")
}

func TestLeaseCleanupUncertaintyPrecedesCancellation(testingCase *testing.T) {
	root := privateRoot(testingCase)
	ctx, cancel := context.WithCancel(testingCase.Context())
	defer cancel()
	closes := 0
	ops := leaseOps{
		close: func(file *os.File) error { closes++; file.Close(); return errors.New("PRIVATE physical close error") },
		sync:  func(file *os.File) error { err := file.Sync(); cancel(); return err },
	}
	lease, err := openLeaseUsing(ctx, root, false, ops)
	if lease != nil || !errors.Is(err, ErrUncertain) || errors.Is(err, context.Canceled) || closes != 3 {
		testingCase.Fatalf("cleanup must precede cancellation: %v; closes=%d", err, closes)
	}
	if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), root) {
		testingCase.Fatal("cleanup leaked private details")
	}
	processProbe(testingCase, root, "offline", "acquired")
}

func TestLeaseWaitingAdmissionRejectsLockReplacement(testingCase *testing.T) {
	root := privateRoot(testingCase)
	offline, err := LockOffline(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatal(err)
	}
	testingCase.Cleanup(func() { offline.lease.fences.Close(); offline.lease.root.Close(); offline.lease.lock.Close() })
	ctx, cancel := context.WithTimeout(testingCase.Context(), time.Second)
	var group sync.WaitGroup
	result := make(chan error, 1)
	testingCase.Cleanup(func() { cancel(); group.Wait() })
	group.Go(func() {
		runtime, err := OpenRuntime(ctx, root)
		if runtime != nil {
			runtime.Close()
		}
		result <- err
	})
	select {
	case <-result:
		testingCase.Fatal("waiter did not wait")
	case <-time.After(25 * time.Millisecond):
	}
	lock := filepath.Join(root, lockName)
	if os.Rename(lock, lock+"-old") != nil || os.WriteFile(lock, nil, 0600) != nil {
		testingCase.Fatal("replacement fixture")
	}
	if err := <-result; !errors.Is(err, ErrInvalid) {
		testingCase.Fatalf("waiting admission accepted replacement: %v", err)
	}
	group.Wait()
	if !errors.Is(offline.Close(), ErrUncertain) {
		testingCase.Fatal("holder failed to detect replacement")
	}
}

func TestLeaseOfflineConcurrentClose(testingCase *testing.T) {
	root := privateRoot(testingCase)
	offline, err := LockOffline(testingCase.Context(), root)
	if err != nil {
		testingCase.Fatal(err)
	}
	var physical atomic.Int32
	offline.lease.ops.close = func(file *os.File) error {
		if file == offline.lease.lock {
			physical.Add(1)
		}
		return file.Close()
	}
	var group sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		group.Go(func() { results <- offline.Close() })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			testingCase.Fatal(err)
		}
	}
	if physical.Load() != 1 {
		testingCase.Fatal("offline close must retire ownership exactly once")
	}
	processProbe(testingCase, root, "shared", "acquired")
}

func TestLeaseOtherOwnerPrivateRootRejected(testingCase *testing.T) {
	for _, candidate := range []string{"/var/lib/private", "/root"} {
		var stat unix.Stat_t
		if unix.Lstat(candidate, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&07777 != 0700 || stat.Uid == uint32(os.Geteuid()) {
			continue
		}
		holder, err := OpenRuntime(testingCase.Context(), candidate)
		if holder != nil {
			holder.Close()
		}
		if holder != nil || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), candidate) {
			testingCase.Fatalf("foreign-owner root accepted or exposed: %v", err)
		}
		testingCase.Log("real existing 0700 foreign-UID root denied without mutation; no privileged fixture setup")
		return
	}
	testingCase.Skip("no real foreign-UID 0700 directory available to unprivileged test runner")
}
