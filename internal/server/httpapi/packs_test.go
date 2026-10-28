package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/skillhub/gen/skillhub/pack/v1"
	"github.com/ca-x/skillhub/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/library"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
)

func TestPackRPCManagementAndDistribution(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := make([]byte, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "state.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	var owner, session string
	for _, name := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		user, err := authentication.Register(test.Context(), name, password)
		if err != nil {
			test.Fatal(err)
		}
		token, err := authentication.Login(test.Context(), name, password)
		if err != nil {
			test.Fatal(err)
		}
		if name == "alice" {
			owner, session = user, token.Reveal()
		}
	}
	archive := libraryArchive(test, "---\nname: Example\ndescription: Useful skill\n---\nInstructions")
	upstream := &http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		test.Fatal(err)
	}
	collection, err := library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = collection.Close() })
	collected, err := collection.Collect(test.Context(), owner, "org/repo", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err = collection.Get(test.Context(), owner, collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	distribution, err := packs.NewService(store, collection, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(distribution.Close)
	server := httptest.NewServer(NewWithOptions(authentication, Options{Store: store, Library: collection, Packs: distribution}))
	test.Cleanup(server.Close)
	client := packv1connect.NewPackServiceClient(server.Client(), server.URL)
	request := connect.NewRequest(&packv1.CreatePackRequest{Draft: &packv1.PackDraft{Name: "My pack", Description: strings.Repeat("d", 2048), Items: []*packv1.PackSelection{{SkillId: collected.Skills[0].ID}}}})
	if _, err := client.CreatePack(test.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("unauthenticated create: %v", err)
	}
	request.Header().Set("Authorization", "Bearer "+session)
	created, err := client.CreatePack(test.Context(), request)
	if err != nil || created.Msg.Pack.Revision != 1 {
		test.Fatalf("create: %v", err)
	}
	shareRequest := connect.NewRequest(&packv1.CreateShareRequest{PackId: created.Msg.Pack.Id})
	shareRequest.Header().Set("Authorization", "Bearer "+session)
	shared, err := client.CreateShare(test.Context(), shareRequest)
	if err != nil || shared.Msg.ShareToken == "" {
		test.Fatalf("share: %v", err)
	}
	reader := packv1connect.NewDistributionServiceClient(server.Client(), server.URL)
	manifestRequest := connect.NewRequest(&packv1.GetManifestRequest{})
	manifestRequest.Header().Set("Authorization", "Bearer "+session)
	if _, err := reader.GetManifest(test.Context(), manifestRequest); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("session accepted as share: %v", err)
	}
	manifestRequest.Header().Set("Authorization", "Bearer "+shared.Msg.ShareToken)
	manifest, err := reader.GetManifest(test.Context(), manifestRequest)
	if err != nil || len(manifest.Msg.Skills) != 1 || manifest.Msg.FormatVersion != 1 {
		test.Fatalf("manifest: %v", err)
	}
	download := connect.NewRequest(&packv1.DownloadSkillRequest{SkillId: collected.Skills[0].ID, ContentHash: collected.Skills[0].ContentHash, PackRevision: 1})
	download.Header().Set("Authorization", "Bearer "+shared.Msg.ShareToken)
	stream, err := reader.DownloadSkill(test.Context(), download)
	if err != nil {
		test.Fatal(err)
	}
	defer stream.Close()
	var output bytes.Buffer
	var final *packv1.DownloadSkillResponse
	for stream.Receive() {
		message := stream.Msg()
		if message.Offset != int64(output.Len()) {
			test.Fatal("wrong transfer offset")
		}
		_, _ = output.Write(message.Data)
		final = message
	}
	digest := sha256.Sum256(output.Bytes())
	if err := stream.Err(); err != nil || final == nil || !final.Final || final.TransferSha256 != hex.EncodeToString(digest[:]) || final.ContentHash != collected.Skills[0].ContentHash {
		test.Fatalf("RPC stream integrity: %v", err)
	}
	revoke := connect.NewRequest(&packv1.RevokeShareRequest{PackId: created.Msg.Pack.Id, ShareId: shared.Msg.Share.Id})
	revoke.Header().Set("Authorization", "Bearer "+session)
	if _, err := client.RevokeShare(test.Context(), revoke); err != nil {
		test.Fatal(err)
	}
	if _, err := reader.GetManifest(test.Context(), manifestRequest); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("revoked share still works: %v", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := client.ListPacks(ctx, connect.NewRequest(&packv1.ListPacksRequest{})); err == nil {
		test.Fatal("canceled call succeeded")
	}
}
