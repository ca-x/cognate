package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func sourceAdmissionPlatform(test *testing.T) {
	test.Helper()
	if errors.Is(maintenance.CheckPlatform(test.Context()), maintenance.ErrUnsupported) {
		test.Skip("physical source inspection requires supported platform")
	}
}

func sourceAdmissionSeed(test *testing.T, pageSize int, shm bool) string {
	test.Helper()
	sourceAdmissionPlatform(test)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	writer := filepath.Join(directory, "writer.sqlite")
	pool, err := sql.Open("sqlite3", writer)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = pool.Close() })
	pool.SetMaxOpenConns(1)
	for _, statement := range []string{
		fmt.Sprintf("PRAGMA page_size=%d", pageSize),
		"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "PRAGMA synchronous=OFF",
		"CREATE TABLE payload (id INTEGER PRIMARY KEY, value INTEGER NOT NULL, secret TEXT NOT NULL)",
		"INSERT INTO payload VALUES (1,1,'synthetic-local-source-secret')", "PRAGMA wal_checkpoint(TRUNCATE)",
		"UPDATE payload SET value=value+1 WHERE id=1",
	} {
		if _, err := pool.ExecContext(test.Context(), statement); err != nil {
			test.Fatal(err)
		}
	}
	path := filepath.Join(directory, "source.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if suffix == "-shm" && !shm {
			continue
		}
		data, err := os.ReadFile(writer + suffix)
		if err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path+suffix, data, 0o600); err != nil {
			test.Fatal(err)
		}
	}
	if err := pool.Close(); err != nil {
		test.Fatal(err)
	}
	main, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	actual := int(binary.BigEndian.Uint16(main[16:18]))
	if actual == 1 {
		actual = 65536
	}
	if actual != pageSize {
		test.Fatalf("fixture page size got %d want %d", actual, pageSize)
	}
	return path
}

func sourceAdmissionBorrow(test *testing.T, path string) (*os.File, *os.File) {
	test.Helper()
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = parent.Close() })
	database, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close() })
	return parent, database
}

func sourceAdmissionHashes(test *testing.T, path string) [3][32]byte {
	test.Helper()
	var hashes [3][32]byte
	for index, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			test.Fatal(err)
		}
		hashes[index] = sha256.Sum256(data)
	}
	return hashes
}

func TestLocalSnapshotSourceGeometry(test *testing.T) {
	for _, pageSize := range []int{512, 4096, 8192, 65536} {
		for _, shm := range []bool{false, true} {
			test.Run(fmt.Sprintf("P%d/SHM%t", pageSize, shm), func(test *testing.T) {
				path := sourceAdmissionSeed(test, pageSize, shm)
				before := sourceAdmissionHashes(test, path)
				parent, database := sourceAdmissionBorrow(test, path)
				result, err := inspectLocalSnapshotSource(test.Context(), parent, database, snapshotSourcePrefixBudget)
				if err != nil {
					test.Fatal(err)
				}
				if result.decision != snapshotSourceNeedsCertificate || result.pageSize != uint64(pageSize) || result.frames != 1 || result.regions != 1 || result.mappingLower != 32768 {
					test.Fatal("wrong geometry or certificate classification")
				}
				if shm && result.shmBytes != 32768 || !shm && result.shmBytes != 0 {
					test.Fatal("wrong SHM geometry")
				}
				if after := sourceAdmissionHashes(test, path); after != before {
					test.Fatal("source mutated")
				}
				if _, err := parent.Stat(); err != nil {
					test.Fatal("borrowed parent closed")
				}
				if _, err := database.Stat(); err != nil {
					test.Fatal("borrowed database closed")
				}
			})
		}
	}
}

func TestLocalSnapshotSourceCancelAndPlatform(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	result, err := inspectLocalSnapshotSource(ctx, nil, nil, snapshotSourcePrefixBudget)
	if !errors.Is(err, context.Canceled) || result != (localSnapshotSourceObservation{}) {
		test.Fatal("cancel must withhold result")
	}
	if errors.Is(maintenance.CheckPlatform(test.Context()), maintenance.ErrUnsupported) {
		result, err = inspectLocalSnapshotSource(test.Context(), nil, nil, 1)
		if !errors.Is(err, maintenance.ErrUnsupported) || result != (localSnapshotSourceObservation{}) {
			test.Fatal("unsupported must precede I/O")
		}
	}
}

