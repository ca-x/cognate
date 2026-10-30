//go:build !linux

package configapply

import (
	"context"
	"time"
)

func (state *codexEngineState) captureCodexLaunch(ctx context.Context, launch CodexLaunch) (codexLaunchDescriptor, [3]nativeImage, nativeIdentity, time.Time, error) {
	return codexLaunchDescriptor{}, [3]nativeImage{}, nativeIdentity{}, time.Time{}, ErrCodexUnsupported
}
