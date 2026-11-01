//go:build linux

package library

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/spf13/afero"
	"golang.org/x/sys/unix"
)

const testRepairID = "0123456789abcdef0123456789abcdef"

func offlineRepairFixture(test *testing.T) (string, string, []byte) {
	test.Helper()
	root := test.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		test.Fatal(err)
	}
	cache, err := OpenArchiveCache(filepath.Join(root, "archives"))
	if err != nil {
		test.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		test.Fatal(err)
	}
	data := fixtureArchive(test)
	hash := archiveHash(data)
	writeRepairFixture(test, root, hash+".tar.gz", bytes.Repeat([]byte("x"), len(data)))
	return root, hash, data
}

func writeRepairFixture(test *testing.T, root, name string, data []byte) {
	test.Helper()
	if err := os.WriteFile(filepath.Join(root, "archives", name), data, 0600); err != nil {
		test.Fatal(err)
	}
}

func invokeOfflineRepair(ctx context.Context, root, hash, repairID string, data []byte, operations archiveRepairOps) (ArchiveRepairResult, error) {
	owner, err := runtimegen.LockOffline(ctx, root)
	if err != nil {
		return ArchiveRepairResult{}, err
	}
	result, repairError := repairOfflineArchive(ctx, owner, hash, repairID, data, operations)
	return result, errors.Join(repairError, owner.Close())
}

func TestOfflineRepairStates(test *testing.T) {
	for _, mode := range []string{"missing", "healthy", "reusable stage", "invalid stage", "retained artifact"} {
		test.Run(mode, func(test *testing.T) {
			root, hash, data := offlineRepairFixture(test)
			target := filepath.Join(root, "archives", hash+".tar.gz")
			stage := ".repair-" + hash + "-" + testRepairID + ".tar.gz"
			before, err := os.Stat(target)
			if err != nil {
				test.Fatal(err)
			}
			switch mode {
			case "missing":
				if err := os.Remove(target); err != nil {
					test.Fatal(err)
				}
			case "healthy", "retained artifact":
				writeRepairFixture(test, root, hash+".tar.gz", data)
				if mode == "retained artifact" {
					writeRepairFixture(test, root, stage, []byte("not a historical receipt"))
				}
			case "reusable stage":
				writeRepairFixture(test, root, stage, data)
			case "invalid stage":
				writeRepairFixture(test, root, stage, []byte("partial stage"))
			}
			operations := defaultArchiveRepairOps()
			syncs := 0
			operations.sync = func(file archiveRepairSyncer) error {
				syncs++
				return file.Sync()
			}
			result, err := invokeOfflineRepair(test.Context(), root, hash, testRepairID, data, operations)
			if mode == "invalid stage" {
				if err == nil {
					test.Fatal("partial stage accepted")
				}
				actual, readError := os.ReadFile(target)
				if readError != nil || !bytes.Equal(actual, bytes.Repeat([]byte("x"), len(data))) {
					test.Fatal("target changed on invalid stage", readError)
				}
				return
			}
			if err != nil {
				test.Fatal(err)
			}
			if mode == "healthy" || mode == "retained artifact" {
				if !result.AlreadyVerified || result.Retained != (mode == "retained artifact") || syncs != 0 {
					test.Fatal("healthy target mutated or invented receipt", result, syncs)
				}
				actual, err := os.Stat(target)
				if err != nil || !os.SameFile(before, actual) {
					test.Fatal("healthy target replaced", err)
				}
				return
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, data) || result.AlreadyVerified || result.Retained != (mode != "missing") {
				test.Fatal("incorrect repair", result, err)
			}
			if mode == "reusable stage" && syncs != 3 {
				test.Fatal("resumed stage was not resynced", syncs)
			}
		})
	}
}

func TestOfflineRepairBudget(test *testing.T) {
	for _, mode := range []string{"exact", "one byte short", "resumed exact", "residual charged"} {
		test.Run(mode, func(test *testing.T) {
			root, hash, data := offlineRepairFixture(test)
			budget := 2 * len(data)
			if mode == "one byte short" {
				budget--
			}
			if mode == "resumed exact" {
				writeRepairFixture(test, root, ".repair-"+hash+"-"+testRepairID+".tar.gz", data)
			}
			if mode == "residual charged" {
				writeRepairFixture(test, root, ".abandoned", []byte("x"))
			}
			writeRepairFixture(test, root, ".cache-budget", []byte("skillhub-archive-budget-v1:"+strconv.Itoa(budget)+"\n"))
			_, err := invokeOfflineRepair(test.Context(), root, hash, testRepairID, data, defaultArchiveRepairOps())
			if mode == "one byte short" || mode == "residual charged" {
				if !errors.Is(err, ErrCacheFull) {
					test.Fatal("budget not enforced", err)
				}
			} else if err != nil {
				test.Fatal("exact budget refused", err)
			}
		})
	}
}

