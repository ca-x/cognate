package codexconfig

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"slices"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type opaque struct{ value *any }
type LocalImage struct{ opaque }
type LocalImages struct{ opaque }
type OwnershipFacts struct{ opaque }
type AuthFacts struct{ opaque }
type LocalResolution struct{ opaque }
type SelectedSecret struct{ opaque }
type ProviderPlan struct{ opaque }
type ProviderCandidate struct{ opaque }
type ProviderBinding struct{ opaque }

func (carrier opaque) get[Value any]() *Value {
	if carrier.value == nil {
		return nil
	}
	value, _ := (*carrier.value).(*Value)
	return value
}

func (carrier opaque) Destroy() {
	if carrier.value == nil {
		return
	}
	if value, ok := (*carrier.value).(interface{ destroy() }); ok {
		value.destroy()
	}
	*carrier.value = nil
}

func (opaque) String() string                 { return "[redacted codex codec]" }
func (opaque) GoString() string               { return "[redacted codex codec]" }
func (opaque) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[redacted codex codec]") }
func (opaque) MarshalJSON() ([]byte, error)   { return []byte(`"[redacted codex codec]"`), nil }
func (opaque) MarshalText() ([]byte, error)   { return []byte("[redacted codex codec]"), nil }
func (opaque) LogValue() slog.Value           { return slog.StringValue("[redacted codex codec]") }

type imageData struct {
	presence Presence
	body     []byte
}

func (image *imageData) destroy() {
	clear(image.body)
	image.body = nil
	image.presence = PresenceInvalid
}

type imagesData struct{ images [3]LocalImage }

func (images *imagesData) destroy() {
	for _, image := range images.images {
		image.Destroy()
	}
	images.images = [3]LocalImage{}
}

type ownershipData struct {
	provider Ownership
	catalog  Ownership
	models   []string
}
type authData struct {
	store     AuthStore
	keyring   KeyringState
	fileLogin LoginState
}
type resolutionData struct {
	profileID string
	scope     EffectiveScope
	ownership ownershipData
	auth      authData
	witness   [32]byte
}
type secretData struct {
	slot agentconfig.CodexSecretSlot
	body []byte
}

func (secret *secretData) destroy() { clear(secret.body); secret.body = nil; secret.slot = "" }

func (image LocalImage) Presence() Presence {
	if data := image.get[imageData](); data != nil {
		return data.presence
	}
	return PresenceInvalid
}
func (image LocalImage) CopyBytes() []byte {
	if data := image.get[imageData](); data != nil {
		return bytes.Clone(data.body)
	}
	return nil
}
func (images LocalImages) CopyImage(role Role) LocalImage {
	if data := images.get[imagesData](); data != nil && validRole(role) {
		image := data.images[int(role)-1]
		copyImage, _ := NewImage(image.Presence(), image.get[imageData]().body)
		return copyImage
	}
	return LocalImage{}
}

type BaselineBinding struct {
	Role     Role
	Presence Presence
	Digest   [32]byte
}
type ObservationBinding struct {
	ProfileID          string
	TemplateDigest     [32]byte
	ProfileDigest      [32]byte
	Policy             agentconfig.CodexLocalAuthPolicy
	RequiresOpenAIAuth bool
	Scope              EffectiveScope
	AuthStore          AuthStore
	Keyring            KeyringState
	FileLogin          LoginState
	ProviderOwnership  Ownership
	CatalogOwnership   Ownership
	ManagedModels      []string
	Witness            [32]byte
}
type bindingData struct {
	baselines   [3]BaselineBinding
	declaration agentconfig.CodexProviderDeclarationV1
	observation ObservationBinding
}

func (data *bindingData) destroy() { *data = bindingData{} }

type planData struct {
	images       LocalImages
	declaration  agentconfig.CodexProviderDeclarationV1
	local        resolutionData
	profile      nativeProfile
	document     *tomlDocument
	catalog      []byte
	catalogEdits int
	requires     bool
	preview      []FieldPreview
}

func (data *planData) destroy() {
	data.images.Destroy()
	clear(data.catalog)
	*data = planData{}
}

type candidateData struct {
	images  LocalImages
	actions [3]Action
	binding ProviderBinding
}

func (data *candidateData) destroy() {
	data.images.Destroy()
	data.binding.Destroy()
	*data = candidateData{}
}

