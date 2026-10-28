//go:build !linux && !darwin && !windows

package install

import (
	"errors"
	"os"
)

func renameNoReplace(_ *os.File, _, _, _ string) error {
	return errors.New("atomic directory publication is unsupported on this platform")
}