func TestOfflineRepairRejectsUnsafeEntries(test *testing.T) {
	for _, fault := range []string{"missing policy", "public policy", "invalid policy", "oversized policy", "public target", "oversized target", "symlink", "hardlink", "fifo", "directory", "public directory", "generation", "wrong input", "invalid archive", "invalid ID"} {
		test.Run(fault, func(test *testing.T) {
			root, hash, data := offlineRepairFixture(test)
			target := filepath.Join(root, "archives", hash+".tar.gz")
			policy := filepath.Join(root, "archives", ".cache-budget")
			repairID := testRepairID
			var err error
			switch fault {
			case "missing policy":
				err = os.Remove(policy)
			case "public policy":
				err = os.Chmod(policy, 0644)
			case "invalid policy":
				err = os.WriteFile(policy, []byte("skillhub-archive-budget-v1:01024\n"), 0600)
			case "oversized policy":
				err = os.WriteFile(policy, []byte("skillhub-archive-budget-v1:1099511627777\n"), 0600)
			case "public target":
				err = os.Chmod(target, 0644)
			case "oversized target":
				err = os.Truncate(target, (32<<20)+1)
			case "symlink":
				err = os.Symlink(target, filepath.Join(root, "archives", ".unsafe"))
			case "hardlink":
				err = os.Link(target, filepath.Join(root, "outside-alias"))
			case "fifo":
				err = unix.Mkfifo(filepath.Join(root, "archives", ".unsafe"), 0600)
			case "directory":
				err = os.Mkdir(filepath.Join(root, "archives", ".unsafe"), 0700)
			case "public directory":
				err = os.Chmod(filepath.Dir(target), 0755)
			case "generation":
				err = os.Mkdir(filepath.Join(root, "generations"), 0700)
			case "wrong input":
				data = []byte("wrong content")
			case "invalid archive":
				data = []byte("valid hash but not an archive")
				hash = archiveHash(data)
			case "invalid ID":
				repairID = strings.Repeat("A", 32)
			}
			if err != nil {
				test.Fatal(err)
			}
			outcome, err := invokeOfflineRepair(test.Context(), root, hash, repairID, data, defaultArchiveRepairOps())
			if err == nil || outcome != (ArchiveRepairResult{}) {
				test.Fatal("unsafe repair accepted", err, outcome)
			}
			entries, err := os.ReadDir(filepath.Join(root, "archives"))
			if err != nil {
				test.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".repair-") {
					test.Fatal("unsafe repair allocated a stage")
				}
			}
		})
	}
}

