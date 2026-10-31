package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"sync"
)

func (engine SealEngine) SealExtentSource(ctx context.Context, attempt SealAttempt, ordinal uint32, length int64, expectedSHA256 [32]byte, produce func(context.Context, io.Writer) error) (result SealedFile, returnedError error) {
	if engine.get == nil || attempt.get == nil || produce == nil {
		return SealedFile{}, ErrInvalid
	}
	state, data := engine.get(), attempt.get()
	if data.engine != state {
		return SealedFile{}, ErrInvalid
	}
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return SealedFile{}, err
	}
	defer release()
	data.mutex.Lock()
	if data.closed || data.poison || data.phase != 0 || ordinal >= state.limits.MaxObjects || data.used[0][ordinal/64]&(1<<(ordinal%64)) != 0 {
		data.mutex.Unlock()
		return SealedFile{}, ErrInvalid
	}
	data.used[0][ordinal/64] |= 1 << (ordinal % 64)
	data.active++
	secret := data.dek
	data.mutex.Unlock()
	defer clear(secret[:])
	scoped, attemptCancel := context.WithCancel(scoped)
	stop := context.AfterFunc(data.ctx, attemptCancel)
	defer stop()
	defer attemptCancel()
	defer func() {
		data.mutex.Lock()
		data.active--
		close(data.changed)
		data.changed = make(chan struct{})
		data.mutex.Unlock()
	}()
	if length <= 0 || length > sealExtentLimit || length > state.limits.InputStagingBytes {
		return SealedFile{}, ErrSealBudget
	}
	header := sealHeader{purpose: 1, attempt: data.identity, ordinal: ordinal, length: length}
	if _, err = rand.Read(header.nonce[:]); err != nil {
		return SealedFile{}, ErrUnavailable
	}
	defer func() {
		if returnedError != nil || result.get == nil {
			data.mutex.Lock()
			data.poison = true
			data.mutex.Unlock()
		}
	}()
	cipherLength, _ := sealCipherSize(length)
	artifact, err := state.writeArtifact(scoped, data.identity, 1, ordinal, length, cipherLength, func(output io.Writer) error {
		return sealEncryptSource(scoped, header, secret[:], expectedSHA256, produce, output)
	})
	if err != nil {
		return SealedFile{}, err
	}
	data.mutex.Lock()
	if err = scoped.Err(); err == nil {
		if data.closed || data.ctx.Err() != nil {
			err = context.Canceled
		} else if data.poison {
			err = ErrInvalid
		} else {
			err = data.addRecord(sealRecord{ordinal: ordinal, length: length, plainHash: expectedSHA256, cipherBytes: cipherLength, cipherHash: artifact.Ref().read().digest}, 0)
		}
	}
	if err == nil {
		data.dataCount++
	}
	data.mutex.Unlock()
	if err != nil {
		return SealedFile{}, mergeSealCleanup(err, artifact.Close())
	}
	return artifact, nil
}

type sealSourceWriter struct {
	mutex     sync.Mutex
	ctx       context.Context
	output    io.Writer
	digest    hash.Hash
	remaining int64
	failure   error
	closed    bool
}

func (*sealSourceWriter) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private backup source writer]")
}
func (*sealSourceWriter) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (*sealSourceWriter) LogValue() slog.Value {
	return slog.StringValue("[private backup source writer]")
}

func (writer *sealSourceWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.closed {
		return 0, ErrClosed
	}
	if writer.failure != nil {
		return 0, writer.failure
	}
	if writer.failure = writer.ctx.Err(); writer.failure != nil {
		return 0, writer.failure
	}
	if int64(len(payload)) > writer.remaining {
		writer.failure = ErrInvalid
		return 0, writer.failure
	}
	count, err := writer.output.Write(payload)
	writer.remaining -= int64(count)
	_, _ = writer.digest.Write(payload[:count])
	if err != nil {
		writer.failure = sealError(writer.ctx, err)
	} else if count != len(payload) {
		writer.failure = ErrInvalid
	} else {
		writer.failure = writer.ctx.Err()
	}
	return count, writer.failure
}

func (writer *sealSourceWriter) expire() (remaining int64, digest [32]byte, failure error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.closed = true
	if writer.digest != nil {
		copy(digest[:], writer.digest.Sum(nil))
	}
	writer.ctx, writer.output, writer.digest = nil, nil, nil
	return writer.remaining, digest, writer.failure
}

type sealSourceOutput struct {
	ctx    context.Context
	output io.Writer
}

func (writer sealSourceOutput) Write(payload []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := writer.output.Write(payload)
	if err != nil {
		return count, sealError(writer.ctx, err)
	}
	if count != len(payload) {
		return count, ErrInvalid
	}
	return count, writer.ctx.Err()
}

func sealEncryptSource(ctx context.Context, header sealHeader, secret []byte, expected [32]byte, produce func(context.Context, io.Writer) error, output io.Writer) error {
	stream, aad, err := objectStream(header, secret)
	if err != nil {
		return err
	}
	encoded := header.encode()
	if err = sealWrite(ctx, output, encoded[:]); err != nil {
		return err
	}
	encrypted := stream.EncryptWriter(sealSourceOutput{ctx: ctx, output: output}, header.nonce[:], aad)
	writer := &sealSourceWriter{ctx: ctx, output: encrypted, digest: sha256.New(), remaining: header.length}
	defer writer.expire()
	produceError := produce(ctx, writer)
	remaining, digest, writeError := writer.expire()
	if produceError != nil {
		return sealError(ctx, produceError)
	}
	if writeError != nil {
		return writeError
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if remaining != 0 || digest != expected {
		return ErrInvalid
	}
	trailer := sealTrailer(header.length, header.purpose, digest[:])
	if err = sealWrite(ctx, encrypted, trailer[:]); err != nil {
		return err
	}
	if err = encrypted.Close(); err != nil {
		return sealError(ctx, err)
	}
	return ctx.Err()
}
