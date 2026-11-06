//go:build linux

package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type restoreDatabaseIO struct {
	closeFile   func(*os.File) error
	readMessage func(io.Reader, int64, any) error
}

type restoreDatabaseScope struct {
	files       []*os.File
	operations  restoreDatabaseIO
	quarantined bool
}

func inspectRestoreDatabase(ctx context.Context, parent, database *os.File, key []byte, limits LocalSnapshotLimits) (RestoreDatabaseEvidence, error) {
	return inspectRestoreDatabaseWithIO(ctx, parent, database, key, limits, restoreDatabaseIO{closeFile: (*os.File).Close, readMessage: restoreReadMessage})
}

func inspectRestoreDatabaseWithIO(ctx context.Context, parent, database *os.File, key []byte, limits LocalSnapshotLimits, operations restoreDatabaseIO) (evidence RestoreDatabaseEvidence, returnedError error) {
	ctx, cancel := context.WithTimeout(ctx, limits.OperationTimeout)
	defer cancel()
	material := bytes.Clone(key)
	defer clear(material)
	scope := &restoreDatabaseScope{operations: operations}
	defer func() {
		if scope.quarantined {
			retainSnapshotResource(scope)
			returnedError = ErrRestoreUncertain
		} else {
			for _, file := range scope.files {
				if file != nil && scope.close(file) != nil {
					returnedError = ErrRestoreUncertain
				}
			}
		}
		returnedError = restoreDatabaseError(returnedError)
		if returnedError != nil {
			evidence = RestoreDatabaseEvidence{}
		}
	}()
	ownedParent, err := scope.duplicate(parent)
	if err != nil {
		return evidence, err
	}
	ownedDatabase, err := scope.duplicate(database)
	if err != nil {
		return evidence, err
	}
	before, err := restoreDatabasePin(ownedParent, ownedDatabase)
	if err != nil {
		return evidence, err
	}
	if before.Size() > limits.MaxDBBytes || before.Size() > limits.MaxStagingBytes {
		return evidence, ErrRestoreBudget
	}
	digest, err := restoreDatabaseHash(ctx, ownedDatabase, before.Size())
	if err != nil {
		return evidence, err
	}
	request := restoreDatabaseRequest{Limits: limits}
	if _, err := rand.Read(request.Nonce[:]); err != nil {
		return evidence, ErrRestoreInvalid
	}
	request.Parent, err = restoreDatabaseFileIdentity(ownedParent)
	if err != nil {
		return evidence, err
	}
	request.Database, err = restoreDatabaseFileIdentity(ownedDatabase)
	if err != nil {
		return evidence, err
	}
	response, err := scope.run(ctx, ownedParent, ownedDatabase, material, request)
	if err != nil {
		return evidence, err
	}
	after, err := restoreDatabasePin(ownedParent, ownedDatabase)
	if err != nil || !snapshotSourceSame(before, after) {
		return evidence, ErrRestoreInvalid
	}
	afterDigest, err := restoreDatabaseHash(ctx, ownedDatabase, before.Size())
	if err != nil {
		return evidence, err
	}
	facts := response.Facts
	keyHash := sha256.Sum256(material)
	if facts.KeyID != base64.RawURLEncoding.EncodeToString(keyHash[:]) {
		return evidence, ErrRestoreKey
	}
	_, knownSchema := snapshotLookupSchema(hex.EncodeToString(facts.SchemaHash[:]))
	if response.Nonce != request.Nonce || facts.DatabaseBytes != before.Size() || facts.DatabaseHash != digest || afterDigest != digest || !knownSchema || facts.ReferenceBytes < 0 || facts.ReferenceBytes > limits.MaxInventoryBytes || facts.Records > limits.MaxRecords || facts.Policy <= 0 || facts.Policy > int64(24*time.Hour) || len(facts.KeyID) != 43 {
		return evidence, ErrRestoreInvalid
	}
	var records uint64
	for _, count := range facts.Counts {
		if count > facts.Records-records {
			return evidence, ErrRestoreInvalid
		}
		records += count
	}
	if records != facts.Records {
		return evidence, ErrRestoreInvalid
	}
	if ctx.Err() != nil {
		return evidence, ctx.Err()
	}
	return RestoreDatabaseEvidence{value: &facts}, nil
}

func (scope *restoreDatabaseScope) add(file *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, ErrRestoreInvalid
	}
	scope.files = append(scope.files, file)
	return file, nil
}

func (scope *restoreDatabaseScope) duplicate(file *os.File) (*os.File, error) {
	if file == nil {
		return nil, ErrRestoreInvalid
	}
	descriptor, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, ErrRestoreInvalid
	}
	return scope.add(os.NewFile(uintptr(descriptor), file.Name()), nil)
}

func (scope *restoreDatabaseScope) close(file *os.File) error {
	for index, owned := range scope.files {
		if owned == file {
			scope.files[index] = nil
			break
		}
	}
	if scope.operations.closeFile(file) != nil {
		retainSnapshotResource(file)
		return ErrRestoreUncertain
	}
	return nil
}

func (scope *restoreDatabaseScope) pipe() (*os.File, *os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, ErrRestoreInvalid
	}
	scope.files = append(scope.files, reader, writer)
	return reader, writer, nil
}

