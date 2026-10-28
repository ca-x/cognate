package install

import (
	"context"
	"fmt"

	"github.com/ca-x/skillhub/internal/state"
)

func (session *Session) UninstallLocal(ctx context.Context, slug string) (state.Backup, error) {
	backup, identifier, err := session.PrepareUninstallLocal(ctx, slug)
	if err != nil {
		return backup, err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return backup, fmt.Errorf("uninstall operation %s requires recovery; backup %s retained: installation session is closed", identifier, backup.ID)
	}
	return backup, session.executeUninstallLocked(ctx, identifier, backup)
}