func (plan ProviderPlan) Preview() []FieldPreview {
	if data := plan.get[planData](); data != nil {
		return slices.Clone(data.preview)
	}
	return nil
}
func (plan ProviderPlan) Binding() ProviderBinding {
	data := plan.get[planData]()
	if data == nil {
		return ProviderBinding{}
	}
	result := &bindingData{declaration: cloneDeclaration(data.declaration)}
	for index, image := range data.images.get[imagesData]().images {
		baseline := BaselineBinding{Role: Role(index + 1), Presence: image.Presence()}
		if image.Presence() == Present {
			baseline.Digest = sha256.Sum256(image.get[imageData]().body)
		}
		result.baselines[index] = baseline
	}
	result.observation = ObservationBinding{
		ProfileID: data.profile.id, TemplateDigest: profileTemplateDigest(data.profile), ProfileDigest: profileDigest(data.profile),
		Policy: data.declaration.LocalAuthPolicy, RequiresOpenAIAuth: data.requires, Scope: data.local.scope,
		AuthStore: data.local.auth.store, Keyring: data.local.auth.keyring, FileLogin: data.local.auth.fileLogin,
		ProviderOwnership: data.local.ownership.provider, CatalogOwnership: data.local.ownership.catalog,
		ManagedModels: slices.Clone(data.local.ownership.models), Witness: data.local.witness,
	}
	return ProviderBinding{value: new(any(result))}
}
func (binding ProviderBinding) CopyBaselines() [3]BaselineBinding {
	if data := binding.get[bindingData](); data != nil {
		return data.baselines
	}
	return [3]BaselineBinding{}
}
func (binding ProviderBinding) CopyDeclaration() agentconfig.CodexProviderDeclarationV1 {
	if data := binding.get[bindingData](); data != nil {
		return cloneDeclaration(data.declaration)
	}
	return agentconfig.CodexProviderDeclarationV1{}
}
func (binding ProviderBinding) CopyObservation() ObservationBinding {
	if data := binding.get[bindingData](); data != nil {
		result := data.observation
		result.ManagedModels = slices.Clone(result.ManagedModels)
		return result
	}
	return ObservationBinding{}
}
func (candidate ProviderCandidate) CopyImage(role Role) LocalImage {
	if data := candidate.get[candidateData](); data != nil {
		return data.images.CopyImage(role)
	}
	return LocalImage{}
}
func (candidate ProviderCandidate) Action(role Role) Action {
	if data := candidate.get[candidateData](); data != nil && validRole(role) {
		return data.actions[int(role)-1]
	}
	return ActionInvalid
}
func (candidate ProviderCandidate) Binding() ProviderBinding {
	data := candidate.get[candidateData]()
	if data == nil {
		return ProviderBinding{}
	}
	result := &bindingData{baselines: data.binding.CopyBaselines(), declaration: data.binding.CopyDeclaration(), observation: data.binding.CopyObservation()}
	return ProviderBinding{value: new(any(result))}
}

func cloneDeclaration(value agentconfig.CodexProviderDeclarationV1) agentconfig.CodexProviderDeclarationV1 {
	value.RequiredSlots = slices.Clone(value.RequiredSlots)
	if value.ReasoningEffort != nil {
		value.ReasoningEffort = new(*value.ReasoningEffort)
	}
	if value.Catalog == nil {
		return value
	}
	value.Catalog = &agentconfig.CodexCatalogDesired{Models: slices.Clone(value.Catalog.Models)}
	for index := range value.Catalog.Models {
		model := &value.Catalog.Models[index]
		if model.DisplayName != nil {
			model.DisplayName = new(*model.DisplayName)
		}
		if model.ContextWindow != nil {
			model.ContextWindow = new(*model.ContextWindow)
		}
		if model.SupportsParallelToolCalls != nil {
			model.SupportsParallelToolCalls = new(*model.SupportsParallelToolCalls)
		}
		if model.BaseInstructions != nil {
			model.BaseInstructions = new(*model.BaseInstructions)
		}
		if model.DefaultReasoningLevel != nil {
			model.DefaultReasoningLevel = new(*model.DefaultReasoningLevel)
		}
		if model.InputModalities != nil {
			model.InputModalities = new(slices.Clone(*model.InputModalities))
		}
		if model.ReasoningLevels != nil {
			model.ReasoningLevels = new(slices.Clone(*model.ReasoningLevels))
		}
	}
	return value
}
