//go:build linux

package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"golang.org/x/sys/unix"
)

func TestRestoreRevokeProtectedProjection(test *testing.T) {
	fixture := restoreCredentialsSeed(test)
	ctx := test.Context()
	before, err := restoreRevokeProjection(ctx, fixture.pool, false, snapshotTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	tx := restoreCredentialsBegin(test, fixture.pool)
	when := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err = revokeRestoreCredentialsTx(ctx, tx, when); err != nil {
		test.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		test.Fatal(err)
	}
	after, err := restoreRevokeProjection(ctx, fixture.pool, true, snapshotTestLimits())
	if err != nil || before != after {
		test.Fatal("allowed writes changed protected projection", err)
	}
	tx = restoreCredentialsBegin(test, fixture.pool)
	if err = verifyRestoreCredentialsTx(ctx, tx, when); err != nil {
		test.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		test.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE mcp_credentials SET revision=revision+1",
		"UPDATE git_hub_credentials SET generation='different'",
		"UPDATE configuration_assets SET ciphertext=x'010203'",
		"UPDATE users SET user_key=user_key||'-changed'",
		"UPDATE sqlite_sequence SET seq=seq+1",
	} {
		test.Run(statement[:min(len(statement), 32)], func(test *testing.T) {
			tx := restoreCredentialsBegin(test, fixture.pool)
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				test.Fatal(err)
			}
			changed, err := restoreRevokeProjection(ctx, tx, true, snapshotTestLimits())
			if err != nil || changed == before {
				test.Fatal("protected write was invisible", err)
			}
			if err := tx.Rollback(); err != nil {
				test.Fatal(err)
			}
		})
	}
}

