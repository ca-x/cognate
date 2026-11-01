//go:build linux

package state

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"golang.org/x/sys/unix"
)

func snapshotRuntimeBarrier(phase string) {
	if os.Getenv("COGNATE_R2_CHILD_BARRIER") != phase {
		return
	}
	ready := os.NewFile(12, "ready")
	release := os.NewFile(13, "release")
	if _, err := fmt.Fprintf(ready, "%d\n", os.Getpid()); err != nil {
		os.Exit(91)
	}
	var signal [1]byte
	if _, err := io.ReadFull(release, signal[:]); err != nil {
		os.Exit(92)
	}
	if errors.Join(ready.Close(), release.Close()) != nil {
		os.Exit(93)
	}
}

func TestLocalSnapshotRuntimeParentProcess(test *testing.T) {
	root := os.Getenv("COGNATE_R2_PARENT_ROOT")
	if root == "" {
		test.Skip("subprocess helper")
	}
	owner, err := runtimegen.OpenRuntime(test.Context(), root)
	if err != nil {
		test.Fatal(err)
	}
	defer owner.Close()
	store, err := OpenWithOptions(test.Context(), filepath.Join(root, "server.db"), bytes.Repeat([]byte{61}, 32), Options{MaintenanceManaged: true, Runtime: owner})
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	ready, release := os.NewFile(3, "ready"), os.NewFile(4, "release")
	defer ready.Close()
	defer release.Close()
	ops := snapshotWorkerOps{prepare: func(command *exec.Cmd) {
		command.Env = append(command.Env, "COGNATE_R2_CHILD_BARRIER=before")
		command.ExtraFiles = append(command.ExtraFiles, ready, release)
		if os.Getenv("COGNATE_R2_DEFAULT_PDEATH") == "" {
			command.SysProcAttr.Pdeathsig = 0
		}
	}}
	_, err = store.withLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil }, ops)
	if err != nil {
		test.Fatal(err)
	}
}

func snapshotRuntimeProbe(test *testing.T, root string, blocked bool) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 150*time.Millisecond)
	defer cancel()
	owner, err := runtimegen.LockOffline(ctx, root)
	if owner != nil {
		if closeError := owner.Close(); closeError != nil {
			test.Fatal(closeError)
		}
	}
	if blocked && !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("EX passed while executable worker retained source/stage FDs", err)
	}
	if !blocked && err != nil {
		test.Fatal("EX did not pass after actual reap", err)
	}
}

func TestLocalSnapshotRuntimeParentDeath(test *testing.T) {
	for _, defaultSignal := range []bool{false, true} {
		test.Run(fmt.Sprintf("defaultPdeath=%t", defaultSignal), func(test *testing.T) {
			snapshotRuntimeParentDeath(test, defaultSignal)
		})
	}
}

func snapshotRuntimeReap(test *testing.T, pid int) unix.WaitStatus {
	test.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status unix.WaitStatus
		waited, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
		if err != nil {
			test.Fatal("wait4 failed", err)
		}
		if waited == pid {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = unix.Kill(pid, unix.SIGKILL)
	var status unix.WaitStatus
	_, _ = unix.Wait4(pid, &status, 0, nil)
	test.Fatal("worker not actually reaped within budget")
	return status
}

func snapshotRuntimeParentDeath(test *testing.T, defaultSignal bool) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		test.Fatal(err)
	}
	defer unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0)
	root := test.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		test.Fatal(err)
	}
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer readyReader.Close()
	defer readyWriter.Close()
	releaseReader, releaseWriter, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer releaseReader.Close()
	defer releaseWriter.Close()
	ctx, cancel := context.WithTimeout(test.Context(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalSnapshotRuntimeParentProcess$", "-test.timeout=10s")
	command.Env = append(os.Environ(), "COGNATE_R2_PARENT_ROOT="+root)
	if defaultSignal {
		command.Env = append(command.Env, "COGNATE_R2_DEFAULT_PDEATH=1")
	}
	command.ExtraFiles = []*os.File{readyWriter, releaseReader}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	if err := readyReader.SetReadDeadline(time.Now().Add(7 * time.Second)); err != nil {
		test.Fatal(err)
	}
	var pid int
	if _, err := fmt.Fscanln(readyReader, &pid); err != nil || pid < 1 {
		_ = command.Process.Kill()
		_ = command.Wait()
		test.Fatal("worker not executable at barrier", err, output.String())
	}
	joined := false
	defer func() {
		if !joined {
			_ = unix.Kill(pid, unix.SIGKILL)
			var status unix.WaitStatus
			_, _ = unix.Wait4(pid, &status, 0, nil)
		}
	}()
	if err := command.Process.Kill(); err != nil {
		test.Fatal(err)
	}
	if err := command.Wait(); err == nil || !command.ProcessState.Exited() && !command.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
		test.Fatal("parent not actually reaped", err)
	}
	if !defaultSignal {
		if err := unix.Kill(pid, 0); err != nil {
			test.Fatal("worker not alive at executable barrier", err)
		}
		snapshotRuntimeProbe(test, root, true)
		if _, err := releaseWriter.Write([]byte{1}); err != nil {
			test.Fatal(err)
		}
	}
	status := snapshotRuntimeReap(test, pid)
	if defaultSignal && (!status.Signaled() || status.Signal() != unix.SIGKILL) {
		test.Fatal("default parent-death signal was not SIGKILL", status)
	}
	joined = true
	test.Log("actual worker terminal", strconv.Itoa(int(status)))
	snapshotRuntimeProbe(test, root, false)
}

