package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

func contentSnapshot(test *testing.T, version string) *repository.ArchiveSnapshot {
	test.Helper()
	content := "---\nname: Managed\ndescription: Synthetic managed content\n---\n" + version
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "source-main/SKILL.md", Mode: 0o644, Size: int64(len(content))}); err != nil {
		test.Fatal(err)
	}
	if _, err := io.WriteString(archive, content); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/source", 0o700); err != nil {
		test.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/source/SKILL.md", []byte(content), 0o644); err != nil {
		test.Fatal(err)
	}
	hash, err := skill.ContentHashContext(test.Context(), filesystem, "/source")
	if err != nil {
		test.Fatal(err)
	}
	data := buffer.Bytes()
	return &repository.ArchiveSnapshot{Data: data, Hash: archiveHash(data), Skills: []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Managed", Description: "Synthetic managed content", Path: "."}, ContentHash: hash}}}
}

func contentSelectedRepository(test *testing.T, store *state.Store, cache *ArchiveCache) state.CollectedRepository {
	test.Helper()
	token, err := github.NewToken("synthetic-managed-lists-token", nil)
	if err != nil {
		test.Fatal(err)
	}
	generation, err := store.PutCredential(test.Context(), "owner", "viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	client, err := github.NewListsClient(token, &http.Client{Transport: contentTransport(func(request *http.Request) (*http.Response, error) {
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			return nil, err
		}
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		metadata := map[string]any{"id": "selected", "name": "Selected", "slug": "selected", "isPrivate": false, "updatedAt": "2026-01-01T00:00:00Z", "lastAddedAt": "2026-01-01T00:00:00Z"}
		var response any
		if query.Variables.ID == "" {
			response = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer", "lists": map[string]any{"pageInfo": page, "nodes": []any{metadata}}}}}
		} else {
			metadata["__typename"], metadata["user"] = "UserList", map[string]any{"id": "viewer"}
			metadata["items"] = map[string]any{"totalCount": 1, "pageInfo": page, "nodes": []any{map[string]any{"__typename": "Repository", "id": "source-node", "nameWithOwner": "org/source", "isPrivate": false, "databaseId": nil}}}
			response = map[string]any{"data": map[string]any{"node": metadata}}
		}
		body, err := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, err
	})})
	if err != nil {
		test.Fatal(err)
	}
	var result state.ListSyncResult
	for round := range 2 {
		ticket, err := store.BeginListSync(test.Context(), "owner", generation)
		if err != nil {
			test.Fatal(err)
		}
		result, err = store.ReadListSync(test.Context(), ticket, client)
		if err != nil {
			test.Fatal(err)
		}
		if err := store.ApplyListSync(test.Context(), result); err != nil {
			test.Fatal(err)
		}
		if round == 0 {
			if err := store.SetListSubscription(test.Context(), "owner", "selected", true); err != nil {
				test.Fatal(err)
			}
		}
	}
	snapshot := contentSnapshot(test, "initial")
	entry, err := store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (state.CollectedRepository, error) {
		if err := cache.Put(ctx, snapshot.Hash, snapshot.Data); err != nil {
			return state.CollectedRepository{}, err
		}
		return store.CollectListRepository(ctx, result, "source-node", repository.Reference{NameWithOwner: "org/source"}, snapshot.Hash, snapshot.Skills)
	})
	if err != nil {
		test.Fatal(err)
	}
	return entry
}

type contentTransport func(*http.Request) (*http.Response, error)

