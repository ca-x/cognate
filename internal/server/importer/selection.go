package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/plugin"
)

type SelectionSource string
type SelectionStatus string
type SelectionNoticeCode string

const (
	SelectionCCSwitch24         SelectionSource     = "ccswitch-schema24"
	SelectionMagpie             SelectionSource     = "magpie-library"
	SelectionReady              SelectionStatus     = "ready"
	SelectionNeedsChoice        SelectionStatus     = "needs-choice"
	SelectionNeedsBinding       SelectionStatus     = "needs-binding"
	SelectionUnsupportedAgent   SelectionStatus     = "unsupported-agent"
	SelectionUnsupportedKind    SelectionStatus     = "unsupported-kind"
	SelectionUnsupportedField   SelectionStatus     = "unsupported-field"
	SelectionCredentialsOmitted SelectionNoticeCode = "credentials-omitted"
	SelectionMetadataExcluded   SelectionNoticeCode = "metadata-excluded"
	SelectionSourceDataExcluded SelectionNoticeCode = "source-data-excluded"
	SelectionContentNotCompared SelectionNoticeCode = "source-content-not-compared"
	MaxSelectionProjectionBytes                     = 1 << 20
	MaxSelectionProviders                           = 32
)

var (
	ErrSelectionInvalid     = errors.New("invalid import selection")
	ErrSelectionLimit       = errors.New("import selection limit exceeded")
	ErrSelectionUnsupported = errors.New("unsupported import selection")
	ErrSelectionPlugin      = errors.New("invalid import adapter result")
)

type SelectionNotice struct {
	Code  SelectionNoticeCode `json:"code"`
	Count int                 `json:"count"`
}

type SelectionSlot struct {
	Slot    string `json:"slot"`
	Present bool   `json:"present"`
}

type SelectionRepository struct {
	ID            string `json:"id"`
	Repository    string `json:"repository"`
	Branch        string `json:"branch"`
	SourceEnabled *bool  `json:"sourceEnabled,omitzero"`
}

type SelectionSkillBinding struct {
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	RepositoryID string `json:"repositoryID"`
	ArchiveHash  string `json:"archiveHash"`
	SkillID      string `json:"skillID"`
	Path         string `json:"path"`
	ContentHash  string `json:"contentHash"`
}

type SelectionSkill struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Repository string                 `json:"repository"`
	Branch     string                 `json:"branch"`
	Path       *string                `json:"path,omitzero"`
	Status     SelectionStatus        `json:"status"`
	Binding    *SelectionSkillBinding `json:"binding,omitzero"`
	Notices    []SelectionNotice      `json:"notices"`
}

type SelectionConfiguration struct {
	ID      string               `json:"id"`
	AgentID string               `json:"agentID"`
	Kind    string               `json:"kind"`
	Name    string               `json:"name"`
	Status  SelectionStatus      `json:"status"`
	Draft   *configuration.Draft `json:"draft,omitzero"`
	Slots   []SelectionSlot      `json:"slots"`
	Notices []SelectionNotice    `json:"notices"`
}

type SelectionView struct {
	Source         SelectionSource          `json:"source"`
	SourceDigest   string                   `json:"sourceDigest"`
	Repositories   []SelectionRepository    `json:"repositories"`
	Skills         []SelectionSkill         `json:"skills"`
	Configurations []SelectionConfiguration `json:"configurations"`
	Notices        []SelectionNotice        `json:"notices"`
}

type SelectionCandidateOptions struct {
	CandidateID        string
	Name               *string
	ClaudeAuthMode     *configuration.AuthMode
	GeminiAuthMode     *agentconfig.GeminiAuthMode
	GeminiKeyTransport *agentconfig.GeminiKeyTransport
	SkillBinding       *SelectionSkillBinding
}

type SelectionOptions struct{ Candidates []SelectionCandidateOptions }

type selectionBox struct {
	mutex sync.Mutex
	view  *SelectionView
}

type SourceProjection struct{ data *selectionBox }
type SelectionCandidates struct{ data *selectionBox }

