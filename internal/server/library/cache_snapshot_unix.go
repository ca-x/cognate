//go:build unix

package library

import (
	"os"
	"syscall"
)

func snapshotArchivePrivateFile(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, valid := info.Sys().(*syscall.Stat_t)
	return valid && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}