func (transport contentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestManagedLibraryNetworkOutsideScope(test *testing.T) {
	for _, route := range []string{"collect", "plugin", "refresh", "automatic", "lists-automatic"} {
		test.Run(route, func(test *testing.T) {
			service, store, cache, instance := contentService(test)
			collecting := route == "collect" || route == "plugin"
			operationContext := test.Context()
			if route == "plugin" {
				hash := bytes.Repeat([]byte{0x73}, 32)
				_, err := store.CreateMCPCredential(test.Context(), "owner", hash, state.MCPMetadata{Name: "Managed plugin", Scopes: []string{state.MCPCatalogWrite}, ExpiresAt: time.Now().Add(time.Hour)})
				if err != nil {
					test.Fatal(err)
				}
				grant, err := store.AuthorizeMCP(test.Context(), hash, state.MCPCatalogWrite)
				if err != nil {
					test.Fatal(err)
				}
				operationContext = state.WithMCPAuthorization(operationContext, grant, state.MCPCatalogWrite)
			}
			var original state.CollectedRepository
			var ticket state.SourceRefreshTicket
			if !collecting {
				var err error
				if route == "lists-automatic" {
					original = contentSelectedRepository(test, store, cache)
				} else {
					original, err = service.Collect(test.Context(), "owner", "org/source", "")
				}
				if err != nil {
					test.Fatal(err)
				}
				ticket, err = store.BeginRepositorySourceRefresh(test.Context(), "owner", original.ID)
				if err != nil {
					test.Fatal(err)
				}
			}
			snapshot := contentSnapshot(test, "network-"+route)
			entered, release := make(chan struct{}), make(chan struct{})
			defer releaseCacheBarrier(release)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				close(entered)
				select {
				case <-release:
					_, _ = writer.Write(snapshot.Data)
				case <-request.Context().Done():
				}
			}))
			test.Cleanup(server.Close)
			client := &http.Client{Transport: contentTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "codeload.github.com" || request.Header.Get("Authorization") != "" {
					return nil, errors.New("unexpected managed network fixture request")
				}
				clone := request.Clone(request.Context())
				clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
				return server.Client().Transport.RoundTrip(clone)
			})}
			service.fetcher = repository.NewArchiveFetcher(client)
			finished := make(chan error, 1)
			go func() {
				if collecting {
					var plugin interface {
						Collect(context.Context, string, string, string) (state.CollectedRepository, error)
					} = service
					_, err := plugin.Collect(operationContext, "owner", "org/source", "")
					finished <- err
					return
				}
				if route == "automatic" || route == "lists-automatic" {
					_, err := service.RefreshAutomatic(test.Context(), ticket)
					finished <- err
					return
				}
				_, err := service.Refresh(test.Context(), "owner", original.ID, state.RepositoryVersion{ArchiveHash: original.ArchiveHash, PublicationRevision: original.PublicationRevision})
				finished <- err
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				test.Fatal("real loopback fetch did not start")
			}
			ctx, cancel := context.WithTimeout(test.Context(), time.Second)
			defer cancel()
			exclusive, err := instance.Quiesce(ctx)
			if err != nil {
				test.Fatal("network retained a maintenance writer lease")
			}
			defer exclusive.Release()
			releaseCacheBarrier(release)
			select {
			case err := <-finished:
				if !errors.Is(err, maintenance.ErrQuiescing) {
					test.Fatalf("network finishing in maintenance did not fail before publication: %v", err)
				}
			case <-time.After(2 * time.Second):
				test.Fatal("completed network request did not exit")
			}
			if _, err := os.Stat(filepath.Join(cache.directory, snapshot.Hash+".tar.gz")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("network result published inside Exclusive")
			}
			stages, err := filepath.Glob(filepath.Join(cache.directory, ".stage-*"))
			if err != nil || len(stages) != 0 {
				test.Fatal("rejected network request created a cache stage")
			}
			entries, err := store.ListCollectedRepositories(test.Context(), "owner")
			if err != nil || collecting && len(entries) != 0 || !collecting && (len(entries) != 1 || entries[0].ArchiveHash != original.ArchiveHash) {
				test.Fatal("rejected request changed database content")
			}
		})
	}
}

