package state

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const snapshotWorkerMode = "--cognate-local-snapshot-worker-v1"
const snapshotRequestBytes = 448
const snapshotResponseBytes = 256

type snapshotFileIdentity struct{ device, inode uint64 }
type snapshotWorkerRequest struct {
	nonce                 [16]byte
	source, parent, stage snapshotFileIdentity
	limits                LocalSnapshotLimits
	name                  string
	op                    byte
}
type snapshotWorkerResponse struct {
	nonce                                           [16]byte
	status                                          byte
	addressSpace, heap, heapUsed, cpu, virtualBytes uint64
	databaseBytes, pageSize, pages                  uint64
	databaseHash, schemaHash                        [32]byte
	referenceBytes, records                         uint64
	referenceHash                                   [32]byte
}

func (snapshotWorkerRequest) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot worker request")
}
func (snapshotWorkerRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot worker request"`), nil
}
func (snapshotWorkerRequest) LogValue() slog.Value {
	return slog.StringValue("local snapshot worker request")
}
func (snapshotWorkerResponse) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "local snapshot worker response")
}
func (snapshotWorkerResponse) MarshalJSON() ([]byte, error) {
	return []byte(`"local snapshot worker response"`), nil
}
func (snapshotWorkerResponse) LogValue() slog.Value {
	return slog.StringValue("local snapshot worker response")
}

func SnapshotWorkerDispatch(args []string) (bool, int) {
	if len(args) < 2 || args[1] != snapshotWorkerMode {
		return false, 0
	}
	if len(args) != 2 {
		return true, 2
	}
	return true, snapshotWorkerServe()
}

func snapshotDecodeRequest(reader io.Reader) (snapshotWorkerRequest, error) {
	var request snapshotWorkerRequest
	var frame [snapshotRequestBytes + 1]byte
	count, err := io.ReadFull(reader, frame[:])
	if err != io.ErrUnexpectedEOF || count != snapshotRequestBytes || string(frame[:8]) != "CGB2WR01" || binary.BigEndian.Uint16(frame[8:10]) != 1 || frame[10] < 1 || frame[10] > 2 || frame[11] != 0 || binary.BigEndian.Uint32(frame[12:16]) != snapshotRequestBytes || !bytes.Equal(frame[162:164], []byte{0, 0}) {
		return request, ErrLocalSnapshotInvalid
	}
	length := int(binary.BigEndian.Uint16(frame[160:162]))
	if length < 1 || length > 255 || !bytes.Equal(frame[164+length:snapshotRequestBytes], make([]byte, snapshotRequestBytes-164-length)) {
		return request, ErrLocalSnapshotInvalid
	}
	request.name = string(frame[164 : 164+length])
	if !utf8.ValidString(request.name) || strings.ContainsAny(request.name, "/\\\x00") || request.name == "." || request.name == ".." {
		return snapshotWorkerRequest{}, ErrLocalSnapshotInvalid
	}
	copy(request.nonce[:], frame[16:32])
	if request.nonce == [16]byte{} {
		return snapshotWorkerRequest{}, ErrLocalSnapshotInvalid
	}
	request.op = frame[10]
	identities := []*snapshotFileIdentity{&request.source, &request.parent, &request.stage}
	for index, identity := range identities {
		identity.device = binary.BigEndian.Uint64(frame[32+16*index:])
		identity.inode = binary.BigEndian.Uint64(frame[40+16*index:])
		if identity.inode == 0 {
			return snapshotWorkerRequest{}, ErrLocalSnapshotInvalid
		}
	}
	var values [10]uint64
	for index := range values {
		values[index] = binary.BigEndian.Uint64(frame[80+index*8:])
		if values[index] == 0 || values[index] > math.MaxInt64 {
			return snapshotWorkerRequest{}, ErrLocalSnapshotInvalid
		}
	}
	request.limits = LocalSnapshotLimits{MaxDBBytes: int64(values[0]), MaxInventoryBytes: int64(values[1]), MaxRecords: values[2], MaxStagingBytes: int64(values[3]), SQLiteHeapBytes: int64(values[4]), WorkerAddressSpaceBytes: int64(values[5]), WorkerCPUSeconds: values[6], MaxWALBytes: int64(values[7]), MaxSHMBytes: int64(values[8]), OperationTimeout: time.Duration(values[9]), DrainTimeout: time.Duration(values[9])}
	if !request.limits.valid() {
		return snapshotWorkerRequest{}, ErrLocalSnapshotInvalid
	}
	return request, nil
}

