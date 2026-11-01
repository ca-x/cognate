package configapply

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/geminiconfig"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var geminiFields = [...]geminiconfig.Field{geminiconfig.APIKeyField, geminiconfig.EndpointField, geminiconfig.TransportField, geminiconfig.EnvModelField, geminiconfig.SettingsModelField, geminiconfig.AuthModeField}

type GeminiSelection struct{ geminiOpaque }
type GeminiAuthorizationConstraints struct{ geminiOpaque }
type GeminiStatusTicket struct{ geminiOpaque }
type geminiTicketData struct{ body []byte }

func (value *geminiTicketData) destroy() { clear(value.body); value.body = nil }
func (value GeminiStatusTicket) CopyBytes() []byte {
	if data := value.get[geminiTicketData](); data != nil {
		return bytes.Clone(data.body)
	}
	return nil
}

type GeminiChange struct {
	Role      geminiconfig.Role
	Field     geminiconfig.Field
	Ownership geminiconfig.Ownership
	Action    geminiconfig.Action
	Cleanup   bool
}
type GeminiFieldApproval struct {
	Field           geminiconfig.Field
	Takeover, Clear bool
}
type SafeGeminiPlan struct {
	OperationID, PlanID, SourceOperation, Origin, OwnerID, ProfileID, AssetID, NativeProfileID string
	AssetRevision                                                                              int64
	SelectionRevision, ConfirmationRevision                                                    uint64
	ExpiresAt                                                                                  time.Time
	Changes                                                                                    []GeminiChange
	RequiredSlots                                                                              []agentconfig.GeminiSecretSlot
	FinalReview, LocalMaterial                                                                 bool
}
type geminiSelectionData struct {
	prepared    *nativev1.PreparationBinding
	declaration agentconfig.GeminiProviderDeclarationV1
	current     *geminiCurrent
	expires     time.Time
}
type geminiPlanData struct {
	engine                           *geminiEngineState
	selection                        *geminiSelectionData
	observed                         *geminiObserved
	id                               string
	facts                            [6]geminiconfig.FieldFact
	actions                          [6]geminiconfig.FieldAction
	approvals                        [6]GeminiFieldApproval
	projection                       *nativev1.GeminiLocalBindingV1
	commitment                       [32]byte
	codec                            geminiconfig.ProviderPlan
	fieldsConfirmed, confirmed, used bool
	view                             SafeGeminiPlan
}
type geminiConstraints struct {
	plan       *geminiPlanData
	projection *nativev1.GeminiLocalBindingV1
	prepared   *nativev1.PreparationBinding
	revision   uint64
	expires    time.Time
	operation  [16]byte
	commitment [32]byte
}

