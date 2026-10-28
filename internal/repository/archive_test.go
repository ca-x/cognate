package repository

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type archiveEntry struct {
	name string
	body string
	kind byte
	pax  map[string]string
	mode int64
}

func archiveFixture(test testing.TB, entries ...archiveEntry) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0o644, Typeflag: kind}
		if entry.mode != 0 {
			header.Mode = entry.mode
		}
		if kind == tar.TypeReg {
			header.Size = int64(len(entry.body))
		}
		if kind == tar.TypeSymlink || kind == tar.TypeLink {
			header.Linkname = "../../outside"
		}
		if kind == tar.TypeXGlobalHeader {
			header.Mode = 0
			header.PAXRecords = map[string]string{"comment": "GitHub commit metadata"}
			if entry.pax != nil {
				header.PAXRecords = entry.pax
			}
		}
		if err := archive.WriteHeader(header); err != nil {
			test.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := io.WriteString(archive, entry.body); err != nil {
				test.Fatal(err)
			}
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func TestParseReferenceNormalizesIdentity(test *testing.T) {
	for _, input := range []string{"Org/Repo", " https://github.com/ORG/REPO.git/ "} {
		reference, err := ParseReference(input, "HEAD")
		if err != nil || reference.NameWithOwner != "org/repo" || reference.Branch != "" {
			test.Fatalf("parse %q: %+v, %v", input, reference, err)
		}
	}
	for _, input := range []string{"https://github.com.evil/a/b", "https://user@github.com/a/b", "https://github.com/a/b?token=hidden", "../repo"} {
		if _, err := ParseReference(input, ""); err == nil {
			test.Fatalf("accepted %q", input)
		}
	}
	for _, branch := range []string{"../main", "main\n", "refs/../main", "-option", "a.lock", "a//b"} {
		if _, err := ParseReference("org/repo", branch); err == nil {
			test.Fatalf("accepted branch %q", branch)
		}
	}
}

func TestArchiveScansHiddenDirectoriesAndBinaryContent(test *testing.T) {
	data := archiveFixture(test,
		archiveEntry{name: "repo-main/.claude/skills/example/SKILL.md", body: "---\nname: Example\ndescription: A skill\n---\nContent"},
		archiveEntry{name: "repo-main/.claude/skills/example/icon.bin", body: "\x00\xffbinary"},
		archiveEntry{name: "repo-main/fallback/SKILL.md", body: "No frontmatter"},
		archiveEntry{name: "repo-main/link", kind: tar.TypeSymlink},
	)
	snapshot, err := inspectArchive(test.Context(), data)
	if err != nil {
		test.Fatal(err)
	}
	if len(snapshot.Skills) != 2 || len(snapshot.Hash) != 64 || !bytes.Equal(snapshot.Data, data) {
		test.Fatalf("unexpected snapshot: %d skills", len(snapshot.Skills))
	}
	if snapshot.Skills[0].Name != "Example" || snapshot.Skills[0].Path != ".claude/skills/example" || len(snapshot.Skills[0].ContentHash) != 64 || snapshot.Skills[1].Name != "fallback" {
		test.Fatalf("wrong metadata: %+v", snapshot.Skills)
	}
	changed := archiveFixture(test,
		archiveEntry{name: "repo-main/.claude/skills/example/SKILL.md", body: "---\nname: Example\ndescription: A skill\n---\nContent"},
		archiveEntry{name: "repo-main/.claude/skills/example/icon.bin", body: "\x00\xffchanged"},
	)
	updated, err := inspectArchive(test.Context(), changed)
	if err != nil || updated.Skills[0].ContentHash == snapshot.Skills[0].ContentHash {
		test.Fatalf("binary content omitted from hash: %v", err)
	}
}

func TestArchiveAcceptsGitHubPAXGlobalMetadata(test *testing.T) {
	data := archiveFixture(test,
		archiveEntry{name: "pax_global_header", kind: tar.TypeXGlobalHeader},
		archiveEntry{name: "repo-main/SKILL.md", body: "A valid skill"},
	)
	snapshot, err := inspectArchive(test.Context(), data)
	if err != nil || len(snapshot.Skills) != 1 {
		test.Fatalf("GitHub metadata header rejected: %v", err)
	}
}

func TestArchiveRejectsUnscannedTrailingContent(test *testing.T) {
	data := archiveFixture(test, archiveEntry{name: "repo-main/SKILL.md", body: "A valid skill"})
	trailing := archiveFixture(test, archiveEntry{name: "repo-main/hidden", body: "must be scanned"})
	if _, err := inspectArchive(test.Context(), append(data, trailing...)); err == nil {
		test.Fatal("unscanned concatenated archive accepted")
	}
}

func TestArchiveRejectsStructuralGlobalPAXAndEntryLimits(test *testing.T) {
	data := archiveFixture(test,
		archiveEntry{name: "pax_global_header", kind: tar.TypeXGlobalHeader, pax: map[string]string{"path": "../../escape"}},
		archiveEntry{name: "root/SKILL.md", body: "ok"},
	)
	if _, err := inspectArchive(test.Context(), data); err == nil {
		test.Fatal("structural global PAX header accepted")
	}
	for _, records := range []map[string]string{
		{"uid": "abc"},
		{"uid": "abc", "path": "../../unsafe"},
		{"uid": "abc", "comment": "metadata"},
		{"gid": "abc", "comment": "metadata"},
		{"mtime": "invalid", "comment": "metadata"},
	} {
		data := archiveFixture(test, archiveEntry{name: "pax_global_header", kind: tar.TypeXGlobalHeader, pax: records}, archiveEntry{name: "root/SKILL.md", body: "ok"})
		if _, err := inspectArchive(test.Context(), data); err == nil {
			test.Fatalf("invalid global PAX records accepted: %v", records)
		}
	}
	entries := make([]archiveEntry, 0, maxArchiveFiles+1)
	entries = append(entries, archiveEntry{name: "root/SKILL.md", body: "ok"})
	for index := range maxArchiveFiles {
		entries = append(entries, archiveEntry{name: fmt.Sprintf("root/file-%d", index), body: ""})
	}
	if _, err := inspectArchive(test.Context(), archiveFixture(test, entries...)); !errors.Is(err, ErrArchiveTooLarge) {
		test.Fatalf("entry count limit: %v", err)
	}
	entries = entries[:0]
	for index := range maxArchiveSkills + 1 {
		entries = append(entries, archiveEntry{name: fmt.Sprintf("root/skill-%d/SKILL.md", index), body: "ok"})
	}
	if _, err := inspectArchive(test.Context(), archiveFixture(test, entries...)); !errors.Is(err, ErrArchiveTooLarge) {
		test.Fatalf("skill count limit: %v", err)
	}
}

func BenchmarkArchiveInspection(benchmark *testing.B) {
	entries := make([]archiveEntry, 0, 20)
	for index := range 10 {
		entries = append(entries, archiveEntry{name: fmt.Sprintf("root/skill-%d/SKILL.md", index), body: "---\nname: Example\ndescription: A skill\n---\nInstructions"}, archiveEntry{name: fmt.Sprintf("root/skill-%d/icon.bin", index), body: strings.Repeat("\x00\xff", 512)})
	}
	data := archiveFixture(benchmark, entries...)
	benchmark.ReportAllocs()
	for benchmark.Loop() {
		if _, err := inspectArchive(benchmark.Context(), data); err != nil {
			benchmark.Fatal(err)
		}
	}
}

func TestArchiveRejectsUnsafeOrInvalidContent(test *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "root/../escape", "root/back\\slash", "other/SKILL.md"} {
		test.Run(name, func(test *testing.T) {
			data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "ok"}, archiveEntry{name: name, body: "bad"})
			if _, err := inspectArchive(test.Context(), data); err == nil {
				test.Fatal("unsafe archive accepted")
			}
		})
	}
	for _, entries := range [][]archiveEntry{
		{{name: "root/README.md", body: "no skills"}},
		{{name: "root/SKILL.md", body: "ok"}, {name: "root/SKILL.md", body: "duplicate"}},
		{{name: "root/SKILL.md", body: "---\nname: unclosed"}},
		{{name: "root/SKILL.md", body: strings.Repeat("x", maxSkillDocument+1)}},
	} {
		if _, err := inspectArchive(test.Context(), archiveFixture(test, entries...)); err == nil {
			test.Fatal("invalid archive accepted")
		}
	}
	data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "ok"})
	if _, err := inspectArchive(test.Context(), data[:len(data)-4]); err == nil {
		test.Fatal("truncated gzip accepted")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := inspectArchive(ctx, data); !errors.Is(err, context.Canceled) {
		test.Fatalf("cancellation: %v", err)
	}
}

type archiveTransport func(*http.Request) (*http.Response, error)

func (transport archiveTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestArchiveFetchUsesFixedHostAndNoCredentials(test *testing.T) {
	data := archiveFixture(test, archiveEntry{name: "repo-main/SKILL.md", body: "ok"})
	client := &http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "codeload.github.com" || request.URL.EscapedPath() != "/org/repo/tar.gz/feature%2Fskills" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			test.Fatal("unsafe upstream request")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	reference, err := ParseReference("org/repo", "feature/skills")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := NewArchiveFetcher(client).Fetch(test.Context(), reference); err != nil {
		test.Fatal(err)
	}
	var calls int
	client.Transport = archiveTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://example.invalid/leak"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err := NewArchiveFetcher(client).Fetch(test.Context(), reference); err == nil || calls != 1 {
		test.Fatalf("redirect followed: calls=%d, err=%v", calls, err)
	}
}
