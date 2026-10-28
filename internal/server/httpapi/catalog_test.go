package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

func publicSourceArchive(test *testing.T, binary []byte) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		name string
		data []byte
		mode int64
	}{
		{"repo-main/skills/example/SKILL.md", []byte("---\nname: Example\ndescription: Useful public skill\n---\nInstructions"), 0o644},
		{"repo-main/skills/example/.hidden/blob.bin", binary, 0o644},
		{"repo-main/skills/example/run.sh", []byte("#!/bin/sh\necho hello\n"), 0o755},
		{"repo-main/secret.txt", []byte("not part of the skill"), 0o600},
	} {
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data))}); err != nil {
			test.Fatal(err)
		}
		if _, err := archive.Write(entry.data); err != nil {
			test.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func TestCatalogAnonymousVerifiedFullArchiveAndOwnerRPCs(test *testing.T) {
	authentication, store := testServiceAndStore(test)
	password := []byte(rand.Text())
	owner, err := authentication.Register(test.Context(), "alice", password)
	if err != nil {
		test.Fatal(err)
	}
	session, err := authentication.Login(test.Context(), "alice", password)
	if err != nil {
		test.Fatal(err)
	}
	binary := make([]byte, 180000)
	if _, err := rand.Read(binary); err != nil {
		test.Fatal(err)
	}
	archive := publicSourceArchive(test, binary)
	var upstreamCalls atomic.Int64
	upstream := &http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "archives"))
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
	service, err := publiccatalog.NewService(store, collection)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	server := httptest.NewServer(NewWithOptions(authentication, Options{Store: store, PublicCatalog: service}))
	test.Cleanup(server.Close)
	guest := catalogv1connect.NewPublicCatalogServiceClient(server.Client(), server.URL)
	categories := catalogv1connect.NewCategoryServiceClient(server.Client(), server.URL)
	publication := catalogv1connect.NewPublicationServiceClient(server.Client(), server.URL)
	if _, err := categories.ListCategories(test.Context(), connect.NewRequest(&catalogv1.ListCategoriesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("anonymous management: %v", err)
	}
	status := connect.NewRequest(&catalogv1.GetPublicationRequest{RepositoryId: collected.ID})
	status.Header().Set("Authorization", "Bearer "+session.Reveal())
	initial, err := publication.GetPublication(test.Context(), status)
	if err != nil || initial.Msg.Publication.Published || initial.Msg.Publication.Revision != 1 {
		test.Fatalf("default: %+v %v", initial, err)
	}
	before, err := guest.ListSkills(test.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{}))
	if err != nil || len(before.Msg.Skills) != 0 {
		test.Fatalf("private metadata exposed: %+v %v", before, err)
	}
	for _, id := range []string{collected.Skills[0].ID, "missing"} {
		if _, err := guest.GetSkill(test.Context(), connect.NewRequest(&catalogv1.GetSkillRequest{SkillId: id})); connect.CodeOf(err) != connect.CodeNotFound || err.Error() != "not_found: public skill not found" {
			test.Fatalf("hidden/missing distinguishable: %v", err)
		}
	}
	create := connect.NewRequest(&catalogv1.CreateCategoryRequest{Name: "Development"})
	create.Header().Set("Authorization", "Bearer "+session.Reveal())
	created, err := categories.CreateCategory(test.Context(), create)
	if err != nil {
		test.Fatal(err)
	}
	set := connect.NewRequest(&catalogv1.SetSkillCategoriesRequest{SkillId: collected.Skills[0].ID, CategoryIds: []string{created.Msg.Category.Id}})
	set.Header().Set("Authorization", "Bearer "+session.Reveal())
	if _, err := categories.SetSkillCategories(test.Context(), set); err != nil {
		test.Fatal(err)
	}
	change := connect.NewRequest(&catalogv1.SetPublicationRequest{RepositoryId: collected.ID, ExpectedRevision: 1, Published: true})
	change.Header().Set("Authorization", "Bearer "+session.Reveal())
	if _, err := publication.SetPublication(test.Context(), change); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		test.Fatalf("publish without confirmation: %v", err)
	}
	change.Msg.ConfirmFullContents = true
	published, err := publication.SetPublication(test.Context(), change)
	if err != nil || published.Msg.Publication.Revision != 2 {
		test.Fatalf("publish: %+v %v", published, err)
	}
	visible, err := guest.ListSkills(test.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{Query: "Useful", CategoryId: created.Msg.Category.Id}))
	if err != nil || len(visible.Msg.Skills) != 1 || len(visible.Msg.Skills[0].Categories) != 1 || visible.Msg.Skills[0].Categories[0].SkillCount != 1 {
		test.Fatalf("public filtering/categories: %+v %v", visible, err)
	}
	selected := visible.Msg.Skills[0]
	var first []byte
	for range 2 {
		stream, err := guest.DownloadSkill(test.Context(), connect.NewRequest(&catalogv1.DownloadSkillRequest{SkillId: selected.Id, ContentHash: selected.ContentHash, PublicationRevision: selected.PublicationRevision}))
		if err != nil {
			test.Fatal(err)
		}
		var received bytes.Buffer
		finals := 0
		for stream.Receive() {
			chunk := stream.Msg()
			if chunk.Offset != int64(received.Len()) || len(chunk.Data) > 64<<10 {
				test.Fatalf("bad offset/size: %+v", chunk)
			}
			if chunk.Final {
				finals++
				digest := sha256.Sum256(received.Bytes())
				if chunk.TransferSha256 != hex.EncodeToString(digest[:]) || chunk.ContentHash != selected.ContentHash || len(chunk.Data) != 0 {
					test.Fatalf("bad final: %+v", chunk)
				}
			}
			_, _ = received.Write(chunk.Data)
		}
		if err := stream.Err(); err != nil || finals != 1 {
			test.Fatalf("stream: %v finals=%d", err, finals)
		}
		_ = stream.Close()
		if first == nil {
			first = bytes.Clone(received.Bytes())
		} else if !bytes.Equal(first, received.Bytes()) {
			test.Fatal("source archive is not deterministic")
		}
	}
	compressed, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		test.Fatal(err)
	}
	defer compressed.Close()
	files := make(map[string][]byte)
	filesystem := afero.NewMemMapFs()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			test.Fatal(err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		files[header.Name], err = io.ReadAll(reader)
		if err != nil {
			test.Fatal(err)
		}
		if header.Name == "skill/run.sh" && header.Mode&0o111 == 0 {
			test.Fatal("executable mode lost")
		}
		target := path.Join("/", header.Name)
		if err := filesystem.MkdirAll(path.Dir(target), 0o755); err != nil {
			test.Fatal(err)
		}
		if err := afero.WriteFile(filesystem, target, files[header.Name], header.FileInfo().Mode()); err != nil {
			test.Fatal(err)
		}
	}
	if len(files) != 3 || !bytes.Equal(files["skill/.hidden/blob.bin"], binary) || len(files["skill/SKILL.md"]) == 0 || len(files["skill/run.sh"]) == 0 || upstreamCalls.Load() != 1 {
		test.Fatalf("incomplete/unsafe archive: files=%v calls=%d", len(files), upstreamCalls.Load())
	}
	actualHash, err := skill.ContentHashContext(test.Context(), filesystem, "/skill")
	if err != nil || actualHash != selected.ContentHash {
		test.Fatalf("logical content hash: %q %v", actualHash, err)
	}
	gate := &catalogGateSource{source: collection, reached: make(chan struct{}), release: make(chan struct{})}
	resume := sync.OnceFunc(func() { close(gate.release) })
	defer resume()
	gated, err := publiccatalog.NewService(store, gate)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = gated.Close() })
	gatedServer := httptest.NewServer(NewWithOptions(authentication, Options{Store: store, PublicCatalog: gated}))
	test.Cleanup(gatedServer.Close)
	gatedGuest := catalogv1connect.NewPublicCatalogServiceClient(gatedServer.Client(), gatedServer.URL)
	requestContext, cancel := context.WithCancel(test.Context())
	defer cancel()
	interrupted, err := gatedGuest.DownloadSkill(requestContext, connect.NewRequest(&catalogv1.DownloadSkillRequest{SkillId: selected.Id, ContentHash: selected.ContentHash, PublicationRevision: selected.PublicationRevision}))
	if err != nil {
		test.Fatal(err)
	}
	defer interrupted.Close()
	if !interrupted.Receive() || interrupted.Msg().Final || len(interrupted.Msg().Data) != packs.ChunkBytes {
		test.Fatalf("gated first chunk: %v", interrupted.Err())
	}
	<-gate.reached
	change.Msg.Published, change.Msg.ExpectedRevision = false, 2
	if _, err := publication.SetPublication(test.Context(), change); err != nil {
		test.Fatal(err)
	}
	resume()
	if interrupted.Receive() || connect.CodeOf(interrupted.Err()) != connect.CodeNotFound {
		test.Fatalf("revoked archive emitted another chunk/final: %v", interrupted.Err())
	}
	stream, err := guest.DownloadSkill(test.Context(), connect.NewRequest(&catalogv1.DownloadSkillRequest{SkillId: selected.Id, ContentHash: selected.ContentHash, PublicationRevision: 2}))
	if err != nil {
		test.Fatal(err)
	}
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeNotFound {
		test.Fatalf("unpublished archive still downloadable: %v", stream.Err())
	}
	_ = stream.Close()
}

type catalogGateSource struct {
	source  packs.ArchiveSource
	reached chan struct{}
	release chan struct{}
}

func (source *catalogGateSource) WriteSkillArchive(ctx context.Context, archiveHash, path, contentHash string, output io.Writer) error {
	return source.source.WriteSkillArchive(ctx, archiveHash, path, contentHash, &catalogGateWriter{ctx: ctx, output: output, gate: source})
}

type catalogGateWriter struct {
	ctx    context.Context
	output io.Writer
	gate   *catalogGateSource
	total  int
	paused bool
}

func (writer *catalogGateWriter) Write(data []byte) (int, error) {
	written, err := writer.output.Write(data)
	writer.total += written
	if err == nil && !writer.paused && writer.total >= packs.ChunkBytes {
		writer.paused = true
		close(writer.gate.reached)
		select {
		case <-writer.gate.release:
		case <-writer.ctx.Done():
			return written, writer.ctx.Err()
		}
	}
	return written, err
}
