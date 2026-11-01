//go:build !linux

package state

type restoreRevokeSQL struct{}

func restoreRevokeWorkerServe(restoreRevokeSQL) int { return 2 }
