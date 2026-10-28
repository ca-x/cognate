package configuration_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestEnvelopeFailsClosed(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	const valid = `{"version":1,"variant":"claude-provider","provider":{"auth_mode":"api-key","models":{}},"secrets":{}}`
	cases := []struct {
		name, data string
		want       error
	}{
		{"duplicate", strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), configuration.ErrInvalid},
		{"escaped duplicate", strings.Replace(valid, `"version":1`, `"version":1,"vers\u0069on":1`, 1), configuration.ErrInvalid},
		{"nested duplicate", strings.Replace(valid, `"models":{}`, `"models":{"haiku":"a","haiku":"b"}`, 1), configuration.ErrInvalid},
		{"unknown", strings.Replace(valid, `"version":1`, `"unknown":"`+sentinel+`","version":1`, 1), configuration.ErrInvalid},
		{"unknown model", strings.Replace(valid, `"models":{}`, `"models":{"unknown":9007199254740993}`, 1), configuration.ErrInvalid},
		{"trailing", valid + ` {}`, configuration.ErrInvalid},
		{"bad version", strings.Replace(valid, `"version":1`, `"version":2`, 1), configuration.ErrUnsupported},
		{"wrong variant", strings.Replace(valid, `claude-provider`, `codex-provider`, 1), configuration.ErrUnsupported},
		{"missing models", strings.Replace(valid, `,"models":{}`, ``, 1), configuration.ErrInvalid},
		{"null model", strings.Replace(valid, `"models":{}`, `"models":{"haiku":null}`, 1), configuration.ErrInvalid},
		{"null provider", strings.Replace(valid, `{"auth_mode":"api-key","models":{}}`, `null`, 1), configuration.ErrInvalid},
		{"null envelope", `null`, configuration.ErrInvalid},
		{"array", `[]`, configuration.ErrInvalid},
		{"empty envelope", `{}`, configuration.ErrInvalid},
		{"secret empty", strings.Replace(valid, `"secrets":{}`, `"secrets":{"api_key":""}`, 1), configuration.ErrInvalid},
		{"secret wrong mode", strings.Replace(valid, `"secrets":{}`, `"secrets":{"auth_token":"`+sentinel+`"}`, 1), configuration.ErrInvalid},
		{"secret unknown", strings.Replace(valid, `"secrets":{}`, `"secrets":{"other":"`+sentinel+`"}`, 1), configuration.ErrInvalid},
		{"fraction version", strings.Replace(valid, `"version":1`, `"version":1.0`, 1), configuration.ErrInvalid},
		{"case changed", strings.Replace(valid, `"variant"`, `"Variant"`, 1), configuration.ErrInvalid},
	}
	for _, entry := range cases {
		test.Run(entry.name, func(test *testing.T) {
			metadata, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: "Invalid envelope", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(entry.data))})
			if err != nil {
				test.Fatal(err)
			}
			view, err := service.Edit(test.Context(), "alice", metadata.ID)
			if !errors.Is(err, entry.want) || view.Metadata.ID != "" || strings.Contains(fmt.Sprint(err), sentinel) {
				test.Fatalf("error classification/zero view: %v", err)
			}
		})
	}
}

