package restore

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/ca-x/cognate/internal/server/backup"
	"golang.org/x/sys/unix"
)

type sourceS3Input struct {
	parent, file         *os.File
	parentInfo, fileInfo os.FileInfo
	name                 string
	ops                  sourceS3FileOps
	failure              error
}

var sourceS3Retained struct {
	sync.Mutex
	files []*os.File
}

func sourceS3Retain(files ...*os.File) {
	sourceS3Retained.Lock()
	defer sourceS3Retained.Unlock()
	for _, file := range files {
		if file != nil {
			sourceS3Retained.files = append(sourceS3Retained.files, file)
		}
	}
}

func newSourceS3Input(parent *os.File, operations sourceS3FileOps) (result *sourceS3Input, returnedError error) {
	if parent == nil || operations.sync == nil || operations.close == nil || operations.unlink == nil {
		return nil, ErrCandidateInvalid
	}
	name := parent.Name()
	canonical, err := filepath.EvalSymlinks(name)
	info, statError := parent.Stat()
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || err != nil || canonical != name || statError != nil || !candidateDirectoryPinned(parent, info) {
		return nil, ErrCandidateInvalid
	}
	owned, err := candidateOpenDirectory(parent, ".")
	if err != nil {
		return nil, err
	}
	input := &sourceS3Input{parent: owned, parentInfo: info, name: "restore-s3-input-" + rand.Text(), ops: operations}
	defer func() {
		if returnedError != nil {
			returnedError = candidateMerge(returnedError, input.cleanup())
		}
	}()
	descriptor, err := unix.Openat(int(owned.Fd()), input.name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, ErrCandidateInvalid
	}
	input.file = os.NewFile(uintptr(descriptor), filepath.Join(name, input.name))
	input.fileInfo, err = input.file.Stat()
	if err != nil || input.check() != nil {
		return nil, ErrCandidateInvalid
	}
	return input, nil
}

func (input *sourceS3Input) check() error {
	if input.failure != nil {
		return input.failure
	}
	if !candidateDirectoryPinned(input.parent, input.parentInfo) || input.file == nil {
		return ErrCandidateInvalid
	}
	info, err := input.file.Stat()
	named, nameError := os.Lstat(input.file.Name())
	if err != nil || nameError != nil || !candidatePrivate(info, false) || !candidatePrivate(named, false) || input.fileInfo == nil || !os.SameFile(info, input.fileInfo) || !os.SameFile(info, named) {
		return ErrCandidateInvalid
	}
	return nil
}

func (input *sourceS3Input) accept(ctx context.Context, maximum int64, copyExact func(io.Writer) (backup.VerifyResult, error), accept func(*os.File) error) (returnedError error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := input.check(); err != nil {
		return err
	}
	if input.file.Truncate(0) != nil {
		return ErrCandidateInvalid
	}
	if _, err := input.file.Seek(0, io.SeekStart); err != nil {
		return ErrCandidateInvalid
	}
	writer := &sourceS3Writer{ctx: ctx, output: input.file, remaining: maximum}
	verified, err := copyExact(writer)
	if err != nil {
		return sourceS3Error(err)
	}
	if writer.limited {
		return ErrCandidateBudget
	}
	if verified != backup.Verified || writer.bytes <= 0 || input.check() != nil {
		return ErrCandidateInvalid
	}
	if input.ops.sync(input.file) != nil {
		return ErrCandidateCleanup
	}
	descriptor, err := unix.Openat(int(input.parent.Fd()), input.name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrCandidateInvalid
	}
	file := os.NewFile(uintptr(descriptor), input.file.Name())
	defer func() {
		if input.ops.close(file) != nil {
			input.failure = ErrCandidateUncertain
			sourceS3Retain(file)
			returnedError = ErrCandidateUncertain
		}
	}()
	info, err := file.Stat()
	if err != nil || !candidatePrivate(info, false) || !os.SameFile(info, input.fileInfo) || info.Size() != writer.bytes || input.check() != nil {
		return ErrCandidateInvalid
	}
	if err := accept(file); err != nil {
		return err
	}
	if err := input.check(); err != nil {
		return err
	}
	return ctx.Err()
}

func sourceS3Unlink(parent *os.File, name string) error {
	return unix.Unlinkat(int(parent.Fd()), name, 0)
}

func (input *sourceS3Input) cleanup() error {
	if input.parent == nil {
		return nil
	}
	if errors.Is(input.failure, ErrCandidateUncertain) {
		sourceS3Retain(input.file, input.parent)
		return ErrCandidateUncertain
	}
	var result error
	if input.file != nil {
		pinned := input.check() == nil
		if input.ops.close(input.file) != nil {
			sourceS3Retain(input.file, input.parent)
			return ErrCandidateUncertain
		}
		if !pinned || input.ops.unlink(input.parent, input.name) != nil || input.ops.sync(input.parent) != nil {
			result = ErrCandidateCleanup
		}
	}
	if input.ops.close(input.parent) != nil {
		sourceS3Retain(input.parent)
		return ErrCandidateUncertain
	}
	return result
}