func (value GeminiAuthorizationConstraints) CopyProjection() *nativev1.GeminiLocalBindingV1 {
	if data := value.get[geminiConstraints](); data != nil {
		return proto.Clone(data.projection).(*nativev1.GeminiLocalBindingV1)
	}
	return nil
}
func (value GeminiAuthorizationConstraints) Commitment() [32]byte {
	if data := value.get[geminiConstraints](); data != nil {
		return data.commitment
	}
	return [32]byte{}
}
func (value GeminiAuthorizationConstraints) ConfirmationRevision() uint64 {
	if data := value.get[geminiConstraints](); data != nil {
		return data.revision
	}
	return 0
}
func (value GeminiAuthorizationConstraints) ExpiresAt() time.Time {
	if data := value.get[geminiConstraints](); data != nil {
		return data.expires
	}
	return time.Time{}
}
func (value GeminiAuthorizationConstraints) OperationID() [16]byte {
	if data := value.get[geminiConstraints](); data != nil {
		return data.operation
	}
	return [16]byte{}
}
func (value *geminiSelectionData) destroy() {
	if value.prepared != nil {
		proto.Reset(value.prepared)
	}
	*value = geminiSelectionData{}
}
func (value *geminiPlanData) destroy() {
	value.codec.Destroy()
	if value.projection != nil {
		proto.Reset(value.projection)
	}
	*value = geminiPlanData{}
}
func geminiCloneDeclaration(value agentconfig.GeminiProviderDeclarationV1) agentconfig.GeminiProviderDeclarationV1 {
	body, _ := json.Marshal(value)
	var result agentconfig.GeminiProviderDeclarationV1
	_ = json.Unmarshal(body, &result)
	return result
}
func geminiDomainHash(domain string, body []byte) [32]byte {
	encoded := binary.BigEndian.AppendUint32(nil, uint32(len(domain)))
	encoded = append(encoded, domain...)
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(body)))
	return sha256.Sum256(append(encoded, body...))
}
func geminiDeclarationHash(value agentconfig.GeminiProviderDeclarationV1) [32]byte {
	body, _ := json.Marshal(value)
	return geminiDomainHash("cognate-native-gemini/1/declaration", body)
}
func geminiWireIntent(preparation *nativev1.PreparationBinding, local *nativev1.GeminiLocalBindingV1, revision uint64, expiry int64) ([32]byte, error) {
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(preparation)
	if err != nil {
		return [32]byte{}, ErrGeminiInvalid
	}
	prepared := geminiDomainHash("cognate-native-gemini/1/preparation", body)
	body, err = (proto.MarshalOptions{Deterministic: true}).Marshal(local)
	if err != nil {
		return [32]byte{}, ErrGeminiInvalid
	}
	domain := "cognate-native-gemini/1/intent"
	encoded := binary.BigEndian.AppendUint32(nil, uint32(len(domain)))
	encoded = append(encoded, domain...)
	for _, part := range [][]byte{prepared[:], body} {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(part)))
		encoded = append(encoded, part...)
	}
	encoded = binary.BigEndian.AppendUint64(encoded, revision)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(expiry))
	return sha256.Sum256(encoded), nil
}
func geminiWireKnown(message proto.Message) bool {
	if message == nil || !message.ProtoReflect().IsValid() || len(message.ProtoReflect().GetUnknown()) != 0 || proto.Size(message) > 16<<10 {
		return false
	}
	valid := true
	message.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Message() != nil {
			if field.IsList() {
				for index := range value.List().Len() {
					if !geminiWireKnown(value.List().Get(index).Message().Interface()) {
						valid = false
						break
					}
				}
			} else {
				valid = geminiWireKnown(value.Message().Interface())
			}
		}
		return valid
	})
	return valid
}
func NewGeminiSelection(preparation *nativev1.PreparationBinding, declaration agentconfig.GeminiProviderDeclarationV1, sessionExpiry time.Time, current GeminiSelectionState) (GeminiSelection, error) {
	baseline := current.get[geminiCurrent]()
	if baseline == nil || !geminiWireKnown(preparation) || preparation.Target != 3 || preparation.Codex != nil || len(preparation.PatchSha256) != 0 || preparation.Gemini == nil || preparation.Gemini.ContractVersion != 1 || len(preparation.Gemini.DeclarationSha256) != 32 || [32]byte(preparation.Gemini.DeclarationSha256) != geminiDeclarationHash(declaration) || preparation.OwnerId != baseline.record.Owner || preparation.ServerOrigin != baseline.record.Origin || preparation.ProfileId != baseline.record.Profile || preparation.AgentId != "gemini" || preparation.AssetKind != 1 || preparation.SchemaVersion != 1 || preparation.AssetRevision <= 0 || len(preparation.OperationId) != 16 || len(preparation.NativeSessionBinding) != 32 || len(preparation.ConsumerKeySha256) != 32 || !codexIdentifier(preparation.PrepareId) || len(preparation.AssetId) != 26 || preparation.Scope != 1 || baseline.record.Revision == math.MaxUint64 || baseline.record.Confirmation == math.MaxUint64 || preparation.SelectionRevision != baseline.record.Revision+1 || preparation.Adapter == nil || preparation.Adapter.ApiVersion != 1 || preparation.Adapter.PluginId != "builtin.gemini" || preparation.Adapter.PluginVersion != "1.1.0" || preparation.Adapter.DefinitionDigest != "b0ba50fab171fea7889625da4ed9d36dc9b811303a166b66fb23bb1b232bd095" || !slices.Equal(preparation.Adapter.RequiredCapabilities, []string{"gemini.provider-plan.v1"}) || preparation.PreparedAtUnixMs <= 0 || preparation.PrepareExpiresAtUnixMs <= preparation.PreparedAtUnixMs || preparation.PrepareExpiresAtUnixMs-preparation.PreparedAtUnixMs > 300000 {
		return GeminiSelection{}, ErrGeminiInvalid
	}
	slots := []nativev1.SecretSlot{}
	if declaration.AuthMode == agentconfig.GeminiAPIKeyAuth {
		slots = []nativev1.SecretSlot{4}
	}
	if !slices.Equal(preparation.RequiredSlots, slots) {
		return GeminiSelection{}, ErrGeminiInvalid
	}
	expires := minTime(time.UnixMilli(preparation.PrepareExpiresAtUnixMs), sessionExpiry)
	return GeminiSelection{geminiOpaque{new(any(&geminiSelectionData{prepared: proto.Clone(preparation).(*nativev1.PreparationBinding), declaration: geminiCloneDeclaration(declaration), current: &geminiCurrent{engine: baseline.engine, record: baseline.record}, expires: expires}))}}, nil
}
func (plan GeminiPlan) View() SafeGeminiPlan {
	if data := plan.get[geminiPlanData](); data != nil {
		view := data.view
		view.Changes = slices.Clone(view.Changes)
		view.RequiredSlots = slices.Clone(view.RequiredSlots)
		return view
	}
	return SafeGeminiPlan{}
}
func (state *geminiEngineState) validateObservation(ctx context.Context, observed *geminiObserved, expected [2]nativeImage, allow *geminiOperation) error {
	if err := state.admitGeminiPublication(ctx, observed); err != nil {
		return err
	}
	actual, images, err := state.captureGemini(ctx, observed.descriptor.Launch)
	if err != nil {
		return err
	}
	if err := state.admitGeminiPublication(ctx, observed); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, observed.descriptor) {
		return ErrGeminiConflict
	}
	if allow == nil {
		if images != expected {
			return ErrGeminiConflict
		}
	} else if err := geminiValidateMix(allow, images); err != nil {
		return err
	}
	return nil
}
func (state *geminiEngineState) admitGeminiPublication(ctx context.Context, observed *geminiObserved) error {
	if err := state.check(ctx); err != nil {
		return err
	}
	if observed == nil || observed.engine != state || !state.now().Before(observed.expires) {
		return ErrGeminiExpired
	}
	return nil
}
func geminiDesiredField(declaration agentconfig.GeminiProviderDeclarationV1, index int) bool {
	switch index {
	case 0:
		return declaration.AuthMode == agentconfig.GeminiAPIKeyAuth
	case 1:
		return declaration.Endpoint != nil
	case 2:
		return declaration.KeyTransport != nil && *declaration.KeyTransport == agentconfig.GeminiBearer
	case 3:
		return declaration.EnvModel != nil
	case 4:
		return declaration.SettingsModel != nil
	case 5:
		return true
	}
	return false
}
func (engine *GeminiEngine) PreviewGeminiObserved(ctx context.Context, selection GeminiSelection, observation GeminiObservation) (GeminiPlan, error) {
	state, input, observed := engine.private(), selection.get[geminiSelectionData](), observation.get[geminiObserved]()
	if state == nil || input == nil || observed == nil || input.current.engine != state || observed.engine != state {
		return GeminiPlan{}, ErrGeminiInvalid
	}
	var result GeminiPlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validateSelection(input.current.record); err != nil {
			return err
		}
		if !state.now().Before(input.expires) {
			return ErrGeminiExpired
		}
		if err := state.validateObservation(ctx, observed, observed.baseline, nil); err != nil {
			return err
		}
		for _, pending := range state.inventory.Operations {
			if !geminiTerminal(pending.State) {
				return ErrGeminiConflict
			}
		}
		target, images, bodies, err := state.observePair()
		defer geminiClearBodies(&bodies)
		if err != nil {
			return err
		}
		defer target.close()
		bundle, err := geminiImages(images, bodies)
		if err != nil {
			return err
		}
		defer bundle.Destroy()
		present, err := geminiconfig.InspectNative0610Fields(ctx, bundle)
		if err != nil {
			return ErrGeminiUnsupported
		}
		data := &geminiPlanData{engine: state, selection: &geminiSelectionData{prepared: proto.Clone(input.prepared).(*nativev1.PreparationBinding), declaration: geminiCloneDeclaration(input.declaration), current: &geminiCurrent{engine: state, record: input.current.record}, expires: input.expires}, observed: &geminiObserved{engine: state, descriptor: observed.descriptor, baseline: observed.baseline, expires: minTime(observed.expires, input.expires)}, id: codexRandomID()}
		previous := state.inventory.Operations[input.current.record.Operation]
		for index, field := range geminiFields {
			owner := geminiconfig.Absent
			if present[index] {
				owner = geminiconfig.Foreign
				if previous != nil && geminiTerminal(previous.State) && previous.State != GeminiRetiredUnpublished && previous.State != GeminiAbandonedBeforeWrite && previous.After == images && previous.Managed[index] {
					owner = geminiconfig.Managed
				}
			}
			action, cleanup := geminiconfig.Keep, geminiconfig.Preserve
			if geminiDesiredField(input.declaration, index) {
				action = geminiconfig.Write
			} else if present[index] && (index == 2 || input.declaration.AuthMode == agentconfig.GeminiNativeLogin && (index == 0 || index == 1)) {
				action, cleanup = geminiconfig.Remove, geminiconfig.Clear
			}
			data.facts[index] = geminiconfig.FieldFact{Field: field, Ownership: owner, Cleanup: cleanup}
			data.actions[index] = geminiconfig.FieldAction{Field: field, Action: action}
		}
		data.view = geminiView(data)
		result = GeminiPlan{geminiOpaque{new(any(data))}}
		return nil
	})
	return result, err
}
func geminiImages(images [2]nativeImage, bodies [2][]byte) (geminiconfig.LocalImages, error) {
	var local [2]geminiconfig.LocalImage
	for index := range images {
		presence := geminiconfig.Missing
		if images[index].Present {
			presence = geminiconfig.Present
		}
		var err error
		local[index], err = geminiconfig.NewImage(presence, bodies[index])
		if err != nil {
			for _, image := range local {
				image.Destroy()
			}
			return geminiconfig.LocalImages{}, err
		}
	}
	defer local[0].Destroy()
	defer local[1].Destroy()
	return geminiconfig.NewImages(local[0], local[1])
}
func geminiView(data *geminiPlanData) SafeGeminiPlan {
	input := data.selection
	view := SafeGeminiPlan{OperationID: hex.EncodeToString(input.prepared.OperationId), PlanID: data.id, Origin: input.prepared.ServerOrigin, OwnerID: input.prepared.OwnerId, ProfileID: input.prepared.ProfileId, AssetID: input.prepared.AssetId, AssetRevision: input.prepared.AssetRevision, NativeProfileID: geminiconfig.Native0610ProfileID, SelectionRevision: input.current.record.Revision + 1, ConfirmationRevision: input.current.record.Confirmation + 1, ExpiresAt: data.observed.expires, FinalReview: data.fieldsConfirmed, RequiredSlots: slices.Clone(input.declaration.RequiredSlots)}
	for index, field := range geminiFields {
		role := geminiconfig.EnvRole
		if index >= 4 {
			role = geminiconfig.SettingsRole
		}
		view.Changes = append(view.Changes, GeminiChange{Role: role, Field: field, Ownership: data.facts[index].Ownership, Action: data.actions[index].Action, Cleanup: data.facts[index].Cleanup == geminiconfig.Clear})
	}
	return view
}
func (state *geminiEngineState) planMAC(domain string, value any) [32]byte {
	body, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, state.keys.anchor)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write(body)
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	clear(body)
	return digest
}
func (engine *GeminiEngine) ConfirmGemini(ctx context.Context, plan GeminiPlan, fields []GeminiFieldApproval, slots []agentconfig.GeminiSecretSlot, final bool) (GeminiPlan, error) {
	state, data := engine.private(), plan.get[geminiPlanData]()
	if data == nil || data.engine != state || data.used || len(fields) != 6 {
		return GeminiPlan{}, ErrGeminiInvalid
	}
	var result GeminiPlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validateSelection(data.selection.current.record); err != nil {
			return err
		}
		if err := state.validateObservation(ctx, data.observed, data.observed.baseline, nil); err != nil {
			return err
		}
		if data.confirmed {
			return ErrGeminiConflict
		}
		for index, field := range fields {
			if field.Field != geminiFields[index] || field.Takeover != (data.facts[index].Ownership == geminiconfig.Foreign) || field.Clear != (data.facts[index].Cleanup == geminiconfig.Clear) {
				return ErrGeminiDenied
			}
		}
		if !data.fieldsConfirmed {
			if final || len(slots) != 0 {
				return ErrGeminiDenied
			}
			clone := *data
			clone.id = codexRandomID()
			clone.approvals = [6]GeminiFieldApproval(fields)
			clone.fieldsConfirmed = true
			facts := clone.facts
			for index := range facts {
				if facts[index].Ownership == geminiconfig.Foreign {
					facts[index].Ownership = geminiconfig.ExplicitTakeover
				}
			}
			witness := state.planMAC("gemini-observation-v1", struct {
				PlanID     string
				Operation  []byte
				Descriptor geminiDescriptor
				Before     [2]nativeImage
				Base       geminiSelectionRecord
				Fields     [6]geminiconfig.FieldFact
				Expires    int64
			}{clone.id, clone.selection.prepared.OperationId, clone.observed.descriptor, clone.observed.baseline, clone.selection.current.record, facts, clone.observed.expires.UnixMilli()})
			resolution, err := geminiconfig.NewResolution(geminiconfig.Native0610ProfileID, geminiconfig.CompleteEffective, facts[:], witness)
			if err != nil {
				return ErrGeminiConflict
			}
			defer resolution.Destroy()
			target, images, bodies, err := state.observePair()
			defer geminiClearBodies(&bodies)
			if err != nil {
				return err
			}
			defer target.close()
			bundle, err := geminiImages(images, bodies)
			if err != nil {
				return err
			}
			defer bundle.Destroy()
			codec, err := geminiconfig.CheckProvider(ctx, bundle, clone.selection.declaration, resolution)
			if err != nil {
				return ErrGeminiUnsupported
			}
			clone.codec = codec
			identity := geminiNativeIdentity()
			local := &nativev1.GeminiLocalBindingV1{BindingVersion: 1, NativeProfileId: identity.ID, NativeProfileSha256: slices.Clone(identity.ProfileSHA256[:]), CodecProfileSha256: slices.Clone(identity.CodecSHA256[:]), EffectiveScope: 1, LocalAuthPolicy: "preserve-local-login-v1", ObservationWitness: slices.Clone(witness[:])}
			for index, image := range images {
				presence := nativev1.GeminiPresence(1)
				if image.Present {
					presence = 2
				}
				digest := state.planMAC("gemini-role-baseline-v1", struct {
					Plan  string
					Role  int
					Image nativeImage
				}{clone.id, index, image})
				local.Roles = append(local.Roles, &nativev1.GeminiRoleBaseline{Role: nativev1.GeminiRole(index + 1), Presence: presence, BaselineHmac: slices.Clone(digest[:])})
			}
			for index, fact := range facts {
				local.FieldDecisions = append(local.FieldDecisions, &nativev1.GeminiFieldDecision{Field: nativev1.GeminiField(index + 1), Ownership: nativev1.GeminiOwnership(fact.Ownership), Action: nativev1.GeminiAction(clone.actions[index].Action), Cleanup: nativev1.GeminiCleanup(fact.Cleanup)})
			}
			clone.projection = local
			clone.commitment, err = geminiWireIntent(clone.selection.prepared, local, clone.selection.current.record.Confirmation+1, clone.observed.expires.UnixMilli())
			if err != nil {
				codec.Destroy()
				return err
			}
			clone.view = geminiView(&clone)
			data.used = true
			result = GeminiPlan{geminiOpaque{new(any(&clone))}}
			return nil
		}
		if !final || !slices.Equal(slots, data.selection.declaration.RequiredSlots) || [6]GeminiFieldApproval(fields) != data.approvals {
			return ErrGeminiDenied
		}
		id := hex.EncodeToString(data.selection.prepared.OperationId)
		if state.inventory.Operations[id] != nil || len(state.inventory.Operations) >= 32 {
			return ErrGeminiConflict
		}
		for _, pending := range state.inventory.Operations {
			if !geminiTerminal(pending.State) {
				return ErrGeminiConflict
			}
		}
		operation := &geminiOperation{ID: id, PlanID: data.id, Origin: data.selection.prepared.ServerOrigin, Owner: data.selection.prepared.OwnerId, Profile: data.selection.prepared.ProfileId, Asset: data.selection.prepared.AssetId, AssetRevision: data.selection.prepared.AssetRevision, Binding: data.commitment, Target: data.observed.descriptor.Target, Before: data.observed.baseline, Base: data.selection.current.record, State: GeminiPreviewed, Expires: data.observed.expires.UnixMilli(), ProfileIdentity: geminiNativeIdentity(), Observation: data.observed.descriptor, Fields: data.facts, Actions: data.actions}
		state.inventory.Operations[id] = operation
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
		data.confirmed = true
		result = plan
		return nil
	})
	return result, err
}
func (state *geminiEngineState) planOperation(data *geminiPlanData) (*geminiOperation, error) {
	if data == nil || data.engine != state || !data.confirmed {
		return nil, ErrGeminiDenied
	}
	operation := state.inventory.Operations[hex.EncodeToString(data.selection.prepared.OperationId)]
	if operation == nil || operation.PlanID != data.id || operation.Binding != data.commitment {
		return nil, ErrGeminiConflict
	}
	return operation, nil
}
func (engine *GeminiEngine) AdmitGeminiAuthorization(ctx context.Context, plan GeminiPlan) (GeminiAuthorizationConstraints, error) {
	state, data := engine.private(), plan.get[geminiPlanData]()
	var result GeminiAuthorizationConstraints
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.State != GeminiPreviewed {
			return ErrGeminiConflict
		}
		if err := state.validateSelection(operation.Base); err != nil {
			return err
		}
		if err := state.validateObservation(ctx, data.observed, operation.Before, nil); err != nil {
			return err
		}
		operation.State = GeminiAuthorizeAttempted
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
		if err := state.checkpoint(ctx, "authorize-attempted"); err != nil {
			return err
		}
		result = GeminiAuthorizationConstraints{geminiOpaque{new(any(&geminiConstraints{plan: data, projection: proto.Clone(data.projection).(*nativev1.GeminiLocalBindingV1), prepared: proto.Clone(data.selection.prepared).(*nativev1.PreparationBinding), revision: operation.Base.Confirmation + 1, expires: data.observed.expires, operation: [16]byte(data.selection.prepared.OperationId), commitment: data.commitment}))}}
		return nil
	})
	return result, err
}
func (engine *GeminiEngine) RecordGeminiAuthorization(ctx context.Context, plan GeminiPlan, binding *nativev1.ApplyBinding) error {
	state, data := engine.private(), plan.get[geminiPlanData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.State != GeminiAuthorizeAttempted || !geminiApplyMatches(data, binding) {
			return ErrGeminiConflict
		}
		operation.Authorized = true
		operation.State = GeminiAuthorizedNoSecret
		if len(data.selection.declaration.RequiredSlots) == 1 {
			operation.State = GeminiAuthorizedGrant
		}
		return state.saveGeminiInventory(ctx)
	})
}
func geminiApplyMatches(data *geminiPlanData, binding *nativev1.ApplyBinding) bool {
	return data != nil && geminiWireKnown(binding) && binding.CodexLocalBinding == nil && proto.Equal(binding.Preparation, data.selection.prepared) && proto.Equal(binding.GeminiLocalBinding, data.projection) && bytes.Equal(binding.IntentCommitment, data.commitment[:]) && binding.IntentExpiresAtUnixMs == data.observed.expires.UnixMilli()
}
func (engine *GeminiEngine) AdmitGeminiConsume(ctx context.Context, plan GeminiPlan) error {
	state, data := engine.private(), plan.get[geminiPlanData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.State != GeminiAuthorizedGrant || operation.ConsumeStarted {
			return ErrGeminiConflict
		}
		if err := state.validateObservation(ctx, data.observed, operation.Before, nil); err != nil {
			return err
		}
		operation.ConsumeStarted = true
		operation.State = GeminiConsumeAttempted
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
		return state.checkpoint(ctx, "consume-attempted")
	})
}
func (engine *GeminiEngine) RecordGeminiConsumption(ctx context.Context, plan GeminiPlan, binding *nativev1.ApplyBinding) error {
	state, data := engine.private(), plan.get[geminiPlanData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.State != GeminiConsumeAttempted || !geminiApplyMatches(data, binding) {
			return ErrGeminiConflict
		}
		operation.Consumed = true
		return state.saveGeminiInventory(ctx)
	})
}
func (engine *GeminiEngine) RecordGeminiRemoteUnknown(ctx context.Context, plan GeminiPlan) error {
	state, data := engine.private(), plan.get[geminiPlanData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.State != GeminiAuthorizeAttempted && operation.State != GeminiConsumeAttempted {
			return ErrGeminiConflict
		}
		operation.RemoteUnknown = true
		operation.State = GeminiRemoteOutcomeUnknown
		return state.saveGeminiInventory(ctx)
	})
}

