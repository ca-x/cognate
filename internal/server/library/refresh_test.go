package library_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

type refreshTransport func(*http.Request) (*http.Response, error)

func (transport refreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type refreshOrigin struct {
	mutex            sync.Mutex
	data             []byte
	status, requests int
	block            bool
	release          <-chan struct{}
	started          chan struct{}
}

func (source *refreshOrigin) set(data []byte, status int) {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	source.data, source.status = data, status
}

func (source *refreshOrigin) count() int {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	return source.requests
}

func refreshArchive(test *testing.T, files map[string]string) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		mode := int64(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}
		if err := archive.WriteHeader(&tar.Header{Name: "source-main/" + path, Mode: mode, Size: int64(len(files[path]))}); err != nil {
			test.Fatal(err)
		}
		if _, err := io.WriteString(archive, files[path]); err != nil {
			test.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func refreshFiles(version string) map[string]string {
	return map[string]string{
		"skills/chosen/SKILL.md": "---\nname: Chosen\ndescription: A synthetic skill\n---\n" + version,
		"skills/chosen/run.sh":   "#!/bin/sh\nprintf '" + version + "'\n",
		"skills/other/SKILL.md":  "---\nname: Other\ndescription: Another synthetic skill\n---\nstable",
	}
}

func refreshFixture(test *testing.T, maxBytes int64) (*library.Service, *state.Store, *state.Store, *library.ArchiveCache, *refreshOrigin) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	peer, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peer.Close() })
	cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "objects"), library.CacheOptions{MaxBytes: maxBytes})
	if err != nil {
		test.Fatal(err)
	}
	source := &refreshOrigin{data: refreshArchive(test, refreshFiles("first")), status: 200, started: make(chan struct{}, 8)}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		source.mutex.Lock()
		data, status, block, release := source.data, source.status, source.block, source.release
		source.requests++
		source.mutex.Unlock()
		if block {
			source.started <- struct{}{}
			<-request.Context().Done()
			return
		}
		if release != nil {
			source.started <- struct{}{}
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		writer.WriteHeader(status)
		_, _ = writer.Write(data)
	}))
	test.Cleanup(server.Close)
	client := &http.Client{Transport: refreshTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "codeload.github.com" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.URL.Path != "/org/source/tar.gz/HEAD" {
			return nil, errors.New("unexpected public source request")
		}
		copy := request.Clone(request.Context())
		copy.URL.Scheme, copy.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(copy)
	})}
	service, err := library.NewService(store, cache, repository.NewArchiveFetcher(client))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	return service, store, peer, cache, source
}

func refreshBase(test *testing.T, service *library.Service) state.RepositoryRefreshSnapshot {
	test.Helper()
	collected, err := service.Collect(test.Context(), "alice", "org/source", "")
	if err != nil {
		test.Fatal(err)
	}
	base, err := service.RefreshState(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	return base
}

func assertRefreshDownload(test *testing.T, data []byte, expectedHash, version string) {
	test.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		test.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/skill", 0o700); err != nil {
		test.Fatal(err)
	}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			test.Fatal(err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Name != "skill/SKILL.md" && header.Name != "skill/run.sh" {
			test.Fatalf("unexpected distributed path: %s", header.Name)
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			test.Fatal(err)
		}
		if !bytes.Contains(content, []byte(version)) {
			test.Fatal("wrong archive generation")
		}
		if header.Name == "skill/run.sh" && header.Mode&0o111 == 0 {
			test.Fatal("executable permission lost")
		}
		if err := afero.WriteFile(filesystem, "/"+header.Name, content, os.FileMode(header.Mode)); err != nil {
			test.Fatal(err)
		}
	}
	actual, err := skill.ContentHashContext(test.Context(), filesystem, "/skill")
	if err != nil || actual != expectedHash {
		test.Fatalf("downloaded directory hash mismatch: %v", err)
	}
}

