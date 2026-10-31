package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/backup"
)

func inventoryFixture(test *testing.T) ([]byte, backup.SealMetadata, inventoryReferences, inventoryLimits) {
	test.Helper()
	references := []byte("{\"kind\":\"repository\",\"id\":1,\"owner\":7,\"archive_hash\":\"" + strings.Repeat("3", 64) + "\"}\n" +
		"{\"kind\":\"repository\",\"id\":2,\"owner\":9,\"archive_hash\":\"" + strings.Repeat("4", 64) + "\"}\n" +
		"{\"kind\":\"skill\",\"id\":1,\"repository\":1}\n" +
		"{\"kind\":\"skill\",\"id\":2,\"repository\":2}\n" +
		"{\"kind\":\"pack\",\"id\":1,\"owner\":7}\n" +
		"{\"kind\":\"pack_item\",\"id\":1,\"pack\":1,\"skill\":1}\n")
	header := inventoryHeader{Format: "cognate-content-inventory", Version: 1, SnapshotID: strings.Repeat("1", 32), References: 6, Archives: []inventoryArchive{{SHA256: strings.Repeat("3", 64), Bytes: 17}, {SHA256: strings.Repeat("4", 64), Bytes: 29}}}
	encoded, err := json.Marshal(header)
	if err != nil {
		test.Fatal(err)
	}
	payload := append(append(encoded, '\n'), references...)
	digest := sha256.Sum256(payload)
	metadata := backup.SealMetadata{CutID: header.SnapshotID, Database: backup.SealFileDescription{Bytes: 4096, SHA256: strings.Repeat("2", 64)}, Inventory: backup.SealInventoryDescription{Bytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]), Records: 7, Owners: 2}, Archives: 2, ClosureSHA256: "a05c419b164a0bafe0e39a3fb99d763020421941663df73cb5cf3eab6010d3a0"}
	return payload, metadata, inventoryReferences{bytes: int64(len(references)), digest: sha256.Sum256(references), records: 6}, inventoryLimits{bytes: 1 << 20, archives: 100, contentBytes: 2 << 20}
}

func TestInventoryExistingFormat(test *testing.T) {
	payload, metadata, references, limits := inventoryFixture(test)
	description, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, references, limits)
	if err != nil {
		test.Fatal(err)
	}
	if len(description.archives) != 2 || description.archives[0].SHA256 != strings.Repeat("3", 64) || description.archives[1].Bytes != 29 || description.contentBytes != 4096+int64(len(payload))+17+29 {
		test.Fatal("inventory contract changed")
	}
}

func inventoryRetag(payload []byte, metadata *backup.SealMetadata) {
	digest := sha256.Sum256(payload)
	metadata.Inventory.Bytes = int64(len(payload))
	metadata.Inventory.SHA256 = hex.EncodeToString(digest[:])
}

func inventoryRewriteHeader(test *testing.T, payload *[]byte, metadata *backup.SealMetadata, references inventoryReferences, change func(*inventoryHeader)) {
	test.Helper()
	encoded, tail, _ := bytes.Cut(*payload, []byte{'\n'})
	var header inventoryHeader
	if err := json.Unmarshal(encoded, &header); err != nil {
		test.Fatal(err)
	}
	change(&header)
	encoded, err := json.Marshal(header)
	if err != nil {
		test.Fatal(err)
	}
	*payload = append(append(encoded, '\n'), tail...)
	inventoryRetag(*payload, metadata)
	closure := sha256.New()
	closure.Write([]byte("cognate/f4/content-closure/v1\x00"))
	database, err := hex.DecodeString(metadata.Database.SHA256)
	if err != nil {
		test.Fatal(err)
	}
	closure.Write(database)
	closure.Write(references.digest[:])
	for _, archive := range header.Archives {
		digest, err := hex.DecodeString(archive.SHA256)
		if err != nil {
			continue
		}
		closure.Write(digest)
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(archive.Bytes))
		closure.Write(length[:])
	}
	metadata.ClosureSHA256 = hex.EncodeToString(closure.Sum(nil))
}