func TestManagedLibraryComponentLifetime(test *testing.T) {
	service, store, cache, instance := contentService(test)
	directory := filepath.Dir(cache.directory)
	peer, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peer.Close() })
	peerCache, err := OpenArchiveCacheWithOptions(test.Context(), cache.directory, CacheOptions{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peerCache.Close() })
	barrier := &cacheBarrierFS{Fs: cache.filesystem, phase: "write", entered: make(chan struct{}), release: make(chan struct{})}
	cache.filesystem = barrier
	defer releaseCacheBarrier(barrier.release)
	finished := make(chan error, 1)
	go func() { _, err := service.Collect(test.Context(), "owner", "org/source", ""); finished <- err }()
	select {
	case <-barrier.entered:
	case <-time.After(2 * time.Second):
		test.Fatal("managed cache write did not start")
	}
	if err := errors.Join(peer.Close(), peerCache.Close(), instance.Close()); err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "blocked")
	serviceClosed, storeClosed := make(chan error, 1), make(chan error, 1)
	go func() { serviceClosed <- service.Close() }()
	go func() { storeClosed <- store.Close() }()
	select {
	case <-service.lifetime.Done():
	case <-time.After(time.Second):
		test.Fatal("service Close did not cancel its operation")
	}
	select {
	case <-serviceClosed:
		test.Fatal("service Close passed its live cache child")
	default:
	}
	select {
	case <-storeClosed:
		test.Fatal("Store Close passed its live logical content scope")
	default:
	}
	cacheProcessProbe(test, directory, "blocked")
	releaseCacheBarrier(barrier.release)
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			test.Fatalf("close cancellation changed: %v", err)
		}
	case <-time.After(2 * time.Second):
		test.Fatal("canceled cache write did not drain")
	}
	for _, closed := range []<-chan error{serviceClosed, storeClosed} {
		select {
		case err := <-closed:
			if err != nil {
				test.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			test.Fatal("component Close did not drain")
		}
	}
	cacheProcessProbe(test, directory, "released")
	stages, err := filepath.Glob(filepath.Join(cache.directory, ".stage-*"))
	if err != nil || len(stages) != 0 {
		test.Fatal("canceled content write left an unclean stage")
	}
}

type contentPrivateFetcher struct{ snapshot *repository.ArchiveSnapshot }

func (*contentPrivateFetcher) Fetch(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
	return nil, errors.New("anonymous fallback forbidden")
}
func (fetcher *contentPrivateFetcher) FetchMetadata(ctx context.Context, reference repository.Reference, _ github.Token, check func(context.Context) error) (repository.GitHubMetadata, error) {
	if err := check(ctx); err != nil {
		return repository.GitHubMetadata{}, err
	}
	return repository.GitHubMetadata{NodeID: "private-node", NameWithOwner: reference.NameWithOwner, Private: true}, nil
}
func (fetcher *contentPrivateFetcher) FetchPrivate(ctx context.Context, _ repository.Reference, _ github.Token, check func(context.Context) error) (*repository.ArchiveSnapshot, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	return fetcher.snapshot, nil
}

