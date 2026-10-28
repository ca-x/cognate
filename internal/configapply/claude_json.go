package configapply

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
)

var (
	ErrInvalidSettings = errors.New("invalid Claude settings")
	ErrInvalidPatch    = errors.New("invalid Claude settings patch")
	ErrInvalidSecret   = errors.New("invalid Claude secret material")
	ErrSettingsLimit   = errors.New("Claude settings limit exceeded")
)

type claudePayload struct{ data *[]byte }
type SecretMaterial struct{ payload *claudePayload }
type SettingsImage struct{ payload *claudePayload }

func (payload *claudePayload) bytes() []byte {
	if payload == nil || payload.data == nil {
		return nil
	}
	return *payload.data
}

func NewSecretMaterial(data []byte) (SecretMaterial, error) {
	if !validSecret(data) {
		return SecretMaterial{}, ErrInvalidSecret
	}
	return SecretMaterial{payload: &claudePayload{data: new(bytes.Clone(data))}}, nil
}

func validSecret(data []byte) bool {
	return len(data) > 0 && len(data) <= 8192 && utf8.Valid(data) && !bytes.ContainsAny(data, "\x00\r\n")
}

func (secret SecretMaterial) CopyBytes() []byte { return bytes.Clone(secret.payload.bytes()) }
func (image SettingsImage) CopyBytes() []byte   { return bytes.Clone(image.payload.bytes()) }
func (SecretMaterial) String() string           { return "[redacted secret material]" }
func (SecretMaterial) GoString() string         { return "[redacted secret material]" }
func (SettingsImage) String() string            { return "[redacted settings image]" }
func (SettingsImage) GoString() string          { return "[redacted settings image]" }
func (SecretMaterial) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted secret material]")
}
func (SettingsImage) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted settings image]")
}
func (SecretMaterial) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted secret material]"`), nil
}
func (SettingsImage) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted settings image]"`), nil
}
func (SecretMaterial) MarshalText() ([]byte, error) {
	return []byte("[redacted secret material]"), nil
}
func (SettingsImage) MarshalText() ([]byte, error) {
	return []byte("[redacted settings image]"), nil
}
func (SecretMaterial) LogValue() slog.Value { return slog.StringValue("[redacted secret material]") }
func (SettingsImage) LogValue() slog.Value  { return slog.StringValue("[redacted settings image]") }

func RewriteClaudeSettings(base []byte, patch agentconfig.ClaudeProviderPatchV1, credentials map[agentconfig.ClaudeSecretSlot]SecretMaterial) (SettingsImage, error) {
	values, err := patchValues(patch, credentials)
	if err != nil {
		return SettingsImage{}, err
	}
	defer clearValues(values)
	document, err := readSettings(base)
	if err != nil {
		return SettingsImage{}, err
	}
	if document.envIndex < 0 {
		selected := false
		for _, value := range values {
			selected = selected || value != nil
		}
		if !selected {
			return SettingsImage{payload: &claudePayload{data: new(bytes.Clone(base))}}, nil
		}
		document.environment = jsonObject{raw: []byte("{}"), openEnd: 1, closeStart: 1}
	}
	order := make([]string, 0, len(claudeSlots))
	for _, slot := range claudeSlots {
		order = append(order, string(slot))
	}
	environment := rewriteObject(document.environment, values, order)
	defer clear(environment)
	result := rewriteObject(document.root, map[string][]byte{"env": environment}, []string{"env"})
	if _, err := readSettings(result); err != nil {
		clear(result)
		return SettingsImage{}, err
	}
	return SettingsImage{payload: &claudePayload{data: new(result)}}, nil
}

var claudeSlots = [...]agentconfig.ClaudeEnvSlot{
	agentconfig.ClaudeEnvAPIKey, agentconfig.ClaudeEnvAuthToken, agentconfig.ClaudeEnvBaseURL,
	agentconfig.ClaudeEnvModel, agentconfig.ClaudeEnvHaikuModel, agentconfig.ClaudeEnvSonnetModel, agentconfig.ClaudeEnvOpusModel,
}

