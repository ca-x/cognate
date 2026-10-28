package packclient

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
	"google.golang.org/protobuf/proto"
)

type distributionFixture struct {
	packv1connect.UnimplementedDistributionServiceHandler
	manifest func(context.Context, *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error)
	download func(context.Context, *connect.Request[packv1.DownloadSkillRequest], *connect.ServerStream[packv1.DownloadSkillResponse]) error
}

func (fixture distributionFixture) GetManifest(ctx context.Context, request *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error) {
	return fixture.manifest(ctx, request)
}

func (fixture distributionFixture) DownloadSkill(ctx context.Context, request *connect.Request[packv1.DownloadSkillRequest], stream *connect.ServerStream[packv1.DownloadSkillResponse]) error {
	return fixture.download(ctx, request, stream)
}

type archiveEntry struct {
	name string
	data []byte
	mode int64
	kind byte
	link string
}

func fixtureToken() string {
	payload := make([]byte, 107)
	payload[0] = 1
	copy(payload[1:27], strings.Repeat("A", 26))
	binary.BigEndian.PutUint64(payload[59:67], uint64(time.Now().Unix()))
	return "shp1." + base64.RawURLEncoding.EncodeToString(payload)
}

func fixtureArchive(test *testing.T, entries []archiveEntry) []byte {
	test.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: kind, Linkname: entry.link, Format: tar.FormatPAX}
		if kind == tar.TypeReg {
			header.Size = int64(len(entry.data))
		}
		if err := archive.WriteHeader(header); err != nil {
			test.Fatal(err)
		}
		if _, err := archive.Write(entry.data); err != nil {
			test.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return output.Bytes()
}

func fixtureManifest(test *testing.T) (*packv1.GetManifestResponse, []archiveEntry) {
	test.Helper()
	entries := []archiveEntry{
		{name: "skill/", kind: tar.TypeDir, mode: 0o755},
		{name: "skill/SKILL.md", data: []byte("---\nname: Binary Skill\ndescription: fixture\n---\nbody\n"), mode: 0o644},
		{name: "skill/.hidden", data: []byte{0, 0xff, 0x80, 10, 13}, mode: 0o644},
		{name: "skill/bin/", kind: tar.TypeDir, mode: 0o755},
		{name: "skill/bin/run", data: []byte("#!/bin/sh\nexit 0\n"), mode: 0o751},
		{name: "skill/empty/", kind: tar.TypeDir, mode: 0o755},
	}
	contentHash := fixtureContentHash(test, entries)
	manifest := &packv1.GetManifestResponse{
		FormatVersion: 1, PackId: "pack-1", Name: "Binary pack", Description: "fixture", Revision: 7,
		Skills: []*packv1.PackSkill{{SkillId: "skill-1", Slug: "binary-skill", Name: "Binary Skill", Description: "fixture", ContentHash: contentHash, RepositoryId: "repo-1", RepositoryName: "org/repo", Path: "skills/binary"}},
	}
	sealManifest(manifest)
	return manifest, entries
}

func fixtureContentHash(test *testing.T, entries []archiveEntry) string {
	test.Helper()
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/skill", 0o700); err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries[1:] {
		if err := filesystem.MkdirAll(filepath.Dir("/"+entry.name), 0o700); err != nil {
			test.Fatal(err)
		}
		if entry.kind == tar.TypeDir {
			if err := filesystem.MkdirAll("/"+entry.name, os.FileMode(entry.mode)); err != nil {
				test.Fatal(err)
			}
		} else if err := afero.WriteFile(filesystem, "/"+entry.name, entry.data, os.FileMode(entry.mode)); err != nil {
			test.Fatal(err)
		}
	}
	contentHash, err := skill.ContentHashContext(test.Context(), filesystem, "/skill")
	if err != nil {
		test.Fatal(err)
	}
	return contentHash
}

