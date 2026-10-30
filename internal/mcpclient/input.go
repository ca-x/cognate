package mcpclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("MCP input or response is invalid")
var ErrClosed = errors.New("MCP client or input is closed")
var ErrUnknown = errors.New("MCP mutation outcome is unknown; reconcile with list/get/read, never replay")

type private struct{}

func (private) String() string                 { return "[private MCP]" }
func (private) GoString() string               { return "[private MCP]" }
func (private) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[private MCP]") }
func (private) LogValue() slog.Value           { return slog.StringValue("[private MCP]") }
func (private) MarshalJSON() ([]byte, error)   { return []byte(`"[private MCP]"`), nil }

type Input struct {
	private
	state func() *inputState
}

type inputState struct {
	mutex     sync.Mutex
	closed    bool
	draft     draft
	mutations []mutation
	refs      map[string]string
}

type draft struct {
	Name     string    `json:"name"`
	AgentID  string    `json:"agentId"`
	Document *document `json:"document"`
}
type document struct {
	Servers []server `json:"servers"`
}
type server struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Enabled *bool   `json:"enabled"`
	Stdio   *stdio  `json:"stdio,omitzero"`
	HTTP    *remote `json:"http,omitzero"`
	SSE     *remote `json:"sse,omitzero"`
}
type stdio struct {
	Command string      `json:"command"`
	Args    *arguments  `json:"args,omitzero"`
	Env     *namedSlots `json:"env,omitzero"`
}
type arguments struct {
	Values []argument `json:"values"`
}
type argument struct {
	ID      string `json:"id"`
	SlotRef string `json:"slotRef"`
}
type namedSlots struct {
	Values []namedSlot `json:"values"`
}
type namedSlot struct {
	Name    string `json:"name"`
	SlotRef string `json:"slotRef"`
}
type remote struct {
	URL     string      `json:"url"`
	Headers *namedSlots `json:"headers,omitzero"`
}
type clearAction struct {
	Confirmed *bool `json:"confirmed"`
}
type mutation struct {
	SlotID  string       `json:"slotId"`
	Keep    *struct{}    `json:"keep,omitzero"`
	Replace *string      `json:"replace,omitzero"`
	Clear   *clearAction `json:"clear,omitzero"`
	value   []byte       `json:"-"`
	decoded bool         `json:"-"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func strictJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || token.Kind() == 'n' {
			return ErrInvalid
		}
		switch token.Kind() {
		case '{', '[':
			depth++
			if depth > 24 {
				return ErrInvalid
			}
		case '}', ']':
			depth--
		}
	}
	if err := json.Unmarshal(data, target, json.RejectUnknownMembers(true)); err != nil {
		return ErrInvalid
	}
	return nil
}

func LoadInput(ctx context.Context, declarationPath, secretsPath string) (*Input, error) {
	data, err := readInputFile(ctx, declarationPath, 512<<10, false)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	state := &inputState{}
	if strictJSON(data, &state.draft) != nil {
		return nil, ErrInvalid
	}
	state.refs, err = validateDraft(&state.draft)
	if err != nil {
		return nil, err
	}
	if secretsPath != "" {
		data, err := readInputFile(ctx, secretsPath, 1536<<10, true)
		if err != nil {
			return nil, err
		}
		defer clear(data)
		if strictJSON(data, &state.mutations) != nil {
			destroyMutations(state.mutations)
			return nil, ErrInvalid
		}
	}
	if err := validateMutations(state.mutations, state.refs, nil, false); err != nil {
		destroyMutations(state.mutations)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		destroyMutations(state.mutations)
		return nil, safeContext(err)
	}
	return &Input{state: func() *inputState { return state }}, nil
}

func safeContext(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ErrInvalid
}

func validateDraft(value *draft) (map[string]string, error) {
	if !validText(value.Name, 128) || value.AgentID != "claude" || value.Document == nil || len(value.Document.Servers) > 32 {
		return nil, ErrInvalid
	}
	refs := map[string]string{}
	ids, names := map[string]bool{}, map[string]bool{}
	add := func(ref, expected, purpose string) error {
		if ref != expected || len(ref) > 128 || refs[ref] != "" {
			return ErrInvalid
		}
		refs[ref] = purpose
		if len(refs) > 512 {
			return ErrInvalid
		}
		return nil
	}
	named := func(id, purpose string, entries *namedSlots, maximum int) error {
		if entries == nil {
			return nil
		}
		if entries.Values == nil {
			entries.Values = []namedSlot{}
		}
		if len(entries.Values) > maximum {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, entry := range entries.Values {
			key := entry.Name
			valid := environmentName.MatchString(key)
			if purpose == "header" {
				valid = headerName.MatchString(key)
				key = strings.ToLower(key)
			}
			if !valid || len(entry.Name) > 128 || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
			if err := add(entry.SlotRef, purpose+"/"+id+"/"+entry.Name, purpose); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Document.Servers == nil {
		value.Document.Servers = []server{}
	}
	for _, entry := range value.Document.Servers {
		if !identifier.MatchString(entry.ID) || !identifier.MatchString(entry.Name) || ids[entry.ID] || names[entry.Name] || entry.Enabled == nil {
			return nil, ErrInvalid
		}
		ids[entry.ID], names[entry.Name] = true, true
		connections := 0
		if entry.Stdio != nil {
			connections++
			if len(entry.Stdio.Command) < 1 || len(entry.Stdio.Command) > 4096 || !utf8.ValidString(entry.Stdio.Command) || strings.ContainsRune(entry.Stdio.Command, 0) {
				return nil, ErrInvalid
			}
			if entry.Stdio.Args != nil {
				if entry.Stdio.Args.Values == nil {
					entry.Stdio.Args.Values = []argument{}
				}
				if len(entry.Stdio.Args.Values) > 64 {
					return nil, ErrInvalid
				}
				for _, arg := range entry.Stdio.Args.Values {
					if !identifier.MatchString(arg.ID) || add(arg.SlotRef, "arg/"+entry.ID+"/"+arg.ID, "arg") != nil {
						return nil, ErrInvalid
					}
				}
			}
			if named(entry.ID, "env", entry.Stdio.Env, 64) != nil {
				return nil, ErrInvalid
			}
		}
		for _, connection := range []*remote{entry.HTTP, entry.SSE} {
			if connection == nil {
				continue
			}
			connections++
			address, err := url.Parse(connection.URL)
			if err != nil || len(connection.URL) > 4096 || !utf8.ValidString(connection.URL) || address.Hostname() == "" || address.User != nil || address.Fragment != "" || strings.ContainsRune(connection.URL, '#') || address.RawQuery != "" || address.ForceQuery || (address.Scheme != "http" && address.Scheme != "https") {
				return nil, ErrInvalid
			}
			if named(entry.ID, "header", connection.Headers, 32) != nil {
				return nil, ErrInvalid
			}
		}
		if connections != 1 {
			return nil, ErrInvalid
		}
	}
	encoded, err := json.Marshal(value.Document)
	defer clear(encoded)
	if err != nil || len(encoded)+len(refs) > 64<<10 {
		return nil, ErrInvalid
	}
	return refs, nil
}

func validText(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.ValidString(value) && value == strings.TrimSpace(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validateMutations(values []mutation, desired, previous map[string]string, enforceUnion bool) error {
	if len(values) > 1024 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	total := 0
	for index := range values {
		value := &values[index]
		if !validRef(value.SlotID) || seen[value.SlotID] {
			return ErrInvalid
		}
		seen[value.SlotID] = true
		purpose, exists := desired[value.SlotID]
		oldPurpose, old := previous[value.SlotID]
		if exists && old && purpose != oldPurpose {
			return ErrInvalid
		}
		actions := 0
		if value.Keep != nil {
			actions++
			if enforceUnion && !exists {
				return ErrInvalid
			}
		}
		if value.Replace != nil {
			actions++
			if enforceUnion && !exists {
				return ErrInvalid
			}
			if !value.decoded {
				decoded, err := base64.StdEncoding.Strict().DecodeString(*value.Replace)
				if err != nil || base64.StdEncoding.EncodeToString(decoded) != *value.Replace || len(decoded) > 8<<10 || !utf8.Valid(decoded) || bytes.ContainsRune(decoded, 0) {
					clear(decoded)
					return ErrInvalid
				}
				clear(value.value)
				value.value = decoded
				value.Replace = new("")
				value.decoded = true
			}
			total += len(value.value)
			if purpose == "header" && bytes.ContainsAny(value.value, "\r\n") {
				return ErrInvalid
			}
		}
		if value.Clear != nil {
			actions++
			if value.Clear.Confirmed == nil || !*value.Clear.Confirmed || enforceUnion && !exists && !old {
				return ErrInvalid
			}
		}
		if actions != 1 || total > 128<<10 {
			return ErrInvalid
		}
	}
	if enforceUnion {
		union := len(desired)
		for ref := range previous {
			if _, exists := desired[ref]; exists {
				continue
			}
			union++
			cleared := false
			for _, value := range values {
				if value.SlotID == ref && value.Clear != nil && value.Clear.Confirmed != nil && *value.Clear.Confirmed {
					cleared = true
				}
			}
			if !cleared {
				return ErrInvalid
			}
		}
		if union > 1024 {
			return ErrInvalid
		}
	}
	return nil
}

func validRef(value string) bool {
	parts := strings.Split(value, "/")
	if len(value) > 128 || len(parts) != 3 || !identifier.MatchString(parts[1]) {
		return false
	}
	switch parts[0] {
	case "arg":
		return identifier.MatchString(parts[2])
	case "env":
		return len(parts[2]) <= 128 && environmentName.MatchString(parts[2])
	case "header":
		return len(parts[2]) <= 128 && headerName.MatchString(parts[2])
	default:
		return false
	}
}

func destroyMutations(values []mutation) {
	for index := range values {
		clear(values[index].value)
		values[index] = mutation{}
	}
	clear(values)
}

func (input *Input) Close() {
	if input == nil || input.state == nil {
		return
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	destroyMutations(state.mutations)
	state.mutations = nil
	state.refs = nil
	state.draft = draft{}
	state.closed = true
}

func (input *Input) ValidateUpdate(previous *Input) error {
	refs := map[string]string{}
	if previous != nil {
		if previous.state == nil {
			return ErrClosed
		}
		state := previous.state()
		state.mutex.Lock()
		if state.closed {
			state.mutex.Unlock()
			return ErrClosed
		}
		for ref, purpose := range state.refs {
			refs[ref] = purpose
		}
		state.mutex.Unlock()
	}
	if input == nil || input.state == nil {
		return ErrClosed
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return ErrClosed
	}
	return validateMutations(state.mutations, state.refs, refs, true)
}

func (input *Input) WriteSummary(ctx context.Context, writer io.Writer) error {
	if input == nil || input.state == nil || writer == nil {
		return ErrInvalid
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return safeContext(err)
	}
	data, err := json.Marshal(state.draft)
	if err != nil {
		return ErrInvalid
	}
	defer clear(data)
	var display bytes.Buffer
	for _, character := range string(data) {
		if unicode.Is(unicode.Bidi_Control, character) || character == '\u2028' || character == '\u2029' {
			fmt.Fprintf(&display, "\\u%04x", character)
		} else {
			display.WriteRune(character)
		}
	}
	data = display.Bytes()
	defer clear(data)
	if len(data) > 512<<10 {
		return ErrInvalid
	}
	if count, err := writer.Write(data); err != nil || count != len(data) {
		return ErrInvalid
	}
	for _, value := range state.mutations {
		action := "KEEP"
		if value.Replace != nil {
			action = "REPLACE"
		}
		if value.Clear != nil {
			action = "CLEAR confirmed"
		}
		if _, err := fmt.Fprintf(writer, "\nSLOT %q %s", value.SlotID, action); err != nil {
			return ErrInvalid
		}
	}
	return nil
}
