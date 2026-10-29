package configapply

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type SafeResult struct {
	OperationID       string
	Status            string
	NeedsAttention    bool
	SelectionRevision uint64
	PolicyRevision    uint64
}

type SafePlan struct {
	OperationID             string
	Kind                    string
	Agent                   string
	Target                  agentconfig.Target
	AssetID                 string
	AssetRevision           uint64
	Slots                   []agentconfig.ClaudeSecretSlot
	Action                  string
	TightenPermissions      bool
	RecoverableAbsentWindow bool
	ExpiresAt               time.Time
	SourceOperation         string
	Readiness               string
	Operations              []SafeSlotAction
	AdapterID               string
	AdapterVersion          string
	AdapterDigest           string
}

type SafeSlotAction struct {
	Slot   agentconfig.ClaudeEnvSlot
	Action agentconfig.ClaudePatchAction
}

type Plan struct {
	private func() *nativePlanState
}

type nativePlanState struct {
	data       planData
	commitment [32]byte
	confirmed  bool
}

type planData struct {
	Selection        selectionData
	Kind             string
	SourceOperation  string
	SourceCommitment [32]byte
	Parent           nativeIdentity
	Base             nativeImage
	ExpiresAt        time.Time
	PreviewedAt      time.Time
}

type nativeIdentity struct {
	Device uint64
	Inode  uint64
	Mode   uint32
	Links  uint64
	Owner  uint32
	Size   int64
}

type nativeImage struct {
	Present  bool
	Identity nativeIdentity
	SHA256   [32]byte
}

func newNativePlan(data planData) *Plan {
	private := &nativePlanState{data: data}
	return &Plan{private: func() *nativePlanState { return private }}
}

func (plan *Plan) Confirm() *Plan {
	if plan == nil || plan.private == nil {
		return nil
	}
	private := *plan.private()
	private.data.Selection.SelectionInput = cloneSelection(private.data.Selection.SelectionInput)
	private.confirmed = true
	return &Plan{private: func() *nativePlanState { return &private }}
}

func (plan *Plan) Commitment() [32]byte {
	if plan == nil || plan.private == nil {
		return [32]byte{}
	}
	return plan.private().commitment
}
func (plan *Plan) OperationID() string {
	if plan == nil || plan.private == nil {
		return ""
	}
	return plan.private().data.Selection.OperationID
}
func (plan *Plan) ExpiresAt() time.Time {
	if plan == nil || plan.private == nil {
		return time.Time{}
	}
	return plan.private().data.ExpiresAt
}
func (plan *Plan) PreparationExpiresAt() time.Time {
	if plan == nil || plan.private == nil {
		return time.Time{}
	}
	return plan.private().data.Selection.PreparationExpiresAt
}
func (plan *Plan) PreparationPreparedAt() time.Time {
	if plan == nil || plan.private == nil {
		return time.Time{}
	}
	return plan.private().data.Selection.PreparationPreparedAt
}
func (plan *Plan) NativeSessionExpiresAt() (time.Time, bool) {
	if plan == nil || plan.private == nil || plan.private().data.Selection.NativeSessionExpiresAt == nil {
		return time.Time{}, false
	}
	return *plan.private().data.Selection.NativeSessionExpiresAt, true
}
func (plan *Plan) PreparationID() string {
	if plan == nil || plan.private == nil {
		return ""
	}
	return plan.private().data.Selection.PreparationID
}
func (plan *Plan) SelectionRevision() uint64 {
	if plan == nil || plan.private == nil {
		return 0
	}
	return plan.private().data.Selection.SelectionRevision
}
func (plan *Plan) ConfirmationRevision() uint64 {
	if plan == nil || plan.private == nil {
		return 0
	}
	return plan.private().data.Selection.ConfirmationRevision
}
func (plan *Plan) PolicyRevision() uint64 {
	if plan == nil || plan.private == nil {
		return 0
	}
	return plan.private().data.Selection.PolicyRevision
}
func (plan *Plan) PolicyID() string {
	if plan == nil || plan.private == nil {
		return ""
	}
	return plan.private().data.Selection.PolicyID
}
func (plan *Plan) RequiredSlots() []agentconfig.ClaudeSecretSlot {
	if plan == nil || plan.private == nil || plan.private().data.Kind == "restore" {
		return nil
	}
	var slots []agentconfig.ClaudeSecretSlot
	for _, operation := range plan.private().data.Selection.Patch.Operations {
		if operation.Reference != nil {
			slots = append(slots, *operation.Reference)
		}
	}
	return slices.Clone(slots)
}
func (plan *Plan) View() SafePlan {
	if plan == nil || plan.private == nil {
		return SafePlan{}
	}
	action := "replace"
	if !plan.private().data.Base.Present {
		action = "create"
	}
	if plan.private().data.Kind == "restore" {
		action = "restore"
	}
	view := SafePlan{OperationID: plan.OperationID(), Kind: plan.private().data.Kind, Agent: "claude", Target: agentconfig.ClaudeSettings, AssetID: plan.private().data.Selection.AssetID, AssetRevision: plan.private().data.Selection.AssetRevision, Slots: plan.RequiredSlots(), Action: action, TightenPermissions: plan.private().data.Base.Present && plan.private().data.Base.Identity.Mode&0o077 != 0, RecoverableAbsentWindow: true, ExpiresAt: plan.private().data.ExpiresAt, SourceOperation: plan.private().data.SourceOperation, Readiness: "no-new-credential"}
	if len(view.Slots) != 0 {
		view.Readiness = "requires-selected-credential"
	}
	view.AdapterID, view.AdapterVersion, view.AdapterDigest = plan.private().data.Selection.Adapter.Manifest.PluginID, plan.private().data.Selection.Adapter.Manifest.PluginVersion, plan.private().data.Selection.Adapter.DefinitionDigest
	if plan.private().data.Kind == "apply" {
		for _, operation := range plan.private().data.Selection.Patch.Operations {
			view.Operations = append(view.Operations, SafeSlotAction{Slot: operation.Slot, Action: operation.Action})
		}
	}
	return view
}
func (Plan) String() string   { return "[private configuration plan]" }
func (Plan) GoString() string { return "[private configuration plan]" }
func (Plan) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private configuration plan]")
}
func (Plan) LogValue() slog.Value         { return slog.StringValue("[private configuration plan]") }
func (Plan) MarshalJSON() ([]byte, error) { return []byte(`"[private configuration plan]"`), nil }

