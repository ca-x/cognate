package state

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func checkDatabaseLinks(path string) error {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("database must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("database changed while opening")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("database must have exactly one hard link; found %d", info.NumberOfLinks)
	}
	return nil
}