func restoreRevokeTestDispatch(args []string) (bool, int) {
	if len(args) == 2 && args[1] == "--t4-lock-probe" {
		for _, exclusive := range []bool{false, true} {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			var err error
			if exclusive {
				var owner *runtimegen.OfflineRoot
				owner, err = runtimegen.LockOffline(ctx, os.Getenv("T4_TEST_ROOT"))
				if owner != nil {
					_ = owner.Close()
				}
			} else {
				var owner *runtimegen.Runtime
				owner, err = runtimegen.OpenRuntime(ctx, os.Getenv("T4_TEST_ROOT"))
				if owner != nil {
					_ = owner.Close()
				}
			}
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				return true, 2
			}
		}
		fmt.Println("shared_blocked=true exclusive_blocked=true")
		return true, 0
	}
	if len(args) == 2 && args[1] == "--t4-network-negative" {
		return true, restoreRevokeNetworkChild()
	}
	if len(args) == 2 && args[1] == "--t4-parent-death" {
		return true, restoreRevokeParentChild()
	}
	if len(args) == 2 && args[1] == restoreRevokeWorkerMode {
		if barrier := os.Getenv("T4_TEST_BARRIER"); barrier != "" {
			if os.WriteFile(barrier+".ready", []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
				return true, 2
			}
			deadline := time.Now().Add(8 * time.Second)
			for {
				if _, err := os.Stat(barrier + ".release"); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return true, 2
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		fault := os.Getenv("T4_TEST_FAULT")
		ops := restoreRevokeSQL{}
		if fault == "cpu-budget" || fault == "fd-budget" || fault == "journal-budget" {
			ops.open = func(address string) (*sql.DB, error) {
				if fault == "cpu-budget" {
					for {
						runtime.Gosched()
					}
				}
				trace, err := os.OpenFile(os.Getenv("T4_TEST_TRACE"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, ErrRestoreInvalid
				}
				retainSnapshotResource(trace)
				if fault == "journal-budget" {
					if unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: 1024, Max: 1024}) != nil {
						return nil, ErrRestoreInvalid
					}
				} else {
					for {
						_, err := unix.Open(os.DevNull, unix.O_RDWR|unix.O_CLOEXEC, 0)
						if errors.Is(err, unix.EMFILE) {
							break
						}
						if err != nil {
							return nil, ErrRestoreInvalid
						}
					}
				}
				if _, err := trace.WriteString("reached"); err != nil {
					return nil, ErrRestoreUncertain
				}
				return sql.Open("sqlite3", address)
			}
		}
		if fault == "commit" {
			ops.commit = func(tx *sql.Tx) error {
				if err := tx.Commit(); err != nil {
					return err
				}
				return ErrRestoreUncertain
			}
		}
		if fault == "pool" {
			ops.closePool = func(pool *sql.DB) error {
				if err := pool.Close(); err != nil {
					return err
				}
				return ErrRestoreUncertain
			}
		}
		if fault == "connection" {
			ops.closeConn = func(conn *sql.Conn) error {
				if err := conn.Close(); err != nil {
					return err
				}
				return ErrRestoreUncertain
			}
		}
		if fault == "fd-close" || fault == "response-close" {
			ops.closeFile = func(file *os.File) error {
				err := file.Close()
				if err != nil {
					return err
				}
				if fault == "fd-close" || file.Name() == "restore revoke response" {
					return ErrRestoreUncertain
				}
				return nil
			}
		}
		if strings.HasPrefix(fault, "statement-") || fault == "rollback" || fault == "read-close" || fault == "read-before" {
			ops.open = func(address string) (*sql.DB, error) {
				base, err := sql.Open("sqlite3", address)
				if err != nil {
					return nil, err
				}
				driver := base.Driver()
				if err := base.Close(); err != nil {
					return nil, err
				}
				faults := &restoreCredentialsFaults{}
				if fault == "read-before" {
					faults.readFail = func(position int) error {
						if position == 1 {
							return ErrRestoreInvalid
						}
						return nil
					}
				}
				position, _ := strconv.Atoi(strings.TrimPrefix(fault, "statement-"))
				if fault == "rollback" {
					position = 8
				}
				if position > 0 {
					faults.afterExec = func(step int) error {
						if trace := os.Getenv("T4_TEST_TRACE"); trace != "" {
							if err := os.WriteFile(trace, []byte(strconv.Itoa(step)), 0600); err != nil {
								return ErrRestoreUncertain
							}
						}
						if step == position {
							return ErrRestoreInvalid
						}
						return nil
					}
				}
				if fault == "read-close" {
					faults.closeFail = func(int) error {
						if faults.execs.Load() == 8 {
							return ErrRestoreUncertain
						}
						return nil
					}
				}
				return sql.OpenDB(restoreCredentialsConnector{base: driver, name: address, faults: faults}), nil
			}
			if fault == "rollback" {
				ops.rollback = func(tx *sql.Tx) error {
					if err := tx.Rollback(); err != nil {
						return err
					}
					return ErrRestoreUncertain
				}
			}
		}
		return true, restoreRevokeWorkerServe(ops)
	}
	return false, 0
}

func restoreRevokeParentChild() int {
	rootPath, path, barrier := os.Getenv("T4_TEST_ROOT"), os.Getenv("T4_TEST_CANDIDATE"), os.Getenv("T4_TEST_BARRIER")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, err := runtimegen.LockOffline(ctx, rootPath)
	if err != nil {
		return 2
	}
	parent, err := os.Open(path)
	if err != nil {
		return 2
	}
	database, err := os.Open(filepath.Join(path, "server.db"))
	if err != nil {
		return 2
	}
	_, err = restoreRevokeWithIO(ctx, root, parent, database, bytes.Repeat([]byte{91}, 32), RestoreDatabaseEvidence{}, [16]byte{}, time.Time{}, snapshotTestLimits(), restoreRevokeIO{prepare: func(command *exec.Cmd) {
		command.SysProcAttr.Pdeathsig = 0
		command.Env = append(command.Env, "T4_TEST_BARRIER="+barrier)
	}})
	if err != nil {
		return 1
	}
	return 0
}