func TestLocalSnapshotSourceR553PreEngine(test *testing.T) {
	path := sourceAdmissionSeed(test, 512, false)
	file, err := os.OpenFile(path+"-wal", os.O_WRONLY, 0)
	if err != nil {
		test.Fatal(err)
	}
	frames := int64(552*4096 - 33)
	if err := errors.Join(file.Truncate(32+frames*536), file.Close()); err != nil {
		test.Fatal(err)
	}
	parent, database := sourceAdmissionBorrow(test, path)
	connections, backups := 0, 0
	result, err := inspectLocalSnapshotSource(test.Context(), parent, database, snapshotSourcePrefixBudget)
	if err == nil {
		connections++
		backups++
	}
	if !errors.Is(err, errSnapshotSourceBudget) || result != (localSnapshotSourceObservation{}) || connections != 0 || backups != 0 {
		test.Fatal("R553 entered source engine or failed for wrong reason", err)
	}
	test.Log("R553 refused: source Conn=0 Backup=0; fixture engine already closed")
}

func TestLocalSnapshotSourceReadBudget(test *testing.T) {
	path := sourceAdmissionSeed(test, 4096, true)
	parent, database := sourceAdmissionBorrow(test, path)
	for _, budget := range []uint64{0, 99, 100, 131, 132, snapshotSourcePrefixBudget - 1, snapshotSourcePrefixBudget + 1} {
		result, err := inspectLocalSnapshotSource(test.Context(), parent, database, budget)
		if err == nil || result != (localSnapshotSourceObservation{}) {
			test.Fatalf("budget %d was ignored", budget)
		}
	}
	result, err := inspectLocalSnapshotSource(test.Context(), parent, database, snapshotSourcePrefixBudget)
	if err != nil || result.decision != snapshotSourceNeedsCertificate {
		test.Fatal("exact read budget refused", err)
	}
}

func TestLocalSnapshotSourceMalformed(test *testing.T) {
	for _, kind := range []string{"signature", "page-size", "main-length", "wal-magic", "wal-checksum", "wal-length", "shm-checksum", "shm-salt", "journal"} {
		test.Run(kind, func(test *testing.T) {
			path := sourceAdmissionSeed(test, 4096, true)
			target, offset := path, 0
			switch kind {
			case "page-size":
				offset = 16
			case "main-length":
				if err := os.Truncate(path, 101); err != nil {
					test.Fatal(err)
				}
			case "wal-magic":
				target = path + "-wal"
			case "wal-checksum":
				target, offset = path+"-wal", 24
			case "wal-length":
				if err := os.Truncate(path+"-wal", 33); err != nil {
					test.Fatal(err)
				}
			case "shm-checksum":
				target, offset = path+"-shm", 40
			case "shm-salt":
				target, offset = path+"-shm", 32
			case "journal":
				if err := os.WriteFile(path+"-journal", nil, 0o600); err != nil {
					test.Fatal(err)
				}
			}
			if kind != "main-length" && kind != "wal-length" && kind != "journal" {
				data, err := os.ReadFile(target)
				if err != nil {
					test.Fatal(err)
				}
				data[offset] ^= 0xff
				if err := os.WriteFile(target, data, 0o600); err != nil {
					test.Fatal(err)
				}
			}
			parent, database := sourceAdmissionBorrow(test, path)
			result, err := inspectLocalSnapshotSource(test.Context(), parent, database, snapshotSourcePrefixBudget)
			if err == nil || result != (localSnapshotSourceObservation{}) {
				test.Fatal("bad geometry accepted")
			}
		})
	}
}