func snapshotRuntimeStore(test *testing.T) (*Store, *runtimegen.Runtime, string) {
	test.Helper()
	root := test.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		test.Fatal(err)
	}
	owner, err := runtimegen.OpenRuntime(test.Context(), root)
	if err != nil {
		test.Fatal(err)
	}
	store, err := OpenWithOptions(test.Context(), filepath.Join(root, "server.db"), bytes.Repeat([]byte{61}, 32), Options{MaintenanceManaged: true, Runtime: owner})
	if err != nil {
		_ = owner.Close()
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if store.Close() == nil {
			_ = owner.Close()
		}
	})
	return store, owner, root
}

func TestLocalSnapshotRuntimeDrain(test *testing.T) {
	for _, cancelWorker := range []bool{false, true} {
		test.Run(fmt.Sprintf("cancel=%t", cancelWorker), func(test *testing.T) {
			store, owner, root := snapshotRuntimeStore(test)
			readyReader, readyWriter, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer readyReader.Close()
			defer readyWriter.Close()
			releaseReader, releaseWriter, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer releaseReader.Close()
			defer releaseWriter.Close()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			worker := make(chan error, 1)
			go func() {
				_, err := store.withLocalSnapshot(ctx, snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil }, snapshotWorkerOps{prepare: func(command *exec.Cmd) {
					command.Env = append(command.Env, "COGNATE_R2_CHILD_BARRIER=after")
					command.ExtraFiles = append(command.ExtraFiles, readyWriter, releaseReader)
				}})
				worker <- err
			}()
			_ = readyReader.SetReadDeadline(time.Now().Add(5 * time.Second))
			var pid int
			if _, err := fmt.Fscanln(readyReader, &pid); err != nil {
				test.Fatal("after real response/EOF barrier missing", err)
			}
			closed := make(chan error, 1)
			go func() { closed <- errors.Join(store.Close(), owner.Close()) }()
			snapshotRuntimeProbe(test, root, true)
			select {
			case err := <-closed:
				test.Fatal("Close bypassed actual Wait", err)
			default:
			}
			if cancelWorker {
				cancel()
			} else {
				_, _ = releaseWriter.Write([]byte{1})
			}
			select {
			case err := <-worker:
				if cancelWorker && !errors.Is(err, context.Canceled) || !cancelWorker && err != nil {
					test.Fatal("worker result", err)
				}
			case <-time.After(5 * time.Second):
				test.Fatal("worker/cancellation callback not joined")
			}
			select {
			case err := <-closed:
				if err != nil {
					test.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				test.Fatal("scope/Close lock inversion")
			}
			if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
				test.Fatal("worker remained after Wait", err)
			}
			snapshotRuntimeProbe(test, root, false)
		})
	}
}

