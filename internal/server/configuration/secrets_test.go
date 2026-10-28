package configuration_test

import (
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func storedSecret(test *testing.T, store *state.Store, id string, slot configuration.Slot) string {
	test.Helper()
	payload, err := store.ReadConfigurationAsset(test.Context(), "alice", id)
	if err != nil {
		test.Fatal(err)
	}
	var decoded struct {
		Secrets struct {
			APIKey    string `json:"api_key"`
			AuthToken string `json:"auth_token"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(payload.Reveal(), &decoded); err != nil {
		test.Fatal("cannot inspect synthetic fixture")
	}
	if slot == configuration.APIKeySlot {
		return decoded.Secrets.APIKey
	}
	return decoded.Secrets.AuthToken
}

func TestSecretMutationModesAndCopies(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	input := draft()
	created, err := service.Create(test.Context(), "alice", input, replacement("  "+sentinel+"\t"))
	if err != nil {
		test.Fatal(err)
	}
	if storedSecret(test, store, created.Metadata.ID, configuration.APIKeySlot) != "  "+sentinel+"\t" {
		test.Fatal("secret was trimmed, mutated or redacted in storage")
	}
	kept, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Keep}})
	if err != nil || !kept.Secrets[0].Present {
		test.Fatalf("explicit keep: %v", err)
	}
	if storedSecret(test, store, created.Metadata.ID, configuration.APIKeySlot) != "  "+sentinel+"\t" {
		test.Fatal("explicit keep changed secret")
	}
	replaced, err := service.Update(test.Context(), "alice", created.Metadata.ID, 2, input, replacement(sentinel+"-new"))
	if err != nil || replaced.Metadata.Revision != 3 {
		test.Fatalf("replace: %v", err)
	}
	if storedSecret(test, store, created.Metadata.ID, configuration.APIKeySlot) != sentinel+"-new" {
		test.Fatal("replacement not persisted")
	}
	input.Provider.AuthMode = configuration.AuthToken
	newToken := []configuration.SecretMutation{{Slot: configuration.AuthTokenSlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(sentinel + "-token"))}}
	if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 3, input, newToken); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatalf("mode switch must explicitly clear old key: %v", err)
	}
	newToken = append(newToken, configuration.SecretMutation{Slot: configuration.APIKeySlot, Action: configuration.Clear})
	switched, err := service.Update(test.Context(), "alice", created.Metadata.ID, 3, input, newToken)
	if err != nil || switched.Secrets[0].Present || !switched.Secrets[1].Present || switched.Readiness != configuration.CredentialComplete {
		test.Fatalf("explicit switch: %v", err)
	}
	input.Provider.AuthMode = configuration.NativeLogin
	if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, 4, input, nil); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatalf("native login must explicitly clear token: %v", err)
	}
	native, err := service.Update(test.Context(), "alice", created.Metadata.ID, 4, input, []configuration.SecretMutation{{Slot: configuration.AuthTokenSlot, Action: configuration.Clear}})
	if err != nil || native.Readiness != configuration.CredentialComplete || native.Secrets[0].Present || native.Secrets[1].Present {
		test.Fatalf("native login: %v", err)
	}
	copyView, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 5, "Native copy")
	if err != nil || copyView.Readiness != configuration.CredentialComplete {
		test.Fatalf("native copy has no credential requirement: %v", err)
	}
	if _, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 4, "Stale copy"); !errors.Is(err, configuration.ErrConflict) {
		test.Fatalf("stale copy: %v", err)
	}
	input.Provider.AuthMode = configuration.APIKey
	missing, err := service.Update(test.Context(), "alice", created.Metadata.ID, 5, input, nil)
	if err != nil || missing.Readiness != configuration.NeedsCredential {
		test.Fatalf("missing key draft: %v", err)
	}
	cleared, err := service.Update(test.Context(), "alice", created.Metadata.ID, 6, input, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Clear}})
	if err != nil || cleared.Readiness != configuration.NeedsCredential {
		test.Fatalf("clear missing: %v", err)
	}
}

func TestRejectInvalidSecretsWithoutMutation(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	created, err := service.Create(test.Context(), "alice", draft(), replacement(sentinel))
	if err != nil {
		test.Fatal(err)
	}
	cases := []struct {
		name      string
		mutations []configuration.SecretMutation
	}{
		{"empty", replacement("")}, {"too long", replacement(strings.Repeat("a", 8193))},
		{"invalid UTF8", replacement(string([]byte{0xff}))}, {"NUL", replacement("a\x00b")}, {"CR", replacement("a\rb")}, {"LF", replacement("a\nb")},
		{"unknown slot", []configuration.SecretMutation{{Slot: "unknown", Action: configuration.Clear}}},
		{"unknown action", []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: "unknown"}}},
		{"duplicate", []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Clear}, {Slot: configuration.APIKeySlot, Action: configuration.Keep}}},
		{"clear with value", []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Clear, Replacement: configuration.NewSecret([]byte(sentinel))}}},
		{"keep with value", []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Keep, Replacement: configuration.NewSecret([]byte(sentinel))}}},
	}
	for _, entry := range cases {
		test.Run(entry.name, func(test *testing.T) {
			if view, err := service.Create(test.Context(), "alice", draft(), entry.mutations); !errors.Is(err, configuration.ErrInvalid) || view.Metadata.ID != "" {
				test.Fatalf("invalid create: %v", err)
			}
			if view, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, draft(), entry.mutations); !errors.Is(err, configuration.ErrInvalid) || view.Metadata.ID != "" {
				test.Fatalf("invalid update: %v", err)
			}
		})
	}
	if _, err := service.Create(test.Context(), "alice", draft(), []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Keep}}); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatalf("create keep: %v", err)
	}
	if storedSecret(test, store, created.Metadata.ID, configuration.APIKeySlot) != sentinel {
		test.Fatal("invalid mutation modified secret")
	}
	view, err := service.Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || view.Metadata.Revision != 1 {
		test.Fatalf("invalid mutation changed revision: %v", err)
	}
	maximum, err := service.Create(test.Context(), "alice", draft(), replacement(strings.Repeat("z", 8192)))
	if err != nil || !maximum.Secrets[0].Present {
		test.Fatalf("secret byte upper bound: %v", err)
	}
}

func TestProviderPresenceAndValidationBoundaries(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	cases := []struct {
		name   string
		mutate func(*configuration.Draft)
	}{
		{"empty name", func(input *configuration.Draft) { input.Name = " " }},
		{"long name", func(input *configuration.Draft) { input.Name = strings.Repeat("n", 129) }},
		{"control name", func(input *configuration.Draft) { input.Name = "a\x00b" }},
		{"bad auth", func(input *configuration.Draft) { input.Provider.AuthMode = "unknown" }},
		{"empty model", func(input *configuration.Draft) { input.Provider.Models.Haiku = new("") }},
		{"long model", func(input *configuration.Draft) { input.Provider.Models.Sonnet = new(strings.Repeat("m", 257)) }},
		{"UTF8 model", func(input *configuration.Draft) { input.Provider.Models.Opus = new(string([]byte{0xff})) }},
		{"model control", func(input *configuration.Draft) { input.Provider.Models.Default = new("a\tb") }},
		{"empty endpoint", func(input *configuration.Draft) { input.Provider.Endpoint = new("") }},
		{"relative endpoint", func(input *configuration.Draft) { input.Provider.Endpoint = new("/relative") }},
		{"missing host", func(input *configuration.Draft) { input.Provider.Endpoint = new("https:///path") }},
		{"userinfo", func(input *configuration.Draft) { input.Provider.Endpoint = new("https://user:password@example.test") }},
		{"query", func(input *configuration.Draft) { input.Provider.Endpoint = new("https://example.test?secret=x") }},
		{"empty query", func(input *configuration.Draft) { input.Provider.Endpoint = new("https://example.test?") }},
		{"fragment", func(input *configuration.Draft) { input.Provider.Endpoint = new("https://example.test#") }},
		{"other protocol", func(input *configuration.Draft) { input.Provider.Endpoint = new("file:///tmp/config") }},
		{"URL control", func(input *configuration.Draft) { input.Provider.Endpoint = new("https://example.test/\x7f") }},
		{"long URL", func(input *configuration.Draft) {
			input.Provider.Endpoint = new("https://example.test/" + strings.Repeat("p", 2049))
		}},
		{"native custom endpoint", func(input *configuration.Draft) {
			input.Provider.AuthMode = configuration.NativeLogin
			input.Provider.Endpoint = new("https://example.test")
		}},
	}
	for _, entry := range cases {
		test.Run(entry.name, func(test *testing.T) {
			input := draft()
			entry.mutate(&input)
			if view, err := service.Create(test.Context(), "alice", input, nil); !errors.Is(err, configuration.ErrInvalid) || view.Metadata.ID != "" {
				test.Fatalf("invalid provider: %v", err)
			}
		})
	}
	for _, endpoint := range []string{"http://127.0.0.1:9876/v1", "http://[::1]:9876", "https://example.test/" + strings.Repeat("p", 2048-len("https://example.test/"))} {
		input := draft()
		input.Name = strings.Repeat("n", 128)
		input.Provider.Endpoint = new(endpoint)
		input.Provider.Models.Default = new(strings.Repeat("界", 85) + "x")
		view, err := service.Create(test.Context(), "alice", input, nil)
		if err != nil || view.Provider.Models.Haiku != nil || len(*view.Provider.Models.Default) != 256 || *view.Provider.Endpoint != endpoint {
			test.Fatalf("valid byte boundary/presence: %v", err)
		}
		*input.Provider.Endpoint = "changed after call"
		if *view.Provider.Endpoint != endpoint {
			test.Fatal("input aliases view")
		}
		*view.Provider.Models.Default = "changed view"
		reloaded, err := service.Edit(test.Context(), "alice", view.Metadata.ID)
		if err != nil || len(*reloaded.Provider.Models.Default) != 256 {
			test.Fatal("view aliases stored provider")
		}
	}
}
