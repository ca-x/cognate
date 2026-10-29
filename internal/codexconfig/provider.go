package codexconfig

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

var (
	ErrInvalidDeclaration        = errors.New("invalid Codex declaration")
	ErrInvalidLocalImage         = errors.New("invalid Codex local image")
	ErrInvalidSecret             = errors.New("invalid Codex secret material")
	ErrOwnershipConflict         = errors.New("Codex ownership conflict")
	ErrUnresolvedLocalAuth       = errors.New("unresolved Codex local authentication")
	ErrUnsupportedNativeProfile  = errors.New("unsupported Codex native profile")
	ErrUnsupportedRepresentation = errors.New("unsupported Codex representation")
	ErrCodecLimit                = errors.New("Codex codec limit exceeded")
)

const MaxImageBytes = 256 << 10
const maxBundleBytes = 768 << 10
const maxWriteBytes = 512 << 10
const maxEdits = 512

type Presence uint8

const (
	PresenceInvalid Presence = iota
	Missing
	Present
	NotObserved
)

type Role uint8

const (
	RoleInvalid Role = iota
	ConfigRole
	AuthRole
	CatalogRole
)

type Ownership uint8

const (
	OwnershipUnknown Ownership = iota
	Absent
	Managed
	Foreign
)

type AuthStore uint8

const (
	AuthStoreUnknown AuthStore = iota
	File
	Keyring
	Auto
	Ephemeral
)

type KeyringState uint8

const (
	KeyringUnknown KeyringState = iota
	FoundCredentialLogin
	KeyringMissing
	KeyringNotObserved
)

type LoginState uint8

const (
	LoginUnknown LoginState = iota
	CredentialLogin
	NoCredentialLogin
	LoginNotObserved
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
)

func validRole(role Role) bool { return role >= ConfigRole && role <= CatalogRole }

func NewImage(presence Presence, body []byte) (LocalImage, error) {
	if presence < Missing || presence > NotObserved || presence != Present && len(body) != 0 {
		return LocalImage{}, ErrInvalidLocalImage
	}
	if len(body) > MaxImageBytes {
		return LocalImage{}, ErrCodecLimit
	}
	return LocalImage{value: new(any(&imageData{presence: presence, body: bytes.Clone(body)}))}, nil
}

func NewImages(config, auth, catalog LocalImage) (LocalImages, error) {
	if config.Presence() == NotObserved {
		return LocalImages{}, ErrInvalidLocalImage
	}
	result := &imagesData{}
	total := 0
	for index, image := range []LocalImage{config, auth, catalog} {
		data := image.get[imageData]()
		if data == nil || data.presence < Missing || data.presence > NotObserved {
			result.destroy()
			return LocalImages{}, ErrInvalidLocalImage
		}
		total += len(data.body)
		if total > maxBundleBytes {
			result.destroy()
			return LocalImages{}, ErrCodecLimit
		}
		result.images[index], _ = NewImage(data.presence, data.body)
	}
	return LocalImages{value: new(any(result))}, nil
}

func NewOwnership(provider, catalog Ownership, managedModels []string) (OwnershipFacts, error) {
	if provider > Foreign || catalog > Foreign || len(managedModels) > 32 || catalog != Managed && len(managedModels) != 0 {
		return OwnershipFacts{}, ErrOwnershipConflict
	}
	for index, model := range managedModels {
		if !validModelID(model) || slices.Contains(managedModels[:index], model) {
			return OwnershipFacts{}, ErrOwnershipConflict
		}
	}
	return OwnershipFacts{value: new(any(&ownershipData{provider: provider, catalog: catalog, models: slices.Clone(managedModels)}))}, nil
}

func NewAuthFacts(store AuthStore, keyring KeyringState, fileLogin LoginState) (AuthFacts, error) {
	if store > Ephemeral || keyring > KeyringNotObserved || fileLogin > LoginNotObserved {
		return AuthFacts{}, ErrUnresolvedLocalAuth
	}
	return AuthFacts{value: new(any(&authData{store: store, keyring: keyring, fileLogin: fileLogin}))}, nil
}

