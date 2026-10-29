package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

type fetchArchive func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)

func TestValidateMaintenancePairLifecycle(test *testing.T) {
	if err := ValidateMaintenancePair(test.Context(), nil, nil); err == nil {
		test.Fatal("nil components accepted")
	}
	for _, closed := range []string{"none", "store", "cache"} {
		test.Run(closed, func(test *testing.T) {
			service, store, cache, instance := contentService(test)
			ctx, cancel := context.WithCancel(test.Context())
			cancel()
			if err := ValidateMaintenancePair(ctx, store, cache); !errors.Is(err, context.Canceled) {
				test.Fatalf("pair ignored cancellation: %v", err)
			}
			if closed == "store" {
				if err := store.Close(); err != nil {
					test.Fatal(err)
				}
			}
			if closed == "cache" {
				if err := cache.Close(); err != nil {
					test.Fatal(err)
				}
			}
			if closed != "none" {
				if err := ValidateMaintenancePair(test.Context(), store, cache); !errors.Is(err, maintenance.ErrClosed) {
					test.Fatalf("pair accepted closed component: %v", err)
				}
				return
			}
			exclusive, err := instance.Quiesce(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			if err := ValidateMaintenancePair(test.Context(), store, cache); !errors.Is(err, maintenance.ErrQuiescing) {
				exclusive.Release()
				test.Fatalf("pair ignored admission: %v", err)
			}
			exclusive.Release()
			if err := ValidateMaintenancePair(test.Context(), store, cache); err != nil {
				test.Fatal(err)
			}
			if _, err := service.Collect(test.Context(), "owner", "org/source", ""); err != nil {
				test.Fatalf("validation retained a lease or closed resources: %v", err)
			}
		})
	}
}

func (fetch fetchArchive) Fetch(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetch(ctx, reference)
}

func fixtureArchive(test *testing.T) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "repo-main/SKILL.md", Mode: 0o644, Size: 7}); err != nil {
		test.Fatal(err)
	}
	if _, err := io.WriteString(archive, "example"); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func TestArchiveCacheIntegrityAndRestart(test *testing.T) {
	directory := filepath.Join(test.TempDir(), "objects")
	cache, err := OpenArchiveCache(directory)
	if err != nil {
		test.Fatal(err)
	}
	data := fixtureArchive(test)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatal(err)
	}
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatalf("idempotent publication: %v", err)
	}
	if err := cache.Put(test.Context(), strings.Repeat("f", 64), data); err == nil {
		test.Fatal("incorrect content hash accepted")
	}
	if _, err := cache.Read(test.Context(), "../escape"); err == nil {
		test.Fatal("unsafe cache path accepted")
	}
	if err := cache.Close(); err != nil {
		test.Fatal(err)
	}
	cache, err = OpenArchiveCache(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer cache.Close()
	stored, err := cache.Read(test.Context(), hash)
	if err != nil || !bytes.Equal(stored, data) {
		test.Fatalf("cached content did not survive restart: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, hash+".tar.gz"), []byte("corrupt"), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := cache.Read(test.Context(), hash); err == nil {
		test.Fatal("corrupt cache accepted")
	}
}

func TestLibraryCloseCancelsAndWaitsForFetch(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	cache, err := OpenArchiveCache(filepath.Join(directory, "objects"))
	if err != nil {
		test.Fatal(err)
	}
	started := make(chan struct{})
	exited := make(chan struct{})
	service, err := NewService(store, cache, fetchArchive(func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(started)
		<-ctx.Done()
		defer close(exited)
		return nil, ctx.Err()
	}))
	if err != nil {
		test.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.Collect(test.Context(), "alice", "org/repo", ""); done <- err }()
	<-started
	if err := service.Close(); err != nil {
		test.Fatal(err)
	}
	select {
	case <-exited:
	default:
		test.Fatal("close returned before fetch exited")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		test.Fatalf("fetch cancellation: %v", err)
	}
	if _, err := service.Collect(test.Context(), "alice", "org/repo", ""); !errors.Is(err, ErrClosed) {
		test.Fatalf("new work accepted after close: %v", err)
	}
	entries, err := store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(entries) != 0 {
		test.Fatalf("canceled fetch changed catalog: %v", err)
	}
}

