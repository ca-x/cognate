//go:build linux

package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
	"golang.org/x/sys/unix"
)

type archiveRepairSyncer interface{ Sync() error }

type archiveRepairOps struct {
	write  func(context.Context, afero.File, []byte) error
	sync   func(archiveRepairSyncer) error
	close  func(io.Closer) error
	rename func(int, string, int, string, uint) error
}

func defaultArchiveRepairOps() archiveRepairOps {
	return archiveRepairOps{
		write: func(ctx context.Context, file afero.File, data []byte) error {
			_, err := io.Copy(file, cacheContextReader{ctx: ctx, reader: bytes.NewReader(data)})
			return err
		},
		sync:   func(file archiveRepairSyncer) error { return file.Sync() },
		close:  func(file io.Closer) error { return file.Close() },
		rename: unix.Renameat2,
	}
}

func ReadArchiveRepairInput(ctx context.Context, path string) (data []byte, result error) {
	if ctx == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errOfflineArchive
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, errOfflineArchive
	}
	defer func() {
		result = errors.Join(result, offlineArchiveClose(root))
		if result != nil {
			clear(data)
			data = nil
		}
	}()
	data, _, result = readOfflinePrivateFile(ctx, root, filepath.Base(path), repository.MaxArchiveBytes)
	return data, result
}

func RepairOfflineArchive(ctx context.Context, owner *runtimegen.OfflineRoot, hash, repairID string, data []byte) (ArchiveRepairResult, error) {
	return repairOfflineArchive(ctx, owner, hash, repairID, data, defaultArchiveRepairOps())
}

func repairOfflineArchive(ctx context.Context, owner *runtimegen.OfflineRoot, hash, repairID string, data []byte, operations archiveRepairOps) (outcome ArchiveRepairResult, result error) {
	if ctx == nil || !validArchiveHash(hash) || !validArchiveRepairID(repairID) || len(data) == 0 || len(data) > repository.MaxArchiveBytes {
		return outcome, errOfflineArchive
	}
	if err := validRepairContent(ctx, data, hash); err != nil {
		return outcome, err
	}
	changed := false
	defer func() {
		if result != nil {
			outcome = ArchiveRepairResult{}
			if changed {
				result = errors.Join(ErrArchiveRepairUncertain, result)
			}
		}
	}()
	result = withOfflineArchives(ctx, owner, func(root *os.Root, directory *os.File) (returned error) {
		originalLock, err := root.Lstat(".cache.lock")
		if err != nil || !snapshotArchivePrivateFile(originalLock) {
			return errOfflineArchive
		}
		lockPath := fmt.Sprintf("/proc/self/fd/%d/.cache.lock", directory.Fd())
		lock, err := filelock.AcquireExisting(ctx, lockPath)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errOfflineArchive
		}
		lockInfo, err := lock.Stat()
		defer func() {
			if returned == nil {
				current, statError := root.Lstat(".cache.lock")
				if statError != nil || !snapshotArchivePrivateFile(current) || !os.SameFile(lockInfo, current) {
					returned = errOfflineArchive
				}
			}
			returned = errors.Join(returned, repairClose(operations, lock))
		}()
		if err != nil || !snapshotArchivePrivateFile(lockInfo) || !os.SameFile(originalLock, lockInfo) {
			return errOfflineArchive
		}
		budget, err := offlineArchiveBudget(ctx, root)
		if err != nil {
			return err
		}
		used, err := offlineArchiveUsage(ctx, root, budget)
		if err != nil {
			return err
		}
		target := hash + ".tar.gz"
		stage := ".repair-" + hash + "-" + repairID + ".tar.gz"
		before, healthy, err := offlineRepairTarget(ctx, root, target, hash)
		if err != nil {
			return err
		}
		stageInfo, err := root.Lstat(stage)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errOfflineArchive
		}
		if healthy {
			outcome = ArchiveRepairResult{AlreadyVerified: true, Retained: stageInfo != nil}
			return nil
		}
		if stageInfo == nil {
			if int64(len(data)) > budget-used {
				return ErrCacheFull
			}
			file, err := skill.RootedFS(root).OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return errOfflineArchive
			}
			changed = true
			created, statError := file.Stat()
			writeError := operations.write(ctx, file, data)
			syncError := operations.sync(file)
			closeError := operations.close(file)
			if errors.Join(statError, writeError, syncError, closeError) != nil || !snapshotArchivePrivateFile(created) {
				return errOfflineArchive
			}
			stageInfo = created
		}
		if err := verifyOfflineArchiveNamedFile(ctx, root, stage, hash); err != nil {
			return err
		}
		stageNow, err := root.Lstat(stage)
		if err != nil || !os.SameFile(stageInfo, stageNow) {
			return errOfflineArchive
		}
		if err := syncRepairStage(root, stage, stageNow, operations); err != nil {
			return err
		}
		if err := operations.sync(directory); err != nil {
			return errOfflineArchive
		}
		current, _, err := offlineRepairTarget(ctx, root, target, hash)
		if err != nil || !sameOptionalArchive(before, current) {
			return errOfflineArchive
		}
		if err := confirmRepairEntry(root, stage, stageNow); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags := uint(unix.RENAME_EXCHANGE)
		if before == nil {
			flags = unix.RENAME_NOREPLACE
		}
		changed = true
		if err := operations.rename(int(directory.Fd()), stage, int(directory.Fd()), target, flags); err != nil {
			return errOfflineArchive
		}
		if err := confirmRepairEntry(root, target, stageNow); err != nil {
			return err
		}
		retained, err := root.Lstat(stage)
		if before != nil {
			if err != nil || !sameOptionalArchive(before, retained) {
				return errOfflineArchive
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return errOfflineArchive
		}
		if err := verifyOfflineArchiveFile(ctx, root, hash); err != nil {
			return err
		}
		if err := operations.sync(directory); err != nil {
			return errOfflineArchive
		}
		if err := confirmRepairEntry(root, target, stageNow); err != nil {
			return err
		}
		if before != nil {
			if err := confirmRepairEntry(root, stage, before); err != nil {
				return err
			}
		} else if _, err := root.Lstat(stage); !errors.Is(err, fs.ErrNotExist) {
			return errOfflineArchive
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome.Retained = before != nil
		return nil
	})
	return outcome, result
}