func TestManualRefreshFetchesNewArchiveAndChangesManifest(test *testing.T) {
	service, store, peer, cache, source := refreshFixture(test, 0)
	base := refreshBase(test, service)
	selected := base.Repository.Skills[0]
	pack, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Tools", Items: []state.PackSelection{{SkillID: selected.ID}}})
	if err != nil {
		test.Fatal(err)
	}
	distribution, err := packs.NewService(store, service, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer distribution.Close()
	share, err := distribution.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	before, err := distribution.Manifest(test.Context(), share.Token.Reveal())
	if err != nil {
		test.Fatal(err)
	}
	source.set(refreshArchive(test, refreshFiles("second")), 200)
	idempotent, err := service.Collect(test.Context(), "alice", "org/source", "")
	if err != nil || idempotent.ArchiveHash != base.Version.ArchiveHash || source.count() != 1 {
		test.Fatalf("Collect stopped being idempotent: %v", err)
	}
	result, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version)
	if err != nil {
		test.Fatal(err)
	}
	if !result.Changed || result.Updated != 1 || result.Added != 0 || result.Removed != 0 || source.count() != 2 || result.Repository.Skills[0].ID != selected.ID || result.Repository.ArchiveHash == base.Version.ArchiveHash {
		test.Fatal("refresh did not fetch and replace the source")
	}
	current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || !reflect.DeepEqual(current.Repository, result.Repository) || current.Version != result.Version {
		test.Fatalf("commit receipt mismatch: %v", err)
	}
	if err := cache.Verify(test.Context(), base.Version.ArchiveHash); err != nil {
		test.Fatalf("old cache removed: %v", err)
	}
	after, err := distribution.Manifest(test.Context(), share.Token.Reveal())
	if err != nil || after.Hash == before.Hash || after.Revision != before.Revision+1 {
		test.Fatalf("manifest did not change: %v", err)
	}
	var downloaded bytes.Buffer
	final := false
	if err := distribution.Download(test.Context(), share.Token.Reveal(), selected.ID, after.Skills[0].ContentHash, after.Revision, func(chunk packs.Chunk) error {
		if chunk.Final {
			final = true
		} else {
			_, _ = downloaded.Write(chunk.Data)
		}
		return nil
	}); err != nil || !final {
		test.Fatalf("new download: %v", err)
	}
	var expected bytes.Buffer
	if err := service.WriteSkillArchive(test.Context(), result.Version.ArchiveHash, selected.Path, after.Skills[0].ContentHash, &expected); err != nil || !bytes.Equal(downloaded.Bytes(), expected.Bytes()) {
		test.Fatalf("new archive download mismatched verified source: %v", err)
	}
	assertRefreshDownload(test, downloaded.Bytes(), after.Skills[0].ContentHash, "second")
}

func TestManualRefreshConcurrentFetchCannotRebaseAndRetainsCache(test *testing.T) {
	service, _, peer, cache, source := refreshFixture(test, 0)
	base := refreshBase(test, service)
	candidate := refreshArchive(test, refreshFiles("second"))
	release := make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()
	source.mutex.Lock()
	source.data, source.release = candidate, release
	source.mutex.Unlock()
	type outcome struct {
		result state.RepositoryRefreshResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version)
		finished <- outcome{result, err}
	}()
	<-source.started
	source.mutex.Lock()
	source.data, source.release = refreshArchive(test, refreshFiles("third")), nil
	source.mutex.Unlock()
	winner, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version)
	resume()
	loser := <-finished
	if err != nil || !winner.Changed {
		test.Fatalf("winning fetch: %v", err)
	}
	if !errors.Is(loser.err, state.ErrRepositoryRefreshConflict) || !reflect.DeepEqual(loser.result, state.RepositoryRefreshResult{}) {
		test.Fatalf("old fetched candidate auto-rebased: %v", loser.err)
	}
	current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || !reflect.DeepEqual(current.Repository, winner.Repository) || current.Version != winner.Version || source.count() != 3 {
		test.Fatalf("winning generation overwritten/refetched: %v", err)
	}
	digest := sha256.Sum256(candidate)
	for _, hash := range []string{base.Version.ArchiveHash, winner.Version.ArchiveHash, hex.EncodeToString(digest[:])} {
		if err := cache.Verify(test.Context(), hash); err != nil {
			test.Fatalf("immutable old/winner/orphan cache missing: %v", err)
		}
	}
}

