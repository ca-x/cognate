package grokclient

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	ErrInvalid     = errors.New("invalid Grok input or response")
	ErrUnsupported = errors.New("Grok asset operation unsupported")
	ErrClosed      = errors.New("Grok client or operation closed")
	ErrDenied      = errors.New("Grok owner session denied")
	ErrConflict    = errors.New("Grok asset or order changed; read and review again")
	ErrInUse       = errors.New("Grok asset is referenced by a Pack")
	ErrUnavailable = errors.New("Grok asset service unavailable")
	ErrUnknown     = errors.New("Grok mutation outcome unknown; never replay; reconcile with reads")
	ErrBusy        = errors.New("Grok client busy")
	ErrExpired     = errors.New("Grok review expired; prepare and review again")
)

const keySlot = "grokbuild.api-key"
const maxContext int64 = 1<<53 - 1

type KeyAction uint8

const (
	NoKeyMutation KeyAction = iota
	KeepKey
	ReplaceKey
	ClearKey
)

type private struct{}

func (private) String() string   { return "[private Grok asset]" }
func (private) GoString() string { return "[private Grok asset]" }
func (private) Format(writer fmt.State, _ rune) {
	_, _ = io.WriteString(writer, "[private Grok asset]")
}
func (private) MarshalJSON() ([]byte, error) { return []byte(`"[private Grok asset]"`), nil }
func (private) MarshalText() ([]byte, error) { return []byte("[private Grok asset]"), nil }
func (private) LogValue() slog.Value         { return slog.StringValue("[private Grok asset]") }

type Input struct {
	private
	state func() *inputState
}
type inputState struct {
	mutex  sync.Mutex
	closed bool
	draft  *configurationv1.Draft
	action KeyAction
	key    []byte
}

func LoadInput(ctx context.Context, draftPath string, action KeyAction, keyPath string) (*Input, error) {
	if ctx == nil || action > ClearKey || action == ReplaceKey && keyPath == "" || action != ReplaceKey && keyPath != "" {
		return nil, ErrInvalid
	}
	data, err := readInputFile(ctx, draftPath, 64<<10, false)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	draft := new(configurationv1.Draft)
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	if !utf8.Valid(data) || !scanDraftJSON(decoder, draft.ProtoReflect().Descriptor(), 0) {
		return nil, ErrInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	if protojson.Unmarshal(data, draft) != nil || validateDraft(draft) != nil {
		return nil, ErrInvalid
	}
	var key []byte
	if action == ReplaceKey {
		key, err = readInputFile(ctx, keyPath, 8192, true)
		if err != nil {
			return nil, err
		}
		if !validKey(key) || draft.GetGrokbuildProvider().Mode == configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN {
			clear(key)
			return nil, ErrInvalid
		}
	}
	if ctx.Err() != nil {
		clear(key)
		return nil, ctx.Err()
	}
	state := &inputState{draft: draft, action: action, key: key}
	return &Input{state: func() *inputState { return state }}, nil
}

func scanDraftJSON(decoder *jsontext.Decoder, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 16 {
		return false
	}
	token, err := decoder.ReadToken()
	if err != nil || token.Kind() != '{' {
		return false
	}
	seen := map[protoreflect.FieldNumber]bool{}
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			return false
		}
		field := descriptor.Fields().ByJSONName(name.String())
		if field == nil || seen[field.Number()] || depth == 0 && field.JSONName() != "name" && field.JSONName() != "grokbuildProvider" {
			return false
		}
		seen[field.Number()] = true
		if field.Kind() == protoreflect.MessageKind {
			if !scanDraftJSON(decoder, field.Message(), depth+1) {
				return false
			}
			continue
		}
		value, err := decoder.ReadToken()
		if err != nil || value.Kind() != '"' {
			return false
		}
		switch field.Kind() {
		case protoreflect.StringKind:
		case protoreflect.EnumKind:
			if field.Enum().Values().ByName(protoreflect.Name(value.String())) == nil {
				return false
			}
		case protoreflect.Int64Kind:
			parsed, err := strconv.ParseInt(value.String(), 10, 64)
			if err != nil || strconv.FormatInt(parsed, 10) != value.String() {
				return false
			}
		default:
			return false
		}
	}
	token, err = decoder.ReadToken()
	return err == nil && token.Kind() == '}'
}

func (input *Input) Close() {
	if input == nil || input.state == nil {
		return
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	clear(state.key)
	state.key = nil
	state.draft = nil
	state.closed = true
}

func cloneInput(input *Input) (*Input, error) {
	if input == nil || input.state == nil {
		return nil, ErrInvalid
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, ErrClosed
	}
	copyState := &inputState{draft: proto.Clone(state.draft).(*configurationv1.Draft), action: state.action, key: bytes.Clone(state.key)}
	return &Input{state: func() *inputState { return copyState }}, nil
}

func ReadOrderFile(ctx context.Context, path string) ([]string, error) {
	data, err := readInputFile(ctx, path, 64<<10, false)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	var ids []string
	if json.Unmarshal(data, &ids) != nil || !validIDs(ids) {
		return nil, ErrInvalid
	}
	return ids, nil
}

func validIDs(ids []string) bool {
	if ids == nil || len(ids) > 250 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func validID(value string) bool {
	if len(value) != 26 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return false
		}
	}
	return true
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func validKey(value []byte) bool {
	return len(value) > 0 && len(value) <= 8192 && utf8.Valid(value) && !bytes.ContainsAny(value, "\x00\r\n")
}
func validateDraft(draft *configurationv1.Draft) error {
	if draft == nil || !validText(strings.TrimSpace(draft.Name), 128) {
		return ErrInvalid
	}
	payload, ok := draft.Payload.(*configurationv1.Draft_GrokbuildProvider)
	if !ok || payload == nil || payload.GrokbuildProvider == nil {
		return ErrInvalid
	}
	provider := payload.GrokbuildProvider
	switch provider.Mode {
	case configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN:
		if provider.Model != nil {
			return ErrInvalid
		}
		return nil
	case configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL:
	default:
		return ErrInvalid
	}
	model := provider.Model
	if model == nil || !validText(model.Profile, 128) || !validText(model.UpstreamModel, 256) || !validText(model.Name, 128) || model.ApiBackend != "responses" || model.ContextWindow < 1 || model.ContextWindow > maxContext || !validEndpoint(model.Endpoint) {
		return ErrInvalid
	}
	if model.EnvKey != nil {
		value := *model.EnvKey
		if len(value) == 0 || len(value) > 128 {
			return ErrInvalid
		}
		for index, letter := range []byte(value) {
			alpha := letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter == '_'
			if !alpha && (index == 0 || letter < '0' || letter > '9') {
				return ErrInvalid
			}
		}
	}
	return nil
}
func validEndpoint(value string) bool {
	if !validText(value, 2048) || strings.ContainsAny(value, "?#\\") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.HasSuffix(parsed.Host, ":") || strings.ContainsAny(parsed.Path, "\\") || strings.ContainsFunc(parsed.Path, unicode.IsControl) || !utf8.ValidString(parsed.Path) {
		return false
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}
