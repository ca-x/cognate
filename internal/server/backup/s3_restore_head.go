package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	aferos3 "github.com/fclairamb/afero-s3"
)

type restoreHeadOps struct {
	open    func(*sealDirectory, string, bool) (*os.File, error)
	sync    func(*os.File) error
	close   func(*os.File) error
	unlink  func(*sealDirectory, string) error
	discard func(*sealDirectory) error
}

func defaultRestoreHeadOps() restoreHeadOps {
	return restoreHeadOps{sealOpen, (*os.File).Sync, (*os.File).Close, sealUnlink, discardSealDirectory}
}

type restoreHeadFile struct {
	name     string
	file     *os.File
	identity os.FileInfo
	readOnly bool
}

type restoreHeadScope struct {
	directory    *sealDirectory
	files        [2]restoreHeadFile
	ops          restoreHeadOps
	cleanupError error
}

// WithRestoreHead lends bootstrap and complete, in that order, as read-only,
// unauthenticated candidates from the canonical expected attempt. The caller
// supplies a private parent directory FD, which is never closed. No trusted or
// activation capability is issued: consume must authenticate with BeginOpen and
// only stage candidates, never activate them inside this scope. Success also
// requires all subsequent local closes and cleanup to succeed.
//
// consume must cooperate with cancellation, return before Close can finish,
// and neither close, duplicate nor retain either borrowed head FD. All remote
// response bodies close before consume starts; both local FDs remain owned by
// this scope until consume returns, including during cancellation/target Close.
func (target *S3Target) WithRestoreHead(ctx context.Context, parent *os.File, expected Attempt, consume func(context.Context, *os.File, *os.File) error) error {
	return target.withRestoreHead(ctx, parent, expected, consume, defaultRestoreHeadOps())
}

func (target *S3Target) withRestoreHead(ctx context.Context, parent *os.File, expected Attempt, consume func(context.Context, *os.File, *os.File) error, ops restoreHeadOps) (returnedError error) {
	operation, err := target.begin(ctx, "restore-head", 4)
	if err != nil {
		return err
	}
	scope := &restoreHeadScope{ops: ops}
	defer func() {
		responseCloseFailed := operation.closeRestoreHeadResponses()
		if !operation.healthy() {
			returnedError = mergeSealCleanup(returnedError, publicError(operation.ctx, ErrUnavailable))
		}
		if responseCloseFailed {
			returnedError = mergeSealCleanup(returnedError, ErrUnavailable)
		}
		cleanupError := scope.cleanup()
		if cleanupError != nil {
			returnedError = mergeSealCleanup(returnedError, cleanupError)
			operation.quarantineRestoreHead()
		}
		if operation.ctx.Err() != nil {
			if !responseCloseFailed {
				returnedError = mergeSealCleanup(returnedError, operation.ctx.Err())
			}
		}
		operation.end()
	}()
	if parent == nil || consume == nil || expected.read == nil {
		return ErrInvalid
	}
	identity := expected.read()
	canonical, err := NewAttempt(identity.instance, identity.backup, identity.attempt)
	if err != nil || identity.instance != target.options.read().instance {
		return ErrInvalid
	}
	scope.directory, err = newSealDirectory(parent)
	if err != nil {
		if errors.Is(err, ErrSealCleanup) || errors.Is(err, ErrSealUncertain) {
			operation.quarantineRestoreHead()
			return err
		}
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnsupported) {
			return err
		}
		return publicError(operation.ctx, ErrUnavailable)
	}
	for index, kind := range []ObjectKind{Bootstrap, Complete} {
		if err := scope.download(operation, canonical, kind, index); err != nil {
			return err
		}
	}
	if scope.ops.sync(scope.directory.root) != nil {
		return ErrSealCleanup
	}
	if operation.closeRestoreHeadResponses() || !operation.healthy() {
		return publicError(operation.ctx, ErrUnavailable)
	}
	if !restoreHeadDirectoryPinned(scope.directory) {
		return ErrSealCleanup
	}
	for _, head := range scope.files {
		if sealRepin(head.file, head.identity) != nil {
			return ErrSealCleanup
		}
	}
	consumeError := consume(operation.ctx, scope.files[0].file, scope.files[1].file)
	if errors.Is(consumeError, ErrSealUncertain) {
		return ErrSealUncertain
	}
	if errors.Is(consumeError, ErrSealCleanup) {
		return ErrSealCleanup
	}
	return publicError(operation.ctx, consumeError)
}

func (operation *operation) closeRestoreHeadResponses() bool {
	operation.mutex.Lock()
	bodies := append([]*boundedBody(nil), operation.bodies...)
	operation.mutex.Unlock()
	failed := false
	for _, body := range bodies {
		if body.Close() != nil {
			failed = true
		}
	}
	return failed
}

func (operation *operation) quarantineRestoreHead() {
	target := operation.target
	target.mutex.Lock()
	target.closed, target.closeFailed = true, true
	for active := range target.active {
		active.cancel()
	}
	target.mutex.Unlock()
}

