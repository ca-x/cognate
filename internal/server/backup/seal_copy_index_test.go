package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func copyIndexID(value uint64) (id [32]byte) {
	binary.BigEndian.PutUint64(id[24:], value)
	return id
}

func copyIndexRecords(count uint32, repetitions uint32) []sealRecord {
	records := make([]sealRecord, count)
	for ordinal := range count {
		record := sealRecord{ordinal: ordinal, length: 1}
		switch ordinal {
		case 0:
			record.kind = 1
		case 1:
			record.kind = 2
		default:
			record.kind = 3
			record.id = copyIndexID(uint64((ordinal-2)/repetitions+1) * 2)
			record.offset = int64((ordinal-2)%repetitions) * sealExtentLimit
			if (ordinal-2)%repetitions != repetitions-1 && ordinal+1 != count {
				record.length = sealExtentLimit
			}
		}
		records[ordinal] = record
	}
	return records
}

func TestSealCopyIndexReadBound(test *testing.T) {
	const count = uint32(100000)
	records := copyIndexRecords(count, 1)
	var order sealOrder
	for _, record := range records {
		if err := order.accept(record); err != nil {
			test.Fatal("counting vector violates production ordering", err)
		}
	}
	catalog, err := os.CreateTemp(test.TempDir(), "catalog-")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = catalog.Close() })
	writer := bufio.NewWriterSize(catalog, 64<<10)
	for _, record := range records {
		encoded := record.encode()
		if _, err := writer.Write(encoded[:]); err != nil {
			test.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		test.Fatal(err)
	}
	for _, row := range []struct {
		name  string
		kind  byte
		id    [32]byte
		first uint32
	}{
		{"database", 1, [32]byte{}, 0},
		{"inventory", 2, [32]byte{}, 1},
		{"first-archive", 3, copyIndexID(2), 2},
		{"middle-archive", 3, copyIndexID(99998), 50000},
		{"last-archive", 3, copyIndexID(199996), 99999},
		{"missing-before", 3, copyIndexID(1), 2},
		{"missing-gap", 3, copyIndexID(99999), 50001},
		{"missing-after", 3, copyIndexID(199997), count},
	} {
		test.Run(row.name, func(test *testing.T) {
			reads := 0
			first, err := sealCopyLowerBound(test.Context(), count, row.kind, row.id, func(ordinal uint32) (sealRecord, error) {
				reads++
				if ordinal >= count {
					test.Fatal("lookup read outside catalog")
				}
				return readSealRecord(catalog, int64(ordinal)*128)
			})
			legacyFirst, legacyReads := count, 0
			for ordinal := range count {
				record, err := readSealRecord(catalog, int64(ordinal)*128)
				if err != nil {
					test.Fatal(err)
				}
				legacyReads++
				if legacyFirst == count && (record.kind > row.kind || record.kind == row.kind && bytes.Compare(record.id[:], row.id[:]) >= 0) {
					legacyFirst = ordinal
				}
			}
			limit := bits.Len32(count-1) + 1
			test.Logf("PERF count=%d case=%s lookup_reads=%d legacy_reads=%d bound=%d first=%d", count, row.name, reads, legacyReads, limit, first)
			if err != nil || first != row.first || legacyFirst != row.first || reads > limit || legacyReads != int(count) {
				test.Fatalf("lookup first=%d want=%d error=%v reads=%d bound=%d legacy_first=%d", first, row.first, err, reads, limit, legacyFirst)
			}
		})
	}
}

func TestSealCopyIndexContinuousExtents(test *testing.T) {
	records := copyIndexRecords(100000, 3)
	var order sealOrder
	for _, record := range records {
		if err := order.accept(record); err != nil {
			test.Fatal(err)
		}
	}
	id := copyIndexID(33334)
	reads := 0
	readRecord := func(ordinal uint32) (sealRecord, error) {
		reads++
		return records[ordinal], nil
	}
	first, err := sealCopyLowerBound(test.Context(), uint32(len(records)), 3, id, readRecord)
	if err != nil || first != 50000 {
		test.Fatalf("duplicate lower bound=%d error=%v", first, err)
	}
	matched := 0
	for ordinal := first; ordinal < uint32(len(records)); ordinal++ {
		record, err := readRecord(ordinal)
		if err != nil {
			test.Fatal(err)
		}
		if record.kind != 3 || record.id != id {
			break
		}
		if record.offset != int64(matched)*sealExtentLimit {
			test.Fatal("lost contiguous extent order")
		}
		matched++
	}
	if matched != 3 || reads > bits.Len32(uint32(len(records)-1))+matched+2 {
		test.Fatalf("continuous extent count=%d reads=%d", matched, reads)
	}
	test.Logf("PERF count=100000 case=three-extents lookup_and_range_reads=%d matched=%d legacy_reads=100000", reads, matched)
}

