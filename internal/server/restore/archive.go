package restore

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/ca-x/cognate/internal/repository"
)

var errArchiveInvalid = errors.New("restore archive refused")
var errArchiveBudget = errors.New("restore archive budget exceeded")

type archiveExpectation struct {
	hash         string
	bytes        int64
	repositories []archiveRepositoryExpectation
}

type archiveRepositoryExpectation struct {
	skillCount int64
	skills     map[string]string
}

type archiveLimits struct {
	bytes      int64
	references uint64
}

func verifyArchive(ctx context.Context, data []byte, expected archiveExpectation, limits archiveLimits) error {
	if ctx == nil || limits.bytes <= 0 || limits.bytes > repository.MaxArchiveBytes || limits.references == 0 || limits.references > 1000000 || expected.bytes <= 0 || len(expected.repositories) == 0 {
		return errArchiveInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if int64(len(data)) > repository.MaxArchiveBytes || expected.bytes > repository.MaxArchiveBytes || int64(len(data)) > limits.bytes || expected.bytes > limits.bytes {
		return errArchiveBudget
	}
	if uint64(len(expected.repositories)) > limits.references {
		return errArchiveBudget
	}
	remaining := limits.references - uint64(len(expected.repositories))
	for _, repositoryExpected := range expected.repositories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if uint64(len(repositoryExpected.skills)) > remaining {
			return errArchiveBudget
		}
		remaining -= uint64(len(repositoryExpected.skills))
		if repositoryExpected.skillCount <= 0 || repositoryExpected.skillCount != int64(len(repositoryExpected.skills)) {
			return errArchiveInvalid
		}
	}
	digest, valid := inventoryHash(expected.hash)
	if !valid || int64(len(data)) != expected.bytes {
		return errArchiveInvalid
	}
	actualHash := sha256.Sum256(data)
	if err := ctx.Err(); err != nil {
		return err
	}
	if actualHash != digest {
		return errArchiveInvalid
	}
	inspected, err := repository.InspectArchive(ctx, data)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		if errors.Is(err, repository.ErrArchiveTooLarge) {
			return errArchiveBudget
		}
		return errArchiveInvalid
	}
	for _, repositoryExpected := range expected.repositories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if int64(len(inspected.Skills)) != repositoryExpected.skillCount || len(inspected.Skills) != len(repositoryExpected.skills) {
			return errArchiveInvalid
		}
		for _, entry := range inspected.Skills {
			if err := ctx.Err(); err != nil {
				return err
			}
			if repositoryExpected.skills[entry.Path] != entry.ContentHash {
				return errArchiveInvalid
			}
		}
	}
	return ctx.Err()
}
