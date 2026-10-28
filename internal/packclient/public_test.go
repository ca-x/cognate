package packclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"google.golang.org/protobuf/proto"
)

type publicFixture struct {
	catalogv1connect.UnimplementedPublicCatalogServiceHandler
	entry    *catalogv1.PublicSkill
	frames   []*packv1.DownloadSkillResponse
	requests atomic.Int32
	private  atomic.Bool
}

func (fixture *publicFixture) GetSkill(_ context.Context, request *connect.Request[catalogv1.GetSkillRequest]) (*connect.Response[catalogv1.GetSkillResponse], error) {
	fixture.requests.Add(1)
	if request.Header().Get("Authorization") != "" || request.Header().Get("Cookie") != "" || request.Msg.SkillId != fixture.entry.Id {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("anonymous boundary violated"))
	}
	if fixture.private.Load() {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("skill not found"))
	}
	return connect.NewResponse(&catalogv1.GetSkillResponse{Skill: proto.Clone(fixture.entry).(*catalogv1.PublicSkill)}), nil
}

func (fixture *publicFixture) DownloadSkill(_ context.Context, request *connect.Request[catalogv1.DownloadSkillRequest], stream *connect.ServerStream[packv1.DownloadSkillResponse]) error {
	fixture.requests.Add(1)
	if request.Header().Get("Authorization") != "" || request.Header().Get("Cookie") != "" || request.Msg.SkillId != fixture.entry.Id || request.Msg.ContentHash != fixture.entry.ContentHash || request.Msg.PublicationRevision != fixture.entry.PublicationRevision {
		return connect.NewError(connect.CodePermissionDenied, errors.New("anonymous boundary violated"))
	}
	if fixture.private.Load() {
		return connect.NewError(connect.CodeNotFound, errors.New("skill not found"))
	}
	for _, frame := range fixture.frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

func newPublicFixture(test *testing.T) (*publicFixture, *httptest.Server) {
	test.Helper()
	manifest, entries := fixtureManifest(test)
	selected := manifest.Skills[0]
	data := fixtureArchive(test, entries)
	digest := sha256.Sum256(data)
	fixture := &publicFixture{entry: &catalogv1.PublicSkill{Id: selected.SkillId, Slug: selected.Slug, Name: selected.Name, Description: selected.Description, ContentHash: selected.ContentHash, RepositoryId: selected.RepositoryId, RepositoryName: selected.RepositoryName, Path: selected.Path, Branch: "main", PublicationRevision: 3}, frames: []*packv1.DownloadSkillResponse{{Data: data}, {Offset: int64(len(data)), Final: true, TransferSha256: hex.EncodeToString(digest[:]), ContentHash: selected.ContentHash}}}
	_, handler := catalogv1connect.NewPublicCatalogServiceHandler(fixture)
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	return fixture, server
}

func TestPublicClientAnonymousBinarySnapshotAndPrivateRejection(test *testing.T) {
	fixture, server := newPublicFixture(test)
	client, err := NewPublic(server.URL, server.Client())
	if err != nil {
		test.Fatal(err)
	}
	entry, err := client.GetSkill(test.Context(), fixture.entry.Id)
	if err != nil {
		test.Fatal(err)
	}
	snapshot, err := client.Download(test.Context(), entry)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = snapshot.Close() })
	data, err := os.ReadFile(filepath.Join(snapshot.Path(), ".hidden"))
	if err != nil || !bytes.Equal(data, []byte{0, 0xff, 0x80, 10, 13}) {
		test.Fatal("anonymous binary/hidden file was not preserved")
	}
	if err := snapshot.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(snapshot.Path()); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("anonymous snapshot cleanup failed")
	}
	fixture.private.Store(true)
	if _, err := client.GetSkill(test.Context(), fixture.entry.Id); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("private skill lookup did not fail")
	}
	if _, err := client.Download(test.Context(), entry); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("saved public metadata retained revoked download access")
	}
	if _, err := New(server.URL, "", server.Client()); err == nil {
		test.Fatal("anonymous API weakened private Pack credential requirements")
	}
}

func TestPublicClientRejectsUntrustedMetadataAndFrames(test *testing.T) {
	for _, scenario := range []string{"slug", "path", "revision", "category", "offset", "hash", "extra-final"} {
		test.Run(scenario, func(test *testing.T) {
			temporary := test.TempDir()
			test.Setenv("TMPDIR", temporary)
			fixture, server := newPublicFixture(test)
			switch scenario {
			case "slug":
				fixture.entry.Slug = "../escape"
			case "path":
				fixture.entry.Path = "/secret"
			case "revision":
				fixture.entry.PublicationRevision = 0
			case "category":
				fixture.entry.Categories = []*catalogv1.PublicCategory{{Id: "category", Name: "private-only", SkillCount: 0}}
			case "offset":
				fixture.frames[0].Offset = 1
			case "hash":
				fixture.frames[1].TransferSha256 = strings.Repeat("0", 64)
			case "extra-final":
				fixture.frames = append(fixture.frames, proto.Clone(fixture.frames[1]).(*packv1.DownloadSkillResponse))
			}
			client, err := NewPublic(server.URL, server.Client())
			if err != nil {
				test.Fatal(err)
			}
			entry, err := client.GetSkill(test.Context(), fixture.entry.Id)
			if err == nil {
				var snapshot *Snapshot
				snapshot, err = client.Download(test.Context(), entry)
				if snapshot != nil {
					_ = snapshot.Close()
					test.Fatal("untrusted public data produced a snapshot")
				}
			}
			if err == nil {
				test.Fatal("untrusted public metadata/framing accepted")
			}
			if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
				test.Fatal("rejected anonymous data left a temporary snapshot")
			}
		})
	}
}

func TestPublicClientDoesNotFollowRedirects(test *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		writer.WriteHeader(http.StatusOK)
	}))
	test.Cleanup(destination.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	test.Cleanup(origin.Close)
	client, err := NewPublic(origin.URL, origin.Client())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.GetSkill(test.Context(), "public-skill"); err == nil {
		test.Fatal("anonymous redirect was accepted")
	}
	if redirected.Load() {
		test.Fatal("anonymous client followed a redirect away from Cognate")
	}
}

func TestPublicClientCancellationAndOriginBoundary(test *testing.T) {
	for _, origin := range []string{"http://hub.example", "https://hub.example/path", "https://name:password@hub.example", "https://hub.example?token=secret"} {
		if _, err := NewPublic(origin, nil); err == nil {
			test.Fatal("unsafe anonymous server origin accepted")
		}
	}
	fixture, server := newPublicFixture(test)
	client, err := NewPublic(server.URL, server.Client())
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := client.GetSkill(ctx, fixture.entry.Id); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled anonymous lookup was not canceled")
	}
	if fixture.requests.Load() != 0 {
		test.Fatal("canceled request reached the server")
	}
	var observed atomic.Bool
	client, err = NewPublic(server.URL, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		observed.Store(true)
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			test.Error("anonymous client sent credentials")
		}
		return http.DefaultTransport.RoundTrip(request)
	})})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.GetSkill(test.Context(), fixture.entry.Id); err != nil || !observed.Load() {
		test.Fatal("anonymous client did not use the bounded transport")
	}
}