func patchValues(patch agentconfig.ClaudeProviderPatchV1, credentials map[agentconfig.ClaudeSecretSlot]SecretMaterial) (map[string][]byte, error) {
	if patch.Target != agentconfig.ClaudeSettings || len(patch.Operations) != len(claudeSlots) {
		return nil, ErrInvalidPatch
	}
	selected := 0
	for index, operation := range patch.Operations {
		if operation.Slot != claudeSlots[index] {
			return nil, ErrInvalidPatch
		}
		switch operation.Action {
		case agentconfig.ClaudeRemove:
			if operation.Literal != nil || operation.Reference != nil {
				return nil, ErrInvalidPatch
			}
		case agentconfig.ClaudeSetLiteral:
			if index < 2 || operation.Reference != nil || operation.Literal == nil || !validLiteral(index, *operation.Literal) {
				return nil, ErrInvalidPatch
			}
		case agentconfig.ClaudeSetSelectedSecret:
			if index > 1 || operation.Literal != nil || operation.Reference == nil {
				return nil, ErrInvalidPatch
			}
			expected := agentconfig.ClaudeAPIKeySecret
			if index == 1 {
				expected = agentconfig.ClaudeAuthTokenSecret
			}
			if *operation.Reference != expected {
				return nil, ErrInvalidPatch
			}
			material, exists := credentials[expected]
			if !exists || !validSecret(material.payload.bytes()) {
				return nil, ErrInvalidSecret
			}
			selected++
		default:
			return nil, ErrInvalidPatch
		}
	}
	if selected > 1 || selected == 0 && patch.Operations[2].Action != agentconfig.ClaudeRemove {
		return nil, ErrInvalidPatch
	}
	if len(credentials) != selected {
		return nil, ErrInvalidSecret
	}
	values := make(map[string][]byte, len(claudeSlots))
	for _, operation := range patch.Operations {
		var value []byte
		var err error
		switch operation.Action {
		case agentconfig.ClaudeSetLiteral:
			value, err = jsontext.AppendQuote(nil, *operation.Literal)
		case agentconfig.ClaudeSetSelectedSecret:
			value, err = jsontext.AppendQuote(nil, credentials[*operation.Reference].payload.bytes())
		}
		if err != nil {
			clearValues(values)
			return nil, ErrInvalidPatch
		}
		values[string(operation.Slot)] = value
	}
	return values, nil
}

func clearValues(values map[string][]byte) {
	for _, value := range values {
		clear(value)
	}
}

func validLiteral(index int, value string) bool {
	limit := 256
	if index == 2 {
		limit = 2048
	}
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	if index != 2 {
		return true
	}
	endpoint, err := url.Parse(value)
	return err == nil && !strings.ContainsAny(value, "?#") && (endpoint.Scheme == "http" || endpoint.Scheme == "https") && endpoint.Host != "" && endpoint.Hostname() != "" && endpoint.User == nil
}

type jsonMember struct {
	name       string
	keyStart   int
	valueStart int
	valueEnd   int
}

type jsonObject struct {
	raw        []byte
	openEnd    int
	closeStart int
	members    []jsonMember
}

type settingsDocument struct {
	root        jsonObject
	environment jsonObject
	envIndex    int
}

func readSettings(base []byte) (settingsDocument, error) {
	if len(base) > agentconfig.MaxConfigBytes {
		return settingsDocument{}, ErrSettingsLimit
	}
	root, err := readObject(base, 512)
	if err != nil {
		return settingsDocument{}, err
	}
	document := settingsDocument{root: root, envIndex: -1}
	for index, member := range root.members {
		if member.name != "env" {
			continue
		}
		raw := base[member.valueStart:member.valueEnd]
		if len(raw) > agentconfig.MaxEnvironmentBytes {
			return settingsDocument{}, ErrSettingsLimit
		}
		environment, err := readObject(raw, agentconfig.MaxEnvironmentEntries)
		if err != nil {
			return settingsDocument{}, err
		}
		if err := validateEnvironment(environment); err != nil {
			return settingsDocument{}, err
		}
		document.environment, document.envIndex = environment, index
	}
	return document, nil
}

