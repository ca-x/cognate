//go:build !linux

package configapply

import (
	"errors"
	"testing"
)

func TestCodexObservationOtherPlatformRefuses(test *testing.T) {
	state := &codexEngineState{}
	_, _, _, _, err := state.captureCodexLaunch(test.Context(), CodexLaunch{})
	if !errors.Is(err, ErrCodexUnsupported) {
		test.Fatal("non-Linux observation claimed support")
	}
}
