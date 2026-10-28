package packs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
)

const ChunkBytes = 64 << 10

type Chunk struct {
	Offset      int64
	Data        []byte
	Final       bool
	SHA256      string
	ContentHash string
}

func (service *Service) Download(ctx context.Context, raw, skillID, contentHash string, revision int64, send func(Chunk) error) error {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return err
	}
	defer done()
	if send == nil {
		return state.ErrPackInvalid
	}
	credential, err := service.parse(raw)
	if err != nil {
		return err
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		return ErrBusy
	}
	pack, err := service.store.SharedPack(ctx, credential.id, credential.digest[:])
	if err != nil {
		return err
	}
	if pack.Revision != revision {
		return state.ErrPackConflict
	}
	var selected *state.PackSkill
	for _, item := range pack.Items {
		if item.SkillID == skillID && item.ContentHash == contentHash {
			selected = &item
			break
		}
	}
	if selected == nil {
		return state.ErrPackShareUnavailable
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := service.parse(raw); err != nil {
			return err
		}
		if err := service.store.AuthorizePackSkill(ctx, credential.id, credential.digest[:], skillID, contentHash, revision); err != nil {
			return err
		}
		_, err := service.parse(raw)
		return err
	}
	writer := &chunkWriter{ctx: ctx, send: send, authorize: authorize, digest: sha256.New(), buffer: make([]byte, 0, ChunkBytes)}
	if err := authorize(); err != nil {
		return err
	}
	if err := service.source.WriteSkillArchive(ctx, selected.ArchiveHash, selected.Path, contentHash, writer); err != nil {
		return err
	}
	if err := writer.flush(); err != nil {
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	return send(Chunk{Offset: writer.offset, Final: true, SHA256: hex.EncodeToString(writer.digest.Sum(nil)), ContentHash: contentHash})
}

type chunkWriter struct {
	ctx       context.Context
	send      func(Chunk) error
	authorize func() error
	digest    hash.Hash
	buffer    []byte
	offset    int64
	total     int
}

func (writer *chunkWriter) Write(data []byte) (int, error) {
	if len(data) > repository.MaxSkillArchiveBytes-writer.total {
		return 0, repository.ErrArchiveTooLarge
	}
	written := 0
	for len(data) != 0 {
		if err := writer.ctx.Err(); err != nil {
			return written, err
		}
		count := min(len(data), ChunkBytes-len(writer.buffer))
		writer.buffer = append(writer.buffer, data[:count]...)
		_, _ = writer.digest.Write(data[:count])
		data = data[count:]
		written, writer.total = written+count, writer.total+count
		if len(writer.buffer) == ChunkBytes {
			if err := writer.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (writer *chunkWriter) flush() error {
	if len(writer.buffer) == 0 {
		return nil
	}
	if err := writer.authorize(); err != nil {
		return err
	}
	if err := writer.send(Chunk{Offset: writer.offset, Data: bytes.Clone(writer.buffer)}); err != nil {
		return err
	}
	writer.offset += int64(len(writer.buffer))
	writer.buffer = writer.buffer[:0]
	return nil
}