func sealManifest(manifest *packv1.GetManifestResponse) {
	var input bytes.Buffer
	appendStrings := func(values ...string) {
		for _, value := range values {
			_ = binary.Write(&input, binary.BigEndian, uint64(len(value)))
			input.WriteString(value)
		}
	}
	appendStrings("skillhub/pack-manifest/v1", manifest.PackId, manifest.Name, manifest.Description)
	_ = binary.Write(&input, binary.BigEndian, uint64(manifest.Revision))
	for _, entry := range manifest.Skills {
		appendStrings(entry.SkillId, entry.Slug, entry.Name, entry.Description, entry.ContentHash, entry.RepositoryId, entry.RepositoryName, entry.Path)
	}
	digest := sha256.Sum256(input.Bytes())
	manifest.ManifestHash = hex.EncodeToString(digest[:])
}

func fixtureFrames(data []byte, contentHash string) []*packv1.DownloadSkillResponse {
	frames := make([]*packv1.DownloadSkillResponse, 0, len(data)/(64<<10)+2)
	for offset := 0; offset < len(data); {
		end := min(offset+64<<10, len(data))
		frames = append(frames, &packv1.DownloadSkillResponse{Offset: int64(offset), Data: bytes.Clone(data[offset:end])})
		offset = end
	}
	digest := sha256.Sum256(data)
	return append(frames, &packv1.DownloadSkillResponse{Offset: int64(len(data)), Final: true, TransferSha256: hex.EncodeToString(digest[:]), ContentHash: contentHash})
}

func fixtureClient(test *testing.T, manifest *packv1.GetManifestResponse, frames []*packv1.DownloadSkillResponse, streamError error) *Client {
	test.Helper()
	token := fixtureToken()
	fixture := distributionFixture{
		manifest: func(_ context.Context, request *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error) {
			if request.Header().Get("Authorization") != "Bearer "+token || strings.Contains(request.Peer().Addr, token) {
				test.Error("incorrect credential boundary")
			}
			return connect.NewResponse(proto.Clone(manifest).(*packv1.GetManifestResponse)), nil
		},
		download: func(_ context.Context, request *connect.Request[packv1.DownloadSkillRequest], stream *connect.ServerStream[packv1.DownloadSkillResponse]) error {
			if request.Header().Get("Authorization") != "Bearer "+token || request.Msg.PackRevision != manifest.Revision || request.Msg.SkillId != manifest.Skills[0].SkillId || request.Msg.ContentHash != manifest.Skills[0].ContentHash {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("incorrect download request"))
			}
			for _, frame := range frames {
				if err := stream.Send(frame); err != nil {
					return err
				}
			}
			return streamError
		},
	}
	_, handler := packv1connect.NewDistributionServiceHandler(fixture)
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	client, err := New(server.URL, token, server.Client())
	if err != nil {
		test.Fatal(err)
	}
	return client
}

func TestBinaryRoundTripAndSnapshotCleanup(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	manifest, entries := fixtureManifest(test)
	archive := fixtureArchive(test, entries)
	client := fixtureClient(test, manifest, fixtureFrames(archive, manifest.Skills[0].ContentHash), nil)
	actual, err := client.Manifest(test.Context())
	if err != nil || !proto.Equal(actual, manifest) {
		test.Fatalf("manifest mismatch: %v", err)
	}
	snapshot, err := client.Download(test.Context(), actual, actual.Skills[0])
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = snapshot.Close() })
	if snapshot.Path() == "" || !filepath.IsAbs(snapshot.Path()) || !strings.HasPrefix(snapshot.Path(), temporary+string(filepath.Separator)) {
		test.Fatal("snapshot is not a private temporary path")
	}
	for _, entry := range entries[1:] {
		filename := filepath.Join(snapshot.Path(), strings.TrimPrefix(entry.name, "skill/"))
		info, err := os.Stat(filename)
		if err != nil {
			test.Fatal(err)
		}
		if entry.kind == tar.TypeDir {
			if !info.IsDir() {
				test.Fatal("empty directory was not preserved")
			}
			continue
		}
		data, err := os.ReadFile(filename)
		if err != nil || !bytes.Equal(data, entry.data) || info.Mode().Perm()&0o111 != os.FileMode(entry.mode)&0o111 {
			test.Fatalf("binary content or executable bits changed: %v", err)
		}
	}
	root := snapshot.Path()
	if err := snapshot.Close(); err != nil {
		test.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		test.Fatal("cleanup is not idempotent")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("snapshot survived cleanup")
	}
	contents, err := os.ReadDir(temporary)
	if err != nil || len(contents) != 0 {
		test.Fatalf("temporary download was not cleaned: %v", err)
	}
}

