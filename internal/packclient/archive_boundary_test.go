package packclient

import (
	"archive/tar"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestDownloadRejectsDanglingPAXAndCleans(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	for _, size := range []int{0, 1, 511, 512, 513} {
		for _, terminalBlocks := range []int{0, 1, 2} {
			test.Run(fmt.Sprintf("member_size_%d/terminal_blocks_%d", size, terminalBlocks), func(test *testing.T) {
				manifest, entries := fixtureManifest(test)
				entries = append(entries, archiveEntry{name: "skill/final", data: bytes.Repeat([]byte{1}, size), mode: 0o600})
				manifest.Skills[0].ContentHash = fixtureContentHash(test, entries)
				sealManifest(manifest)
				entries = append(entries, archiveEntry{name: "skill/PaxHeaders/trailing", data: []byte("22 path=skill/ignored\n"), mode: 0o600})
				data := rewriteTar(test, fixtureArchive(test, entries), func(expanded []byte) []byte {
					header := expanded[len(expanded)-2048 : len(expanded)-1536]
					header[156] = tar.TypeXHeader
					copy(header[148:156], "        ")
					checksum := 0
					for _, value := range header {
						checksum += int(value)
					}
					copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
					return expanded[:len(expanded)-1024+terminalBlocks*512]
				})
				client := fixtureClient(test, manifest, fixtureFrames(data, manifest.Skills[0].ContentHash), nil)
				assertRejectedDownload(test, client, manifest, temporary)
			})
		}
	}
}

func TestTarBoundaryPreservesFinalPAXMemberAndPadding(test *testing.T) {
	for _, size := range []int{0, 1, 511, 512, 513} {
		test.Run(fmt.Sprint(size), func(test *testing.T) {
			manifest, entries := fixtureManifest(test)
			name := "skill/" + strings.Repeat("子目录", 20) + "/final"
			data := bytes.Repeat([]byte{1}, size)
			entries = append(entries, archiveEntry{name: name, data: data, mode: 0o710})
			manifest.Skills[0].ContentHash = fixtureContentHash(test, entries)
			sealManifest(manifest)
			client := fixtureClient(test, manifest, fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash), nil)
			snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
			if err != nil {
				test.Fatalf("valid final PAX member or padding rejected: %v", err)
			}
			if err := snapshot.Close(); err != nil {
				test.Fatal(err)
			}
		})
	}
}
