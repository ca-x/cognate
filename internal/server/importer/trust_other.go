//go:build !linux

package importer

import (
	"fmt"
	"os"
)

func validateDatabaseTrust(_ string, _ os.FileInfo) error {
	return fmt.Errorf("cc-switch database trust checks are unsupported on this platform")
}