func TestLocalSnapshotSourcePhysicalTrust(test *testing.T) {
	for _, kind := range []string{"main-symlink", "wal-symlink", "shm-symlink", "main-hardlink", "wal-hardlink", "shm-hardlink", "main-mode", "wal-mode", "shm-mode", "parent-mode", "parent-alias", "borrowed-mismatch", "main-fifo", "wal-fifo", "shm-fifo"} {
		test.Run(kind, func(test *testing.T) {
			path := sourceAdmissionSeed(test, 4096, true)
			parent, database := sourceAdmissionBorrow(test, path)
			target := path
			if strings.HasPrefix(kind, "wal-") {
				target += "-wal"
			}
			if strings.HasPrefix(kind, "shm-") {
				target += "-shm"
			}
			switch {
			case strings.HasSuffix(kind, "-symlink"):
				if err := os.Rename(target, target+".original"); err != nil {
					test.Fatal(err)
				}
				if err := os.Symlink(target+".original", target); err != nil {
					test.Fatal(err)
				}
			case strings.HasSuffix(kind, "-hardlink"):
				if err := os.Link(target, target+".alias"); err != nil {
					test.Fatal(err)
				}
			case strings.HasSuffix(kind, "-fifo"):
				tool, err := exec.LookPath("mkfifo")
				if err != nil {
					test.Skip("mkfifo unavailable")
				}
				if err := os.Remove(target); err != nil {
					test.Fatal(err)
				}
				if err := exec.CommandContext(test.Context(), tool, "-m", "600", target).Run(); err != nil {
					test.Fatal(err)
				}
			case kind == "parent-mode":
				if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
					test.Fatal(err)
				}
			case kind == "parent-alias":
				alias := filepath.Join(test.TempDir(), "alias")
				if err := os.Symlink(filepath.Dir(path), alias); err != nil {
					test.Fatal(err)
				}
				aliasParent, err := os.Open(alias)
				if err != nil {
					test.Fatal(err)
				}
				parent = aliasParent
				test.Cleanup(func() { _ = parent.Close() })
			case kind == "borrowed-mismatch":
				if err := os.Rename(path, path+".previous"); err != nil {
					test.Fatal(err)
				}
				data, err := os.ReadFile(path + ".previous")
				if err != nil {
					test.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					test.Fatal(err)
				}
			default:
				if err := os.Chmod(target, 0o644); err != nil {
					test.Fatal(err)
				}
			}
			reads := 0
			operations := localSnapshotSourceIO{readAt: func(file *os.File, data []byte, offset int64) (int, error) { reads++; return file.ReadAt(data, offset) }, closeFile: (*os.File).Close}
			result, err := inspectLocalSnapshotSourceWithIO(test.Context(), parent, database, snapshotSourcePrefixBudget, operations)
			if !errors.Is(err, errSnapshotSourceInvalid) || result != (localSnapshotSourceObservation{}) || reads != 0 {
				test.Fatal("physical trust failure did not precede reads", err)
			}
		})
	}
}

func TestLocalSnapshotSourceIdentityDuringRead(test *testing.T) {
	for _, kind := range []string{"main-replace", "wal-replace", "shm-replace", "main-truncate", "wal-truncate", "shm-truncate", "wal-mode", "shm-link", "parent-replace", "late-journal", "absent-wal", "absent-shm"} {
		test.Run(kind, func(test *testing.T) {
			path := sourceAdmissionSeed(test, 4096, true)
			if kind == "absent-wal" || kind == "absent-shm" {
				if err := os.Remove(path + "-shm"); err != nil {
					test.Fatal(err)
				}
				if kind == "absent-wal" {
					if err := os.Remove(path + "-wal"); err != nil {
						test.Fatal(err)
					}
				}
			}
			parent, database := sourceAdmissionBorrow(test, path)
			mutated := false
			operations := localSnapshotSourceIO{closeFile: (*os.File).Close}
			operations.readAt = func(file *os.File, data []byte, offset int64) (int, error) {
				count, err := file.ReadAt(data, offset)
				if mutated {
					return count, err
				}
				mutated = true
				target := path
				if strings.HasPrefix(kind, "wal-") {
					target += "-wal"
				}
				if strings.HasPrefix(kind, "shm-") {
					target += "-shm"
				}
				switch {
				case kind == "parent-replace":
					directory := filepath.Dir(path)
					if err := os.Rename(directory, directory+".moved"); err != nil {
						test.Fatal(err)
					}
					test.Cleanup(func() { _ = os.RemoveAll(directory + ".moved") })
					if err := os.Mkdir(directory, 0o700); err != nil {
						test.Fatal(err)
					}
				case strings.HasSuffix(kind, "-replace"):
					content, err := os.ReadFile(target)
					if err != nil {
						test.Fatal(err)
					}
					if err := os.Rename(target, target+".old"); err != nil {
						test.Fatal(err)
					}
					if err := os.WriteFile(target, content, 0o600); err != nil {
						test.Fatal(err)
					}
				case strings.HasSuffix(kind, "-truncate"):
					if err := os.Truncate(target, 0); err != nil {
						test.Fatal(err)
					}
				case kind == "wal-mode":
					if err := os.Chmod(target, 0o644); err != nil {
						test.Fatal(err)
					}
				case kind == "shm-link":
					if err := os.Link(target, target+".alias"); err != nil {
						test.Fatal(err)
					}
				case kind == "late-journal":
					if err := os.WriteFile(path+"-journal", nil, 0o600); err != nil {
						test.Fatal(err)
					}
				case kind == "absent-wal":
					if err := os.WriteFile(path+"-wal", nil, 0o600); err != nil {
						test.Fatal(err)
					}
				case kind == "absent-shm":
					if err := os.WriteFile(path+"-shm", nil, 0o600); err != nil {
						test.Fatal(err)
					}
				}
				return count, err
			}
			result, err := inspectLocalSnapshotSourceWithIO(test.Context(), parent, database, snapshotSourcePrefixBudget, operations)
			if err == nil || result != (localSnapshotSourceObservation{}) || !mutated {
				test.Fatal("changed source identity returned geometry")
			}
		})
	}
}

