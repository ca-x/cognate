package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"
)

type MCPSecret struct{ private func(bool) []byte }

func NewMCPSecret(value []byte) MCPSecret {
	owned := append([]byte{}, value...)
	return MCPSecret{private: func(close bool) []byte {
		if close {
			clear(owned)
			owned = nil
		}
		return owned
	}}
}
func (secret *MCPSecret) Clear() {
	if secret != nil && secret.private != nil {
		secret.private(true)
		secret.private = nil
	}
}
func (secret *MCPSecret) Close() { secret.Clear() }
func (secret MCPSecret) bytes() []byte {
	if secret.private == nil {
		return nil
	}
	return secret.private(false)
}
func (secret MCPSecret) clone() MCPSecret {
	if secret.bytes() == nil {
		return MCPSecret{}
	}
	return NewMCPSecret(secret.bytes())
}
func (MCPSecret) String() string               { return "[redacted MCP secret]" }
func (MCPSecret) GoString() string             { return "[redacted MCP secret]" }
func (MCPSecret) MarshalJSON() ([]byte, error) { return []byte(`"[redacted MCP secret]"`), nil }
func (MCPSecret) MarshalText() ([]byte, error) { return []byte("[redacted MCP secret]"), nil }
func (MCPSecret) LogValue() slog.Value         { return slog.StringValue("[redacted MCP secret]") }
func (MCPSecret) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted MCP secret]")
}

type mcpEnvelopeData struct {
	document agentconfig.MCPDocumentV1
	slots    map[string]MCPSecret
}
type mcpEnvelope struct{ private func(bool) *mcpEnvelopeData }

func newMCPEnvelope(document agentconfig.MCPDocumentV1, slots map[string]MCPSecret) (mcpEnvelope, error) {
	cloned, err := agentconfig.CloneMCPDocument(document)
	if err != nil {
		return mcpEnvelope{}, mcpShapeFailure(err)
	}
	data := &mcpEnvelopeData{document: cloned, slots: make(map[string]MCPSecret, len(slots))}
	for ref, secret := range slots {
		data.slots[ref] = secret.clone()
	}
	return mcpEnvelope{private: func(close bool) *mcpEnvelopeData {
		if close && data != nil {
			for _, secret := range data.slots {
				secret.Clear()
			}
			clear(data.slots)
			data.document = agentconfig.MCPDocumentV1{}
			data = nil
		}
		return data
	}}, nil
}
func (envelope mcpEnvelope) data() *mcpEnvelopeData {
	if envelope.private == nil {
		return nil
	}
	return envelope.private(false)
}
func (envelope *mcpEnvelope) Close() {
	if envelope != nil && envelope.private != nil {
		envelope.private(true)
		envelope.private = nil
	}
}
func (envelope *mcpEnvelope) Clear()             { envelope.Close() }
func (mcpEnvelope) String() string               { return "[redacted MCP envelope]" }
func (mcpEnvelope) GoString() string             { return "[redacted MCP envelope]" }
func (mcpEnvelope) MarshalJSON() ([]byte, error) { return []byte(`"[redacted MCP envelope]"`), nil }
func (mcpEnvelope) MarshalText() ([]byte, error) { return []byte("[redacted MCP envelope]"), nil }
func (mcpEnvelope) LogValue() slog.Value         { return slog.StringValue("[redacted MCP envelope]") }
func (mcpEnvelope) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted MCP envelope]")
}

type mcpEnvelopeWire struct {
	Version  int                       `json:"version"`
	Document agentconfig.MCPDocumentV1 `json:"document"`
	Slots    map[string][]byte         `json:"slots"`
}

