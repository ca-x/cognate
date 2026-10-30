package geminiconfig

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func checkProvider(ctx context.Context, images LocalImages, declaration agentconfig.GeminiProviderDeclarationV1, local LocalResolution, profile nativeProfile) (result ProviderPlan, returnedError error) {
	defer func() {
		if err := checkContext(ctx); err != nil {
			result.Destroy()
			result = ProviderPlan{}
			returnedError = err
		}
	}()
	if err := checkContext(ctx); err != nil {
		return ProviderPlan{}, err
	}
	if err := validateDeclaration(declaration); err != nil {
		return ProviderPlan{}, err
	}
	if err := validateProfile(ctx, profile); err != nil {
		return ProviderPlan{}, err
	}
	source, facts := images.get[imagesData](), local.get[resolutionData]()
	if source == nil || facts == nil {
		return ProviderPlan{}, ErrInvalidLocalImage
	}
	ownedImages, err := NewImages(source.images[0], source.images[1])
	if err != nil {
		return ProviderPlan{}, err
	}
	profile.ignoredEnv = slices.Clone(profile.ignoredEnv)
	profile.ignoredJSON = slices.Clone(profile.ignoredJSON)
	data := &planData{images: ownedImages, declaration: cloneDeclaration(declaration), local: *facts, profile: profile}
	defer func() {
		if returnedError != nil {
			data.destroy()
		}
	}()
	env, settings, err := parsePair(ctx, data.images)
	if err != nil {
		return ProviderPlan{}, err
	}
	defer env.destroy()
	defer settings.destroy()
	if err := validateLocal(ctx, env, settings, data.local, profile); err != nil {
		return ProviderPlan{}, err
	}
	actions, err := planActions(env, settings, data.declaration, data.local)
	if err != nil {
		return ProviderPlan{}, err
	}
	data.actions = actions
	placeholder := bytes.Repeat([]byte{'x'}, 8192)
	defer clear(placeholder)
	probe, _, err := renderData(ctx, data, placeholder)
	if err != nil {
		return ProviderPlan{}, err
	}
	probe.Destroy()
	return ProviderPlan{opaque: own(data)}, nil
}

func parsePair(ctx context.Context, images LocalImages) (*envDocument, *jsonDocument, error) {
	data := images.get[imagesData]()
	if data == nil {
		return nil, nil, ErrInvalidLocalImage
	}
	env, err := readEnv(ctx, data.images[0].get[imageData]().body)
	if err != nil {
		return nil, nil, err
	}
	body := data.images[1].get[imageData]().body
	if data.images[1].Presence() == Missing {
		body = []byte(`{}`)
	}
	settings, err := readJSON(ctx, body)
	if err != nil {
		env.destroy()
		return nil, nil, err
	}
	return env, settings, nil
}

func declared(value agentconfig.GeminiProviderDeclarationV1, field Field) bool {
	switch field {
	case APIKeyField:
		return value.AuthMode == agentconfig.GeminiAPIKeyAuth
	case AuthModeField:
		return true
	case EndpointField:
		return value.Endpoint != nil
	case TransportField:
		return value.KeyTransport != nil && *value.KeyTransport == agentconfig.GeminiBearer
	case EnvModelField:
		return value.EnvModel != nil
	case SettingsModelField:
		return value.SettingsModel != nil
	}
	return false
}

func localField(env *envDocument, settings *jsonDocument, field Field) bool {
	if field == SettingsModelField {
		parent := settings.root.member("model")
		return parent != nil && parent.member("name") != nil
	}
	if field == AuthModeField {
		parent := settings.root.member("security")
		if parent != nil {
			parent = parent.member("auth")
		}
		return parent != nil && parent.member("selectedType") != nil
	}
	return env.find(string(field)) != nil
}

func planActions(env *envDocument, settings *jsonDocument, declaration agentconfig.GeminiProviderDeclarationV1, local resolutionData) ([6]FieldAction, error) {
	var actions [6]FieldAction
	for index, field := range fields {
		present, fact := localField(env, settings, field), local.fields[index]
		if fact.Field != field || present && fact.Ownership != Managed && fact.Ownership != Foreign || !present && fact.Ownership != Absent || fact.Cleanup < Preserve || fact.Cleanup > Clear || fact.Cleanup == Clear && fact.Ownership != Managed {
			return [6]FieldAction{}, ErrOwnershipConflict
		}
		action := Keep
		if declared(declaration, field) {
			if fact.Ownership == Foreign || fact.Cleanup == Clear {
				return [6]FieldAction{}, ErrOwnershipConflict
			}
			action = Write
		} else if present {
			if fact.Ownership == Foreign {
				return [6]FieldAction{}, ErrOwnershipConflict
			}
			if fact.Cleanup == Clear {
				action = Remove
			} else if field == TransportField || declaration.AuthMode == agentconfig.GeminiNativeLogin && (field == APIKeyField || field == EndpointField) {
				return [6]FieldAction{}, ErrUnresolvedLocalAuth
			}
		}
		actions[index] = FieldAction{Field: field, Action: action}
	}
	return actions, nil
}

