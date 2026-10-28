//go:build !linux

package cli

import "fmt"

func prepareStateDirectory(_ string) error {
	return fmt.Errorf("local state is unsupported on this platform")
}

func validateStateDirectory(_ string, _ bool) error {
	return fmt.Errorf("local state is unsupported on this platform")
}

func createTrustedDirectory(_ string, _ bool) error {
	return fmt.Errorf("local state is unsupported on this platform")
}

func validateTrustedDirectory(_ string) error {
	return fmt.Errorf("local state is unsupported on this platform")
}

func resolveTrustedDirectory(_ string) (string, error) {
	return "", fmt.Errorf("local state is unsupported on this platform")
}
