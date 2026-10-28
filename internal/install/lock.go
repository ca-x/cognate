package install

import (
	"context"

	"github.com/ca-x/skillhub/internal/filelock"
	"github.com/gofrs/flock"
)

func acquireLock(ctx context.Context, path string) (*flock.Flock, error) {
	return filelock.Acquire(ctx, path)
}