func TestLibraryDuplicateDoesNotReportMissingArchiveAsSuccess(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	cache, err := OpenArchiveCache(filepath.Join(directory, "objects"))
	if err != nil {
		test.Fatal(err)
	}
	data := fixtureArchive(test)
	snapshot := &repository.ArchiveSnapshot{Data: data, Hash: archiveHash(data), Skills: []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "A skill", Path: "."}, ContentHash: strings.Repeat("a", 64)}}}
	service, err := NewService(store, cache, fetchArchive(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return snapshot, nil }))
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	entry, err := service.Collect(test.Context(), "alice", "org/repo", "")
	if err != nil {
		test.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "objects", snapshot.Hash+".tar.gz")); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Collect(test.Context(), "alice", "org/repo", ""); !errors.Is(err, ErrArchiveMissing) {
		test.Fatalf("missing archive reported as successful collection: %v", err)
	}
	if _, err := service.Get(test.Context(), "alice", entry.ID); !errors.Is(err, ErrArchiveMissing) {
		test.Fatalf("missing archive not reported on browse: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "objects", snapshot.Hash+".tar.gz"), []byte("corrupt"), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Collect(test.Context(), "alice", "org/repo", ""); !errors.Is(err, ErrArchiveMissing) {
		test.Fatalf("corrupt archive reported as successful collection: %v", err)
	}
}

func TestArchiveCacheBudgetIncludesAbandonedStages(test *testing.T) {
	directory := filepath.Join(test.TempDir(), "objects")
	data := fixtureArchive(test)
	cache, err := OpenArchiveCacheWithOptions(test.Context(), directory, CacheOptions{MaxBytes: int64(len(data))})
	if err != nil {
		test.Fatal(err)
	}
	defer cache.Close()
	if err := os.WriteFile(filepath.Join(directory, ".stage-interrupted"), []byte("abandoned"), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := cache.Put(test.Context(), archiveHash(data), data); !errors.Is(err, ErrCacheFull) {
		test.Fatalf("abandoned stage bypassed budget: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, archiveHash(data)+".tar.gz")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("over-budget object published")
	}
	if err := os.Remove(filepath.Join(directory, ".stage-interrupted")); err != nil {
		test.Fatal(err)
	}
	if err := cache.Put(test.Context(), archiveHash(data), data); err != nil {
		test.Fatal(err)
	}
	if err := cache.Put(test.Context(), archiveHash(data), data); err != nil {
		test.Fatalf("existing object counted twice: %v", err)
	}
}

func TestArchiveCacheBudgetAcrossTwoInstances(test *testing.T) {
	directory := filepath.Join(test.TempDir(), "objects")
	first := fixtureArchive(test)
	second := append(bytes.Clone(first), byte(1))
	options := CacheOptions{MaxBytes: int64(len(first) + len(second) - 1)}
	caches := make([]*ArchiveCache, 0, 2)
	for range 2 {
		cache, err := OpenArchiveCacheWithOptions(test.Context(), directory, options)
		if err != nil {
			test.Fatal(err)
		}
		defer cache.Close()
		caches = append(caches, cache)
	}
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index, data := range [][]byte{first, second} {
		workers.Go(func() { results <- caches[index].Put(test.Context(), archiveHash(data), data) })
	}
	workers.Wait()
	close(results)
	succeeded, exhausted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrCacheFull) {
			exhausted++
		} else {
			test.Fatal(err)
		}
	}
	if succeeded != 1 || exhausted != 1 {
		test.Fatalf("shared budget exceeded: success=%d exhausted=%d", succeeded, exhausted)
	}
}

func TestArchiveCacheRejectsConflictingSharedBudget(test *testing.T) {
	directory := filepath.Join(test.TempDir(), "objects")
	first, err := OpenArchiveCacheWithOptions(test.Context(), directory, CacheOptions{MaxBytes: 100})
	if err != nil {
		test.Fatal(err)
	}
	defer first.Close()
	second, err := OpenArchiveCacheWithOptions(test.Context(), directory, CacheOptions{MaxBytes: 160})
	if second != nil {
		_ = second.Close()
	}
	if err == nil {
		test.Fatal("conflicting shared cache budget accepted")
	}
}

func TestConcurrentCollectionVerifiesTheCommittedSnapshot(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	cache, err := OpenArchiveCache(filepath.Join(directory, "objects"))
	if err != nil {
		test.Fatal(err)
	}
	data := fixtureArchive(test)
	skills := []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "A skill", Path: "."}, ContentHash: strings.Repeat("a", 64)}}
	started, proceed := make(chan struct{}), make(chan struct{})
	service, err := NewService(store, cache, fetchArchive(func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(started)
		select {
		case <-proceed:
			return &repository.ArchiveSnapshot{Data: data, Hash: archiveHash(data), Skills: skills}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	result := make(chan error, 1)
	go func() { _, err := service.Collect(test.Context(), "alice", "org/repo", ""); result <- err }()
	<-started
	reference, _ := repository.ParseReference("org/repo", "")
	if _, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), skills); err != nil {
		test.Fatal(err)
	}
	close(proceed)
	if err := <-result; !errors.Is(err, ErrArchiveMissing) {
		test.Fatalf("committed conflicting snapshot was not verified: %v", err)
	}
}
