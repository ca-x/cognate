package codexconfig

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

const Native0166ProfileID = "codex-0.166.0-linux-amd64-full-responses-v1"

const Native0166ExecutableSHA256 = "b8a11fedf24b2dfb59a70c9e52b030f9a508dcd49cd2dd0d41956f4c785156b8"

const Native0166EffectiveProfileID = "codex-0.166.0-linux-amd64-full-responses-effective-v2"

func native0166EffectiveProfile() nativeProfile {
	profile := native0166Profile()
	profile.id = Native0166EffectiveProfileID
	profile.modelSemantics = sha256.Sum256([]byte(native0166EffectiveSource))
	return profile
}

func Native0166EffectiveManifest() (id string, manifest, profile, template [32]byte) {
	data := native0166EffectiveProfile()
	profile, template = profileDigest(data), profileTemplateDigest(data)
	raw, _ := json.Marshal(struct {
		ID         string
		Source     string
		Executable string
		Profile    [32]byte
		Template   [32]byte
		Launch     string
		Metadata   [32]byte
		Semantics  string
	}{data.id, "6ff1e1ca3b30fc8b914720681fcf5fe62e95f856", Native0166ExecutableSHA256, profile, template, "direct-app-server-env-cleared-file-auth-config-read-v1", data.modelSemantics, "effective-model-info-pass-through-7-v2"}, json.Deterministic(true))
	return data.id, sha256.Sum256(raw), profile, template
}

func native0166Profile() nativeProfile {
	efforts := []agentconfig.CodexReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	descriptions := make(map[agentconfig.CodexReasoningEffort]string, len(efforts))
	for _, effort := range efforts {
		descriptions[effort] = "Cognate " + string(effort)
	}
	return nativeProfile{id: Native0166ProfileID, dialect: "toml-1.1", defaultStore: File, parallel: parallelAlwaysOn,
		legacyInstructions: true, relativeCatalog: true, bearerPrecedence: true,
		rootDefaults:  []byte(`{"models":[]}`),
		modelDefaults: []byte(`{"display_name":"Cognate model","supported_reasoning_levels":[{"effort":"low","description":"Cognate low"}],"shell_type":"unified_exec","visibility":"list","supported_in_api":true,"priority":0,"support_verbosity":false,"truncation_policy":{"mode":"bytes","limit":10000},"experimental_supported_tools":[],"base_instructions":"You are a helpful assistant."}`),
		requiredRoot:  []string{"models"},
		requiredModel: []string{"slug", "display_name", "supported_reasoning_levels", "shell_type", "visibility", "supported_in_api", "priority", "support_verbosity", "truncation_policy", "experimental_supported_tools", "base_instructions"},
		ignoredModel:  []string{"shell_type", "visibility", "supported_in_api", "priority", "support_verbosity", "truncation_policy", "experimental_supported_tools"},
		efforts:       efforts, descriptions: descriptions}
}

func Native0166Manifest() (id string, manifest, profile, template [32]byte) {
	data := native0166Profile()
	profile, template = profileDigest(data), profileTemplateDigest(data)
	raw, _ := json.Marshal(struct {
		ID         string
		Source     string
		Executable string
		Profile    [32]byte
		Template   [32]byte
		Launch     string
	}{data.id, "6ff1e1ca3b30fc8b914720681fcf5fe62e95f856", Native0166ExecutableSHA256, profile, template, "direct-app-server-env-cleared-file-auth-config-read-v1"}, json.Deterministic(true))
	return data.id, sha256.Sum256(raw), profile, template
}

func Native0166ProviderPresent(ctx context.Context, config LocalImage) (bool, error) {
	if config.Presence() == Missing {
		return false, nil
	}
	if config.Presence() != Present {
		return false, ErrInvalidLocalImage
	}
	document, err := indexTOML(ctx, config.get[imageData]().body)
	if err != nil {
		return false, err
	}
	_, exists := document.lookup("model_providers", "cognate")
	return exists, nil
}