func TestLocalSnapshotSourceFaultsAndLifetime(test *testing.T) {
	for _, kind := range []string{"read1", "read2", "read3", "short1", "short2", "short3", "close1", "close2", "close3", "close4", "close5", "close6", "cancel-read1", "cancel-read2", "cancel-read3", "cancel-close6"} {
		test.Run(kind, func(test *testing.T) {
			path := sourceAdmissionSeed(test, 4096, true)
			parent, database := sourceAdmissionBorrow(test, path)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			reads, closes := 0, 0
			closed := map[*os.File]bool{}
			secretError := errors.New("synthetic-sensitive-source-error")
			operations := localSnapshotSourceIO{}
			operations.readAt = func(file *os.File, data []byte, offset int64) (int, error) {
				reads++
				count, err := file.ReadAt(data, offset)
				if kind == fmt.Sprintf("read%d", reads) {
					return count, secretError
				}
				if kind == fmt.Sprintf("short%d", reads) {
					return count - 1, nil
				}
				if kind == fmt.Sprintf("cancel-read%d", reads) {
					cancel()
				}
				return count, err
			}
			operations.closeFile = func(file *os.File) error {
				if closed[file] {
					test.Error("duplicate close")
				}
				closed[file] = true
				closes++
				err := file.Close()
				if kind == fmt.Sprintf("cancel-close%d", closes) {
					cancel()
				}
				if kind == fmt.Sprintf("close%d", closes) {
					return errors.Join(err, secretError)
				}
				return err
			}
			result, err := inspectLocalSnapshotSourceWithIO(ctx, parent, database, snapshotSourcePrefixBudget, operations)
			if err == nil || result != (localSnapshotSourceObservation{}) {
				test.Fatal("fault result was not withheld")
			}
			if strings.Contains(err.Error(), secretError.Error()) {
				test.Fatal("private I/O error escaped")
			}
			if strings.HasPrefix(kind, "close") && !errors.Is(err, errSnapshotSourceCleanup) {
				test.Fatal("unchecked Close")
			}
			if strings.HasPrefix(kind, "cancel-") && !errors.Is(err, context.Canceled) {
				test.Fatal("lost cancellation")
			}
			for file := range closed {
				if _, err := file.Stat(); err == nil {
					test.Fatal("descriptor left open")
				}
			}
			if closes < 3 {
				test.Fatal("acquired inputs not joined")
			}
			if _, err := database.Stat(); err != nil {
				test.Fatal("borrowed source closed")
			}
			if _, err := parent.Stat(); err != nil {
				test.Fatal("borrowed root closed")
			}
		})
	}
}

func TestLocalSnapshotSourceFixedReadsAndFDs(test *testing.T) {
	path := sourceAdmissionSeed(test, 4096, true)
	parent, database := sourceAdmissionBorrow(test, path)
	baseline, fdError := os.ReadDir("/proc/self/fd")
	for range 12 {
		bytesRead, closes := 0, 0
		operations := localSnapshotSourceIO{readAt: func(file *os.File, data []byte, offset int64) (int, error) {
			if offset != 0 || len(data) != 100 && len(data) != 32 && len(data) != 136 {
				test.Error("unbounded source read")
			}
			bytesRead += len(data)
			return file.ReadAt(data, offset)
		}, closeFile: func(file *os.File) error {
			closes++
			return file.Close()
		}}
		result, err := inspectLocalSnapshotSourceWithIO(test.Context(), parent, database, snapshotSourcePrefixBudget, operations)
		if err != nil || result.decision != snapshotSourceNeedsCertificate || bytesRead != 268 || closes != 6 {
			test.Fatal("bounded ownership mismatch", bytesRead, closes, err)
		}
		if fdError == nil {
			current, err := os.ReadDir("/proc/self/fd")
			if err != nil || len(current) != len(baseline) {
				test.Fatal("FD leak", len(baseline), len(current))
			}
		}
	}
	if fdError != nil {
		test.Log("/proc FD census unavailable; per-file Close checks still executed")
	}
}

