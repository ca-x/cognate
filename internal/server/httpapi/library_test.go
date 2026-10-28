package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	libraryv1 "github.com/ca-x/skillhub/gen/skillhub/library/v1"
	"github.com/ca-x/skillhub/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/library"
)

func libraryArchive(test *testing.T, document string) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "repo-main/skills/example/SKILL.md", Mode: 0o644, Size: int64(len(document))}); err != nil {
		test.Fatal(err)
	}
	if _, err := io.WriteString(archive, document); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func TestLibraryRPCCollectionAndUserIsolation(test *testing.T) {
	authentication, store := testServiceAndStore(test)
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "objects"))
	if err != nil {
		test.Fatal(err)
	}
	var calls atomic.Int64
	archive := libraryArchive(test, "---\nname: Example\ndescription: A useful skill\n---\nInstructions")
	upstream := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if strings.Contains(request.URL.Path, "/missing/") {
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	service, err := library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	server := httptest.NewServer(NewWithOptions(authentication, Options{Store: store, Library: service}))
	test.Cleanup(server.Close)
	client := libraryv1connect.NewLibraryServiceClient(server.Client(), server.URL)
	if _, err := client.CollectRepository(test.Context(), connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: "org/repo"})); connect.CodeOf(err) != connect.CodeUnauthenticated || calls.Load() != 0 {
		test.Fatalf("unauthenticated collection: %v", err)
	}
	sessions := make([]string, 0, 2)
	for _, username := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		if _, err := authentication.Register(test.Context(), username, password); err != nil {
			test.Fatal(err)
		}
		token, err := authentication.Login(test.Context(), username, password)
		if err != nil {
			test.Fatal(err)
		}
		sessions = append(sessions, token.Reveal())
	}
	for _, input := range []string{"org/repo", "https://github.com/ORG/REPO.git"} {
		request := connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: input, Branch: "HEAD"})
		request.Header().Set("Authorization", "Bearer "+sessions[0])
		response, err := client.CollectRepository(test.Context(), request)
		if err != nil || response.Msg.Repository.NameWithOwner != "org/repo" || response.Msg.Repository.SkillCount != 1 || !response.Msg.Repository.Manual {
			test.Fatalf("collect: %v", err)
		}
	}
	if calls.Load() != 1 {
		test.Fatalf("duplicate collection refetched archive: %d", calls.Load())
	}
	listRequest := connect.NewRequest(&libraryv1.ListRepositoriesRequest{})
	listRequest.Header().Set("Authorization", "Bearer "+sessions[0])
	listed, err := client.ListRepositories(test.Context(), listRequest)
	if err != nil || len(listed.Msg.Repositories) != 1 {
		test.Fatalf("list: %v", err)
	}
	id := listed.Msg.Repositories[0].Id
	getRequest := connect.NewRequest(&libraryv1.GetRepositoryRequest{RepositoryId: id})
	getRequest.Header().Set("Authorization", "Bearer "+sessions[0])
	detail, err := client.GetRepository(test.Context(), getRequest)
	if err != nil || len(detail.Msg.Skills) != 1 || detail.Msg.Skills[0].Name != "Example" || len(detail.Msg.Skills[0].ContentHash) != 64 {
		test.Fatalf("detail: %v", err)
	}
	getRequest.Header().Set("Authorization", "Bearer "+sessions[1])
	if _, err := client.GetRepository(test.Context(), getRequest); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("cross-account detail: %v", err)
	}
	listRequest.Header().Set("Authorization", "Bearer "+sessions[1])
	other, err := client.ListRepositories(test.Context(), listRequest)
	if err != nil || len(other.Msg.Repositories) != 0 {
		test.Fatalf("cross-account list: %v", err)
	}
	for _, input := range []string{"../escape", "org/missing"} {
		request := connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: input})
		request.Header().Set("Authorization", "Bearer "+sessions[1])
		if _, err := client.CollectRepository(test.Context(), request); err == nil {
			test.Fatalf("invalid repository accepted: %s", input)
		}
	}
	other, err = client.ListRepositories(test.Context(), listRequest)
	if err != nil || len(other.Msg.Repositories) != 0 {
		test.Fatalf("failed fetch inserted repository: %v", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := client.ListRepositories(ctx, listRequest); connect.CodeOf(err) != connect.CodeCanceled {
		test.Fatalf("canceled request: %v", err)
	}
}
