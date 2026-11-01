//go:build linux

package state

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

type restoreRevokeSQL struct {
	open      func(string) (*sql.DB, error)
	commit    func(*sql.Tx) error
	rollback  func(*sql.Tx) error
	closeConn func(*sql.Conn) error
	closePool func(*sql.DB) error
	closeFile func(*os.File) error
}

func restoreRevokeWorkerServe(ops restoreRevokeSQL) (exitCode int) {
	if ops.closeFile == nil {
		ops.closeFile = (*os.File).Close
	}
	for _, descriptor := range []int{3, 4, 8} {
		var info unix.Stat_t
		flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
		expected := unix.O_RDONLY
		if descriptor == 4 {
			expected = unix.O_WRONLY
		}
		if err != nil || flags&unix.O_ACCMODE != expected || unix.Fstat(descriptor, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO || info.Uid != uint32(os.Geteuid()) {
			return 2
		}
	}
	var image, self unix.Stat_t
	if unix.Stat("/proc/self/exe", &self) != nil || unix.Fstat(5, &image) != nil || self.Dev != image.Dev || self.Ino != image.Ino || image.Mode&unix.S_IFMT != unix.S_IFREG {
		return 2
	}
	if restoreRevokeInheritedFiles() != nil {
		return 2
	}
	input, output := os.NewFile(3, "restore revoke request"), os.NewFile(4, "restore revoke response")
	defer func() {
		if ops.closeFile(output) != nil {
			exitCode = 2
		}
	}()
	var request restoreRevokeRequest
	err := restoreReadMessage(input, 4096, &request)
	closeError := input.Close()
	if err != nil || request.Nonce == [16]byte{} || !request.Limits.valid() || request.Limits.MaxDBBytes > 1<<40 || request.Mode != "inspect" && request.Mode != "revoke" && request.Mode != "postinspect" {
		return 2
	}
	if request.Mode != "inspect" && (request.Operation == [16]byte{} || request.RevokedAt.IsZero() || request.RevokedAt.Location() != time.UTC) {
		return 2
	}
	response := restoreRevokeResponse{Nonce: request.Nonce, Status: "invalid"}
	if closeError != nil {
		err = ErrRestoreUncertain
	} else if request.Mode == "revoke" && 3*request.Limits.MaxDBBytes+65536 > request.Limits.MaxStagingBytes {
		err = ErrRestoreBudget
	} else {
		controls := request.Limits
		controls.MaxInventoryBytes = 2*controls.MaxDBBytes + 65536
		err = snapshotInstallControls(controls, new(snapshotWorkerResponse))
		if err == nil {
			err = snapshotSetLimit(unix.RLIMIT_NOFILE, 32)
		}
		if err == nil {
			err = restoreRevokeSandbox()
		}
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), request.Limits.OperationTimeout)
			response.Facts, err = restoreRevokeWorker(ctx, request, ops)
			cancel()
		}
	}
	switch restoreDatabaseError(err) {
	case nil:
		response.Status = "ok"
	case ErrRestoreKey:
		response.Status = "key"
	case ErrRestoreBudget:
		response.Status = "budget"
	case ErrRestoreUnsupported:
		response.Status = "unsupported"
	case ErrRestoreUncertain:
		response.Status = "uncertain"
	}
	if err != nil {
		response.Facts = restoreRevocationFacts{}
	}
	if restoreWriteMessage(output, response) != nil {
		return 2
	}
	if err != nil {
		return 1
	}
	return 0
}

func restoreRevokeInheritedFiles() (returnedError error) {
	directory, err := os.Open("/proc/self/fd")
	if err != nil {
		return ErrRestoreInvalid
	}
	defer func() {
		if directory.Close() != nil {
			returnedError = ErrRestoreUncertain
		}
	}()
	var seen int
	for {
		entries, readError := directory.ReadDir(64)
		for _, entry := range entries {
			seen++
			if seen > 64 {
				return ErrRestoreInvalid
			}
			descriptor, err := strconv.Atoi(entry.Name())
			if err != nil {
				return ErrRestoreInvalid
			}
			var info unix.Stat_t
			if unix.Fstat(descriptor, &info) != nil || info.Mode&unix.S_IFMT == unix.S_IFSOCK {
				return ErrRestoreInvalid
			}
		}
		if errors.Is(readError, io.EOF) {
			return nil
		}
		if readError != nil {
			return ErrRestoreInvalid
		}
	}
}