func (envelope mcpEnvelope) encode() (state.ConfigurationPayload, error) {
	data := envelope.data()
	if data == nil {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	values := make(map[string][]byte, len(data.slots))
	for ref, secret := range data.slots {
		values[ref] = secret.bytes()
	}
	defer clear(values)
	encoded, err := json.Marshal(mcpEnvelopeWire{Version: 1, Document: data.document, Slots: values})
	defer clear(encoded)
	if err != nil {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	if len(encoded) > state.MaxConfigurationPayloadBytes {
		return state.ConfigurationPayload{}, ErrLimit
	}
	return state.NewConfigurationPayload(encoded), nil
}

func decodeMCPEnvelope(payload state.ConfigurationPayload) (mcpEnvelope, error) {
	raw := payload.Reveal()
	defer clear(raw)
	if len(raw) > state.MaxConfigurationPayloadBytes {
		return mcpEnvelope{}, ErrLimit
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	for decoder.InputOffset() < int64(len(raw)) {
		token, err := decoder.ReadToken()
		if err != nil {
			break
		}
		if token.Kind() == 'n' {
			return mcpEnvelope{}, ErrInvalid
		}
	}
	var wire mcpEnvelopeWire
	defer func() {
		for _, value := range wire.Slots {
			clear(value)
		}
		clear(wire.Slots)
	}()
	if json.Unmarshal(raw, &wire, json.RejectUnknownMembers(true)) != nil {
		return mcpEnvelope{}, ErrUnsupported
	}
	if wire.Version != 1 {
		return mcpEnvelope{}, ErrUnsupported
	}
	if wire.Slots == nil {
		return mcpEnvelope{}, ErrInvalid
	}
	slots, err := agentconfig.ValidateMCPDocument(wire.Document)
	if err != nil {
		return mcpEnvelope{}, mcpShapeFailure(err)
	}
	allowed := make(map[string]string, len(slots))
	for _, slot := range slots {
		allowed[slot.Ref] = slot.Purpose
	}
	values := make(map[string]MCPSecret, len(wire.Slots))
	defer func() {
		for _, value := range values {
			value.Clear()
		}
	}()
	total := 0
	for ref, value := range wire.Slots {
		purpose, exists := allowed[ref]
		if !exists || value == nil || !validMCPSlotValue(value, purpose) {
			return mcpEnvelope{}, ErrInvalid
		}
		total += len(value)
		if total > 128<<10 {
			return mcpEnvelope{}, ErrLimit
		}
		values[ref] = NewMCPSecret(value)
	}
	return newMCPEnvelope(wire.Document, values)
}

func validMCPSlotValue(value []byte, purpose string) bool {
	return len(value) <= 8<<10 && utf8.Valid(value) && !bytes.ContainsRune(value, 0) && (purpose != "header" || !bytes.ContainsAny(value, "\r\n"))
}

func clearMCPMutations(mutations []MCPSecretMutation) {
	for index := range mutations {
		mutations[index].Replacement.Clear()
	}
}

func mergeMCPEnvelope(current mcpEnvelope, document agentconfig.MCPDocumentV1, mutations []MCPSecretMutation) (mcpEnvelope, error) {
	slots, err := agentconfig.ValidateMCPDocument(document)
	if err != nil {
		return mcpEnvelope{}, mcpShapeFailure(err)
	}
	if len(mutations) > 2*agentconfig.MaxMCPSlots {
		return mcpEnvelope{}, ErrLimit
	}
	next := map[string]string{}
	for _, slot := range slots {
		next[slot.Ref] = slot.Purpose
	}
	previous := map[string]string{}
	if data := current.data(); data != nil {
		oldSlots, err := agentconfig.ValidateMCPDocument(data.document)
		if err != nil {
			return mcpEnvelope{}, ErrInvalid
		}
		for _, slot := range oldSlots {
			previous[slot.Ref] = slot.Purpose
		}
	}
	seen := map[string]bool{}
	for _, mutation := range mutations {
		purpose, inNew := next[mutation.Slot]
		_, inOld := previous[mutation.Slot]
		if seen[mutation.Slot] || (!inNew && !inOld) {
			return mcpEnvelope{}, ErrInvalid
		}
		seen[mutation.Slot] = true
		switch mutation.Action {
		case Keep:
			if !inNew || mutation.Replacement.bytes() != nil || mutation.Confirmed {
				return mcpEnvelope{}, ErrInvalid
			}
		case Replace:
			if !inNew || mutation.Replacement.bytes() == nil || mutation.Confirmed || !validMCPSlotValue(mutation.Replacement.bytes(), purpose) {
				return mcpEnvelope{}, ErrInvalid
			}
		case Clear:
			if !mutation.Confirmed || mutation.Replacement.bytes() != nil {
				return mcpEnvelope{}, ErrInvalid
			}
		default:
			return mcpEnvelope{}, ErrInvalid
		}
	}
	for ref := range previous {
		if _, exists := next[ref]; !exists && !seen[ref] {
			return mcpEnvelope{}, ErrInvalid
		}
	}
	values := map[string]MCPSecret{}
	defer func() {
		for _, value := range values {
			value.Clear()
		}
	}()
	if data := current.data(); data != nil {
		for ref, value := range data.slots {
			if _, exists := next[ref]; exists {
				values[ref] = value.clone()
			}
		}
	}
	for _, mutation := range mutations {
		if mutation.Action == Keep {
			continue
		}
		old := values[mutation.Slot]
		old.Clear()
		delete(values, mutation.Slot)
		if mutation.Action == Replace {
			values[mutation.Slot] = mutation.Replacement.clone()
		}
	}
	total := 0
	for ref, value := range values {
		if !validMCPSlotValue(value.bytes(), next[ref]) {
			return mcpEnvelope{}, ErrInvalid
		}
		total += len(value.bytes())
	}
	if total > 128<<10 {
		return mcpEnvelope{}, ErrLimit
	}
	return newMCPEnvelope(document, values)
}

func mcpShapeFailure(err error) error {
	if errors.Is(err, agentconfig.ErrMCPLimit) {
		return ErrLimit
	}
	if errors.Is(err, agentconfig.ErrMCPUnsupported) {
		return ErrUnsupported
	}
	return ErrInvalid
}

func mcpWarnings(document agentconfig.MCPDocumentV1) []string {
	warnings := []string{}
	for _, server := range document.Servers {
		for _, remote := range []*agentconfig.MCPRemote{server.HTTP, server.SSE} {
			if remote != nil && strings.HasPrefix(remote.URL, "http:") {
				warnings = append(warnings, "insecure-http")
			}
		}
	}
	return warnings
}