func TestInventoryRejectsAuthenticatedContradictions(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		header func(*inventoryHeader)
	}{
		{"format", func(header *inventoryHeader) { header.Format = "other" }},
		{"version", func(header *inventoryHeader) { header.Version = 2 }},
		{"cut", func(header *inventoryHeader) { header.SnapshotID = strings.Repeat("9", 32) }},
		{"header-reference-count", func(header *inventoryHeader) { header.References++ }},
		{"empty-archives", func(header *inventoryHeader) { header.Archives = nil }},
		{"duplicate-archive", func(header *inventoryHeader) { header.Archives[1] = header.Archives[0] }},
		{"unordered-archives", func(header *inventoryHeader) {
			header.Archives[0], header.Archives[1] = header.Archives[1], header.Archives[0]
		}},
		{"unknown-archive", func(header *inventoryHeader) { header.Archives[1].SHA256 = strings.Repeat("5", 64) }},
		{"missing-archive", func(header *inventoryHeader) { header.Archives = header.Archives[:1] }},
		{"zero-archive-size", func(header *inventoryHeader) { header.Archives[0].Bytes = 0 }},
		{"negative-archive-size", func(header *inventoryHeader) { header.Archives[0].Bytes = -1 }},
		{"archive-size-overflow", func(header *inventoryHeader) { header.Archives[0].Bytes = 1<<63 - 1 }},
		{"uppercase-hash", func(header *inventoryHeader) { header.Archives[0].SHA256 = strings.Repeat("A", 64) }},
		{"short-hash", func(header *inventoryHeader) { header.Archives[0].SHA256 = "33" }},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			payload, metadata, references, limits := inventoryFixture(test)
			inventoryRewriteHeader(test, &payload, &metadata, references, scenario.header)
			description, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, references, limits)
			if err == nil || !reflect.ValueOf(description).IsZero() {
				test.Fatal("authenticated contradictory header admitted")
			}
		})
	}
	for _, scenario := range []struct {
		name   string
		change func(*backup.SealMetadata, *inventoryReferences, *inventoryLimits)
	}{
		{"inventory-hash", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Inventory.SHA256 = strings.Repeat("0", 64)
		}},
		{"inventory-size", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Inventory.Bytes--
		}},
		{"inventory-records", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Inventory.Records++
		}},
		{"inventory-owners", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Inventory.Owners++
		}},
		{"archive-count", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) { metadata.Archives++ }},
		{"reference-hash", func(_ *backup.SealMetadata, references *inventoryReferences, _ *inventoryLimits) {
			references.digest[0]++
		}},
		{"reference-size", func(_ *backup.SealMetadata, references *inventoryReferences, _ *inventoryLimits) { references.bytes-- }},
		{"reference-count", func(_ *backup.SealMetadata, references *inventoryReferences, _ *inventoryLimits) {
			references.records++
		}},
		{"closure", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.ClosureSHA256 = strings.Repeat("0", 64)
		}},
		{"database-hash", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Database.SHA256 = strings.Repeat("8", 64)
		}},
		{"inventory-budget", func(metadata *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) {
			limits.bytes = metadata.Inventory.Bytes - 1
		}},
		{"archive-budget", func(_ *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) { limits.archives = 1 }},
		{"content-budget", func(metadata *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) {
			limits.contentBytes = metadata.Database.Bytes + metadata.Inventory.Bytes + 45
		}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			payload, metadata, references, limits := inventoryFixture(test)
			scenario.change(&metadata, &references, &limits)
			description, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, references, limits)
			if err == nil || !reflect.ValueOf(description).IsZero() {
				test.Fatal("contradictory metadata or budget admitted")
			}
		})
	}
}