func TestRejectsUnsafeOriginsAndCredentialSyntaxWithoutDisclosure(test *testing.T) {
	token := fixtureToken()
	for _, origin := range []string{"", "https://", "https://example.com/path", "https://example.com/?query=" + token, "https://example.com/#" + token, "https://" + token + "@example.com", "http://example.com", "file:///tmp", "//example.com", "https://example.com:bad", "https://example.com:0", "https://example.com:65536", "https://example.com\\@other.test", "https://example.com\n"} {
		client, err := New(origin, token, nil)
		if err == nil || client != nil {
			test.Fatal("unsafe origin accepted")
		}
		if strings.Contains(fmt.Sprintf("%v %#v %+v", err, err, err), token) {
			test.Fatal("origin error disclosed credentials")
		}
	}
	for _, credential := range []string{"", token + "\n", "Bearer " + token, strings.Replace(token, "shp1.", "shp2.", 1), "shp1." + strings.Repeat("A", 143), token + "=", token[:len(token)-1] + "!"} {
		client, err := New("https://example.com", credential, nil)
		if err == nil || client != nil || credential != "" && strings.Contains(fmt.Sprint(err), credential) {
			test.Fatal("invalid credential accepted or disclosed")
		}
	}
	for _, origin := range []string{"https://example.com", "https://example.com/", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080"} {
		client, err := New(origin, token, nil)
		if err != nil {
			test.Fatalf("safe origin rejected: %v", err)
		}
		for _, formatted := range []string{fmt.Sprintf("%v %+v %#v %s %q %x", client, client, client, client, client, client), fmt.Sprintf("%#v", *client), slog.AnyValue(client).Resolve().String()} {
			if strings.Contains(formatted, token) {
				test.Fatal("client formatter disclosed credentials")
			}
		}
	}
}

func TestManifestRejectsUntrustedMetadata(test *testing.T) {
	cases := map[string]func(*packv1.GetManifestResponse){
		"format":                 func(manifest *packv1.GetManifestResponse) { manifest.FormatVersion = 2 },
		"revision":               func(manifest *packv1.GetManifestResponse) { manifest.Revision = 0 },
		"negative revision":      func(manifest *packv1.GetManifestResponse) { manifest.Revision = -1 },
		"pack id":                func(manifest *packv1.GetManifestResponse) { manifest.PackId = "" },
		"empty skills":           func(manifest *packv1.GetManifestResponse) { manifest.Skills = nil },
		"unsafe slug":            func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].Slug = "../outside" },
		"reserved slug":          func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].Slug = "con" },
		"uppercase slug":         func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].Slug = "MixedCase" },
		"invalid content hash":   func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].ContentHash = strings.Repeat("z", 64) },
		"uppercase content hash": func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].ContentHash = strings.Repeat("A", 64) },
		"missing skill id":       func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].SkillId = "" },
		"missing repository id":  func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].RepositoryId = "" },
		"unsafe source path":     func(manifest *packv1.GetManifestResponse) { manifest.Skills[0].Path = "../outside" },
		"duplicate selection": func(manifest *packv1.GetManifestResponse) {
			manifest.Skills = append(manifest.Skills, proto.Clone(manifest.Skills[0]).(*packv1.PackSkill))
		},
		"duplicate slug": func(manifest *packv1.GetManifestResponse) {
			duplicate := proto.Clone(manifest.Skills[0]).(*packv1.PackSkill)
			duplicate.SkillId = "skill-2"
			manifest.Skills = append(manifest.Skills, duplicate)
		},
		"duplicate skill id": func(manifest *packv1.GetManifestResponse) {
			duplicate := proto.Clone(manifest.Skills[0]).(*packv1.PackSkill)
			duplicate.Slug = "other-slug"
			manifest.Skills = append(manifest.Skills, duplicate)
		},
		"unsorted selection": func(manifest *packv1.GetManifestResponse) {
			first := proto.Clone(manifest.Skills[0]).(*packv1.PackSkill)
			first.SkillId, first.Slug = "skill-2", "z-last"
			manifest.Skills = append([]*packv1.PackSkill{first}, manifest.Skills...)
		},
		"too many skills": func(manifest *packv1.GetManifestResponse) {
			manifest.Skills = make([]*packv1.PackSkill, 257)
			for index := range manifest.Skills {
				manifest.Skills[index] = &packv1.PackSkill{SkillId: fmt.Sprint(index)}
			}
		},
	}
	for name, mutate := range cases {
		test.Run(name, func(test *testing.T) {
			manifest, _ := fixtureManifest(test)
			mutate(manifest)
			sealManifest(manifest)
			client := fixtureClient(test, manifest, nil, nil)
			if received, err := client.Manifest(test.Context()); err == nil || received != nil {
				test.Fatal("untrusted manifest accepted")
			}
		})
	}
	test.Run("manifest digest", func(test *testing.T) {
		manifest, _ := fixtureManifest(test)
		manifest.Name = "tampered metadata"
		client := fixtureClient(test, manifest, nil, nil)
		if received, err := client.Manifest(test.Context()); err == nil || received != nil {
			test.Fatal("manifest with invalid digest accepted")
		}
	})
}