func (scope *restoreDatabaseScope) run(ctx context.Context, parent, database *os.File, key []byte, request restoreDatabaseRequest) (restoreDatabaseResponse, error) {
	response := restoreDatabaseResponse{}
	image, err := scope.add(os.Open("/proc/self/exe"))
	if err != nil {
		return response, err
	}
	input, inputWriter, err := scope.pipe()
	if err != nil {
		return response, err
	}
	output, outputWriter, err := scope.pipe()
	if err != nil {
		return response, err
	}
	keyReader, keyWriter, err := scope.pipe()
	if err != nil {
		return response, err
	}
	quiet, err := scope.add(os.OpenFile(os.DevNull, os.O_RDWR, 0))
	if err != nil {
		return response, err
	}
	if err := restoreWriteMessage(inputWriter, request); err != nil {
		return response, err
	}
	if count, err := keyWriter.Write(key); err != nil || count != 32 {
		return response, ErrRestoreKey
	}
	if err := errors.Join(scope.close(inputWriter), scope.close(keyWriter)); err != nil {
		return response, err
	}
	command := exec.Command("/proc/self/fd/5", restoreDatabaseWorkerMode)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	command.Stdin, command.Stdout, command.Stderr = quiet, quiet, quiet
	command.ExtraFiles = []*os.File{input, outputWriter, image, parent, database, keyReader}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if ctx.Err() != nil {
		return response, ctx.Err()
	}
	if err := command.Start(); err != nil {
		return response, ErrRestoreInvalid
	}
	closeError := errors.Join(scope.close(input), scope.close(outputWriter), scope.close(keyReader))
	type outcome struct {
		response             restoreDatabaseResponse
		readError, waitError error
	}
	done := make(chan outcome, 1)
	go func() {
		var result outcome
		result.readError = scope.operations.readMessage(output, 16384, &result.response)
		if result.readError != nil {
			_ = command.Process.Kill()
		}
		result.waitError = command.Wait()
		done <- result
	}()
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		_ = command.Process.Kill()
		timer := time.NewTimer(request.Limits.DrainTimeout)
		defer timer.Stop()
		select {
		case result = <-done:
		case <-timer.C:
			scope.quarantined = true
			retainSnapshotResource(command)
			return response, ErrRestoreUncertain
		}
	}
	if command.ProcessState == nil {
		scope.quarantined = true
		retainSnapshotResource(command)
		return response, ErrRestoreUncertain
	}
	if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); !ok || !status.Exited() && !status.Signaled() {
		scope.quarantined = true
		retainSnapshotResource(command)
		return response, ErrRestoreUncertain
	}
	if closeError != nil {
		return response, ErrRestoreUncertain
	}
	if result.response.Status == "uncertain" {
		return response, ErrRestoreUncertain
	}
	if ctx.Err() != nil {
		return response, ctx.Err()
	}
	if result.readError != nil || result.response.Nonce != request.Nonce {
		return response, ErrRestoreInvalid
	}
	switch result.response.Status {
	case "key":
		return response, ErrRestoreKey
	case "budget":
		return response, ErrRestoreBudget
	case "ok":
		if result.waitError == nil {
			return result.response, nil
		}
	}
	return response, ErrRestoreInvalid
}

func restoreDatabaseFileIdentity(file *os.File) (restoreDatabaseIdentity, error) {
	var info unix.Stat_t
	if unix.Fstat(int(file.Fd()), &info) != nil {
		return restoreDatabaseIdentity{}, ErrRestoreInvalid
	}
	return restoreDatabaseIdentity{Device: uint64(info.Dev), Inode: info.Ino}, nil
}

func restoreDatabaseReadOnly(file *os.File) bool {
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	return err == nil && flags&unix.O_ACCMODE == unix.O_RDONLY && flags&unix.O_PATH == 0
}

func restoreDatabasePin(parent, database *os.File) (info os.FileInfo, returnedError error) {
	if _, err := snapshotSourceParent(parent); err != nil || !restoreDatabaseReadOnly(parent) || !restoreDatabaseReadOnly(database) {
		return nil, ErrRestoreInvalid
	}
	info, err := database.Stat()
	if err != nil || !snapshotSourcePrivateFile(info) || info.Size() < 512 {
		return nil, ErrRestoreInvalid
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		var sidecar unix.Stat_t
		if !errors.Is(unix.Fstatat(int(parent.Fd()), "server.db"+suffix, &sidecar, unix.AT_SYMLINK_NOFOLLOW), unix.ENOENT) {
			return nil, ErrRestoreInvalid
		}
	}
	named, err := snapshotSourceOpen(parent, "server.db")
	if err != nil {
		return nil, ErrRestoreInvalid
	}
	defer func() {
		if named.file.Close() != nil {
			info = nil
			returnedError = ErrRestoreUncertain
		}
	}()
	namedInfo, err := named.file.Stat()
	if err != nil || !snapshotSourcePrivateFile(namedInfo) || !snapshotSourceSame(info, namedInfo) {
		return nil, ErrRestoreInvalid
	}
	return info, nil
}

func restoreDatabaseHash(ctx context.Context, file *os.File, size int64) ([32]byte, error) {
	hasher := sha256.New()
	reader := &snapshotContextReader{ctx: ctx, reader: io.NewSectionReader(file, 0, size+1)}
	count, err := io.Copy(hasher, reader)
	if err != nil || count != size {
		return [32]byte{}, restoreDatabaseError(errors.Join(err, ErrRestoreInvalid))
	}
	var digest [32]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, ctx.Err()
}
