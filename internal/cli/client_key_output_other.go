//go:build !darwin

package cli

import (
	"context"
	"io"
)

func preparePlatformClientKeyOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	return prepareNativeOutput(ctx, writer)
}