func TestLocalSnapshotSourceArithmetic(test *testing.T) {
	for _, entry := range []struct{ pageSize, expected uint64 }{{512, 18155128}, {4096, 18162296}, {8192, 18170488}, {65536, 18285176}} {
		actual, ok := snapshotSourceEnvelope(entry.pageSize, 1)
		if !ok || actual != entry.expected {
			test.Fatalf("E(P,1) changed P=%d got=%d", entry.pageSize, actual)
		}
	}
	if actual, ok := snapshotSourceEnvelope(512, 2); !ok || actual != 18156280 {
		test.Fatal("original N2 envelope changed")
	}
	for _, entry := range []struct{ pageSize, pages uint64 }{{0, 1}, {513, 1}, {131072, 1}, {512, 0}, {512, math.MaxUint64}, {65536, math.MaxInt64}} {
		if _, ok := snapshotSourceEnvelope(entry.pageSize, entry.pages); ok {
			test.Fatal("overflow/invalid envelope accepted")
		}
	}
	for _, regions := range []uint64{math.MaxUint64, math.MaxInt64, math.MaxInt64/32768 + 1} {
		result := localSnapshotSourceObservation{pageSize: 512, regions: regions, baselineEnvelope: 18156280}
		if !errors.Is(snapshotSourceNecessaryRefusal(&result), errSnapshotSourceBudget) {
			test.Fatal("mapping arithmetic overflow accepted")
		}
	}
	result := localSnapshotSourceObservation{pageSize: 512, regions: 553, baselineEnvelope: 18156280}
	if !errors.Is(snapshotSourceNecessaryRefusal(&result), errSnapshotSourceBudget) || result.mappingLower+snapshotSourceLiveCoreLower+512+24+32768 != 18157144 {
		test.Fatal("R553 lower/refusal changed")
	}
}

func TestLocalSnapshotSourcePrivacy(test *testing.T) {
	path := sourceAdmissionSeed(test, 4096, true)
	parent, database := sourceAdmissionBorrow(test, path)
	result, err := inspectLocalSnapshotSource(test.Context(), parent, database, snapshotSourcePrefixBudget)
	if err != nil {
		test.Fatal(err)
	}
	var output bytes.Buffer
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		_, _ = fmt.Fprintf(&output, verb, result)
		_, _ = fmt.Fprintf(&output, verb, &result)
		_, _ = fmt.Fprintf(&output, verb, struct {
			observation localSnapshotSourceObservation
		}{result})
	}
	encoded, err := json.Marshal(struct {
		Observation localSnapshotSourceObservation
	}{result})
	if err != nil {
		test.Fatal(err)
	}
	output.Write(encoded)
	slog.New(slog.NewJSONHandler(&output, nil)).Info("inspection", "observation", result)
	for _, forbidden := range []string{path, parent.Name(), "SQLite format", "payload", "source.sqlite", "writer.sqlite", "synthetic-local-source-secret"} {
		if strings.Contains(output.String(), forbidden) {
			test.Fatal("source detail exposed")
		}
	}
}