func TestManagedLibraryRejectedCommitKeepsOrphan(test *testing.T) {
	for _, invalidation := range []string{"source", "mcp", "cas"} {
		test.Run(invalidation, func(test *testing.T) {
			service, store, cache, instance := contentService(test)
			ctx := test.Context()
			var generation string
			var credential state.MCPCredential
			var original state.CollectedRepository
			var ticket state.SourceRefreshTicket
			winner := contentSnapshot(test, "winner")
			if invalidation == "cas" {
				var err error
				original, err = service.Collect(ctx, "owner", "org/source", "")
				if err != nil {
					test.Fatal(err)
				}
				ticket, err = store.BeginRepositorySourceRefresh(ctx, "owner", original.ID)
				if err != nil {
					test.Fatal(err)
				}
				if err := cache.Put(ctx, winner.Hash, winner.Data); err != nil {
					test.Fatal(err)
				}
			}
			snapshot := contentSnapshot(test, "rejected-"+invalidation)
			if invalidation == "source" {
				token, err := github.NewToken("synthetic-content-token", nil)
				if err != nil {
					test.Fatal(err)
				}
				generation, err = store.PutCredential(ctx, "owner", "viewer", "", token)
				if err != nil {
					test.Fatal(err)
				}
				service.fetcher = &contentPrivateFetcher{snapshot: snapshot}
			} else {
				service.fetcher = fetchArchive(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return snapshot, nil })
			}
			if invalidation == "mcp" {
				hash := bytes.Repeat([]byte{0x37}, 32)
				var err error
				credential, err = store.CreateMCPCredential(ctx, "owner", hash, state.MCPMetadata{Name: "Content gate", Scopes: []string{state.MCPCatalogWrite}, ExpiresAt: time.Now().Add(time.Hour)})
				if err != nil {
					test.Fatal(err)
				}
				grant, err := store.AuthorizeMCP(ctx, hash, state.MCPCatalogWrite)
				if err != nil {
					test.Fatal(err)
				}
				ctx = state.WithMCPAuthorization(ctx, grant, state.MCPCatalogWrite)
			}
			barrier := &cacheBarrierFS{Fs: cache.filesystem, phase: "cleanup", entered: make(chan struct{}), release: make(chan struct{})}
			cache.filesystem = barrier
			defer releaseCacheBarrier(barrier.release)
			finished := make(chan error, 1)
			go func() {
				if invalidation == "cas" {
					_, err := service.RefreshAutomatic(ctx, ticket)
					finished <- err
					return
				}
				_, err := service.Collect(ctx, "owner", "org/source", "")
				finished <- err
			}()
			select {
			case <-barrier.entered:
			case <-time.After(2 * time.Second):
				test.Fatal("rejection fixture never published candidate")
			}
			switch invalidation {
			case "source":
				token, _ := github.NewToken("synthetic-content-replacement", nil)
				if _, err := store.PutCredential(test.Context(), "owner", "viewer", generation, token); err != nil {
					test.Fatal(err)
				}
			case "mcp":
				if _, err := store.RevokeMCPCredential(test.Context(), "owner", credential.ID, credential.Revision); err != nil {
					test.Fatal(err)
				}
			case "cas":
				if _, err := store.RefreshCollectedRepository(test.Context(), "owner", original.ID, ticket.Snapshot().Version, winner.Hash, winner.Skills); err != nil {
					test.Fatal(err)
				}
			}
			releaseCacheBarrier(barrier.release)
			expectedError := map[string]error{"source": state.ErrSourceAccessUnavailable, "mcp": state.ErrMCPUnauthorized, "cas": state.ErrRepositoryRefreshConflict}[invalidation]
			select {
			case err := <-finished:
				if !errors.Is(err, expectedError) {
					test.Fatalf("stale source/MCP/CAS error classification changed: %v", err)
				}
			case <-time.After(2 * time.Second):
				test.Fatal("rejected content operation did not return")
			}
			entries, err := store.ListCollectedRepositories(test.Context(), "owner")
			if err != nil || invalidation != "cas" && len(entries) != 0 || invalidation == "cas" && (len(entries) != 1 || entries[0].ArchiveHash != winner.Hash || entries[0].PublicationRevision != original.PublicationRevision+1) {
				test.Fatal("rejected candidate altered the authoritative database")
			}
			if err := cache.Verify(test.Context(), snapshot.Hash); err != nil {
				test.Fatal("bounded orphan was unexpectedly deleted or corrupt")
			}
			ctx, cancel := context.WithTimeout(test.Context(), time.Second)
			defer cancel()
			exclusive, err := instance.Quiesce(ctx)
			if err != nil {
				test.Fatal("failed operation leaked a maintenance lease")
			}
			exclusive.Release()
		})
	}
}

type contentFailFS struct{ afero.Fs }
type contentFailFile struct{ afero.File }

