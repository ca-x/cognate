package install

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"uuid"

	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/state/ent"
)

func (session *Session) InstallSubscription(ctx context.Context, home, slug, source, expectedHash string) (bool, error) {
	if len(expectedHash) != 64 {
		return false, fmt.Errorf("invalid subscription content hash")
	}
	if _, err := hex.DecodeString(expectedHash); err != nil {
		return false, fmt.Errorf("invalid subscription content hash")
	}
	return session.installLocal(ctx, home, slug, source, expectedHash, nil, false, true)
}

func (session *Session) PreflightSubscription(ctx context.Context, home, slug, desiredHash string) (state.Installation, bool, error) {
	plan, err := PlanLocalTargets(home, slug, uuid.New().String(), desiredHash)
	if err != nil {
		return state.Installation{}, false, err
	}
	record, err := session.Installation(ctx, slug)
	if ent.IsNotFound(err) {
		for _, target := range plan.Targets {
			if _, err := os.Lstat(target.Path); err == nil {
				return state.Installation{}, false, fmt.Errorf("unmanaged installation target already exists")
			} else if !errors.Is(err, os.ErrNotExist) {
				return state.Installation{}, false, err
			}
		}
		return state.Installation{}, false, nil
	}
	if err != nil {
		return state.Installation{}, false, err
	}
	if record.ContentHash != desiredHash && !record.SubscriptionManaged {
		return state.Installation{}, false, fmt.Errorf("requested skill conflicts with a non-subscription installation")
	}
	if err := session.VerifyLocal(ctx, home, slug); err != nil {
		return state.Installation{}, false, err
	}
	return record, true, nil
}

func (session *Session) VerifyLocal(ctx context.Context, home, slug string) error {
	record, err := session.Installation(ctx, slug)
	if err != nil {
		return err
	}
	plan, err := PlanLocalTargets(home, slug, uuid.New().String(), record.ContentHash)
	if err != nil {
		return err
	}
	matched, err := session.matchExistingInstallation(ctx, plan, plan.Targets[0].Path)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("installation changed during inspection")
	}
	return nil
}