type nativeEncoding struct{ bytes.Buffer }

func (encoding *nativeEncoding) number(value uint64) {
	encoding.Write(binary.BigEndian.AppendUint64(nil, value))
}
func (encoding *nativeEncoding) blob(value []byte) {
	encoding.Write(binary.BigEndian.AppendUint32(nil, uint32(len(value))))
	encoding.Write(value)
}
func (encoding *nativeEncoding) text(value string) { encoding.blob([]byte(value)) }
func (encoding *nativeEncoding) present(value bool) {
	if value {
		encoding.WriteByte(1)
	} else {
		encoding.WriteByte(0)
	}
}
func (encoding *nativeEncoding) identity(identity nativeIdentity) {
	encoding.number(identity.Device)
	encoding.number(identity.Inode)
	encoding.number(uint64(identity.Mode))
	encoding.number(identity.Links)
	encoding.number(uint64(identity.Owner))
	encoding.number(uint64(identity.Size))
}
func (encoding *nativeEncoding) image(image nativeImage) {
	encoding.present(image.Present)
	if image.Present {
		encoding.identity(image.Identity)
		encoding.blob(image.SHA256[:])
	}
}
func (encoding *nativeEncoding) capabilities(values []agentconfig.Capability) {
	values = slices.Clone(values)
	slices.Sort(values)
	encoding.number(uint64(len(values)))
	for _, value := range values {
		encoding.text(string(value))
	}
}
func (encoding *nativeEncoding) selection(value selectionData, operation bool) {
	encoding.text(value.Origin)
	encoding.text(value.Owner)
	encoding.text(value.Profile)
	encoding.text(value.DeviceID)
	encoding.text(value.AssetID)
	encoding.number(uint64(value.SchemaVersion))
	encoding.number(value.AssetRevision)
	manifest := value.Adapter.Manifest
	encoding.number(uint64(manifest.APIVersion))
	encoding.text(manifest.PluginID)
	encoding.text(manifest.PluginVersion)
	encoding.text(manifest.AgentID)
	encoding.text(string(manifest.Target))
	encoding.text(string(manifest.Format))
	encoding.capabilities(manifest.Capabilities)
	encoding.text(value.Adapter.DefinitionDigest)
	encoding.capabilities(value.RequiredCapabilities)
	encoding.number(value.SelectionRevision)
	encoding.number(value.ConfirmationRevision)
	encoding.number(value.PolicyRevision)
	encoding.text(value.PolicyID)
	encoding.present(value.AutoApply)
	encoding.text(value.Scope)
	encoding.present(value.ConfigRootOverride)
	encoding.present(value.Selected)
	encoding.present(value.Pack != nil)
	if value.Pack != nil {
		encoding.text(value.Pack.ID)
		encoding.number(value.Pack.Revision)
		encoding.number(value.Pack.AttachmentRevision)
	}
	encoding.text(string(value.Patch.Target))
	encoding.number(uint64(len(value.Patch.Operations)))
	for _, patch := range value.Patch.Operations {
		encoding.text(string(patch.Slot))
		encoding.text(string(patch.Action))
		encoding.present(patch.Literal != nil)
		if patch.Literal != nil {
			encoding.text(*patch.Literal)
		}
		encoding.present(patch.Reference != nil)
		if patch.Reference != nil {
			encoding.text(string(*patch.Reference))
		}
	}
	if operation {
		encoding.text(value.OperationID)
		encoding.text(value.PreparationID)
		encoding.number(uint64(value.PreparationPreparedAt.UnixNano()))
		encoding.number(uint64(value.PreparationExpiresAt.UnixNano()))
		encoding.present(value.NativeSessionExpiresAt != nil)
		if value.NativeSessionExpiresAt != nil {
			encoding.number(uint64(value.NativeSessionExpiresAt.UnixNano()))
		}
	}
}

func nativePlanBytes(data planData) []byte {
	var encoding nativeEncoding
	encoding.text("cognate/native-configuration/intent/v1")
	encoding.selection(data.Selection, true)
	encoding.text(data.Kind)
	encoding.text(data.SourceOperation)
	encoding.blob(data.SourceCommitment[:])
	encoding.identity(data.Parent)
	encoding.image(data.Base)
	encoding.number(uint64(data.ExpiresAt.UnixNano()))
	encoding.number(uint64(data.PreviewedAt.UnixNano()))
	return encoding.Bytes()
}

func nativeMAC(key, value []byte) [32]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
