//go:build linux

package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"golang.org/x/sys/unix"
)

type restoreRevokeIO struct {
	prepare func(*exec.Cmd)
	wait    func(*exec.Cmd) error
	close   func(*os.File) error
	sync    func(*os.File) error
	read    func(io.Reader, int64, any) error
}
type restoreRevokeScope struct {
	files     []*os.File
	guard     *os.File
	ops       restoreRevokeIO
	uncertain bool
}

func restoreRevokeRun(ctx context.Context, root *runtimegen.OfflineRoot, parent, database *os.File, key []byte, before RestoreDatabaseEvidence, operation [16]byte, when time.Time, limits LocalSnapshotLimits) (RestoreRevocationEvidence, error) {
	return restoreRevokeWithIO(ctx, root, parent, database, key, before, operation, when, limits, restoreRevokeIO{})
}

func restoreRevokeWithIO(ctx context.Context, root *runtimegen.OfflineRoot, parent, database *os.File, key []byte, before RestoreDatabaseEvidence, operation [16]byte, when time.Time, limits LocalSnapshotLimits, ops restoreRevokeIO) (evidence RestoreRevocationEvidence, returnedError error) {
	if ctx == nil || root == nil || parent == nil || database == nil || !limits.valid() || limits.MaxDBBytes > (1<<40) || len(key) != 32 {
		return evidence, ErrRestoreInvalid
	}
	if before.Valid() && 3*limits.MaxDBBytes+65536 > limits.MaxStagingBytes {
		return evidence, ErrRestoreBudget
	}
	if ops.wait == nil {
		ops.wait = (*exec.Cmd).Wait
	}
	if ops.close == nil {
		ops.close = (*os.File).Close
	}
	if ops.sync == nil {
		ops.sync = (*os.File).Sync
	}
	if ops.read == nil {
		ops.read = restoreReadMessage
	}
	ctx, cancel := context.WithTimeout(ctx, limits.OperationTimeout)
	defer cancel()
	scope := &restoreRevokeScope{ops: ops}
	var facts restoreRevocationFacts
	loanReturned := false
	defer func() {
		if !loanReturned && scope.guard != nil {
			retainSnapshotResource(scope)
		}
	}()
	returnedError = root.WithWorkerFiles(ctx, func(rootFile, lock *os.File) (loanError error) {
		defer func() {
			for _, file := range scope.files {
				if file != nil {
					loanError = errors.Join(loanError, scope.close(file))
				}
			}
			if errors.Is(restoreDatabaseError(loanError), ErrRestoreUncertain) || scope.uncertain {
				scope.uncertain = true
				retainSnapshotResource(scope)
				loanError = ErrRestoreUncertain
			}
		}()
		var err error
		scope.guard, err = restoreRevokeDuplicate(lock)
		if err != nil {
			return err
		}
		request := restoreRevokeRequest{Mode: "inspect", Operation: operation, RevokedAt: when, Limits: limits}
		if before.Valid() {
			request.Mode, request.Before = "revoke", *before.value
		}
		if _, err := rand.Read(request.Nonce[:]); err != nil {
			return ErrRestoreInvalid
		}
		for index, file := range []*os.File{rootFile, lock, parent, database} {
			identity, err := restoreDatabaseFileIdentity(file)
			if err != nil {
				return err
			}
			switch index {
			case 0:
				request.Root = identity
			case 1:
				request.Lock = identity
			case 2:
				request.Parent = identity
			case 3:
				request.Database = identity
			}
		}
		if err := restoreRevokeNamespace(rootFile, lock, parent, database, operation); err != nil {
			return err
		}
		initial, err := restoreDatabasePin(parent, database)
		if err != nil {
			return err
		}
		if initial.Size() > limits.MaxDBBytes || initial.Size() > limits.MaxStagingBytes {
			return ErrRestoreBudget
		}
		initialHash, err := restoreDatabaseHash(ctx, database, initial.Size())
		if err != nil {
			return err
		}
		if before.Valid() && (initial.Size() != before.value.DatabaseBytes || initialHash != before.value.DatabaseHash) {
			return ErrRestoreInvalid
		}
		response, err := scope.run(ctx, rootFile, parent, database, key, request)
		if err != nil {
			return err
		}
		facts = response.Facts
		if before.Valid() {
			if facts.Protected == [32]byte{} || !restoreRevokeSameFacts(*before.value, facts.Database) {
				return ErrRestoreUncertain
			}
			request.Mode, request.Before, request.Protected = "postinspect", facts.Database, facts.Protected
			response, err = scope.run(ctx, rootFile, parent, database, key, request)
			if err != nil || response.Facts != facts {
				return ErrRestoreUncertain
			}
		}
		final, err := restoreDatabasePin(parent, database)
		if err != nil {
			return err
		}
		if !os.SameFile(initial, final) || facts.Database.DatabaseBytes != final.Size() {
			return ErrRestoreInvalid
		}
		finalHash, err := restoreDatabaseHash(ctx, database, final.Size())
		if err != nil {
			return err
		}
		if facts.Database.DatabaseHash != finalHash || !before.Valid() && (initialHash != finalHash || !snapshotSourceSame(initial, final)) {
			return ErrRestoreInvalid
		}
		if scope.ops.sync(database) != nil || scope.ops.sync(parent) != nil {
			return ErrRestoreUncertain
		}
		if err := restoreRevokeNamespace(rootFile, lock, parent, database, operation); err != nil {
			return err
		}
		return ctx.Err()
	})
	loanReturned = true
	if scope.guard != nil {
		if scope.uncertain || errors.Is(restoreDatabaseError(returnedError), ErrRestoreUncertain) {
			retainSnapshotResource(scope)
		} else {
			releaseCtx := ctx
			if ctx.Err() != nil {
				var stop context.CancelFunc
				releaseCtx, stop = context.WithTimeout(context.Background(), limits.DrainTimeout)
				defer stop()
			}
			releaseError := root.WithRetainedWorkerFiles(releaseCtx, func(_, _ *os.File) error {
				if scope.ops.close(scope.guard) != nil {
					return runtimegen.ErrUncertain
				}
				scope.guard = nil
				return nil
			})
			if releaseError != nil {
				retainSnapshotResource(scope)
				returnedError = errors.Join(returnedError, releaseError)
			}
		}
	}
	if errors.Is(returnedError, runtimegen.ErrUncertain) {
		returnedError = ErrRestoreUncertain
	}
	if errors.Is(returnedError, runtimegen.ErrUnsupported) {
		returnedError = ErrRestoreUnsupported
	}
	if returnedError != nil {
		return RestoreRevocationEvidence{}, restoreDatabaseError(returnedError)
	}
	return RestoreRevocationEvidence{value: &facts}, nil
}