func validRepairContent(ctx context.Context, data []byte, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if archiveHash(data) != hash {
		return errOfflineArchive
	}
	if _, err := repository.InspectArchive(ctx, data); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errOfflineArchive
	}
	return ctx.Err()
}

func offlineRepairTarget(ctx context.Context, root *os.Root, name, hash string) (os.FileInfo, bool, error) {
	if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, errOfflineArchive
	}
	data, identity, err := readOfflinePrivateFile(ctx, root, name, repository.MaxArchiveBytes)
	if err != nil {
		return nil, false, err
	}
	defer clear(data)
	err = validRepairContent(ctx, data, hash)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	return identity, err == nil, nil
}

func offlineArchiveBudget(ctx context.Context, root *os.Root) (int64, error) {
	data, _, err := readOfflinePrivateFile(ctx, root, ".cache-budget", 64)
	if err != nil {
		return 0, ErrCachePolicy
	}
	defer clear(data)
	value, ok := strings.CutPrefix(string(data), "skillhub-archive-budget-v1:")
	if !ok {
		return 0, ErrCachePolicy
	}
	value, ok = strings.CutSuffix(value, "\n")
	budget, err := strconv.ParseInt(value, 10, 64)
	if !ok || err != nil || budget <= 0 || budget > 1<<40 || strconv.FormatInt(budget, 10) != value {
		return 0, ErrCachePolicy
	}
	return budget, nil
}

func offlineArchiveUsage(ctx context.Context, root *os.Root, budget int64) (used int64, result error) {
	directory, err := root.Open(".")
	if err != nil {
		return 0, errOfflineArchive
	}
	defer func() { result = errors.Join(result, offlineArchiveClose(directory)) }()
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		names, err := directory.Readdirnames(128)
		for _, name := range names {
			entry, statError := root.Lstat(name)
			if statError != nil || !snapshotArchivePrivateFile(entry) || entry.Size() < 0 {
				return 0, errOfflineArchive
			}
			if name == ".cache-budget" || name == ".cache.lock" {
				continue
			}
			if entry.Size() > budget-used {
				return 0, ErrCacheFull
			}
			used += entry.Size()
		}
		if errors.Is(err, io.EOF) {
			return used, nil
		}
		if err != nil {
			return 0, errOfflineArchive
		}
	}
}

func sameOptionalArchive(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return snapshotArchivePrivateFile(after) && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func confirmRepairEntry(root *os.Root, name string, expected os.FileInfo) error {
	current, err := root.Lstat(name)
	if err != nil || !sameOptionalArchive(expected, current) {
		return errOfflineArchive
	}
	return nil
}

func syncRepairStage(root *os.Root, name string, expected os.FileInfo, operations archiveRepairOps) error {
	file, err := skill.RootedFS(root).Open(name)
	if err != nil {
		return errOfflineArchive
	}
	opened, statError := file.Stat()
	syncError := operations.sync(file)
	closeError := operations.close(file)
	if errors.Join(statError, syncError, closeError) != nil || !sameOptionalArchive(expected, opened) {
		return errOfflineArchive
	}
	return confirmRepairEntry(root, name, expected)
}

func repairClose(operations archiveRepairOps, closer io.Closer) error {
	if operations.close(closer) != nil {
		return errOfflineArchive
	}
	return nil
}
