package packattachments

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestReferencesFile(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("real nofollow file fixture is Linux-only")
	}
	valid := `[{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":1,"targetId":"codex.provider.user"}]`
	for _, value := range []string{valid, `[]`} {
		path := filepath.Join(test.TempDir(), "references.json")
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			test.Fatal(err)
		}
		refs, err := ReadReferences(test.Context(), path)
		if err != nil || refs == nil || value == valid && (len(refs) != 1 || refs[0].ExpectedAssetRevision != 1) {
			test.Fatal("valid ordered references file refused")
		}
	}
	for _, value := range []string{`null`, `{}`, `[null]`, `[{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":1,"targetId":"codex.provider.user","extra":true}]`, `[{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":1,"targetId":"codex.provider.user"}]`, `[{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":1.0,"targetId":"codex.provider.user"}]`, `[{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":9223372036854775808,"targetId":"codex.provider.user"}]`, `[` + valid[1:len(valid)-1] + `,` + valid[1:len(valid)-1] + `]`} {
		path := filepath.Join(test.TempDir(), "references.json")
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			test.Fatal(err)
		}
		if _, err := ReadReferences(test.Context(), path); !errors.Is(err, ErrInvalid) {
			test.Fatal("malformed references accepted")
		}
	}
}

func TestReferencesPhysicalRefusals(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("actual Linux nofollow fixture")
	}
	for _, mode := range []string{"symlink", "hardlink", "fifo", "directory", "public-write", "oversize", "too-many"} {
		test.Run(mode, func(test *testing.T) {
			dir := test.TempDir()
			path := filepath.Join(dir, "references.json")
			source := filepath.Join(dir, "owned.json")
			if os.WriteFile(source, []byte(`[]`), 0600) != nil {
				test.Fatal("fixture write")
			}
			switch mode {
			case "symlink":
				if os.Symlink(source, path) != nil {
					test.Fatal("symlink fixture")
				}
			case "hardlink":
				if os.Link(source, path) != nil {
					test.Fatal("hardlink fixture")
				}
			case "fifo":
				if exec.Command("mkfifo", path).Run() != nil {
					test.Fatal("FIFO fixture")
				}
			case "directory":
				if os.Mkdir(path, 0700) != nil {
					test.Fatal("directory fixture")
				}
			case "public-write":
				if os.WriteFile(path, []byte(`[]`), 0666) != nil || os.Chmod(path, 0666) != nil {
					test.Fatal("public file fixture")
				}
			case "oversize":
				if os.WriteFile(path, bytes.Repeat([]byte{' '}, (64<<10)+1), 0600) != nil {
					test.Fatal("large fixture")
				}
			case "too-many":
				data := "["
				for index := range 17 {
					if index > 0 {
						data += ","
					}
					data += fmt.Sprintf(`{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":1,"targetId":"target%d"}`, index)
				}
				data += "]"
				if os.WriteFile(path, []byte(data), 0600) != nil {
					test.Fatal("refs fixture")
				}
			}
			if _, err := ReadReferences(test.Context(), path); !errors.Is(err, ErrInvalid) {
				test.Fatal("unsafe physical/oversize references accepted")
			}
		})
	}
}

func TestReferencesUnknownStringMember(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("physical Linux input")
	}
	path := filepath.Join(test.TempDir(), "references.json")
	data := []byte(`[{"assetId":"AAAAAAAAAAAAAAAAAAAAAAAAAA","expectedAssetRevision":1,"targetId":"codex.provider.user","unknown":"not silently dropped"}]`)
	if os.WriteFile(path, data, 0600) != nil {
		test.Fatal("file fixture")
	}
	if _, err := ReadReferences(test.Context(), path); !errors.Is(err, ErrInvalid) {
		test.Fatal("unknown string member silently dropped")
	}
}

func TestCodecRejectsTransactionally(test *testing.T) {
	codec := attachmentCodec{}
	wrap := func(inner []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), inner)
	}
	duplicate := append(wrap(nil), wrap(nil)...)
	wrongWire := wrap(protowire.AppendBytes(protowire.AppendTag(nil, 2, protowire.BytesType), []byte("1")))
	unknown := wrap(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	badEnum := wrap(protowire.AppendBytes(protowire.AppendTag(nil, 4, protowire.BytesType), protowire.AppendVarint(protowire.AppendTag(nil, 9, protowire.VarintType), 99)))
	badOrdinal := wrap(protowire.AppendBytes(protowire.AppendTag(nil, 4, protowire.BytesType), protowire.AppendVarint(protowire.AppendTag(nil, 8, protowire.VarintType), 1<<32)))
	for _, data := range [][]byte{duplicate, wrongWire, unknown, badEnum, badOrdinal, {0x8a, 0x00, 0}, bytes.Repeat([]byte{0}, (64<<10)+1)} {
		destination := &packv1.GetAttachmentsResponse{Snapshot: &packv1.AttachmentSnapshot{PackId: "KEEP"}}
		original := proto.Clone(destination)
		if codec.Unmarshal(data, destination) == nil || !proto.Equal(destination, original) {
			test.Fatal("strict response rejection mutated destination")
		}
	}
}