func TestDownloadRevalidatesSelectionBeforeNetwork(test *testing.T) {
	manifest, _ := fixtureManifest(test)
	var requests atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected network access")
	})
	client, err := New("https://example.com", fixtureToken(), &http.Client{Transport: transport})
	if err != nil {
		test.Fatal(err)
	}
	invalid := proto.Clone(manifest).(*packv1.GetManifestResponse)
	invalid.Revision++
	foreign := proto.Clone(manifest.Skills[0]).(*packv1.PackSkill)
	foreign.Slug = "other"
	for _, input := range []struct {
		manifest *packv1.GetManifestResponse
		selected *packv1.PackSkill
	}{{nil, nil}, {manifest, nil}, {invalid, invalid.Skills[0]}, {manifest, foreign}} {
		if snapshot, err := client.Download(test.Context(), input.manifest, input.selected); err == nil || snapshot != nil {
			test.Fatal("invalid selection accepted")
		}
	}
	if requests.Load() != 0 {
		test.Fatal("invalid selection crossed the network boundary")
	}
}

func TestDownloadRejectsInvalidFramingAndCleansTemporaryFiles(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	cases := map[string]func([]*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse{
		"missing final": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			return frames[:len(frames)-1]
		},
		"offset gap": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[0].Offset++
			return frames
		},
		"negative offset": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[0].Offset = -1
			return frames
		},
		"final offset": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[len(frames)-1].Offset--
			return frames
		},
		"duplicate final": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			return append(frames, proto.Clone(frames[len(frames)-1]).(*packv1.DownloadSkillResponse))
		},
		"data after final": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			return append(frames, &packv1.DownloadSkillResponse{Offset: frames[len(frames)-1].Offset, Data: []byte{1}})
		},
		"empty frame": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			return append([]*packv1.DownloadSkillResponse{{}}, frames...)
		},
		"data in final": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[len(frames)-1].Data = []byte{1}
			return frames
		},
		"early digest": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[0].TransferSha256 = strings.Repeat("a", 64)
			return frames
		},
		"early logical hash": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[0].ContentHash = strings.Repeat("a", 64)
			return frames
		},
		"wrong transfer digest": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[len(frames)-1].TransferSha256 = strings.Repeat("0", 64)
			return frames
		},
		"wrong final logical hash": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[len(frames)-1].ContentHash = strings.Repeat("0", 64)
			return frames
		},
		"oversized frame": func(frames []*packv1.DownloadSkillResponse) []*packv1.DownloadSkillResponse {
			frames[0].Data = make([]byte, 64<<10+1)
			return frames
		},
	}
	for name, mutate := range cases {
		test.Run(name, func(test *testing.T) {
			manifest, entries := fixtureManifest(test)
			frames := mutate(fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash))
			client := fixtureClient(test, manifest, frames, nil)
			assertRejectedDownload(test, client, manifest, temporary)
		})
	}
	test.Run("error after final", func(test *testing.T) {
		manifest, entries := fixtureManifest(test)
		client := fixtureClient(test, manifest, fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash), connect.NewError(connect.CodeAborted, errors.New("revision changed")))
		assertRejectedDownload(test, client, manifest, temporary)
	})
}

