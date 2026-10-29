//go:build !linux

package desktop

import "errors"

func prepareHome(string) (string, error) {
	return "", errors.New("local installation is unsupported on this platform")
}
