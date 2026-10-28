package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/state"
)

type publishedProjection struct {
	root       *os.Root
	link       *os.File
	parentInfo os.FileInfo
	linkInfo   os.FileInfo
}

func canonicalTarget(entry state.Operation) (int, error) {
	position := -1
	for index, target := range entry.Plan.Targets {
		if target.Kind == "canonical" {
			if position >= 0 {
				return -1, fmt.Errorf("installation has multiple canonical targets")
			}
			position = index
		}
	}
	if position < 0 || !entry.Plan.Targets[position].Published {
		return -1, fmt.Errorf("installation has no published canonical target")
	}
	return position, nil
}

func (session *Session) verifyCanonical(ctx context.Context, entry state.Operation, position int) error {
	stage, exists := session.stages[position]
	if !exists {
		return fmt.Errorf("published stage was not created in this session")
	}
	return stage.VerifyPublished(ctx, filepath.Base(entry.Plan.Targets[position].Path), entry.Plan.ContentHash)
}

func (session *Session) PublishProjection(ctx context.Context, id string, position int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.store == nil || session.active != id || id == "" {
		return fmt.Errorf("operation was not started in this session")
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("symlink publication is unsupported on this platform")
	}
	if err := rejectWSL(); err != nil {
		return err
	}
	entry, err := session.store.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	if entry.Status != "active" || entry.Plan.Action != "install" || position < 0 || position >= len(entry.Plan.Targets) {
		return fmt.Errorf("operation is not an active installation")
	}
	target := entry.Plan.Targets[position]
	if target.Kind != "symlink" || target.StageName != "" || target.PublishIntent || target.Published {
		return fmt.Errorf("target is not an unpublished symlink")
	}
	canonicalPosition, err := canonicalTarget(entry)
	if err != nil {
		return err
	}
	if err := session.verifyCanonical(ctx, entry, canonicalPosition); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	root, err := os.OpenRoot(filepath.Dir(target.Path))
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	projection := &publishedProjection{root: root}
	if session.projections == nil {
		session.projections = make(map[int]*publishedProjection)
	}
	session.projections[position] = projection
	projection.parentInfo, err = root.Stat(".")
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := projection.verifyParent(target.Path); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := session.store.BeginProjection(ctx, id, position); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := ctx.Err(); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	canonical := entry.Plan.Targets[canonicalPosition].Path
	if err := root.Symlink(canonical, filepath.Base(target.Path)); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	projection.link, err = openProjectionLink(root, filepath.Base(target.Path))
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	projection.linkInfo, err = projection.link.Stat()
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := projection.verify(target.Path, canonical); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := session.verifyCanonical(ctx, entry, canonicalPosition); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	identity, err := objectid.FromLinkInfo(projection.linkInfo)
	if err != nil {
		return session.stageFailure(ctx, id, err)
	}
	if err := session.store.MarkProjectionPublished(ctx, id, position, identity); err != nil {
		return session.stageFailure(ctx, id, err)
	}
	return nil
}

func (projection *publishedProjection) verify(path, canonical string) error {
	if err := projection.verifyParent(path); err != nil {
		return err
	}
	link, err := projection.root.Lstat(filepath.Base(path))
	if err != nil || link.Mode()&os.ModeSymlink == 0 || projection.linkInfo == nil || !os.SameFile(projection.linkInfo, link) {
		return fmt.Errorf("projection link changed or disappeared")
	}
	value, err := projection.root.Readlink(filepath.Base(path))
	if err != nil || value != canonical {
		return fmt.Errorf("projection link target changed")
	}
	return nil
}

func (projection *publishedProjection) verifyParent(path string) error {
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !os.SameFile(projection.parentInfo, parent) {
		return fmt.Errorf("projection parent changed or disappeared")
	}
	return nil
}

func rejectWSL() error {
	for _, path := range []string{"/proc/sys/kernel/osrelease", "/proc/version"} {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot verify symlink platform: %w", err)
		}
		if isWSLKernel(string(data)) {
			return fmt.Errorf("WSL requires copy projections")
		}
	}
	return nil
}

func isWSLKernel(version string) bool {
	version = strings.ToLower(version)
	return strings.Contains(version, "microsoft") || strings.Contains(version, "wsl")
}