func TestLocalSnapshotSourceSidecarBoundaries(test *testing.T) {
	for _, kind := range []string{"missing-both", "empty-both", "wal-no-shm", "shm-no-wal", "wal-short", "wal-page-size", "wal-version", "wal-frame-overflow", "shm-short", "shm-unaligned", "shm-duplicate", "shm-version", "shm-page-size", "shm-uninitialized", "shm-checksum", "shm-last", "shm-backfill", "shm-salt", "shm-order"} {
		test.Run(kind, func(test *testing.T) {
			path := sourceAdmissionSeed(test, 512, true)
			valid := kind == "missing-both" || kind == "empty-both" || kind == "wal-no-shm"
			switch kind {
			case "missing-both":
				for _, suffix := range []string{"-wal", "-shm"} {
					if err := os.Remove(path + suffix); err != nil {
						test.Fatal(err)
					}
				}
			case "empty-both":
				for _, suffix := range []string{"-wal", "-shm"} {
					if err := os.Truncate(path+suffix, 0); err != nil {
						test.Fatal(err)
					}
				}
			case "wal-no-shm":
				if err := os.Remove(path + "-shm"); err != nil {
					test.Fatal(err)
				}
			case "shm-no-wal":
				if err := os.Remove(path + "-wal"); err != nil {
					test.Fatal(err)
				}
			case "wal-short":
				if err := os.Truncate(path+"-wal", 31); err != nil {
					test.Fatal(err)
				}
			case "wal-frame-overflow":
				if err := os.Truncate(path+"-wal", 32+(int64(math.MaxUint32)+1)*536); err != nil {
					test.Fatal(err)
				}
			case "shm-short":
				if err := os.Truncate(path+"-shm", 135); err != nil {
					test.Fatal(err)
				}
			case "shm-unaligned":
				if err := os.Truncate(path+"-shm", 32769); err != nil {
					test.Fatal(err)
				}
			default:
				target := path + "-shm"
				if strings.HasPrefix(kind, "wal-") {
					target = path + "-wal"
				}
				data, err := os.ReadFile(target)
				if err != nil {
					test.Fatal(err)
				}
				switch kind {
				case "wal-page-size":
					binary.BigEndian.PutUint32(data[8:12], 4096)
				case "wal-version":
					binary.BigEndian.PutUint32(data[4:8], 3007001)
				case "shm-duplicate":
					data[48] ^= 1
				case "shm-version":
					binary.LittleEndian.PutUint32(data[:4], 3007001)
				case "shm-page-size":
					binary.LittleEndian.PutUint16(data[14:16], 4096)
				case "shm-uninitialized":
					data[12] = 0
				case "shm-checksum":
					data[40] ^= 1
				case "shm-last":
					binary.LittleEndian.PutUint32(data[16:20], 2)
				case "shm-backfill":
					binary.LittleEndian.PutUint32(data[96:100], 2)
				case "shm-salt":
					data[32] ^= 1
				case "shm-order":
					data[13] ^= 1
				}
				if strings.HasPrefix(kind, "shm-") && kind != "shm-duplicate" {
					if kind != "shm-checksum" {
						var first, second uint32
						for offset := 0; offset < 40; offset += 8 {
							first = first + binary.LittleEndian.Uint32(data[offset:offset+4]) + second
							second = second + binary.LittleEndian.Uint32(data[offset+4:offset+8]) + first
						}
						binary.LittleEndian.PutUint32(data[40:44], first)
						binary.LittleEndian.PutUint32(data[44:48], second)
					}
					copy(data[48:96], data[:48])
				}
				if err := os.WriteFile(target, data, 0o600); err != nil {
					test.Fatal(err)
				}
			}
			parent, database := sourceAdmissionBorrow(test, path)
			result, err := inspectLocalSnapshotSource(test.Context(), parent, database, snapshotSourcePrefixBudget)
			if valid {
				if err != nil || result.decision != snapshotSourceNeedsCertificate {
					test.Fatal("valid sidecar family refused", err)
				}
			} else if err == nil || result != (localSnapshotSourceObservation{}) {
				test.Fatal("invalid sidecar accepted")
			}
		})
	}
}

func TestLocalSnapshotSourceBudgetBeforeRead(test *testing.T) {
	path := sourceAdmissionSeed(test, 4096, true)
	parent, database := sourceAdmissionBorrow(test, path)
	for _, entry := range []struct {
		budget       uint64
		expectedRead int
	}{{99, 0}, {100, 100}, {131, 100}, {132, 132}, {267, 132}, {269, 0}} {
		read := 0
		operations := localSnapshotSourceIO{readAt: func(file *os.File, data []byte, offset int64) (int, error) {
			read += len(data)
			return file.ReadAt(data, offset)
		}, closeFile: (*os.File).Close}
		result, err := inspectLocalSnapshotSourceWithIO(test.Context(), parent, database, entry.budget, operations)
		if err == nil || result != (localSnapshotSourceObservation{}) || read != entry.expectedRead {
			test.Fatalf("budget charged after read: budget=%d read=%d", entry.budget, read)
		}
	}
}