func ProjectSelection(ctx context.Context, source SelectionSource, raw []byte) (SourceProjection, error) {
	if err := ctx.Err(); err != nil {
		return SourceProjection{}, err
	}
	maximum := MaxMagpieExportBytes
	if source == SelectionCCSwitch24 {
		maximum = MaxCCSwitchSnapshotBytes
	} else if source != SelectionMagpie {
		return SourceProjection{}, ErrSelectionUnsupported
	}
	if len(raw) == 0 {
		return SourceProjection{}, ErrSelectionInvalid
	}
	if len(raw) > maximum {
		return SourceProjection{}, ErrSelectionLimit
	}
	digest := sha256.Sum256(raw)
	view := SelectionView{Source: source, SourceDigest: hex.EncodeToString(digest[:]), Repositories: []SelectionRepository{}, Skills: []SelectionSkill{}, Configurations: []SelectionConfiguration{}, Notices: []SelectionNotice{}}
	var err error
	if source == SelectionMagpie {
		err = projectMagpieSelection(ctx, raw, &view)
	} else {
		err = projectCCSwitchSelection(ctx, raw, &view)
	}
	if canceled := ctx.Err(); canceled != nil {
		return SourceProjection{}, canceled
	}
	if err != nil {
		return SourceProjection{}, selectionError(ctx, err)
	}
	identifiers := make(map[string]bool)
	for _, skill := range view.Skills {
		if identifiers[skill.ID] {
			return SourceProjection{}, ErrSelectionInvalid
		}
		identifiers[skill.ID] = true
	}
	for _, candidate := range view.Configurations {
		if identifiers[candidate.ID] {
			return SourceProjection{}, ErrSelectionInvalid
		}
		identifiers[candidate.ID] = true
	}
	encoded, err := json.Marshal(view)
	if err != nil || len(encoded) > MaxSelectionProjectionBytes {
		return SourceProjection{}, ErrSelectionLimit
	}
	return SourceProjection{data: &selectionBox{view: &view}}, nil
}

func BuildSelection(ctx context.Context, projection SourceProjection, options SelectionOptions) (SelectionCandidates, error) {
	if err := ctx.Err(); err != nil {
		return SelectionCandidates{}, err
	}
	view, err := projection.View()
	if err != nil {
		return SelectionCandidates{}, err
	}
	if len(options.Candidates) > MaxSelectionProviders+128 {
		return SelectionCandidates{}, ErrSelectionLimit
	}
	seen := make(map[string]bool)
	for _, option := range options.Candidates {
		if seen[option.CandidateID] || len(option.CandidateID) != 64 {
			return SelectionCandidates{}, ErrSelectionInvalid
		}
		seen[option.CandidateID] = true
		found := false
		for index := range view.Configurations {
			candidate := &view.Configurations[index]
			if candidate.ID != option.CandidateID {
				continue
			}
			found = true
			if err := applySelectionProviderOption(candidate, option); err != nil {
				return SelectionCandidates{}, err
			}
		}
		for index := range view.Skills {
			candidate := &view.Skills[index]
			if candidate.ID != option.CandidateID {
				continue
			}
			found = true
			if err := applySelectionSkillOption(candidate, option); err != nil {
				return SelectionCandidates{}, err
			}
		}
		if !found {
			return SelectionCandidates{}, ErrSelectionInvalid
		}
	}
	for index := range view.Configurations {
		finalizeSelectionProvider(&view.Configurations[index])
	}
	encoded, err := json.Marshal(view)
	if err != nil || len(encoded) > MaxSelectionProjectionBytes {
		return SelectionCandidates{}, ErrSelectionLimit
	}
	defer clear(encoded)
	wire, err := selectionWireValue(encoded)
	if err != nil || len(wire) > MaxSelectionProjectionBytes {
		return SelectionCandidates{}, ErrSelectionLimit
	}
	defer clear(wire)
	module, err := selectionModule(view.Source)
	if err != nil {
		return SelectionCandidates{}, err
	}
	var actual map[string]any
	if err := plugin.RunJSON(ctx, module, "previewSelection", wire, &actual); err != nil {
		if errors.Is(err, context.Canceled) {
			return SelectionCandidates{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return SelectionCandidates{}, context.DeadlineExceeded
		}
		return SelectionCandidates{}, selectionError(ctx, ErrSelectionPlugin)
	}
	var expected map[string]any
	if json.Unmarshal(wire, &expected) != nil || !reflect.DeepEqual(actual, expected) {
		return SelectionCandidates{}, ErrSelectionPlugin
	}
	if err := ctx.Err(); err != nil {
		return SelectionCandidates{}, err
	}
	return SelectionCandidates{data: &selectionBox{view: &view}}, nil
}

func SelectionAdapterDigest(source SelectionSource) (string, error) {
	module, err := selectionModule(source)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("cognate/import-selection/schema24/v1\x00" + string(source) + "\x00" + module))
	return hex.EncodeToString(digest[:]), nil
}

func selectionModule(source SelectionSource) (string, error) {
	switch source {
	case SelectionCCSwitch24:
		return ccSwitchModule, nil
	case SelectionMagpie:
		return magpieModule, nil
	default:
		return "", ErrSelectionUnsupported
	}
}

