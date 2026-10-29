//go:build !linux

package cli

import (
	"context"
	"io"
	"os"

	"github.com/ca-x/cognate/internal/configapply"
)

func nativeConfigurationPlatform(context.Context) error { return configapply.ErrUnsupportedPlatform }
func prepareNativeFileInput(context.Context, *os.File) (io.ReadCloser, error) {
	return nil, configapply.ErrUnsupportedPlatform
}
func prepareNativeFileOutput(context.Context, *os.File) (io.WriteCloser, error) {
	return nil, configapply.ErrUnsupportedPlatform
}
func openNativeConfirmationTTY(context.Context) (nativeTerminal, error) {
	return nil, configapply.ErrUnsupportedPlatform
}
