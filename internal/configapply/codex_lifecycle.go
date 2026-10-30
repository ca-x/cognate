package configapply

import (
	"context"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
)

type codexAuthorizationRecord struct {
	Operation      [16]byte
	Slot           agentconfig.CodexSecretSlot
	Revision       uint64
	DeadlineUnixMS int64
}

type CodexAuthorizationConstraints struct{ codexOpaque }
type CodexAbandonPlan struct{ codexOpaque }
type CodexRetirePlan struct{ codexOpaque }

func (constraints CodexAuthorizationConstraints) OperationID() [16]byte {
	if record := constraints.get[codexAuthorizationRecord](); record != nil {
		return record.Operation
	}
	return [16]byte{}
}
func (constraints CodexAuthorizationConstraints) RequiredSlots() []agentconfig.CodexSecretSlot {
	if record := constraints.get[codexAuthorizationRecord](); record != nil {
		return []agentconfig.CodexSecretSlot{record.Slot}
	}
	return nil
}
func (constraints CodexAuthorizationConstraints) ConfirmationRevision() uint64 {
	if record := constraints.get[codexAuthorizationRecord](); record != nil {
		return record.Revision
	}
	return 0
}
func (constraints CodexAuthorizationConstraints) ApprovedDeadline() time.Time {
	if record := constraints.get[codexAuthorizationRecord](); record != nil {
		return time.UnixMilli(record.DeadlineUnixMS)
	}
	return time.Time{}
}
func (admission CodexAdmission) CopyAuthorizationConstraints() CodexAuthorizationConstraints {
	if data := admission.get[codexAdmissionData](); data != nil && !data.used && data.plan != nil && data.lifecycle == 1 {
		return CodexAuthorizationConstraints{value: new(any(new(data.constraints)))}
	}
	return CodexAuthorizationConstraints{}
}
func (engine *CodexEngine) ConfirmCodexForConsumer(ctx context.Context, plan CodexPlan, slots []agentconfig.CodexSecretSlot, approved bool) (CodexAdmission, error) {
	return engine.confirmCodex(ctx, plan, slots, approved, 1)
}

func codexTerminal(phase CodexLocalState) bool {
	return phase == CodexCompleted || phase == CodexRestored || phase == CodexAbandonedBeforeWrite || phase == CodexRetiredUnpublished
}

func codexLifecycleShape(operation *codexOperation) error {
	if operation.ManagedModelsVersion > 1 || operation.ManagedModelsVersion == 1 && operation.LifecycleVersion != 1 && !operation.Restoring {
		return ErrCodexAttention
	}
	if operation.ManagedModelsVersion == 1 {
		ownership, err := codexconfig.NewOwnership(codexconfig.Absent, codexconfig.Managed, operation.ManagedModels)
		if err != nil {
			return ErrCodexAttention
		}
		ownership.Destroy()
	}
	if operation.LifecycleVersion == 0 {
		if operation.Authorization != nil {
			return ErrCodexAttention
		}
		return nil
	}
	if operation.LifecycleVersion != 1 || operation.Authorization == nil {
		return ErrCodexAttention
	}
	record := operation.Authorization
	if hex.EncodeToString(record.Operation[:]) != operation.ID || record.Slot != agentconfig.CodexProviderKeySecret || record.Revision == 0 || record.Revision != operation.Base.Confirmation+1 || record.DeadlineUnixMS <= 0 || record.DeadlineUnixMS > operation.Expires/1_000_000 {
		return ErrCodexAttention
	}
	return nil
}

func codexAdmissionRecordMatches(data *codexAdmissionData, operation *codexOperation) bool {
	if data.lifecycle != operation.LifecycleVersion {
		return false
	}
	if data.lifecycle == 0 {
		return operation.Authorization == nil
	}
	return operation.Authorization != nil && *operation.Authorization == data.constraints
}

func (admission CodexAdmission) advance(ctx context.Context, constraints CodexAuthorizationConstraints, next CodexLocalState) error {
	data := admission.get[codexAdmissionData]()
	copy := constraints.get[codexAuthorizationRecord]()
	if data == nil || data.used || data.plan == nil || data.lifecycle != 1 || copy == nil || *copy != data.constraints {
		return ErrCodexInvalid
	}
	state := data.plan.engine
	return state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validatePlan(ctx, data.plan); err != nil {
			return err
		}
		expected, err := codexOperationFromPlan(data.plan)
		if err != nil {
			return err
		}
		actual := state.inventory.Operations[data.plan.view.OperationID]
		if actual == nil || !codexAdmissionRecordMatches(data, actual) || actual.Binding != expected.Binding || actual.Base != expected.Base || codexTerminal(actual.State) {
			return ErrCodexConflict
		}
		wanted := CodexPreviewed
		if next == CodexConsumeAttempted {
			wanted = CodexAuthorizeAttempted
		}
		if actual.State != wanted {
			return ErrCodexConflict
		}
		if !state.now().Before(time.UnixMilli(copy.DeadlineUnixMS)) {
			return ErrCodexExpired
		}
		target, err := state.home.child(".codex", false)
		if err != nil {
			return err
		}
		defer target.close()
		if err := state.checkCodexReservations(target, actual); err != nil {
			return err
		}
		if err := state.validatePlanExpiry(data.plan); err != nil {
			return err
		}
		if !state.now().Before(time.UnixMilli(copy.DeadlineUnixMS)) {
			return ErrCodexExpired
		}
		actual.State = next
		if err := state.saveCodexInventory(); err != nil {
			return err
		}
		return state.checkpointContext(ctx, "consumer-phase-"+strconv.Itoa(int(next)))
	})
}