func fieldValue(value agentconfig.GeminiProviderDeclarationV1, field Field, secret []byte) []byte {
	switch field {
	case APIKeyField:
		return bytes.Clone(secret)
	case EndpointField:
		if value.Endpoint != nil {
			return []byte(*value.Endpoint)
		}
	case TransportField:
		return []byte("bearer")
	case EnvModelField:
		if value.EnvModel != nil {
			return []byte(*value.EnvModel)
		}
	case SettingsModelField:
		if value.SettingsModel != nil {
			return []byte(*value.SettingsModel)
		}
	case AuthModeField:
		return []byte(value.SelectedType)
	}
	return nil
}

func renderData(ctx context.Context, data *planData, secret []byte) (LocalImages, [2]Action, error) {
	env, settings, err := parsePair(ctx, data.images)
	if err != nil {
		return LocalImages{}, [2]Action{}, err
	}
	defer env.destroy()
	defer settings.destroy()
	var envUpdates []envUpdate
	var jsonUpdates []jsonChange
	defer func() {
		for _, update := range envUpdates {
			clear(update.value)
		}
		for _, update := range jsonUpdates {
			clear(update.value)
		}
	}()
	for _, action := range data.actions {
		if action.Action == Keep {
			continue
		}
		value := fieldValue(data.declaration, action.Field, secret)
		if action.Field == SettingsModelField || action.Field == AuthModeField {
			path := []string{"model", "name"}
			if action.Field == AuthModeField {
				path = []string{"security", "auth", "selectedType"}
			}
			encoded, err := json.Marshal(string(value))
			clear(value)
			if err != nil {
				return LocalImages{}, [2]Action{}, ErrInvalidDeclaration
			}
			jsonUpdates = append(jsonUpdates, jsonChange{path: path, value: encoded, remove: action.Action == Remove})
		} else {
			envUpdates = append(envUpdates, envUpdate{name: string(action.Field), value: value, remove: action.Action == Remove})
		}
	}
	envBody, err := rewriteEnv(ctx, env, envUpdates)
	if err != nil {
		return LocalImages{}, [2]Action{}, err
	}
	defer clear(envBody)
	settingsBody, err := rewriteSettings(ctx, settings, jsonUpdates)
	if err != nil {
		return LocalImages{}, [2]Action{}, err
	}
	defer clear(settingsBody)
	if len(envBody)+len(settingsBody) > MaxPairBytes {
		return LocalImages{}, [2]Action{}, ErrCodecLimit
	}
	presence := Present
	if data.images.get[imagesData]().images[0].Presence() == Missing && len(envBody) == 0 {
		presence = Missing
	}
	first, err := NewImage(presence, envBody)
	if err != nil {
		return LocalImages{}, [2]Action{}, err
	}
	defer first.Destroy()
	second, err := NewImage(Present, settingsBody)
	if err != nil {
		return LocalImages{}, [2]Action{}, err
	}
	defer second.Destroy()
	images, err := NewImages(first, second)
	if err != nil {
		return LocalImages{}, [2]Action{}, err
	}
	var actions [2]Action
	for index, image := range images.get[imagesData]().images {
		before := data.images.get[imagesData]().images[index].get[imageData]()
		after := image.get[imageData]()
		actions[index] = Write
		if before.presence == after.presence && bytes.Equal(before.body, after.body) {
			actions[index] = Keep
		}
	}
	if err := checkContext(ctx); err != nil {
		images.Destroy()
		return LocalImages{}, [2]Action{}, err
	}
	return images, actions, nil
}

func RenderProvider(ctx context.Context, plan ProviderPlan, selected []SelectedSecret) (result ProviderCandidate, returnedError error) {
	defer func() {
		if err := checkContext(ctx); err != nil {
			result.Destroy()
			result = ProviderCandidate{}
			returnedError = err
		}
	}()
	if err := checkContext(ctx); err != nil {
		return ProviderCandidate{}, err
	}
	data := plan.get[planData]()
	if data == nil {
		return ProviderCandidate{}, ErrInvalidDeclaration
	}
	var material []byte
	if data.declaration.AuthMode == agentconfig.GeminiAPIKeyAuth {
		if len(selected) != 1 {
			return ProviderCandidate{}, ErrInvalidSecret
		}
		secret := selected[0].get[secretData]()
		if secret == nil || secret.slot != agentconfig.GeminiAPIKeySecret || len(secret.body) < 1 || len(secret.body) > 8192 || !utf8.Valid(secret.body) || bytes.ContainsAny(secret.body, "\x00\r\n") {
			return ProviderCandidate{}, ErrInvalidSecret
		}
		material = secret.body
	} else if len(selected) != 0 {
		return ProviderCandidate{}, ErrInvalidSecret
	}
	images, actions, err := renderData(ctx, data, material)
	if err != nil {
		return ProviderCandidate{}, err
	}
	return ProviderCandidate{opaque: own(&candidateData{images: images, actions: actions, binding: plan.Binding()})}, nil
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidDeclaration
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return context.Canceled
	}
	return nil
}

