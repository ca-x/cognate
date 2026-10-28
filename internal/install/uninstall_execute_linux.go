package install

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/state"
	"golang.org/x/sys/unix"
)

type uninstallExecutionTarget struct {
	position   int
	plan       state.OperationTarget
	parent     *os.Root
	directory  *os.File
	parentInfo os.FileInfo
	info       os.FileInfo
	link       *os.File
	tree       *uninstallExecutionTree
	current    string
	canonical  string
	cleaned    bool
}

type uninstallExecutionTree struct {
	root    *os.Root
	info    os.FileInfo
	entries []*uninstallExecutionEntry
}

type uninstallExecutionEntry struct {
	info   os.FileInfo
	file   *os.File
	tree   *uninstallExecutionTree
	digest [sha256.Size]byte
}

func (session *Session) executeUninstallLocked(ctx context.Context, identifier string, backup state.Backup) (resultErr error) {
	var targets []*uninstallExecutionTarget
	completed := false
	defer func() {
		for _, target := range targets {
			resultErr = errors.Join(resultErr, target.close())
		}
		session.active = ""
		if resultErr == nil {
			return
		}
		if completed {
			resultErr = fmt.Errorf("uninstall operation %s completed; backup %s retained; closing handles failed: %w", identifier, backup.ID, resultErr)
			return
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := session.store.MarkOperationNeedsAttention(recovery, identifier); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("attention status was not recorded; reopen and reconcile: %w", err))
		}
		resultErr = fmt.Errorf("uninstall operation %s requires recovery; backup %s retained at %s; inspect install-status: %w", identifier, backup.ID, backup.Path, resultErr)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if session.plan != identifier || identifier == "" {
		return fmt.Errorf("uninstall was not prepared in this session")
	}
	operation, err := session.store.GetOperation(ctx, identifier)
	if err != nil {
		return err
	}
	if operation.Status != "planned" || operation.Plan.Action != "uninstall" || operation.Plan.Backup.BackupID != backup.ID {
		return fmt.Errorf("uninstall plan changed")
	}
	if err := session.store.StartOperation(ctx, identifier); err != nil {
		return err
	}
	session.plan, session.active = "", identifier
	if err := verifyUninstallBackup(ctx, backup); err != nil {
		return err
	}
	for _, kind := range []string{"symlink", "canonical"} {
		for position, planned := range operation.Plan.Targets {
			if planned.Kind != kind {
				continue
			}
			target := &uninstallExecutionTarget{position: position, plan: planned, current: filepath.Base(planned.Path), canonical: backup.SourcePath}
			targets = append(targets, target)
			if err := target.open(ctx, backup); err != nil {
				return err
			}
		}
	}
	for _, target := range targets {
		if err := session.store.BeginUninstallTarget(ctx, identifier, target.position, target.plan.ExpectedIdentity); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyUninstallBackup(ctx, backup); err != nil {
			return err
		}
		if err := verifyUninstallExecutionTargets(ctx, targets); err != nil {
			return err
		}
		if err := target.verify(); err != nil {
			return err
		}
		if err := renameNoReplace(target.directory, filepath.Dir(target.plan.Path), target.current, target.plan.StageName); err != nil {
			return fmt.Errorf("quarantine uninstall target: %w", err)
		}
		target.current = target.plan.StageName
		if err := target.verify(); err != nil {
			return err
		}
		if err := session.store.MarkUninstallTargetMoved(ctx, identifier, target.position, target.plan.ExpectedIdentity); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if err := session.store.BeginUninstallCleanup(ctx, identifier, target.position, target.plan.ExpectedIdentity); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyUninstallBackup(ctx, backup); err != nil {
			return err
		}
		if err := verifyUninstallExecutionTargets(ctx, targets); err != nil {
			return err
		}
		if target.tree != nil {
			if err := target.tree.remove(ctx, target.verify); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := target.verify(); err != nil {
			return err
		}
		if err := target.parent.Remove(target.current); err != nil {
			return fmt.Errorf("remove quarantined uninstall target: %w", err)
		}
		if err := target.verifyAbsent(); err != nil {
			return err
		}
		if err := session.store.MarkUninstallTargetCleaned(ctx, identifier, target.position, target.plan.ExpectedIdentity); err != nil {
			return err
		}
		target.cleaned = true
	}
	if err := verifyUninstallBackup(ctx, backup); err != nil {
		return err
	}
	for _, target := range targets {
		if err := target.verifyAbsent(); err != nil {
			return err
		}
	}
	if err := session.store.CompleteUninstall(ctx, identifier); err != nil {
		return err
	}
	completed = true
	return nil
}

func verifyUninstallExecutionTargets(ctx context.Context, targets []*uninstallExecutionTarget) error {
	for _, target := range targets {
		if target.cleaned {
			if err := target.verifyAbsent(); err != nil {
				return err
			}
			continue
		}
		if err := target.verify(); err != nil {
			return err
		}
		if target.tree != nil {
			if err := target.tree.verify(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (target *uninstallExecutionTarget) open(ctx context.Context, backup state.Backup) error {
	parent := filepath.Dir(target.plan.Path)
	if err := validateUninstallParent(parent); err != nil {
		return err
	}
	var err error
	target.parent, err = os.OpenRoot(parent)
	if err != nil {
		return err
	}
	target.parentInfo, err = target.parent.Stat(".")
	if err != nil {
		return err
	}
	target.directory, err = target.parent.Open(".")
	if err != nil {
		return err
	}
	if _, err := target.parent.Lstat(target.plan.StageName); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("uninstall quarantine already exists or cannot be inspected: %w", errors.Join(err, fs.ErrExist))
	}
	target.info, err = target.parent.Lstat(target.current)
	if err != nil {
		return err
	}
	var identity string
	if target.plan.Kind == "symlink" {
		identity, err = objectid.FromLinkInfo(target.info)
	} else {
		identity, err = objectid.FromFileInfo(target.info)
	}
	if err != nil || identity != target.plan.ExpectedIdentity {
		return fmt.Errorf("uninstall target identity changed")
	}
	if target.plan.Kind == "symlink" {
		target.link, err = openProjectionLink(target.parent, target.current)
		if err != nil {
			return err
		}
	} else {
		root, err := target.parent.OpenRoot(target.current)
		if err != nil {
			return err
		}
		target.tree = &uninstallExecutionTree{root: root, info: target.info}
		if err := validateTrustedRoot(ctx, root, true); err != nil {
			return err
		}
		fingerprint := skill.BackupFingerprint{ContentHash: backup.ContentHash, ModeHash: backup.ModeHash, RootMode: backup.RootMode}
		if err := verifyUninstallCanonical(ctx, root, target.plan.Path, target.info, fingerprint, backup.InstallationHash); err != nil {
			return err
		}
		remaining := 4096
		if err := target.tree.capture(ctx, &remaining, 0); err != nil {
			return err
		}
		if err := verifyUninstallCanonical(ctx, root, target.plan.Path, target.info, fingerprint, backup.InstallationHash); err != nil {
			return err
		}
	}
	return target.verify()
}

func validateUninstallParent(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 || info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("uninstall parent path is not trusted")
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func (target *uninstallExecutionTarget) verifyParent() error {
	parent := filepath.Dir(target.plan.Path)
	if err := validateUninstallParent(parent); err != nil {
		return err
	}
	return verifyRestoreParent(target.parent, parent, target.parentInfo)
}

func (target *uninstallExecutionTarget) verify() error {
	if err := target.verifyParent(); err != nil {
		return err
	}
	current, err := target.parent.Lstat(target.current)
	if err != nil || !sameUninstallEntry(target.info, current) {
		return fmt.Errorf("uninstall target changed or was replaced")
	}
	if target.current != filepath.Base(target.plan.Path) {
		if err := uninstallNameAbsent(target.parent, filepath.Base(target.plan.Path)); err != nil {
			return err
		}
	}
	if target.link != nil {
		opened, err := target.link.Stat()
		if err != nil || !sameUninstallEntry(target.info, opened) {
			return fmt.Errorf("opened uninstall projection changed")
		}
		value, err := target.parent.Readlink(target.current)
		if err != nil || value != target.canonical {
			return fmt.Errorf("uninstall projection target changed")
		}
	} else if target.tree != nil {
		opened, err := target.tree.root.Stat(".")
		if err != nil || !sameUninstallEntry(target.info, opened) {
			return fmt.Errorf("opened uninstall canonical changed")
		}
	}
	return nil
}

func (target *uninstallExecutionTarget) verifyAbsent() error {
	if err := target.verifyParent(); err != nil {
		return err
	}
	return errors.Join(uninstallNameAbsent(target.parent, filepath.Base(target.plan.Path)), uninstallNameAbsent(target.parent, target.plan.StageName))
}

func uninstallNameAbsent(root *os.Root, name string) error {
	if _, err := root.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("uninstall path reappeared or cannot be inspected: %w", errors.Join(err, fs.ErrExist))
	}
	return nil
}

func sameUninstallEntry(expected, current os.FileInfo) bool {
	return expected != nil && current != nil && os.SameFile(expected, current) && expected.Mode() == current.Mode()
}

func (tree *uninstallExecutionTree) capture(ctx context.Context, remaining *int, depth int) error {
	if depth > 128 {
		return fmt.Errorf("uninstall directory depth exceeds 128")
	}
	entries, err := fs.ReadDir(tree.root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		*remaining -= 1
		if *remaining < 0 {
			return fmt.Errorf("uninstall tree exceeds 4096 entries")
		}
		info, err := tree.root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		item := &uninstallExecutionEntry{info: info}
		tree.entries = append(tree.entries, item)
		if info.IsDir() {
			root, err := tree.root.OpenRoot(info.Name())
			if err != nil {
				return err
			}
			item.tree = &uninstallExecutionTree{root: root, info: info}
			if err := item.tree.capture(ctx, remaining, depth+1); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			item.file, err = tree.root.OpenFile(info.Name(), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			item.digest, err = uninstallFileDigest(ctx, item.file, item.info)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("unsupported uninstall entry")
		}
	}
	return tree.verify(ctx)
}

func (tree *uninstallExecutionTree) verify(ctx context.Context) error {
	opened, err := tree.root.Stat(".")
	if err != nil || !sameUninstallEntry(tree.info, opened) {
		return fmt.Errorf("uninstall directory changed")
	}
	entries, err := fs.ReadDir(tree.root.FS(), ".")
	if err != nil {
		return err
	}
	if len(entries) != len(tree.entries) {
		return fmt.Errorf("uninstall directory contents changed")
	}
	for index, item := range tree.entries {
		if entries[index].Name() != item.info.Name() {
			return fmt.Errorf("uninstall directory entries changed")
		}
		if err := tree.verifyEntry(ctx, item); err != nil {
			return err
		}
		if item.tree != nil {
			if err := item.tree.verify(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tree *uninstallExecutionTree) verifyEntry(ctx context.Context, item *uninstallExecutionEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tree.verifyEntryIdentity(item); err != nil {
		return err
	}
	if item.file != nil {
		digest, err := uninstallFileDigest(ctx, item.file, item.info)
		if err != nil {
			return err
		}
		if digest != item.digest {
			return fmt.Errorf("uninstall file contents changed")
		}
	}
	return tree.verifyEntryIdentity(item)
}

func (tree *uninstallExecutionTree) verifyEntryIdentity(item *uninstallExecutionEntry) error {
	current, err := tree.root.Lstat(item.info.Name())
	if err != nil || !sameUninstallEntry(item.info, current) {
		return fmt.Errorf("uninstall entry changed or was replaced")
	}
	return nil
}

func (tree *uninstallExecutionTree) remove(ctx context.Context, anchor func() error) error {
	for _, item := range tree.entries {
		if err := anchor(); err != nil {
			return err
		}
		if err := tree.verifyEntry(ctx, item); err != nil {
			return err
		}
		if item.tree != nil {
			childAnchor := func() error {
				if err := anchor(); err != nil {
					return err
				}
				return tree.verifyEntryIdentity(item)
			}
			if err := item.tree.remove(ctx, childAnchor); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := anchor(); err != nil {
			return err
		}
		if err := tree.verifyEntry(ctx, item); err != nil {
			return err
		}
		if err := anchor(); err != nil {
			return err
		}
		if err := tree.verifyEntryIdentity(item); err != nil {
			return err
		}
		if err := tree.root.Remove(item.info.Name()); err != nil {
			return fmt.Errorf("remove verified uninstall entry: %w", err)
		}
		if err := uninstallNameAbsent(tree.root, item.info.Name()); err != nil {
			return err
		}
	}
	return nil
}

func uninstallFileDigest(ctx context.Context, file *os.File, expected os.FileInfo) (digest [sha256.Size]byte, resultErr error) {
	before, err := file.Stat()
	if err != nil || !sameUninstallEntry(expected, before) || before.Size() != expected.Size() || !before.ModTime().Equal(expected.ModTime()) {
		return digest, fmt.Errorf("opened uninstall file changed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return digest, err
	}
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return digest, err
		}
		count, err := file.Read(buffer)
		_, _ = hash.Write(buffer[:count])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return digest, err
		}
	}
	after, err := file.Stat()
	if err != nil || !sameUninstallEntry(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return digest, fmt.Errorf("uninstall file changed during inspection")
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func (tree *uninstallExecutionTree) close() error {
	var resultErr error
	for _, item := range tree.entries {
		if item.file != nil {
			resultErr = errors.Join(resultErr, item.file.Close())
		}
		if item.tree != nil {
			resultErr = errors.Join(resultErr, item.tree.close())
		}
	}
	return errors.Join(resultErr, tree.root.Close())
}

func (target *uninstallExecutionTarget) close() error {
	var resultErr error
	if target.link != nil {
		resultErr = errors.Join(resultErr, target.link.Close())
	}
	if target.tree != nil {
		resultErr = errors.Join(resultErr, target.tree.close())
	}
	if target.directory != nil {
		resultErr = errors.Join(resultErr, target.directory.Close())
	}
	if target.parent != nil {
		resultErr = errors.Join(resultErr, target.parent.Close())
	}
	return resultErr
}
