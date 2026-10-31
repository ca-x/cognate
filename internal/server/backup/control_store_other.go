//go:build !unix

package backup

import "os"

func controlPlatform() error { return ErrUnsupported }

func controlPrivate(os.FileInfo, bool) bool           { return false }
func controlLock(*os.Root) (*os.File, error)          { return nil, ErrUnsupported }
func controlDuplicateLock(*os.File) (*os.File, error) { return nil, ErrUnsupported }