func TestLocalSnapshotRuntimeUncertain(test *testing.T) {
	for _, fault := range []string{"wait", "pipe-close"} {
		test.Run(fault, func(test *testing.T) {
			store, _, root := snapshotRuntimeStore(test)
			calls := 0
			ops := snapshotWorkerOps{}
			if fault == "wait" {
				ops.wait = func(command *exec.Cmd) error {
					calls++
					if err := command.Wait(); err != nil {
						test.Error("actual Wait", err)
					}
					command.ProcessState = nil
					return errors.New("lost terminal proof after real reap")
				}
			} else {
				ops.close = func(file *os.File) error {
					err := file.Close()
					calls++
					if calls == 2 {
						return errors.Join(err, errors.New("physical pipe close succeeded, result unknown"))
					}
					return err
				}
			}
			called := false
			_, err := store.withLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil }, ops)
			if !errors.Is(err, ErrLocalSnapshotUncertain) || called {
				test.Fatal("worker uncertainty lost", err)
			}
			before := calls
			for range 2 {
				if err := store.Close(); !errors.Is(err, ErrLocalSnapshotUncertain) {
					test.Fatal("Store poison lost", err)
				}
			}
			runtime.GC()
			if calls != before {
				test.Fatal("physical lifecycle operation retried")
			}
			snapshotRuntimeProbe(test, root, true)
		})
	}
}

func TestLocalSnapshotRuntimeBindingAndDescriptors(test *testing.T) {
	test.Run("closed-runtime-before-write", func(test *testing.T) {
		root := test.TempDir()
		_ = os.Chmod(root, 0700)
		owner, err := runtimegen.OpenRuntime(test.Context(), root)
		if err != nil {
			test.Fatal(err)
		}
		if err := owner.Close(); err != nil {
			test.Fatal(err)
		}
		store, err := OpenWithOptions(test.Context(), filepath.Join(root, "server.db"), bytes.Repeat([]byte{61}, 32), Options{Runtime: owner})
		if store != nil || !errors.Is(err, runtimegen.ErrInvalid) {
			test.Fatal("closed Runtime admitted", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "server.db")); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("closed Runtime wrote DB", err)
		}
	})
	test.Run("wrong-root-before-write", func(test *testing.T) {
		_, owner, _ := snapshotRuntimeStore(test)
		root := test.TempDir()
		_ = os.Chmod(root, 0700)
		store, err := OpenWithOptions(test.Context(), filepath.Join(root, "server.db"), bytes.Repeat([]byte{61}, 32), Options{MaintenanceManaged: true, Runtime: owner})
		if store != nil || !errors.Is(err, runtimegen.ErrInvalid) {
			test.Fatal("wrong root admitted", err)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			test.Fatal("wrong root wrote before binding", err)
		}
	})
	for _, fault := range []string{"missing-lock", "missing-root", "swapped", "wrong-root", "wrong-lock", "closed-lock"} {
		test.Run(fault, func(test *testing.T) {
			store, _, _ := snapshotRuntimeStore(test)
			otherRoot := test.TempDir()
			_ = os.Chmod(otherRoot, 0700)
			other, err := runtimegen.OpenRuntime(test.Context(), otherRoot)
			if err != nil {
				test.Fatal(err)
			}
			defer other.Close()
			var foreignRoot, foreignLock *os.File
			if err := other.WithWorkerFiles(test.Context(), func(root, lock *os.File) error { foreignRoot, foreignLock = root, lock; return nil }); err != nil {
				test.Fatal(err)
			}
			dead, err := os.Open(os.DevNull)
			if err != nil {
				test.Fatal(err)
			}
			_ = dead.Close()
			called := false
			var stagePath string
			_, err = store.withLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil }, snapshotWorkerOps{prepare: func(command *exec.Cmd) {
				stagePath = command.ExtraFiles[5].Name()
				switch fault {
				case "missing-lock":
					command.ExtraFiles[7] = nil
				case "missing-root":
					command.ExtraFiles[8] = nil
				case "swapped":
					command.ExtraFiles[7], command.ExtraFiles[8] = command.ExtraFiles[8], command.ExtraFiles[7]
				case "wrong-root":
					command.ExtraFiles[8] = foreignRoot
				case "wrong-lock":
					command.ExtraFiles[7] = foreignLock
				case "closed-lock":
					command.ExtraFiles[7] = dead
				}
			}, wait: func(command *exec.Cmd) error {
				err := command.Wait()
				entries, readError := os.ReadDir(stagePath)
				if readError != nil || len(entries) != 0 {
					test.Error("FD rejection touched stage", readError)
				}
				return err
			}})
			if err == nil || called {
				test.Fatal("untrusted exec descriptors admitted", err)
			}
			if err := store.Close(); err != nil {
				test.Fatal("known refusal poisoned Store", err)
			}
		})
	}
}

