package configapply

import (
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"google.golang.org/protobuf/proto"
)

type CodexLocalState uint8

const (
	CodexPreviewed CodexLocalState = iota + 1
	CodexAuthorizeAttempted
	CodexAuthorized
	CodexConsumeAttempted
	CodexCandidatesDurable
	CodexPublishingCatalog
	CodexPublishingConfig
	CodexCompleted
	CodexReconcileRequired
	CodexNeedsAttention
	CodexRemoteOutcomeUnknown
	CodexRestorePrepared
	CodexRestoring
	CodexRestored
	CodexAbandonedBeforeWrite
)

type CodexErrorClass uint8

const (
	CodexInvalid CodexErrorClass = iota + 1
	CodexUnsupported
	CodexConflict
	CodexCapacity
	CodexExpired
	CodexClosed
	CodexBusy
	CodexUnknown
	CodexAttention
	CodexIO
)

type codexError struct{ class CodexErrorClass }

func (err codexError) Error() string { return "Codex local operation refused" }

func (err codexError) Is(target error) bool {
	other, ok := target.(codexError)
	return ok && other.class == err.class
}

var (
	ErrCodexInvalid     error = codexError{CodexInvalid}
	ErrCodexUnsupported error = codexError{CodexUnsupported}
	ErrCodexConflict    error = codexError{CodexConflict}
	ErrCodexCapacity    error = codexError{CodexCapacity}
	ErrCodexExpired     error = codexError{CodexExpired}
	ErrCodexClosed      error = codexError{CodexClosed}
	ErrCodexUnknown     error = codexError{CodexUnknown}
	ErrCodexAttention   error = codexError{CodexAttention}
)

func codexSafeError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{ErrCodexInvalid, ErrCodexUnsupported, ErrCodexConflict, ErrCodexCapacity, ErrCodexExpired, ErrCodexClosed, ErrCodexUnknown, ErrCodexAttention, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	switch {
	case errors.Is(err, ErrConflict):
		return ErrCodexConflict
	case errors.Is(err, ErrCapacity):
		return ErrCodexCapacity
	case errors.Is(err, ErrUnsupportedPlatform):
		return ErrCodexUnsupported
	case errors.Is(err, ErrOutcomeUnknown):
		return ErrCodexUnknown
	default:
		return ErrCodexAttention
	}
}

type codexOpaque struct{ value *any }

func (carrier codexOpaque) get[Value any]() *Value {
	if carrier.value == nil {
		return nil
	}
	value, _ := (*carrier.value).(*Value)
	return value
}

func (carrier codexOpaque) Destroy() {
	if carrier.value == nil {
		return
	}
	if owned, ok := (*carrier.value).(interface{ destroy() }); ok {
		owned.destroy()
	}
	*carrier.value = nil
}

func (codexOpaque) String() string   { return "[private Codex local state]" }
func (codexOpaque) GoString() string { return "[private Codex local state]" }
func (codexOpaque) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private Codex local state]")
}
func (codexOpaque) MarshalJSON() ([]byte, error) { return []byte(`"[private Codex local state]"`), nil }
func (codexOpaque) MarshalText() ([]byte, error) { return []byte("[private Codex local state]"), nil }
func (codexOpaque) LogValue() slog.Value         { return slog.StringValue("[private Codex local state]") }

type CodexSelectionState struct{ codexOpaque }
type CodexSelection struct{ codexOpaque }
type CodexPlan struct{ codexOpaque }
type CodexAdmission struct{ codexOpaque }
type CodexRecoveryPlan struct{ codexOpaque }
type CodexRestorePlan struct{ codexOpaque }

type CodexSafeChange struct {
	Role   codexconfig.Role
	Field  codexconfig.FieldID
	Action codexconfig.Action
	Count  uint32
}

type SafeCodexPlan struct {
	OperationID          string
	AssetID              string
	C0ProfileID          string
	AssetRevision        uint64
	SelectionRevision    uint64
	ConfirmationRevision uint64
	ExpiresAt            time.Time
	Changes              []CodexSafeChange
	RequiredSlots        []agentconfig.CodexSecretSlot
	RecoverableWindow    bool
	TightenPermissions   bool
}

type CodexProfileIdentity struct {
	ID             string
	ManifestSHA256 [32]byte
	ProfileDigest  [32]byte
	TemplateDigest [32]byte
}

type CodexResult struct {
	OperationID          string
	State                CodexLocalState
	NeedsAttention       bool
	SelectionRevision    uint64
	ConfirmationRevision uint64
}

type codexSelectionRecord struct {
	Origin       string
	Owner        string
	Profile      string
	Device       string
	Revision     uint64
	Confirmation uint64
	Asset        string
	Operation    string
}

type codexCurrent struct {
	engine *codexEngineState
	record codexSelectionRecord
	anchor nativeImage
}

func (selection CodexSelectionState) NextSelectionRevision() (uint64, error) {
	current := selection.get[codexCurrent]()
	if current == nil || current.record.Revision == math.MaxUint64 {
		return 0, ErrCodexInvalid
	}
	return current.record.Revision + 1, nil
}

func (selection CodexSelectionState) NextConfirmationRevision() (uint64, error) {
	current := selection.get[codexCurrent]()
	if current == nil || current.record.Confirmation == math.MaxUint64 {
		return 0, ErrCodexInvalid
	}
	return current.record.Confirmation + 1, nil
}

func (selection CodexSelectionState) DeviceID() string {
	if current := selection.get[codexCurrent](); current != nil {
		return current.record.Device
	}
	return ""
}

type codexSelectionData struct {
	prepared    *nativev1.PreparationBinding
	declaration agentconfig.CodexProviderDeclarationV1
	expires     time.Time
	current     codexCurrent
}

