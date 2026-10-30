package geminiconfig

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type opaque struct{ private func() *any }
type LocalImage struct{ opaque }
type LocalImages struct{ opaque }
type LocalResolution struct{ opaque }
type SelectedSecret struct{ opaque }
type ProviderPlan struct{ opaque }
type ProviderCandidate struct{ opaque }
type ProviderBinding struct{ opaque }

func own(value any) opaque { state := new(value); return opaque{private: func() *any { return state }} }
func (value opaque) get[Value any]() *Value {
	if value.private == nil {
		return nil
	}
	data, _ := (*value.private()).(*Value)
	return data
}
func (value opaque) Destroy() {
	if value.private == nil {
		return
	}
	if data, ok := (*value.private()).(interface{ destroy() }); ok {
		data.destroy()
	}
	*value.private() = nil
}
func (opaque) String() string                 { return "[private Gemini codec]" }
func (opaque) GoString() string               { return "[private Gemini codec]" }
func (opaque) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[private Gemini codec]") }
func (opaque) MarshalJSON() ([]byte, error)   { return []byte(`"[private Gemini codec]"`), nil }
func (opaque) MarshalText() ([]byte, error)   { return []byte("[private Gemini codec]"), nil }
func (opaque) LogValue() slog.Value           { return slog.StringValue("[private Gemini codec]") }

type secretData struct {
	slot agentconfig.GeminiSecretSlot
	body []byte
}

func (value *secretData) destroy() { clear(value.body); value.body = nil; value.slot = "" }
func NewSelectedSecret(slot agentconfig.GeminiSecretSlot, body []byte) (SelectedSecret, error) {
	if slot != agentconfig.GeminiAPIKeySecret || len(body) < 1 || len(body) > 8192 || !utf8.Valid(body) || bytes.ContainsAny(body, "\x00\r\n") {
		return SelectedSecret{}, ErrInvalidSecret
	}
	return SelectedSecret{opaque: own(&secretData{slot: slot, body: bytes.Clone(body)})}, nil
}

type imageData struct {
	presence Presence
	body     []byte
}

func (value *imageData) destroy() {
	clear(value.body)
	value.body = nil
	value.presence = PresenceInvalid
}

type imagesData struct{ images [2]LocalImage }

func (value *imagesData) destroy() {
	for _, image := range value.images {
		image.Destroy()
	}
	value.images = [2]LocalImage{}
}
func NewImage(presence Presence, body []byte) (LocalImage, error) {
	if presence != Missing && presence != Present || presence == Missing && len(body) != 0 {
		return LocalImage{}, ErrInvalidLocalImage
	}
	if len(body) > MaxImageBytes {
		return LocalImage{}, ErrCodecLimit
	}
	return LocalImage{opaque: own(&imageData{presence: presence, body: bytes.Clone(body)})}, nil
}
func NewImages(env, settings LocalImage) (LocalImages, error) {
	value := &imagesData{}
	total := 0
	for index, image := range []LocalImage{env, settings} {
		data := image.get[imageData]()
		if data == nil {
			value.destroy()
			return LocalImages{}, ErrInvalidLocalImage
		}
		total += len(data.body)
		if total > MaxPairBytes {
			value.destroy()
			return LocalImages{}, ErrCodecLimit
		}
		copyImage, err := NewImage(data.presence, data.body)
		if err != nil {
			value.destroy()
			return LocalImages{}, err
		}
		value.images[index] = copyImage
	}
	return LocalImages{opaque: own(value)}, nil
}
func validRole(role Role) bool { return role == EnvRole || role == SettingsRole }
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
		source := data.images[int(role)-1].get[imageData]()
		if source != nil {
			result, _ := NewImage(source.presence, source.body)
			return result
		}
	}
	return LocalImage{}
}

type resolutionData struct {
	profileID string
	scope     EffectiveScope
	fields    [6]FieldFact
	witness   [32]byte
}

func (value *resolutionData) destroy() { *value = resolutionData{} }
func NewResolution(profileID string, scope EffectiveScope, facts []FieldFact, witness [32]byte) (LocalResolution, error) {
	if !validModel(profileID) || len(profileID) > 128 || scope > CompleteEffective || len(facts) != len(fields) {
		return LocalResolution{}, ErrUnresolvedLocalAuth
	}
	value := &resolutionData{profileID: strings.Clone(profileID), scope: scope, witness: witness}
	seen := [6]bool{}
	for _, fact := range facts {
		index := slices.Index(fields[:], fact.Field)
		if index < 0 || seen[index] || fact.Ownership < Absent || fact.Ownership > Foreign || fact.Cleanup < Preserve || fact.Cleanup > Clear || fact.Cleanup == Clear && fact.Ownership != Managed {
			return LocalResolution{}, ErrOwnershipConflict
		}
		seen[index] = true
		value.fields[index] = fact
	}
	return LocalResolution{opaque: own(value)}, nil
}

