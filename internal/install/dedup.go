package install

import (
	"context"
	"fmt"
	"os"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/state/ent"
	"github.com/spf13/afero"
)

func (session *Session) matchExistingInstallation(ctx context.Context, plan state.OperationPlan, source string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil {
		return false, fmt.Errorf("installation session is closed")
	}
	pending, err := session.store.PendingOperations(ctx)
	if err != nil {
		return false, err
	}
	if len(pending) != 0 {
		return false, fmt.Errorf("pending installation operations require recovery")
	}
	record, err := session.store.Get(ctx, plan.Slug)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.ContentHash != plan.ContentHash || len(record.Projections) != len(plan.Targets) {
		return false, fmt.Errorf("existing installation conflicts with the requested skill or targets")
	}
	paths := make(map[string]state.Projection, len(record.Projections))
	for _, projection := range record.Projections {
		paths[projection.Path] = projection
	}
	for _, target := range plan.Targets {
		if projection, exists := paths[target.Path]; !exists || projection.Kind != target.Kind {
			return false, fmt.Errorf("existing installation targets do not match the current plan")
		}
	}
	canonical := plan.Targets[0].Path
	before, err := os.Lstat(canonical)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("existing canonical directory is unavailable or changed")
	}
	if expected := paths[canonical].Identity; expected != "" {
		identity, err := objectid.FromFileInfo(before)
		if err != nil || identity != expected {
			return false, fmt.Errorf("existing canonical identity differs from the installation record")
		}
	}
	if err := validateTrustedTree(ctx, canonical, true); err != nil {
		return false, fmt.Errorf("untrusted canonical content: %w", err)
	}
	hash, err := skill.ContentHashIncludingGitContext(ctx, afero.NewOsFs(), canonical)
	if err != nil {
		return false, fmt.Errorf("inspect existing canonical content: %w", err)
	}
	if hash != plan.ContentHash {
		return false, fmt.Errorf("existing canonical content differs from the requested skill")
	}
	for _, target := range plan.Targets[1:] {
		beforeLink, err := os.Lstat(target.Path)
		if err != nil || beforeLink.Mode()&os.ModeSymlink == 0 {
			return false, fmt.Errorf("existing projection is unavailable or changed")
		}
		if expected := paths[target.Path].Identity; expected != "" {
			identity, err := objectid.FromLinkInfo(beforeLink)
			if err != nil || identity != expected {
				return false, fmt.Errorf("existing projection identity differs from the installation record")
			}
		}
		link, err := os.Readlink(target.Path)
		if err != nil || link != canonical {
			return false, fmt.Errorf("existing projection does not point to the canonical directory")
		}
		afterLink, err := os.Lstat(target.Path)
		if err != nil || !os.SameFile(beforeLink, afterLink) {
			return false, fmt.Errorf("existing projection changed during inspection")
		}
	}
	after, err := os.Lstat(canonical)
	if err != nil || !os.SameFile(before, after) {
		return false, fmt.Errorf("existing canonical directory changed during inspection")
	}
	sourceHash, err := skill.ContentHashContext(ctx, afero.NewOsFs(), source)
	if err != nil {
		return false, fmt.Errorf("recheck source content: %w", err)
	}
	if sourceHash != plan.ContentHash {
		return false, fmt.Errorf("source content changed during inspection")
	}
	return true, nil
}