func (selection *codexSelectionData) destroy() {
	if selection.prepared != nil {
		proto.Reset(selection.prepared)
	}
	*selection = codexSelectionData{}
}

func NewCodexSelection(prepared *nativev1.PreparationBinding, declaration agentconfig.CodexProviderDeclarationV1, childExpires time.Time, current CodexSelectionState) (CodexSelection, error) {
	base := current.get[codexCurrent]()
	next, err := current.NextSelectionRevision()
	if err != nil || base == nil || prepared == nil || prepared.Target != nativev1.Target(2) || prepared.SchemaVersion != 1 || prepared.AgentId != "codex" || prepared.Codex == nil || len(prepared.OperationId) != 16 || prepared.SelectionRevision != next || prepared.OwnerId != base.record.Owner || prepared.ServerOrigin != base.record.Origin || prepared.ProfileId != base.record.Profile || prepared.AssetRevision <= 0 || !codexIdentifier(prepared.AssetId) || !slices.Equal(prepared.RequiredSlots, []nativev1.SecretSlot{nativev1.SecretSlot(3)}) || len(declaration.RequiredSlots) != 1 || declaration.RequiredSlots[0] != agentconfig.CodexSecretSlot("codex.provider-key") {
		return CodexSelection{}, ErrCodexInvalid
	}
	if declaration.APIVersion != 1 || declaration.Target != agentconfig.CodexConfig || declaration.ProviderID != agentconfig.CodexProviderID || declaration.WireAPI != agentconfig.CodexWireAPI || declaration.CredentialReference != agentconfig.CodexProviderKeySecret || declaration.LocalAuthPolicy != agentconfig.CodexPreserveLocalLoginV1 {
		return CodexSelection{}, ErrCodexInvalid
	}
	encoded, err := json.Marshal(declaration)
	if err != nil || len(encoded) > agentconfig.MaxCodexDeclarationBytes {
		clear(encoded)
		return CodexSelection{}, ErrCodexCapacity
	}
	var owned agentconfig.CodexProviderDeclarationV1
	err = json.Unmarshal(encoded, &owned)
	clear(encoded)
	if err != nil {
		return CodexSelection{}, ErrCodexInvalid
	}
	expires := minTime(childExpires, time.UnixMilli(prepared.PrepareExpiresAtUnixMs))
	if expires.IsZero() || !expires.After(time.Now()) {
		return CodexSelection{}, ErrCodexExpired
	}
	value := &codexSelectionData{prepared: proto.Clone(prepared).(*nativev1.PreparationBinding), declaration: owned, expires: expires, current: *base}
	return CodexSelection{value: new(any(value))}, nil
}

func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

func codexIdentifier(value string) bool {
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e || character == '/' || character == '\\' {
			return false
		}
	}
	return true
}

func codexOperationID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

type codexPlanData struct {
	engine    *codexEngineState
	selection *codexSelectionData
	view      SafeCodexPlan
	baseline  [3]nativeImage
	target    nativeIdentity
	provider  codexconfig.ProviderPlan
	profile   CodexProfileIdentity
}

func (plan CodexPlan) View() SafeCodexPlan {
	if data := plan.get[codexPlanData](); data != nil {
		view := data.view
		view.RequiredSlots = slices.Clone(view.RequiredSlots)
		view.Changes = slices.Clone(view.Changes)
		return view
	}
	return SafeCodexPlan{}
}

type codexAdmissionData struct {
	plan            *codexPlanData
	used            bool
	materialized    bool
	rendered        bool
	renderedImages  [3]codexconfig.BaselineBinding
	renderedActions [3]codexconfig.Action
}

func (data *codexAdmissionData) destroy() { data.used = true; data.plan = nil }

func (data *codexPlanData) destroy() { data.provider.Destroy(); *data = codexPlanData{} }

func (admission CodexAdmission) CopyPreparedBinding() *nativev1.PreparationBinding {
	if data := admission.get[codexAdmissionData](); data != nil && data.plan != nil && data.plan.selection != nil && !data.used {
		return proto.Clone(data.plan.selection.prepared).(*nativev1.PreparationBinding)
	}
	return nil
}

func (admission CodexAdmission) CopyProviderBinding() codexconfig.ProviderBinding {
	if data := admission.get[codexAdmissionData](); data != nil && data.plan != nil && !data.used {
		return data.plan.provider.Binding()
	}
	return codexconfig.ProviderBinding{}
}

func (admission CodexAdmission) CopyProfileIdentity() CodexProfileIdentity {
	if data := admission.get[codexAdmissionData](); data != nil && data.plan != nil && !data.used {
		return data.plan.profile
	}
	return CodexProfileIdentity{}
}

func (admission CodexAdmission) Revalidate(ctx context.Context) error {
	data := admission.get[codexAdmissionData]()
	if data == nil || data.used || data.plan == nil || data.plan.engine == nil {
		return ErrCodexInvalid
	}
	return data.plan.engine.withLock(ctx, func(ctx context.Context) error {
		if err := data.plan.engine.validatePlan(data.plan); err != nil {
			return err
		}
		operation, err := codexOperationFromPlan(data.plan)
		if err != nil {
			return err
		}
		actual := data.plan.engine.inventory.Operations[operation.ID]
		if actual == nil || actual.Binding != operation.Binding || actual.Base != operation.Base {
			return ErrCodexConflict
		}
		target, err := data.plan.engine.home.child(".codex", false)
		if err != nil {
			return err
		}
		defer target.close()
		return data.plan.engine.checkCodexReservations(target, actual)
	})
}