func TestInventoryStrictHeader(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(string) string
	}{
		{"duplicate", func(header string) string {
			return strings.Replace(header, "\"version\":1", "\"version\":1,\"version\":1", 1)
		}},
		{"unknown", func(header string) string {
			return strings.Replace(header, "\"version\":1", "\"version\":1,\"future\":true", 1)
		}},
		{"missing", func(header string) string { return strings.Replace(header, "\"references\":6,", "", 1) }},
		{"null", func(header string) string {
			return strings.Replace(header, "\"references\":6", "\"references\":null", 1)
		}},
		{"multiple-values", func(header string) string { return header + "{}" }},
		{"array", func(string) string { return "[]" }},
		{"null-archives", func(header string) string {
			prefix, _, _ := strings.Cut(header, "\"archives\":")
			return prefix + "\"archives\":null}"
		}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			payload, metadata, references, limits := inventoryFixture(test)
			header, tail, _ := bytes.Cut(payload, []byte{'\n'})
			payload = append([]byte(scenario.change(string(header))+"\n"), tail...)
			inventoryRetag(payload, &metadata)
			if description, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, references, limits); err == nil || !reflect.ValueOf(description).IsZero() {
				test.Fatal("non-strict header admitted")
			}
		})
	}
}

func TestInventoryArchiveAllocationBudget(test *testing.T) {
	payload, metadata, references, limits := inventoryFixture(test)
	inventoryRewriteHeader(test, &payload, &metadata, references, func(header *inventoryHeader) {
		header.Archives = append(header.Archives, make([]inventoryArchive, 1000)...)
	})
	if description, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, references, limits); !errors.Is(err, errInventoryBudget) || !reflect.ValueOf(description).IsZero() {
		test.Fatal("archive allocation count not bounded", err)
	}
}

func TestInventoryEmptyRequiredMembers(test *testing.T) {
	payload, metadata, _, limits := inventoryFixture(test)
	references := inventoryReferences{digest: sha256.Sum256(nil)}
	_, tail, _ := bytes.Cut(payload, []byte{'\n'})
	payload = payload[:len(payload)-len(tail)]
	metadata.Archives, metadata.Inventory.Owners, metadata.Inventory.Records = 0, 0, 1
	inventoryRewriteHeader(test, &payload, &metadata, references, func(header *inventoryHeader) {
		header.References = 0
		header.Archives = []inventoryArchive{}
	})
	if _, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, references, limits); err != nil {
		test.Fatal("empty inventory control refused", err)
	}
	for _, member := range []string{"\"references\":0,", ",\"archives\":[]"} {
		test.Run(member, func(test *testing.T) {
			changed := bytes.Replace(payload, []byte(member), nil, 1)
			if bytes.Equal(changed, payload) {
				test.Fatal("missing-member fixture unchanged")
			}
			changedMetadata := metadata
			inventoryRetag(changed, &changedMetadata)
			if description, err := readInventory(test.Context(), bytes.NewReader(changed), changedMetadata, references, limits); err == nil || !reflect.ValueOf(description).IsZero() {
				test.Fatal("empty required member silently defaulted")
			}
		})
	}
}

