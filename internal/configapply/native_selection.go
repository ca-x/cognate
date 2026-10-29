package configapply

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
)

var (
	ErrNotSelected         = errors.New("configuration not selected")
	ErrSelection           = errors.New("invalid configuration selection")
	ErrUnconfirmed         = errors.New("configuration plan not confirmed")
	ErrPlanExpired         = errors.New("configuration plan expired")
	ErrConflict            = errors.New("configuration conflict")
	ErrIdempotency         = errors.New("configuration idempotency conflict")
	ErrTargetAbsent        = errors.New("configuration target directory absent")
	ErrUnsafeTarget        = errors.New("unsafe configuration filesystem")
	ErrUnsupportedPlatform = errors.New("configuration platform unsupported")
	ErrStateInvalid        = errors.New("configuration encrypted state invalid")
	ErrPending             = errors.New("configuration recovery required")
	ErrCapacity            = errors.New("configuration capacity exceeded")
	ErrOutcomeUnknown      = errors.New("configuration outcome unknown")
	ErrClosed              = errors.New("configuration engine closed")
)

type PackBinding struct {
	ID                 string
	Revision           uint64
	AttachmentRevision uint64
}

type SelectionInput struct {
	Selected               bool
	Origin                 string
	Owner                  string
	Profile                string
	DeviceID               string
	AssetID                string
	SchemaVersion          uint32
	AssetRevision          uint64
	Adapter                agentconfig.Descriptor
	RequiredCapabilities   []agentconfig.Capability
	Patch                  agentconfig.ClaudeProviderPatchV1
	SelectionRevision      uint64
	ConfirmationRevision   uint64
	PolicyRevision         uint64
	PolicyID               string
	AutoApply              bool
	OperationID            string
	PreparationID          string
	PreparationPreparedAt  time.Time
	PreparationExpiresAt   time.Time
	NativeSessionExpiresAt *time.Time
	Pack                   *PackBinding
	Scope                  string
	ConfigRootOverride     bool
}

type Selection struct{ private func() *selectionData }
type selectionData struct{ SelectionInput }

func NewSelection(input SelectionInput) (Selection, error) {
	if !input.Selected {
		return Selection{}, ErrNotSelected
	}
	if input.Scope != "user" || input.ConfigRootOverride || input.AutoApply {
		return Selection{}, ErrSelection
	}
	if input.PolicyID != "" && !nativeIdentifier(input.PolicyID) {
		return Selection{}, ErrSelection
	}
	origin, err := url.Parse(input.Origin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.Opaque != "" || origin.String() != input.Origin {
		return Selection{}, ErrSelection
	}
	for _, value := range []string{input.Owner, input.Profile, input.DeviceID, input.AssetID, input.OperationID, input.PreparationID} {
		if !nativeIdentifier(value) {
			return Selection{}, ErrSelection
		}
	}
	if input.SchemaVersion != 1 || input.AssetRevision == 0 || input.SelectionRevision == 0 || input.ConfirmationRevision == 0 {
		return Selection{}, ErrSelection
	}
	if input.PreparationPreparedAt.IsZero() || !input.PreparationExpiresAt.After(input.PreparationPreparedAt) || input.NativeSessionExpiresAt != nil && input.NativeSessionExpiresAt.IsZero() {
		return Selection{}, ErrSelection
	}
	manifest := input.Adapter.Manifest
	if manifest.APIVersion != agentconfig.APIVersion || manifest.PluginID != "builtin.claude" || manifest.PluginVersion != "1.1.0" || manifest.AgentID != "claude" || manifest.Target != agentconfig.ClaudeSettings || manifest.Format != agentconfig.JSON {
		return Selection{}, ErrSelection
	}
	digest, err := hex.DecodeString(input.Adapter.DefinitionDigest)
	if err != nil || len(digest) != 32 || input.Adapter.DefinitionDigest != strings.ToLower(input.Adapter.DefinitionDigest) {
		return Selection{}, ErrSelection
	}
	for _, capabilities := range [][]agentconfig.Capability{manifest.Capabilities, input.RequiredCapabilities} {
		if len(capabilities) == 0 || len(capabilities) > 16 || !slices.Contains(capabilities, agentconfig.ClaudeProviderSwitchV1) {
			return Selection{}, ErrSelection
		}
		seen := make(map[agentconfig.Capability]bool)
		for _, capability := range capabilities {
			if capability != agentconfig.ClaudeProviderSwitchV1 && capability != agentconfig.ClaudeProviderEnvV1 || seen[capability] {
				return Selection{}, ErrSelection
			}
			seen[capability] = true
		}
	}
	for _, capability := range input.RequiredCapabilities {
		if !slices.Contains(manifest.Capabilities, capability) {
			return Selection{}, ErrSelection
		}
	}
	if input.Pack != nil && (!nativeIdentifier(input.Pack.ID) || input.Pack.Revision == 0 || input.Pack.AttachmentRevision == 0) {
		return Selection{}, ErrSelection
	}
	input = cloneSelection(input)
	credentials := make(map[agentconfig.ClaudeSecretSlot]SecretMaterial)
	defer func() {
		for _, secret := range credentials {
			secret.Destroy()
		}
	}()
	for _, operation := range input.Patch.Operations {
		if operation.Reference != nil {
			secret, _ := NewSecretMaterial([]byte("validation"))
			credentials[*operation.Reference] = secret
		}
	}
	values, err := patchValues(input.Patch, credentials)
	clearValues(values)
	if err != nil {
		return Selection{}, ErrSelection
	}
	private := &selectionData{input}
	return Selection{private: func() *selectionData { return private }}, nil
}

func cloneSelection(input SelectionInput) SelectionInput {
	input.Adapter.Manifest.Capabilities = slices.Clone(input.Adapter.Manifest.Capabilities)
	input.RequiredCapabilities = slices.Clone(input.RequiredCapabilities)
	input.Patch.Operations = slices.Clone(input.Patch.Operations)
	for index := range input.Patch.Operations {
		operation := &input.Patch.Operations[index]
		if operation.Literal != nil {
			operation.Literal = new(*operation.Literal)
		}
		if operation.Reference != nil {
			operation.Reference = new(*operation.Reference)
		}
	}
	if input.Pack != nil {
		input.Pack = new(*input.Pack)
	}
	if input.NativeSessionExpiresAt != nil {
		input.NativeSessionExpiresAt = new(*input.NativeSessionExpiresAt)
	}
	return input
}

func nativeIdentifier(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsFunc(value, func(character rune) bool {
		return character < 0x21 || character > 0x7e || strings.ContainsRune("/\\.\"", character)
	})
}

func (Selection) String() string   { return "[private configuration selection]" }
func (Selection) GoString() string { return "[private configuration selection]" }
func (Selection) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private configuration selection]")
}
func (Selection) LogValue() slog.Value { return slog.StringValue("[private configuration selection]") }
func (Selection) MarshalJSON() ([]byte, error) {
	return bytes.Clone([]byte(`"[private configuration selection]"`)), nil
}
