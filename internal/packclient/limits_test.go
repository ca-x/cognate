package packclient

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
)

func TestDownloadResourceLimits(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	test.Run("compressed transfer", func(test *testing.T) {
		manifest, _ := fixtureManifest(test)
		chunk := make([]byte, 64<<10)
		frames := make([]*packv1.DownloadSkillResponse, 0, 1300)
		for offset := int64(0); offset <= 81<<20; offset += int64(len(chunk)) {
			frames = append(frames, &packv1.DownloadSkillResponse{Offset: offset, Data: chunk})
		}
		assertLimitedDownload(test, fixtureClient(test, manifest, frames, nil), manifest, temporary)
	})
	test.Run("frame count", func(test *testing.T) {
		manifest, _ := fixtureManifest(test)
		frames := make([]*packv1.DownloadSkillResponse, 16_385)
		for index := range frames {
			frames[index] = &packv1.DownloadSkillResponse{Offset: int64(index), Data: []byte{1}}
		}
		assertLimitedDownload(test, fixtureClient(test, manifest, frames, nil), manifest, temporary)
	})
	test.Run("skill document", func(test *testing.T) {
		manifest, entries := fixtureManifest(test)
		entries[1].data = make([]byte, 1<<20+1)
		data := fixtureArchive(test, entries)
		assertLimitedDownload(test, fixtureClient(test, manifest, fixtureFrames(data, manifest.Skills[0].ContentHash), nil), manifest, temporary)
	})
	test.Run("declared file bytes", func(test *testing.T) {
		manifest, entries := fixtureManifest(test)
		data := rewriteTar(test, fixtureArchive(test, entries), func(expanded []byte) []byte {
			header := expanded[512:1024]
			copy(header[124:136], fmt.Sprintf("%011o\x00", 64<<20+1))
			copy(header[148:156], "        ")
			checksum := 0
			for _, value := range header {
				checksum += int(value)
			}
			copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
			return expanded
		})
		assertLimitedDownload(test, fixtureClient(test, manifest, fixtureFrames(data, manifest.Skills[0].ContentHash), nil), manifest, temporary)
	})
	test.Run("expanded tar bytes", func(test *testing.T) {
		manifest, entries := fixtureManifest(test)
		original, err := gzip.NewReader(bytes.NewReader(fixtureArchive(test, entries)))
		if err != nil {
			test.Fatal(err)
		}
		var output bytes.Buffer
		compressed := gzip.NewWriter(&output)
		if _, err := io.Copy(compressed, original); err != nil {
			test.Fatal(err)
		}
		if _, err := io.CopyN(compressed, zeroReader{}, 80<<20); err != nil {
			test.Fatal(err)
		}
		if err := errors.Join(original.Close(), compressed.Close()); err != nil {
			test.Fatal(err)
		}
		assertLimitedDownload(test, fixtureClient(test, manifest, fixtureFrames(output.Bytes(), manifest.Skills[0].ContentHash), nil), manifest, temporary)
	})
}

func assertLimitedDownload(test *testing.T, client *Client, manifest *packv1.GetManifestResponse, temporary string) {
	test.Helper()
	snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
	if snapshot != nil {
		_ = snapshot.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "limits") {
		test.Fatalf("download did not enforce its resource budget: %v", err)
	}
	contents, err := os.ReadDir(temporary)
	if err != nil || len(contents) != 0 {
		test.Fatalf("limited download left temporary files: %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
