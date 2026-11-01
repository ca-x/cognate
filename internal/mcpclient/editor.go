package mcpclient

import (
	"bytes"
	"context"
	"unicode/utf8"
)

type Draft struct {
	private
	Name, AgentID string
	Servers       []Server
}

type Server struct {
	ID, Name string
	Enabled  bool
	Stdio    *Stdio
	HTTP     *Remote
	SSE      *Remote
}

type Stdio struct {
	Command string
	Args    *Arguments
	Env     *NamedSlots
}

type Arguments struct{ Values []Argument }
type Argument struct{ ID, SlotRef string }
type NamedSlots struct{ Values []NamedSlot }
type NamedSlot struct{ Name, SlotRef string }
type Remote struct {
	URL     string
	Headers *NamedSlots
}

type SecretAction uint8

const (
	Keep SecretAction = iota + 1
	Replace
	Clear
)

type SecretEdit struct {
	private
	SlotID      string
	Action      SecretAction
	Replacement []byte
	Confirmed   bool
}

type SlotState struct {
	SlotID  string
	Present bool
}

type EditView struct {
	private
	Metadata        Metadata
	Draft           Draft
	Secrets         []SlotState
	ReadOnly        bool
	NeedsCredential bool
}

func editorNamed(values *NamedSlots) (*namedSlots, error) {
	if values == nil {
		return nil, nil
	}
	if len(values.Values) > 64 {
		return nil, ErrInvalid
	}
	result := &namedSlots{Values: make([]namedSlot, len(values.Values))}
	for index, value := range values.Values {
		result.Values[index] = namedSlot{Name: value.Name, SlotRef: value.SlotRef}
	}
	return result, nil
}

func editorRemote(value *Remote) (*remote, error) {
	if value == nil {
		return nil, nil
	}
	headers, err := editorNamed(value.Headers)
	return &remote{URL: value.URL, Headers: headers}, err
}

func editorDeclaration(value Draft) (draft, error) {
	result := draft{Name: value.Name, AgentID: value.AgentID, Document: &document{}}
	if len(value.Servers) > 32 {
		return result, ErrInvalid
	}
	result.Document.Servers = make([]server, len(value.Servers))
	for index, source := range value.Servers {
		entry := server{ID: source.ID, Name: source.Name, Enabled: new(source.Enabled)}
		if source.Stdio != nil {
			entry.Stdio = &stdio{Command: source.Stdio.Command}
			if source.Stdio.Args != nil {
				if len(source.Stdio.Args.Values) > 64 {
					return result, ErrInvalid
				}
				entry.Stdio.Args = &arguments{Values: make([]argument, len(source.Stdio.Args.Values))}
				for argumentIndex, value := range source.Stdio.Args.Values {
					entry.Stdio.Args.Values[argumentIndex] = argument{ID: value.ID, SlotRef: value.SlotRef}
				}
			}
			var err error
			entry.Stdio.Env, err = editorNamed(source.Stdio.Env)
			if err != nil {
				return result, err
			}
		}
		var err error
		entry.HTTP, err = editorRemote(source.HTTP)
		if err != nil {
			return result, err
		}
		entry.SSE, err = editorRemote(source.SSE)
		if err != nil {
			return result, err
		}
		result.Document.Servers[index] = entry
	}
	return result, nil
}

func NewInput(ctx context.Context, value Draft, edits []SecretEdit) (*Input, error) {
	if ctx == nil || len(edits) > 1024 {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, safeContext(err)
	}
	declaration, err := editorDeclaration(value)
	if err != nil {
		return nil, err
	}
	refs, err := validateDraft(&declaration)
	if err != nil {
		return nil, err
	}
	state := &inputState{draft: declaration, refs: refs}
	input := &Input{state: func() *inputState { return state }}
	valid := false
	defer func() {
		if !valid {
			input.Close()
		}
	}()
	var total int
	for _, edit := range edits {
		entry := mutation{SlotID: edit.SlotID}
		if len(edit.Replacement) > 8<<10 || len(edit.Replacement) > (128<<10)-total || !utf8.Valid(edit.Replacement) || bytes.ContainsRune(edit.Replacement, 0) {
			return nil, ErrInvalid
		}
		switch edit.Action {
		case Keep:
			if len(edit.Replacement) != 0 || edit.Confirmed {
				return nil, ErrInvalid
			}
			entry.Keep = &struct{}{}
		case Replace:
			if edit.Confirmed {
				return nil, ErrInvalid
			}
			entry.Replace, entry.decoded, entry.value = new(""), true, bytes.Clone(edit.Replacement)
			total += len(entry.value)
		case Clear:
			if len(edit.Replacement) != 0 || !edit.Confirmed {
				return nil, ErrInvalid
			}
			entry.Clear = &clearAction{Confirmed: new(true)}
		default:
			return nil, ErrInvalid
		}
		state.mutations = append(state.mutations, entry)
	}
	if err := validateMutations(state.mutations, refs, nil, false); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, safeContext(err)
	}
	valid = true
	return input, nil
}

func editableNamed(value *namedSlots) *NamedSlots {
	if value == nil {
		return nil
	}
	result := &NamedSlots{Values: make([]NamedSlot, len(value.Values))}
	for index, entry := range value.Values {
		result.Values[index] = NamedSlot{Name: entry.Name, SlotRef: entry.SlotRef}
	}
	return result
}

func editableRemote(value *remote) *Remote {
	if value == nil {
		return nil
	}
	return &Remote{URL: value.URL, Headers: editableNamed(value.Headers)}
}

func (view *View) Editable(ctx context.Context) (EditView, error) {
	if ctx == nil || view == nil || view.state == nil {
		return EditView{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, safeContext(err)
	}
	state := view.state()
	result := EditView{Metadata: state.metadata, ReadOnly: state.readonly || !state.metadata.Editable()}
	if state.draft.Document == nil {
		return result, nil
	}
	result.Draft = Draft{Name: state.draft.Name, AgentID: state.draft.AgentID, Servers: make([]Server, len(state.draft.Document.Servers))}
	for index, source := range state.draft.Document.Servers {
		entry := Server{ID: source.ID, Name: source.Name, Enabled: *source.Enabled, HTTP: editableRemote(source.HTTP), SSE: editableRemote(source.SSE)}
		if source.Stdio != nil {
			entry.Stdio = &Stdio{Command: source.Stdio.Command, Env: editableNamed(source.Stdio.Env)}
			if source.Stdio.Args != nil {
				entry.Stdio.Args = &Arguments{Values: make([]Argument, len(source.Stdio.Args.Values))}
				for argumentIndex, value := range source.Stdio.Args.Values {
					entry.Stdio.Args.Values[argumentIndex] = Argument{ID: value.ID, SlotRef: value.SlotRef}
				}
			}
		}
		result.Draft.Servers[index] = entry
	}
	for _, ref := range orderedRefs(state.draft) {
		result.Secrets = append(result.Secrets, SlotState{SlotID: ref, Present: state.presence[ref]})
		result.NeedsCredential = result.NeedsCredential || !state.presence[ref]
	}
	return result, nil
}

func (input *Input) ChangedSlots(ctx context.Context) ([]string, error) {
	if ctx == nil || input == nil || input.state == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, safeContext(err)
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, ErrClosed
	}
	var result []string
	for _, value := range state.mutations {
		if value.Replace != nil || value.Clear != nil {
			result = append(result, value.SlotID)
		}
	}
	return result, nil
}