func NewResolution(profileID string, scope EffectiveScope, ownership OwnershipFacts, auth AuthFacts, witness [32]byte) (LocalResolution, error) {
	owners, facts := ownership.get[ownershipData](), auth.get[authData]()
	if !validModelID(profileID) || scope > CompleteEffective || owners == nil || facts == nil {
		return LocalResolution{}, ErrUnresolvedLocalAuth
	}
	cloned := *owners
	cloned.models = slices.Clone(owners.models)
	return LocalResolution{value: new(any(&resolutionData{profileID: profileID, scope: scope, ownership: cloned, auth: *facts, witness: witness}))}, nil
}

func validSecret(body []byte) bool {
	return len(body) >= 1 && len(body) <= 8192 && utf8.Valid(body) && !bytes.ContainsAny(body, "\x00\r\n")
}
func NewSelectedSecret(slot agentconfig.CodexSecretSlot, body []byte) (SelectedSecret, error) {
	if slot != agentconfig.CodexProviderKeySecret || !validSecret(body) {
		return SelectedSecret{}, ErrInvalidSecret
	}
	return SelectedSecret{value: new(any(&secretData{slot: slot, body: bytes.Clone(body)}))}, nil
}

func validModelID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
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
func validEffort(value agentconfig.CodexReasoningEffort) bool {
	return slices.Contains([]agentconfig.CodexReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, value)
}
func validLiteral(value string, limit int) bool {
	return len(value) >= 1 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func jsonWithin(value any, limit int) bool {
	encoded, err := json.Marshal(value)
	return err == nil && len(encoded) <= limit
}

func validateDeclaration(value agentconfig.CodexProviderDeclarationV1) error {
	if value.APIVersion != agentconfig.APIVersion || value.Target != agentconfig.CodexConfig || value.ProviderID != agentconfig.CodexProviderID || value.WireAPI != agentconfig.CodexWireAPI || value.CredentialReference != agentconfig.CodexProviderKeySecret || len(value.RequiredSlots) != 1 || value.RequiredSlots[0] != agentconfig.CodexProviderKeySecret || value.LocalAuthPolicy != agentconfig.CodexPreserveLocalLoginV1 {
		return ErrInvalidDeclaration
	}
	if !validLiteral(value.Endpoint, 2048) || strings.ContainsAny(value.Endpoint, "?#\\") || !validModelID(value.Model) || value.ReasoningEffort != nil && !validEffort(*value.ReasoningEffort) {
		return ErrInvalidDeclaration
	}
	endpoint, err := url.Parse(value.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
		return ErrInvalidDeclaration
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return ErrInvalidDeclaration
		}
	}
	if value.Catalog != nil {
		models := value.Catalog.Models
		if len(models) < 1 || len(models) > 32 {
			return ErrInvalidDeclaration
		}
		seen := make(map[string]bool, len(models))
		for _, model := range models {
			if !validModelID(model.Model) || seen[model.Model] || !validCatalogModel(model) {
				return ErrInvalidDeclaration
			}
			seen[model.Model] = true
			if model.Model == value.Model && value.ReasoningEffort != nil && model.ReasoningLevels != nil && !slices.Contains(*model.ReasoningLevels, *value.ReasoningEffort) {
				return ErrInvalidDeclaration
			}
		}
		if !seen[value.Model] || !jsonWithin(value.Catalog, agentconfig.MaxCodexCatalogBytes) {
			return ErrInvalidDeclaration
		}
	}
	desired := agentconfig.CodexProviderDesired{AuthMode: agentconfig.CodexProviderKeyAuth, KeyPresent: true, Endpoint: value.Endpoint, Model: value.Model, ReasoningEffort: value.ReasoningEffort, Catalog: value.Catalog}
	if !jsonWithin(desired, agentconfig.MaxCodexDesiredBytes) || !jsonWithin(value, agentconfig.MaxCodexDeclarationBytes) {
		return ErrInvalidDeclaration
	}
	return nil
}

