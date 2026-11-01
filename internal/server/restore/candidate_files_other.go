//go:build !linux

package restore

import (
	"context"
	"io"
	"os"
)

func (*candidateFiles) initialize(*os.File) error {
	return ErrCandidateUnsupported
}
func candidateUnlink(*os.File, string, bool) error { return ErrCandidateUnsupported }
func (*candidateFiles) write(context.Context, string, int64, func(io.Writer) error) error {
	return ErrCandidateUnsupported
}
func (*candidateFiles) withFile(string, func(*os.File) error) error { return ErrCandidateUnsupported }
func (*candidateFiles) read(context.Context, string, int64, int64) ([]byte, error) {
	return nil, ErrCandidateUnsupported
}
func (*candidateFiles) syncDirectories() error { return ErrCandidateUnsupported }
func (*candidateFiles) checkEntries() error    { return ErrCandidateUnsupported }
func (*candidateFiles) cleanup() error         { return ErrCandidateUnsupported }
func (*candidateFiles) mutateDatabase(context.Context, func(*os.File) (int64, [32]byte, error)) error {
	return ErrCandidateUnsupported
}
