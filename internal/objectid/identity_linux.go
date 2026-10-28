package objectid

import (
	"fmt"
	"io/fs"
	"syscall"
)

func FromFileInfo(info fs.FileInfo) (string, error) {
	if info == nil || !info.IsDir() {
		return "", fmt.Errorf("identity requires a directory")
	}
	return identityFromInfo(info)
}

func FromLinkInfo(info fs.FileInfo) (string, error) {
	if info == nil || info.Mode()&fs.ModeSymlink == 0 {
		return "", fmt.Errorf("identity requires a symlink")
	}
	return identityFromInfo(info)
}

func identityFromInfo(info fs.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return "", fmt.Errorf("object identity is unavailable")
	}
	return fmt.Sprintf("linux-dev-ino-v1:%d:%d", stat.Dev, stat.Ino), nil
}
