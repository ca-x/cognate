package httpapi

import (
	"bytes"
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
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestLibraryRefreshRPCVersionIsolationAndPublication(test *testing.T) {
	authentication, store := testServiceAndStore(test)
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "objects"))
	if err != nil {
		test.Fatal(err)
	}
	var source atomic.Value
	source.Store(libraryArchive(test, "---\nname: Example\ndescription: A useful skill\n---\nFirst instructions"))
	var calls atomic.Int64
	var fail atomic.Bool
	upstream := &http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("synthetic-private-upstream-material")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(source.Load().([]byte)))}, nil
	})}
	service, err := library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	server := httptest.NewServer(NewWithOptions(authentication, Options{Store: store, Library: service}))
	test.Cleanup(server.Close)
	client := libraryv1connect.NewLibraryServiceClient(server.Client(), server.URL)
	sessions := make([]string, 2)
	owners := make([]string, 2)
	for index, username := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		owner, err := authentication.Register(test.Context(), username, password)
		if err != nil {
			test.Fatal("could not register synthetic account")
		}
		owners[index] = owner
		token, err := authentication.Login(test.Context(), username, password)
		if err != nil {
			test.Fatal("could not log in synthetic account")
		}
		sessions[index] = token.Reveal()
	}
	collect := connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: "org/repo"})
	collect.Header().Set("Authorization", "Bearer "+sessions[0])
	collected, err := client.CollectRepository(test.Context(), collect)
	if err != nil || collected.Msg.Repository.PublicationRevision != 1 {
		test.Fatal("collection did not expose its coherent publication revision")
	}
	id := collected.Msg.Repository.Id
	before, err := store.CollectedRepository(test.Context(), owners[0], id)
	if err != nil || len(before.Skills) != 1 {
		test.Fatal("synthetic repository missing")
	}
	var packs []state.Pack
	for _, name := range []string{"First", "Second"} {
		pack, err := store.CreatePack(test.Context(), owners[0], state.PackDraft{Name: name, Items: []state.PackSelection{{SkillID: before.Skills[0].ID, Slug: "example"}}})
		if err != nil {
			test.Fatal("could not create synthetic pack")
		}
		packs = append(packs, pack)
	}
	refresh := func(ctx context.Context, token string, input *libraryv1.RefreshRepositoryRequest) (*connect.Response[libraryv1.RefreshRepositoryResponse], error) {
		request := connect.NewRequest(input)
		if token != "" {
			request.Header().Set("Authorization", "Bearer "+token)
		}
		return client.RefreshRepository(ctx, request)
	}
	input := func() *libraryv1.RefreshRepositoryRequest {
		return &libraryv1.RefreshRepositoryRequest{RepositoryId: id, ExpectedArchiveHash: before.ArchiveHash, ExpectedPublicationRevision: before.PublicationRevision}
	}
	for _, attempt := range []struct {
		token string
		code  connect.Code
	}{{"", connect.CodeUnauthenticated}, {sessions[1], connect.CodeNotFound}} {
		if _, err := refresh(test.Context(), attempt.token, input()); connect.CodeOf(err) != attempt.code {
			test.Fatal("refresh authentication or owner isolation failed")
		}
	}
	for _, invalid := range []*libraryv1.RefreshRepositoryRequest{
		{RepositoryId: id, ExpectedArchiveHash: strings.Repeat("A", 64), ExpectedPublicationRevision: 1},
		{RepositoryId: id, ExpectedArchiveHash: before.ArchiveHash},
		{ExpectedArchiveHash: before.ArchiveHash, ExpectedPublicationRevision: 1},
	} {
		if _, err := refresh(test.Context(), sessions[0], invalid); connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("malformed refresh was not rejected")
		}
	}
	stale := input()
	stale.ExpectedPublicationRevision++
	if _, err := refresh(test.Context(), sessions[0], stale); connect.CodeOf(err) != connect.CodeAborted || calls.Load() != 1 {
		test.Fatal("invalid or stale authorization fetched upstream content")
	}
	source.Store(libraryArchive(test, "---\nname: Example\ndescription: A useful skill\n---\nSecond instructions"))
	updated, err := refresh(test.Context(), sessions[0], input())
	if err != nil || !updated.Msg.Changed || updated.Msg.Updated != 1 || updated.Msg.Added != 0 || updated.Msg.Removed != 0 || len(updated.Msg.Packs) != 2 || updated.Msg.Repository.PublicationRevision != before.PublicationRevision+1 {
		test.Fatal("refresh did not return the committed content and pack changes")
	}
	expectedPacks := make(map[string]int64, len(packs))
	for _, pack := range packs {
		expectedPacks[pack.ID] = pack.Revision + 1
	}
	for _, pack := range updated.Msg.Packs {
		if expectedPacks[pack.PackId] != pack.Revision || pack.Revision < 1 {
			test.Fatal("refresh response returned incorrect or duplicate pack metadata")
		}
		delete(expectedPacks, pack.PackId)
	}
	if len(expectedPacks) != 0 {
		test.Fatal("refresh response omitted an affected pack")
	}
	after, err := store.CollectedRepository(test.Context(), owners[0], id)
	if err != nil || after.Skills[0].ID != before.Skills[0].ID || after.Skills[0].ContentHash == before.Skills[0].ContentHash {
		test.Fatal("refresh did not preserve skill identity while replacing content")
	}
	for _, pack := range packs {
		actual, err := store.Pack(test.Context(), owners[0], pack.ID)
		if err != nil || actual.Revision != pack.Revision+1 {
			test.Fatal("refresh failed to advance each affected pack exactly once")
		}
	}
	before = after
	get := connect.NewRequest(&libraryv1.GetRepositoryRequest{RepositoryId: id})
	get.Header().Set("Authorization", "Bearer "+sessions[0])
	detail, err := client.GetRepository(test.Context(), get)
	if err != nil || detail.Msg.Repository.PublicationRevision != before.PublicationRevision || detail.Msg.Repository.ArchiveHash != before.ArchiveHash {
		test.Fatal("repository detail omitted the next coherent CAS version")
	}
	unchanged, err := refresh(test.Context(), sessions[0], input())
	if err != nil || unchanged.Msg.Changed || unchanged.Msg.Repository.PublicationRevision != before.PublicationRevision || len(unchanged.Msg.Packs) != 0 || calls.Load() != 3 {
		test.Fatal("no-op refresh changed version or skipped its real upstream check")
	}
	fail.Store(true)
	if _, err := refresh(test.Context(), sessions[0], input()); err == nil || strings.Contains(err.Error(), "synthetic-private-upstream-material") {
		test.Fatal("failed upstream refresh succeeded or exposed private diagnostics")
	}
	fail.Store(false)
	publication, err := store.SetPublication(test.Context(), owners[0], id, before.PublicationRevision, true, true)
	if err != nil {
		test.Fatal("could not publish synthetic repository")
	}
	before.PublicationRevision = publication.Revision
	source.Store(libraryArchive(test, "---\nname: Example\ndescription: A useful skill\n---\nUnconfirmed published changes"))
	if _, err := refresh(test.Context(), sessions[0], input()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		test.Fatal("published contents changed without a separate confirmation")
	}
	preserved, err := store.CollectedRepository(test.Context(), owners[0], id)
	if err != nil || preserved.ArchiveHash != before.ArchiveHash || preserved.Skills[0].ContentHash != before.Skills[0].ContentHash {
		test.Fatal("failed refresh replaced the previous working snapshot")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := refresh(ctx, sessions[0], input()); connect.CodeOf(err) != connect.CodeCanceled {
		test.Fatal("canceled refresh was not canceled")
	}
}
