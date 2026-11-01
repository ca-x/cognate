package state

import (
	"bytes"
	"encoding/json/v2"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestGeminiRecordStrictFamilyConsumerAndCanonical(test *testing.T) {
	flow := newGeminiStateFlow(test)
	prepared := flow.prepare(test)
	record := nativeGeminiRecord{preparation: prepared.Binding()}
	body, err := nativeEncodeGeminiRecord(record)
	if err != nil {
		test.Fatal("Gemini record encode")
	}
	defer clear(body)
	decoded, err := nativeDecodeGeminiRecord(body, flow.consumer)
	if err != nil || !proto.Equal(decoded.preparation.CopyWire(), record.preparation.CopyWire()) {
		test.Fatal("Gemini record roundtrip")
	}
	for _, mutate := range []func([]byte) []byte{func(value []byte) []byte { return value[:len(value)-1] }, func(value []byte) []byte { value[0] = 'C'; return value }, func(value []byte) []byte { return append(value, 0) }, func(value []byte) []byte {
		var stored geminiStoredRecord
		_ = json.Unmarshal(value[4:], &stored)
		stored.Version = 2
		changed, _ := json.Marshal(stored)
		return append([]byte("GM3\x00"), changed...)
	}} {
		bad := mutate(bytes.Clone(body))
		if result, err := nativeDecodeGeminiRecord(bad, flow.consumer); err == nil || result.preparation.CopyWire() != nil {
			test.Fatal("noncanonical/truncated/foreign-version record accepted")
		}
		clear(bad)
	}
	foreign := flow.consumer
	foreign.PublicKey[0] ^= 1
	if _, err := nativeDecodeGeminiRecord(body, foreign); err == nil {
		test.Fatal("record accepted foreign authenticated consumer")
	}
	if _, err := nativeDecodeCodexRecord(body, flow.consumer); err == nil {
		test.Fatal("Gemini record accepted by old Codex decoder")
	}
	if bytes.Contains(body, []byte("synthetic-gemini-state-material")) {
		test.Fatal("prepared record contains slot plaintext")
	}
}
