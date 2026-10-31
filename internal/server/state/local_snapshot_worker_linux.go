//go:build linux

package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func snapshotRunWorker(ctx context.Context, source *localSnapshotSource, stage *localSnapshotStage, guard *os.File, limits LocalSnapshotLimits) (response snapshotWorkerResponse, knownTerminal bool, returnedError error) {
	knownTerminal = true
	if ctx.Err() != nil {
		return response, true, ctx.Err()
	}
	request := snapshotWorkerRequest{name: source.name, limits: limits, op: 1}
	if _, err := rand.Read(request.nonce[:]); err != nil {
		return response, true, ErrLocalSnapshotInvalid
	}
	for index, file := range []*os.File{source.database, source.parent, stage.root} {
		var info unix.Stat_t
		if unix.Fstat(int(file.Fd()), &info) != nil {
			return response, true, ErrLocalSnapshotInvalid
		}
		identity := snapshotFileIdentity{device: uint64(info.Dev), inode: info.Ino}
		switch index {
		case 0:
			request.source = identity
		case 1:
			request.parent = identity
		case 2:
			request.stage = identity
		}
	}
	frame, err := snapshotEncodeRequest(request)
	if err != nil {
		return response, true, err
	}
	var owned []*os.File
	closeOwned := func(file *os.File) error {
		for index, entry := range owned {
			if entry == file {
				owned[index] = nil
				break
			}
		}
		if file.Close() != nil {
			retainSnapshotResource(file)
			knownTerminal = false
			return ErrLocalSnapshotUncertain
		}
		return nil
	}
	defer func() {
		for _, file := range owned {
			if file != nil {
				returnedError = snapshotMerge(returnedError, closeOwned(file))
			}
		}
	}()
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		return response, true, ErrLocalSnapshotInvalid
	}
	owned = append(owned, image)
	requestReader, requestWriter, err := os.Pipe()
	if err != nil {
		return response, true, ErrLocalSnapshotInvalid
	}
	owned = append(owned, requestReader, requestWriter)
	responseReader, responseWriter, err := os.Pipe()
	if err != nil {
		return response, true, ErrLocalSnapshotInvalid
	}
	owned = append(owned, responseReader, responseWriter)
	quiet, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return response, true, ErrLocalSnapshotInvalid
	}
	owned = append(owned, quiet)
	if count, err := requestWriter.Write(frame[:]); err != nil || count != len(frame) {
		return response, true, ErrLocalSnapshotInvalid
	}
	if err = closeOwned(requestWriter); err != nil {
		return response, false, err
	}
	command := exec.Command("/proc/self/fd/5", snapshotWorkerMode)
	command.Env = []string{"HOME=" + stage.root.Name(), "TMPDIR=" + stage.root.Name(), "LANG=C", "LC_ALL=C"}
	command.Stdin = quiet
	command.Stdout = quiet
	command.Stderr = quiet
	command.ExtraFiles = []*os.File{requestReader, responseWriter, image, source.parent, source.database, stage.root, guard}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if ctx.Err() != nil {
		return response, true, ctx.Err()
	}
	if command.Start() != nil {
		return response, true, ErrLocalSnapshotInvalid
	}
	knownTerminal = false
	requestClose, responseClose := closeOwned(requestReader), closeOwned(responseWriter)
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = command.Process.Kill(); close(interrupted) })
	var input [snapshotResponseBytes + 1]byte
	count, readError := io.ReadFull(responseReader, input[:])
	if readError != io.ErrUnexpectedEOF || count != snapshotResponseBytes {
		_ = command.Process.Kill()
	}
	waitError := command.Wait()
	if !stop() {
		<-interrupted
	}
	if command.ProcessState != nil {
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && (status.Exited() || status.Signaled()) {
			knownTerminal = true
		}
	}
	if !knownTerminal {
		retainSnapshotResource(command)
		return response, false, ErrLocalSnapshotUncertain
	}
	if requestClose != nil || responseClose != nil {
		return response, false, ErrLocalSnapshotUncertain
	}
	if readError == io.ErrUnexpectedEOF && count == snapshotResponseBytes {
		response, err = snapshotDecodeResponse(input[:snapshotResponseBytes], request)
		if errors.Is(err, ErrLocalSnapshotCleanup) {
			return response, true, err
		}
	}
	if ctx.Err() != nil {
		return response, true, ctx.Err()
	}
	if readError != io.ErrUnexpectedEOF || count != snapshotResponseBytes {
		return response, true, ErrLocalSnapshotInvalid
	}
	if err != nil {
		return snapshotWorkerResponse{}, true, err
	}
	if waitError != nil {
		return snapshotWorkerResponse{}, true, ErrLocalSnapshotInvalid
	}
	return response, true, nil
}

