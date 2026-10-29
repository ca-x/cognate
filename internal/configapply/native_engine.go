package configapply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/install"
)

type Engine struct {
	private func() *nativeEngineState
}

type nativeEngineState struct {
	mutex       sync.Mutex
	keys        nativeKeys
	home        *nativeDirectory
	state       *nativeDirectory
	vault       *nativeDirectory
	statePath   string
	closed      bool
	now         func() time.Time
	fault       func(string) error
	anchor      nativeRecord
	anchorImage nativeImage
}

func Open(ctx context.Context, home, stateRoot string, key []byte) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(key) != 32 || home == stateRoot || !filepath.IsAbs(home) || filepath.Clean(home) != home || !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot {
		return nil, ErrStateInvalid
	}
	if err := nativePlatform(); err != nil {
		return nil, err
	}
	if override := os.Getenv("CLAUDE_CONFIG_DIR"); override != "" && override != filepath.Join(home, ".claude") {
		return nil, ErrSelection
	}
	ownedKey := bytes.Clone(key)
	defer clear(ownedKey)
	keys, err := deriveNativeKeys(ownedKey)
	if err != nil {
		return nil, err
	}
	private := &nativeEngineState{keys: keys, statePath: stateRoot, now: time.Now}
	engine := &Engine{private: func() *nativeEngineState { return private }}
	failed := true
	defer func() {
		if failed {
			_ = engine.Close()
		}
	}()
	engine.private().home, err = nativeOpenDirectory(home, false)
	if err != nil {
		return nil, err
	}
	engine.private().state, err = nativeOpenDirectory(stateRoot, true)
	if err != nil {
		return nil, err
	}
	if err := engine.private().state.validateSessionFiles(); err != nil {
		return nil, err
	}
	session, err := install.OpenSession(ctx, stateRoot)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrStateInvalid
	}
	defer func() {
		if session != nil {
			_ = session.Close()
		}
	}()
	if engine.private().home.check() != nil || engine.private().state.check() != nil {
		return nil, ErrUnsafeTarget
	}
	pending, err := session.PendingOperations(ctx)
	if err != nil || len(pending) != 0 {
		return nil, ErrPending
	}
	if err := engine.openVault(); err != nil {
		return nil, err
	}
	inventory, err := engine.readInventory()
	if err != nil {
		return nil, err
	}
	for _, operation := range inventory.operations {
		if operation.Phase != "Planned" && operation.Phase != "NeedsAttention" {
			if _, err := engine.reconcileOperation(ctx, inventory, operation); err != nil {
				inventory.destroy()
				return nil, err
			}
		}
	}
	inventory.destroy()
	if err := session.Close(); err != nil {
		return nil, ErrOutcomeUnknown
	}
	session = nil
	failed = false
	return engine, nil
}

func (engine *Engine) Close() error {
	if engine == nil || engine.private == nil {
		return nil
	}
	engine.private().mutex.Lock()
	defer engine.private().mutex.Unlock()
	if engine.private().closed {
		return nil
	}
	engine.private().closed = true
	engine.private().keys.destroy()
	var result error
	for _, directory := range []*nativeDirectory{engine.private().vault, engine.private().state, engine.private().home} {
		if directory != nil {
			result = errors.Join(result, directory.close())
		}
	}
	if result != nil {
		return ErrOutcomeUnknown
	}
	return nil
}

func (engine *Engine) run(ctx context.Context, callback func(*nativeInventory) error) (returnedError error) {
	if engine == nil || engine.private == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !engine.private().mutex.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	defer engine.private().mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if engine.private().closed {
		return ErrClosed
	}
	if engine.private().home.check() != nil || engine.private().state.check() != nil || engine.private().vault.check() != nil {
		return ErrUnsafeTarget
	}
	if err := engine.private().state.validateSessionFiles(); err != nil {
		return err
	}
	session, err := install.OpenSession(ctx, engine.private().statePath)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPending
	}
	defer func() {
		if session.Close() != nil {
			returnedError = ErrOutcomeUnknown
		}
	}()
	if engine.private().state.check() != nil {
		return ErrUnsafeTarget
	}
	pending, err := session.PendingOperations(ctx)
	if err != nil || len(pending) != 0 {
		return ErrPending
	}
	inventory, err := engine.readInventory()
	if err != nil {
		return err
	}
	defer inventory.destroy()
	return callback(inventory)
}

