package sourcerefresh_test

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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/sourcerefresh"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/spf13/afero"
)

type refreshTransport func(*http.Request) (*http.Response, error)

func (transport refreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func publicSourceArchive(test *testing.T, version string) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, directory := range []string{"source/", "source/skills/", "source/skills/demo/", "source/skills/demo/.hidden/", "source/skills/demo/.hidden/empty/"} {
		if err := archive.WriteHeader(&tar.Header{Name: directory, Typeflag: tar.TypeDir, Mode: 0o700}); err != nil {
			test.Fatal(err)
		}
	}
	for _, entry := range []struct {
		name, data string
		mode       int64
	}{
		{"SKILL.md", "---\nname: Demo\ndescription: Public refresh fixture\n---\n" + version, 0o600},
		{".hidden/payload.bin", "\x00\xff" + version, 0o600},
		{"run", "#!/bin/sh\nprintf '" + version + "'\n", 0o700},
	} {
		if err := archive.WriteHeader(&tar.Header{Name: "source/skills/demo/" + entry.name, Typeflag: tar.TypeReg, Mode: entry.mode, Size: int64(len(entry.data))}); err != nil {
			test.Fatal(err)
		}
		if _, err := io.WriteString(archive, entry.data); err != nil {
			test.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		test.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func selectedStarSource(test *testing.T, store *state.Store, snapshot *repository.ArchiveSnapshot) state.CollectedRepository {
	test.Helper()
	token, err := github.NewToken("synthetic-worker-star-credential", nil)
	if err != nil {
		test.Fatal("synthetic credential creation failed")
	}
	generation, err := store.PutCredential(test.Context(), "alice", "viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	client, err := github.NewListsClient(token, &http.Client{Transport: refreshTransport(func(request *http.Request) (*http.Response, error) {
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			return nil, err
		}
		metadata := map[string]any{"id": "selected", "name": "selected", "slug": "selected", "isPrivate": false, "updatedAt": "2026-01-01T00:00:00Z", "lastAddedAt": "2026-01-01T00:00:00Z"}
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		var response any
		if query.Variables.ID == "" {
			response = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer", "lists": map[string]any{"pageInfo": page, "nodes": []any{metadata}}}}}
		} else {
			metadata["__typename"], metadata["user"] = "UserList", map[string]any{"id": "viewer"}
			metadata["items"] = map[string]any{"totalCount": 1, "pageInfo": page, "nodes": []any{map[string]any{"__typename": "Repository", "id": "remote", "nameWithOwner": "org/source", "isPrivate": false, "databaseId": nil}}}
			response = map[string]any{"data": map[string]any{"node": metadata}}
		}
		body, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	if err != nil {
		test.Fatal(err)
	}
	synchronize := func() state.ListSyncResult {
		ticket, err := store.BeginListSync(test.Context(), "alice", generation)
		if err != nil {
			test.Fatal(err)
		}
		result, err := store.ReadListSync(test.Context(), ticket, client)
		if err != nil {
			test.Fatal(err)
		}
		if err := store.ApplyListSync(test.Context(), result); err != nil {
			test.Fatal(err)
		}
		return result
	}
	synchronize()
	if err := store.SetListSubscription(test.Context(), "alice", "selected", true); err != nil {
		test.Fatal(err)
	}
	result := synchronize()
	collected, err := store.CollectListRepository(test.Context(), result, "remote", repository.Reference{NameWithOwner: "org/source"}, snapshot.Hash, snapshot.Skills)
	if err != nil {
		test.Fatal(err)
	}
	collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil || collected.Manual {
		test.Fatal("star collection did not remain star-only")
	}
	return collected
}

func TestPeriodicSourceRefreshTwoPacksConnectAndAutomaticWatch(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux installer")
	}
	for _, mode := range []string{"manual", "plugin", "star"} {
		test.Run(mode, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			key := bytes.Repeat([]byte{19}, 32)
			store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
			if err != nil {
				test.Fatal(err)
			}
			defer store.Close()
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			var source atomic.Value
			source.Store(publicSourceArchive(test, "first"))
			var fetches atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				fetches.Add(1)
				if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.URL.RawQuery != "" {
					test.Error("source request carried private authority")
				}
				_, _ = writer.Write(source.Load().([]byte))
			}))
			defer upstream.Close()
			upstreamURL, _ := url.Parse(upstream.URL)
			fetcher := repository.NewArchiveFetcher(&http.Client{Transport: refreshTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "codeload.github.com" || request.URL.Path != "/org/source/tar.gz/HEAD" {
					return nil, errors.New("unexpected source destination")
				}
				copy := request.Clone(request.Context())
				copy.URL = request.URL.Clone()
				copy.URL.Scheme, copy.URL.Host = upstreamURL.Scheme, upstreamURL.Host
				return upstream.Client().Transport.RoundTrip(copy)
			})})
			cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
			if err != nil {
				test.Fatal(err)
			}
			collection, err := library.NewService(store, cache, fetcher)
			if err != nil {
				test.Fatal(err)
			}
			defer collection.Close()
			var collected state.CollectedRepository
			if mode == "star" {
				snapshot, err := fetcher.Fetch(test.Context(), repository.Reference{NameWithOwner: "org/source"})
				if err != nil {
					test.Fatal(err)
				}
				if err := cache.Put(test.Context(), snapshot.Hash, snapshot.Data); err != nil {
					test.Fatal(err)
				}
				collected = selectedStarSource(test, store, snapshot)
			} else {
				input, branch := "org/source", ""
				if mode == "plugin" {
					preview, err := importer.PreviewMagpie(test.Context(), []byte(`{"skills":[{"name":"Demo","source":{"kind":"github","repo":"org/source","ref":"HEAD"}}]}`))
					if err != nil || len(preview.Repositories) != 1 {
						test.Fatal("server plugin preview failed")
					}
					entries, err := store.ListCollectedRepositories(test.Context(), "alice")
					if err != nil || len(entries) != 0 {
						test.Fatal("preview implicitly collected")
					}
					input, branch = preview.Repositories[0].Repo, preview.Repositories[0].Ref
				}
				collected, err = collection.Collect(test.Context(), "alice", input, branch)
				if err != nil {
					test.Fatal(err)
				}
			}
			collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
			if err != nil || len(collected.Skills) != 1 {
				test.Fatal("source fixture did not contain one collected skill")
			}
			distribution, err := packs.NewService(store, collection, key)
			if err != nil {
				test.Fatal(err)
			}
			defer distribution.Close()
			authentication, err := auth.NewService(store, key)
			if err != nil {
				test.Fatal(err)
			}
			var downloads atomic.Int64
			handler := httpapi.NewWithOptions(authentication, httpapi.Options{Store: store, Library: collection, Packs: distribution})
			endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/DownloadSkill") {
					downloads.Add(1)
				}
				handler.ServeHTTP(writer, request)
			}))
			defer endpoint.Close()
			home := test.TempDir()
			for _, relative := range []string{".agents/skills", ".claude/skills"} {
				if err := os.MkdirAll(filepath.Join(home, relative), 0o700); err != nil {
					test.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(directory, "client"), 0o700); err != nil {
				test.Fatal(err)
			}
			manager, err := subscription.Open(test.Context(), home, filepath.Join(directory, "client"), key, endpoint.Client())
			if err != nil {
				test.Fatal(err)
			}
			defer manager.Close()
			var originals []state.Pack
			var clients []*packclient.Client
			var hashes []string
			for _, name := range []string{"First", "Second"} {
				pack, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: name, Items: []state.PackSelection{{SkillID: collected.Skills[0].ID, Slug: "demo"}}})
				if err != nil {
					test.Fatal(err)
				}
				originals = append(originals, pack)
				share, err := distribution.CreateShare(test.Context(), "alice", pack.ID, nil)
				if err != nil {
					test.Fatal("share creation failed")
				}
				if _, err := manager.Subscribe(test.Context(), endpoint.URL, share.Token.Reveal()); err != nil {
					test.Fatal("subscription failed")
				}
				client, err := packclient.New(endpoint.URL, share.Token.Reveal(), endpoint.Client())
				if err != nil {
					test.Fatal("pack client creation failed")
				}
				manifest, err := client.Manifest(test.Context())
				if err != nil {
					test.Fatal("initial Connect manifest failed")
				}
				clients = append(clients, client)
				hashes = append(hashes, manifest.ManifestHash)
			}
			initial, err := manager.Sync(test.Context())
			if err != nil || initial.Installed != 1 || downloads.Load() != 1 {
				test.Fatal("initial overlapping subscription did not install once")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
			defer cancel()
			reports := make(chan subscription.WatchReport, 16)
			finished := make(chan error, 1)
			go func() {
				finished <- subscription.Watch(ctx, subscription.WatchOptions{Interval: time.Second, AttemptTimeout: 3 * time.Second}, manager.Sync, func(ctx context.Context, report subscription.WatchReport) error {
					select {
					case reports <- report:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			defer func() {
				cancel()
				if err := <-finished; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					test.Error("watch failed to stop")
				}
			}()
			select {
			case report := <-reports:
				if !report.Succeeded || report.Result.Reused != 1 {
					test.Fatal("initial watch did not reuse installation")
				}
			case <-ctx.Done():
				test.Fatal("watch did not start")
			}
			source.Store(publicSourceArchive(test, "second"))
			worker, err := sourcerefresh.New(store, collection, sourcerefresh.Options{Interval: time.Second, DataDirectory: directory})
			if err != nil {
				test.Fatal(err)
			}
			defer worker.Close()
			if err := worker.Start(ctx); err != nil {
				test.Fatal(err)
			}
			updated := false
			for !updated {
				select {
				case report := <-reports:
					if !report.Succeeded {
						test.Fatal("automatic watch cycle failed")
					}
					updated = report.Result.Updated == 1
				case <-ctx.Done():
					test.Fatal("periodic source refresh did not reach automatic Watch")
				}
			}
			if err := worker.Close(); err != nil {
				test.Fatal(err)
			}
			current, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
			if err != nil || current.ArchiveHash == collected.ArchiveHash || current.Skills[0].ID != collected.Skills[0].ID || current.PublicationRevision != collected.PublicationRevision+1 {
				test.Fatal("source snapshot was not atomically refreshed")
			}
			for index, original := range originals {
				pack, err := store.Pack(test.Context(), "alice", original.ID)
				if err != nil || pack.Revision != original.Revision+1 || pack.Items[0].ContentHash != current.Skills[0].ContentHash {
					test.Fatal("automatic source update did not revise both packs once")
				}
				manifest, err := clients[index].Manifest(test.Context())
				if err != nil || manifest.Revision != pack.Revision || manifest.ManifestHash == hashes[index] {
					test.Fatal("Connect manifest hash/revision did not advance")
				}
			}
			canonical := filepath.Join(home, ".agents", "skills", "demo")
			actual, err := skill.ContentHash(afero.NewOsFs(), canonical)
			if err != nil || actual != current.Skills[0].ContentHash || downloads.Load() != 2 || fetches.Load() < 2 {
				test.Fatal("automatic installation hash or single-download invariant failed")
			}
			if data, err := os.ReadFile(filepath.Join(canonical, ".hidden", "payload.bin")); err != nil || !bytes.Equal(data, []byte("\x00\xffsecond")) {
				test.Fatal("binary attachment did not update")
			}
			if info, err := os.Stat(filepath.Join(canonical, "run")); err != nil || info.Mode().Perm()&0o111 == 0 {
				test.Fatal("executable bit did not survive")
			}
			if info, err := os.Stat(filepath.Join(canonical, ".hidden", "empty")); err != nil || !info.IsDir() {
				test.Fatal("empty directory did not survive")
			}
		})
	}
}