func snapshotWorkerServe() (exitCode int) {
	for _, descriptor := range []int{3, 4} {
		var info unix.Stat_t
		if unix.Fstat(descriptor, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO || info.Uid != uint32(os.Geteuid()) {
			return 2
		}
	}
	var image, self unix.Stat_t
	imageFD, err := unix.Open("/proc/self/exe", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 2
	}
	selfError, imageError := unix.Fstat(imageFD, &self), unix.Fstat(5, &image)
	closeImageError := unix.Close(imageFD)
	if selfError != nil || imageError != nil || closeImageError != nil || image.Mode&unix.S_IFMT != unix.S_IFREG || image.Dev != self.Dev || image.Ino != self.Ino {
		return 2
	}
	input, output := snapshotWorkerFiles()
	if input == nil || output == nil {
		return 2
	}
	defer func() {
		if output.Close() != nil {
			exitCode = 2
		}
	}()
	request, err := snapshotDecodeRequest(input)
	closeError := input.Close()
	if err != nil || closeError != nil {
		return 2
	}
	response := snapshotWorkerResponse{nonce: request.nonce, status: 1}
	if snapshotWorkerIdentities(request) == nil {
		response.status = 3
		if snapshotInstallControls(request.limits, &response) == nil {
			ctx, cancel := context.WithTimeout(context.Background(), request.limits.OperationTimeout)
			response.status = 4
			if err = snapshotWorkerCopy(ctx, request, &response); err == nil {
				response.status = 0
			} else if errors.Is(err, ErrLocalSnapshotBudget) {
				response.status = 6
			} else if errors.Is(err, ErrLocalSnapshotCleanup) {
				response.status = 7
			} else if errors.Is(err, ErrLocalSnapshotResource) {
				response.status = 3
			}
			cancel()
		}
	}
	frame := snapshotEncodeResponse(response)
	if count, err := output.Write(frame[:]); err != nil || count != len(frame) {
		return 2
	}
	if response.status != 0 {
		return 1
	}
	return 0
}

func snapshotWorkerIdentities(request snapshotWorkerRequest) error {
	for index, expected := range []snapshotFileIdentity{request.parent, request.source, request.stage} {
		var attributes unix.Stat_t
		if unix.Fstat(6+index, &attributes) != nil || uint64(attributes.Dev) != expected.device || attributes.Ino != expected.inode || attributes.Uid != uint32(os.Geteuid()) || attributes.Mode&0077 != 0 {
			return ErrLocalSnapshotInvalid
		}
		if index == 1 {
			if attributes.Mode&unix.S_IFMT != unix.S_IFREG || attributes.Nlink != 1 {
				return ErrLocalSnapshotInvalid
			}
		} else if attributes.Mode&unix.S_IFMT != unix.S_IFDIR {
			return ErrLocalSnapshotInvalid
		}
	}
	var source, fence, named unix.Stat_t
	if unix.Fstatat(6, request.name, &source, unix.AT_SYMLINK_NOFOLLOW) != nil || uint64(source.Dev) != request.source.device || source.Ino != request.source.inode || source.Mode&unix.S_IFMT != unix.S_IFREG || source.Nlink != 1 {
		return ErrLocalSnapshotInvalid
	}
	if unix.Fstat(9, &fence) != nil || unix.Fstatat(6, ".snapshot-worker.lock", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || fence.Dev != named.Dev || fence.Ino != named.Ino || fence.Uid != uint32(os.Geteuid()) || fence.Mode&unix.S_IFMT != unix.S_IFREG || fence.Mode&0777 != 0600 || fence.Nlink != 1 {
		return ErrLocalSnapshotInvalid
	}
	independent, err := unix.Openat(6, ".snapshot-worker.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	lockError := unix.Flock(independent, unix.LOCK_EX|unix.LOCK_NB)
	closeError := unix.Close(independent)
	if lockError != unix.EWOULDBLOCK || closeError != nil {
		return ErrLocalSnapshotInvalid
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		var sidecar unix.Stat_t
		err := unix.Fstatat(6, request.name+suffix, &sidecar, unix.AT_SYMLINK_NOFOLLOW)
		if err == unix.ENOENT {
			continue
		}
		maximum := request.limits.MaxWALBytes
		if suffix == "-shm" {
			maximum = request.limits.MaxSHMBytes
		}
		if err != nil || sidecar.Mode&unix.S_IFMT != unix.S_IFREG || sidecar.Uid != uint32(os.Geteuid()) || sidecar.Mode&0077 != 0 || sidecar.Nlink != 1 || sidecar.Size < 0 || sidecar.Size > maximum {
			return ErrLocalSnapshotBudget
		}
	}
	return nil
}

func snapshotSetLimit(resource int, value uint64) error {
	limit := unix.Rlimit{Cur: value, Max: value}
	if unix.Setrlimit(resource, &limit) != nil {
		return ErrLocalSnapshotResource
	}
	var actual unix.Rlimit
	if unix.Getrlimit(resource, &actual) != nil || actual != limit {
		return ErrLocalSnapshotResource
	}
	return nil
}

func snapshotVirtualBytes() (uint64, error) {
	file, err := os.Open("/proc/self/statm")
	if err != nil {
		return 0, ErrLocalSnapshotResource
	}
	var buffer [1025]byte
	count, readError := io.ReadFull(file, buffer[:])
	closeError := file.Close()
	if readError != io.ErrUnexpectedEOF || closeError != nil || count == len(buffer) {
		return 0, ErrLocalSnapshotResource
	}
	fields := strings.Fields(string(buffer[:count]))
	if len(fields) != 7 {
		return 0, ErrLocalSnapshotResource
	}
	pages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || pages > math.MaxUint64/uint64(os.Getpagesize()) {
		return 0, ErrLocalSnapshotResource
	}
	return pages * uint64(os.Getpagesize()), nil
}

func snapshotInstallControls(limits LocalSnapshotLimits, response *snapshotWorkerResponse) error {
	if !limits.valid() || unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) != nil {
		return ErrLocalSnapshotResource
	}
	for _, limit := range []struct {
		resource int
		value    uint64
	}{
		{unix.RLIMIT_CORE, 0}, {unix.RLIMIT_AS, uint64(limits.WorkerAddressSpaceBytes)}, {unix.RLIMIT_CPU, limits.WorkerCPUSeconds}, {unix.RLIMIT_FSIZE, uint64(max(limits.MaxDBBytes, limits.MaxInventoryBytes, 65536))},
	} {
		if snapshotSetLimit(limit.resource, limit.value) != nil {
			return ErrLocalSnapshotResource
		}
	}
	virtual, err := snapshotVirtualBytes()
	if err != nil || virtual > uint64(limits.WorkerAddressSpaceBytes) {
		return ErrLocalSnapshotResource
	}
	tls := libc.NewTLS()
	defer tls.Close()
	arguments := libc.NewVaListN(1)
	if arguments == 0 {
		return ErrLocalSnapshotResource
	}
	libc.VaList(arguments, int32(1))
	configured := sqlite3.Xsqlite3_config(tls, sqlite3.SQLITE_CONFIG_MEMSTATUS, arguments)
	libc.Xfree(tls, arguments)
	if configured != sqlite3.SQLITE_OK || sqlite3.Xsqlite3_initialize(tls) != sqlite3.SQLITE_OK {
		return ErrLocalSnapshotResource
	}
	sqlite3.Xsqlite3_hard_heap_limit64(tls, limits.SQLiteHeapBytes)
	if sqlite3.Xsqlite3_hard_heap_limit64(tls, -1) != limits.SQLiteHeapBytes {
		return ErrLocalSnapshotResource
	}
	before := sqlite3.Xsqlite3_memory_used(tls)
	if before < 0 || before > limits.SQLiteHeapBytes {
		return ErrLocalSnapshotResource
	}
	allocation := sqlite3.Xsqlite3_malloc64(tls, 4096)
	if allocation == 0 {
		return ErrLocalSnapshotResource
	}
	during := sqlite3.Xsqlite3_memory_used(tls)
	sqlite3.Xsqlite3_free(tls, allocation)
	if during < before+4096 || sqlite3.Xsqlite3_memory_used(tls) != before {
		return ErrLocalSnapshotResource
	}
	oversized := sqlite3.Xsqlite3_malloc64(tls, uint64(limits.SQLiteHeapBytes)+1)
	if oversized != 0 {
		sqlite3.Xsqlite3_free(tls, oversized)
		return ErrLocalSnapshotResource
	}
	response.addressSpace = uint64(limits.WorkerAddressSpaceBytes)
	response.heap = uint64(limits.SQLiteHeapBytes)
	response.heapUsed = uint64(before)
	response.cpu = limits.WorkerCPUSeconds
	response.virtualBytes = virtual
	return nil
}

func snapshotAddress(path string, mode string, pragmas ...string) string {
	parameters := url.Values{"mode": []string{mode}}
	for _, pragma := range pragmas {
		parameters.Add("_pragma", pragma)
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: parameters.Encode()}).String()
}

const snapshotCatalogColumns = "CASE WHEN typeof(type)='text' AND length(CAST(type AS BLOB)) BETWEEN 1 AND 64 THEN 1 ELSE 2 END,CASE WHEN typeof(type)='text' AND length(CAST(type AS BLOB)) BETWEEN 1 AND 64 THEN type END,CASE WHEN typeof(name)='text' AND length(CAST(name AS BLOB)) BETWEEN 1 AND 256 THEN 1 ELSE 2 END,CASE WHEN typeof(name)='text' AND length(CAST(name AS BLOB)) BETWEEN 1 AND 256 THEN name END,CASE WHEN typeof(tbl_name)='text' AND length(CAST(tbl_name AS BLOB)) BETWEEN 1 AND 256 THEN 1 ELSE 2 END,CASE WHEN typeof(tbl_name)='text' AND length(CAST(tbl_name AS BLOB)) BETWEEN 1 AND 256 THEN tbl_name END,CASE WHEN sql IS NULL THEN 0 WHEN typeof(sql)='text' AND length(CAST(sql AS BLOB))<=65536 THEN 1 ELSE 2 END,CASE WHEN typeof(sql)='text' AND length(CAST(sql AS BLOB))<=65536 THEN sql END"

type snapshotCatalogRow struct {
	kind, name, table, definition string
	definitionNull                bool
}

func snapshotScanCatalogRow(rows *sql.Rows) (snapshotCatalogRow, error) {
	var states [4]int64
	var cells [4]sql.NullString
	if rows.Scan(&states[0], &cells[0], &states[1], &cells[1], &states[2], &cells[2], &states[3], &cells[3]) != nil {
		return snapshotCatalogRow{}, ErrLocalSnapshotInvalid
	}
	limits := [...]int{64, 256, 256, 65536}
	for index, state := range states {
		if state != 0 && state != 1 || index < 3 && state != 1 || cells[index].Valid != (state == 1) || len(cells[index].String) > limits[index] || index < 3 && len(cells[index].String) == 0 {
			return snapshotCatalogRow{}, ErrLocalSnapshotInvalid
		}
	}
	return snapshotCatalogRow{kind: cells[0].String, name: cells[1].String, table: cells[2].String, definition: cells[3].String, definitionNull: states[3] == 0}, nil
}

func snapshotInternalCatalogRow(row snapshotCatalogRow) (uint8, bool) {
	switch row.name {
	case "sqlite_autoindex_category_skills_1":
		return 1, row == (snapshotCatalogRow{kind: "index", name: "sqlite_autoindex_category_skills_1", table: "category_skills", definitionNull: true})
	case "sqlite_sequence":
		return 2, row == (snapshotCatalogRow{kind: "table", name: "sqlite_sequence", table: "sqlite_sequence", definition: "CREATE TABLE sqlite_sequence(name,seq)"})
	default:
		return 0, false
	}
}

func snapshotSchema(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([32]byte, error) {
	rows, err := query.QueryContext(ctx, "SELECT "+snapshotCatalogColumns+" FROM main.sqlite_schema ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		return [32]byte{}, ErrLocalSnapshotInvalid
	}
	return snapshotReadCatalog(ctx, rows)
}

func snapshotReadCatalog(ctx context.Context, rows *sql.Rows) ([32]byte, error) {
	var zero [32]byte
	hash := sha256.New()
	normalizedHash := sha256.New()
	count, applications, total := 0, 0, 0
	var seen uint8
	var previousKind, previousName string
	var failure error
	for rows.Next() {
		if ctx.Err() != nil {
			failure = ctx.Err()
			break
		}
		row, err := snapshotScanCatalogRow(rows)
		if err != nil {
			failure = err
			break
		}
		count++
		total += len(row.kind) + len(row.name) + len(row.table) + len(row.definition)
		if count > 1024 || total > 1<<20 || count > 1 && (row.kind < previousKind || row.kind == previousKind && row.name <= previousName) {
			failure = ErrLocalSnapshotInvalid
			break
		}
		previousKind, previousName = row.kind, row.name
		internal, trusted := snapshotInternalCatalogRow(row)
		if internal != 0 {
			if !trusted || seen&internal != 0 {
				failure = ErrLocalSnapshotInvalid
				break
			}
			seen |= internal
			continue
		}
		if row.definitionNull {
			failure = ErrLocalSnapshotInvalid
			break
		}
		applications++
		for fieldIndex, value := range []string{row.kind, row.name, row.table, row.definition} {
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(value)))
			hash.Write(length[:])
			hash.Write([]byte(value))
			if fieldIndex == 3 {
				value = strings.Join(strings.Fields(value), " ")
			}
			binary.BigEndian.PutUint32(length[:], uint32(len(value)))
			normalizedHash.Write(length[:])
			normalizedHash.Write([]byte(value))
		}
	}
	if rows.Err() != nil {
		failure = ErrLocalSnapshotInvalid
	}
	if rows.Close() != nil {
		return zero, ErrLocalSnapshotUncertain
	}
	if failure != nil {
		return zero, failure
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if hex.EncodeToString(hash.Sum(nil)) != snapshotKnownSchema || hex.EncodeToString(normalizedHash.Sum(nil)) != snapshotKnownNormalizedSchema || count != 92 || applications != 90 || seen != 3 {
		return zero, ErrLocalSnapshotInvalid
	}
	copy(zero[:], hash.Sum(nil))
	return zero, nil
}

func snapshotWorkerCopy(ctx context.Context, request snapshotWorkerRequest, response *snapshotWorkerResponse) (returnedError error) {
	if request.op != 1 {
		return ErrLocalSnapshotInvalid
	}
	parentName, nameError := os.Readlink("/proc/self/fd/6")
	if nameError != nil {
		return ErrLocalSnapshotInvalid
	}
	parentFD, duplicateError := unix.Dup(6)
	if duplicateError != nil {
		return ErrLocalSnapshotInvalid
	}
	parent := os.NewFile(uintptr(parentFD), parentName)
	databaseFD, duplicateError := unix.Dup(7)
	if duplicateError != nil {
		if parent.Close() != nil {
			return ErrLocalSnapshotUncertain
		}
		return ErrLocalSnapshotInvalid
	}
	database := os.NewFile(uintptr(databaseFD), parentName+"/"+request.name)
	observation, inspectionError := inspectLocalSnapshotSource(ctx, parent, database, snapshotSourcePrefixBudget)
	databaseClose, parentClose := database.Close(), parent.Close()
	if databaseClose != nil || parentClose != nil {
		return ErrLocalSnapshotUncertain
	}
	if inspectionError != nil || observation.decision != snapshotSourceNeedsCertificate {
		return ErrLocalSnapshotInvalid
	}
	pool, err := sql.Open("sqlite3", snapshotAddress("/proc/self/fd/6/"+request.name, "ro", "mmap_size(0)", "temp_store(MEMORY)", "busy_timeout(0)"))
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	poolClosed := false
	defer func() {
		if !poolClosed && pool.Close() != nil {
			returnedError = ErrLocalSnapshotUncertain
		}
	}()
	connection, err := pool.Conn(ctx)
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	connectionClosed := false
	defer func() {
		if !connectionClosed && connection.Close() != nil {
			returnedError = ErrLocalSnapshotUncertain
		}
	}()
	transaction, err := connection.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	transactionClosed := false
	defer func() {
		if !transactionClosed && transaction.Rollback() != nil {
			returnedError = ErrLocalSnapshotUncertain
		}
	}()
	var pageSize, pages int64
	if transaction.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize) != nil || transaction.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages) != nil || pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || pages < 1 || pages > request.limits.MaxDBBytes/pageSize {
		return ErrLocalSnapshotBudget
	}
	response.schemaHash, err = snapshotSchema(ctx, transaction)
	if err != nil {
		response.status = 5
		return err
	}
	length := pages * pageSize
	if max(length, 4096) > request.limits.MaxStagingBytes || request.limits.MaxInventoryBytes > request.limits.MaxStagingBytes-max(length, 4096) || request.limits.MaxStagingBytes-max(length, 4096)-request.limits.MaxInventoryBytes < 65536 {
		return ErrLocalSnapshotBudget
	}
	var fileLimit unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_FSIZE, &fileLimit) != nil {
		return ErrLocalSnapshotResource
	}
	fileLimit.Cur = uint64(max(length, 4096))
	if unix.Setrlimit(unix.RLIMIT_FSIZE, &fileLimit) != nil {
		return ErrLocalSnapshotResource
	}
	outputFD, err := unix.Openat(8, "snapshot.sqlite", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	if unix.Close(outputFD) != nil {
		return ErrLocalSnapshotUncertain
	}
	target := snapshotAddress("/proc/self/fd/8/snapshot.sqlite", "rw", fmt.Sprintf("page_size(%d)", pageSize), "journal_mode(OFF)", "temp_store(MEMORY)", "mmap_size(0)")
	err = connection.Raw(func(raw any) error {
		provider, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return ErrLocalSnapshotInvalid
		}
		backup, err := provider.NewBackup(target)
		if err != nil {
			return ErrLocalSnapshotUncertain
		}
		var failure error
		for {
			if ctx.Err() != nil {
				failure = ctx.Err()
				break
			}
			more, stepError := backup.Step(64)
			if stepError != nil {
				failure = ErrLocalSnapshotInvalid
				break
			}
			var size unix.Stat_t
			if unix.Fstatat(8, "snapshot.sqlite", &size, unix.AT_SYMLINK_NOFOLLOW) != nil || size.Size < 0 || size.Size > max(length, 4096) || backup.PageCount() != int(pages) {
				failure = ErrLocalSnapshotBudget
				break
			}
			if !more {
				if backup.Remaining() != 0 {
					failure = ErrLocalSnapshotInvalid
				}
				break
			}
		}
		var finished driver.Conn
		finished, err = backup.Commit()
		if err != nil {
			return ErrLocalSnapshotUncertain
		}
		if finished == nil || finished.Close() != nil {
			return ErrLocalSnapshotUncertain
		}
		return failure
	})
	if err != nil {
		return err
	}
	if err = transaction.Rollback(); err != nil {
		return ErrLocalSnapshotUncertain
	}
	transactionClosed = true
	if err = connection.Close(); err != nil {
		return ErrLocalSnapshotUncertain
	}
	connectionClosed = true
	if err = pool.Close(); err != nil {
		return ErrLocalSnapshotUncertain
	}
	poolClosed = true
	fileLimit.Cur = fileLimit.Max
	if unix.Setrlimit(unix.RLIMIT_FSIZE, &fileLimit) != nil {
		return ErrLocalSnapshotResource
	}
	checker, err := sql.Open("sqlite3", snapshotAddress("/proc/self/fd/8/snapshot.sqlite", "ro", "mmap_size(0)", "temp_store(MEMORY)")+"&immutable=1")
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	checker.SetMaxOpenConns(1)
	checker.SetMaxIdleConns(1)
	checkerClosed := false
	defer func() {
		if !checkerClosed && checker.Close() != nil {
			returnedError = ErrLocalSnapshotUncertain
		}
	}()
	var integrity string
	err = checker.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
	if err == nil && integrity != "ok" {
		err = ErrLocalSnapshotInvalid
	}
	if err == nil {
		_, err = snapshotSchema(ctx, checker)
	}
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	fileFD, err := unix.Openat(8, "snapshot.sqlite", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	file := os.NewFile(uintptr(fileFD), "snapshot output")
	var attributes unix.Stat_t
	identityError := unix.Fstat(fileFD, &attributes)
	hasher := sha256.New()
	count, readError := io.Copy(hasher, io.LimitReader(file, length+1))
	syncError := file.Sync()
	closeError := file.Close()
	if closeError != nil {
		return ErrLocalSnapshotUncertain
	}
	if identityError != nil || attributes.Mode&unix.S_IFMT != unix.S_IFREG || attributes.Uid != uint32(os.Geteuid()) || attributes.Mode&0777 != 0600 || attributes.Nlink != 1 || attributes.Size != length || count != length || readError != nil || syncError != nil {
		return ErrLocalSnapshotInvalid
	}
	if unix.Fsync(8) != nil {
		return ErrLocalSnapshotCleanup
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	response.databaseBytes = uint64(length)
	response.pageSize = uint64(pageSize)
	response.pages = uint64(pages)
	copy(response.databaseHash[:], hasher.Sum(nil))
	if err = snapshotWorkerInventory(ctx, checker, request, response); err != nil {
		return err
	}
	if checker.Close() != nil {
		return ErrLocalSnapshotUncertain
	}
	checkerClosed = true
	if unix.Fsync(8) != nil {
		return ErrLocalSnapshotCleanup
	}
	return nil
}

func snapshotWorkerInventory(ctx context.Context, pool *sql.DB, request snapshotWorkerRequest, response *snapshotWorkerResponse) (returnedError error) {
	rows, err := pool.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	invalid := rows.Next()
	rowError := rows.Err()
	closeError := rows.Close()
	if closeError != nil {
		return ErrLocalSnapshotUncertain
	}
	if invalid || rowError != nil {
		return ErrLocalSnapshotInvalid
	}
	descriptor, err := unix.Openat(8, "references.ndjson", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	output := os.NewFile(uintptr(descriptor), "snapshot references")
	counts, size, digest, err := snapshotWriteInventory(ctx, pool, output, request.limits)
	syncError := output.Sync()
	closeError = output.Close()
	if closeError != nil {
		return ErrLocalSnapshotUncertain
	}
	if err != nil {
		return err
	}
	if syncError != nil {
		return ErrLocalSnapshotCleanup
	}
	manifest := localSnapshotManifest{Format: "cognate-local-sqlite-candidate", Version: 1, ContentClosure: "not-included", ID: hex.EncodeToString(request.nonce[:]), DatabaseBytes: int64(response.databaseBytes), DatabaseSHA256: hex.EncodeToString(response.databaseHash[:]), SchemaSHA256: hex.EncodeToString(response.schemaHash[:]), ReferenceBytes: size, ReferenceSHA256: hex.EncodeToString(digest[:]), Repositories: counts.repositories, Skills: counts.skills, Packs: counts.packs, Items: counts.items, TotalRecords: counts.total}
	if pool.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&manifest.SQLiteVersion) != nil {
		return ErrLocalSnapshotInvalid
	}
	var policyID string
	if pool.QueryRowContext(ctx, "SELECT "+snapshotText("key_id", 64)+",CASE WHEN length(key_proof)<=512 THEN key_proof END FROM server_metadata WHERE name=?", keyMetadataName).Scan(&manifest.KeyID, &manifest.KeyProof) != nil {
		return ErrLocalSnapshotInvalid
	}
	if pool.QueryRowContext(ctx, "SELECT "+snapshotText("key_id", 64)+",CASE WHEN length(key_proof)<=512 THEN key_proof END FROM server_metadata WHERE name=?", observationMetadataName).Scan(&policyID, &manifest.PolicyProof) != nil || policyID != manifest.KeyID || len(manifest.KeyProof) == 0 || len(manifest.PolicyProof) == 0 {
		return ErrLocalSnapshotInvalid
	}
	manifest.Credentials = make([]snapshotCredential, 0, 1)
	credentialRows, err := pool.QueryContext(ctx, "SELECT "+snapshotText("u.user_key", 256)+","+snapshotText("c.viewer_id", 256)+","+snapshotText("c.client_id", 256)+","+snapshotText("c.key_id", 64)+","+snapshotText("c.generation", 64)+",CASE WHEN length(c.ciphertext)<=32768 THEN c.ciphertext END FROM git_hub_credentials c JOIN users u ON u.id=c.user_id ORDER BY c.id LIMIT 1")
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	var credentialError error
	if credentialRows.Next() {
		var credential snapshotCredential
		if credentialRows.Scan(&credential.UserKey, &credential.ViewerID, &credential.ClientID, &credential.KeyID, &credential.Generation, &credential.Ciphertext) != nil {
			credentialError = ErrLocalSnapshotInvalid
		} else {
			manifest.Credentials = append(manifest.Credentials, credential)
		}
	}
	if credentialRows.Err() != nil {
		credentialError = ErrLocalSnapshotInvalid
	}
	if credentialRows.Close() != nil {
		return ErrLocalSnapshotUncertain
	}
	if credentialError != nil {
		return credentialError
	}
	encoded, err := json.Marshal(manifest)
	if err != nil || len(encoded)+1 > 65536 {
		return ErrLocalSnapshotBudget
	}
	encoded = append(encoded, '\n')
	descriptor, err = unix.Openat(8, "candidate.json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrLocalSnapshotInvalid
	}
	metadata := os.NewFile(uintptr(descriptor), "snapshot candidate metadata")
	written, writeError := metadata.Write(encoded)
	syncError = metadata.Sync()
	closeError = metadata.Close()
	if closeError != nil {
		return ErrLocalSnapshotUncertain
	}
	if written != len(encoded) || writeError != nil {
		return ErrLocalSnapshotInvalid
	}
	if syncError != nil {
		return ErrLocalSnapshotCleanup
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	response.referenceBytes = uint64(size)
	response.referenceHash = digest
	response.records = counts.total
	return nil
}
