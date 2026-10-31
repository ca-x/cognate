//go:build !linux

package state

func restoreDatabaseWorkerServe() int { return 2 }
