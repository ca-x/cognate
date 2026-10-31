package restore

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"

	"github.com/ca-x/cognate/internal/server/backup"
)

func S3CandidateSource(options backup.S3Options, parent *os.File, expected backup.Attempt, recoveryID [16]byte, recoveryMaterial []byte, limits backup.SealLimits) CandidateSource {
	return s3CandidateSource(options, parent, expected, recoveryID, recoveryMaterial, limits, defaultSourceS3Ops())
}

type sourceS3Target interface {
	WithRestoreHead(context.Context, *os.File, backup.Attempt, func(context.Context, *os.File, *os.File) error) error
	CopyExact(context.Context, backup.ObjectRef, io.Writer) (backup.VerifyResult, error)
	Close() error
}

type sourceS3Ops struct {
	target       func(backup.S3Options) (sourceS3Target, error)
	destroy      func(backup.RecoveryKey) error
	closeEngine  func(backup.SealEngine) error
	closeSession func(backup.OpenSession) error
	closeBundle  func(backup.CryptoVerifiedBundle) error
	files        sourceS3FileOps
}

type sourceS3FileOps struct {
	sync   func(*os.File) error
	close  func(*os.File) error
	unlink func(*os.File, string) error
}

func defaultSourceS3Ops() sourceS3Ops {
	return sourceS3Ops{
		target:  func(options backup.S3Options) (sourceS3Target, error) { return backup.NewS3Target(options) },
		destroy: backup.RecoveryKey.Destroy, closeEngine: backup.SealEngine.Close,
		closeSession: backup.OpenSession.Close, closeBundle: backup.CryptoVerifiedBundle.Close,
		files: sourceS3FileOps{(*os.File).Sync, (*os.File).Close, sourceS3Unlink},
	}
}

func s3CandidateSource(options backup.S3Options, parent *os.File, expected backup.Attempt, recoveryID [16]byte, recoveryMaterial []byte, limits backup.SealLimits, operations sourceS3Ops) CandidateSource {
	return func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) (returnedError error) {
		if ctx == nil || consume == nil || parent == nil || limits.OperationTimeout <= 0 || limits.InputStagingBytes <= 0 {
			return ErrCandidateInvalid
		}
		if err := ctx.Err(); err != nil {
			return candidateError(err)
		}
		ctx, cancel := context.WithTimeout(ctx, limits.OperationTimeout)
		defer cancel()
		defer func() { returnedError = sourceS3Merge(returnedError, ctx.Err()) }()
		input, err := newSourceS3Input(parent, operations.files)
		if err != nil {
			return err
		}
		defer func() { returnedError = sourceS3Merge(returnedError, input.cleanup()) }()
		key, err := backup.ImportRecoveryKey(recoveryID, recoveryMaterial)
		if err != nil {
			return err
		}
		defer func() { returnedError = sourceS3Merge(returnedError, sourceS3CloseError(operations.destroy(key))) }()
		engine, err := backup.NewSealEngine(parent, key, limits)
		if err != nil {
			return err
		}
		defer func() {
			returnedError = sourceS3Merge(returnedError, sourceS3CloseError(operations.closeEngine(engine)))
		}()
		target, err := operations.target(options)
		if err != nil {
			return err
		}
		defer func() { returnedError = sourceS3Merge(returnedError, sourceS3CloseError(target.Close())) }()
		var session backup.OpenSession
		defer func() {
			returnedError = sourceS3Merge(returnedError, sourceS3CloseError(operations.closeSession(session)))
		}()
		var openError error
		err = target.WithRestoreHead(ctx, parent, expected, func(headContext context.Context, bootstrap, complete *os.File) error {
			if err := headContext.Err(); err != nil {
				return err
			}
			session, openError = engine.BeginOpen(ctx, bootstrap, complete, expected)
			return errors.Join(openError, headContext.Err())
		})
		if err != nil || openError != nil {
			return errors.Join(err, openError)
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			ref, err := session.NextRef(ctx)
			if err != nil {
				return err
			}
			if reflect.ValueOf(ref).IsZero() {
				break
			}
			if err := input.accept(ctx, limits.InputStagingBytes, func(output io.Writer) (backup.VerifyResult, error) {
				return target.CopyExact(ctx, ref, output)
			}, func(file *os.File) error { return session.AcceptFile(ctx, file) }); err != nil {
				return err
			}
		}
		bundle, err := session.Finish(ctx)
		if err != nil {
			return err
		}
		defer func() {
			returnedError = sourceS3Merge(returnedError, sourceS3CloseError(operations.closeBundle(bundle)))
		}()
		if err := ctx.Err(); err != nil {
			return err
		}
		return consume(ctx, bundle)
	}
}

func sourceS3Error(err error) error {
	if errors.Is(err, backup.ErrUnavailable) {
		return candidateMerge(err, ErrCandidateUncertain)
	}
	return candidateError(err)
}

func sourceS3Merge(previous, next error) error {
	return candidateMerge(sourceS3Error(previous), sourceS3Error(next))
}

func sourceS3CloseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, backup.ErrSealCleanup) || errors.Is(err, ErrCandidateCleanup) {
		return candidateError(err)
	}
	return ErrCandidateUncertain
}

type sourceS3Writer struct {
	ctx       context.Context
	output    io.Writer
	remaining int64
	bytes     int64
	limited   bool
}

func (writer *sourceS3Writer) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > writer.remaining {
		writer.limited = true
		return 0, ErrCandidateBudget
	}
	count, err := writer.output.Write(data)
	writer.remaining -= int64(count)
	writer.bytes += int64(count)
	if count != len(data) && err == nil {
		err = io.ErrShortWrite
	}
	return count, err
}