func TestSealCopyIndexCancellationAndReadErrors(test *testing.T) {
	records := copyIndexRecords(100000, 1)
	for _, failure := range []string{"cancel", "read-error"} {
		for _, at := range []int{1, 8, 17} {
			test.Run(fmt.Sprintf("%s/%d", failure, at), func(test *testing.T) {
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				reads := 0
				first, err := sealCopyLowerBound(ctx, uint32(len(records)), 1, [32]byte{}, func(ordinal uint32) (sealRecord, error) {
					reads++
					if reads == at {
						if failure == "read-error" {
							return sealRecord{}, ErrInvalid
						}
						cancel()
					}
					return records[ordinal], nil
				})
				want := ErrInvalid
				if failure == "cancel" {
					want = context.Canceled
				}
				if first != 0 || !errors.Is(err, want) || reads != at {
					test.Fatalf("failure result=%d error=%v reads=%d want=%d", first, err, reads, at)
				}
			})
		}
	}
	for _, count := range []uint32{0, 100000} {
		test.Run(fmt.Sprintf("pre-cancel/%d", count), func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			cancel()
			_, err := sealCopyLowerBound(ctx, count, 1, [32]byte{}, func(uint32) (sealRecord, error) {
				test.Fatal("pre-canceled lookup read a record")
				return sealRecord{}, nil
			})
			if !errors.Is(err, context.Canceled) {
				test.Fatal(err)
			}
		})
	}
}

func TestSealCopyIndexSmallBoundaries(test *testing.T) {
	for _, count := range []uint32{0, 1, 2, 3, 31, 32, 33} {
		test.Run(fmt.Sprint(count), func(test *testing.T) {
			records := copyIndexRecords(count, 1)
			reads := 0
			first, err := sealCopyLowerBound(test.Context(), count, 3, copyIndexID(2), func(ordinal uint32) (sealRecord, error) {
				reads++
				if ordinal >= count {
					test.Fatal("small lookup exceeded count")
				}
				return records[ordinal], nil
			})
			if err != nil || first != min(count, 2) || reads > bits.Len32(count)+1 || count == 0 && reads != 0 {
				test.Fatalf("count=%d first=%d reads=%d error=%v", count, first, reads, err)
			}
		})
	}
}

type copyIndexPlain struct {
	kind, id string
	payload  []byte
}

