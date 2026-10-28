package library

import (
	"context"
	"io"

	"github.com/ca-x/cognate/internal/repository"
)

func (service *Service) WriteSkillArchive(ctx context.Context, archiveHash, path, contentHash string, destination io.Writer) error {
	ctx, done, err := service.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	if err := service.reserveSlot(); err != nil {
		return err
	}
	defer func() { <-service.slots }()
	data, err := service.cache.Read(ctx, archiveHash)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrArchiveMissing
	}
	return repository.WriteSkillArchive(ctx, data, path, contentHash, destination)
}