func validCatalogModel(model agentconfig.CodexCatalogModelDesired) bool {
	if model.DisplayName != nil && !validLiteral(*model.DisplayName, 256) || model.ContextWindow != nil && (*model.ContextWindow < 1 || *model.ContextWindow > 2147483647) {
		return false
	}
	if model.BaseInstructions != nil {
		body := *model.BaseInstructions
		if len(body) < 1 || len(body) > 2048 || !utf8.ValidString(body) || strings.ContainsFunc(body, func(character rune) bool {
			return unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t'
		}) {
			return false
		}
	}
	if model.InputModalities != nil {
		values := *model.InputModalities
		if len(values) < 1 || len(values) > 2 {
			return false
		}
		for index, value := range values {
			if value != "text" && value != "image" || slices.Contains(values[:index], value) {
				return false
			}
		}
	}
	if model.ReasoningLevels != nil {
		values := *model.ReasoningLevels
		if len(values) < 1 || len(values) > 7 {
			return false
		}
		for index, value := range values {
			if !validEffort(value) || slices.Contains(values[:index], value) {
				return false
			}
		}
	}
	return model.DefaultReasoningLevel == nil || model.ReasoningLevels != nil && slices.Contains(*model.ReasoningLevels, *model.DefaultReasoningLevel)
}

type FieldID uint8

const (
	FieldModelProvider FieldID = iota + 1
	FieldModel
	FieldReasoningEffort
	FieldCatalogReference
	FieldProviderName
	FieldEndpoint
	FieldWireAPI
	FieldRequiresAuth
	FieldBearer
	FieldCatalogModels
	FieldDisplayName
	FieldContextWindow
	FieldParallel
	FieldModalities
	FieldInstructions
	FieldReasoningLevels
	FieldDefaultReasoning
)

type ChangeKind uint8

const (
	ChangeKeep ChangeKind = iota + 1
	ChangeInsert
	ChangeSet
	ChangeReorder
	ChangeManagedModelRemove
)

type FieldPreview struct {
	Role              Role
	Field             FieldID
	Change            ChangeKind
	SecretPlaceholder bool
}

func (role Role) ID() string {
	switch role {
	case ConfigRole:
		return "codex.config"
	case AuthRole:
		return "codex.auth-baseline.v1"
	case CatalogRole:
		return "cognate-model-catalog.v1"
	default:
		return ""
	}
}

func CheckProvider(ctx context.Context, images LocalImages, declaration agentconfig.CodexProviderDeclarationV1, local LocalResolution) (ProviderPlan, error) {
	if ctx == nil {
		return ProviderPlan{}, ErrInvalidDeclaration
	}
	if err := ctx.Err(); err != nil {
		return ProviderPlan{}, err
	}
	resolution := local.get[resolutionData]()
	if resolution == nil {
		return ProviderPlan{}, ErrUnresolvedLocalAuth
	}
	profile, found := approvedProfile(resolution.profileID)
	if !found {
		return ProviderPlan{}, ErrUnsupportedNativeProfile
	}
	return checkProvider(ctx, images, declaration, local, profile)
}