func validateEnvironment(environment jsonObject) error {
	for _, member := range environment.members {
		raw := environment.raw[member.valueStart:member.valueEnd]
		if raw[0] == '"' {
			decoded, err := jsontext.AppendUnquote(nil, raw)
			length := len(decoded)
			clear(decoded)
			if err != nil {
				return ErrInvalidSettings
			}
			if length > agentconfig.MaxEnvironmentValueBytes {
				return ErrSettingsLimit
			}
		} else {
			for _, slot := range claudeSlots {
				if member.name == string(slot) {
					return ErrInvalidSettings
				}
			}
			if len(raw) > agentconfig.MaxEnvironmentValueBytes {
				return ErrSettingsLimit
			}
		}
	}
	return nil
}

func readObject(raw []byte, limit int) (jsonObject, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.ReadToken()
	if err != nil || opening.Kind() != '{' {
		return jsonObject{}, ErrInvalidSettings
	}
	object := jsonObject{raw: raw, openEnd: int(decoder.InputOffset())}
	for decoder.PeekKind() != '}' {
		before := int(decoder.InputOffset())
		key, err := decoder.ReadToken()
		if err != nil || key.Kind() != '"' {
			return jsonObject{}, ErrInvalidSettings
		}
		name := key.String()
		value, err := decoder.ReadValue()
		if err != nil {
			return jsonObject{}, ErrInvalidSettings
		}
		end := int(decoder.InputOffset())
		object.members = append(object.members, jsonMember{name: name, keyStart: spanStart(raw, before), valueStart: end - len(value), valueEnd: end})
		if len(object.members) > limit {
			return jsonObject{}, ErrSettingsLimit
		}
	}
	object.closeStart = spanStart(raw, int(decoder.InputOffset()))
	if _, err := decoder.ReadToken(); err != nil {
		return jsonObject{}, ErrInvalidSettings
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return jsonObject{}, ErrInvalidSettings
	}
	return object, nil
}

func spanStart(raw []byte, offset int) int {
	for offset < len(raw) && bytes.ContainsRune([]byte(" \t\r\n,"), rune(raw[offset])) {
		offset++
	}
	return offset
}

func rewriteObject(object jsonObject, updates map[string][]byte, order []string) []byte {
	result := make([]byte, 0, len(object.raw))
	result = append(result, object.raw[:object.openEnd]...)
	prefixEnd := object.closeStart
	if len(object.members) != 0 {
		prefixEnd = object.members[0].keyStart
	}
	result = append(result, object.raw[object.openEnd:prefixEnd]...)
	count := 0
	seen := make(map[string]bool, len(updates))
	for index, member := range object.members {
		value, replaced := updates[member.name]
		if replaced {
			seen[member.name] = true
			if value == nil {
				continue
			}
		} else {
			value = object.raw[member.valueStart:member.valueEnd]
		}
		if count > 0 {
			result = append(result, object.raw[object.members[index-1].valueEnd:member.keyStart]...)
		}
		result = append(result, object.raw[member.keyStart:member.valueStart]...)
		result = append(result, value...)
		count++
	}
	for _, name := range order {
		value := updates[name]
		if seen[name] || value == nil {
			continue
		}
		if count > 0 {
			result = append(result, ',')
		}
		result, _ = jsontext.AppendQuote(result, name)
		result = append(result, ':')
		result = append(result, value...)
		count++
	}
	suffixStart := object.closeStart
	if len(object.members) > 0 {
		suffixStart = object.members[len(object.members)-1].valueEnd
	}
	return append(result, object.raw[suffixStart:]...)
}
