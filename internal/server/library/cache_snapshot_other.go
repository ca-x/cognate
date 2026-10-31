//go:build !unix

package library

import "os"

func snapshotArchivePrivateFile(os.FileInfo) bool { return false }
