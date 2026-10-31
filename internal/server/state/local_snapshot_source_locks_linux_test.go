//go:build linux

package state

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
)

type snapshotLockProbeResult struct {
	MainLocked      bool `json:"mainLocked"`
	SHMLocked       bool `json:"shmLocked"`
	Truncated       bool `json:"truncated"`
	TruncateBlocked bool `json:"truncateBlocked"`
}

func TestLocalSnapshotLockProbeProcess(test *testing.T) {
	mode := os.Getenv("COGNATE_SNAPSHOT_LOCK_PROBE")
	if mode == "" {
		return
	}
	database := os.Getenv("COGNATE_SNAPSHOT_LOCK_PATH")
	result := snapshotLockProbeResult{}
	for index, suffix := range []string{"", "-shm"} {
		file, err := os.OpenFile(database+suffix, os.O_RDWR, 0)
		if err != nil {
			os.Exit(3)
		}
		start, length := int64(1073741826), int64(510)
		if index == 1 {
			start, length = 128, 1
		}
		lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: start, Len: length}
		if unix.FcntlFlock(file.Fd(), unix.F_GETLK, &lock) != nil {
			os.Exit(4)
		}
		if index == 0 {
			result.MainLocked = lock.Type != unix.F_UNLCK
		} else {
			result.SHMLocked = lock.Type != unix.F_UNLCK
			if mode == "truncate" {
				lock.Type = unix.F_WRLCK
				err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &lock)
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EACCES) {
					result.TruncateBlocked = true
				} else if err != nil || file.Truncate(3) != nil {
					os.Exit(5)
				} else {
					result.Truncated = true
				}
			}
		}
		if file.Close() != nil {
			os.Exit(6)
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		os.Exit(7)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
}

func snapshotProbeLocks(test *testing.T, database, mode string) snapshotLockProbeResult {
	test.Helper()
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestLocalSnapshotLockProbeProcess$")
	command.Env = append(os.Environ(), "COGNATE_SNAPSHOT_LOCK_PROBE="+mode, "COGNATE_SNAPSHOT_LOCK_PATH="+database)
	data, err := command.Output()
	if err != nil {
		test.Fatal("independent lock probe failed")
	}
	var result snapshotLockProbeResult
	if json.Unmarshal(data, &result) != nil {
		test.Fatal("independent lock probe response invalid")
	}
	return result
}

func snapshotLiveLockSource(test *testing.T) (string, *sql.DB, *localSnapshotSource) {
	test.Helper()
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private fixture directory unavailable")
	}
	path := filepath.Join(directory, "live.sqlite")
	pool, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal("live SQLite fixture unavailable")
	}
	pool.SetMaxOpenConns(1)
	test.Cleanup(func() { _ = pool.Close() })
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE lock_probe (value INTEGER NOT NULL)", "INSERT INTO lock_probe VALUES (1)",
		"PRAGMA wal_checkpoint(TRUNCATE)", "UPDATE lock_probe SET value=2",
	} {
		if _, err := pool.ExecContext(test.Context(), statement); err != nil {
			test.Fatal("live SQLite fixture initialization failed")
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if os.Chmod(path+suffix, 0600) != nil {
			test.Fatal("private SQLite fixture file unavailable")
		}
	}
	var value int
	if pool.QueryRowContext(test.Context(), "SELECT value FROM lock_probe").Scan(&value) != nil || value != 2 {
		test.Fatal("live SQLite baseline query failed")
	}
	source, err := retainLocalSnapshotSource(path)
	if err != nil {
		test.Fatal("source retention failed")
	}
	test.Cleanup(func() { _ = source.close() })
	before := snapshotProbeLocks(test, path, "inspect")
	if !before.MainLocked || !before.SHMLocked {
		test.Fatalf("baseline locks missing main=%t shm=%t", before.MainLocked, before.SHMLocked)
	}
	return path, pool, source
}

func TestLocalSnapshotSourcePreservesSQLiteLocks(test *testing.T) {
	for _, phase := range []string{"admission", "source-close"} {
		test.Run(phase, func(test *testing.T) {
			path, _, source := snapshotLiveLockSource(test)
			var err error
			if phase == "admission" {
				err = source.inspect(test.Context(), snapshotTestLimits())
			} else {
				err = source.close()
			}
			if err != nil {
				test.Fatal("source operation failed")
			}
			after := snapshotProbeLocks(test, path, "inspect")
			test.Logf("stage=%s main_locked=%t shm_locked=%t", phase, after.MainLocked, after.SHMLocked)
			if !after.MainLocked || !after.SHMLocked {
				test.Fatal("source operation released live SQLite process locks")
			}
		})
	}
}

func TestLocalSnapshotAdmissionProtectsLiveMapping(test *testing.T) {
	path, pool, source := snapshotLiveLockSource(test)
	if source.inspect(test.Context(), snapshotTestLimits()) != nil {
		test.Fatal("source admission failed")
	}
	result := snapshotProbeLocks(test, path, "truncate")
	var value int
	err := pool.QueryRowContext(test.Context(), "SELECT value FROM lock_probe").Scan(&value)
	code := 0
	if failure, ok := errors.AsType[*sqlite.Error](err); ok {
		code = failure.Code()
	}
	test.Logf("stage=dms_guarded_truncate truncate_bytes=3 blocked=%t truncated=%t sqlite_code=%d", result.TruncateBlocked, result.Truncated, code)
	if !result.TruncateBlocked || result.Truncated || err != nil || value != 2 {
		test.Fatal("source admission did not protect live WAL mapping")
	}
}
