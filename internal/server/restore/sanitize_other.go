//go:build !linux

package restore

import (
	"context"
	"github.com/ca-x/cognate/internal/server/runtimegen"
)

func sanitizeCandidate(context.Context, *runtimegen.OfflineRoot, *Candidate, SanitizeRequest) (*Sanitized, error) {
	return nil, ErrCandidateUnsupported
}
func (*Sanitized) withCandidate(context.Context, *runtimegen.OfflineRoot, func(context.Context, SanitizedCandidate) error) (*publicationOwnedFiles, error) {
	return nil, ErrCandidateUnsupported
}