func TestLocalSnapshotRuntimeExecFrameRefusal(test *testing.T) {
	for _, fault := range []string{"old", "flags", "short", "root-inode", "root-parent", "extra"} {
		test.Run(fault, func(test *testing.T) {
			store, _, _ := snapshotRuntimeStore(test)
			var injected *os.File
			defer func() {
				if injected != nil {
					_ = injected.Close()
				}
			}()
			var stagePath string
			called := false
			_, err := store.withLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil }, snapshotWorkerOps{
				prepare: func(command *exec.Cmd) {
					stagePath = command.ExtraFiles[5].Name()
					frame, err := io.ReadAll(command.ExtraFiles[0])
					if err != nil || len(frame) != 448 {
						test.Error("cannot read genuine request", err)
						return
					}
					switch fault {
					case "old":
						copy(frame[:8], "CGB2WR01")
						binary.BigEndian.PutUint16(frame[8:10], 1)
					case "flags":
						frame[11] = 128
					case "short":
						frame = frame[:447]
					case "root-inode":
						frame[447] ^= 128
					case "root-parent":
						frame[63] ^= 128
					case "extra":
						frame = append(frame, 0)
					}
					reader, writer, err := os.Pipe()
					if err != nil {
						test.Error(err)
						return
					}
					injected = reader
					if _, err := writer.Write(frame); err != nil {
						test.Error(err)
					}
					if err := writer.Close(); err != nil {
						test.Error(err)
					}
					command.ExtraFiles[0] = reader
				}, wait: func(command *exec.Cmd) error {
					err := command.Wait()
					entries, readError := os.ReadDir(stagePath)
					if readError != nil || len(entries) != 0 {
						test.Error("frame rejection touched stage", readError)
					}
					return err
				},
			})
			if err == nil || called || errors.Is(err, ErrLocalSnapshotUncertain) {
				test.Fatal("unsafe real-exec frame accepted or poisoned", err)
			}
		})
	}
}

func TestLocalSnapshotRuntimeRequestV2(test *testing.T) {
	request := snapshotWorkerRequest{nonce: [16]byte{1}, source: snapshotFileIdentity{1, 2}, parent: snapshotFileIdentity{1, 3}, stage: snapshotFileIdentity{1, 4}, root: snapshotFileIdentity{1, 3}, name: "server.db", op: 1, limits: snapshotTestLimits()}
	request.limits.DrainTimeout = request.limits.OperationTimeout
	frame, err := snapshotEncodeRequest(request)
	if err != nil {
		test.Fatal(err)
	}
	decoded, err := snapshotDecodeRequest(bytes.NewReader(frame[:]))
	if err != nil || decoded != request {
		test.Fatal("v2 roundtrip", err)
	}
	for _, name := range []string{"server.db", string(bytes.Repeat([]byte{'z'}, 255))} {
		bare := request
		bare.name = name
		bare.root = snapshotFileIdentity{}
		encoded, err := snapshotEncodeRequest(bare)
		if err != nil {
			test.Fatal(err)
		}
		decoded, err := snapshotDecodeRequest(bytes.NewReader(encoded[:]))
		if err != nil || decoded != bare {
			test.Fatal("optional nil Runtime/name boundary", err)
		}
	}
	for _, fault := range []string{"old", "unknown-version", "flags", "short", "extra", "zero-root", "unflagged-root", "padding"} {
		test.Run(fault, func(test *testing.T) {
			input := bytes.Clone(frame[:])
			switch fault {
			case "old":
				copy(input[:8], "CGB2WR01")
				binary.BigEndian.PutUint16(input[8:10], 1)
			case "unknown-version":
				input[9] = 3
			case "flags":
				input[11] = 3
			case "short":
				input = input[:447]
			case "extra":
				input = append(input, 0)
			case "zero-root":
				clear(input[432:])
			case "unflagged-root":
				input[11] = 0
			case "padding":
				input[431] = 1
			}
			if _, err := snapshotDecodeRequest(bytes.NewReader(input)); !errors.Is(err, ErrLocalSnapshotInvalid) {
				test.Fatal("unsafe frame admitted", err)
			}
		})
	}
}