func TestOfflineRepairFailureBoundaries(test *testing.T) {
	for _, fault := range []string{"partial write", "sync1", "sync2", "sync3", "sync4", "close1", "close2", "close3", "rename unsupported", "rename applied error", "target replaced", "stage replaced", "cancel before publish", "post-sync target replaced", "post-sync retained replaced", "lock replaced"} {
		test.Run(fault, func(test *testing.T) {
			root, hash, data := offlineRepairFixture(test)
			target := filepath.Join(root, "archives", hash+".tar.gz")
			stage := filepath.Join(root, "archives", ".repair-"+hash+"-"+testRepairID+".tar.gz")
			before, err := os.Stat(target)
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			operations := defaultArchiveRepairOps()
			syncs, closes := 0, 0
			injected := errors.New("synthetic fault")
			operations.sync = func(file archiveRepairSyncer) error {
				syncs++
				if fault == "sync"+strconv.Itoa(syncs) {
					return injected
				}
				if syncs == 3 {
					switch fault {
					case "target replaced", "stage replaced":
						path := target
						if fault == "stage replaced" {
							path = stage
						}
						if err := os.Rename(path, path+".moved"); err != nil {
							return err
						}
						if err := os.WriteFile(path, data, 0600); err != nil {
							return err
						}
					case "cancel before publish":
						cancel()
					}
				}
				if syncs == 4 && (strings.HasPrefix(fault, "post-sync") || fault == "lock replaced") {
					path := target
					if fault == "post-sync retained replaced" {
						path = stage
					} else if fault == "lock replaced" {
						path = filepath.Join(root, "archives", ".cache.lock")
					}
					if err := os.Rename(path, path+".moved"); err != nil {
						return err
					}
					if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
						return err
					}
				}
				return file.Sync()
			}
			operations.close = func(file io.Closer) error {
				closes++
				closed := file.Close()
				if fault == "close"+strconv.Itoa(closes) {
					return errors.Join(closed, injected)
				}
				return closed
			}
			if fault == "partial write" {
				operations.write = func(_ context.Context, file afero.File, data []byte) error {
					_, err := file.Write(data[:len(data)/2])
					return errors.Join(err, injected)
				}
			}
			operations.rename = func(oldFD int, old string, newFD int, target string, flags uint) error {
				if fault == "rename unsupported" {
					return unix.ENOSYS
				}
				err := unix.Renameat2(oldFD, old, newFD, target, flags)
				if fault == "rename applied error" {
					return errors.Join(err, injected)
				}
				return err
			}
			outcome, err := invokeOfflineRepair(ctx, root, hash, testRepairID, data, operations)
			if !errors.Is(err, ErrArchiveRepairUncertain) || outcome != (ArchiveRepairResult{}) {
				test.Fatal("unconfirmed repair returned success", fault, outcome, err)
			}
			stageInfo, err := os.Stat(stage)
			if err != nil {
				test.Fatal("artifact was deleted", err)
			}
			published := fault == "sync4" || fault == "close3" || fault == "rename applied error" || strings.HasPrefix(fault, "post-sync") || fault == "lock replaced"
			actual, err := os.ReadFile(target)
			if err != nil {
				test.Fatal(err)
			}
			if published {
				if fault != "post-sync target replaced" && !bytes.Equal(actual, data) {
					test.Fatal("published fault lost new content")
				}
				if fault != "post-sync retained replaced" && !os.SameFile(before, stageInfo) {
					test.Fatal("published fault lost original or new content")
				}
			} else if fault != "target replaced" && !bytes.Equal(actual, bytes.Repeat([]byte("x"), len(data))) {
				test.Fatal("prepublication failure changed original target")
			}
		})
	}
}