func copyIndexBundle(test *testing.T) (SealEngine, CryptoVerifiedBundle, []copyIndexPlain) {
	test.Helper()
	engine, attempt, bootstrap := sourceAttempt(test)
	archives := []copyIndexPlain{{kind: "archive", payload: []byte("copy index archive first")}, {kind: "archive", payload: []byte("copy index archive second")}, {kind: "archive", payload: []byte("copy index archive third")}}
	for index := range archives {
		digest := sha256.Sum256(archives[index].payload)
		archives[index].id = hex.EncodeToString(digest[:])
	}
	slices.SortFunc(archives, func(left, right copyIndexPlain) int { return strings.Compare(left.id, right.id) })
	inputs := append([]copyIndexPlain{{kind: "database", id: "database", payload: bytes.Repeat([]byte("copy index database"), 8192)}, {kind: "inventory", id: "inventory", payload: []byte("copy index inventory")}}, archives...)
	var artifacts []SealedFile
	var records []SealExtentRecord
	for ordinal, input := range inputs {
		digest := sha256.Sum256(input.payload)
		artifact, err := engine.SealExtentSource(test.Context(), attempt, uint32(ordinal), int64(len(input.payload)), digest, func(ctx context.Context, output io.Writer) error {
			_, err := output.Write(input.payload)
			return err
		})
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { _ = artifact.Close() })
		artifacts = append(artifacts, artifact)
		ref := artifact.Ref().read()
		records = append(records, SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: input.kind, FileID: input.id, PlaintextBytes: int64(len(input.payload)), PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
	}
	page, err := engine.SealPage(test.Context(), attempt, 1, uint32(len(records)), func(yield func(SealExtentRecord, error) bool) {
		for _, record := range records {
			if !yield(record, nil) {
				return
			}
		}
	})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = page.Close() })
	databaseHash, inventoryHash := sha256.Sum256(inputs[0].payload), sha256.Sum256(inputs[1].payload)
	databaseHex, inventoryHex := hex.EncodeToString(databaseHash[:]), hex.EncodeToString(inventoryHash[:])
	root, err := engine.SealRoot(test.Context(), attempt, SealMetadata{AppVersion: "copy-index-test", SchemaSHA256: databaseHex, CutID: "01000000000000000000000000000000", MasterKeyID: strings.Repeat("A", 43), Database: SealFileDescription{Bytes: int64(len(inputs[0].payload)), SHA256: databaseHex, Extents: 1}, Inventory: SealInventoryDescription{Bytes: int64(len(inputs[1].payload)), SHA256: inventoryHex, Extents: 1, Records: 3, Owners: 1}, Archives: uint32(len(archives)), ClosureSHA256: inventoryHex, ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = root.Close() })
	complete, err := engine.SealComplete(test.Context(), attempt)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = complete.Close() })
	var session OpenSession
	err = bootstrap.WithFile(test.Context(), func(first *os.File) error {
		return complete.WithFile(test.Context(), func(last *os.File) error {
			var err error
			session, err = engine.BeginOpen(test.Context(), first, last, Attempt{read: func() attemptData { return attempt.get().identity }})
			return err
		})
	})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = session.Close() })
	for _, artifact := range append([]SealedFile{root, page}, artifacts...) {
		ref, err := session.NextRef(test.Context())
		if err != nil || ref.read == nil || ref.read() != artifact.Ref().read() {
			test.Fatal("authenticated next ref mismatch", err)
		}
		if err := artifact.WithFile(test.Context(), func(input *os.File) error { return session.AcceptFile(test.Context(), input) }); err != nil {
			test.Fatal(err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = bundle.Close() })
	return engine, bundle, inputs
}

func TestSealCopyIndexAuthenticatedCompatibility(test *testing.T) {
	_, bundle, inputs := copyIndexBundle(test)
	for _, index := range []int{4, 0, 3, 1, 2, 4, 2} {
		input := inputs[index]
		var output bytes.Buffer
		count, err := bundle.CopyFile(test.Context(), input.kind, input.id, &output)
		if err != nil || count != int64(len(input.payload)) || !bytes.Equal(output.Bytes(), input.payload) {
			test.Fatalf("authenticated CopyFile kind=%s count=%d error=%v", input.kind, count, err)
		}
	}
	missing := sha256.Sum256([]byte("missing authenticated archive"))
	var output bytes.Buffer
	count, err := bundle.CopyFile(test.Context(), "archive", hex.EncodeToString(missing[:]), &output)
	if !errors.Is(err, ErrInvalid) || count != 0 || output.Len() != 0 {
		test.Fatal("missing file was exposed", count, err)
	}
	test.Log("actual SealExtentSource/SealPage/SealRoot/SealComplete -> BeginOpen/AcceptFile/Finish -> CopyFile: database, inventory, first/middle/last archive and repeated out-of-order copies match original bytes")
}