func restoreRevokeDuplicate(file *os.File) (*os.File, error) {
	if file == nil {
		return nil, ErrRestoreInvalid
	}
	descriptor, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, ErrRestoreInvalid
	}
	return os.NewFile(uintptr(descriptor), file.Name()), nil
}
func (scope *restoreRevokeScope) close(file *os.File) error {
	for index, owned := range scope.files {
		if owned == file {
			scope.files[index] = nil
			break
		}
	}
	if scope.ops.close(file) != nil {
		scope.uncertain = true
		retainSnapshotResource(file)
		return ErrRestoreUncertain
	}
	return nil
}
func (scope *restoreRevokeScope) add(file *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, ErrRestoreInvalid
	}
	scope.files = append(scope.files, file)
	return file, nil
}
func (scope *restoreRevokeScope) pipe() (*os.File, *os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, ErrRestoreInvalid
	}
	scope.files = append(scope.files, reader, writer)
	return reader, writer, nil
}

func (scope *restoreRevokeScope) run(ctx context.Context, root, parent, database *os.File, key []byte, request restoreRevokeRequest) (response restoreRevokeResponse, returnedError error) {
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
	if errors.Join(scope.close(inputWriter), scope.close(keyWriter)) != nil {
		return response, ErrRestoreUncertain
	}
	command := exec.Command("/proc/self/fd/5", restoreRevokeWorkerMode)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	command.Stdin, command.Stdout, command.Stderr = quiet, quiet, quiet
	command.ExtraFiles = []*os.File{input, outputWriter, image, parent, database, keyReader, scope.guard, root}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if scope.ops.prepare != nil {
		scope.ops.prepare(command)
	}
	if ctx.Err() != nil {
		return response, ctx.Err()
	}
	if command.Start() != nil {
		return response, ErrRestoreInvalid
	}
	closeError := errors.Join(scope.close(input), scope.close(outputWriter), scope.close(keyReader))
	type outcome struct {
		response   restoreRevokeResponse
		read, wait error
	}
	done := make(chan outcome, 1)
	go func() {
		var result outcome
		result.read = scope.ops.read(output, 16384, &result.response)
		if result.read != nil {
			_ = command.Process.Kill()
		}
		result.wait = scope.ops.wait(command)
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
			scope.uncertain = true
			retainSnapshotResource(command)
			return response, ErrRestoreUncertain
		}
	}
	if command.ProcessState == nil {
		scope.uncertain = true
		retainSnapshotResource(command)
		return response, ErrRestoreUncertain
	}
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Exited() && !status.Signaled() || result.wait != nil && command.ProcessState.Success() {
		return response, ErrRestoreUncertain
	}
	if closeError != nil || result.response.Status == "uncertain" {
		return response, ErrRestoreUncertain
	}
	if result.read != nil || result.response.Nonce != request.Nonce {
		return response, ErrRestoreUncertain
	}
	switch result.response.Status {
	case "ok":
		if result.wait == nil {
			return result.response, nil
		}
	case "key":
		return response, ErrRestoreKey
	case "budget":
		return response, ErrRestoreBudget
	case "unsupported":
		return response, ErrRestoreUnsupported
	case "invalid", "rolledback":
		return response, ErrRestoreInvalid
	}
	return response, ErrRestoreUncertain
}