func TestRestoreRevokeParentDeathSameOFD(test *testing.T) {
	seed := restoreFixture(test, 0, 0)
	data, err := os.ReadFile(seed.database.Name())
	if err != nil {
		test.Fatal(err)
	}
	fixture := newRevokeFixture(test, data, seed.key)
	if err := fixture.root.Close(); err != nil {
		test.Fatal(err)
	}
	barrier := filepath.Join(test.TempDir(), "worker")
	command := exec.Command(os.Args[0], "--t4-parent-death")
	command.Env = []string{"LANG=C", "LC_ALL=C", "T4_TEST_ROOT=" + fixture.path, "T4_TEST_CANDIDATE=" + fixture.parent.Name(), "T4_TEST_BARRIER=" + barrier}
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	deadline := time.Now().Add(5 * time.Second)
	var worker int
	for {
		ready, err := os.ReadFile(barrier + ".ready")
		if err == nil {
			worker, err = strconv.Atoi(string(ready))
			if err != nil {
				test.Fatal("worker readiness class")
			}
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("worker readiness timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	pidfd, err := unix.PidfdOpen(worker, 0)
	if err != nil {
		test.Fatal("worker pidfd", err)
	}
	defer unix.Close(pidfd)
	defer func() { _ = unix.PidfdSendSignal(pidfd, syscall.SIGKILL, nil, 0) }()
	if err := command.Process.Kill(); err != nil {
		test.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		test.Fatal("parent not killed")
	}
	for _, exclusive := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(test.Context(), 80*time.Millisecond)
		if exclusive {
			other, err := runtimegen.LockOffline(ctx, fixture.path)
			if other != nil {
				_ = other.Close()
				test.Fatal("orphan lost EX")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				test.Fatal(err)
			}
		} else {
			other, err := runtimegen.OpenRuntime(ctx, fixture.path)
			if other != nil {
				_ = other.Close()
				test.Fatal("orphan admitted SH")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				test.Fatal(err)
			}
		}
		cancel()
	}
	if err := os.WriteFile(barrier+".release", nil, 0600); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	other, err := runtimegen.LockOffline(ctx, fixture.path)
	if err != nil {
		test.Fatal("worker did not release actual OFD", err)
	}
	if err := other.Close(); err != nil {
		test.Fatal(err)
	}
	poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if count, err := unix.Poll(poll, 5000); err != nil || count != 1 {
		test.Fatal("worker exit not observed", err)
	}
	test.Log("parent_killed=true inherited_same_ofd_ex_held=true child_exit_observed=true")
}

func TestRestoreRevokeAdmissionAndRevisionBounds(test *testing.T) {
	seed := restoreCredentialsSeed(test)
	if err := errors.Join(seed.pool.Close(), seed.native.store.Close()); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(seed.native.path)
	if err != nil {
		test.Fatal(err)
	}
	for _, scenario := range []string{"wrong-operation", "foreign-parent", "sidecar-journal", "sidecar-wal", "sidecar-shm", "hardlink", "symlink", "mode", "revision-max", "revision-one-below", "projection-budget"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newRevokeFixture(test, data, seed.native.key)
			operation := [16]byte{1}
			parent := fixture.parent
			limits := snapshotTestLimits()
			if scenario == "wrong-operation" {
				operation[1] = 1
			}
			if scenario == "foreign-parent" {
				parent, err = os.Open(test.TempDir())
				if err != nil {
					test.Fatal(err)
				}
				defer parent.Close()
			}
			if strings.HasPrefix(scenario, "sidecar-") {
				if err := os.WriteFile(fixture.database.Name()+"-"+strings.TrimPrefix(scenario, "sidecar-"), nil, 0600); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "hardlink" {
				if err := os.Link(fixture.database.Name(), filepath.Join(test.TempDir(), "link")); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Rename(fixture.database.Name(), fixture.database.Name()+"-saved"); err != nil {
					test.Fatal(err)
				}
				if err := os.Symlink(fixture.database.Name()+"-saved", fixture.database.Name()); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "mode" {
				if err := os.Chmod(fixture.database.Name(), 0644); err != nil {
					test.Fatal(err)
				}
			}
			if strings.HasPrefix(scenario, "revision-") {
				pool, err := sql.Open("sqlite3", snapshotAddress(fixture.database.Name(), "rw", "journal_mode(DELETE)"))
				if err != nil {
					test.Fatal(err)
				}
				value := "9223372036854775807"
				if scenario == "revision-one-below" {
					value = "9223372036854775806"
				}
				if _, err := pool.ExecContext(test.Context(), "UPDATE mcp_credentials SET revision="+value); err != nil {
					test.Fatal(err)
				}
				if err := pool.Close(); err != nil {
					test.Fatal(err)
				}
				fixture.before, err = InspectRestoreCandidateOffline(test.Context(), fixture.root, fixture.parent, fixture.database, fixture.key, limits)
				if err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "projection-budget" {
				limits.MaxRecords = 16
			}
			facts, err := RevokeRestoreCandidate(test.Context(), fixture.root, parent, fixture.database, fixture.key, fixture.before, operation, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), limits)
			if scenario == "revision-one-below" {
				if err != nil || !facts.Database().Valid() {
					test.Fatal("last safe revision refused", err)
				}
				return
			}
			if err == nil || facts.Database().Valid() {
				test.Fatal("unsafe candidate admitted", scenario)
			}
		})
	}
}

func TestRestoreRevokeStagingReserve(test *testing.T) {
	seed := restoreFixture(test, 0, 0)
	data, err := os.ReadFile(seed.database.Name())
	if err != nil {
		test.Fatal(err)
	}
	fixture := newRevokeFixture(test, data, seed.key)
	limits := snapshotTestLimits()
	limits.MaxStagingBytes = 3*limits.MaxDBBytes + 65535
	facts, err := RevokeRestoreCandidate(test.Context(), fixture.root, fixture.parent, fixture.database, fixture.key, fixture.before, [16]byte{1}, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), limits)
	if !errors.Is(err, ErrRestoreBudget) || facts.Database().Valid() {
		test.Fatal("Dmax + Jmax reserve was not enforced", err)
	}
}

func restoreRevokeNetworkChild() int {
	const workers = 8
	ready := make(chan struct{}, workers)
	start := make(chan struct{})
	results := make(chan bool, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			descriptor, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				ready <- struct{}{}
				<-start
				results <- false
				return
			}
			defer unix.Close(descriptor)
			ready <- struct{}{}
			<-start
			fresh, socketError := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
			if fresh >= 0 {
				_ = unix.Close(fresh)
			}
			connectError := unix.Connect(descriptor, &unix.SockaddrInet4{Port: 9, Addr: [4]byte{127, 0, 0, 1}})
			privileges, privilegeError := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
			results <- errors.Is(socketError, unix.EPERM) && errors.Is(connectError, unix.EPERM) && privilegeError == nil && privileges == 1
		})
	}
	for range workers {
		<-ready
	}
	if restoreRevokeSandbox() != nil {
		return 2
	}
	close(start)
	group.Wait()
	for range workers {
		if !<-results {
			return 3
		}
	}
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return 4
	}
	for _, entry := range entries {
		status, err := os.ReadFile("/proc/self/task/" + entry.Name() + "/status")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !strings.Contains(string(status), "NoNewPrivs:\t1") || !strings.Contains(string(status), "Seccomp:\t2") {
			return 5
		}
	}
	fmt.Println("socket_denied=8 connect_denied=8 all_threads_restricted=true")
	return 0
}

