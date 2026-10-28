//go:build !linux && !darwin

package install

import (
	"context"
	"fmt"
	"os"
)

func validateTrustedTree(_ context.Context, _ string, _ bool) error {
	return fmt.Errorf("skill trust checks are unsupported on this platform")
}

func validateTrustedRoot(_ context.Context, _ *os.Root, _ bool) error {
	return fmt.Errorf("skill trust checks are unsupported on this platform")
}

func validatePrivateDirectory(_ os.FileInfo) error {
	return fmt.Errorf("private backup directories are unsupported on this platform")
}