func TestManualRefreshSourceAndCacheFailuresPreserveState(test *testing.T) {
	for _, failure := range []string{"404", "403", "429", "invalid", "empty", "cache-full", "cache-closed"} {
		test.Run(failure, func(test *testing.T) {
			budget := int64(0)
			if failure == "cache-full" {
				budget = int64(len(refreshArchive(test, refreshFiles("first"))))
			}
			service, _, peer, cache, source := refreshFixture(test, budget)
			base := refreshBase(test, service)
			candidate, status := refreshArchive(test, refreshFiles("second")), 200
			expected := repository.ErrArchiveUnavailable
			switch failure {
			case "404":
				status = 404
			case "403":
				status = 403
			case "429":
				status = 429
				expected = repository.ErrArchiveRateLimited
			case "invalid":
				candidate = []byte("invalid gzip")
				expected = repository.ErrArchiveInvalid
			case "empty":
				candidate = refreshArchive(test, map[string]string{"README.md": "no skills"})
				expected = repository.ErrArchiveInvalid
			case "cache-full":
				expected = library.ErrCacheFull
			case "cache-closed":
				if err := cache.Close(); err != nil {
					test.Fatal(err)
				}
				expected = nil
			}
			source.set(candidate, status)
			result, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version)
			if err == nil || expected != nil && !errors.Is(err, expected) || !reflect.DeepEqual(result, state.RepositoryRefreshResult{}) {
				test.Fatalf("failure did not preserve zero receipt: %v", err)
			}
			current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
			if err != nil || !reflect.DeepEqual(current, base) {
				test.Fatalf("source/cache failure changed stored state: %v", err)
			}
			if failure != "cache-closed" {
				if err := cache.Verify(test.Context(), base.Version.ArchiveHash); err != nil {
					test.Fatalf("failure damaged old cache: %v", err)
				}
			}
		})
	}
}

func TestManualRefreshPreflightAndPublishedPolicy(test *testing.T) {
	service, store, _, _, source := refreshFixture(test, 0)
	base := refreshBase(test, service)
	for _, attempt := range []struct {
		owner    string
		version  state.RepositoryVersion
		expected error
	}{
		{"bob", base.Version, state.ErrRepositoryNotFound},
		{"alice", state.RepositoryVersion{}, state.ErrRepositoryRefreshConflict},
		{"alice", state.RepositoryVersion{ArchiveHash: base.Version.ArchiveHash, PublicationRevision: 2}, state.ErrRepositoryRefreshConflict},
	} {
		result, err := service.Refresh(test.Context(), attempt.owner, base.Repository.ID, attempt.version)
		if !errors.Is(err, attempt.expected) || !reflect.DeepEqual(result, state.RepositoryRefreshResult{}) || source.count() != 1 {
			test.Fatalf("preflight performed fetch or exposed result: %v", err)
		}
	}
	if _, err := store.SetPublication(test.Context(), "alice", base.Repository.ID, 1, true, true); err != nil {
		test.Fatal(err)
	}
	base, err := service.RefreshState(test.Context(), "alice", base.Repository.ID)
	if err != nil {
		test.Fatal(err)
	}
	result, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version)
	if err != nil || result.Changed || result.Version != base.Version || source.count() != 2 {
		test.Fatalf("same published archive rejected: %v", err)
	}
	source.set(refreshArchive(test, refreshFiles("second")), 200)
	if _, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version); !errors.Is(err, state.ErrRepositoryRefreshPublished) {
		test.Fatalf("published contents silently changed: %v", err)
	}
}

func TestManualRefreshUnchangedSelectionKeepsManifestAndOldDownload(test *testing.T) {
	service, store, _, _, source := refreshFixture(test, 0)
	base := refreshBase(test, service)
	selected := base.Repository.Skills[1]
	pack, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Stable selection", Items: []state.PackSelection{{SkillID: selected.ID}}})
	if err != nil {
		test.Fatal(err)
	}
	distribution, err := packs.NewService(store, service, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer distribution.Close()
	share, err := distribution.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	before, err := distribution.Manifest(test.Context(), share.Token.Reveal())
	if err != nil {
		test.Fatal(err)
	}
	source.set(refreshArchive(test, refreshFiles("second")), 200)
	refreshed, final := false, false
	var refreshError error
	err = distribution.Download(test.Context(), share.Token.Reveal(), selected.ID, selected.ContentHash, 1, func(chunk packs.Chunk) error {
		if chunk.Final {
			final = true
			return nil
		}
		if !refreshed {
			refreshed = true
			_, refreshError = service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version)
		}
		return refreshError
	})
	if err != nil || refreshError != nil || !refreshed || !final {
		test.Fatalf("unchanged selection interrupted: %v %v", err, refreshError)
	}
	after, err := distribution.Manifest(test.Context(), share.Token.Reveal())
	if err != nil || after.Hash != before.Hash || after.Revision != before.Revision {
		test.Fatalf("unselected source update changed manifest: %v", err)
	}
}