func TestRestoreRevokeNetworkAllThreads(test *testing.T) {
	command := exec.CommandContext(test.Context(), os.Args[0], "--t4-network-negative")
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "socket_denied=8 connect_denied=8 all_threads_restricted=true\n" {
		test.Fatal("actual network negative control failed", err)
	}
	test.Log(strings.TrimSpace(string(output)))
}

type revokeFixture struct {
	root             *runtimegen.OfflineRoot
	path             string
	parent, database *os.File
	key              []byte
	before           RestoreDatabaseEvidence
}

func newRevokeFixture(test *testing.T, data, key []byte) revokeFixture {
	test.Helper()
	rootPath := test.TempDir()
	if err := os.Chmod(rootPath, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(rootPath, ".restore-01000000000000000000000000000000", "candidate")
	if err := os.MkdirAll(path, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "server.db"), data, 0600); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	database, err := os.Open(filepath.Join(path, "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	root, err := runtimegen.LockOffline(test.Context(), rootPath)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close(); _ = parent.Close(); _ = root.Close() })
	before, err := InspectRestoreCandidateOffline(test.Context(), root, parent, database, key, snapshotTestLimits())
	if err != nil {
		test.Fatal("fixture EX inspector", err)
	}
	return revokeFixture{root, rootPath, parent, database, key, before}
}

func TestRestoreRevokeActualWorkerFaults(test *testing.T) {
	seed := restoreCredentialsSeed(test)
	beforeRows := restoreCredentialsSnapshot(test, seed.pool)
	if err := errors.Join(seed.pool.Close(), seed.native.store.Close()); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(seed.native.path)
	if err != nil {
		test.Fatal(err)
	}
	for _, fault := range []string{"statement-1", "statement-2", "statement-3", "statement-4", "statement-5", "statement-6", "statement-7", "statement-8", "rollback", "read-close", "read-before", "commit", "connection", "pool", "fd-close", "response-close", "wait", "close", "after-start-close", "guard-close", "sync", "lost-result", "canceled-unknown"} {
		test.Run(fault, func(test *testing.T) {
			fixture := newRevokeFixture(test, data, seed.native.key)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			trace := filepath.Join(test.TempDir(), "trace")
			ops := restoreRevokeIO{prepare: func(command *exec.Cmd) {
				command.Env = append(command.Env, "T4_TEST_FAULT="+fault, "T4_TEST_TRACE="+trace)
			}}
			if fault == "wait" {
				ops.wait = func(command *exec.Cmd) error { _ = command.Wait(); return ErrRestoreUncertain }
			}
			if fault == "close" {
				ops.close = func(file *os.File) error { _ = file.Close(); return ErrRestoreUncertain }
			}
			if fault == "guard-close" {
				ops.close = func(file *os.File) error {
					err := file.Close()
					if err != nil {
						return err
					}
					if file.Name() == "runtime coordination" {
						return ErrRestoreUncertain
					}
					return nil
				}
			}
			if fault == "after-start-close" {
				var count int
				ops.close = func(file *os.File) error {
					count++
					err := file.Close()
					if err != nil {
						return err
					}
					if count == 3 {
						return ErrRestoreUncertain
					}
					return nil
				}
			}
			if fault == "sync" {
				ops.sync = func(file *os.File) error { _ = file.Sync(); return ErrRestoreUncertain }
			}
			if fault == "lost-result" || fault == "canceled-unknown" {
				ops.read = func(reader io.Reader, maximum int64, output any) error {
					err := restoreReadMessage(reader, maximum, output)
					if fault == "canceled-unknown" {
						cancel()
					}
					if err != nil {
						return err
					}
					return ErrRestoreUncertain
				}
			}
			facts, err := restoreRevokeWithIO(ctx, fixture.root, fixture.parent, fixture.database, fixture.key, fixture.before, [16]byte{1}, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), snapshotTestLimits(), ops)
			if !errors.Is(err, ErrRestoreUncertain) || facts.Database().Valid() {
				test.Fatal("uncertainty minted facts or was masked", err)
			}
			observer, err := sql.Open("sqlite3", snapshotAddress(fixture.database.Name(), "ro", "query_only(1)"))
			if err != nil {
				test.Fatal(err)
			}
			afterRows := restoreCredentialsSnapshot(test, observer)
			if err := observer.Close(); err != nil {
				test.Fatal(err)
			}
			rolledBack := strings.HasPrefix(fault, "statement-") || fault == "rollback" || fault == "read-close" || fault == "read-before" || fault == "close"
			if reflect.DeepEqual(beforeRows, afterRows) != rolledBack {
				test.Fatal("actual rollback/commit disagrees with independently reopened rows")
			}
			if strings.HasPrefix(fault, "statement-") || fault == "rollback" {
				count, err := os.ReadFile(trace)
				expected := strings.TrimPrefix(fault, "statement-")
				if fault == "rollback" {
					expected = "8"
				}
				if err != nil || string(count) != expected {
					test.Fatal("actual DML boundary not reached", err)
				}
			}
			if err := fixture.root.Close(); fault == "guard-close" && !errors.Is(err, runtimegen.ErrUncertain) || fault != "guard-close" && err != nil {
				test.Fatal("original EX close", err)
			}
			probe, stop := context.WithTimeout(test.Context(), 60*time.Millisecond)
			defer stop()
			other, err := runtimegen.LockOffline(probe, fixture.path)
			if other != nil {
				_ = other.Close()
				test.Fatal("uncertainty released actual independent guard")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				test.Fatal("guard contention class", err)
			}
			if fault == "guard-close" || fault == "commit" || fault == "canceled-unknown" {
				command := exec.CommandContext(test.Context(), os.Args[0], "--t4-lock-probe")
				command.Env = []string{"LANG=C", "LC_ALL=C", "T4_TEST_ROOT=" + fixture.path}
				output, err := command.CombinedOutput()
				if err != nil || string(output) != "shared_blocked=true exclusive_blocked=true\n" {
					test.Fatal("fresh process was not excluded after uncertainty", err)
				}
			}
		})
	}
}

func TestRestoreRevokeResourceAndCancellation(test *testing.T) {
	seed := restoreFixture(test, 2, 2)
	data, err := os.ReadFile(seed.database.Name())
	if err != nil {
		test.Fatal("fixture class")
	}
	for _, budget := range []string{"database", "staging", "records", "heap", "address-space", "cpu-budget", "fd-budget", "journal-budget", "cancel-running"} {
		test.Run(budget, func(test *testing.T) {
			fixture := newRevokeFixture(test, data, seed.key)
			limits := snapshotTestLimits()
			switch budget {
			case "database":
				limits.MaxDBBytes = 1
			case "staging":
				limits.MaxStagingBytes = 1
			case "records":
				limits.MaxRecords = 1
			case "heap":
				limits.SQLiteHeapBytes = 65536
			case "address-space":
				limits.WorkerAddressSpaceBytes = limits.SQLiteHeapBytes + 1
			case "cpu-budget":
				limits.WorkerCPUSeconds = 1
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			trace := filepath.Join(test.TempDir(), "resource-trace")
			barrier := filepath.Join(test.TempDir(), "running")
			waited, killedByCPU, cancellationReached := 0, false, false
			ops := restoreRevokeIO{prepare: func(command *exec.Cmd) {
				command.Env = append(command.Env, "T4_TEST_FAULT="+budget, "T4_TEST_TRACE="+trace)
				if budget == "cancel-running" {
					command.Env = append(command.Env, "T4_TEST_BARRIER="+barrier)
				}
			}, wait: func(command *exec.Cmd) error {
				err := command.Wait()
				waited++
				if command.ProcessState != nil {
					status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
					killedByCPU = ok && status.Signaled() && status.Signal() == syscall.SIGKILL && command.ProcessState.UserTime()+command.ProcessState.SystemTime() >= 900*time.Millisecond
				}
				return err
			}}
			if budget == "cancel-running" {
				ops.read = func(reader io.Reader, maximum int64, output any) error {
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(barrier + ".ready"); err == nil {
							cancellationReached = true
							break
						}
						time.Sleep(time.Millisecond)
					}
					cancel()
					return restoreReadMessage(reader, maximum, output)
				}
			}
			facts, err := restoreRevokeWithIO(ctx, fixture.root, fixture.parent, fixture.database, fixture.key, fixture.before, [16]byte{1}, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), limits, ops)
			if err == nil || facts.Database().Valid() {
				test.Fatal("resource failure minted facts")
			}
			if budget == "cpu-budget" && (!killedByCPU || waited != 1) || budget == "cancel-running" && (!cancellationReached || waited != 1 || !errors.Is(err, ErrRestoreUncertain)) {
				test.Fatal("actual process limit or cancellation did not drain")
			}
			if budget == "fd-budget" || budget == "journal-budget" {
				value, err := os.ReadFile(trace)
				if err != nil || string(value) != "reached" || waited != 1 {
					test.Fatal("actual worker resource gate not reached")
				}
			}
			test.Logf("no_facts=true process_waits=%d cpu_limit_killed=%t running_cancel_joined=%t", waited, killedByCPU, cancellationReached && waited == 1)
		})
	}
}

