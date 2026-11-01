//go:build !linux

package configapply

import (
	"context"
	"time"
)

type geminiSourceStamp struct{}
type geminiDescriptor struct {
	Version uint32
	Launch  GeminiLaunch
	Target  nativeIdentity
}
type geminiObserved struct {
	engine     *geminiEngineState
	descriptor geminiDescriptor
	baseline   [2]nativeImage
	expires    time.Time
}

func (value *geminiObserved) destroy()            { *value = geminiObserved{} }
func geminiPlatform() error                       { return ErrGeminiUnsupported }
func geminiDirectoryPath(*nativeDirectory) string { return "" }
func (*geminiEngineState) captureGemini(context.Context, GeminiLaunch) (geminiDescriptor, [2]nativeImage, error) {
	return geminiDescriptor{}, [2]nativeImage{}, ErrGeminiUnsupported
}
func (*GeminiEngine) ObserveGemini(context.Context, GeminiLaunch, time.Time) (GeminiObservation, error) {
	return GeminiObservation{}, ErrGeminiUnsupported
}
func geminiRestoreMode(*nativeDirectory, int, nativeImage, uint32, func() error) (nativeImage, error) {
	return nativeImage{}, ErrGeminiUnsupported
}
