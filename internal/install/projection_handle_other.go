//go:build !linux

package install

import (
	"fmt"
	"os"
)

func openProjectionLink(_ *os.Root, _ string) (*os.File, error) {
	return nil, fmt.Errorf("symlink handles are unsupported on this platform")
}