func (filesystem contentFailFS) OpenFile(name string, flags int, mode os.FileMode) (afero.File, error) {
	file, err := filesystem.Fs.OpenFile(name, flags, mode)
	if err == nil && strings.HasPrefix(name, ".stage-") {
		return contentFailFile{File: file}, nil
	}
	return file, err
}
func (contentFailFile) Write([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestManagedLibraryCleanupScope(test *testing.T) {
	service, store, cache, instance := contentService(test)
	barrier := &cacheBarrierFS{Fs: contentFailFS{Fs: cache.filesystem}, phase: "cleanup", entered: make(chan struct{}), release: make(chan struct{})}
	cache.filesystem = barrier
	defer releaseCacheBarrier(barrier.release)
	finished := make(chan error, 1)
	go func() { _, err := service.Collect(test.Context(), "owner", "org/source", ""); finished <- err }()
	select {
	case <-barrier.entered:
	case <-time.After(2 * time.Second):
		test.Fatal("failed stage cleanup did not run")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 50*time.Millisecond)
	exclusive, err := instance.Quiesce(ctx)
	cancel()
	if exclusive != nil {
		exclusive.Release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("quiescence passed a live cache cleanup tail")
	}
	releaseCacheBarrier(barrier.release)
	select {
	case err := <-finished:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			test.Fatalf("cache error changed: %v", err)
		}
	case <-time.After(2 * time.Second):
		test.Fatal("failed stage cleanup did not finish")
	}
	entries, err := store.ListCollectedRepositories(test.Context(), "owner")
	if err != nil || len(entries) != 0 {
		test.Fatal("failed cache publication wrote database content")
	}
	stages, err := filepath.Glob(filepath.Join(cache.directory, ".stage-*"))
	if err != nil || len(stages) != 0 {
		test.Fatal("failed content operation leaked a stage")
	}
	ctx, cancel = context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	exclusive, err = instance.Quiesce(ctx)
	if err != nil {
		test.Fatal("cleanup leaked admission")
	}
	exclusive.Release()
}

func TestManagedLibraryPeerMismatch(test *testing.T) {
	for _, mode := range []string{"store-only", "cache-only", "different-root", "replaced-cache", "same-physical-root", "default"} {
		test.Run(mode, func(test *testing.T) {
			store, cache, _, directory := managedCacheFixture(test)
			if mode == "store-only" || mode == "default" {
				plain, err := OpenArchiveCache(filepath.Join(directory, "plain-archives"))
				if err != nil {
					test.Fatal(err)
				}
				test.Cleanup(func() { _ = plain.Close() })
				cache = plain
			}
			if mode == "cache-only" || mode == "default" {
				plain, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
				if err != nil {
					test.Fatal(err)
				}
				test.Cleanup(func() { _ = plain.Close() })
				store = plain
			}
			if mode == "different-root" {
				_, cache, _, _ = managedCacheFixture(test)
			}
			if mode == "same-physical-root" {
				shared, err := OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "second-archives"), CacheOptions{MaintenanceManaged: true})
				if err != nil {
					test.Fatal(err)
				}
				test.Cleanup(func() { _ = shared.Close() })
				cache = shared
			}
			if mode == "replaced-cache" {
				if err := os.Rename(cache.directory, cache.directory+"-old"); err != nil {
					test.Fatal(err)
				}
				if err := os.Mkdir(cache.directory, 0o700); err != nil {
					test.Fatal(err)
				}
			}
			snapshot := contentSnapshot(test, "pairing")
			service, err := NewService(store, cache, fetchArchive(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return snapshot, nil }))
			valid := mode == "same-physical-root" || mode == "default"
			if valid {
				if err != nil {
					test.Fatal(err)
				}
				service.cancel()
			} else if err == nil || service != nil {
				if service != nil {
					service.cancel()
				}
				test.Error("library constructor accepted partial or mismatched physical barrier")
			}
			if err := store.CreateUser(test.Context(), "constructor-owner"); err != nil {
				test.Fatal("pairing check closed its caller-owned Store")
			}
			if mode != "replaced-cache" {
				if err := cache.Put(test.Context(), snapshot.Hash, snapshot.Data); err != nil {
					test.Fatal("pairing check closed its caller-owned cache")
				}
			}
			if mode == "default" {
				cacheProcessProbe(test, directory, "default")
			}
		})
	}
}

func contentService(test *testing.T) (*Service, *state.Store, *ArchiveCache, *maintenance.Instance) {
	test.Helper()
	store, cache, instance, _ := managedCacheFixture(test)
	if err := store.CreateUser(test.Context(), "owner"); err != nil {
		test.Fatal(err)
	}
	initial := contentSnapshot(test, "initial")
	service, err := NewService(store, cache, fetchArchive(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
		return initial, nil
	}))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	return service, store, cache, instance
}

