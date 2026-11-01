package state

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
)

type RestoreRevocationEvidence struct{ value *restoreRevocationFacts }
type restoreRevocationFacts struct {
	Database  restoreDatabaseFacts
	Protected [32]byte
}

func InspectRestoreCandidateOffline(ctx context.Context, root *runtimegen.OfflineRoot, parent, database *os.File, key []byte, limits LocalSnapshotLimits) (RestoreDatabaseEvidence, error) {
	facts, err := restoreRevokeRun(ctx, root, parent, database, key, RestoreDatabaseEvidence{}, [16]byte{}, time.Time{}, limits)
	if err != nil {
		return RestoreDatabaseEvidence{}, err
	}
	return facts.Database(), nil
}

func RevokeRestoreCandidate(ctx context.Context, root *runtimegen.OfflineRoot, parent, database *os.File, key []byte, before RestoreDatabaseEvidence, operationID [16]byte, revokedAt time.Time, limits LocalSnapshotLimits) (RestoreRevocationEvidence, error) {
	if !before.Valid() || operationID == [16]byte{} || revokedAt.IsZero() || revokedAt.Location() != time.UTC {
		return RestoreRevocationEvidence{}, ErrRestoreInvalid
	}
	return restoreRevokeRun(ctx, root, parent, database, key, before, operationID, revokedAt, limits)
}

func (facts RestoreRevocationEvidence) Database() RestoreDatabaseEvidence {
	if facts.value == nil {
		return RestoreDatabaseEvidence{}
	}
	copy := facts.value.Database
	return RestoreDatabaseEvidence{value: &copy}
}
func (facts RestoreRevocationEvidence) ProtectedSHA256() [32]byte {
	if facts.value == nil {
		return [32]byte{}
	}
	return facts.value.Protected
}
func (RestoreRevocationEvidence) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private restore revocation evidence]")
}
func (RestoreRevocationEvidence) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (RestoreRevocationEvidence) LogValue() slog.Value {
	return slog.StringValue("[private restore revocation evidence]")
}

func restoreRevokeSameFacts(before, after restoreDatabaseFacts) bool {
	before.DatabaseBytes, before.DatabaseHash = after.DatabaseBytes, after.DatabaseHash
	return before == after
}
