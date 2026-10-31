package state

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"strconv"
	"time"
)

const restoreDatabaseWorkerMode = "--cognate-restore-database-worker-v1"

type restoreDatabaseIdentity struct{ Device, Inode uint64 }
type restoreDatabaseRequest struct {
	Nonce            [16]byte
	Parent, Database restoreDatabaseIdentity
	Limits           LocalSnapshotLimits
}
type restoreDatabaseResponse struct {
	Nonce  [16]byte
	Status string
	Facts  restoreDatabaseFacts
}

func RestoreDatabaseWorkerDispatch(args []string) (bool, int) {
	if len(args) < 2 || args[1] != restoreDatabaseWorkerMode {
		return false, 0
	}
	if len(args) != 2 {
		return true, 2
	}
	return true, restoreDatabaseWorkerServe()
}

func restoreReadMessage(reader io.Reader, maximum int64, output any) error {
	data, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	defer clear(data)
	if err != nil || int64(len(data)) > maximum || json.Unmarshal(data, output, json.RejectUnknownMembers(true), json.WithUnmarshalers(json.UnmarshalFunc(func(encoded []byte, duration *time.Duration) error {
		value, err := strconv.ParseInt(string(encoded), 10, 64)
		*duration = time.Duration(value)
		return err
	}))) != nil {
		return ErrRestoreInvalid
	}
	canonical, err := restoreMarshalMessage(output)
	defer clear(canonical)
	if err != nil || !bytes.Equal(data, append(canonical, '\n')) {
		return ErrRestoreInvalid
	}
	return nil
}

func restoreWriteMessage(writer io.Writer, input any) error {
	data, err := restoreMarshalMessage(input)
	if err != nil {
		return ErrRestoreInvalid
	}
	data = append(data, '\n')
	count, err := writer.Write(data)
	clear(data)
	if err != nil || count != len(data) {
		return ErrRestoreInvalid
	}
	return nil
}

func restoreMarshalMessage(input any) ([]byte, error) {
	return json.Marshal(input, json.WithMarshalers(json.MarshalFunc(func(duration time.Duration) ([]byte, error) {
		return []byte(strconv.FormatInt(int64(duration), 10)), nil
	})))
}
