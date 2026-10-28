package library

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

type ArchiveCache struct {
	root       *os.Root
	filesystem afero.Fs
	directory  string
	maxBytes   int64
}

const DefaultCacheBytes int64 = 512 << 20

var (
	ErrCacheFull   = errors.New("repository archive cache capacity exhausted")
	ErrCachePolicy = errors.New("repository archive cache budget does not match its persisted policy")
)

type CacheOptions struct{ MaxBytes int64 }

func OpenArchiveCache(directory string) (*ArchiveCache, error) {
	return OpenArchiveCacheWithOptions(context.Background(), directory, CacheOptions{})
}

func OpenArchiveCacheWithOptions(ctx context.Context, directory string, options CacheOptions) (*ArchiveCache, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultCacheBytes
	}
	if options.MaxBytes < 0 || options.MaxBytes > 1<<40 {
		return nil, fmt.Errorf("repository archive cache budget must be positive and at most 1 TiB")
	}
	root, err := state.OpenPrivateDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("open repository archive cache")
	}
	cache := &ArchiveCache{root: root, filesystem: skill.RootedFS(root), directory: directory, maxBytes: options.MaxBytes}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lock, err := filelock.Acquire(ctx, filepath.Join(directory, ".cache.lock"))
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	defer lock.Close()
	if err := cache.verifyDirectory(); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := cache.checkPolicy(true); err != nil {
		_ = root.Close()
		return nil, err
	}
	return cache, nil
}

func (cache *ArchiveCache) Put(ctx context.Context, hash string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validArchiveHash(hash) || len(data) == 0 || len(data) > repository.MaxArchiveBytes || archiveHash(data) != hash {
		return fmt.Errorf("invalid repository archive content")
	}
	lock, err := filelock.Acquire(ctx, filepath.Join(cache.directory, ".cache.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := cache.verifyDirectory(); err != nil {
		return err
	}
	if err := cache.checkPolicy(false); err != nil {
		return err
	}
	if _, err := cache.filesystem.Stat(hash + ".tar.gz"); err == nil {
		return cache.Verify(ctx, hash)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect existing repository archive")
	}
	used, err := cache.usedBytes(ctx)
	if err != nil {
		return err
	}
	if int64(len(data)) > cache.maxBytes-used {
		return ErrCacheFull
	}
	stage := ".stage-" + rand.Text()
	file, err := cache.filesystem.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create repository archive stage")
	}
	defer cache.filesystem.Remove(stage)
	_, writeErr := io.Copy(file, cacheContextReader{ctx: ctx, reader: bytes.NewReader(data)})
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cache.root.Link(stage, hash+".tar.gz"); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("publish repository archive")
		}
		if err := cache.Verify(ctx, hash); err != nil {
			return err
		}
	}
	if err := cache.filesystem.Remove(stage); err != nil {
		return fmt.Errorf("remove repository archive stage")
	}
	return cache.syncDirectory()
}

func (cache *ArchiveCache) Read(ctx context.Context, hash string) ([]byte, error) {
	var buffer bytes.Buffer
	if err := cache.copyVerified(ctx, hash, &buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (cache *ArchiveCache) Verify(ctx context.Context, hash string) error {
	return cache.copyVerified(ctx, hash, io.Discard)
}

func (cache *ArchiveCache) copyVerified(ctx context.Context, hash string, destination io.Writer) error {
	if !validArchiveHash(hash) {
		return fmt.Errorf("invalid repository archive hash")
	}
	file, err := afero.NewReadOnlyFs(cache.filesystem).Open(hash + ".tar.gz")
	if err != nil {
		return fmt.Errorf("open repository archive")
	}
	defer file.Close()
	digest := sha256.New()
	count, err := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(cacheContextReader{ctx: ctx, reader: file}, repository.MaxArchiveBytes+1))
	if err != nil {
		return err
	}
	if count > repository.MaxArchiveBytes || hex.EncodeToString(digest.Sum(nil)) != hash {
		return fmt.Errorf("repository archive integrity check failed")
	}
	return nil
}

func (cache *ArchiveCache) Close() error { return cache.root.Close() }

func (cache *ArchiveCache) verifyDirectory() error {
	entry, err := os.Lstat(cache.directory)
	if err != nil {
		return fmt.Errorf("inspect repository archive directory")
	}
	opened, err := cache.root.Stat(".")
	if err != nil || !entry.IsDir() || !os.SameFile(entry, opened) {
		return fmt.Errorf("repository archive directory changed")
	}
	return nil
}

func (cache *ArchiveCache) usedBytes(ctx context.Context) (int64, error) {
	directory, err := cache.filesystem.Open(".")
	if err != nil {
		return 0, fmt.Errorf("read repository archive directory")
	}
	defer directory.Close()
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		entries, err := directory.Readdir(128)
		for _, entry := range entries {
			if !entry.Mode().IsRegular() || entry.Size() < 0 {
				return 0, fmt.Errorf("repository archive cache contains an unsupported entry")
			}
			if entry.Name() == ".cache-budget" || entry.Name() == ".cache.lock" {
				continue
			}
			if entry.Size() > cache.maxBytes-total {
				return 0, ErrCacheFull
			}
			total += entry.Size()
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read repository archive cache usage")
		}
	}
}

func (cache *ArchiveCache) checkPolicy(initialize bool) error {
	expected := "skillhub-archive-budget-v1:" + strconv.FormatInt(cache.maxBytes, 10) + "\n"
	file, err := afero.NewReadOnlyFs(cache.filesystem).Open(".cache-budget")
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, 64))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || string(data) != expected {
			return ErrCachePolicy
		}
		return nil
	}
	if !initialize || !errors.Is(err, fs.ErrNotExist) {
		return ErrCachePolicy
	}
	file, err = cache.filesystem.OpenFile(".cache-budget", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrCachePolicy
	}
	_, writeErr := io.WriteString(file, expected)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		_ = cache.filesystem.Remove(".cache-budget")
		return ErrCachePolicy
	}
	return cache.syncDirectory()
}

func (cache *ArchiveCache) syncDirectory() error {
	directory, err := cache.root.Open(".")
	if err != nil {
		return fmt.Errorf("open repository archive directory")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync repository archive directory")
	}
	return nil
}

func validArchiveHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func archiveHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type cacheContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader cacheContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
