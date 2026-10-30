package importer

import (
	"context"
	"encoding/json/jsontext"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/configuration"
)

func parseClaudeSelection(ctx context.Context, name string, raw []byte) (configuration.Draft, []SelectionSlot, error) {
	if err := ctx.Err(); err != nil {
		return configuration.Draft{}, nil, err
	}
	root, err := sourceObject(raw)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(root)
	if !sourceFields(root, "env") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	environment, err := sourceObject(root["env"])
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(environment)
	if !sourceFields(environment, "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	slots, err := selectionSlots(environment, []struct{ field, slot string }{{"ANTHROPIC_API_KEY", "claude.api-key"}, {"ANTHROPIC_AUTH_TOKEN", "claude.auth-token"}})
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	provider := configuration.ClaudeProvider{}
	for _, field := range []struct {
		key    string
		target **string
		limit  int
	}{
		{"ANTHROPIC_BASE_URL", &provider.Endpoint, 2048},
		{"ANTHROPIC_MODEL", &provider.Models.Default, 256},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", &provider.Models.Haiku, 256},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", &provider.Models.Sonnet, 256},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", &provider.Models.Opus, 256},
	} {
		if value, present := environment[field.key]; present {
			parsed, err := sourceString(value)
			if err != nil || !selectionText(parsed, field.limit) {
				return configuration.Draft{}, nil, ErrSelectionUnsupported
			}
			*field.target = new(parsed)
		}
	}
	if provider.Endpoint != nil && !selectionEndpoint(*provider.Endpoint, false) {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	return configuration.Draft{Name: name, Provider: provider}, slots, ctx.Err()
}

func selectionSlots(object map[string]jsontext.Value, definitions []struct{ field, slot string }) ([]SelectionSlot, error) {
	slots := make([]SelectionSlot, 0, len(definitions))
	for _, definition := range definitions {
		value, present := object[definition.field]
		if present && (len(value) < 2 || value[0] != '"') {
			return nil, ErrSelectionUnsupported
		}
		slots = append(slots, SelectionSlot{Slot: definition.slot, Present: present && len(value) > 2})
		clear(value)
		delete(object, definition.field)
	}
	return slots, nil
}

func selectionEndpoint(value string, httpsOnly bool) bool {
	if !selectionText(value, 2048) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "?#\\") {
		return false
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || endpoint.Scheme != "https" && (httpsOnly || endpoint.Scheme != "http") {
		return false
	}
	if httpsOnly && (!utf8.ValidString(endpoint.Path) || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl)) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func selectionModel(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for index, character := range []byte(value) {
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (index == 0 || !strings.ContainsRune("._:/-", rune(character))) {
			return false
		}
	}
	return true
}