func TestManagedLibraryContentCommitScope(test *testing.T) {
	for _, route := range []string{"collect", "plugin", "refresh", "automatic", "lists-automatic"} {
		test.Run(route, func(test *testing.T) {
			service, store, cache, instance := contentService(test)
			collecting := route == "collect" || route == "plugin"
			operationContext := test.Context()
			if route == "plugin" {
				hash := bytes.Repeat([]byte{0x73}, 32)
				_, err := store.CreateMCPCredential(test.Context(), "owner", hash, state.MCPMetadata{Name: "Managed plugin", Scopes: []string{state.MCPCatalogWrite}, ExpiresAt: time.Now().Add(time.Hour)})
				if err != nil {
					test.Fatal(err)
				}
				grant, err := store.AuthorizeMCP(test.Context(), hash, state.MCPCatalogWrite)
				if err != nil {
					test.Fatal(err)
				}
				operationContext = state.WithMCPAuthorization(operationContext, grant, state.MCPCatalogWrite)
			}
			var original state.CollectedRepository
			var automatic state.SourceRefreshTicket
			var packs []state.Pack
			if !collecting {
				var err error
				if route == "lists-automatic" {
					original = contentSelectedRepository(test, store, cache)
				} else {
					original, err = service.Collect(test.Context(), "owner", "org/source", "")
				}
				if err != nil {
					test.Fatal(err)
				}
				original, err = store.CollectedRepository(test.Context(), "owner", original.ID)
				if err != nil || len(original.Skills) != 1 {
					test.Fatal("initial repository detail missing")
				}
				for _, name := range []string{"first", "second"} {
					pack, err := store.CreatePack(test.Context(), "owner", state.PackDraft{Name: name, Items: []state.PackSelection{{SkillID: original.Skills[0].ID, Slug: "managed"}}})
					if err != nil {
						test.Fatal(err)
					}
					packs = append(packs, pack)
				}
				automatic, err = store.BeginRepositorySourceRefresh(test.Context(), "owner", original.ID)
				if err != nil {
					test.Fatal(err)
				}
			}
			snapshot := contentSnapshot(test, route)
			service.fetcher = fetchArchive(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return snapshot, nil })
			barrier := &cacheBarrierFS{Fs: cache.filesystem, phase: "cleanup", entered: make(chan struct{}), release: make(chan struct{})}
			cache.filesystem = barrier
			defer releaseCacheBarrier(barrier.release)
			finished := make(chan error, 1)
			go func() {
				if collecting {
					_, err := service.Collect(operationContext, "owner", "org/source", "")
					finished <- err
					return
				}
				if route == "automatic" || route == "lists-automatic" {
					_, err := service.RefreshAutomatic(test.Context(), automatic)
					finished <- err
					return
				}
				_, err := service.Refresh(test.Context(), "owner", original.ID, state.RepositoryVersion{ArchiveHash: original.ArchiveHash, PublicationRevision: original.PublicationRevision})
				finished <- err
			}()
			select {
			case <-barrier.entered:
			case <-time.After(2 * time.Second):
				test.Fatal("cache publication barrier not reached")
			}
			entries, err := store.ListCollectedRepositories(test.Context(), "owner")
			if err != nil || collecting && len(entries) != 0 || !collecting && (len(entries) != 1 || entries[0].ArchiveHash != original.ArchiveHash) {
				test.Fatal("barrier was not before database content update")
			}
			quiesced := make(chan *maintenance.Exclusive, 1)
			quiesceContext, cancel := context.WithTimeout(test.Context(), 3*time.Second)
			defer cancel()
			go func() {
				exclusive, err := instance.Quiesce(quiesceContext)
				if err != nil {
					exclusive = nil
				}
				quiesced <- exclusive
			}()
			var exclusive *maintenance.Exclusive
			received := false
			defer func() {
				cancel()
				if !received {
					exclusive = <-quiesced
				}
				if exclusive != nil {
					exclusive.Release()
				}
			}()
			awaitCacheAdmissionClosed(test, cache)
			select {
			case exclusive = <-quiesced:
				received = true
				exclusive.Release()
				test.Error("collect outer content scope did not cover cache-to-commit gap")
			default:
			}
			releaseCacheBarrier(barrier.release)
			select {
			case err := <-finished:
				if err != nil {
					test.Fatalf("%s lost nested admission after cache publication: %v", route, err)
				}
			case <-time.After(2 * time.Second):
				test.Fatal("admitted collection did not complete during quiescence")
			}
			if exclusive == nil {
				select {
				case exclusive = <-quiesced:
					received = true
				case <-time.After(2 * time.Second):
					test.Fatal("completed collection did not drain")
				}
			}
			defer exclusive.Release()
			entries, err = store.ListCollectedRepositories(test.Context(), "owner")
			if err != nil || len(entries) != 1 || entries[0].ArchiveHash != snapshot.Hash {
				test.Fatal("collection and cache closure disagree")
			}
			if err := cache.Verify(test.Context(), snapshot.Hash); err != nil {
				test.Fatal(err)
			}
			for _, prior := range packs {
				pack, err := store.Pack(test.Context(), "owner", prior.ID)
				if err != nil || pack.Revision != prior.Revision+1 || len(pack.Items) != 1 || pack.Items[0].ArchiveHash != snapshot.Hash || pack.Items[0].ContentHash != snapshot.Skills[0].ContentHash {
					test.Fatal("repository, both Packs and archive closure disagree")
				}
			}
			if !exclusive.ValidFor(instance) {
				test.Fatal("quiescence was not ready after collection")
			}
		})
	}
}
