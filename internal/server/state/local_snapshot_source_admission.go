package state

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

var (
	errSnapshotSourceInvalid = errors.New("local snapshot source inspection refused")
	errSnapshotSourceBudget  = errors.New("local snapshot source inspection budget exceeded")
	errSnapshotSourceCleanup = errors.New("local snapshot source inspection cleanup failed")
)

const snapshotSourcePrefixBudget uint64 = 100 + 32 + 136
const snapshotSourcePlanningSchemaBytes uint64 = 66040
const snapshotSourceLiveCoreLower uint64 = 2*864 + 4*120 + 2*312 + 2*152

type localSnapshotSourceDecision uint8

const snapshotSourceNeedsCertificate localSnapshotSourceDecision = 1

type localSnapshotSourceObservation struct {
	decision                                                            localSnapshotSourceDecision
	pageSize, frames, regions, shmBytes, mappingLower, baselineEnvelope uint64
}

func (localSnapshotSourceObservation) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot source observation")
}

func (localSnapshotSourceObservation) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot source observation"`), nil
}

func (localSnapshotSourceObservation) LogValue() slog.Value {
	return slog.StringValue("local snapshot source observation")
}

type localSnapshotSourceIO struct {
	readAt    func(*os.File, []byte, int64) (int, error)
	closeFile func(*os.File) error
}

func inspectLocalSnapshotSource(ctx context.Context, parent, database *os.File, readBudget uint64) (localSnapshotSourceObservation, error) {
	return inspectLocalSnapshotSourceWithIO(ctx, parent, database, readBudget, localSnapshotSourceIO{
		readAt:    (*os.File).ReadAt,
		closeFile: (*os.File).Close,
	})
}

func inspectLocalSnapshotSourceWithIO(ctx context.Context, parent, database *os.File, readBudget uint64, operations localSnapshotSourceIO) (localSnapshotSourceObservation, error) {
	if err := maintenance.CheckPlatform(ctx); err != nil {
		return localSnapshotSourceObservation{}, err
	}
	if parent == nil || database == nil || readBudget == 0 || readBudget > snapshotSourcePrefixBudget || operations.readAt == nil || operations.closeFile == nil {
		return localSnapshotSourceObservation{}, errSnapshotSourceInvalid
	}
	return inspectLocalSnapshotSourcePlatform(ctx, parent, database, readBudget, operations)
}

func snapshotSourceEnvelope(pageSize, pages uint64) (uint64, bool) {
	const fixed = 128*(2*65536+2048) + 16*65536 + snapshotSourcePlanningSchemaBytes
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || pages == 0 || pages > (math.MaxInt64-fixed)/(2*pageSize+128) {
		return 0, false
	}
	return (2*pageSize+128)*pages + fixed, true
}

func snapshotSourceMain(header *[100]byte, size int64) (localSnapshotSourceObservation, error) {
	pageSize := uint64(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	envelope, ok := snapshotSourceEnvelope(pageSize, 1)
	if !ok || size < int64(pageSize) || uint64(size)%pageSize != 0 || string(header[:16]) != "SQLite format 3\x00" || header[18] != 2 || header[19] != 2 || header[20] != 0 || !bytes.Equal(header[21:24], []byte{64, 32, 32}) {
		return localSnapshotSourceObservation{}, errSnapshotSourceInvalid
	}
	return localSnapshotSourceObservation{pageSize: pageSize, regions: 1, baselineEnvelope: envelope}, nil
}

func snapshotSourceChecksum(order binary.ByteOrder, data []byte) (first, second uint32) {
	for offset := 0; offset < len(data); offset += 8 {
		first += order.Uint32(data[offset:offset+4]) + second
		second += order.Uint32(data[offset+4:offset+8]) + first
	}
	return first, second
}

func snapshotSourceWAL(result *localSnapshotSourceObservation, header *[32]byte, size int64) error {
	if size == 0 {
		return nil
	}
	magic := binary.BigEndian.Uint32(header[:4])
	if size < 32 || (uint64(size)-32)%(result.pageSize+24) != 0 || magic&0xfffffffe != 0x377f0682 || binary.BigEndian.Uint32(header[4:8]) != 3007000 || uint64(binary.BigEndian.Uint32(header[8:12])) != result.pageSize {
		return errSnapshotSourceInvalid
	}
	var order binary.ByteOrder = binary.LittleEndian
	if magic&1 != 0 {
		order = binary.BigEndian
	}
	first, second := snapshotSourceChecksum(order, header[:24])
	if first != binary.BigEndian.Uint32(header[24:28]) || second != binary.BigEndian.Uint32(header[28:32]) {
		return errSnapshotSourceInvalid
	}
	result.frames = (uint64(size) - 32) / (result.pageSize + 24)
	if result.frames > math.MaxUint32 {
		return errSnapshotSourceBudget
	}
	result.regions = 1 + (result.frames+33)/4096
	return nil
}

func snapshotSourceSHM(result *localSnapshotSourceObservation, header *[136]byte, size int64, wal *[32]byte, walSize int64) error {
	if size == 0 {
		return nil
	}
	pageSize := uint64(binary.LittleEndian.Uint16(header[14:16]))
	if pageSize == 1 {
		pageSize = 65536
	}
	first, second := snapshotSourceChecksum(binary.LittleEndian, header[:40])
	last := uint64(binary.LittleEndian.Uint32(header[16:20]))
	if size < 32768 || size%32768 != 0 || !bytes.Equal(header[:48], header[48:96]) || binary.LittleEndian.Uint32(header[:4]) != 3007000 || header[12] != 1 || header[13] > 1 || pageSize != result.pageSize || first != binary.LittleEndian.Uint32(header[40:44]) || second != binary.LittleEndian.Uint32(header[44:48]) || last > result.frames || uint64(size)/32768 < 1+(last+33)/4096 || uint64(binary.LittleEndian.Uint32(header[96:100])) > last {
		return errSnapshotSourceInvalid
	}
	if walSize != 0 && (header[13] != wal[3]&1 || !bytes.Equal(header[32:40], wal[16:24])) {
		return errSnapshotSourceInvalid
	}
	result.shmBytes = uint64(size)
	result.regions = max(result.regions, uint64(size)/32768)
	return nil
}

func snapshotSourceNecessaryRefusal(result *localSnapshotSourceObservation) error {
	const scratchFixed = snapshotSourceLiveCoreLower + 24 + 32768
	if result.regions > (math.MaxInt64-scratchFixed-result.pageSize)/32768 {
		return errSnapshotSourceBudget
	}
	result.mappingLower = result.regions * 32768
	if result.mappingLower+scratchFixed+result.pageSize > result.baselineEnvelope {
		return errSnapshotSourceBudget
	}
	return nil
}