func applySelectionProviderOption(candidate *SelectionConfiguration, option SelectionCandidateOptions) error {
	if candidate.Draft == nil || option.SkillBinding != nil {
		return ErrSelectionInvalid
	}
	if option.Name != nil {
		if !selectionName(*option.Name) {
			return ErrSelectionInvalid
		}
		candidate.Name, candidate.Draft.Name = *option.Name, *option.Name
	}
	switch candidate.AgentID {
	case "claude":
		if option.GeminiAuthMode != nil || option.GeminiKeyTransport != nil {
			return ErrSelectionInvalid
		}
		if option.ClaudeAuthMode != nil {
			mode := *option.ClaudeAuthMode
			if mode != configuration.NativeLogin && mode != configuration.APIKey && mode != configuration.AuthToken || mode == configuration.NativeLogin && candidate.Draft.Provider.Endpoint != nil {
				return ErrSelectionInvalid
			}
			candidate.Draft.Provider.AuthMode = mode
		}
	case "codex", "grokbuild":
		if option.ClaudeAuthMode != nil || option.GeminiAuthMode != nil || option.GeminiKeyTransport != nil {
			return ErrSelectionInvalid
		}
	case "gemini":
		if option.ClaudeAuthMode != nil || candidate.Draft.GeminiProvider == nil {
			return ErrSelectionInvalid
		}
		provider := candidate.Draft.GeminiProvider
		if option.GeminiAuthMode != nil {
			mode := *option.GeminiAuthMode
			if mode != agentconfig.GeminiNativeLogin && mode != agentconfig.GeminiAPIKeyAuth || provider.AuthMode != "" && provider.AuthMode != mode || mode == agentconfig.GeminiNativeLogin && provider.Endpoint != nil {
				return ErrSelectionInvalid
			}
			provider.AuthMode = mode
		}
		if option.GeminiKeyTransport != nil {
			transport := *option.GeminiKeyTransport
			if transport != agentconfig.GeminiNativeDefault && transport != agentconfig.GeminiBearer || provider.AuthMode != agentconfig.GeminiAPIKeyAuth {
				return ErrSelectionInvalid
			}
			provider.KeyTransport = new(transport)
		}
	default:
		return ErrSelectionInvalid
	}
	return nil
}

func finalizeSelectionProvider(candidate *SelectionConfiguration) {
	if candidate.Draft == nil {
		return
	}
	candidate.Status = SelectionNeedsChoice
	if !selectionName(candidate.Draft.Name) {
		return
	}
	switch candidate.AgentID {
	case "claude":
		if candidate.Draft.Provider.AuthMode != "" {
			candidate.Status = SelectionReady
		}
	case "codex":
		candidate.Status = SelectionReady
	case "grokbuild":
		if candidate.Draft.GrokBuildProvider != nil {
			candidate.Status = SelectionReady
		}
	case "gemini":
		provider := candidate.Draft.GeminiProvider
		if provider != nil && (provider.AuthMode == agentconfig.GeminiNativeLogin && provider.KeyTransport == nil || provider.AuthMode == agentconfig.GeminiAPIKeyAuth && provider.KeyTransport != nil) {
			candidate.Status = SelectionReady
		}
	}
}

func applySelectionSkillOption(candidate *SelectionSkill, option SelectionCandidateOptions) error {
	if option.Name != nil || option.ClaudeAuthMode != nil || option.GeminiAuthMode != nil || option.GeminiKeyTransport != nil || option.SkillBinding == nil || candidate.Status != SelectionNeedsBinding {
		return ErrSelectionInvalid
	}
	binding := *option.SkillBinding
	if binding.Repository != candidate.Repository || binding.Branch != candidate.Branch || !selectionText(binding.RepositoryID, 64) || !selectionText(binding.SkillID, 64) || !selectionHexDigest(binding.ArchiveHash) || !selectionHexDigest(binding.ContentHash) || !validMagpieSkillPath(binding.Path) || candidate.Path != nil && *candidate.Path != binding.Path {
		return ErrSelectionInvalid
	}
	candidate.Binding, candidate.Status = &binding, SelectionReady
	selectionNotice(&candidate.Notices, SelectionContentNotCompared, 1)
	return nil
}

func selectionHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func selectionWireValue(raw []byte) (jsontext.Value, error) {
	if len(raw) == 0 {
		return nil, ErrSelectionInvalid
	}
	switch raw[0] {
	case '{':
		var object map[string]jsontext.Value
		if json.Unmarshal(raw, &object) != nil {
			return nil, ErrSelectionInvalid
		}
		defer clearSourceObject(object)
		for key, value := range object {
			var converted jsontext.Value
			var err error
			if key == "contextWindow" {
				var window uint64
				if json.Unmarshal(value, &window) != nil {
					return nil, ErrSelectionInvalid
				}
				converted, err = json.Marshal(strconv.FormatUint(window, 10))
			} else {
				converted, err = selectionWireValue(value)
			}
			if err != nil {
				return nil, err
			}
			clear(value)
			object[key] = converted
		}
		return json.Marshal(object)
	case '[':
		var values []jsontext.Value
		if json.Unmarshal(raw, &values) != nil {
			return nil, ErrSelectionInvalid
		}
		defer func() {
			for _, value := range values {
				clear(value)
			}
		}()
		for index, value := range values {
			converted, err := selectionWireValue(value)
			if err != nil {
				return nil, err
			}
			clear(value)
			values[index] = converted
		}
		return json.Marshal(values)
	default:
		return bytes.Clone(raw), nil
	}
}

