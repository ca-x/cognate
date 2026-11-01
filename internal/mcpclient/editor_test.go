package mcpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func editorDraft() Draft {
	return Draft{Name: "Native MCP", AgentID: "claude", Servers: []Server{{ID: "tools", Name: "tools", Enabled: false, Stdio: &Stdio{Command: "not-executed", Args: &Arguments{Values: []Argument{{ID: "first", SlotRef: "arg/tools/first"}}}, Env: &NamedSlots{Values: []NamedSlot{}}}}, {ID: "remote", Name: "remote", Enabled: true, HTTP: &Remote{URL: "https://example.invalid/mcp"}}}}
}

func TestEditorInputOwnershipPresenceAndClear(test *testing.T) {
	draft := editorDraft()
	value := []byte("synthetic-editor-private")
	input, err := NewInput(test.Context(), draft, []SecretEdit{{SlotID: "arg/tools/first", Action: Replace, Replacement: value}})
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	draft.Servers[0].Stdio.Command = "changed"
	draft.Servers[0].Stdio.Args.Values[0].ID = "changed"
	clear(value)
	if input.state().draft.Document.Servers[0].Stdio.Command != "not-executed" || *input.state().draft.Document.Servers[0].Enabled || input.state().draft.Document.Servers[0].Stdio.Env == nil || input.state().draft.Document.Servers[1].HTTP.Headers != nil {
		test.Fatal("borrowed draft or collection presence changed")
	}
	owned := input.state().mutations[0].value
	if string(owned) != "synthetic-editor-private" {
		test.Fatal("replacement was borrowed")
	}
	var summary bytes.Buffer
	if err := input.WriteSummary(test.Context(), &summary); err != nil || strings.Contains(summary.String(), "synthetic-editor-private") || !strings.Contains(summary.String(), "REPLACE") {
		test.Fatal("unsafe summary", err)
	}
	for _, text := range []string{fmt.Sprintf("%+v", input), fmt.Sprintf("%+v", SecretEdit{Replacement: []byte("synthetic-editor-private")})} {
		if strings.Contains(text, "synthetic-editor-private") {
			test.Fatal("format exposed replacement")
		}
	}
	input.Close()
	if !bytes.Equal(owned, make([]byte, len(owned))) {
		test.Fatal("owned replacement not cleared")
	}
}

