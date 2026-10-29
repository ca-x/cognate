//go:build !linux

package configapply

type nativeDirectory struct{ identity nativeIdentity }

func nativePlatform() error                                      { return ErrUnsupportedPlatform }
func nativeOpenDirectory(string, bool) (*nativeDirectory, error) { return nil, ErrUnsupportedPlatform }
func sameDirectory(first, second nativeIdentity) bool            { return first == second }
func (*nativeDirectory) check() error                            { return ErrUnsupportedPlatform }
func (*nativeDirectory) close() error                            { return nil }
func (*nativeDirectory) child(string, bool) (*nativeDirectory, error) {
	return nil, ErrUnsupportedPlatform
}
func (*nativeDirectory) read(string, int64) (nativeImage, []byte, error) {
	return nativeImage{}, nil, ErrUnsupportedPlatform
}
func (*nativeDirectory) write(string, []byte) (nativeImage, error) {
	return nativeImage{}, ErrUnsupportedPlatform
}
func (*nativeDirectory) entries() ([]string, error)                    { return nil, ErrUnsupportedPlatform }
func (*nativeDirectory) sync() error                                   { return ErrUnsupportedPlatform }
func (*nativeDirectory) rename(string, *nativeDirectory, string) error { return ErrUnsupportedPlatform }
func (*nativeDirectory) syncWith(*nativeDirectory) error               { return ErrUnsupportedPlatform }
func (*nativeDirectory) removeOwn(string, nativeImage) error           { return ErrUnsupportedPlatform }
func (*nativeDirectory) removeDirectory(string, nativeIdentity) error  { return ErrUnsupportedPlatform }
func (*nativeDirectory) reserve(int) (nativeImage, error) {
	return nativeImage{}, ErrUnsupportedPlatform
}
func (*nativeDirectory) exchange(string, string) error       { return ErrUnsupportedPlatform }
func (*nativeDirectory) validateSessionFiles() error         { return ErrUnsupportedPlatform }
func (*nativeDirectory) syncImage(string, nativeImage) error { return ErrUnsupportedPlatform }
func (*nativeDirectory) reserveAt(string, int) (nativeImage, error) {
	return nativeImage{}, ErrUnsupportedPlatform
}