func TestSealCopyIndexAuthenticatedFailures(test *testing.T) {
	for _, variant := range []string{"plain-hash", "plain-length", "catalog-read", "writer-error", "writer-short", "physical-close", "cancel-and-close"} {
		test.Run(variant, func(test *testing.T) {
			engine, bundle, inputs := copyIndexBundle(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			state := bundle.get()
			input := inputs[2]
			if variant == "plain-hash" || variant == "plain-length" || variant == "catalog-read" {
				name := filepath.Join(engine.get().directory.root.Name(), fmt.Sprintf("%sp-1-%08x", state.prefix, 2))
				if variant == "catalog-read" {
					name = state.catalog.Name()
				}
				file, err := os.OpenFile(name, os.O_WRONLY, 0)
				if err != nil {
					test.Fatal(err)
				}
				if variant == "plain-hash" {
					_, err = file.WriteAt([]byte{input.payload[0] ^ 0xff}, 0)
				} else {
					size := int64(len(input.payload) - 1)
					if variant == "catalog-read" {
						size = 0
					}
					err = file.Truncate(size)
				}
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					test.Fatal(err, closeErr)
				}
			}
			originalClose := engine.get().ops.close
			physicalCloses := 0
			if variant == "physical-close" || variant == "cancel-and-close" {
				engine.get().ops.close = func(file *os.File) error {
					physicalCloses++
					if err := originalClose(file); err != nil {
						return err
					}
					if variant == "cancel-and-close" {
						cancel()
					}
					return errors.New("PRIVATE-COPY-CLOSE")
				}
				defer func() { engine.get().ops.close = originalClose }()
			}
			output := &copyCandidateWriter{}
			if strings.HasPrefix(variant, "writer-") {
				output.onWrite = func(buffer []byte) (int, error) {
					count, _ := output.Buffer.Write(buffer[:len(buffer)/2])
					if variant == "writer-short" {
						return count, nil
					}
					return count, errors.New("PRIVATE-COPY-WRITER")
				}
			}
			count, err := bundle.CopyFile(ctx, input.kind, input.id, output)
			want := ErrInvalid
			if variant == "physical-close" || variant == "cancel-and-close" {
				want = ErrSealUncertain
				if physicalCloses != 1 {
					test.Fatal("physical extent Close was not checked exactly once")
				}
			}
			if count != 0 || !errors.Is(err, want) || output.closed || strings.Contains(fmt.Sprintf("%+v", err), "PRIVATE-") {
				test.Fatalf("failure count=%d error=%v writer_closed=%t", count, err, output.closed)
			}
		})
	}
}

func TestSealCopyIndexAuthenticatedCloseDrain(test *testing.T) {
	for _, scope := range []string{"caller", "bundle", "engine"} {
		test.Run(scope, func(test *testing.T) {
			engine, bundle, inputs := copyIndexBundle(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			releaseWriter := sync.OnceFunc(func() { close(release) })
			defer releaseWriter()
			output := &copyCandidateWriter{}
			firstWrite := sync.OnceFunc(func() { close(entered); <-release })
			output.onWrite = func(buffer []byte) (int, error) {
				count, err := output.Buffer.Write(buffer)
				firstWrite()
				return count, err
			}
			done := make(chan error, 1)
			go func() {
				count, err := bundle.CopyFile(ctx, "database", "database", output)
				if count != 0 {
					err = ErrInvalid
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				test.Fatal("real CopyFile did not borrow writer")
			}
			closed := make(chan error, 1)
			cancelled := ctx.Done()
			switch scope {
			case "caller":
				cancel()
			case "bundle":
				cancelled = bundle.get().ctx.Done()
				go func() { closed <- bundle.Close() }()
			case "engine":
				cancelled = engine.get().ctx.Done()
				go func() { closed <- engine.Close() }()
			}
			select {
			case <-cancelled:
			case <-time.After(5 * time.Second):
				test.Fatal("CopyFile scope did not cancel")
			}
			select {
			case <-done:
				test.Fatal("CopyFile returned before borrowed writer")
			case <-closed:
				test.Fatal("Close returned before active CopyFile joined")
			default:
			}
			if _, err := bundle.get().catalog.Stat(); err != nil {
				test.Fatal("catalog closed while CopyFile active")
			}
			releaseWriter()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					test.Fatal("CopyFile cancellation lost", err)
				}
			case <-time.After(5 * time.Second):
				test.Fatal("CopyFile did not drain")
			}
			if scope != "caller" {
				select {
				case err := <-closed:
					if err != nil {
						test.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					test.Fatal("Close did not finish after CopyFile")
				}
			}
			if output.closed || output.Len() == 0 || output.Len() >= len(inputs[0].payload) {
				test.Fatal("cancellation did not leave a partial borrowed output")
			}
		})
	}
}
