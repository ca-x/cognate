//go:build !unix

package state

import (
	"errors"
	"os"
)

func validateServerDirectoryInfo(os.FileInfo) error {
	return errors.New("server data directory security is unsupported on this platform")
}

func validateServerDirectory(string) error {
	return errors.New("server data directory security is unsupported on this platform")
}

func validateServerAncestor(string) error {
	return errors.New("server data directory security is unsupported on this platform")
}

func inspectServerFile(string) (bool, error) {
	return false, errors.New("server database file security is unsupported on this platform")
}
