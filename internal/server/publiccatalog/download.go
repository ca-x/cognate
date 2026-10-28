package publiccatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
)

func (service *Service) Download(ctx context.Context, skillID, contentHash string, revision int64, send func(packs.Chunk) error) error {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return err
	}
	defer done()
	if send == nil {
		return state.ErrPublicCatalogInvalid
	}
	selected, err := service.store.AuthorizePublicSkill(ctx, skillID, contentHash, revision)
	if err != nil {
		return err
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		return ErrBusy
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := service.store.AuthorizePublicSkill(ctx, skillID, contentHash, revision)
		return err
	}
	writer := &chunkWriter{ctx: ctx, send: send, authorize: authorize, digest: sha256.New(), buffer: make([]byte, 0, packs.ChunkBytes)}
	if err := service.source.WriteSkillArchive(ctx, selected.ArchiveHash, selected.Path, selected.ContentHash, writer); err != nil {
		return err
	}
	if err := writer.flush(); err != nil {
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	return send(packs.Chunk{Offset: writer.offset, Final: true, SHA256: hex.EncodeToString(writer.digest.Sum(nil)), ContentHash: contentHash})
}

type chunkWriter struct {
	ctx       context.Context
	send      func(packs.Chunk) error
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
		count := min(len(data), packs.ChunkBytes-len(writer.buffer))
		writer.buffer = append(writer.buffer, data[:count]...)
		_, _ = writer.digest.Write(data[:count])
		data = data[count:]
		written, writer.total = written+count, writer.total+count
		if len(writer.buffer) == packs.ChunkBytes {
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
	if err := writer.send(packs.Chunk{Offset: writer.offset, Data: bytes.Clone(writer.buffer)}); err != nil {
		return err
	}
	writer.offset += int64(len(writer.buffer))
	writer.buffer = writer.buffer[:0]
	return nil
}
