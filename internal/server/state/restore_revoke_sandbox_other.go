//go:build !linux || !amd64

package state

func restoreRevokeSandbox() error { return ErrRestoreUnsupported }