func TestOfflineRepairMissingTargetDoesNotReplaceArrival(test *testing.T) {
	root, hash, data := offlineRepairFixture(test)
	target := filepath.Join(root, "archives", hash+".tar.gz")
	if err := os.Remove(target); err != nil {
		test.Fatal(err)
	}
	arrival := []byte("new arrival must not be overwritten")
	operations := defaultArchiveRepairOps()
	operations.rename = func(oldFD int, old string, newFD int, targetName string, flags uint) error {
		if flags != unix.RENAME_NOREPLACE {
			test.Fatal("missing target did not request NOREPLACE")
		}
		if err := os.WriteFile(target, arrival, 0600); err != nil {
			return err
		}
		return unix.Renameat2(oldFD, old, newFD, targetName, flags)
	}
	if _, err := invokeOfflineRepair(test.Context(), root, hash, testRepairID, data, operations); !errors.Is(err, ErrArchiveRepairUncertain) {
		test.Fatal("late arrival was not refused", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, arrival) {
		test.Fatal("late arrival was overwritten", err)
	}
	stage, err := os.ReadFile(filepath.Join(root, "archives", ".repair-"+hash+"-"+testRepairID+".tar.gz"))
	if err != nil || !bytes.Equal(stage, data) {
		test.Fatal("staged repair discarded", err)
	}
}

func TestOfflineRepairKilledProcess(test *testing.T) {
	root := os.Getenv("COGNATE_REPAIR_TEST_ROOT")
	if root == "" {
		test.Skip("subprocess helper")
	}
	data, err := ReadArchiveRepairInput(test.Context(), filepath.Join(root, "input.tar.gz"))
	if err != nil {
		test.Fatal(err)
	}
	defer clear(data)
	boundary := os.Getenv("COGNATE_REPAIR_TEST_BOUNDARY")
	waitForKill := func() {
		if _, err := io.WriteString(os.Stdout, "repair-barrier\n"); err != nil {
			test.Fatal(err)
		}
		time.Sleep(time.Hour)
	}
	operations := defaultArchiveRepairOps()
	if boundary == "partial" {
		operations.write = func(_ context.Context, file afero.File, data []byte) error {
			if _, err := file.Write(data[:len(data)/2]); err != nil {
				return err
			}
			waitForKill()
			return errors.New("kill barrier returned")
		}
	} else {
		operations.rename = func(oldFD int, old string, newFD int, target string, flags uint) error {
			if boundary == "before" {
				waitForKill()
			}
			if err := unix.Renameat2(oldFD, old, newFD, target, flags); err != nil {
				return err
			}
			waitForKill()
			return errors.New("kill barrier returned")
		}
	}
	_, err = invokeOfflineRepair(test.Context(), root, archiveHash(data), testRepairID, data, operations)
	test.Fatal("process survived barrier", err)
}

func TestOfflineRepairSIGKILLAndResume(test *testing.T) {
	for _, boundary := range []string{"partial", "before", "after"} {
		test.Run(boundary, func(test *testing.T) {
			root, hash, data := offlineRepairFixture(test)
			if err := os.WriteFile(filepath.Join(root, "input.tar.gz"), data, 0600); err != nil {
				test.Fatal(err)
			}
			target := filepath.Join(root, "archives", hash+".tar.gz")
			stage := filepath.Join(root, "archives", ".repair-"+hash+"-"+testRepairID+".tar.gz")
			before, err := os.Stat(target)
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOfflineRepairKilledProcess$")
			command.Env = append(os.Environ(), "COGNATE_REPAIR_TEST_ROOT="+root, "COGNATE_REPAIR_TEST_BOUNDARY="+boundary)
			output, err := command.StdoutPipe()
			if err != nil {
				test.Fatal(err)
			}
			var diagnostics bytes.Buffer
			command.Stderr = &diagnostics
			if err := command.Start(); err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = command.Process.Kill() })
			line, readError := bufio.NewReader(output).ReadString('\n')
			killError := command.Process.Kill()
			waitError := command.Wait()
			if readError != nil || line != "repair-barrier\n" || killError != nil {
				test.Fatalf("did not reach live barrier: %q %v %v %s", line, readError, killError, diagnostics.String())
			}
			exit, ok := errors.AsType[*exec.ExitError](waitError)
			if !ok {
				test.Fatal("child was not killed", waitError)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				test.Fatal("not a genuine SIGKILL", waitError)
			}
			owner, err := runtimegen.LockOffline(test.Context(), root)
			if err != nil {
				test.Fatal("lock not released after kill", err)
			}
			verifyError := VerifyOfflineArchive(test.Context(), owner, hash)
			if err := owner.Close(); err != nil {
				test.Fatal(err)
			}
			if (verifyError == nil) != (boundary == "after") {
				test.Fatal("incorrect post-kill target state", verifyError)
			}
			retained, err := os.ReadFile(stage)
			if err != nil {
				test.Fatal("stage lost after kill", err)
			}
			if boundary == "partial" {
				if !bytes.Equal(retained, data[:len(data)/2]) {
					test.Fatal("partial stage not retained")
				}
				if _, err := invokeOfflineRepair(test.Context(), root, hash, testRepairID, data, defaultArchiveRepairOps()); err == nil {
					test.Fatal("partial stage silently replayed")
				}
				if _, err := invokeOfflineRepair(test.Context(), root, hash, strings.Repeat("b", 32), data, defaultArchiveRepairOps()); err != nil {
					test.Fatal("new explicit repair could not proceed", err)
				}
				retainedAgain, err := os.ReadFile(stage)
				if err != nil || !bytes.Equal(retainedAgain, retained) {
					test.Fatal("new repair removed partial artifact", err)
				}
			} else {
				outcome, err := invokeOfflineRepair(test.Context(), root, hash, testRepairID, data, defaultArchiveRepairOps())
				if err != nil || !outcome.Retained || outcome.AlreadyVerified != (boundary == "after") {
					test.Fatal("same-ID resume failed", outcome, err)
				}
				old, err := os.Stat(stage)
				if err != nil || !os.SameFile(before, old) {
					test.Fatal("original inode not retained on resume", err)
				}
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, data) {
				test.Fatal("resumed target incorrect", err)
			}
		})
	}
}