func TestManualRefreshSlotsCancellationAndClose(test *testing.T) {
	service, _, peer, _, source := refreshFixture(test, 0)
	base := refreshBase(test, service)
	source.mutex.Lock()
	source.block = true
	source.mutex.Unlock()
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	finished := make(chan error, 2)
	for _, ctx := range []context.Context{ctx, test.Context()} {
		go func() { _, err := service.Refresh(ctx, "alice", base.Repository.ID, base.Version); finished <- err }()
	}
	for range 2 {
		<-source.started
	}
	if _, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version); !errors.Is(err, library.ErrCapacity) {
		test.Fatalf("unbounded refresh IO: %v", err)
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		test.Fatalf("caller cancellation: %v", err)
	}
	if err := service.Close(); err != nil {
		test.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		test.Fatalf("close did not cancel pending refresh: %v", err)
	}
	if _, err := service.Refresh(test.Context(), "alice", base.Repository.ID, base.Version); !errors.Is(err, library.ErrClosed) {
		test.Fatalf("refresh after close: %v", err)
	}
	current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || !reflect.DeepEqual(current, base) {
		test.Fatalf("canceled fetch changed state: %v", err)
	}
}

func TestManualRefreshOldDownloadsRejectFinal(test *testing.T) {
	for _, domain := range []string{"pack", "public"} {
		test.Run(domain, func(test *testing.T) {
			service, store, peer, cache, source := refreshFixture(test, 0)
			base := refreshBase(test, service)
			selected := base.Repository.Skills[0]
			pack, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Tools", Items: []state.PackSelection{{SkillID: selected.ID}}})
			if err != nil {
				test.Fatal(err)
			}
			distribution, err := packs.NewService(store, service, make([]byte, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer distribution.Close()
			share, err := distribution.CreateShare(test.Context(), "alice", pack.ID, nil)
			if err != nil {
				test.Fatal(err)
			}
			catalog, err := publiccatalog.NewService(store, service)
			if err != nil {
				test.Fatal(err)
			}
			defer catalog.Close()
			if domain == "public" {
				if _, err := store.SetPublication(test.Context(), "alice", base.Repository.ID, 1, true, true); err != nil {
					test.Fatal(err)
				}
			}
			source.set(refreshArchive(test, refreshFiles("second")), 200)
			var received bytes.Buffer
			var replacement state.RepositoryRefreshResult
			var commitError error
			committed, final := false, false
			send := func(chunk packs.Chunk) error {
				if chunk.Final {
					final = true
					return nil
				}
				_, _ = received.Write(chunk.Data)
				if committed {
					return nil
				}
				committed = true
				version := base.Version
				if domain == "public" {
					if _, commitError = peer.SetPublication(test.Context(), "alice", base.Repository.ID, 2, false, false); commitError != nil {
						return commitError
					}
					version.PublicationRevision = 3
				}
				replacement, commitError = service.Refresh(test.Context(), "alice", base.Repository.ID, version)
				if commitError != nil {
					return commitError
				}
				if domain == "public" {
					_, commitError = peer.SetPublication(test.Context(), "alice", base.Repository.ID, replacement.Version.PublicationRevision, true, true)
				}
				return commitError
			}
			if domain == "pack" {
				err = distribution.Download(test.Context(), share.Token.Reveal(), selected.ID, selected.ContentHash, 1, send)
				if !errors.Is(err, state.ErrPackShareUnavailable) {
					test.Fatalf("old pack did not reject final: %v", err)
				}
			} else {
				err = catalog.Download(test.Context(), selected.ID, selected.ContentHash, 2, send)
				if !errors.Is(err, state.ErrPublicationConflict) {
					test.Fatalf("old public request did not reject final: %v", err)
				}
			}
			if commitError != nil || !committed || final || received.Len() == 0 {
				test.Fatalf("real old download/commit barrier missing: committed=%v final=%v bytes=%d err=%v", committed, final, received.Len(), commitError)
			}
			var original bytes.Buffer
			if err := service.WriteSkillArchive(test.Context(), base.Version.ArchiveHash, selected.Path, selected.ContentHash, &original); err != nil || !bytes.Equal(original.Bytes(), received.Bytes()) {
				test.Fatalf("old reader did not stay pinned to immutable object: %v", err)
			}
			if err := cache.Verify(test.Context(), base.Version.ArchiveHash); err != nil {
				test.Fatal(err)
			}
			assertRefreshDownload(test, received.Bytes(), selected.ContentHash, "first")
			if err := cache.Verify(test.Context(), replacement.Version.ArchiveHash); err != nil {
				test.Fatal(err)
			}
		})
	}
}
