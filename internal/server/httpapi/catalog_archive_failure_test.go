package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/skillhub/gen/skillhub/catalog/v1"
	"github.com/ca-x/skillhub/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/library"
	"github.com/ca-x/skillhub/internal/server/publiccatalog"
)

func TestCatalogMissingOrMismatchedArchiveFailsClosed(test *testing.T) {
	for _, mode := range []string{"missing-cache", "mismatched-logical-hash"} {
		test.Run(mode, func(test *testing.T) {
			authentication, store := testServiceAndStore(test)
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			archive := publicSourceArchive(test, []byte{0, 1, 0, 255})
			var fetches atomic.Int64
			upstream := &http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) {
				fetches.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
			})}
			fetcher := repository.NewArchiveFetcher(upstream)
			reference, _ := repository.ParseReference("org/fail-closed", "")
			snapshot, err := fetcher.Fetch(test.Context(), reference)
			if err != nil {
				test.Fatal(err)
			}
			cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "archives"))
			if err != nil {
				test.Fatal(err)
			}
			if mode == "mismatched-logical-hash" {
				if err := cache.Put(test.Context(), snapshot.Hash, snapshot.Data); err != nil {
					test.Fatal(err)
				}
				snapshot.Skills[0].ContentHash = strings.Repeat("c", 64)
			}
			collected, err := store.CollectRepository(test.Context(), "alice", reference, snapshot.Hash, snapshot.Skills)
			if err != nil {
				test.Fatal(err)
			}
			collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := store.SetPublication(test.Context(), "alice", collected.ID, 1, true, true); err != nil {
				test.Fatal(err)
			}
			collection, err := library.NewService(store, cache, fetcher)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = collection.Close() })
			service, err := publiccatalog.NewService(store, collection)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = service.Close() })
			server := httptest.NewServer(NewWithOptions(authentication, Options{Store: store, PublicCatalog: service}))
			test.Cleanup(server.Close)
			guest := catalogv1connect.NewPublicCatalogServiceClient(server.Client(), server.URL)
			stream, err := guest.DownloadSkill(test.Context(), connect.NewRequest(&catalogv1.DownloadSkillRequest{SkillId: collected.Skills[0].ID, ContentHash: collected.Skills[0].ContentHash, PublicationRevision: 2}))
			if err != nil {
				test.Fatal(err)
			}
			defer stream.Close()
			if stream.Receive() {
				test.Fatal("unverified or missing source emitted a chunk/final")
			}
			expected := map[string]connect.Code{"missing-cache": connect.CodeDataLoss, "mismatched-logical-hash": connect.CodeFailedPrecondition}[mode]
			if connect.CodeOf(stream.Err()) != expected || fetches.Load() != 1 {
				test.Fatalf("source did not fail closed without refetch: %v fetches=%d", stream.Err(), fetches.Load())
			}
		})
	}
}
