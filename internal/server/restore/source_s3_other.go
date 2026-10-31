//go:build !linux

package restore

import (
	"context"
	"io"
	"os"

	"github.com/ca-x/cognate/internal/server/backup"
)

type sourceS3Input struct{}

func newSourceS3Input(*os.File, sourceS3FileOps) (*sourceS3Input, error) {
	return nil, ErrCandidateUnsupported
}
func (*sourceS3Input) accept(context.Context, int64, func(io.Writer) (backup.VerifyResult, error), func(*os.File) error) error {
	return ErrCandidateUnsupported
}
func (*sourceS3Input) cleanup() error       { return ErrCandidateUnsupported }
func sourceS3Unlink(*os.File, string) error { return ErrCandidateUnsupported }