func checkProvider(ctx context.Context, images LocalImages, declaration agentconfig.CodexProviderDeclarationV1, local LocalResolution, profile nativeProfile) (ProviderPlan, error) {
	if err := ctx.Err(); err != nil {
		return ProviderPlan{}, err
	}
	if err := validateDeclaration(declaration); err != nil {
		return ProviderPlan{}, err
	}
	declaration = cloneDeclaration(declaration)
	if err := validateDeclaration(declaration); err != nil {
		return ProviderPlan{}, err
	}
	source, resolution := images.get[imagesData](), local.get[resolutionData]()
	if source == nil {
		return ProviderPlan{}, ErrInvalidLocalImage
	}
	if resolution == nil {
		return ProviderPlan{}, ErrUnresolvedLocalAuth
	}
	captured, err := NewImages(source.images[0], source.images[1], source.images[2])
	if err != nil {
		return ProviderPlan{}, err
	}
	completed := false
	var catalog []byte
	defer func() {
		if !completed {
			captured.Destroy()
			clear(catalog)
		}
	}()
	profile = profile.clone()
	if err := validateProfile(ctx, profile); err != nil {
		return ProviderPlan{}, err
	}
	observation := *resolution
	observation.ownership.models = slices.Clone(resolution.ownership.models)
	values := captured.get[imagesData]().images
	document, err := indexTOML(ctx, values[0].get[imageData]().body)
	if err != nil {
		return ProviderPlan{}, err
	}
	if err := validateLocal(ctx, document, values[2], declaration, observation, profile); err != nil {
		return ProviderPlan{}, err
	}
	requires, err := resolveLocalAuth(ctx, document, values[1], observation, profile)
	if err != nil {
		return ProviderPlan{}, err
	}
	catalogEdits, removed := 0, 0
	if declaration.Catalog != nil {
		catalog, catalogEdits, removed, err = rewriteCatalog(ctx, values[2], declaration, observation.ownership, profile)
		if err != nil {
			return ProviderPlan{}, err
		}
	}
	if declaration.ReasoningEffort == nil {
		if retained, exists := document.lookup("model_reasoning_effort"); exists {
			finalCatalog, err := readJSON(ctx, catalog)
			if err != nil {
				return ProviderPlan{}, err
			}
			compatible := false
			for _, model := range finalCatalog.root.member("models").elements {
				if model.member("slug").text == declaration.Model {
					if levels := model.member("supported_reasoning_levels"); levels != nil {
						for _, level := range levels.elements {
							if level.member("effort").text == retained {
								compatible = true
							}
						}
					}
				}
			}
			if !compatible {
				return ProviderPlan{}, ErrOwnershipConflict
			}
		}
	}
	config, count, err := rewriteTOML(ctx, document, declaration, requires, []byte("[selected codex.provider-key]"))
	if err != nil {
		return ProviderPlan{}, err
	}
	defer clear(config)
	if count+catalogEdits > maxEdits {
		return ProviderPlan{}, ErrCodecLimit
	}
	if err := checkOutputSizes(len(config), len(catalog), len(values[1].get[imageData]().body), len(values[2].get[imageData]().body), declaration.Catalog != nil); err != nil {
		return ProviderPlan{}, err
	}
	preview := buildPreview(document, declaration, removed)
	if err := ctx.Err(); err != nil {
		return ProviderPlan{}, err
	}
	completed = true
	return ProviderPlan{value: new(any(&planData{images: captured, declaration: declaration, local: observation, profile: profile, document: document, catalog: catalog, catalogEdits: catalogEdits, requires: requires, preview: preview}))}, nil
}

func checkOutputSizes(config, catalog, auth, retainedCatalog int, catalogWrite bool) error {
	if config > MaxImageBytes || catalog > MaxImageBytes || auth > MaxImageBytes || retainedCatalog > MaxImageBytes {
		return ErrCodecLimit
	}
	written, retained := config, config+auth+retainedCatalog
	if catalogWrite {
		written += catalog
		retained = config + auth + catalog
	}
	if written > maxWriteBytes || retained > maxBundleBytes {
		return ErrCodecLimit
	}
	return nil
}

