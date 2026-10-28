//go:build !linux

package cli

import (
	"context"
	"io"
	"os"
)

func prepareWatchFileOutput(context.Context, *os.File) (io.Writer, func() error, error) {
	return nil, nil, errWatchOutputUnsupported
}