func TestInventoryRejectsBeforeReading(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*backup.SealMetadata, *inventoryReferences, *inventoryLimits)
		want   error
	}{
		{"zero-inventory-budget", func(_ *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) { limits.bytes = 0 }, errInventoryInvalid},
		{"excessive-inventory-budget", func(_ *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) {
			limits.bytes = 64<<20 + 1
		}, errInventoryInvalid},
		{"zero-archive-budget", func(_ *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) { limits.archives = 0 }, errInventoryInvalid},
		{"excessive-archive-budget", func(_ *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) {
			limits.archives = 100001
		}, errInventoryInvalid},
		{"excessive-content-budget", func(_ *backup.SealMetadata, _ *inventoryReferences, limits *inventoryLimits) {
			limits.contentBytes = 1<<40 + 1
		}, errInventoryInvalid},
		{"negative-reference-size", func(_ *backup.SealMetadata, references *inventoryReferences, _ *inventoryLimits) {
			references.bytes = -1
		}, errInventoryInvalid},
		{"excessive-reference-count", func(_ *backup.SealMetadata, references *inventoryReferences, _ *inventoryLimits) {
			references.records = 1000001
		}, errInventoryInvalid},
		{"overflowing-inventory-size", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Inventory.Bytes = 1<<63 - 1
		}, errInventoryBudget},
		{"negative-database-size", func(metadata *backup.SealMetadata, _ *inventoryReferences, _ *inventoryLimits) {
			metadata.Database.Bytes = -1
		}, errInventoryInvalid},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			payload, metadata, references, limits := inventoryFixture(test)
			scenario.change(&metadata, &references, &limits)
			reader := &inventoryObservedReader{reader: bytes.NewReader(payload)}
			description, err := readInventory(test.Context(), reader, metadata, references, limits)
			if !errors.Is(err, scenario.want) || !reflect.ValueOf(description).IsZero() || reader.read != 0 || reader.closed {
				test.Fatal("invalid input read bytes or lost refusal", err)
			}
		})
	}
}

type inventoryErrorReader struct{ failure error }

func (reader inventoryErrorReader) Read([]byte) (int, error) { return 0, reader.failure }

func TestInventoryErrorPrivacy(test *testing.T) {
	payload, metadata, references, limits := inventoryFixture(test)
	sentinel := "inventory-synthetic-private-reader-detail"
	description, err := readInventory(test.Context(), inventoryErrorReader{failure: errors.New(sentinel)}, metadata, references, limits)
	if !errors.Is(err, errInventoryInvalid) || strings.Contains(err.Error(), sentinel) || !reflect.ValueOf(description).IsZero() {
		test.Fatal("source error exposed or admitted")
	}
	if _, err := readInventory(nil, bytes.NewReader(payload), metadata, references, limits); !errors.Is(err, errInventoryInvalid) {
		test.Fatal("nil context not refused")
	}
	if _, err := readInventory(test.Context(), nil, metadata, references, limits); !errors.Is(err, errInventoryInvalid) {
		test.Fatal("nil source not refused")
	}
}

type inventoryObservedReader struct {
	reader io.Reader
	read   int
	closed bool
	cancel context.CancelFunc
}

func (reader *inventoryObservedReader) Read(output []byte) (int, error) {
	count, err := reader.reader.Read(output)
	reader.read += count
	if reader.cancel != nil {
		reader.cancel()
	}
	return count, err
}

func (reader *inventoryObservedReader) Close() error {
	reader.closed = true
	return nil
}

func TestInventoryBorrowingBudgetsAndCancellation(test *testing.T) {
	for _, scenario := range []string{"borrowed", "oversize", "canceled-before", "canceled-during"} {
		test.Run(scenario, func(test *testing.T) {
			payload, metadata, references, limits := inventoryFixture(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			reader := &inventoryObservedReader{reader: bytes.NewReader(payload)}
			if scenario == "oversize" {
				reader.reader = io.MultiReader(bytes.NewReader(payload), strings.NewReader(strings.Repeat("x", 128)))
			}
			if scenario == "canceled-before" {
				cancel()
			}
			if scenario == "canceled-during" {
				reader.cancel = cancel
			}
			description, err := readInventory(ctx, reader, metadata, references, limits)
			if scenario == "borrowed" {
				if err != nil || len(description.archives) != 2 {
					test.Fatal("borrowed read failed", err)
				}
			} else if err == nil || !reflect.ValueOf(description).IsZero() {
				test.Fatal("invalid read admitted")
			}
			if strings.HasPrefix(scenario, "canceled-") && !errors.Is(err, context.Canceled) {
				test.Fatal("cancellation category lost", err)
			}
			if reader.closed || int64(reader.read) > metadata.Inventory.Bytes+1 || scenario == "canceled-before" && reader.read != 0 {
				test.Fatal("borrowed ownership or bounded read violated")
			}
		})
	}
}
