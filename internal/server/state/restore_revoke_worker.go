package state

import "time"

const restoreRevokeWorkerMode = "--cognate-restore-revoke-worker-v1"

type restoreRevokeRequest struct {
	Nonce                        [16]byte
	Mode                         string
	Operation                    [16]byte
	RevokedAt                    time.Time
	Root, Lock, Parent, Database restoreDatabaseIdentity
	Before                       restoreDatabaseFacts
	Protected                    [32]byte
	Limits                       LocalSnapshotLimits
}
type restoreRevokeResponse struct {
	Nonce  [16]byte
	Status string
	Facts  restoreRevocationFacts
}

func RestoreRevokeWorkerDispatch(args []string) (bool, int) {
	if len(args) < 2 || args[1] != restoreRevokeWorkerMode {
		return false, 0
	}
	if len(args) != 2 {
		return true, 2
	}
	return true, restoreRevokeWorkerServe(restoreRevokeSQL{})
}