func buildPreview(document *tomlDocument, declaration agentconfig.CodexProviderDeclarationV1, removed int) []FieldPreview {
	paths := [][]string{{"model_provider"}, {"model"}, {"model_reasoning_effort"}, {"model_catalog_json"}, {"model_providers", "cognate", "name"}, {"model_providers", "cognate", "base_url"}, {"model_providers", "cognate", "wire_api"}, {"model_providers", "cognate", "requires_openai_auth"}, {"model_providers", "cognate", "experimental_bearer_token"}}
	result := make([]FieldPreview, 0, 18)
	for index, path := range paths {
		change := ChangeInsert
		if _, exists := document.leaves[pathID(path)]; exists {
			change = ChangeSet
		}
		if index == 2 && declaration.ReasoningEffort == nil || index == 3 && declaration.Catalog == nil {
			change = ChangeKeep
		}
		result = append(result, FieldPreview{Role: ConfigRole, Field: FieldID(index + 1), Change: change, SecretPlaceholder: index == 8})
	}
	if declaration.Catalog != nil {
		result = append(result, FieldPreview{Role: CatalogRole, Field: FieldCatalogModels, Change: ChangeReorder})
		if removed > 0 {
			result = append(result, FieldPreview{Role: CatalogRole, Field: FieldCatalogModels, Change: ChangeManagedModelRemove})
		}
		for field := FieldDisplayName; field <= FieldDefaultReasoning; field++ {
			selected := false
			for _, model := range declaration.Catalog.Models {
				switch field {
				case FieldDisplayName:
					selected = selected || model.DisplayName != nil
				case FieldContextWindow:
					selected = selected || model.ContextWindow != nil
				case FieldParallel:
					selected = selected || model.SupportsParallelToolCalls != nil
				case FieldModalities:
					selected = selected || model.InputModalities != nil
				case FieldInstructions:
					selected = selected || model.BaseInstructions != nil
				case FieldReasoningLevels:
					selected = selected || model.ReasoningLevels != nil
				case FieldDefaultReasoning:
					selected = selected || model.DefaultReasoningLevel != nil
				}
			}
			change := ChangeKeep
			if selected {
				change = ChangeSet
			}
			result = append(result, FieldPreview{Role: CatalogRole, Field: field, Change: change})
		}
	}
	return result
}

func RenderProvider(ctx context.Context, plan ProviderPlan, selected []SelectedSecret) (ProviderCandidate, error) {
	if ctx == nil {
		return ProviderCandidate{}, ErrInvalidDeclaration
	}
	if err := ctx.Err(); err != nil {
		return ProviderCandidate{}, err
	}
	data := plan.get[planData]()
	if data == nil || data.document == nil {
		return ProviderCandidate{}, ErrInvalidDeclaration
	}
	if len(selected) != 1 {
		return ProviderCandidate{}, ErrInvalidSecret
	}
	material := selected[0].get[secretData]()
	if material == nil || material.slot != agentconfig.CodexProviderKeySecret || !validSecret(material.body) {
		return ProviderCandidate{}, ErrInvalidSecret
	}
	secret := bytes.Clone(material.body)
	defer clear(secret)
	config, count, err := rewriteTOML(ctx, data.document, data.declaration, data.requires, secret)
	if err != nil {
		return ProviderCandidate{}, err
	}
	defer clear(config)
	values := data.images.get[imagesData]().images
	if count+data.catalogEdits > maxEdits {
		return ProviderCandidate{}, ErrCodecLimit
	}
	if err := checkOutputSizes(len(config), len(data.catalog), len(values[1].get[imageData]().body), len(values[2].get[imageData]().body), data.declaration.Catalog != nil); err != nil {
		return ProviderCandidate{}, err
	}
	configImage, _ := NewImage(Present, config)
	defer configImage.Destroy()
	catalogImage := values[2]
	if data.declaration.Catalog != nil {
		catalogImage, _ = NewImage(Present, data.catalog)
		defer catalogImage.Destroy()
	}
	images, err := NewImages(configImage, values[1], catalogImage)
	if err != nil {
		return ProviderCandidate{}, err
	}
	actions := [3]Action{Write, Keep, Keep}
	if data.declaration.Catalog != nil {
		actions[2] = Write
	}
	result := ProviderCandidate{value: new(any(&candidateData{images: images, actions: actions, binding: plan.Binding()}))}
	if err := ctx.Err(); err != nil {
		result.Destroy()
		return ProviderCandidate{}, err
	}
	return result, nil
}