func assertRejectedDownload(test *testing.T, client *Client, manifest *packv1.GetManifestResponse, temporary string) {
	test.Helper()
	snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
	if err == nil || snapshot != nil {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		test.Fatal("invalid download produced a snapshot")
	}
	contents, err := os.ReadDir(temporary)
	if err != nil || len(contents) != 0 {
		test.Fatalf("failed download left temporary files: %v", err)
	}
}

func TestDownloadRejectsUnsafeArchives(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	cases := map[string]archiveEntry{
		"traversal":           {name: "skill/../escape", data: []byte("x")},
		"absolute":            {name: "/skill/escape", data: []byte("x")},
		"backslash":           {name: "skill/dir\\escape", data: []byte("x")},
		"windows drive":       {name: "skill/C:escape", data: []byte("x")},
		"windows reserved":    {name: "skill/CON.txt", data: []byte("x")},
		"trailing dot":        {name: "skill/hidden.", data: []byte("x")},
		"wrong root":          {name: "skills/escape", data: []byte("x")},
		"prefix sibling":      {name: "skill-sibling/escape", data: []byte("x")},
		"git metadata":        {name: "skill/.git/config", data: []byte("x")},
		"duplicate":           {name: "skill/SKILL.md", data: []byte("x")},
		"directory over file": {name: "skill/SKILL.md/", kind: tar.TypeDir},
		"symlink":             {name: "skill/link", kind: tar.TypeSymlink, link: "../escape"},
		"hardlink":            {name: "skill/link", kind: tar.TypeLink, link: "skill/SKILL.md"},
		"fifo":                {name: "skill/fifo", kind: tar.TypeFifo},
		"device":              {name: "skill/device", kind: tar.TypeChar},
	}
	for name, entry := range cases {
		test.Run(name, func(test *testing.T) {
			manifest, entries := fixtureManifest(test)
			data := fixtureArchive(test, append(entries, entry))
			client := fixtureClient(test, manifest, fixtureFrames(data, manifest.Skills[0].ContentHash), nil)
			assertRejectedDownload(test, client, manifest, temporary)
		})
	}
	test.Run("logical hash mismatch", func(test *testing.T) {
		manifest, entries := fixtureManifest(test)
		entries[2].data = []byte("modified content")
		client := fixtureClient(test, manifest, fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash), nil)
		assertRejectedDownload(test, client, manifest, temporary)
	})
}

func TestDownloadRejectsMalformedGzipAndTar(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	cases := map[string]func(*testing.T, []byte) []byte{
		"truncated gzip":    func(_ *testing.T, data []byte) []byte { return data[:len(data)-1] },
		"bad checksum":      func(_ *testing.T, data []byte) []byte { data[len(data)-8] ^= 0xff; return data },
		"trailing bytes":    func(_ *testing.T, data []byte) []byte { return append(data, []byte("ignored suffix")...) },
		"trailing zeros":    func(_ *testing.T, data []byte) []byte { return append(data, 0, 0) },
		"concatenated gzip": func(_ *testing.T, data []byte) []byte { return append(data, data...) },
		"tar suffix": func(test *testing.T, data []byte) []byte {
			return rewriteTar(test, data, func(expanded []byte) []byte { return append(expanded, []byte("hidden payload")...) })
		},
		"missing tar terminator": func(test *testing.T, data []byte) []byte {
			return rewriteTar(test, data, func(expanded []byte) []byte { return expanded[:len(expanded)-1024] })
		},
		"one tar terminator": func(test *testing.T, data []byte) []byte {
			return rewriteTar(test, data, func(expanded []byte) []byte { return expanded[:len(expanded)-512] })
		},
	}
	for name, mutate := range cases {
		test.Run(name, func(test *testing.T) {
			manifest, entries := fixtureManifest(test)
			data := mutate(test, fixtureArchive(test, entries))
			client := fixtureClient(test, manifest, fixtureFrames(data, manifest.Skills[0].ContentHash), nil)
			assertRejectedDownload(test, client, manifest, temporary)
		})
	}
}