func (admission CodexAdmission) BeginAuthorize(ctx context.Context, constraints CodexAuthorizationConstraints) error {
	return admission.advance(ctx, constraints, CodexAuthorizeAttempted)
}
func (admission CodexAdmission) BeginConsume(ctx context.Context) error {
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	return admission.advance(ctx, constraints, CodexConsumeAttempted)
}
func (admission CodexAdmission) requireConsumed(ctx context.Context) error {
	data := admission.get[codexAdmissionData]()
	if data == nil || data.used || data.plan == nil || data.lifecycle != 1 {
		return ErrCodexInvalid
	}
	return data.plan.engine.withLock(ctx, func(ctx context.Context) error {
		actual := data.plan.engine.inventory.Operations[data.plan.view.OperationID]
		if actual == nil || !codexAdmissionRecordMatches(data, actual) || actual.State != CodexConsumeAttempted {
			return ErrCodexConflict
		}
		return nil
	})
}

type codexStopData struct {
	engine    *codexEngineState
	operation codexOperation
	expires   time.Time
	retire    bool
	confirmed bool
	used      bool
}

func (data *codexStopData) destroy() { *data = codexStopData{} }

func (state *codexEngineState) unpublishedCodex(ctx context.Context, operation *codexOperation, retire bool) (result *nativeDirectory, resultError error) {
	if operation == nil || operation.Candidate.Present || operation.Backup.Present || operation.Restoring || operation.Before != operation.After || operation.Write != [3]bool{} || operation.Stages != [3]nativeImage{} || operation.StageNames != [3]string{} || operation.State != CodexPreviewed && operation.State != CodexAuthorizeAttempted && operation.State != CodexAuthorized && operation.State != CodexConsumeAttempted && !codexTerminal(operation.State) {
		return nil, ErrCodexConflict
	}
	if !retire && (operation.LifecycleVersion != 1 || operation.State != CodexPreviewed && operation.State != CodexAuthorizeAttempted && operation.State != CodexAuthorized && operation.State != CodexAbandonedBeforeWrite) {
		return nil, ErrCodexConflict
	}
	if err := state.validateSelection(operation.Base); err != nil {
		return nil, err
	}
	if err := state.validateCodexOperationObservation(ctx, operation); err != nil {
		return nil, err
	}
	target, images, bodies, err := state.observeCodex()
	defer codexClearBodies(&bodies)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultError != nil {
			_ = target.close()
		}
	}()
	if !sameDirectory(target.identity, operation.Target) || images != operation.Before {
		return nil, ErrCodexConflict
	}
	for _, name := range []string{"codex-" + operation.ID + "-0.stage", "codex-" + operation.ID + "-1.stage", "codex-" + operation.ID + "-2.stage"} {
		image, body, err := target.read(name, codexRoleLimit)
		clear(body)
		if err != nil || image.Present {
			return nil, ErrCodexAttention
		}
	}
	return target, nil
}

func (engine *CodexEngine) previewCodexStop(ctx context.Context, id string, retire bool) (codexOpaque, error) {
	if !codexOperationID(id) {
		return codexOpaque{}, ErrCodexInvalid
	}
	state := engine.private()
	var result codexOpaque
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation := state.inventory.Operations[id]
		if operation == nil || codexTerminal(operation.State) {
			return ErrCodexConflict
		}
		target, err := state.unpublishedCodex(ctx, operation, retire)
		if err != nil {
			return err
		}
		if err := target.close(); err != nil {
			return ErrCodexUnknown
		}
		copy := *operation
		if operation.Authorization != nil {
			copy.Authorization = new(*operation.Authorization)
		}
		result = codexOpaque{value: new(any(&codexStopData{engine: state, operation: copy, expires: state.now().Add(time.Minute), retire: retire}))}
		return nil
	})
	return result, err
}

func (engine *CodexEngine) PreviewCodexAbandon(ctx context.Context, id string) (CodexAbandonPlan, error) {
	result, err := engine.previewCodexStop(ctx, id, false)
	return CodexAbandonPlan{codexOpaque: result}, err
}
func (engine *CodexEngine) PreviewCodexRetireUnpublished(ctx context.Context, id string) (CodexRetirePlan, error) {
	result, err := engine.previewCodexStop(ctx, id, true)
	return CodexRetirePlan{codexOpaque: result}, err
}

