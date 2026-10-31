package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"

	"github.com/ca-x/cognate/internal/server/backup"
)

var errInventoryInvalid = errors.New("restore inventory refused")
var errInventoryBudget = errors.New("restore inventory budget exceeded")

type inventoryLimits struct {
	bytes        int64
	archives     uint32
	contentBytes int64
}

type inventoryReferences struct {
	bytes   int64
	digest  [32]byte
	records uint64
}

type inventoryArchive struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type inventoryHeader struct {
	Format     string             `json:"format"`
	Version    uint32             `json:"version"`
	SnapshotID string             `json:"snapshot_id"`
	References uint64             `json:"references"`
	Archives   []inventoryArchive `json:"archives"`
}

type inventoryDescription struct {
	archives     []inventoryArchive
	contentBytes int64
}

func readInventory(ctx context.Context, source io.Reader, metadata backup.SealMetadata, references inventoryReferences, limits inventoryLimits) (inventoryDescription, error) {
	if ctx == nil || source == nil || limits.bytes <= 0 || limits.bytes > 64<<20 || limits.archives == 0 || limits.archives > 100000 || limits.contentBytes <= 0 || limits.contentBytes > 1<<40 || references.bytes < 0 || references.records > 1000000 || metadata.Inventory.Bytes <= 0 || metadata.Database.Bytes <= 0 {
		return inventoryDescription{}, errInventoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return inventoryDescription{}, err
	}
	if metadata.Inventory.Bytes > limits.bytes || metadata.Archives > limits.archives || metadata.Database.Bytes > limits.contentBytes || metadata.Inventory.Bytes > limits.contentBytes-metadata.Database.Bytes {
		return inventoryDescription{}, errInventoryBudget
	}
	if references.bytes >= metadata.Inventory.Bytes || uint64(metadata.Inventory.Records) != references.records+1 {
		return inventoryDescription{}, errInventoryInvalid
	}
	inventoryDigest, inventoryValid := inventoryHash(metadata.Inventory.SHA256)
	databaseDigest, databaseValid := inventoryHash(metadata.Database.SHA256)
	closureDigest, closureValid := inventoryHash(metadata.ClosureSHA256)
	cut, cutError := hex.DecodeString(metadata.CutID)
	if !inventoryValid || !databaseValid || !closureValid || cutError != nil || len(cut) != 16 || hex.EncodeToString(cut) != metadata.CutID || metadata.CutID == strings.Repeat("0", 32) {
		return inventoryDescription{}, errInventoryInvalid
	}
	payload, err := io.ReadAll(io.LimitReader(inventoryReader{ctx: ctx, source: source}, metadata.Inventory.Bytes+1))
	defer clear(payload)
	if ctx.Err() != nil {
		return inventoryDescription{}, ctx.Err()
	}
	if err != nil {
		return inventoryDescription{}, errInventoryInvalid
	}
	if int64(len(payload)) != metadata.Inventory.Bytes || sha256.Sum256(payload) != inventoryDigest {
		return inventoryDescription{}, errInventoryInvalid
	}
	headerBytes, tail, found := bytes.Cut(payload, []byte{'\n'})
	if !found || int64(len(tail)) != references.bytes || sha256.Sum256(tail) != references.digest {
		return inventoryDescription{}, errInventoryInvalid
	}
	var wire struct {
		Format     *string         `json:"format"`
		Version    *uint32         `json:"version"`
		SnapshotID *string         `json:"snapshot_id"`
		References *uint64         `json:"references"`
		Archives   *jsontext.Value `json:"archives"`
	}
	if err := json.Unmarshal(headerBytes, &wire, json.RejectUnknownMembers(true)); err != nil || wire.Format == nil || wire.Version == nil || wire.SnapshotID == nil || wire.References == nil || wire.Archives == nil {
		return inventoryDescription{}, errInventoryInvalid
	}
	headerArchives, err := inventoryReadArchives(ctx, *wire.Archives, metadata.Archives)
	if err != nil {
		return inventoryDescription{}, err
	}
	header := inventoryHeader{Format: *wire.Format, Version: *wire.Version, SnapshotID: *wire.SnapshotID, References: *wire.References, Archives: headerArchives}
	if header.Format != "cognate-content-inventory" || header.Version != 1 || header.SnapshotID != metadata.CutID || header.References != references.records || uint64(len(header.Archives)) != uint64(metadata.Archives) {
		return inventoryDescription{}, errInventoryInvalid
	}
	owners := make(map[int64]struct{})
	archives := make(map[string]struct{})
	var records uint64
	for len(tail) != 0 {
		if err := ctx.Err(); err != nil {
			return inventoryDescription{}, err
		}
		line, remaining, found := bytes.Cut(tail, []byte{'\n'})
		if !found || len(line) == 0 || records >= references.records {
			return inventoryDescription{}, errInventoryInvalid
		}
		var reference struct {
			Kind        string `json:"kind"`
			Owner       int64  `json:"owner"`
			ArchiveHash string `json:"archive_hash"`
		}
		if err := json.Unmarshal(line, &reference); err != nil {
			return inventoryDescription{}, errInventoryInvalid
		}
		switch reference.Kind {
		case "repository", "pack":
			if reference.Owner <= 0 {
				return inventoryDescription{}, errInventoryInvalid
			}
			owners[reference.Owner] = struct{}{}
			if reference.Kind == "repository" {
				if _, valid := inventoryHash(reference.ArchiveHash); !valid {
					return inventoryDescription{}, errInventoryInvalid
				}
				archives[reference.ArchiveHash] = struct{}{}
			}
		case "skill", "pack_item":
		default:
			return inventoryDescription{}, errInventoryInvalid
		}
		records++
		tail = remaining
	}
	if records != references.records || uint64(len(owners)) != uint64(metadata.Inventory.Owners) || len(archives) != len(header.Archives) {
		return inventoryDescription{}, errInventoryInvalid
	}
	closure := sha256.New()
	_, _ = closure.Write([]byte("cognate/f4/content-closure/v1\x00"))
	_, _ = closure.Write(databaseDigest[:])
	_, _ = closure.Write(references.digest[:])
	contentBytes := metadata.Database.Bytes + metadata.Inventory.Bytes
	previous := ""
	for _, archive := range header.Archives {
		if err := ctx.Err(); err != nil {
			return inventoryDescription{}, err
		}
		digest, valid := inventoryHash(archive.SHA256)
		if !valid || archive.SHA256 <= previous || archive.Bytes <= 0 {
			return inventoryDescription{}, errInventoryInvalid
		}
		if _, present := archives[archive.SHA256]; !present {
			return inventoryDescription{}, errInventoryInvalid
		}
		if archive.Bytes > limits.contentBytes-contentBytes {
			return inventoryDescription{}, errInventoryBudget
		}
		contentBytes += archive.Bytes
		previous = archive.SHA256
		_, _ = closure.Write(digest[:])
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(archive.Bytes))
		_, _ = closure.Write(length[:])
	}
	if [32]byte(closure.Sum(nil)) != closureDigest {
		return inventoryDescription{}, errInventoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return inventoryDescription{}, err
	}
	return inventoryDescription{archives: header.Archives, contentBytes: contentBytes}, nil
}

func inventoryReadArchives(ctx context.Context, encoded jsontext.Value, maximum uint32) ([]inventoryArchive, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	opening, err := decoder.ReadToken()
	if err != nil || opening.Kind() != '[' {
		return nil, errInventoryInvalid
	}
	archives := make([]inventoryArchive, 0, min(maximum, 128))
	for decoder.PeekKind() != ']' {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if uint64(len(archives)) >= uint64(maximum) {
			return nil, errInventoryBudget
		}
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, errInventoryInvalid
		}
		var archive inventoryArchive
		if err := json.Unmarshal(value, &archive, json.RejectUnknownMembers(true)); err != nil {
			return nil, errInventoryInvalid
		}
		archives = append(archives, archive)
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, errInventoryInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errInventoryInvalid
	}
	return archives, nil
}

func inventoryHash(encoded string) (digest [32]byte, valid bool) {
	if len(encoded) != 64 {
		return digest, false
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || hex.EncodeToString(decoded) != encoded {
		return digest, false
	}
	copy(digest[:], decoded)
	return digest, true
}

type inventoryReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader inventoryReader) Read(output []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := reader.source.Read(output)
	if canceled := reader.ctx.Err(); canceled != nil {
		return count, canceled
	}
	return count, err
}