func snapshotEncodeResponse(response snapshotWorkerResponse) [snapshotResponseBytes]byte {
	var frame [snapshotResponseBytes]byte
	copy(frame[:8], "CGB2RS01")
	binary.BigEndian.PutUint16(frame[8:10], 1)
	frame[11] = response.status
	copy(frame[16:32], response.nonce[:])
	for index, value := range []uint64{response.addressSpace, response.heap, response.heapUsed, response.cpu, response.virtualBytes, response.databaseBytes, response.pageSize, response.pages} {
		binary.BigEndian.PutUint64(frame[32+index*8:], value)
	}
	copy(frame[96:128], response.databaseHash[:])
	copy(frame[128:160], response.schemaHash[:])
	binary.BigEndian.PutUint64(frame[160:168], response.referenceBytes)
	copy(frame[168:200], response.referenceHash[:])
	binary.BigEndian.PutUint64(frame[200:208], response.records)
	return frame
}

func snapshotWorkerFiles() (*os.File, *os.File) {
	return os.NewFile(3, "snapshot request"), os.NewFile(4, "snapshot response")
}

func snapshotEncodeRequest(request snapshotWorkerRequest) ([snapshotRequestBytes]byte, error) {
	var frame [snapshotRequestBytes]byte
	if !request.limits.valid() || request.name == "" || len(request.name) > 255 || strings.ContainsAny(request.name, "/\\\x00") || request.name == "." || request.name == ".." || !utf8.ValidString(request.name) || request.nonce == [16]byte{} || request.op != 1 {
		return frame, ErrLocalSnapshotInvalid
	}
	copy(frame[:8], "CGB2WR01")
	binary.BigEndian.PutUint16(frame[8:10], 1)
	frame[10] = request.op
	binary.BigEndian.PutUint32(frame[12:16], snapshotRequestBytes)
	copy(frame[16:32], request.nonce[:])
	for index, identity := range []snapshotFileIdentity{request.source, request.parent, request.stage} {
		binary.BigEndian.PutUint64(frame[32+16*index:], identity.device)
		binary.BigEndian.PutUint64(frame[40+16*index:], identity.inode)
	}
	for index, value := range []uint64{uint64(request.limits.MaxDBBytes), uint64(request.limits.MaxInventoryBytes), request.limits.MaxRecords, uint64(request.limits.MaxStagingBytes), uint64(request.limits.SQLiteHeapBytes), uint64(request.limits.WorkerAddressSpaceBytes), request.limits.WorkerCPUSeconds, uint64(request.limits.MaxWALBytes), uint64(request.limits.MaxSHMBytes), uint64(request.limits.OperationTimeout)} {
		binary.BigEndian.PutUint64(frame[80+8*index:], value)
	}
	binary.BigEndian.PutUint16(frame[160:162], uint16(len(request.name)))
	copy(frame[164:], request.name)
	return frame, nil
}

func snapshotDecodeResponse(frame []byte, request snapshotWorkerRequest) (snapshotWorkerResponse, error) {
	var response snapshotWorkerResponse
	if len(frame) != snapshotResponseBytes || string(frame[:8]) != "CGB2RS01" || binary.BigEndian.Uint16(frame[8:10]) != 1 || frame[10] != 0 || frame[11] > 7 || !bytes.Equal(frame[12:16], make([]byte, 4)) || !bytes.Equal(frame[208:], make([]byte, 48)) || !bytes.Equal(frame[16:32], request.nonce[:]) {
		return response, ErrLocalSnapshotInvalid
	}
	response.nonce = request.nonce
	response.status = frame[11]
	values := []*uint64{&response.addressSpace, &response.heap, &response.heapUsed, &response.cpu, &response.virtualBytes, &response.databaseBytes, &response.pageSize, &response.pages}
	for index, value := range values {
		*value = binary.BigEndian.Uint64(frame[32+8*index:])
	}
	copy(response.databaseHash[:], frame[96:128])
	copy(response.schemaHash[:], frame[128:160])
	response.referenceBytes = binary.BigEndian.Uint64(frame[160:168])
	copy(response.referenceHash[:], frame[168:200])
	response.records = binary.BigEndian.Uint64(frame[200:208])
	if response.status != 0 {
		if response.status == 6 {
			return response, ErrLocalSnapshotBudget
		}
		if response.status == 7 {
			return response, ErrLocalSnapshotCleanup
		}
		if response.status == 3 {
			return response, ErrLocalSnapshotResource
		}
		return response, ErrLocalSnapshotInvalid
	}
	if response.addressSpace != uint64(request.limits.WorkerAddressSpaceBytes) || response.heap != uint64(request.limits.SQLiteHeapBytes) || response.cpu != request.limits.WorkerCPUSeconds || response.virtualBytes > response.addressSpace || response.heapUsed > response.heap || response.pageSize < 512 || response.pageSize > 65536 || response.pageSize&(response.pageSize-1) != 0 || response.pages == 0 || response.pages > uint64(request.limits.MaxDBBytes)/response.pageSize || response.databaseBytes != response.pageSize*response.pages || response.databaseHash == [32]byte{} || response.schemaHash == [32]byte{} {
		return snapshotWorkerResponse{}, ErrLocalSnapshotInvalid
	}
	return response, nil
}