func TestUnknownAssetIsExplicitlyUnsupported(test *testing.T) {
	store, _, _ := fixture(test)
	metadata, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "Other agent", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{}`))})
	if err != nil {
		test.Fatal(err)
	}
	service := configuration.New(store)
	if _, err := service.Edit(test.Context(), "alice", metadata.ID); !errors.Is(err, configuration.ErrUnsupported) {
		test.Fatalf("edit: %v", err)
	}
	if _, err := service.Update(test.Context(), "alice", metadata.ID, 1, draft(), nil); !errors.Is(err, configuration.ErrUnsupported) {
		test.Fatalf("update: %v", err)
	}
	if _, err := service.Copy(test.Context(), "alice", metadata.ID, 1, "Copy"); !errors.Is(err, configuration.ErrUnsupported) {
		test.Fatalf("copy: %v", err)
	}
	if _, err := service.Create(test.Context(), "alice", draft(), nil); err != nil {
		test.Fatal(err)
	}
	listed, err := service.List(test.Context(), "alice")
	if err != nil || len(listed) != 2 {
		test.Fatalf("metadata mixed list: %v", err)
	}
	got, err := service.Get(test.Context(), "alice", metadata.ID)
	if err != nil || got.AgentID != "codex" {
		test.Fatalf("metadata other agent: %v", err)
	}
	if err := service.Delete(test.Context(), "alice", metadata.ID, 2); !errors.Is(err, configuration.ErrConflict) {
		test.Fatalf("unknown asset stale delete: %v", err)
	}
	if err := service.Delete(test.Context(), "alice", metadata.ID, 1); err != nil {
		test.Fatalf("unknown asset owner CAS delete: %v", err)
	}
}

func TestSecretWrapperAndRequestRedaction(test *testing.T) {
	data := []byte(sentinel)
	secret := configuration.NewSecret(data)
	clear(data)
	mutation := replacement(sentinel)[0]
	for _, value := range []any{secret, &secret, mutation, &mutation, []configuration.SecretMutation{mutation}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if strings.Contains(fmt.Sprintf(format, value), sentinel) || strings.Contains(fmt.Sprintf(format, value), fmt.Sprintf("%x", sentinel)) {
				test.Fatal("format leaked secret")
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte(sentinel)) {
			test.Fatal("JSON leaked secret")
		}
		for _, jsonHandler := range []bool{false, true} {
			var output bytes.Buffer
			var handler slog.Handler = slog.NewTextHandler(&output, nil)
			if jsonHandler {
				handler = slog.NewJSONHandler(&output, nil)
			}
			slog.New(handler).Info("test", "value", value)
			if strings.Contains(output.String(), sentinel) {
				test.Fatal("slog leaked secret")
			}
		}
	}
	text, err := secret.MarshalText()
	if err != nil || bytes.Contains(text, []byte(sentinel)) {
		test.Fatal("text leaked secret")
	}
	store, _, _ := fixture(test)
	view, err := configuration.New(store).Create(test.Context(), "alice", draft(), []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Replace, Replacement: secret}})
	if err != nil || !view.Secrets[0].Present {
		test.Fatal("secret input was not cloned")
	}
}

type readFailureStore struct {
	configuration.Store
	err      error
	payload  []byte
	metadata *state.ConfigurationAsset
}

func (store readFailureStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	if store.err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, store.err
	}
	metadata, payload, err := store.Store.ReadConfigurationAssetVersion(ctx, owner, id)
	if store.payload != nil {
		payload = state.NewConfigurationPayload(store.payload)
	}
	if store.metadata != nil {
		metadata = *store.metadata
	}
	return metadata, payload, err
}

func TestCanceledAndAbnormalStores(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	created, err := service.Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	for _, operation := range []func() error{
		func() error { _, err := service.Create(ctx, "alice", draft(), nil); return err },
		func() error { _, err := service.Edit(ctx, "alice", created.Metadata.ID); return err },
		func() error { _, err := service.Update(ctx, "alice", created.Metadata.ID, 1, draft(), nil); return err },
		func() error { _, err := service.Copy(ctx, "alice", created.Metadata.ID, 1, "Copy"); return err },
		func() error { _, err := service.Get(ctx, "alice", created.Metadata.ID); return err },
		func() error { _, err := service.List(ctx, "alice"); return err },
		func() error { return service.Delete(ctx, "alice", created.Metadata.ID, 1) },
	} {
		if err := operation(); !errors.Is(err, context.Canceled) {
			test.Fatalf("cancel: %v", err)
		}
	}
	for _, entry := range []struct{ source, want error }{
		{state.ErrConfigurationInvalid, configuration.ErrInvalid}, {state.ErrConfigurationNotFound, configuration.ErrNotFound},
		{state.ErrConfigurationConflict, configuration.ErrConflict}, {state.ErrConfigurationLimit, configuration.ErrLimit},
		{errors.New(sentinel), configuration.ErrUnavailable}, {context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		service := configuration.New(readFailureStore{Store: store, err: fmt.Errorf("%s: %w", sentinel, entry.source)})
		view, err := service.Edit(test.Context(), "alice", created.Metadata.ID)
		if !errors.Is(err, entry.want) || strings.Contains(fmt.Sprint(err), sentinel) || view.Metadata.ID != "" {
			test.Fatalf("mapped error: %v", err)
		}
	}
	for _, abnormal := range []readFailureStore{
		{Store: store, payload: bytes.Repeat([]byte(" "), state.MaxConfigurationPayloadBytes+1)},
		{Store: store, metadata: &state.ConfigurationAsset{}},
	} {
		if view, err := configuration.New(abnormal).Edit(test.Context(), "alice", created.Metadata.ID); err == nil || view.Metadata.ID != "" {
			test.Fatal("abnormal store accepted")
		}
	}
}
