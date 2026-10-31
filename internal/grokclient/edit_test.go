package grokclient

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func memoryDraft() Draft {
	return Draft{Name: "Memory Grok", Mode: CustomModel, Model: &Model{Profile: "work", UpstreamModel: "grok-model", Endpoint: "https://synthetic.invalid/v1", Name: "Work model", APIBackend: "responses", ContextWindow: maxContext, EnvKey: new("GROK_REF")}}
}

func TestEditableSnapshotBoundaries(test *testing.T) {
	for _, environment := range []*string{nil, new("GROK_REF")} {
		draft := memoryDraft()
		draft.Model.EnvKey = environment
		input, err := NewInput(test.Context(), draft, NoKeyMutation, nil)
		if err != nil {
			test.Fatal(err)
		}
		data := input.state()
		state := &viewState{draft: data.draft, extras: true}
		view := &View{state: func() *viewState { return state }}
		edit, err := view.Editable(test.Context())
		if err != nil || !edit.ReadOnly || edit.KeyPresent || edit.NeedsCredential != (environment == nil) || (edit.Draft.Model.EnvKey == nil) != (environment == nil) {
			test.Fatal("unknown extras or readiness hidden by snapshot")
		}
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		if _, err := view.Editable(ctx); !errors.Is(err, context.Canceled) {
			test.Fatal("cancelled view returned snapshot")
		}
		for _, value := range []any{edit, edit.Draft, *edit.Draft.Model} {
			encoded, err := json.Marshal(value)
			if err != nil || bytes.Contains(encoded, []byte("GROK_REF")) || strings.Contains(fmt.Sprintf("%#v", value), "synthetic.invalid") {
				test.Fatal("safe snapshot bypassed default redaction")
			}
		}
		var workers sync.WaitGroup
		for range 16 {
			workers.Go(func() {
				if _, err := view.Editable(test.Context()); err != nil && !errors.Is(err, ErrClosed) {
					test.Error("unexpected snapshot failure")
				}
			})
		}
		view.Close()
		workers.Wait()
		input.Close()
	}
	if _, err := (*View)(nil).Editable(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("nil view accepted")
	}
	if _, err := NewInput(nil, memoryDraft(), NoKeyMutation, nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil context accepted")
	}
}

func TestMemoryInputOwnsDataAndRedacts(test *testing.T) {
	draft := memoryDraft()
	key := []byte("synthetic-memory-key")
	input, err := NewInput(test.Context(), draft, ReplaceKey, key)
	if err != nil {
		test.Fatal("memory input unavailable", err)
	}
	defer input.Close()
	draft.Model.Profile = "changed"
	*draft.Model.EnvKey = "OTHER_REF"
	clear(key)
	owned := input.state()
	if owned.draft.GetGrokbuildProvider().Model.Profile != "work" || owned.draft.GetGrokbuildProvider().Model.GetEnvKey() != "GROK_REF" || string(owned.key) != "synthetic-memory-key" {
		test.Fatal("caller aliases reached owned input")
	}
	for _, value := range []any{input, memoryDraft(), *memoryDraft().Model} {
		for _, pattern := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if strings.Contains(fmt.Sprintf(pattern, value), "synthetic") || strings.Contains(fmt.Sprintf(pattern, value), "GROK_REF") {
				test.Fatal("default formatter exposed private draft")
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("synthetic")) || bytes.Contains(encoded, []byte("GROK_REF")) {
			test.Fatal("JSON exposed private draft")
		}
	}
	secret := owned.key
	input.Close()
	if !bytes.Equal(secret, make([]byte, len(secret))) || owned.draft != nil || !owned.closed {
		test.Fatal("close did not clear owned state")
	}
}

