package backup

import (
	"context"
	"crypto/sha256"
	"io"
	"reflect"

	aferos3 "github.com/fclairamb/afero-s3"
)

// CopyExact copies only the ciphertext bound to ref into a borrowed writer.
// All output remains an untrusted candidate until Verified and a nil error are
// returned, including after all response bodies close successfully. On failure
// output may contain partial or complete untrusted bytes; it cannot be rolled
// back. Callers must stage the candidate and decide whether to use it from the
// final result. CopyExact never closes output. A writer must return from Write
// for cancellation and target Close to finish joining the operation.
func (target *S3Target) CopyExact(ctx context.Context, ref ObjectRef, output io.Writer) (result VerifyResult, err error) {
	operation, err := target.begin(ctx, "verify", 3)
	if err != nil {
		return VerifyUnknown, err
	}
	defer func() {
		operation.mutex.Lock()
		bodies := append([]*boundedBody(nil), operation.bodies...)
		operation.mutex.Unlock()
		for _, body := range bodies {
			_ = body.Close()
		}
		if !operation.healthy() {
			result, err = VerifyUnknown, publicError(operation.ctx, ErrUnavailable)
		}
		operation.end()
	}()
	data, err := operation.bind(ref)
	if err != nil {
		return VerifyUnknown, err
	}
	writerValue := reflect.ValueOf(output)
	switch writerValue.Kind() {
	case reflect.Invalid:
		return VerifyUnknown, ErrInvalid
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if writerValue.IsNil() {
			return VerifyUnknown, ErrInvalid
		}
	}
	if _, result, err := operation.probe(); result != Verified || err != nil {
		return result, err
	}
	options := target.options.read()
	adapter := aferos3.NewFsFromClient(options.bucket, operation.client("HeadObject", "GetObject"))
	file, err := adapter.Open(operation.key)
	if err != nil {
		return VerifyUnknown, safeServiceError(operation.ctx, err)
	}
	digest := sha256.New()
	var buffer [32 << 10]byte
	size, readError := io.CopyBuffer(io.MultiWriter(output, digest), file, buffer[:])
	closeError := file.Close()
	if readError != nil || closeError != nil || !operation.healthy() {
		return VerifyUnknown, publicError(operation.ctx, ErrUnavailable)
	}
	if size != data.size || [32]byte(digest.Sum(nil)) != data.digest {
		return VerifyConflict, nil
	}
	return Verified, nil
}