func (engine *GeminiEngine) BindGeminiStatusTicket(ctx context.Context, plan GeminiPlan, body []byte) error {
	if len(body) == 0 || len(body) > 8192 {
		return ErrGeminiInvalid
	}
	state, data := engine.private(), plan.get[geminiPlanData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.State != GeminiPreviewed || len(operation.StatusTicket) != 0 {
			return ErrGeminiConflict
		}
		operation.StatusTicket = bytes.Clone(body)
		return state.saveGeminiInventory(ctx)
	})
}
func (engine *GeminiEngine) GeminiStatusTicket(ctx context.Context, operationID, origin, owner, profile string) (GeminiStatusTicket, error) {
	state := engine.private()
	var ticket GeminiStatusTicket
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation := state.inventory.Operations[operationID]
		if operation == nil || operation.Origin != origin || operation.Owner != owner || operation.Profile != profile || len(operation.StatusTicket) == 0 {
			return ErrGeminiConflict
		}
		ticket = GeminiStatusTicket{geminiOpaque{new(any(&geminiTicketData{body: bytes.Clone(operation.StatusTicket)}))}}
		return nil
	})
	return ticket, err
}
func (engine *GeminiEngine) RecordGeminiStatus(ctx context.Context, operationID, origin, owner, profile string, remote uint32) error {
	state := engine.private()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation := state.inventory.Operations[operationID]
		if operation == nil || operation.Origin != origin || operation.Owner != owner || operation.Profile != profile || remote < 1 || remote > 5 {
			return ErrGeminiConflict
		}
		if remote == 3 {
			if !operation.ConsumeStarted {
				return ErrGeminiConflict
			}
			operation.Consumed = true
		}
		if remote != 1 {
			operation.RemoteUnknown = false
			if !operation.Candidate.Present && !geminiTerminal(operation.State) {
				operation.State = GeminiReconcileRequired
			}
		}
		return state.saveGeminiInventory(ctx)
	})
}

