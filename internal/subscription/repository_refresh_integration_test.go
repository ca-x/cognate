package subscription_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

type repositoryRefreshTransport func(*http.Request) (*http.Response, error)

func (transport repositoryRefreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type countedLibraryArchives struct {
	service *library.Service
	calls   atomic.Int64
}

func (source *countedLibraryArchives) WriteSkillArchive(ctx context.Context, archiveHash, path, contentHash string, destination io.Writer) error {
	source.calls.Add(1)
	return source.service.WriteSkillArchive(ctx, archiveHash, path, contentHash, destination)
}

func subscriptionRepositoryArchive(test *testing.T, version string) ([]byte, string) {
	test.Helper()
	root := test.TempDir()
	files := map[string][]byte{
		"SKILL.md":            []byte("---\nname: Demo\ndescription: Repository refresh integration\n---\n" + version),
		".hidden/payload.bin": {0, 255, 128, 0, byte(len(version))},
		"run.sh":              []byte("#!/bin/sh\nprintf '" + version + "'\n"),
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		mode := os.FileMode(0o600)
		if name == "run.sh" {
			mode = 0o700
		}
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, files[name], mode); err != nil {
			test.Fatal(err)
		}
		if err := archive.WriteHeader(&tar.Header{Name: "source-main/skills/demo/" + name, Mode: int64(mode), Size: int64(len(files[name]))}); err != nil {
			test.Fatal(err)
		}
		if _, err := archive.Write(files[name]); err != nil {
			test.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), root)
	if err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes(), hash
}

func TestRepositoryRefreshUpdatesOverlappingSubscriptionsWithoutPackEdits(test *testing.T) {
	var upstream atomic.Pointer[[]byte]
	initialArchive, initialHash := subscriptionRepositoryArchive(test, "first")
	updatedArchive, updatedHash := subscriptionRepositoryArchive(test, "second version")
	upstream.Store(&initialArchive)
	var fetches atomic.Int64
	origin := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/org/source/tar.gz/HEAD" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			http.Error(writer, "unexpected upstream request", http.StatusBadRequest)
			return
		}
		fetches.Add(1)
		_, _ = writer.Write(*upstream.Load())
	}))
	test.Cleanup(origin.Close)
	var archives countedLibraryArchives
	setup := newFixtureWithArchiveSource(test, func(test *testing.T, store *serverstate.Store) packs.ArchiveSource {
		cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "objects"))
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() {
			if err := cache.Close(); err != nil {
				test.Error(err)
			}
		})
		client := &http.Client{Transport: repositoryRefreshTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.Scheme != "https" || request.URL.Host != "codeload.github.com" {
				return nil, errors.New("unexpected upstream host")
			}
			forward := request.Clone(request.Context())
			forward.URL.Host = strings.TrimPrefix(origin.URL, "https://")
			forward.Host = forward.URL.Host
			return origin.Client().Transport.RoundTrip(forward)
		})}
		archives.service, err = library.NewService(store, cache, repository.NewArchiveFetcher(client))
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() {
			if err := archives.service.Close(); err != nil {
				test.Error(err)
			}
		})
		return &archives
	})
	collected, err := archives.service.Collect(test.Context(), "alice", "org/source", "HEAD")
	if err != nil {
		test.Fatal("whole-repository collection failed", err)
	}
	collected, err = archives.service.Get(test.Context(), "alice", collected.ID)
	if err != nil || len(collected.Skills) != 1 || collected.Skills[0].ContentHash != initialHash {
		test.Fatal("whole-repository collection did not preserve skill content", err)
	}
	firstPack, firstToken := setup.newPack(test, "First", collected.Skills[0].ID)
	secondPack, secondToken := setup.newPack(test, "Second", collected.Skills[0].ID)
	first, err := setup.manager.Subscribe(test.Context(), setup.server.URL, firstToken)
	if err != nil {
		test.Fatal(err)
	}
	second, err := setup.manager.Subscribe(test.Context(), setup.server.URL, secondToken)
	if err != nil {
		test.Fatal(err)
	}
	result, err := setup.manager.Sync(test.Context())
	if err != nil || result.Installed != 1 || archives.calls.Load() != 1 {
		test.Fatal("overlapping repository skill was not installed exactly once", err)
	}
	canonical := filepath.Join(setup.home, ".agents", "skills", "demo")
	assertContent := func(expected string) {
		test.Helper()
		if actual, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || actual != expected {
			test.Fatal("canonical files or executable bits differ from repository source", err)
		}
		link := filepath.Join(setup.home, ".claude", "skills", "demo")
		info, err := os.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			test.Fatal("Claude target is not a canonical symlink", err)
		}
		if resolved, err := filepath.EvalSymlinks(link); err != nil || resolved != canonical {
			test.Fatal("Claude link does not resolve to the single canonical install", err)
		}
	}
	assertContent(initialHash)
	before, err := archives.service.RefreshState(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	upstream.Store(&updatedArchive)
	refreshed, err := archives.service.Refresh(test.Context(), "alice", collected.ID, before.Version)
	if err != nil || !refreshed.Changed || refreshed.Updated != 1 || len(refreshed.Packs) != 2 || fetches.Load() != 2 {
		test.Fatal("source refresh did not propagate to both packs", err)
	}
	for _, pack := range []serverstate.Pack{firstPack, secondPack} {
		current, err := setup.store.Pack(test.Context(), "alice", pack.ID)
		if err != nil || current.Revision <= pack.Revision || len(current.Items) != 1 || current.Items[0].ContentHash != updatedHash {
			test.Fatal("source refresh left a stale pack manifest", err)
		}
	}
	assertContent(initialHash)
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Updated != 1 || archives.calls.Load() != 2 || len(result.Backups) != 1 {
		test.Fatal("next client sync did not update exactly once", err)
	}
	assertContent(updatedHash)
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Reused != 1 || archives.calls.Load() != 2 {
		test.Fatal("unchanged packs downloaded duplicate content", err)
	}
	if err := setup.manager.Unsubscribe(test.Context(), first.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := setup.manager.Sync(test.Context()); err != nil {
		test.Fatal(err)
	}
	assertContent(updatedHash)
	if err := setup.manager.Unsubscribe(test.Context(), second.ID); err != nil {
		test.Fatal(err)
	}
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Removed != 1 || archives.calls.Load() != 2 {
		test.Fatal("last unsubscribe did not uninstall the shared skill", err)
	}
	for _, path := range []string{canonical, filepath.Join(setup.home, ".claude", "skills", "demo")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			test.Fatal("last unsubscribe left installed content or a dangling link")
		}
	}
}