func (data *codexStopData) validate(ctx context.Context) (*codexOperation, *nativeDirectory, error) {
	if data.used || !data.engine.now().Before(data.expires) {
		return nil, nil, ErrCodexExpired
	}
	actual := data.engine.inventory.Operations[data.operation.ID]
	if actual == nil || actual.Binding != data.operation.Binding || actual.Base != data.operation.Base || actual.State != data.operation.State || actual.LifecycleVersion != data.operation.LifecycleVersion || (actual.Authorization == nil) != (data.operation.Authorization == nil) || actual.Authorization != nil && *actual.Authorization != *data.operation.Authorization {
		return nil, nil, ErrCodexConflict
	}
	target, err := data.engine.unpublishedCodex(ctx, actual, data.retire)
	return actual, target, err
}

func (data *codexStopData) confirm(ctx context.Context, approved bool) (codexOpaque, error) {
	if data == nil || data.engine == nil || !approved {
		return codexOpaque{}, ErrCodexInvalid
	}
	var result codexOpaque
	err := data.engine.withLock(ctx, func(ctx context.Context) error {
		_, target, err := data.validate(ctx)
		if err != nil {
			return err
		}
		if err := target.close(); err != nil {
			return ErrCodexUnknown
		}
		copy := *data
		copy.confirmed = true
		result = codexOpaque{value: new(any(&copy))}
		return nil
	})
	return result, err
}

func (plan CodexAbandonPlan) Confirm(ctx context.Context, approved bool) (CodexAbandonPlan, error) {
	result, err := plan.get[codexStopData]().confirm(ctx, approved)
	return CodexAbandonPlan{codexOpaque: result}, err
}
func (plan CodexRetirePlan) Confirm(ctx context.Context, approved bool) (CodexRetirePlan, error) {
	result, err := plan.get[codexStopData]().confirm(ctx, approved)
	return CodexRetirePlan{codexOpaque: result}, err
}

func codexStopView(carrier codexOpaque) SafeCodexPlan {
	if data := carrier.get[codexStopData](); data != nil {
		return SafeCodexPlan{OperationID: data.operation.ID, AssetID: data.operation.Asset, C0ProfileID: data.operation.Profile, SelectionRevision: data.operation.Base.Revision, ConfirmationRevision: data.operation.Base.Confirmation, ExpiresAt: data.expires}
	}
	return SafeCodexPlan{}
}
func (plan CodexAbandonPlan) View() SafeCodexPlan { return codexStopView(plan.codexOpaque) }
func (plan CodexRetirePlan) View() SafeCodexPlan  { return codexStopView(plan.codexOpaque) }

func (engine *CodexEngine) stopCodex(ctx context.Context, carrier codexOpaque, retire bool) (CodexResult, error) {
	data := carrier.get[codexStopData]()
	state := engine.private()
	if data == nil || data.engine != state || !data.confirmed || data.retire != retire {
		return CodexResult{}, ErrCodexInvalid
	}
	var result CodexResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		actual, target, err := data.validate(ctx)
		if err != nil {
			return err
		}
		defer target.close()
		data.used = true
		actual.State = CodexAbandonedBeforeWrite
		if retire {
			actual.State = CodexRetiredUnpublished
		}
		if err := state.saveCodexInventory(); err != nil {
			return err
		}
		result = codexOperationResult(actual)
		if err := state.checkpointContext(ctx, "consumer-tombstone-durable"); err != nil {
			return err
		}
		if !retire {
			if err := state.releaseCodexReservations(target, actual); err != nil {
				return err
			}
			return state.checkpointContext(ctx, "consumer-abandon-cleaned")
		}
		return nil
	})
	return result, err
}
func (engine *CodexEngine) AbandonCodex(ctx context.Context, plan CodexAbandonPlan) (CodexResult, error) {
	return engine.stopCodex(ctx, plan.codexOpaque, false)
}
func (engine *CodexEngine) RetireCodexUnpublished(ctx context.Context, plan CodexRetirePlan) (CodexResult, error) {
	return engine.stopCodex(ctx, plan.codexOpaque, true)
}
func (state *codexEngineState) cleanupCodexAbandoned(ctx context.Context, operation *codexOperation) error {
	if operation.LifecycleVersion != 1 || operation.State != CodexAbandonedBeforeWrite || operation.Candidate.Present || operation.Backup.Present || operation.Before != operation.After || operation.Write != [3]bool{} {
		return ErrCodexConflict
	}
	target, err := state.home.child(".codex", false)
	if err != nil {
		return err
	}
	defer target.close()
	if !sameDirectory(target.identity, operation.Target) {
		return ErrCodexConflict
	}
	return state.releaseCodexReservations(target, operation)
}
