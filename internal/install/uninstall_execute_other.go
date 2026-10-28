//go:build !linux

package install

import (
	"context"
	"fmt"

	"github.com/ca-x/skillhub/internal/state"
)

func (session *Session) executeUninstallLocked(_ context.Context, _ string, _ state.Backup) error {
	return fmt.Errorf("local uninstall is unsupported on this platform")
}