func restoreRevokeWorker(ctx context.Context, request restoreRevokeRequest, ops restoreRevokeSQL) (facts restoreRevocationFacts, returnedError error) {
	if ops.closeFile == nil {
		ops.closeFile = (*os.File).Close
	}
	var files []*os.File
	for _, descriptor := range []int{6, 7, 8, 9, 10} {
		name, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(descriptor))
		if err != nil {
			return facts, ErrRestoreInvalid
		}
		files = append(files, os.NewFile(uintptr(descriptor), name))
	}
	defer func() {
		for _, file := range files {
			if ops.closeFile(file) != nil {
				returnedError = ErrRestoreUncertain
			}
		}
		if returnedError != nil {
			facts = restoreRevocationFacts{}
		}
	}()
	parent, database, keyPipe, lock, root := files[0], files[1], files[2], files[3], files[4]
	for index, expected := range []restoreDatabaseIdentity{request.Parent, request.Database, request.Lock, request.Root} {
		file := []*os.File{parent, database, lock, root}[index]
		actual, err := restoreDatabaseFileIdentity(file)
		if err != nil || actual != expected {
			return facts, ErrRestoreInvalid
		}
	}
	if err := restoreRevokeNamespace(root, lock, parent, database, request.Operation); err != nil {
		return facts, err
	}
	info, err := restoreDatabasePin(parent, database)
	if err != nil {
		return facts, err
	}
	if info.Mode().Perm() != 0600 {
		return facts, ErrRestoreInvalid
	}
	if info.Size() > request.Limits.MaxDBBytes || info.Size() > request.Limits.MaxStagingBytes {
		return facts, ErrRestoreBudget
	}
	key, err := io.ReadAll(io.LimitReader(keyPipe, 33))
	defer clear(key)
	if err != nil || len(key) != 32 {
		return facts, ErrRestoreKey
	}
	facts.Database, err = restoreDatabaseCheck(ctx, database, key, info.Size(), request.Limits)
	if err != nil {
		return facts, err
	}
	if request.Mode != "inspect" && facts.Database != request.Before {
		return facts, ErrRestoreInvalid
	}
	if request.Mode == "revoke" {
		facts.Protected, err = restoreRevokeMutate(ctx, parent, request.RevokedAt, request.Limits, ops)
		if err != nil {
			return facts, err
		}
		info, err = restoreDatabasePin(parent, database)
		if err != nil {
			return facts, ErrRestoreUncertain
		}
		if database.Sync() != nil || parent.Sync() != nil {
			return facts, ErrRestoreUncertain
		}
		facts.Database, err = restoreDatabaseCheck(ctx, database, key, info.Size(), request.Limits)
		if err != nil {
			return facts, ErrRestoreUncertain
		}
	} else if request.Mode == "postinspect" {
		facts.Protected, err = restoreRevokeReadPost(ctx, request.RevokedAt, request.Limits)
		if err != nil || facts.Protected != request.Protected {
			return facts, errors.Join(err, ErrRestoreInvalid)
		}
	}
	after, err := restoreDatabasePin(parent, database)
	if err != nil || !os.SameFile(info, after) {
		return facts, ErrRestoreInvalid
	}
	return facts, ctx.Err()
}