func (projection SourceProjection) View() (SelectionView, error) {
	return readSelectionBox(projection.data)
}

func (projection SourceProjection) Clone() SourceProjection {
	view, err := projection.View()
	if err != nil {
		return SourceProjection{}
	}
	return SourceProjection{data: &selectionBox{view: &view}}
}
func (projection SourceProjection) Destroy() { destroySelectionBox(projection.data) }
func (candidates SelectionCandidates) View() (SelectionView, error) {
	return readSelectionBox(candidates.data)
}
func (candidates SelectionCandidates) Clone() SelectionCandidates {
	view, err := candidates.View()
	if err != nil {
		return SelectionCandidates{}
	}
	return SelectionCandidates{data: &selectionBox{view: &view}}
}
func (candidates SelectionCandidates) Destroy() { destroySelectionBox(candidates.data) }

func readSelectionBox(box *selectionBox) (SelectionView, error) {
	if box == nil {
		return SelectionView{}, ErrSelectionInvalid
	}
	box.mutex.Lock()
	defer box.mutex.Unlock()
	if box.view == nil {
		return SelectionView{}, ErrSelectionInvalid
	}
	encoded, err := json.Marshal(box.view)
	if err != nil {
		return SelectionView{}, ErrSelectionInvalid
	}
	defer clear(encoded)
	var view SelectionView
	if json.Unmarshal(encoded, &view) != nil {
		return SelectionView{}, ErrSelectionInvalid
	}
	return view, nil
}

func destroySelectionBox(box *selectionBox) {
	if box == nil {
		return
	}
	box.mutex.Lock()
	defer box.mutex.Unlock()
	if box.view != nil {
		*box.view = SelectionView{}
		box.view = nil
	}
}

func (SourceProjection) String() string   { return "[redacted source projection]" }
func (SourceProjection) GoString() string { return "[redacted source projection]" }
func (SourceProjection) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted source projection]")
}
func (SourceProjection) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted source projection]"`), nil
}
func (SourceProjection) MarshalText() ([]byte, error) {
	return []byte("[redacted source projection]"), nil
}
func (SourceProjection) LogValue() slog.Value {
	return slog.StringValue("[redacted source projection]")
}
func (SelectionCandidates) String() string   { return "[redacted import candidates]" }
func (SelectionCandidates) GoString() string { return "[redacted import candidates]" }
func (SelectionCandidates) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted import candidates]")
}
func (SelectionCandidates) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted import candidates]"`), nil
}
func (SelectionCandidates) MarshalText() ([]byte, error) {
	return []byte("[redacted import candidates]"), nil
}
func (SelectionCandidates) LogValue() slog.Value {
	return slog.StringValue("[redacted import candidates]")
}

func selectionError(ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, ErrSelectionLimit, ErrSelectionUnsupported, ErrSelectionInvalid, ErrSelectionPlugin} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return ErrSelectionInvalid
}

func selectionID(view *SelectionView, kind, locator string) string {
	digest := sha256.Sum256([]byte("cognate/import-selection/v1\x00" + view.SourceDigest + "\x00" + kind + "\x00" + locator))
	return hex.EncodeToString(digest[:])
}

func sourceObject(raw []byte) (map[string]jsontext.Value, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(raw) > MaxSelectionProjectionBytes || trimmed[0] != '{' {
		return nil, ErrSelectionUnsupported
	}
	var result map[string]jsontext.Value
	if json.Unmarshal(raw, &result) != nil || result == nil || len(result) > 128 {
		return nil, ErrSelectionUnsupported
	}
	return result, nil
}

func clearSourceObject(object map[string]jsontext.Value) {
	for _, value := range object {
		clear(value)
	}
	clear(object)
}

func sourceFields(object map[string]jsontext.Value, allowed ...string) bool {
	for key := range object {
		found := false
		for _, field := range allowed {
			if key == field {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sourceString(raw []byte) (string, error) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", ErrSelectionUnsupported
	}
	return value, nil
}

func selectionText(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func selectionName(value string) bool {
	return selectionText(value, 128) && strings.TrimSpace(value) == value
}

func selectionNotice(notices *[]SelectionNotice, code SelectionNoticeCode, count int) {
	if count <= 0 {
		return
	}
	for index := range *notices {
		if (*notices)[index].Code == code {
			(*notices)[index].Count += count
			return
		}
	}
	*notices = append(*notices, SelectionNotice{Code: code, Count: count})
}
