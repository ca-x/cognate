//go:build !unix && !windows

package state

import "errors"

func checkDatabaseLinks(string) error {
	return errors.New("database hard-link inspection is unsupported on this platform")
}