func (scope *restoreHeadScope) download(operation *operation, expected Attempt, kind ObjectKind, index int) (returnedError error) {
	maximum, name := int64(156), "bootstrap"
	if kind == Complete {
		maximum, name = 16<<10, "complete"
	}
	ref, err := NewObjectRef(expected, kind, 0, maximum, [32]byte{})
	if err != nil {
		return err
	}
	if _, err := operation.bind(ref); err != nil {
		return err
	}
	options := operation.target.options.read()
	adapter := aferos3.NewFsFromClient(options.bucket, operation.client("HeadObject", "GetObject"))
	remote, err := adapter.Open(operation.key)
	if err != nil {
		return safeServiceError(operation.ctx, err)
	}
	defer func() {
		closeError := remote.Close()
		if operation.closeRestoreHeadResponses() || closeError != nil {
			returnedError = ErrUnavailable
		} else if !operation.healthy() {
			returnedError = publicError(operation.ctx, ErrUnavailable)
		}
	}()
	size := operation.size
	if !operation.healthy() || size <= 0 || size > maximum || kind == Bootstrap && size != 156 {
		return publicError(operation.ctx, ErrUnavailable)
	}
	file, err := scope.ops.open(scope.directory, name, true)
	if err != nil {
		return publicError(operation.ctx, ErrUnavailable)
	}
	head := &scope.files[index]
	head.name, head.file = name, file
	head.identity, err = file.Stat()
	if err != nil {
		return ErrSealCleanup
	}
	var buffer [4096]byte
	count, readError := io.CopyBuffer(&sealLimitWriter{ctx: operation.ctx, writer: file, remaining: size}, remote, buffer[:])
	if readError != nil || count != size || !operation.healthy() {
		return publicError(operation.ctx, ErrUnavailable)
	}
	if scope.ops.sync(file) != nil {
		return publicError(operation.ctx, ErrUnavailable)
	}
	head.identity, err = sealBorrow(file)
	if err != nil {
		return ErrSealCleanup
	}
	if scope.closeFile(head) != nil {
		return ErrSealUncertain
	}
	head.file, err = scope.ops.open(scope.directory, name, false)
	if err != nil {
		return publicError(operation.ctx, ErrUnavailable)
	}
	head.readOnly = true
	if sealRepin(head.file, head.identity) != nil {
		return ErrSealCleanup
	}
	return nil
}

func (scope *restoreHeadScope) closeFile(head *restoreHeadFile) error {
	if head.file == nil {
		return nil
	}
	file := head.file
	head.file = nil
	if scope.ops.close(file) == nil {
		return nil
	}
	sealUnresolved.Lock()
	sealUnresolved.files = append(sealUnresolved.files, file)
	sealUnresolved.Unlock()
	scope.cleanupError = ErrSealUncertain
	return ErrSealUncertain
}

func restoreHeadDirectoryPinned(directory *sealDirectory) bool {
	for _, file := range []*os.File{directory.parent, directory.root} {
		current, err := file.Stat()
		if err != nil || !current.IsDir() || current.Mode().Perm()&0077 != 0 || current.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return false
		}
		named, err := os.Lstat(file.Name())
		if err != nil || !os.SameFile(current, named) || named.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func (scope *restoreHeadScope) cleanup() error {
	if scope.directory == nil {
		return nil
	}
	for index := range scope.files {
		head := &scope.files[index]
		if head.readOnly && head.file != nil && head.identity != nil && sealRepin(head.file, head.identity) != nil {
			scope.cleanupError = mergeSealCleanup(scope.cleanupError, ErrSealCleanup)
		}
		_ = scope.closeFile(head)
		if head.name == "" {
			continue
		}
		named, err := os.Lstat(filepath.Join(scope.directory.root.Name(), head.name))
		if err != nil || head.identity == nil || !os.SameFile(named, head.identity) || !restoreHeadDirectoryPinned(scope.directory) {
			scope.cleanupError = mergeSealCleanup(scope.cleanupError, ErrSealCleanup)
			continue
		}
		if scope.ops.unlink(scope.directory, head.name) != nil {
			scope.cleanupError = mergeSealCleanup(scope.cleanupError, ErrSealCleanup)
		}
	}
	if scope.ops.sync(scope.directory.root) != nil {
		scope.cleanupError = mergeSealCleanup(scope.cleanupError, ErrSealCleanup)
	}
	if !restoreHeadDirectoryPinned(scope.directory) {
		scope.cleanupError = mergeSealCleanup(scope.cleanupError, ErrSealCleanup)
	}
	if scope.cleanupError != nil {
		scope.directory.failed = true
		retainSealDirectory(scope.directory)
		return scope.cleanupError
	}
	if err := scope.ops.discard(scope.directory); err != nil {
		if !scope.directory.failed {
			scope.directory.failed = true
			retainSealDirectory(scope.directory)
		}
		if errors.Is(err, ErrSealUncertain) {
			return ErrSealUncertain
		}
		return ErrSealCleanup
	}
	return nil
}