func rewriteTar(test *testing.T, data []byte, mutate func([]byte) []byte) []byte {
	test.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		test.Fatal(err)
	}
	expanded, err := io.ReadAll(compressed)
	if err != nil {
		test.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		test.Fatal(err)
	}
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(mutate(expanded)); err != nil {
		test.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		test.Fatal(err)
	}
	return output.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (transport roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestRedirectsNeverForwardCredentialsAndErrorsAreRedacted(test *testing.T) {
	token := fixtureToken()
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	test.Cleanup(target.Close)
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		test.Run(fmt.Sprint(status), func(test *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.Contains(request.URL.String(), token) || request.Header.Get("Authorization") != "Bearer "+token {
					test.Error("credential entered the URL or was not authorized")
				}
				writer.Header().Set("Location", target.URL+"/?credential="+token)
				writer.WriteHeader(status)
			}))
			test.Cleanup(server.Close)
			original := server.Client()
			original.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
			client, err := New(server.URL, token, original)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := client.Manifest(test.Context()); err == nil || strings.Contains(fmt.Sprintf("%v %#v", err, err), token) {
				test.Fatal("redirect succeeded or disclosed credentials")
			}
		})
	}
	if redirected.Load() != 0 {
		test.Fatal("redirect was followed")
	}
	test.Run("transport error", func(test *testing.T) {
		var called atomic.Bool
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return nil, fmt.Errorf("untrusted transport %s", token)
		})
		client, err := New("https://example.com", token, &http.Client{Transport: transport})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := client.Manifest(test.Context()); err == nil || strings.Contains(fmt.Sprintf("%v %#v", err, err), token) || !called.Load() {
			test.Fatal("transport was not preserved or its error disclosed credentials")
		}
	})
}

func TestCancellationInterruptsInFlightDownloadAndCleans(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	manifest, _ := fixtureManifest(test)
	started := make(chan struct{})
	finished := make(chan struct{})
	fixture := distributionFixture{download: func(ctx context.Context, _ *connect.Request[packv1.DownloadSkillRequest], stream *connect.ServerStream[packv1.DownloadSkillResponse]) error {
		defer close(finished)
		if err := stream.Send(&packv1.DownloadSkillResponse{Data: []byte{1, 2, 3}}); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	_, handler := packv1connect.NewDistributionServiceHandler(fixture)
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	client, err := New(server.URL, fixtureToken(), server.Client())
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	result := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		snapshot, err := client.Download(ctx, manifest, manifest.Skills[0])
		if snapshot != nil {
			_ = snapshot.Close()
		}
		result <- err
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		test.Fatal("download did not reach the server")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			test.Fatalf("cancellation was lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("download ignored cancellation")
	}
	workers.Wait()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		test.Fatal("server request leaked")
	}
	contents, err := os.ReadDir(temporary)
	if err != nil || len(contents) != 0 {
		test.Fatalf("canceled download left files: %v", err)
	}
}

func TestDownloadRejectsRegularFileWithDirectorySuffix(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	manifest, entries := fixtureManifest(test)
	data := rewriteTar(test, fixtureArchive(test, entries), func(expanded []byte) []byte {
		header := expanded[512:1024]
		header[len("skill/SKILL.md")] = '/'
		copy(header[148:156], "        ")
		checksum := 0
		for _, value := range header {
			checksum += int(value)
		}
		copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
		return expanded
	})
	client := fixtureClient(test, manifest, fixtureFrames(data, manifest.Skills[0].ContentHash), nil)
	assertRejectedDownload(test, client, manifest, temporary)
}

func TestDownloadRequiresHTTPCompletionAfterConnectEnd(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	manifest, entries := fixtureManifest(test)
	frames := fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash)
	for _, malformed := range []bool{false, true} {
		test.Run(fmt.Sprint(malformed), func(test *testing.T) {
			var wire bytes.Buffer
			writeEnvelope := func(flags byte, message []byte) {
				wire.WriteByte(flags)
				_ = binary.Write(&wire, binary.BigEndian, uint32(len(message)))
				wire.Write(message)
			}
			for _, frame := range frames {
				message, err := proto.Marshal(frame)
				if err != nil {
					test.Fatal(err)
				}
				writeEnvelope(0, message)
			}
			writeEnvelope(2, []byte("{}"))
			if malformed {
				writeEnvelope(0, []byte{1, 2, 3})
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/connect+proto")
				_, _ = writer.Write(wire.Bytes())
			}))
			test.Cleanup(server.Close)
			client, err := New(server.URL, fixtureToken(), server.Client())
			if err != nil {
				test.Fatal(err)
			}
			if malformed {
				assertRejectedDownload(test, client, manifest, temporary)
				return
			}
			snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
			if err != nil {
				test.Fatalf("normal RPC completion rejected: %v", err)
			}
			if err := snapshot.Close(); err != nil {
				test.Fatal(err)
			}
		})
	}
}