func (engine *Engine) checkpoint(ctx context.Context, name string) error {
	if engine.private().fault != nil {
		if err := engine.private().fault(name); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func selectionSame(first, second selectionData) bool {
	var firstEncoding, secondEncoding nativeEncoding
	firstEncoding.selection(first, false)
	secondEncoding.selection(second, false)
	return bytes.Equal(firstEncoding.Bytes(), secondEncoding.Bytes())
}

func terminalPhase(phase string) bool {
	return phase == "AppliedOnDisk" || phase == "Restored" || phase == "Cancelled" || phase == "RolledBack"
}

func (engine *Engine) registerSelection(selection selectionData, inventory *nativeInventory) error {
	key := selectionKey(selection)
	previous, exists := inventory.selections[key]
	if exists && previous.SelectionRevision == selection.SelectionRevision {
		if !selectionSame(previous, selection) {
			return ErrConflict
		}
		return nil
	}
	if !exists && selection.SelectionRevision != 1 || exists && (selection.SelectionRevision != previous.SelectionRevision+1 || selection.ConfirmationRevision < previous.ConfirmationRevision || selection.PolicyRevision < previous.PolicyRevision) {
		return ErrConflict
	}
	if err := engine.appendRecord(nativeRecord{Type: "selection", ID: key, Selection: selection}, inventory); err != nil {
		return err
	}
	inventory.selections[key] = selection
	return nil
}

func (engine *Engine) Preview(ctx context.Context, selection Selection) (*Plan, error) {
	if selection.private == nil || !selection.private().Selected {
		return nil, ErrNotSelected
	}
	validated, err := NewSelection(selection.private().SelectionInput)
	if err != nil {
		return nil, err
	}
	var plan *Plan
	err = engine.run(ctx, func(inventory *nativeInventory) error {
		now := engine.private().now().UTC()
		if now.Before(validated.private().PreparationPreparedAt) || !now.Before(validated.private().PreparationExpiresAt) {
			return ErrPlanExpired
		}
		expires := now.Add(5 * time.Minute)
		if validated.private().PreparationExpiresAt.Before(expires) {
			expires = validated.private().PreparationExpiresAt
		}
		if sessionExpiry := validated.private().NativeSessionExpiresAt; sessionExpiry != nil && sessionExpiry.Before(expires) {
			expires = *sessionExpiry
		}
		if !now.Before(expires) {
			return ErrPlanExpired
		}
		for identifier, operation := range inventory.operations {
			if operation.Residue || !terminalPhase(operation.Phase) && operation.Phase != "Planned" {
				return ErrPending
			}
			if identifier == validated.private().OperationID {
				return ErrIdempotency
			}
		}
		parent, err := engine.private().home.child(".claude", false)
		if err != nil {
			return err
		}
		defer parent.close()
		base, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
		if err != nil {
			return err
		}
		defer clear(data)
		if !base.Present {
			data = []byte("{}")
		}
		if _, err := readSettings(data); err != nil {
			return err
		}
		if err := engine.registerSelection(*validated.private(), inventory); err != nil {
			return err
		}
		plan = newNativePlan(planData{Selection: *validated.private(), Kind: "apply", Parent: parent.identity, Base: base, ExpiresAt: expires})
		plan.private().data.PreviewedAt = now
		plan.private().commitment = nativeMAC(engine.private().keys.intent, nativePlanBytes(plan.private().data))
		operation := &operationRecord{Plan: plan.private().data, Commitment: plan.private().commitment, Phase: "Planned"}
		if err := engine.appendRecord(nativeRecord{Type: "operation", ID: plan.OperationID(), Selection: plan.private().data.Selection, Operation: operation}, inventory); err != nil {
			plan = nil
			return err
		}
		return nil
	})
	return plan, err
}

func (engine *Engine) validatePlan(plan *Plan, inventory *nativeInventory, kind string) (*operationRecord, error) {
	if plan == nil || plan.private == nil || !plan.private().confirmed {
		return nil, ErrUnconfirmed
	}
	if plan.private().data.Kind != kind || nativeMAC(engine.private().keys.intent, nativePlanBytes(plan.private().data)) != plan.private().commitment {
		return nil, ErrIdempotency
	}
	operation := inventory.operations[plan.OperationID()]
	if operation == nil || operation.Commitment != plan.private().commitment {
		return nil, ErrIdempotency
	}
	if terminalPhase(operation.Phase) {
		return operation, nil
	}
	if err := engine.checkPlanTime(plan.private().data); err != nil {
		return nil, err
	}
	current, exists := inventory.selections[selectionKey(plan.private().data.Selection)]
	if !exists || !selectionSame(current, plan.private().data.Selection) {
		return nil, ErrConflict
	}
	for identifier, other := range inventory.operations {
		if other.Residue || identifier != plan.OperationID() && !terminalPhase(other.Phase) && other.Phase != "Planned" {
			return nil, ErrPending
		}
	}
	if operation.Phase != "Planned" {
		return nil, ErrPending
	}
	return operation, nil
}

func (engine *Engine) checkPlanTime(plan planData) error {
	now := engine.private().now().UTC()
	if now.Before(plan.PreviewedAt) || now.Before(plan.Selection.PreparationPreparedAt) || !now.Before(plan.ExpiresAt) {
		return ErrPlanExpired
	}
	return nil
}

func (engine *Engine) Apply(ctx context.Context, plan *Plan, material map[agentconfig.ClaudeSecretSlot]SecretMaterial) (SafeResult, error) {
	result := SafeResult{}
	err := engine.run(ctx, func(inventory *nativeInventory) error {
		operation, err := engine.validatePlan(plan, inventory, "apply")
		if err != nil {
			return err
		}
		result.OperationID = plan.OperationID()
		if terminalPhase(operation.Phase) {
			result = operationResult(inventory, operation)
			return nil
		}
		if len(material) != len(plan.RequiredSlots()) {
			return ErrInvalidSecret
		}
		parent, err := engine.private().home.child(".claude", false)
		if err != nil {
			return err
		}
		defer parent.close()
		base, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
		defer clear(data)
		if err != nil || base != plan.private().data.Base || !sameDirectory(parent.identity, plan.private().data.Parent) {
			return ErrConflict
		}
		if !base.Present {
			data = []byte("{}")
		}
		image, err := RewriteClaudeSettings(data, plan.private().data.Selection.Patch, material)
		if err != nil {
			return err
		}
		defer image.Destroy()
		output := image.CopyBytes()
		defer clear(output)
		backupData := data
		if !base.Present {
			backupData = nil
		}
		result, err = engine.publish(ctx, inventory, operation, parent, backupData, output, false)
		return err
	})
	if errors.Is(err, ErrOutcomeUnknown) {
		result.Status = "OutcomeUnknown"
	}
	return result, err
}

func (engine *Engine) publish(ctx context.Context, inventory *nativeInventory, operation *operationRecord, parent *nativeDirectory, base, output []byte, remove bool) (result SafeResult, returnedError error) {
	identifier := operation.Plan.Selection.OperationID
	result = SafeResult{OperationID: identifier}
	reserved := false
	defer func() {
		if returnedError == nil {
			return
		}
		if operation.Transaction != "" {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			latest, err := engine.readInventory()
			if err == nil {
				defer latest.destroy()
				if pending := latest.operations[identifier]; pending != nil {
					_, _ = engine.reconcileOperation(recovery, latest, pending)
				}
			}
		}
		if reserved {
			returnedError = ErrOutcomeUnknown
			result.Status = "OutcomeUnknown"
		}
	}()
	if err := engine.checkpoint(ctx, "before-stage"); err != nil {
		return result, err
	}
	transaction := "cognate-" + uuid.New().String()
	operation.Transaction = transaction
	operation.VaultCapacityName = "capacity-" + uuid.New().String()
	if err := engine.saveOperation(operation, inventory, "Staging"); err != nil {
		return result, err
	}
	stage, err := parent.child(transaction, true)
	if err != nil {
		return result, err
	}
	defer stage.close()
	if err := engine.checkpoint(ctx, "transaction-created"); err != nil {
		return result, err
	}
	operation.Transaction, operation.TransactionIdentity, operation.RestoreAbsent = transaction, stage.identity, remove
	if err := engine.saveOperation(operation, inventory, "Staging"); err != nil {
		return result, err
	}
	if !remove {
		operation.Stage, err = stage.write("stage", output)
		if err != nil {
			return result, err
		}
	}
	if err := engine.checkpoint(ctx, "stage-file-synced"); err != nil {
		return result, err
	}
	if err := engine.saveOperation(operation, inventory, "Staging"); err != nil {
		return result, err
	}
	if err := engine.checkpoint(ctx, "stage-synced"); err != nil {
		return result, err
	}
	operation.Capacity, err = stage.reserve(2 << 20)
	if err != nil {
		return result, err
	}
	if err := engine.checkpoint(ctx, "capacity-file-synced"); err != nil {
		return result, err
	}
	if err := engine.saveOperation(operation, inventory, "Staging"); err != nil {
		return result, err
	}
	operation.VaultCapacity, err = engine.private().vault.reserveAt(operation.VaultCapacityName, 2<<20)
	if err != nil {
		return result, err
	}
	if err := engine.checkpoint(ctx, "vault-capacity-file-synced"); err != nil {
		return result, err
	}
	if err := engine.saveOperation(operation, inventory, "Staging"); err != nil {
		return result, err
	}
	if err := engine.checkpoint(ctx, "before-backup"); err != nil {
		return result, err
	}
	backupID := uuid.New().String()
	backup := &nativeRecord{Type: "backup", ID: backupID, Selection: operation.Plan.Selection, Backup: &backupRecord{Image: operation.Plan.Base, Data: bytes.Clone(base)}}
	defer clearBackup(*backup)
	if err := engine.appendRecord(*backup, inventory); err != nil {
		return result, err
	}
	operation.BackupID = backupID
	if err := engine.saveOperation(operation, inventory, "Staging"); err != nil {
		return result, err
	}
	if err := engine.checkpoint(ctx, "backup-durable"); err != nil {
		return result, err
	}
	operation.MaterializedCommitment = nativeMaterializedMAC(engine.private().keys.intent, operation)
	if err := engine.saveOperation(operation, inventory, "Prepared"); err != nil {
		return result, err
	}
	if err := engine.checkpoint(ctx, "prepared-durable"); err != nil {
		return result, err
	}
	if err := engine.verifyPublicationState(operation); err != nil {
		return result, err
	}
	actual, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
	clear(data)
	if err != nil || actual != operation.Plan.Base || !sameDirectory(parent.identity, operation.Plan.Parent) {
		return result, ErrConflict
	}
	if err := engine.checkPlanTime(operation.Plan); err != nil {
		return result, err
	}
	if operation.Plan.Base.Present {
		reserved = true
		if err := engine.saveOperation(operation, inventory, "ReserveIntent"); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := engine.checkpoint(ctx, "reserve-intent-durable"); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := engine.verifyPublicationState(operation); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := engine.checkPlanTime(operation.Plan); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := parent.rename("settings.json", stage, "holding"); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := engine.checkpoint(ctx, "entry-captured"); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := parent.syncWith(stage); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := engine.checkpoint(ctx, "capture-synced"); err != nil {
			return result, ErrOutcomeUnknown
		}
		captured, data, err := stage.read("holding", agentconfig.MaxConfigBytes)
		if err != nil || captured != operation.Plan.Base {
			if err == nil && captured.Present {
				conflictID := uuid.New().String()
				_ = stage.removeOwn("capacity", operation.Capacity)
				_ = engine.private().vault.removeOwn(operation.VaultCapacityName, operation.VaultCapacity)
				_ = engine.appendRecord(nativeRecord{Type: "backup", ID: conflictID, Selection: operation.Plan.Selection, Backup: &backupRecord{Image: captured, Data: data}}, inventory)
			}
			clear(data)
			_ = stage.rename("holding", parent, "settings.json")
			_ = parent.syncWith(stage)
			_ = engine.saveOperation(operation, inventory, "NeedsAttention")
			return result, ErrOutcomeUnknown
		}
		clear(data)
	}
	reserved = true
	if err := engine.saveOperation(operation, inventory, "PublishIntent"); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := engine.checkpoint(ctx, "publish-intent-durable"); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := engine.verifyPublicationState(operation); err != nil {
		return result, ErrOutcomeUnknown
	}
	if operation.Plan.Base.Present {
		captured, data, err := stage.read("holding", agentconfig.MaxConfigBytes)
		clear(data)
		if err != nil || captured != operation.Plan.Base {
			return result, ErrOutcomeUnknown
		}
	}
	if !remove {
		current, data, err := stage.read("stage", agentconfig.MaxConfigBytes)
		clear(data)
		if err != nil || current != operation.Stage {
			return result, ErrOutcomeUnknown
		}
		if err := engine.checkPlanTime(operation.Plan); err != nil {
			return result, ErrOutcomeUnknown
		}
		if err := stage.rename("stage", parent, "settings.json"); err != nil {
			return result, ErrOutcomeUnknown
		}
	} else if err := engine.checkPlanTime(operation.Plan); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := engine.checkpoint(ctx, "entry-published"); err != nil {
		return result, ErrOutcomeUnknown
	}
	if !remove {
		if err := parent.syncImage("settings.json", operation.Stage); err != nil {
			return result, ErrOutcomeUnknown
		}
	}
	if err := engine.checkpoint(ctx, "target-file-synced"); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := parent.syncWith(stage); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := engine.checkpoint(ctx, "publication-synced"); err != nil {
		return result, ErrOutcomeUnknown
	}
	current, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
	clear(data)
	if err != nil || remove && current.Present || !remove && current != operation.Stage {
		return result, ErrOutcomeUnknown
	}
	phase := "AppliedOnDisk"
	if operation.Plan.Kind == "restore" {
		phase = "Restored"
	}
	if err := engine.saveOperation(operation, inventory, phase); err != nil {
		return result, ErrOutcomeUnknown
	}
	if phase == "Restored" {
		if err := engine.pauseAfterRestore(inventory, operation); err != nil {
			return result, ErrOutcomeUnknown
		}
	}
	if err := engine.checkpoint(ctx, "result-durable"); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := engine.cleanup(operation, parent, stage); err != nil {
		return result, ErrOutcomeUnknown
	}
	if err := ctx.Err(); err != nil {
		return result, ErrOutcomeUnknown
	}
	result.Status = phase
	selection := inventory.selections[selectionKey(operation.Plan.Selection)]
	result.SelectionRevision, result.PolicyRevision = selection.SelectionRevision, selection.PolicyRevision
	return result, nil
}

func (engine *Engine) verifyPublicationState(operation *operationRecord) error {
	inventory, err := engine.readInventory()
	if err != nil {
		return err
	}
	defer inventory.destroy()
	current := inventory.operations[operation.Plan.Selection.OperationID]
	if current == nil || current.Sequence != operation.Sequence || current.Commitment != operation.Commitment || current.MaterializedCommitment != operation.MaterializedCommitment || current.Phase != operation.Phase {
		return ErrStateInvalid
	}
	selection, exists := inventory.selections[selectionKey(operation.Plan.Selection)]
	if !exists || !selectionSame(selection, operation.Plan.Selection) {
		return ErrConflict
	}
	if _, err := engine.operationBackup(inventory, current); err != nil {
		return err
	}
	capacity, data, err := engine.private().vault.read(operation.VaultCapacityName, 4<<20)
	clear(data)
	if err != nil || !capacity.Present || capacity != operation.VaultCapacity {
		return ErrCapacity
	}
	return nil
}

func (engine *Engine) cleanup(operation *operationRecord, parent, transaction *nativeDirectory) error {
	if operation.VaultCapacityName != "" {
		actual, data, err := engine.private().vault.read(operation.VaultCapacityName, 4<<20)
		clear(data)
		if err != nil {
			return err
		}
		if actual.Present {
			if actual != operation.VaultCapacity {
				return ErrConflict
			}
			if err := engine.private().vault.removeOwn(operation.VaultCapacityName, actual); err != nil {
				return err
			}
		}
	}
	for _, item := range []struct {
		name  string
		image nativeImage
	}{{"holding", operation.Plan.Base}, {"stage", operation.Stage}, {"capacity", operation.Capacity}} {
		actual, data, err := transaction.read(item.name, 4<<20)
		clear(data)
		if err != nil {
			return err
		}
		if actual.Present {
			if actual != item.image {
				return ErrConflict
			}
			if err := transaction.removeOwn(item.name, actual); err != nil {
				return err
			}
		}
	}
	if err := engine.checkpoint(context.Background(), "cleanup-synced"); err != nil {
		return err
	}
	return parent.removeDirectory(operation.Transaction, operation.TransactionIdentity)
}

func (Engine) String() string { return "[private configuration engine]" }
func (Engine) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private configuration engine]")
}
func (Engine) LogValue() slog.Value         { return slog.StringValue("[private configuration engine]") }
func (Engine) MarshalJSON() ([]byte, error) { return []byte(`"[private configuration engine]"`), nil }