func restoreRevokeNamespace(root, lock, parent, database *os.File, operation [16]byte) (returnedError error) {
	rootInfo, err := root.Stat()
	rootName, nameError := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(root.Fd()), 10))
	namedRoot, namedError := os.Lstat(rootName)
	if err != nil || nameError != nil || namedError != nil || !os.SameFile(rootInfo, namedRoot) || validateServerDirectory(rootName) != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0700 || !restoreDatabaseReadOnly(root) {
		return ErrRestoreInvalid
	}
	var held, named unix.Stat_t
	if unix.Fstat(int(lock.Fd()), &held) != nil || held.Mode&unix.S_IFMT != unix.S_IFREG || held.Mode&07777 != 0600 || held.Uid != uint32(os.Geteuid()) || held.Nlink != 1 || held.Size != 0 || unix.Fstatat(int(root.Fd()), ".runtime.lock", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || held.Dev != named.Dev || held.Ino != named.Ino {
		return ErrRestoreInvalid
	}
	descriptor, err := unix.Openat(int(parent.Fd()), "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrRestoreInvalid
	}
	operationParent := os.NewFile(uintptr(descriptor), "restore operation")
	defer func() {
		if operationParent.Close() != nil {
			returnedError = ErrRestoreUncertain
		}
	}()
	var operationStat, candidateStat unix.Stat_t
	if unix.Fstat(int(operationParent.Fd()), &operationStat) != nil || operationStat.Mode&unix.S_IFMT != unix.S_IFDIR || operationStat.Mode&07777 != 0700 || operationStat.Uid != uint32(os.Geteuid()) || operationStat.Nlink == 0 {
		return ErrRestoreInvalid
	}
	name := ".restore-" + hex.EncodeToString(operation[:])
	if operation == [16]byte{} {
		link, err := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(operationParent.Fd()), 10))
		if err != nil {
			return ErrRestoreInvalid
		}
		name = filepath.Base(link)
		if len(name) != 41 || name[:9] != ".restore-" {
			return ErrRestoreInvalid
		}
		decoded, err := hex.DecodeString(name[9:])
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != name[9:] {
			return ErrRestoreInvalid
		}
	}
	if unix.Fstatat(int(root.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || named.Dev != operationStat.Dev || named.Ino != operationStat.Ino {
		return ErrRestoreInvalid
	}
	if unix.Fstat(int(parent.Fd()), &candidateStat) != nil || unix.Fstatat(int(operationParent.Fd()), "candidate", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || candidateStat.Dev != named.Dev || candidateStat.Ino != named.Ino {
		return ErrRestoreInvalid
	}
	_, err = restoreDatabasePin(parent, database)
	return err
}
