//go:build linux

package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/skill"
)

var errOfflineArchive = errors.New("offline archive verification failed: missing, unsafe or invalid content")

func VerifyOfflineArchive(ctx context.Context, owner *runtimegen.OfflineRoot, hash string) error {
	if ctx == nil || !validArchiveHash(hash) {
		return errOfflineArchive
	}
	return withOfflineArchives(ctx, owner, func(archives *os.Root, _ *os.File) error {
		return verifyOfflineArchiveFile(ctx, archives, hash)
	})
}

func withOfflineArchives(ctx context.Context, owner *runtimegen.OfflineRoot, consume func(*os.Root, *os.File) error) error {
	return owner.WithLegacyWorkerFiles(ctx, func(directory, _ *os.File) (result error) {
		root, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", directory.Fd()))
		if err != nil {
			return errOfflineArchive
		}
		defer func() { result = errors.Join(result, offlineArchiveClose(root)) }()
		borrowed, err := directory.Stat()
		opened, statError := root.Stat(".")
		if err != nil || statError != nil || !os.SameFile(borrowed, opened) {
			return errOfflineArchive
		}
		before, err := root.Lstat("archives")
		if err != nil || !offlineArchivePrivateDirectory(before) {
			return errOfflineArchive
		}
		archives, err := root.OpenRoot("archives")
		if err != nil {
			return errOfflineArchive
		}
		defer func() { result = errors.Join(result, offlineArchiveClose(archives)) }()
		opened, err = archives.Stat(".")
		if err != nil || !os.SameFile(before, opened) {
			return errOfflineArchive
		}
		handle, err := archives.Open(".")
		if err != nil {
			return errOfflineArchive
		}
		defer func() { result = errors.Join(result, offlineArchiveClose(handle)) }()
		if err := consume(archives, handle); err != nil {
			return err
		}
		after, err := root.Lstat("archives")
		if err != nil || !os.SameFile(before, after) || !offlineArchivePrivateDirectory(after) {
			return errOfflineArchive
		}
		return ctx.Err()
	})
}

func verifyOfflineArchiveFile(ctx context.Context, root *os.Root, hash string) (result error) {
	return verifyOfflineArchiveNamedFile(ctx, root, hash+".tar.gz", hash)
}

func verifyOfflineArchiveNamedFile(ctx context.Context, root *os.Root, name, hash string) error {
	data, _, err := readOfflinePrivateFile(ctx, root, name, repository.MaxArchiveBytes)
	if err != nil {
		return err
	}
	defer clear(data)
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != hash {
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

func readOfflinePrivateFile(ctx context.Context, root *os.Root, name string, limit int64) (data []byte, identity os.FileInfo, result error) {
	before, err := root.Lstat(name)
	if err != nil || !snapshotArchivePrivateFile(before) || before.Size() < 0 || before.Size() > limit {
		return nil, nil, errOfflineArchive
	}
	file, err := skill.RootedFS(root).Open(name)
	if err != nil {
		return nil, nil, errOfflineArchive
	}
	defer func() {
		result = errors.Join(result, offlineArchiveClose(file))
		if result != nil {
			clear(data)
			data, identity = nil, nil
		}
	}()
	opened, err := file.Stat()
	if err != nil || !snapshotArchivePrivateFile(opened) || !os.SameFile(before, opened) {
		return nil, nil, errOfflineArchive
	}
	data, err = io.ReadAll(io.LimitReader(cacheContextReader{ctx: ctx, reader: file}, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return data, nil, ctx.Err()
		}
		return data, nil, errOfflineArchive
	}
	after, err := root.Lstat(name)
	if err != nil || int64(len(data)) != before.Size() || !snapshotArchivePrivateFile(after) || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return data, nil, errOfflineArchive
	}
	return data, before, ctx.Err()
}

func offlineArchiveClose(closer io.Closer) error {
	if closer.Close() != nil {
		return errOfflineArchive
	}
	return nil
}

func offlineArchivePrivateDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return false
	}
	attributes, valid := info.Sys().(*syscall.Stat_t)
	return valid && attributes.Uid == uint32(os.Geteuid())
}