func (engine *GeminiEngine) MaterializeGemini(ctx context.Context, plan GeminiPlan, secrets []geminiconfig.SelectedSecret) error {
	state, data := engine.private(), plan.get[geminiPlanData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		if operation.Candidate.Present || operation.RemoteUnknown || !operation.Authorized || len(data.selection.declaration.RequiredSlots) == 1 && (!operation.Consumed || operation.State != GeminiConsumeAttempted) || len(data.selection.declaration.RequiredSlots) == 0 && operation.State != GeminiAuthorizedNoSecret {
			return ErrGeminiConflict
		}
		if err := state.validateSelection(operation.Base); err != nil {
			return err
		}
		if err := state.validateObservation(ctx, data.observed, operation.Before, nil); err != nil {
			return err
		}
		candidate, err := geminiconfig.RenderProvider(ctx, data.codec, secrets)
		if err != nil {
			return ErrGeminiUnsupported
		}
		defer candidate.Destroy()
		var bodies [2][]byte
		defer geminiClearBodies(&bodies)
		for index := range bodies {
			image := candidate.CopyImage(geminiconfig.Role(index + 1))
			bodies[index] = image.CopyBytes()
			operation.After[index] = operation.Before[index]
			operation.Write[index] = candidate.Action(geminiconfig.Role(index+1)) != geminiconfig.Keep
			if operation.Write[index] {
				operation.After[index] = nativeImage{Present: image.Presence() == geminiconfig.Present}
				if operation.After[index].Present {
					operation.After[index].SHA256 = sha256.Sum256(bodies[index])
				}
				operation.StageNames[index] = "gemini-" + operation.ID + "-" + strconv.Itoa(index) + ".stage"
				operation.FinalModes[index] = 0o600
			}
			image.Destroy()
		}
		afterBundle, err := geminiImages(operation.After, bodies)
		if err != nil {
			return err
		}
		present, err := geminiconfig.InspectNative0610Fields(ctx, afterBundle)
		afterBundle.Destroy()
		if err != nil {
			return ErrGeminiUnsupported
		}
		operation.Managed = present
		return state.prepareGeminiOperation(ctx, operation, bodies)
	})
}
func (state *geminiEngineState) prepareGeminiOperation(ctx context.Context, operation *geminiOperation, bodies [2][]byte) error {
	if err := state.check(ctx); err != nil {
		return err
	}
	target, images, originals, err := state.observePair()
	defer geminiClearBodies(&originals)
	if err != nil {
		return err
	}
	defer target.close()
	if !sameDirectory(target.identity, operation.Target) || images != operation.Before {
		return ErrGeminiConflict
	}
	if !state.now().Before(time.UnixMilli(operation.Expires)) {
		return ErrGeminiExpired
	}
	if err := state.saveGeminiInventory(ctx); err != nil {
		return err
	}
	operation.VaultReservation, err = state.vault.reserveAt(operation.ID+".reserve", 2*geminiArtifactLimit)
	if err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "vault-reserve-written"); err != nil {
		return err
	}
	if err := state.saveGeminiInventory(ctx); err != nil {
		return err
	}
	operation.TargetReservation, err = target.reserveAt("gemini-"+operation.ID+".reserve", 2*(256<<10))
	if err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "target-reserve-written"); err != nil {
		return err
	}
	if err := state.saveGeminiInventory(ctx); err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "operation-reserved"); err != nil {
		return err
	}
	operation.Backup, err = state.writeArtifact("backup", operation, originals)
	if err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "backup-written"); err != nil {
		return err
	}
	if err := state.saveGeminiInventory(ctx); err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "backup-durable"); err != nil {
		return err
	}
	operation.Candidate, err = state.writeArtifact("candidate", operation, bodies)
	if err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "candidate-written"); err != nil {
		return err
	}
	if err := state.saveGeminiInventory(ctx); err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "candidate-durable"); err != nil {
		return err
	}
	for index := range bodies {
		if !operation.Write[index] || !operation.After[index].Present {
			continue
		}
		stage, err := target.write(operation.StageNames[index], bodies[index])
		if err != nil {
			return err
		}
		if err := state.checkpoint(ctx, "stage-written-"+strconv.Itoa(index)); err != nil {
			return err
		}
		operation.Stages[index] = stage
		operation.After[index] = stage
		if operation.FinalModes[index] != 0 && operation.FinalModes[index] != 0o600 {
			operation.After[index].Identity.Mode = operation.After[index].Identity.Mode&^0o777 | operation.FinalModes[index]
		}
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
		if err := state.checkpoint(ctx, "stage-durable-"+strconv.Itoa(index)); err != nil {
			return err
		}
	}
	operation.State = GeminiCandidatesDurable
	if operation.Restoring {
		operation.State = GeminiRestorePrepared
	}
	if err := state.saveGeminiInventory(ctx); err != nil {
		return err
	}
	return state.checkpoint(ctx, "candidates-durable")
}
func geminiValidateMix(operation *geminiOperation, images [2]nativeImage) error {
	before, after, actual := [3]nativeImage{}, [3]nativeImage{}, [3]nativeImage{}
	writes := [3]bool{}
	for index := range images {
		before[index], after[index], actual[index], writes[index] = operation.Before[index], operation.After[index], images[index], operation.Write[index]
		if operation.Restoring && operation.After[index].Present && images[index] == operation.Stages[index] && operation.FinalModes[index] != 0o600 && (operation.State == GeminiRestoring || operation.State == GeminiPublishingSettings) {
			actual[index] = after[index]
		}
	}
	return bundleValidateMix(before, after, actual, writes)
}
func geminiOperationResult(operation *geminiOperation) GeminiResult {
	if operation == nil {
		return GeminiResult{State: GeminiNeedsAttention}
	}
	unknown := operation.RemoteUnknown || operation.State == GeminiAuthorizeAttempted || operation.ConsumeStarted && !operation.Consumed
	return GeminiResult{OperationID: operation.ID, State: operation.State, RemoteOutcomeUnknown: unknown, Consumed: operation.Consumed, Applied: operation.State == GeminiCompleted || operation.State == GeminiRestored}
}
func (engine *GeminiEngine) ApplyGemini(ctx context.Context, plan GeminiPlan) (GeminiResult, error) {
	state, data := engine.private(), plan.get[geminiPlanData]()
	var result GeminiResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.planOperation(data)
		if err != nil {
			return err
		}
		result = geminiOperationResult(operation)
		if operation.State != GeminiCandidatesDurable {
			return ErrGeminiConflict
		}
		result, err = state.publishGemini(ctx, operation, data.observed)
		return err
	})
	return result, err
}
func (state *geminiEngineState) publishGemini(ctx context.Context, operation *geminiOperation, observed *geminiObserved) (GeminiResult, error) {
	result := geminiOperationResult(operation)
	if err := state.validateSelection(operation.Base); err != nil {
		return result, err
	}
	if err := state.validateObservation(ctx, observed, operation.Before, operation); err != nil {
		return result, err
	}
	if !operation.Backup.Present || !operation.Candidate.Present {
		return result, ErrGeminiAttention
	}
	candidates, err := state.readArtifact("candidate", operation)
	geminiClearBodies(&candidates)
	if err != nil {
		return result, err
	}
	target, images, bodies, err := state.observePair()
	defer geminiClearBodies(&bodies)
	if err != nil {
		return result, err
	}
	defer target.close()
	if !sameDirectory(target.identity, operation.Target) {
		return result, ErrGeminiConflict
	}
	for index, image := range images {
		if !operation.Write[index] || image == operation.After[index] || operation.Restoring && image == operation.Stages[index] {
			continue
		}
		stage, body, err := target.read(operation.StageNames[index], 256<<10)
		clear(body)
		if err != nil || operation.After[index].Present && stage != operation.Stages[index] || !operation.After[index].Present && stage.Present {
			return result, ErrGeminiAttention
		}
	}
	for index := range images {
		if !operation.Write[index] || images[index] == operation.After[index] {
			continue
		}
		if err := state.check(ctx); err != nil {
			return geminiOperationResult(operation), err
		}
		if !state.now().Before(observed.expires) {
			return geminiOperationResult(operation), ErrGeminiExpired
		}
		operation.State = GeminiPublishingEnv
		if index == 1 {
			operation.State = GeminiPublishingSettings
		}
		if operation.Restoring {
			operation.State = GeminiRestoring
		}
		operation.PublicationStarted = true
		if err := state.saveGeminiInventory(ctx); err != nil {
			return geminiOperationResult(operation), err
		}
		if err := state.checkpoint(ctx, "before-publish-"+strconv.Itoa(index)); err != nil {
			return geminiOperationResult(operation), err
		}
		if err := state.validateObservation(ctx, observed, operation.Before, operation); err != nil {
			return geminiOperationResult(operation), err
		}
		if target.check() != nil || state.guard.check() != nil {
			return geminiOperationResult(operation), ErrGeminiAttention
		}
		if images[index] != operation.Stages[index] || !operation.Restoring {
			after := operation.After[index]
			if after.Present {
				after = operation.Stages[index]
			}
			if err := bundlePublishRoleAdmitted(target, bundleGemini, index, operation.StageNames[index], operation.Before[index], after, operation.Stages[index], func() error {
				if err := state.checkpoint(ctx, "publish-admission-"+strconv.Itoa(index)); err != nil {
					return err
				}
				return state.admitGeminiPublication(ctx, observed)
			}); err != nil {
				return geminiOperationResult(operation), err
			}
		}
		if err := state.checkpoint(ctx, "entry-published-"+strconv.Itoa(index)); err != nil {
			return geminiOperationResult(operation), err
		}
		if operation.After[index].Present && operation.FinalModes[index] != 0o600 {
			actual, err := geminiRestoreMode(target, index, operation.Stages[index], operation.FinalModes[index], func() error {
				if err := state.checkpoint(ctx, "restore-mode-admission"); err != nil {
					return err
				}
				return state.admitGeminiPublication(ctx, observed)
			})
			if err != nil {
				return geminiOperationResult(operation), err
			}
			if err != nil || actual != operation.After[index] {
				if err == nil {
					err = ErrGeminiUnknown
				}
				return geminiOperationResult(operation), err
			}
		}
		if operation.After[index].Present {
			if err := target.syncImage(geminiRoleNames[index], operation.After[index]); err != nil {
				return geminiOperationResult(operation), err
			}
		}
		if err := target.sync(); err != nil {
			return geminiOperationResult(operation), err
		}
		if err := state.checkpoint(ctx, "directory-synced-"+strconv.Itoa(index)); err != nil {
			return geminiOperationResult(operation), err
		}
		images[index] = operation.After[index]
		if err := state.validateObservation(ctx, observed, operation.Before, operation); err != nil {
			return geminiOperationResult(operation), err
		}
	}
	actual, err := bundleActualImages(target, bundleGemini)
	if err != nil || [2]nativeImage(actual[:2]) != operation.After {
		return geminiOperationResult(operation), ErrGeminiUnknown
	}
	if err := state.checkpoint(ctx, "targets-durable"); err != nil {
		return geminiOperationResult(operation), err
	}
	if err := state.releaseGeminiReservations(ctx, target, operation); err != nil {
		return geminiOperationResult(operation), err
	}
	if err := state.validateObservation(ctx, observed, operation.Before, operation); err != nil {
		return geminiOperationResult(operation), err
	}
	if err := state.checkpoint(ctx, "before-selection"); err != nil {
		return geminiOperationResult(operation), err
	}
	if err := state.validateGeminiPublishedStages(target, operation, [2]nativeImage(actual[:2])); err != nil {
		return geminiOperationResult(operation), err
	}
	if err := state.validateSelection(operation.Base); err != nil {
		return geminiOperationResult(operation), err
	}
	if err := state.admitGeminiPublication(ctx, observed); err != nil {
		return geminiOperationResult(operation), err
	}
	selection := operation.Base
	if selection.Revision == math.MaxUint64 || selection.Confirmation == math.MaxUint64 {
		return geminiOperationResult(operation), ErrGeminiCapacity
	}
	selection.Revision++
	selection.Confirmation++
	selection.Asset = operation.Asset
	selection.Operation = operation.ID
	previousState := operation.State
	key := geminiSelectionKey(selection.Origin, selection.Owner, selection.Profile)
	previousSelection, existed := state.inventory.Selections[key]
	operation.State = GeminiCompleted
	if operation.Restoring {
		operation.State = GeminiRestored
	}
	state.inventory.Selections[key] = selection
	if err := state.saveGeminiInventoryAdmitted(ctx, func() error {
		return state.admitGeminiPublication(ctx, observed)
	}); err != nil {
		operation.State = previousState
		if existed {
			state.inventory.Selections[key] = previousSelection
		} else {
			delete(state.inventory.Selections, key)
		}
		return geminiOperationResult(operation), err
	}
	if err := state.checkpoint(ctx, "completed-durable"); err != nil {
		return geminiOperationResult(operation), err
	}
	for index, name := range operation.StageNames {
		if !operation.Write[index] || name == "" {
			continue
		}
		image, body, err := target.read(name, 256<<10)
		clear(body)
		if err != nil {
			return geminiOperationResult(operation), err
		}
		if image.Present {
			if image != operation.Before[index] {
				return geminiOperationResult(operation), ErrGeminiAttention
			}
			if err := target.removeOwn(name, image); err != nil {
				return geminiOperationResult(operation), err
			}
		}
	}
	return geminiOperationResult(operation), nil
}
func (state *geminiEngineState) releaseGeminiReservations(ctx context.Context, target *nativeDirectory, operation *geminiOperation) error {
	if !operation.ReservationsReleased {
		operation.ReservationsReleased = true
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		directory *nativeDirectory
		name      string
		image     nativeImage
	}{{state.vault, operation.ID + ".reserve", operation.VaultReservation}, {target, "gemini-" + operation.ID + ".reserve", operation.TargetReservation}} {
		if !item.image.Present {
			continue
		}
		actual, body, err := item.directory.read(item.name, 2*geminiArtifactLimit)
		clear(body)
		if err != nil {
			return err
		}
		if actual.Present {
			if actual != item.image {
				return ErrGeminiAttention
			}
			if err := item.directory.removeOwn(item.name, actual); err != nil {
				return err
			}
		}
	}
	return nil
}