func Native0166LayerWitness(ctx context.Context, response []byte, config LocalImage, userConfig string) ([32]byte, error) {
	if err := ctx.Err(); err != nil {
		return [32]byte{}, err
	}
	document, err := readJSON(ctx, response)
	if ctx.Err() != nil {
		return [32]byte{}, ctx.Err()
	}
	if err != nil || document.root.kind != '{' {
		return [32]byte{}, ErrUnresolvedLocalAuth
	}
	layers := document.root.member("layers")
	if layers == nil || layers.kind != '[' || len(layers.elements) != 2 {
		return [32]byte{}, ErrUnresolvedLocalAuth
	}
	seen := make(map[string]bool, 2)
	var nonRole []byte
	for _, layer := range layers.elements {
		if !native0166Members(layer, "name", "config", "version", "disabledReason") || !native0166Absent(layer.member("disabledReason")) || !native0166String(layer.member("version")) {
			return [32]byte{}, ErrUnresolvedLocalAuth
		}
		name, value := layer.member("name"), layer.member("config")
		if !native0166Members(name, "type", "file", "profile") || !native0166String(name.member("type")) || !native0166String(name.member("file")) || !native0166Absent(name.member("profile")) || value == nil || value.kind != '{' {
			return [32]byte{}, ErrUnresolvedLocalAuth
		}
		kind := name.member("type").text
		if seen[kind] {
			return [32]byte{}, ErrUnresolvedLocalAuth
		}
		seen[kind] = true
		switch kind {
		case "user":
			if name.member("file").text != userConfig {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
			semantic := map[string]any{}
			if config.Presence() == Present {
				indexed, err := indexTOML(ctx, config.get[imageData]().body)
				if err != nil {
					return [32]byte{}, ErrUnresolvedLocalAuth
				}
				semantic = indexed.semantic
				if catalog, exists := semantic["model_catalog_json"]; exists {
					if catalog != "cognate-model-catalog.json" || !path.IsAbs(userConfig) {
						return [32]byte{}, ErrUnresolvedLocalAuth
					}
					semantic["model_catalog_json"] = path.Join(path.Dir(userConfig), "cognate-model-catalog.json")
				}
			} else if config.Presence() != Missing {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
			var actual any
			if json.Unmarshal(response[value.start:value.end], &actual) != nil {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
			wanted, err := json.Marshal(semantic, json.Deterministic(true))
			if err != nil {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
			defer clear(wanted)
			native, err := json.Marshal(actual, json.Deterministic(true))
			if err != nil {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
			defer clear(native)
			if string(wanted) != string(native) {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
		case "system":
			if name.member("file").text != "/etc/codex/config.toml" || len(value.members) != 0 {
				return [32]byte{}, ErrUnresolvedLocalAuth
			}
			nonRole = append(nonRole, response[layer.start:layer.end]...)
		default:
			return [32]byte{}, ErrUnresolvedLocalAuth
		}
	}
	if !seen["user"] || !seen["system"] {
		return [32]byte{}, ErrUnresolvedLocalAuth
	}
	defer clear(nonRole)
	return sha256.Sum256(nonRole), ctx.Err()
}

func InspectNative0166(ctx context.Context, config, auth LocalImage, now time.Time) (AuthFacts, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return AuthFacts{}, time.Time{}, err
	}
	if now.IsZero() || config.Presence() != Missing && config.Presence() != Present || auth.Presence() != Missing && auth.Presence() != Present {
		return AuthFacts{}, time.Time{}, ErrUnresolvedLocalAuth
	}
	if config.Presence() == Present {
		document, err := indexTOML(ctx, config.get[imageData]().body)
		if err != nil {
			return AuthFacts{}, time.Time{}, ErrUnresolvedLocalAuth
		}
		for name := range document.semantic {
			if !slices.Contains([]string{"model_provider", "model", "model_reasoning_effort", "model_catalog_json", "model_providers", "cli_auth_credentials_store"}, name) {
				return AuthFacts{}, time.Time{}, ErrUnresolvedLocalAuth
			}
		}
		if store, exists := document.lookup("cli_auth_credentials_store"); exists && store != "file" {
			return AuthFacts{}, time.Time{}, ErrUnresolvedLocalAuth
		}
		if catalog, exists := document.lookup("model_catalog_json"); exists && catalog != "cognate-model-catalog.json" {
			return AuthFacts{}, time.Time{}, ErrUnresolvedLocalAuth
		}
	}
	deadline := now.Add(5 * time.Minute)
	login := NoCredentialLogin
	if auth.Presence() == Present {
		var err error
		deadline, err = inspect0166Auth(ctx, auth.get[imageData]().body, now, deadline)
		if err != nil {
			return AuthFacts{}, time.Time{}, ErrUnresolvedLocalAuth
		}
		login = CredentialLogin
	}
	if err := ctx.Err(); err != nil {
		return AuthFacts{}, time.Time{}, err
	}
	facts, err := NewAuthFacts(File, KeyringNotObserved, login)
	return facts, deadline, err
}

func native0166Members(node *jsonNode, names ...string) bool {
	if node == nil || node.kind != '{' {
		return false
	}
	for _, member := range node.members {
		if !slices.Contains(names, member.name) {
			return false
		}
	}
	return true
}

func native0166String(node *jsonNode) bool {
	return node != nil && node.kind == '"' && validSecret([]byte(node.text)) && strings.TrimSpace(node.text) == node.text
}

func native0166Absent(node *jsonNode) bool { return node == nil || node.kind == 'n' }

func inspect0166Auth(ctx context.Context, raw []byte, now, deadline time.Time) (time.Time, error) {
	document, err := readJSON(ctx, raw)
	if err != nil || !native0166Members(document.root, "auth_mode", "OPENAI_API_KEY", "tokens", "last_refresh", "agent_identity", "personal_access_token", "bedrock_api_key", "bedrock_access_keys") {
		return time.Time{}, ErrUnresolvedLocalAuth
	}
	root := document.root
	for _, name := range []string{"agent_identity", "personal_access_token", "bedrock_api_key", "bedrock_access_keys"} {
		if !native0166Absent(root.member(name)) {
			return time.Time{}, ErrUnresolvedLocalAuth
		}
	}
	key, tokens, mode := root.member("OPENAI_API_KEY"), root.member("tokens"), root.member("auth_mode")
	if native0166String(key) {
		if !native0166Absent(tokens) || !native0166Absent(root.member("last_refresh")) || !native0166Absent(mode) && (mode.kind != '"' || mode.text != "apikey") {
			return time.Time{}, ErrUnresolvedLocalAuth
		}
		return deadline, nil
	}
	if !native0166Absent(key) || !native0166Absent(mode) && (mode.kind != '"' || mode.text != "chatgpt") || !native0166Members(tokens, "id_token", "access_token", "refresh_token", "account_id", "oauth_provenance", "login_owner_id") {
		return time.Time{}, ErrUnresolvedLocalAuth
	}
	for _, name := range []string{"id_token", "access_token", "refresh_token"} {
		if !native0166String(tokens.member(name)) {
			return time.Time{}, ErrUnresolvedLocalAuth
		}
	}
	for _, name := range []string{"account_id", "login_owner_id"} {
		if value := tokens.member(name); !native0166Absent(value) && !native0166String(value) {
			return time.Time{}, ErrUnresolvedLocalAuth
		}
	}
	if provenance := tokens.member("oauth_provenance"); !native0166Absent(provenance) {
		if !native0166Members(provenance, "issuer", "client_id") || !native0166String(provenance.member("issuer")) || !native0166String(provenance.member("client_id")) {
			return time.Time{}, ErrUnresolvedLocalAuth
		}
	}
	identity, err := native0166JWT(ctx, tokens.member("id_token").text)
	if err != nil || identity.root.kind != '{' {
		return time.Time{}, ErrUnresolvedLocalAuth
	}
	clear(identity.raw)
	access := tokens.member("access_token").text
	var refreshAt time.Time
	if strings.Contains(access, ".") {
		parsed, err := native0166JWT(ctx, access)
		if err != nil {
			return time.Time{}, ErrUnresolvedLocalAuth
		}
		defer clear(parsed.raw)
		if expiry := parsed.root.member("exp"); expiry != nil {
			seconds, err := strconv.ParseInt(string(parsed.raw[expiry.start:expiry.end]), 10, 64)
			if err != nil || seconds <= 0 || seconds > 253402300799 {
				return time.Time{}, ErrUnresolvedLocalAuth
			}
			refreshAt = time.Unix(seconds, 0).Add(-5 * time.Minute)
		}
	}
	refresh := root.member("last_refresh")
	if !native0166String(refresh) {
		return time.Time{}, ErrUnresolvedLocalAuth
	}
	lastRefresh, err := time.Parse(time.RFC3339Nano, refresh.text)
	if err != nil || lastRefresh.After(now) {
		return time.Time{}, ErrUnresolvedLocalAuth
	}
	if refreshAt.IsZero() {
		refreshAt = lastRefresh.Add(8 * 24 * time.Hour)
	}
	if !refreshAt.After(now) {
		return time.Time{}, ErrUnresolvedLocalAuth
	}
	if refreshAt.Before(deadline) {
		deadline = refreshAt
	}
	return deadline, ctx.Err()
}

func native0166JWT(ctx context.Context, token string) (*jsonDocument, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" || len(token) > 8192 {
		return nil, ErrUnresolvedLocalAuth
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, ErrUnresolvedLocalAuth
	}
	document, err := readJSON(ctx, raw)
	if err != nil || document.root.kind != '{' {
		clear(raw)
		return nil, ErrUnresolvedLocalAuth
	}
	return document, nil
}