func TestOutputEntryBudgetIncludesImplicitDirectories(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	manifest, entries := fixtureManifest(test)
	for index := range 350 {
		name := fmt.Sprintf("skill/tree-%04d/", index) + strings.Repeat("nested/", 30) + "leaf"
		entries = append(entries, archiveEntry{name: name, mode: 0o600})
	}
	client := fixtureClient(test, manifest, fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash), nil)
	snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
	if snapshot != nil {
		_ = snapshot.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "limits") {
		test.Fatalf("implicit directories bypassed output budget: %v", err)
	}
	contents, readErr := os.ReadDir(temporary)
	if readErr != nil || len(contents) != 0 {
		test.Fatalf("output budget failure left files: %v", readErr)
	}
}

func TestMultiFrameBinaryAndPAXPaths(test *testing.T) {
	manifest, entries := fixtureManifest(test)
	data := make([]byte, 150<<10)
	if _, err := rand.Read(data); err != nil {
		test.Fatal(err)
	}
	name := "skill/" + strings.Repeat("子目录", 20) + "/binary"
	entries = append(entries, archiveEntry{name: name, data: data, mode: 0o710})
	manifest.Skills[0].ContentHash = fixtureContentHash(test, entries)
	sealManifest(manifest)
	frames := fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash)
	if len(frames) < 4 {
		test.Fatal("fixture does not exercise multiple chunks")
	}
	client := fixtureClient(test, manifest, frames, nil)
	snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = snapshot.Close() })
	actual, err := os.ReadFile(filepath.Join(snapshot.Path(), strings.TrimPrefix(name, "skill/")))
	if err != nil || !bytes.Equal(data, actual) {
		test.Fatalf("multi-frame binary data changed: %v", err)
	}
}

func TestArchiveValidationDoesNotDependOnHashMismatch(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	cases := map[string]func([]archiveEntry) []archiveEntry{
		"case alias": func(entries []archiveEntry) []archiveEntry {
			return append(entries, archiveEntry{name: "skill/.HIDDEN", data: []byte("alias"), mode: 0o600})
		},
		"case alias in parent": func(entries []archiveEntry) []archiveEntry {
			return append(entries, archiveEntry{name: "skill/BIN/other", mode: 0o600})
		},
		"reserved name": func(entries []archiveEntry) []archiveEntry {
			return append(entries, archiveEntry{name: "skill/aux.txt", mode: 0o600})
		},
		"trailing dot": func(entries []archiveEntry) []archiveEntry {
			return append(entries, archiveEntry{name: "skill/hidden.", mode: 0o600})
		},
		"setuid mode": func(entries []archiveEntry) []archiveEntry {
			entries[2].mode |= 0o4000
			return entries
		},
		"missing skill document": func(entries []archiveEntry) []archiveEntry {
			return append(entries[:1], entries[2:]...)
		},
	}
	for name, mutate := range cases {
		test.Run(name, func(test *testing.T) {
			manifest, entries := fixtureManifest(test)
			entries = mutate(entries)
			manifest.Skills[0].ContentHash = fixtureContentHash(test, entries)
			sealManifest(manifest)
			client := fixtureClient(test, manifest, fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash), nil)
			assertRejectedDownload(test, client, manifest, temporary)
		})
	}
}
