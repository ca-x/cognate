//go:build !linux

package configapply

import "context"

type codexGuard struct{}

func codexPlatform() error { return ErrCodexUnsupported }

func acquireCodexGuard(ctx context.Context, _ *nativeDirectory) (*codexGuard, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupportedPlatform
}

func (*codexGuard) check() error { return ErrUnsupportedPlatform }
func (*codexGuard) close() error { return nil }

func codexDirectoryEntries(*nativeDirectory) ([]string, error) { return nil, ErrCodexUnsupported }
