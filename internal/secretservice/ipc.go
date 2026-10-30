package secretservice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const maxSecretBytes = 64 << 10

var errFrame = errors.New("invalid-helper-frame")

func encodeKey(request Key) ([]byte, error) {
	if !strings.HasPrefix(request.Socket, "/") || len(request.Socket) > 107 {
		return nil, errFrame
	}
	var output bytes.Buffer
	output.WriteString("SSQ1")
	for _, field := range []string{request.Socket, request.Service, request.Account} {
		if len(field) == 0 || len(field) > 256 || !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return nil, errFrame
		}
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(field)))
		output.Write(length[:])
		output.WriteString(field)
	}
	return output.Bytes(), nil
}

func requireEOF(reader io.Reader) error {
	var extra [1]byte
	count, err := reader.Read(extra[:])
	if count != 0 || err != io.EOF {
		return errFrame
	}
	return nil
}

func decodeKey(reader io.Reader) (Key, error) {
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "SSQ1" {
		return Key{}, errFrame
	}
	fields := make([]string, 0, 3)
	for index := range 3 {
		var length [2]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return Key{}, errFrame
		}
		count := binary.BigEndian.Uint16(length[:])
		if count == 0 || count > 256 || (index == 0 && count > 107) {
			return Key{}, errFrame
		}
		field := make([]byte, int(count))
		if _, err := io.ReadFull(reader, field); err != nil {
			return Key{}, errFrame
		}
		fields = append(fields, string(field))
	}
	request := Key{Socket: fields[0], Service: fields[1], Account: fields[2]}
	if _, err := encodeKey(request); err != nil || requireEOF(reader) != nil {
		return Key{}, errFrame
	}
	return request, nil
}

func writeResponse(writer io.Writer, result Result) error {
	value := result.Secret.Copy()
	defer clear(value)
	if result.State > Locked || len(value) > maxSecretBytes || (result.State != Found && len(value) != 0) {
		return errFrame
	}
	var header [9]byte
	copy(header[:4], "SSR1")
	header[4] = byte(result.State)
	binary.BigEndian.PutUint32(header[5:], uint32(len(value)))
	if count, err := writer.Write(header[:]); err != nil || count != len(header) {
		return errFrame
	}
	if len(value) > 0 {
		if count, err := writer.Write(value); err != nil || count != len(value) {
			return errFrame
		}
	}
	return nil
}

func readResponse(reader io.Reader) (Result, error) {
	var header [9]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil || string(header[:4]) != "SSR1" {
		return Result{}, errFrame
	}
	state := State(header[4])
	length := binary.BigEndian.Uint32(header[5:])
	if state > Locked || length > maxSecretBytes || (state != Found && length != 0) {
		return Result{}, errFrame
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader, value); err != nil || requireEOF(reader) != nil {
		clear(value)
		return Result{}, errFrame
	}
	if state != Found {
		return Result{State: state}, nil
	}
	return Result{State: Found, Secret: Secret{payload: &secretPayload{data: &value}}}, nil
}
