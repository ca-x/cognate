//go:build !linux

package cli

import "fmt"

func loadClientKey(string, bool) ([]byte, error) {
	return nil, fmt.Errorf("local client credentials are unsupported on this platform")
}