func TestRestoreRevokeGuardEMFILE(test *testing.T) {
	if os.Getenv("T4_WORKER_RETENTION_CHILD") != "1" {
		command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestRestoreRevokeGuardEMFILE$", "-test.v")
		command.Env = []string{"LANG=C", "LC_ALL=C", "T4_WORKER_RETENTION_CHILD=1", "TMPDIR=" + os.Getenv("TMPDIR")}
		output, err := command.CombinedOutput()
		test.Log(string(output))
		if err != nil {
			test.Fatal("worker actual EMFILE gate")
		}
		return
	}
	seed := restoreFixture(test, 0, 0)
	data, err := os.ReadFile(seed.database.Name())
	if err != nil {
		test.Fatal("fixture class")
	}
	fixture := newRevokeFixture(test, data, seed.key)
	if unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: 64, Max: 64}) != nil {
		test.Fatal("resource gate class")
	}
	var fill []int
	exhausted, auxiliaryClosed := false, false
	ops := restoreRevokeIO{close: func(file *os.File) error {
		if file.Close() != nil {
			return ErrRestoreUncertain
		}
		if file.Name() != "runtime coordination" {
			return nil
		}
		auxiliaryClosed = true
		for {
			descriptor, err := unix.Open(os.DevNull, unix.O_RDWR|unix.O_CLOEXEC, 0)
			if errors.Is(err, unix.EMFILE) {
				exhausted = true
				break
			}
			if err != nil {
				return ErrRestoreUncertain
			}
			fill = append(fill, descriptor)
		}
		return ErrRestoreUncertain
	}}
	facts, err := restoreRevokeWithIO(test.Context(), fixture.root, fixture.parent, fixture.database, fixture.key, fixture.before, [16]byte{1}, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), snapshotTestLimits(), ops)
	for _, descriptor := range fill {
		if unix.Close(descriptor) != nil {
			test.Fatal("filler close class")
		}
	}
	closeError := fixture.root.Close()
	command := exec.CommandContext(test.Context(), os.Args[0], "--t4-lock-probe")
	command.Env = []string{"LANG=C", "LC_ALL=C", "T4_TEST_ROOT=" + fixture.path}
	output, probeError := command.CombinedOutput()
	blocked := probeError == nil && string(output) == "shared_blocked=true exclusive_blocked=true\n"
	test.Logf("real_emfile=%t auxiliary_physically_closed=%t no_facts=%t root_close_uncertain=%t fresh_process_blocked=%t", exhausted, auxiliaryClosed, !facts.Database().Valid(), errors.Is(closeError, runtimegen.ErrUncertain), blocked)
	if !exhausted || !auxiliaryClosed || facts.Database().Valid() || !errors.Is(err, ErrRestoreUncertain) || !errors.Is(closeError, runtimegen.ErrUncertain) || !blocked {
		test.Fatal("worker quarantine gate")
	}
}

