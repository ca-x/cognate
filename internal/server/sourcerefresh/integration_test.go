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
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/scheduler"
	"github.com/ca-x/cognate/internal/server/sourcerefresh"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	clientstate "github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/spf13/afero"
	"google.golang.org/protobuf/proto"
)

type refreshTransport func(*http.Request) (*http.Response, error)

func (transport refreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func readWatchManifest(response *http.Response) (*packv1.GetManifestResponse, error) {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(body) > 8<<20 {
		return nil, errors.New("watch fixture manifest response unavailable")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	payload := body
	switch response.Header.Get("Content-Encoding") {
	case "":
	case "gzip":
		compressed, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("watch fixture manifest compression invalid")
		}
		decoded, readErr := io.ReadAll(io.LimitReader(compressed, (8<<20)+1))
		closeErr := compressed.Close()
		if readErr != nil || closeErr != nil || len(decoded) > 8<<20 {
			return nil, errors.New("watch fixture manifest compression incomplete")
		}
		payload = decoded
	default:
		return nil, errors.New("watch fixture manifest compression unsupported")
	}
	manifest := new(packv1.GetManifestResponse)
	if err := proto.Unmarshal(payload, manifest); err != nil {
		return nil, errors.New("watch fixture manifest response invalid")
	}
	return manifest, nil
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
	result, _ := selectedSourceSync(test, store, false)
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

func selectedSourceSync(test *testing.T, store *state.Store, private bool) (state.ListSyncResult, *http.Client) {
	test.Helper()
	token, err := github.NewToken("synthetic-worker-star-credential", nil)
	if err != nil {
		test.Fatal("synthetic credential creation failed")
	}
	credential, credentialError := store.Credential(test.Context(), "alice")
	generation := credential.Generation
	if errors.Is(credentialError, state.ErrCredentialNotFound) {
		generation, err = store.PutCredential(test.Context(), "alice", "viewer", "", token)
	} else if credentialError != nil {
		test.Fatal(credentialError)
	} else {
		token = credential.Token
	}
	if err != nil {
		test.Fatal(err)
	}
	transport := &http.Client{Transport: refreshTransport(func(request *http.Request) (*http.Response, error) {
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
			metadata["items"] = map[string]any{"totalCount": 1, "pageInfo": page, "nodes": []any{map[string]any{"__typename": "Repository", "id": "remote", "nameWithOwner": "org/source", "isPrivate": private, "databaseId": nil}}}
			response = map[string]any{"data": map[string]any{"node": metadata}}
		}
		body, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	client, err := github.NewListsClient(token, transport)
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
	return synchronize(), transport
}

func TestPeriodicSourceRefreshTwoPacksConnectAndAutomaticWatch(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux installer")
	}
	testSourceRefreshTwoPacksConnectAndAutomaticWatch(test, []string{"manual", "plugin", "star"})
}

func TestPrivateSourceTwoPacksConnectAndAutomaticWatch(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux installer")
	}
	testSourceRefreshTwoPacksConnectAndAutomaticWatch(test, []string{"private-manual", "private-lists"})
}

func testSourceRefreshTwoPacksConnectAndAutomaticWatch(test *testing.T, modes []string) {
	for _, mode := range modes {
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
			peer, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			var source atomic.Value
			source.Store(publicSourceArchive(test, "first"))
			var fetches atomic.Int64
			var publicationArmed atomic.Bool
			publicationRelease := make(chan struct{})
			releasePublication := sync.OnceFunc(func() { close(publicationRelease) })
			private := strings.HasPrefix(mode, "private-")
			var metadataCalls, privateCalls atomic.Int64
			var denied atomic.Int64
			var rotateAt atomic.Value
			var rotations atomic.Int64
			rotateAt.Store("")
			rotate := func(stage string) {
				if rotateAt.Load().(string) != stage {
					return
				}
				credential, err := peer.Credential(test.Context(), "alice")
				if err != nil {
					test.Error("credential rotation fixture unavailable")
					return
				}
				if _, err := peer.PutCredential(test.Context(), "alice", credential.ViewerID, credential.Generation, credential.Token); err != nil {
					test.Error("credential rotation fixture failed")
				} else {
					rotations.Add(1)
				}
			}
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if !private || request.Header.Get("Authorization") != "Bearer synthetic-worker-star-credential" {
					test.Error("API authority missing")
					writer.WriteHeader(401)
					return
				}
				if status := denied.Load(); status != 0 {
					writer.WriteHeader(int(status))
					_, _ = io.WriteString(writer, "synthetic-secret-body")
					return
				}
				switch request.URL.Path {
				case "/repos/org/source":
					metadataCalls.Add(1)
					_, _ = io.WriteString(writer, `{"node_id":"remote","full_name":"Org/Source","private":true}`)
					rotate("metadata")
				case "/repos/org/source/tarball":
					privateCalls.Add(1)
					if metadataCalls.Load() < privateCalls.Load() {
						test.Error("archive preceded metadata")
					}
					writer.Header().Set("Location", "https://codeload.github.com/org/source/tar.gz/private-capability?signature=synthetic")
					writer.WriteHeader(302)
					rotate("redirect")
				default:
					test.Error("unexpected API path")
					writer.WriteHeader(404)
				}
			}))
			defer api.Close()
			apiURL, _ := url.Parse(api.URL)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				fetches.Add(1)
				if publicationArmed.Load() {
					select {
					case <-publicationRelease:
					case <-request.Context().Done():
						return
					}
				}
				expectedQuery := ""
				if private {
					expectedQuery = "signature=synthetic"
				}
				if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Referer") != "" || request.URL.RawQuery != expectedQuery {
					test.Error("source request carried private authority")
				}
				_, _ = writer.Write(source.Load().([]byte))
				rotate("body")
			}))
			defer upstream.Close()
			upstreamURL, _ := url.Parse(upstream.URL)
			fetcher := repository.NewArchiveFetcher(&http.Client{Transport: refreshTransport(func(request *http.Request) (*http.Response, error) {
				if private && request.URL.Host == "api.github.com" {
					copy := request.Clone(request.Context())
					copy.URL = request.URL.Clone()
					copy.URL.Scheme, copy.URL.Host = apiURL.Scheme, apiURL.Host
					return api.Client().Transport.RoundTrip(copy)
				}
				expectedPath := "/org/source/tar.gz/HEAD"
				if private {
					expectedPath = "/org/source/tar.gz/private-capability"
				}
				if request.URL.Host != "codeload.github.com" || request.URL.Path != expectedPath {
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
			if mode == "private-lists" {
				_, listsClient := selectedSourceSync(test, store, true)
				worker, err := scheduler.New(store, cache, fetcher, listsClient, scheduler.Options{MaxCollections: 1})
				if err != nil {
					test.Fatal(err)
				}
				defer worker.Close()
				for _, status := range []int64{401, 403, 404, 429} {
					denied.Store(status)
					if err := worker.RunOnce(test.Context()); err == nil {
						test.Fatal("private Lists first-collection API rejection ignored")
					}
					if entries, err := store.ListCollectedRepositories(test.Context(), "alice"); err != nil || len(entries) != 0 || fetches.Load() != 0 {
						test.Fatal("failed private Lists first collection persisted or fell back")
					}
				}
				denied.Store(0)
				if err := worker.RunOnce(test.Context()); err != nil {
					test.Fatal(err)
				}
				entries, err := store.ListCollectedRepositories(test.Context(), "alice")
				if err != nil || len(entries) != 1 || entries[0].Manual {
					test.Fatalf("private Lists first collection missing: %v", err)
				}
				collected = entries[0]
			} else if mode == "star" {
				snapshot, err := fetcher.Fetch(test.Context(), repository.Reference{NameWithOwner: "org/source"})
				if err != nil {
					test.Fatal(err)
				}
				if err := cache.Put(test.Context(), snapshot.Hash, snapshot.Data); err != nil {
					test.Fatal(err)
				}
				collected = selectedStarSource(test, store, snapshot)
			} else {
				if private {
					token, _ := github.NewToken("synthetic-worker-star-credential", nil)
					if _, err := store.PutCredential(test.Context(), "alice", "viewer", "", token); err != nil {
						test.Fatal(err)
					}
					for _, status := range []int64{401, 403, 404, 429} {
						denied.Store(status)
						if _, err := collection.Collect(test.Context(), "alice", "org/source", ""); err == nil {
							test.Fatal("private manual first-collection API rejection ignored")
						}
						if entries, err := store.ListCollectedRepositories(test.Context(), "alice"); err != nil || len(entries) != 0 || fetches.Load() != 0 {
							test.Fatal("failed private manual first collection persisted or fell back")
						}
					}
					denied.Store(0)
				}
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
			endpointURL, _ := url.Parse(endpoint.URL)
			var originals []state.Pack
			var manifestRequests atomic.Int64
			var oldResponseObserved, newResponseObserved atomic.Bool
			var firstManifestPack string
			clientHTTP := &http.Client{Transport: refreshTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != endpointURL.Host || strings.Contains(request.URL.RawQuery, "signature=") || request.Header.Get("Authorization") == "Bearer synthetic-worker-star-credential" {
					return nil, errors.New("subscription client escaped Cognate boundary")
				}
				response, err := endpoint.Client().Transport.RoundTrip(request)
				if err != nil || !publicationArmed.Load() || !strings.HasSuffix(request.URL.Path, "/GetManifest") {
					return response, err
				}
				ordinal := manifestRequests.Add(1)
				if ordinal > 2 {
					return response, nil
				}
				manifest, err := readWatchManifest(response)
				if err != nil {
					return nil, err
				}
				var original *state.Pack
				for index := range originals {
					if originals[index].ID == manifest.PackId {
						original = &originals[index]
						break
					}
				}
				if original == nil || len(manifest.Skills) != 1 || manifest.Skills[0].Slug != "demo" {
					return nil, errors.New("watch fixture manifest selection invalid")
				}
				if ordinal == 1 {
					if manifest.Revision != original.Revision || manifest.Skills[0].ContentHash != collected.Skills[0].ContentHash {
						return nil, errors.New("watch fixture first manifest was not old")
					}
					firstManifestPack = manifest.PackId
					oldResponseObserved.Store(true)
					releasePublication()
					for {
						if err := request.Context().Err(); err != nil {
							return nil, err
						}
						current, err := peer.CollectedRepository(request.Context(), "alice", collected.ID)
						if err != nil {
							return nil, err
						}
						if current.PublicationRevision == collected.PublicationRevision+1 {
							break
						}
						runtime.Gosched()
					}
				} else {
					current, err := peer.CollectedRepository(request.Context(), "alice", collected.ID)
					if err != nil || manifest.PackId == firstManifestPack || manifest.Revision != original.Revision+1 || current.PublicationRevision != collected.PublicationRevision+1 || len(current.Skills) != 1 || manifest.Skills[0].ContentHash != current.Skills[0].ContentHash || manifest.Skills[0].ContentHash == collected.Skills[0].ContentHash {
						return nil, errors.New("watch fixture second manifest was not committed new content")
					}
					newResponseObserved.Store(true)
				}
				return response, nil
			})}
			home := test.TempDir()
			for _, relative := range []string{".agents/skills", ".claude/skills"} {
				if err := os.MkdirAll(filepath.Join(home, relative), 0o700); err != nil {
					test.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(directory, "client"), 0o700); err != nil {
				test.Fatal(err)
			}
			manager, err := subscription.Open(test.Context(), home, filepath.Join(directory, "client"), key, clientHTTP)
			if err != nil {
				test.Fatal(err)
			}
			defer manager.Close()
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
				client, err := packclient.New(endpoint.URL, share.Token.Reveal(), clientHTTP)
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
			canonical := filepath.Join(home, ".agents", "skills", "demo")
			type watchCycle struct {
				report        subscription.WatchReport
				err           error
				unchanged     bool
				manifestCount int64
				oldResponse   bool
				newResponse   bool
			}
			reports := make(chan watchCycle, 16)
			finished := make(chan error, 1)
			go func() {
				var cycleErr error
				var unchanged bool
				finished <- subscription.Watch(ctx, subscription.WatchOptions{Interval: time.Second, AttemptTimeout: 3 * time.Second}, func(attempt context.Context) (subscription.SyncResult, error) {
					unchanged = false
					before, err := manager.List(attempt)
					if err != nil {
						cycleErr = err
						return subscription.SyncResult{}, err
					}
					beforeInfo, err := os.Stat(canonical)
					if err != nil {
						cycleErr = err
						return subscription.SyncResult{}, err
					}
					result, err := manager.Sync(attempt)
					cycleErr = err
					if errors.Is(err, clientstate.ErrSubscriptionConflict) {
						after, afterErr := manager.List(attempt)
						afterInfo, statErr := os.Stat(canonical)
						actual, hashErr := skill.ContentHash(afero.NewOsFs(), canonical)
						unchanged = afterErr == nil && statErr == nil && hashErr == nil && attempt.Err() == nil && ctx.Err() == nil && len(before) == 2 && reflect.DeepEqual(before, after) && os.SameFile(beforeInfo, afterInfo) && beforeInfo.ModTime() == afterInfo.ModTime() && actual == collected.Skills[0].ContentHash && downloads.Load() == 1
					}
					return result, err
				}, func(ctx context.Context, report subscription.WatchReport) error {
					cycle := watchCycle{
						report:        report,
						err:           cycleErr,
						unchanged:     unchanged,
						manifestCount: manifestRequests.Load(),
						oldResponse:   oldResponseObserved.Load(),
						newResponse:   newResponseObserved.Load(),
					}
					select {
					case reports <- cycle:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			stopWatch := sync.OnceFunc(func() {
				cancel()
				releasePublication()
				if err := <-finished; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					test.Error("watch failed to stop")
				}
			})
			defer stopWatch()
			select {
			case cycle := <-reports:
				if cycle.err != nil || !cycle.report.Succeeded || cycle.report.Result.Reused != 1 || cycle.report.ConsecutiveFailures != 0 {
					test.Fatal("initial watch did not reuse installation")
				}
			case <-ctx.Done():
				test.Fatal("watch did not start")
			}
			publicationArmed.Store(true)
			source.Store(publicSourceArchive(test, "second"))
			worker, err := sourcerefresh.New(store, collection, sourcerefresh.Options{Interval: time.Second, DataDirectory: directory})
			if err != nil {
				test.Fatal(err)
			}
			defer func() {
				cancel()
				releasePublication()
				if err := worker.Close(); err != nil {
					test.Error("source worker failed to stop")
				}
			}()
			if err := worker.Start(ctx); err != nil {
				test.Fatal(err)
			}
			for cycleIndex := range 2 {
				select {
				case cycle := <-reports:
					report := cycle.report
					if cycleIndex == 0 {
						_, connectError := errors.AsType[*connect.Error](cycle.err)
						if !errors.Is(cycle.err, clientstate.ErrSubscriptionConflict) || connectError || errors.Is(cycle.err, context.Canceled) || errors.Is(cycle.err, context.DeadlineExceeded) || ctx.Err() != nil || report.Succeeded || !cycle.unchanged || !cycle.oldResponse || !cycle.newResponse || cycle.manifestCount != 2 || !reflect.DeepEqual(report.Result, subscription.SyncResult{}) || report.ConsecutiveFailures != 1 || report.NextAttemptIn != time.Second {
							test.Fatalf("unexpected publication-conflict cycle: type=%T code=%s canceled=%t deadline=%t unchanged=%t", cycle.err, connect.CodeOf(cycle.err), errors.Is(cycle.err, context.Canceled), errors.Is(cycle.err, context.DeadlineExceeded), cycle.unchanged)
						}
						if err := worker.Close(); err != nil {
							test.Fatal("source worker failed to drain after publication")
						}
						test.Log("publication conflict verified: old_then_committed_new=true local_and_subscriptions_unchanged=true downloads_initial=true failure_result_zero=true failures_one=true next_one_second=true")
					} else if cycle.err != nil || !report.Succeeded || report.Result.Updated != 1 || report.ConsecutiveFailures != 0 || report.NextAttemptIn != time.Second {
						test.Fatalf("automatic watch did not naturally recover: type=%T code=%s canceled=%t deadline=%t", cycle.err, connect.CodeOf(cycle.err), errors.Is(cycle.err, context.Canceled), errors.Is(cycle.err, context.DeadlineExceeded))
					} else {
						test.Log("same Watch next cycle recovered: updated_one=true failures_zero=true next_one_second=true")
					}
				case <-ctx.Done():
					test.Fatal("periodic source refresh did not reach automatic Watch")
				}
			}
			stopWatch()
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
			actual, err := skill.ContentHash(afero.NewOsFs(), canonical)
			if err != nil || actual != current.Skills[0].ContentHash || downloads.Load() != 2 || fetches.Load() < 2 {
				test.Fatal("automatic installation hash or single-download invariant failed")
			}
			if private && (metadataCalls.Load() < 2 || privateCalls.Load() != fetches.Load()) {
				test.Fatal("private source bypassed authenticated metadata/archive chain")
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
			if private {
				if result, err := collection.Refresh(test.Context(), "alice", current.ID, state.RepositoryVersion{ArchiveHash: current.ArchiveHash, PublicationRevision: current.PublicationRevision}); err != nil || result.Changed {
					test.Fatalf("private manual Refresh no-op failed: %v", err)
				}
				if target, err := os.Readlink(filepath.Join(home, ".claude", "skills", "demo")); err != nil || target != canonical {
					test.Fatal("client projection did not target canonical skill")
				}
				source.Store(publicSourceArchive(test, "third-rejected"))
				unchanged := func() {
					actual, err := peer.CollectedRepository(test.Context(), "alice", current.ID)
					if err != nil || actual.ArchiveHash != current.ArchiveHash || actual.PublicationRevision != current.PublicationRevision {
						test.Fatal("denied update changed source")
					}
					for index, original := range originals {
						manifest, err := clients[index].Manifest(test.Context())
						if err != nil || manifest.Revision != original.Revision+1 {
							test.Fatal("denied update changed Pack")
						}
					}
					if hash, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || hash != current.Skills[0].ContentHash || downloads.Load() != 2 {
						test.Fatal("denied update changed installation")
					}
				}
				for _, status := range []int64{401, 403, 404, 429} {
					denied.Store(status)
					ticket, err := store.BeginRepositorySourceRefresh(test.Context(), "alice", current.ID)
					if err != nil {
						test.Fatal(err)
					}
					before := fetches.Load()
					if _, err := collection.RefreshAutomatic(test.Context(), ticket); err == nil || strings.Contains(err.Error(), "synthetic-secret-body") {
						test.Fatal("API rejection succeeded or leaked body")
					}
					if fetches.Load() != before {
						test.Fatal("API rejection fell back to codeload")
					}
					if _, err := collection.Collect(test.Context(), "alice", "org/source", ""); err == nil {
						test.Fatal("cached duplicate bypassed authenticated metadata")
					}
					unchanged()
				}
				denied.Store(0)
				for _, stage := range []string{"metadata", "redirect", "body"} {
					rotateAt.Store(stage)
					ticket, err := store.BeginRepositorySourceRefresh(test.Context(), "alice", current.ID)
					if err != nil {
						test.Fatal(err)
					}
					before := fetches.Load()
					beforeRotations := rotations.Load()
					if _, err := collection.RefreshAutomatic(test.Context(), ticket); err == nil {
						test.Fatalf("credential rotation after %s committed", stage)
					}
					if stage != "body" && fetches.Load() != before {
						test.Fatal("stale credential reached codeload")
					}
					if rotations.Load() != beforeRotations+1 {
						test.Fatal("negative control did not rotate credentials")
					}
					unchanged()
					if mode == "private-lists" {
						selectedSourceSync(test, store, true)
					}
				}
				rotateAt.Store("")
			}
		})
	}
}