const (
	MaxImageBytes = 256 << 10
	MaxPairBytes  = 384 << 10
)

type Presence uint8

const (
	PresenceInvalid Presence = iota
	Missing
	Present
)

type Role uint8

const (
	RoleInvalid Role = iota
	EnvRole
	SettingsRole
)

type Field string

const (
	APIKeyField        Field = "GEMINI_API_KEY"
	EndpointField      Field = "GOOGLE_GEMINI_BASE_URL"
	TransportField     Field = "GEMINI_API_KEY_AUTH_MECHANISM"
	EnvModelField      Field = "GEMINI_MODEL"
	SettingsModelField Field = "/model/name"
	AuthModeField      Field = "/security/auth/selectedType"
)

var fields = [...]Field{APIKeyField, EndpointField, TransportField, EnvModelField, SettingsModelField, AuthModeField}

type Ownership uint8

const (
	OwnershipUnknown Ownership = iota
	Absent
	Managed
	Foreign
)

type Cleanup uint8

const (
	CleanupInvalid Cleanup = iota
	Preserve
	Clear
)

type EffectiveScope uint8

const (
	ScopeUnknown EffectiveScope = iota
	CompleteEffective
)

type Action uint8

const (
	ActionInvalid Action = iota
	Keep
	Write
	Remove
)

type FieldFact struct {
	Field     Field
	Ownership Ownership
	Cleanup   Cleanup
}
type FieldAction struct {
	Field  Field
	Action Action
}

var (
	ErrInvalidDeclaration        = errors.New("invalid Gemini declaration")
	ErrInvalidLocalImage         = errors.New("invalid Gemini local image")
	ErrInvalidSecret             = errors.New("invalid Gemini secret material")
	ErrOwnershipConflict         = errors.New("Gemini ownership conflict")
	ErrUnresolvedLocalAuth       = errors.New("unresolved Gemini local authentication")
	ErrUnsupportedNativeProfile  = errors.New("unsupported Gemini native profile")
	ErrUnsupportedRepresentation = errors.New("unsupported Gemini representation")
	ErrCodecLimit                = errors.New("Gemini codec limit exceeded")
)

func validateDeclaration(value agentconfig.GeminiProviderDeclarationV1) error {
	if value.Version != 1 || value.Target != agentconfig.GeminiSettings || value.LocalAuthPolicy != agentconfig.GeminiPreserveLocalLoginV1 || value.RequiredSlots == nil {
		return ErrInvalidDeclaration
	}
	switch value.AuthMode {
	case agentconfig.GeminiNativeLogin:
		if value.SelectedType != "oauth-personal" || value.Endpoint != nil || value.KeyTransport != nil || value.CredentialReference != nil || len(value.RequiredSlots) != 0 {
			return ErrInvalidDeclaration
		}
	case agentconfig.GeminiAPIKeyAuth:
		if value.SelectedType != "gemini-api-key" || value.KeyTransport == nil || *value.KeyTransport != agentconfig.GeminiNativeDefault && *value.KeyTransport != agentconfig.GeminiBearer || value.CredentialReference == nil || *value.CredentialReference != agentconfig.GeminiAPIKeySecret || len(value.RequiredSlots) != 1 || value.RequiredSlots[0] != agentconfig.GeminiAPIKeySecret {
			return ErrInvalidDeclaration
		}
	default:
		return ErrInvalidDeclaration
	}
	if value.Endpoint != nil && !validEndpoint(*value.Endpoint) {
		return ErrInvalidDeclaration
	}
	for _, model := range []*string{value.EnvModel, value.SettingsModel} {
		if model != nil && !validModel(*model) {
			return ErrInvalidDeclaration
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > agentconfig.MaxGeminiDeclarationBytes {
		return ErrInvalidDeclaration
	}
	return nil
}

func validEndpoint(value string) bool {
	if len(value) < 1 || len(value) > 2048 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "?#\\") || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
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

func validModel(value string) bool {
	if len(value) < 1 || len(value) > 256 {
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
