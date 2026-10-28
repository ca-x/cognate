//go:build !linux

package objectid

import (
	"fmt"
	"io/fs"
)

func FromFileInfo(_ fs.FileInfo) (string, error) {
	return "", fmt.Errorf("directory identity is unsupported on this platform")
}

func FromLinkInfo(_ fs.FileInfo) (string, error) {
	return "", fmt.Errorf("symlink identity is unsupported on this platform")
}