func TestMemoryInputRejectsInvalid(test *testing.T) {
	for name, mutate := range map[string]func(*Draft){
		"mode":              func(draft *Draft) { draft.Mode = "other" },
		"model missing":     func(draft *Draft) { draft.Model = nil },
		"native with model": func(draft *Draft) { draft.Mode = NativeLogin },
		"window":            func(draft *Draft) { draft.Model.ContextWindow++ },
		"env empty":         func(draft *Draft) { draft.Model.EnvKey = new("") },
		"insecure":          func(draft *Draft) { draft.Model.Endpoint = "http://synthetic.invalid" },
	} {
		test.Run(name, func(test *testing.T) {
			draft := memoryDraft()
			mutate(&draft)
			if input, err := NewInput(test.Context(), draft, NoKeyMutation, nil); input != nil || !errors.Is(err, ErrInvalid) {
				test.Fatal("invalid typed input accepted")
			}
		})
	}
	for _, action := range []KeyAction{NoKeyMutation, KeepKey, ClearKey, KeyAction(255)} {
		if input, err := NewInput(test.Context(), memoryDraft(), action, []byte("synthetic-key")); input != nil || !errors.Is(err, ErrInvalid) {
			test.Fatal("unrequested key or unknown action accepted")
		}
	}
	if input, err := NewInput(test.Context(), Draft{Name: "Native", Mode: NativeLogin}, ReplaceKey, []byte("synthetic-key")); input != nil || !errors.Is(err, ErrInvalid) {
		test.Fatal("native credential accepted")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if input, err := NewInput(ctx, memoryDraft(), NoKeyMutation, nil); input != nil || !errors.Is(err, context.Canceled) {
		test.Fatal("cancelled input constructed")
	}
}

func TestMemoryInputCredentialBoundaries(test *testing.T) {
	for name, key := range map[string][]byte{
		"empty":           nil,
		"oversized":       bytes.Repeat([]byte("k"), 8193),
		"invalid utf8":    {0xff},
		"nul":             []byte("synthetic\x00key"),
		"carriage return": []byte("synthetic\rkey"),
		"newline":         []byte("synthetic\nkey"),
	} {
		test.Run(name, func(test *testing.T) {
			if input, err := NewInput(test.Context(), memoryDraft(), ReplaceKey, key); input != nil || !errors.Is(err, ErrInvalid) {
				test.Fatal("invalid credential accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(test.Context())
	input, err := NewInput(ctx, memoryDraft(), ReplaceKey, bytes.Repeat([]byte("k"), 8192))
	if err != nil {
		test.Fatal("maximum credential rejected", err)
	}
	defer input.Close()
	cancel()
	if len(input.state().key) != 8192 {
		test.Fatal("owned input changed after caller cancellation")
	}
}

func TestEditableSnapshotOutlivesView(test *testing.T) {
	input, err := NewInput(test.Context(), memoryDraft(), NoKeyMutation, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	state := &viewState{draft: input.state().draft}
	view := &View{state: func() *viewState { return state }}
	if _, err := view.Editable(nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil snapshot context accepted")
	}
	edit, err := view.Editable(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	view.Close()
	if !reflect.DeepEqual(edit.Draft, memoryDraft()) {
		test.Fatal("closing view changed independent snapshot")
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logger.Info("synthetic snapshot", "view", edit, "pointer", &edit, "draft", &edit.Draft, "model", edit.Draft.Model)
	for _, private := range []string{"synthetic.invalid", "GROK_REF", "Work model", "Memory Grok"} {
		if strings.Contains(output.String(), private) {
			test.Fatal("snapshot exposed private data to structured logging")
		}
	}
}

func TestEditableMemoryRoundtrip(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	input, err := NewInput(test.Context(), memoryDraft(), ReplaceKey, []byte("synthetic-memory-key"))
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	created := submitChange(test, client, Change{Action: Create}, input)
	view, err := client.Read(test.Context(), created.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	defer view.Close()
	edit, err := view.Editable(test.Context())
	if err != nil || !edit.KeyPresent || edit.ReadOnly || edit.NeedsCredential || edit.Draft.Mode != CustomModel || edit.Draft.Model.ContextWindow != maxContext || edit.Metadata.Revision != 1 {
		test.Fatal("editable snapshot not complete")
	}
	if !reflect.DeepEqual(edit.Draft, memoryDraft()) {
		test.Fatal("seven-field model did not survive RPC roundtrip")
	}
	edit.Draft.Model.Profile = "mutated"
	*edit.Draft.Model.EnvKey = "MUTATED_REF"
	fresh, err := view.Editable(test.Context())
	if err != nil || fresh.Draft.Model.Profile != "work" || *fresh.Draft.Model.EnvKey != "GROK_REF" {
		test.Fatal("editable DTO aliases retained response")
	}
	updated, err := NewInput(test.Context(), Draft{Name: "Native", Mode: NativeLogin}, ClearKey, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer updated.Close()
	result := submitChange(test, client, Change{Action: Update, ID: created.Metadata.ID, ExpectedRevision: 1}, updated)
	current, err := client.Read(test.Context(), result.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	defer current.Close()
	native, err := current.Editable(test.Context())
	if err != nil || native.Draft.Mode != NativeLogin || native.Draft.Model != nil || native.KeyPresent || native.NeedsCredential || native.Metadata.Revision != 2 {
		test.Fatal("native transition retained custom state")
	}
	view.Close()
	if _, err := view.Editable(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("closed view returned draft")
	}
}

func TestMemoryInputCredentialPreservation(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	draft := memoryDraft()
	draft.Model.EnvKey = nil
	input, err := NewInput(test.Context(), draft, ReplaceKey, []byte("synthetic-owned-key"))
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	created := submitChange(test, client, Change{Action: Create}, input)
	revision := created.Metadata.Revision
	for _, action := range []KeyAction{KeepKey, NoKeyMutation} {
		update, err := NewInput(test.Context(), draft, action, nil)
		if err != nil {
			test.Fatal(err)
		}
		defer update.Close()
		result := submitChange(test, client, Change{Action: Update, ID: created.Metadata.ID, ExpectedRevision: revision}, update)
		revision = result.Metadata.Revision
		assertStored(test, fixture, created.Metadata.ID, revision, "synthetic-owned-key", update.state().draft)
		view, err := client.Read(test.Context(), created.Metadata.ID)
		if err != nil {
			test.Fatal(err)
		}
		edit, err := view.Editable(test.Context())
		view.Close()
		if err != nil || !edit.KeyPresent || edit.NeedsCredential || edit.Draft.Model.EnvKey != nil || !reflect.DeepEqual(edit.Draft, draft) {
			test.Fatal("typed update lost secret or absent environment semantics")
		}
	}
	native, err := NewInput(test.Context(), Draft{Name: "Native", Mode: NativeLogin}, NoKeyMutation, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer native.Close()
	if operation, err := client.Prepare(test.Context(), Change{Action: Update, ID: created.Metadata.ID, ExpectedRevision: revision}, native); operation != nil || !errors.Is(err, ErrInvalid) {
		test.Fatal("native switch bypassed explicit credential clearing")
	}
	assertStored(test, fixture, created.Metadata.ID, revision, "synthetic-owned-key", input.state().draft)
}
