package install

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func renameNoReplace(_ *os.File, parent, source, destination string) error {
	from, err := windows.UTF16PtrFromString(filepath.Join(parent, source))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(parent, destination))
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
