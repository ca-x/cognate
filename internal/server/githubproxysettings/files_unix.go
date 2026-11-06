//go:build unix

package githubproxysettings

import (
	"os"
	"syscall"
)

func privateFileInfo(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}