type BaselineBinding struct {
	Role     Role
	Presence Presence
	Digest   [32]byte
}
type ObservationBinding struct {
	ProfileID     string
	Dialect       string
	SourceDigest  [32]byte
	ProfileDigest [32]byte
	Scope         EffectiveScope
	Policy        agentconfig.GeminiLocalAuthPolicy
	Witness       [32]byte
	Fields        [6]FieldFact
}
type bindingData struct {
	baselines   [2]BaselineBinding
	declaration agentconfig.GeminiProviderDeclarationV1
	observation ObservationBinding
	actions     [6]FieldAction
}

func (value *bindingData) destroy() { *value = bindingData{} }

type planData struct {
	images      LocalImages
	declaration agentconfig.GeminiProviderDeclarationV1
	local       resolutionData
	profile     nativeProfile
	actions     [6]FieldAction
}

func (value *planData) destroy() { value.images.Destroy(); *value = planData{} }

type candidateData struct {
	images  LocalImages
	binding ProviderBinding
	actions [2]Action
}

func (value *candidateData) destroy() {
	value.images.Destroy()
	value.binding.Destroy()
	*value = candidateData{}
}
func cloneDeclaration(value agentconfig.GeminiProviderDeclarationV1) agentconfig.GeminiProviderDeclarationV1 {
	value.RequiredSlots = slices.Clone(value.RequiredSlots)
	if value.Endpoint != nil {
		value.Endpoint = new(*value.Endpoint)
	}
	if value.KeyTransport != nil {
		value.KeyTransport = new(*value.KeyTransport)
	}
	if value.EnvModel != nil {
		value.EnvModel = new(*value.EnvModel)
	}
	if value.SettingsModel != nil {
		value.SettingsModel = new(*value.SettingsModel)
	}
	if value.CredentialReference != nil {
		value.CredentialReference = new(*value.CredentialReference)
	}
	return value
}
func (plan ProviderPlan) Binding() ProviderBinding {
	data := plan.get[planData]()
	if data == nil {
		return ProviderBinding{}
	}
	value := &bindingData{declaration: cloneDeclaration(data.declaration), actions: data.actions, observation: ObservationBinding{ProfileID: data.profile.id, Dialect: data.profile.dialect, SourceDigest: data.profile.sourceDigest, ProfileDigest: profileDigest(data.profile), Scope: data.local.scope, Policy: data.declaration.LocalAuthPolicy, Witness: data.local.witness, Fields: data.local.fields}}
	for index, image := range data.images.get[imagesData]().images {
		source := image.get[imageData]()
		value.baselines[index] = BaselineBinding{Role: Role(index + 1), Presence: source.presence}
		if source.presence == Present {
			value.baselines[index].Digest = sha256.Sum256(source.body)
		}
	}
	return ProviderBinding{opaque: own(value)}
}
func (binding ProviderBinding) CopyBaselines() [2]BaselineBinding {
	if data := binding.get[bindingData](); data != nil {
		return data.baselines
	}
	return [2]BaselineBinding{}
}
func (binding ProviderBinding) CopyDeclaration() agentconfig.GeminiProviderDeclarationV1 {
	if data := binding.get[bindingData](); data != nil {
		return cloneDeclaration(data.declaration)
	}
	return agentconfig.GeminiProviderDeclarationV1{}
}
func (binding ProviderBinding) CopyObservation() ObservationBinding {
	if data := binding.get[bindingData](); data != nil {
		return data.observation
	}
	return ObservationBinding{}
}
func (binding ProviderBinding) Actions() [6]FieldAction {
	if data := binding.get[bindingData](); data != nil {
		return data.actions
	}
	return [6]FieldAction{}
}
func (plan ProviderPlan) Actions() [6]FieldAction {
	if data := plan.get[planData](); data != nil {
		return data.actions
	}
	return [6]FieldAction{}
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
	if data := candidate.get[candidateData](); data != nil {
		return ProviderBinding{opaque: own(&bindingData{baselines: data.binding.CopyBaselines(), declaration: data.binding.CopyDeclaration(), observation: data.binding.CopyObservation(), actions: data.binding.Actions()})}
	}
	return ProviderBinding{}
}