func restoreRevokeReadPost(ctx context.Context, when time.Time, limits LocalSnapshotLimits) (digest [32]byte, returnedError error) {
	pool, err := sql.Open("sqlite3", snapshotAddress("/proc/self/fd/7", "ro", "foreign_keys(1)", "query_only(1)", "trusted_schema(0)", "temp_store(MEMORY)", "mmap_size(0)", "busy_timeout(0)")+"&immutable=1")
	if err != nil {
		return digest, ErrRestoreInvalid
	}
	pool.SetMaxOpenConns(1)
	defer func() {
		if pool.Close() != nil {
			returnedError = ErrRestoreUncertain
		}
	}()
	tx, err := pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return digest, ErrRestoreInvalid
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			returnedError = ErrRestoreUncertain
		}
	}()
	if err := verifyRestoreCredentialsTx(ctx, tx, when); err != nil {
		return digest, err
	}
	return restoreRevokeProjection(ctx, tx, true, limits)
}

func restoreRevokeMutate(ctx context.Context, parent *os.File, when time.Time, limits LocalSnapshotLimits, ops restoreRevokeSQL) (digest [32]byte, returnedError error) {
	if ops.open == nil {
		ops.open = func(address string) (*sql.DB, error) { return sql.Open("sqlite3", address) }
	}
	if ops.commit == nil {
		ops.commit = (*sql.Tx).Commit
	}
	if ops.rollback == nil {
		ops.rollback = (*sql.Tx).Rollback
	}
	if ops.closeConn == nil {
		ops.closeConn = (*sql.Conn).Close
	}
	if ops.closePool == nil {
		ops.closePool = (*sql.DB).Close
	}
	address := snapshotAddress("/proc/self/fd/6/server.db", "rw", "foreign_keys(1)", "journal_mode(DELETE)", "synchronous(FULL)", "mmap_size(0)", "temp_store(MEMORY)", "trusted_schema(0)", "busy_timeout(0)") + "&_txlock=immediate"
	pool, err := ops.open(address)
	if err != nil {
		return digest, ErrRestoreInvalid
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	defer func() {
		if ops.closePool(pool) != nil {
			returnedError = ErrRestoreUncertain
		}
	}()
	connection, err := pool.Conn(ctx)
	if err != nil {
		return digest, ErrRestoreInvalid
	}
	defer func() {
		if ops.closeConn(connection) != nil {
			returnedError = ErrRestoreUncertain
		}
	}()
	for _, setting := range []struct {
		name     string
		expected any
	}{{"foreign_keys", int64(1)}, {"journal_mode", "delete"}, {"synchronous", int64(2)}, {"mmap_size", int64(0)}, {"temp_store", int64(2)}, {"trusted_schema", int64(0)}, {"busy_timeout", int64(0)}} {
		var value any
		if connection.QueryRowContext(ctx, "PRAGMA "+setting.name).Scan(&value) != nil {
			return digest, ErrRestoreUncertain
		}
		if value != setting.expected {
			return digest, ErrRestoreInvalid
		}
	}
	var pageSize, pages int64
	if connection.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize) != nil {
		return digest, ErrRestoreUncertain
	}
	if pageSize < 512 || pageSize > 65536 {
		return digest, ErrRestoreInvalid
	}
	maximum := limits.MaxDBBytes / pageSize
	if connection.QueryRowContext(ctx, "PRAGMA max_page_count="+strconv.FormatInt(maximum, 10)).Scan(&pages) != nil {
		return digest, ErrRestoreUncertain
	}
	if pages != maximum {
		return digest, ErrRestoreBudget
	}
	tx, err := connection.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return digest, ErrRestoreInvalid
	}
	finished := false
	defer func() {
		if !finished {
			if ops.rollback(tx) != nil {
				returnedError = ErrRestoreUncertain
			}
		}
	}()
	digest, err = restoreRevokeProjection(ctx, tx, false, limits)
	if err != nil {
		return digest, err
	}
	if err := revokeRestoreCredentialsTx(ctx, tx, when); err != nil {
		return digest, err
	}
	after, err := restoreRevokeProjection(ctx, tx, true, limits)
	if err != nil || after != digest {
		return digest, errors.Join(err, ErrRestoreInvalid)
	}
	err = ops.commit(tx)
	finished = true
	if err != nil {
		return digest, ErrRestoreUncertain
	}
	return digest, nil
}