func TestEditorInputOldOnlyReferencesRequireExplicitClear(test *testing.T) {
	before, err := NewInput(test.Context(), editorDraft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	defer before.Close()
	after := editorDraft()
	after.Servers[0].Stdio.Args = nil
	for _, action := range []SecretEdit{{}, {SlotID: "arg/tools/first", Action: Keep}, {SlotID: "arg/tools/first", Action: Clear}} {
		input, err := NewInput(test.Context(), after, []SecretEdit{action})
		if err == nil {
			err = input.ValidateUpdate(before)
			input.Close()
		}
		if !errors.Is(err, ErrInvalid) {
			test.Fatal("removed reference accepted without explicit confirmed CLEAR")
		}
	}
	input, err := NewInput(test.Context(), after, []SecretEdit{{SlotID: "arg/tools/first", Action: Clear, Confirmed: true}})
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	if err := input.ValidateUpdate(before); err != nil {
		test.Fatal(err)
	}
}

func TestEditorInputRejectsBoundsAndAmbiguousValues(test *testing.T) {
	for _, edits := range [][]SecretEdit{{{SlotID: "arg/tools/first", Action: Replace, Replacement: bytes.Repeat([]byte("a"), 8193)}}, {{SlotID: "arg/tools/first", Action: Replace, Replacement: []byte{0xff}}}, {{SlotID: "arg/tools/first", Action: Keep, Replacement: []byte("not-keep")}}, {{SlotID: "arg/tools/first", Action: Clear}}, {{SlotID: "foreign", Action: Keep}}} {
		if input, err := NewInput(test.Context(), editorDraft(), edits); !errors.Is(err, ErrInvalid) {
			if input != nil {
				input.Close()
			}
			test.Fatal("invalid memory input admitted")
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := NewInput(ctx, editorDraft(), nil); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation lost")
	}
	if _, err := NewInput(nil, editorDraft(), nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil context")
	}
	draft := editorDraft()
	draft.Servers[1].HTTP.URL += "?secret=not-supported"
	if _, err := NewInput(test.Context(), draft, nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("query stripped or accepted")
	}
	input, err := NewInput(test.Context(), editorDraft(), []SecretEdit{{SlotID: "arg/tools/first", Action: Replace}})
	if err != nil || input.state().mutations[0].Replace == nil {
		test.Fatal("present empty replacement lost", err)
	}
	input.Close()
}

func TestEditorViewOwnsDeclarationAndKeepsReadOnly(test *testing.T) {
	declaration, err := editorDeclaration(editorDraft())
	if err != nil {
		test.Fatal(err)
	}
	state := &viewState{metadata: Metadata{Agent: "claude", Schema: 1, Revision: 7}, draft: declaration, presence: map[string]bool{"arg/tools/first": true}}
	view := &View{state: func() *viewState { return state }}
	first, err := view.Editable(test.Context())
	if err != nil || first.ReadOnly || first.NeedsCredential || !first.Secrets[0].Present {
		test.Fatal("editable presence projection", err)
	}
	first.Draft.Servers[0].Stdio.Args.Values[0].ID = "mutated"
	first.Draft.Servers[0].Stdio.Env.Values = append(first.Draft.Servers[0].Stdio.Env.Values, NamedSlot{Name: "MUTATED"})
	first.Secrets[0].Present = false
	second, err := view.Editable(test.Context())
	if err != nil || second.Draft.Servers[0].Stdio.Args.Values[0].ID != "first" || len(second.Draft.Servers[0].Stdio.Env.Values) != 0 || !second.Secrets[0].Present {
		test.Fatal("edit snapshot aliases immutable view", err)
	}
	state.readonly = true
	second, err = view.Editable(test.Context())
	if err != nil || !second.ReadOnly {
		test.Fatal("preserved extras became editable")
	}
	state.metadata.Schema, state.draft = 2, draft{}
	second, err = view.Editable(test.Context())
	if err != nil || !second.ReadOnly || len(second.Draft.Servers) != 0 {
		test.Fatal("unknown schema not metadata only")
	}
}

func TestEditorChangedSlotsDerivedAndOwned(test *testing.T) {
	input, err := NewInput(test.Context(), editorDraft(), []SecretEdit{{SlotID: "arg/tools/first", Action: Replace}})
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	slots, err := input.ChangedSlots(test.Context())
	if err != nil || len(slots) != 1 || slots[0] != "arg/tools/first" {
		test.Fatal("empty REPLACE omitted from approval", err)
	}
	slots[0] = "foreign"
	slots, err = input.ChangedSlots(test.Context())
	if err != nil || slots[0] != "arg/tools/first" {
		test.Fatal("approval refs borrowed")
	}
	input.Close()
	if _, err := input.ChangedSlots(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("closed input accepted")
	}
}

func TestEditorInputCollectionAndHeaderBounds(test *testing.T) {
	for _, kind := range []string{"servers", "args", "headers", "header-newline", "two-transports", "document"} {
		test.Run(kind, func(test *testing.T) {
			draft := editorDraft()
			var edits []SecretEdit
			switch kind {
			case "servers":
				draft.Servers = make([]Server, 33)
			case "args":
				draft.Servers[0].Stdio.Args.Values = make([]Argument, 65)
			case "headers":
				draft.Servers[1].HTTP.Headers = &NamedSlots{Values: make([]NamedSlot, 33)}
			case "header-newline":
				draft.Servers[1].HTTP.Headers = &NamedSlots{Values: []NamedSlot{{Name: "Authorization", SlotRef: "header/remote/Authorization"}}}
				edits = []SecretEdit{{SlotID: "header/remote/Authorization", Action: Replace, Replacement: []byte("synthetic\r\nheader")}}
			case "two-transports":
				draft.Servers[1].SSE = &Remote{URL: "https://example.invalid/events"}
			case "document":
				for index := range 17 {
					draft.Servers = append(draft.Servers, Server{ID: fmt.Sprintf("long%d", index), Name: fmt.Sprintf("long%d", index), Stdio: &Stdio{Command: strings.Repeat("x", 4096)}})
				}
			}
			if input, err := NewInput(test.Context(), draft, edits); !errors.Is(err, ErrInvalid) {
				if input != nil {
					input.Close()
				}
				test.Fatal("typed editor bypassed existing bound")
			}
		})
	}
}
