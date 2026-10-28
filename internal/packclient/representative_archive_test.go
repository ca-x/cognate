package packclient

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestRepresentativeRejectsOrphanPAXAtEnd(test *testing.T) {
	for _, terminalBlocks := range []int{0, 1, 2} {
		test.Run(fmt.Sprintf("%d_terminal_blocks", terminalBlocks), func(test *testing.T) {
			manifest, entries := fixtureManifest(test)
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
			directory := filepath.Join(test.TempDir(), "skill")
			if err := extractSkill(test.Context(), bytes.NewReader(data), directory, manifest.Skills[0].ContentHash); !errors.Is(err, errArchive) {
				test.Fatalf("orphan PAX metadata with %d terminal blocks must be rejected, got %v", terminalBlocks, err)
			}
		})
	}
}