func TestRestoreRevokeAllOwnersAndOldAuthority(test *testing.T) {
	seed := restoreCredentialsSeed(test)
	store := seed.native.store
	credentials, err := store.state.client.GitHubCredential.Query().WithUser().All(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	credential, err := store.Credential(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := store.BeginListSync(test.Context(), "alice", credential.Generation)
	if err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(seed.pool.Close(), store.Close()); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(seed.native.path)
	if err != nil {
		test.Fatal(err)
	}
	fixture := newRevokeFixture(test, data, seed.native.key)
	when := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	facts, err := RevokeRestoreCandidate(test.Context(), fixture.root, fixture.parent, fixture.database, fixture.key, fixture.before, [16]byte{1}, when, snapshotTestLimits())
	if err != nil || !facts.Database().Valid() {
		test.Fatal("real all-owner worker", err)
	}
	pool, err := sql.Open("sqlite3", snapshotAddress(fixture.database.Name(), "ro", "foreign_keys(1)", "query_only(1)"))
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	store.state.client = ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", pool)))
	for _, previous := range credentials {
		current, err := store.state.client.GitHubCredential.Get(test.Context(), previous.ID)
		if err != nil || current.Generation != previous.Generation || current.KeyID != previous.KeyID || current.ViewerID != previous.ViewerID || current.ClientID != previous.ClientID || !bytes.Equal(current.Ciphertext, previous.Ciphertext) {
			test.Fatal("AAD or ciphertext changed", err)
		}
		if _, err := store.decryptCredential(previous.Edges.User.UserKey, current); err != nil {
			test.Fatal("original AAD lost", err)
		}
		if _, err := store.Credential(test.Context(), previous.Edges.User.UserKey); !errors.Is(err, ErrCredentialUnavailable) {
			test.Fatal("old GitHub ready authority survived", err)
		}
	}
	for _, asset := range seed.assets {
		payload, err := store.ReadConfigurationAsset(test.Context(), asset.owner, asset.id)
		if err != nil || !bytes.Equal(payload.Reveal(), []byte("synthetic-configuration-"+asset.owner)) {
			test.Fatal("configuration ciphertext changed", err)
		}
	}
	for _, parent := range seed.parents {
		if _, _, err := store.SessionUser(test.Context(), parent[:], store.state.now()); !errors.Is(err, ErrSessionInvalid) {
			test.Fatal("old session survived", err)
		}
	}
	for _, child := range seed.children {
		if _, err := store.AuthenticateNativeConsumer(test.Context(), child.credential, "https://cognate.example", child.operation); !errors.Is(err, ErrNativeUnauthorized) {
			test.Fatal("old terminal native authority survived", err)
		}
	}
	for _, share := range seed.shares {
		if _, err := store.SharedPack(test.Context(), share.id, share.hash); !errors.Is(err, ErrPackShareUnavailable) {
			test.Fatal("share authority survived", err)
		}
	}
	for _, hash := range seed.mcpHashes {
		if _, err := store.AuthorizeMCP(test.Context(), hash, MCPCatalogRead); !errors.Is(err, ErrMCPUnauthorized) {
			test.Fatal("MCP authority survived", err)
		}
	}
	if _, err := store.ReadListSync(test.Context(), ticket, nil); !errors.Is(err, ErrSyncSuperseded) {
		test.Fatal("old refresh ticket survived", err)
	}
	var wrong int
	if pool.QueryRowContext(test.Context(), "SELECT count(*) FROM mcp_credentials WHERE revision<>2").Scan(&wrong) != nil || wrong != 0 {
		test.Fatal("MCP did not advance exactly once")
	}
}

var _ interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
} = (*sql.Tx)(nil)

func TestRestoreRevokeOfflineTransaction(test *testing.T) {
	fixture := restoreFixture(test, 2, 2)
	rootPath := test.TempDir()
	if err := os.Chmod(rootPath, 0700); err != nil {
		test.Fatal(err)
	}
	operation := [16]byte{1}
	path := filepath.Join(rootPath, ".restore-01000000000000000000000000000000", "candidate")
	if err := os.MkdirAll(path, 0700); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(fixture.database.Name())
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "server.db"), data, 0600); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer parent.Close()
	database, err := os.Open(filepath.Join(path, "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	root, err := runtimegen.LockOffline(test.Context(), rootPath)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	if err := root.WithWorkerFiles(test.Context(), func(rootFile, lock *os.File) error {
		return restoreRevokeNamespace(rootFile, lock, parent, database, operation)
	}); err != nil {
		test.Fatal("namespace admission", err)
	}
	before, err := InspectRestoreCandidateOffline(test.Context(), root, parent, database, fixture.key, snapshotTestLimits())
	if err != nil || !before.Valid() {
		test.Fatal("EX inspect", err)
	}
	when := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	facts, err := RevokeRestoreCandidate(test.Context(), root, parent, database, fixture.key, before, operation, when, snapshotTestLimits())
	if err != nil || !facts.Database().Valid() || facts.ProtectedSHA256() == [32]byte{} {
		test.Fatal("EX revoke", err)
	}
	if facts.Database().KeyIdentity() != before.KeyIdentity() || facts.Database().SchemaFingerprint() != before.SchemaFingerprint() {
		test.Fatal("identity changed")
	}
	after, err := os.ReadFile(database.Name())
	if err != nil || bytes.Equal(data, after) {
		test.Fatal("no real mutation", err)
	}
	if _, err := RevokeRestoreCandidate(test.Context(), root, parent, database, fixture.key, before, operation, when, snapshotTestLimits()); !errors.Is(err, ErrRestoreInvalid) {
		test.Fatal("stale source facts replayed", err)
	}
}
