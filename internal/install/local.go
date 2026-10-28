package install

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"uuid"

	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/state"
	"github.com/spf13/afero"
)

func (session *Session) InstallLocal(ctx context.Context, home, slug, source string) error {
	_, err := session.InstallLocalWithResult(ctx, home, slug, source)
	return err
}

func (session *Session) InstallLocalWithResult(ctx context.Context, home, slug, source string) (alreadyPresent bool, resultErr error) {
	return session.installLocal(ctx, home, slug, source, "", nil, false, false)
}

func (session *Session) InstallLocalWithHash(ctx context.Context, home, slug, source, expectedHash string) (bool, error) {
	if len(expectedHash) != 64 {
		return false, fmt.Errorf("invalid expected content hash")
	}
	if _, err := hex.DecodeString(expectedHash); err != nil {
		return false, fmt.Errorf("invalid expected content hash")
	}
	return session.installLocal(ctx, home, slug, source, expectedHash, nil, false, false)
}

func (session *Session) InstallLocalWithSelection(ctx context.Context, home, slug, source, expectedHash string, agents []string) (alreadyPresent bool, resultErr error) {
	if len(expectedHash) != 64 {
		return false, fmt.Errorf("invalid preview hash")
	}
	if _, err := hex.DecodeString(expectedHash); err != nil {
		return false, fmt.Errorf("invalid preview hash")
	}
	return session.installLocal(ctx, home, slug, source, expectedHash, agents, true, false)
}

func (session *Session) installLocal(ctx context.Context, home, slug, source, expectedHash string, agents []string, selected, subscriptionManaged bool) (alreadyPresent bool, resultErr error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if runtime.GOOS != "linux" {
		return false, fmt.Errorf("local installation is unsupported on this platform")
	}
	if err := ValidateSlug(slug); err != nil {
		return false, err
	}
	pending, err := session.PendingOperations(ctx)
	if err != nil {
		return false, err
	}
	if len(pending) != 0 {
		return false, fmt.Errorf("pending installation operations require recovery")
	}
	source, err = skill.ResolvePath(source)
	if err != nil {
		return false, fmt.Errorf("resolve source skill: %w", err)
	}
	metadata, err := os.Lstat(filepath.Join(source, "SKILL.md"))
	if err != nil {
		return false, fmt.Errorf("inspect source SKILL.md: %w", err)
	}
	if !metadata.Mode().IsRegular() {
		return false, fmt.Errorf("source SKILL.md must be a regular file")
	}
	if err := validateTrustedTree(ctx, source, false); err != nil {
		return false, fmt.Errorf("untrusted source contents: %w", err)
	}
	hash, err := skill.ContentHashContext(ctx, afero.NewOsFs(), source)
	if err != nil {
		return false, fmt.Errorf("hash source skill: %w", err)
	}
	if expectedHash != "" && hash != expectedHash {
		return false, fmt.Errorf("source content changed since preview")
	}
	var plan state.OperationPlan
	if selected {
		plan, err = PlanLocalTargetsWithAgents(home, slug, uuid.New().String(), hash, agents)
	} else {
		plan, err = PlanLocalTargets(home, slug, uuid.New().String(), hash)
	}
	if err != nil {
		return false, err
	}
	plan.SubscriptionManaged = subscriptionManaged
	if len(plan.Targets) > 1 {
		if err := rejectWSL(); err != nil {
			return false, err
		}
	}
	matched, err := session.matchExistingInstallation(ctx, plan, source)
	if err != nil {
		return false, err
	}
	if matched {
		return true, nil
	}
	for _, target := range plan.Targets {
		if _, err := os.Lstat(target.Path); err == nil {
			return false, fmt.Errorf("installation target already exists: %s", target.Path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("inspect installation target: %w", err)
		}
	}
	if err := session.BeginOperation(ctx, plan); err != nil {
		return false, fmt.Errorf("installation intent may need reconciliation: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = fmt.Errorf("installation may have pending work; reopen the session and reconcile: %w", resultErr)
		}
	}()
	if err := session.StartOperation(ctx, plan.ID); err != nil {
		return false, err
	}
	if _, err := session.CreateStage(ctx, plan.ID, 0); err != nil {
		return false, err
	}
	if err := session.CopyStage(ctx, plan.ID, 0, source); err != nil {
		return false, err
	}
	if err := session.PublishStage(ctx, plan.ID, 0); err != nil {
		return false, err
	}
	for position := 1; position < len(plan.Targets); position++ {
		if err := session.PublishProjection(ctx, plan.ID, position); err != nil {
			return false, err
		}
	}
	return false, session.CompleteInstallation(ctx, plan.ID)
}
