package repository

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/skill"
	"github.com/spf13/afero"
)

func TestSkillArchiveIsDeterministicAndConfined(test *testing.T) {
	data := archiveFixture(test,
		archiveEntry{name: "repo-main/skills/selected/SKILL.md", body: "---\nname: Example\n---\nContent"},
		archiveEntry{name: "repo-main/skills/selected/.hidden/asset.bin", body: "\x00\xffbinary"},
		archiveEntry{name: "repo-main/skills/selected/run", body: "#!/bin/sh\necho yes\n", mode: 0o755},
		archiveEntry{name: "repo-main/skills/selected/.git/secret", body: "not distributable"},
		archiveEntry{name: "repo-main/skills/selected-other/secret", body: "sibling"},
		archiveEntry{name: "repo-main/skills/selected/link", kind: tar.TypeSymlink},
	)
	snapshot, err := inspectArchive(test.Context(), data)
	if err != nil {
		test.Fatal(err)
	}
	var first, second bytes.Buffer
	for _, output := range []*bytes.Buffer{&first, &second} {
		if err := WriteSkillArchive(test.Context(), data, "skills/selected", snapshot.Skills[0].ContentHash, output); err != nil {
			test.Fatal(err)
		}
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		test.Fatal("archive bytes are not deterministic")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(first.Bytes()))
	if err != nil {
		test.Fatal(err)
	}
	defer compressed.Close()
	filesystem := afero.NewMemMapFs()
	if err := filesystem.Mkdir("/repository", 0o700); err != nil {
		test.Fatal(err)
	}
	if err := extractArchive(test.Context(), tar.NewReader(compressed), filesystem); err != nil {
		test.Fatal(err)
	}
	hash, err := skill.ContentHash(filesystem, "/repository")
	if err != nil || hash != snapshot.Skills[0].ContentHash {
		test.Fatalf("binary/directory/executable hash round trip failed: %v", err)
	}
	for _, omitted := range []string{"/repository/.git", "/repository/link", "/repository/secret"} {
		if exists, _ := afero.Exists(filesystem, omitted); exists {
			test.Fatalf("unselected path included: %s", omitted)
		}
	}
	var invalid bytes.Buffer
	if err := WriteSkillArchive(test.Context(), data, "skills/selected", strings.Repeat("f", 64), &invalid); err == nil || invalid.Len() != 0 {
		test.Fatal("wrong content hash emitted bytes")
	}
	for _, path := range []string{"../skills/selected", "/skills/selected", "skills/selected/../selected", "skills\\selected"} {
		if err := WriteSkillArchive(test.Context(), data, path, snapshot.Skills[0].ContentHash, io.Discard); err == nil {
			test.Fatalf("accepted invalid path %q", path)
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if err := WriteSkillArchive(ctx, data, "skills/selected", snapshot.Skills[0].ContentHash, io.Discard); !errors.Is(err, context.Canceled) {
		test.Fatalf("cancellation: %v", err)
	}
}
