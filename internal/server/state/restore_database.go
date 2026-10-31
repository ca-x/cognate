package state

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

var (
	ErrRestoreInvalid     = errors.New("restore database inspection refused")
	ErrRestoreKey         = errors.New("restore database original key verification failed")
	ErrRestoreBudget      = errors.New("restore database inspection budget exceeded")
	ErrRestoreUncertain   = errors.New("restore database inspection cleanup uncertain")
	ErrRestoreUnsupported = errors.New("restore database inspection unsupported")
)

type RestoreDatabaseEvidence struct{ value *restoreDatabaseFacts }

type restoreDatabaseFacts struct {
	DatabaseBytes  int64
	DatabaseHash   [32]byte
	SchemaHash     [32]byte
	KeyID          string
	ReferenceBytes int64
	ReferenceHash  [32]byte
	Records        uint64
	Counts         [4]uint64
	Policy         int64
}

func InspectRestoreDatabase(ctx context.Context, parent, database *os.File, originalMasterKey []byte, limits LocalSnapshotLimits) (RestoreDatabaseEvidence, error) {
	if ctx == nil || parent == nil || database == nil || !limits.valid() {
		return RestoreDatabaseEvidence{}, ErrRestoreInvalid
	}
	if err := ctx.Err(); err != nil {
		return RestoreDatabaseEvidence{}, err
	}
	if len(originalMasterKey) != 32 {
		return RestoreDatabaseEvidence{}, ErrRestoreKey
	}
	return inspectRestoreDatabase(ctx, parent, database, originalMasterKey, limits)
}

func (evidence RestoreDatabaseEvidence) Valid() bool { return evidence.value != nil }
func (evidence RestoreDatabaseEvidence) Database() (int64, [32]byte) {
	if evidence.value == nil {
		return 0, [32]byte{}
	}
	return evidence.value.DatabaseBytes, evidence.value.DatabaseHash
}
func (evidence RestoreDatabaseEvidence) SchemaFingerprint() [32]byte {
	if evidence.value == nil {
		return [32]byte{}
	}
	return evidence.value.SchemaHash
}
func (evidence RestoreDatabaseEvidence) KeyIdentity() string {
	if evidence.value == nil {
		return ""
	}
	return evidence.value.KeyID
}
func (evidence RestoreDatabaseEvidence) References() (int64, [32]byte, uint64) {
	if evidence.value == nil {
		return 0, [32]byte{}, 0
	}
	return evidence.value.ReferenceBytes, evidence.value.ReferenceHash, evidence.value.Records
}
func (evidence RestoreDatabaseEvidence) ReferenceCounts() (uint64, uint64, uint64, uint64) {
	if evidence.value == nil {
		return 0, 0, 0, 0
	}
	counts := evidence.value.Counts
	return counts[0], counts[1], counts[2], counts[3]
}
func (evidence RestoreDatabaseEvidence) ObservationMaxAge() time.Duration {
	if evidence.value == nil {
		return 0
	}
	return time.Duration(evidence.value.Policy)
}
func (RestoreDatabaseEvidence) Format(output fmt.State, _ rune) {
	_, _ = output.Write([]byte("[private restore database evidence]"))
}
func (RestoreDatabaseEvidence) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (RestoreDatabaseEvidence) LogValue() slog.Value {
	return slog.StringValue("[private restore database evidence]")
}

func restoreDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrRestoreUncertain, ErrLocalSnapshotUncertain, ErrLocalSnapshotCleanup} {
		if errors.Is(err, known) {
			return ErrRestoreUncertain
		}
	}
	for _, known := range []error{ErrRestoreKey, ErrRestoreBudget, ErrRestoreUnsupported, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, ErrLocalSnapshotBudget) || errors.Is(err, ErrLocalSnapshotResource) {
		return ErrRestoreBudget
	}
	return ErrRestoreInvalid
}
