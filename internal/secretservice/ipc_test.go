package secretservice

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestIPCBoundsAndStates(test *testing.T) {
	request := Key{Socket: "/synthetic/private-bus", Service: "service", Account: "account"}
	frame, err := encodeKey(request)
	if err != nil {
		test.Fatal("request-encode=false")
	}
	decoded, err := decodeKey(bytes.NewReader(frame))
	if err != nil || decoded != request || len(frame) > 629 {
		test.Fatal("request-roundtrip=false")
	}
	for _, invalid := range [][]byte{frame[:len(frame)-1], append(bytes.Clone(frame), 'x'), []byte("SSQ2")} {
		if _, err := decodeKey(bytes.NewReader(invalid)); err == nil {
			test.Fatal("invalid-request-accepted=false")
		}
	}
	for _, state := range []State{Unknown, Missing, Locked} {
		var output bytes.Buffer
		if writeResponse(&output, Result{State: state}) != nil {
			test.Fatal("state-encode=false")
		}
		result, err := readResponse(bytes.NewReader(output.Bytes()))
		if err != nil || result.State != state || result.Secret.Copy() != nil || result.AllowFileFallback() != (state == Missing) {
			test.Fatal("state-roundtrip=false")
		}
	}
	for _, value := range []struct {
		state  State
		length uint32
	}{{State(4), 0}, {Missing, 1}, {Locked, 1}, {Found, maxSecretBytes + 1}} {
		var header [9]byte
		copy(header[:4], "SSR1")
		header[4] = byte(value.state)
		binary.BigEndian.PutUint32(header[5:], value.length)
		if _, err := readResponse(bytes.NewReader(header[:])); err == nil {
			test.Fatal("invalid-reply-header=false")
		}
	}
	maximum := make([]byte, maxSecretBytes)
	secret := Secret{payload: &secretPayload{data: &maximum}}
	defer secret.Destroy()
	var output bytes.Buffer
	if writeResponse(&output, Result{State: Found, Secret: secret}) != nil {
		test.Fatal("maximum-encode=false")
	}
	result, err := readResponse(bytes.NewReader(output.Bytes()))
	defer result.Secret.Destroy()
	copy := result.Secret.Copy()
	defer clear(copy)
	if err != nil || result.State != Found || len(copy) != maxSecretBytes {
		test.Fatal("maximum-roundtrip=false")
	}
}

func TestReturnedSecretPrivacy(test *testing.T) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		test.Fatal("fixture-random")
	}
	value := []byte(base64.RawStdEncoding.EncodeToString(random))
	clear(random)
	defer clear(value)
	secret := Secret{payload: &secretPayload{data: &value}}
	defer secret.Destroy()
	result := Result{State: Found, Secret: secret}
	private := struct{ result Result }{result}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%t", "%b", "%d", "%o", "%O", "%T", "%U", "%e", "%E", "%f", "%F", "%g", "%G", "%p", "%c", "%w"} {
		for _, container := range []any{secret, result, private, []Result{result}, map[string]Result{"entry": result}} {
			output := fmt.Sprintf(verb, container)
			if strings.Contains(output, string(value)) || strings.Contains(output, hex.EncodeToString(value)) || strings.Contains(output, fmt.Sprint(value)) {
				test.Fatal("fmt-privacy=false")
			}
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("probe", "result", result, "private", private)
	if bytes.Contains(output.Bytes(), value) {
		test.Fatal("slog-privacy=false")
	}
	encoded, err := json.Marshal(result)
	if bytes.Contains(encoded, value) || (err != nil && strings.Contains(err.Error(), string(value))) {
		test.Fatal("json-privacy=false")
	}
	copy := secret.Copy()
	defer clear(copy)
	copy[0] ^= 0xff
	if bytes.Equal(copy, value) {
		test.Fatal("copy-not-independent=false")
	}
}

type countKeyReads struct {
	io.Reader
	count int
}

func (reader *countKeyReads) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	reader.count += count
	return count, err
}

func TestKeySocketLimitPrecedesRead(test *testing.T) {
	frame := []byte("SSQ1")
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], 108)
	frame = append(frame, length[:]...)
	frame = append(frame, bytes.Repeat([]byte{'A'}, 108)...)
	frame = append(frame, 0, 1, 's', 0, 1, 'a')
	reader := &countKeyReads{Reader: bytes.NewReader(frame)}
	if _, err := decodeKey(reader); err == nil || reader.count != 6 {
		test.Fatal("socket-limit-after-read=false")
	}
}

func TestDestroyedSecretCannotBeCopied(test *testing.T) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		test.Fatal("fixture-random")
	}
	secret := Secret{payload: &secretPayload{data: &value}}
	first := secret.Copy()
	defer clear(first)
	var copies sync.WaitGroup
	for range 8 {
		copies.Go(func() {
			for range 100 {
				copied := secret.Copy()
				clear(copied)
			}
		})
	}
	secret.Destroy()
	copies.Wait()
	secret.Destroy()
	if copied := secret.Copy(); len(copied) != 0 {
		clear(copied)
		test.Fatal("destroyed-copy=false")
	}
	if len(first) != 32 {
		test.Fatal("copy-lifetime=false")
	}
}
