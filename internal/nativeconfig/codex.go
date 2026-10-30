package nativeconfig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
)

type codexPrivate struct{}

func (codexPrivate) String() string   { return "[private Codex configuration]" }
func (codexPrivate) GoString() string { return "[private Codex configuration]" }
func (codexPrivate) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private Codex configuration]")
}
func (codexPrivate) MarshalJSON() ([]byte, error) {
	return []byte(`"[private Codex configuration]"`), nil
}
func (codexPrivate) MarshalText() ([]byte, error) {
	return []byte("[private Codex configuration]"), nil
}
func (codexPrivate) LogValue() slog.Value { return slog.StringValue("[private Codex configuration]") }

type CodexApplyRequest struct {
	codexPrivate
	ApplyRequest
	Executable, Directory string
}
type CodexChange struct {
	codexPrivate
	Role   codexconfig.Role
	Field  codexconfig.FieldID
	Action codexconfig.Action
	Count  uint32
}
type CodexPreview struct {
	codexPrivate
	OperationID, AssetID, C0ProfileID, NativeProfileID     string
	AssetRevision, SelectionRevision, ConfirmationRevision uint64
	ExpiresAt                                              time.Time
	Changes                                                []CodexChange
	RequiredSlots                                          []agentconfig.CodexSecretSlot
	AuthKept, RecoverableWindow, TightenPermissions        bool
}
type CodexApproval struct {
	codexPrivate
	Confirmed     bool
	ApprovedSlots []agentconfig.CodexSecretSlot
}
type CodexInteraction struct {
	codexPrivate
	Preview func(context.Context, CodexPreview) error
	Approve func(context.Context, CodexPreview, []agentconfig.CodexSecretSlot) (CodexApproval, error)
}
type CodexLocalResult struct {
	codexPrivate
	OperationID                             string
	State                                   configapply.CodexLocalState
	NeedsAttention                          bool
	SelectionRevision, ConfirmationRevision uint64
	RemoteOutcomeUnknown                    bool
}
type CodexResult struct {
	codexPrivate
	Preview       CodexPreview
	Local         CodexLocalResult
	RemoteState   configclient.NativeGrantStatus
	RemoteUnknown bool
}

func codexView(view configapply.SafeCodexPlan, profile string) CodexPreview {
	result := CodexPreview{OperationID: view.OperationID, AssetID: view.AssetID, C0ProfileID: view.C0ProfileID, NativeProfileID: profile, AssetRevision: view.AssetRevision, SelectionRevision: view.SelectionRevision, ConfirmationRevision: view.ConfirmationRevision, ExpiresAt: view.ExpiresAt, RequiredSlots: slices.Clone(view.RequiredSlots), AuthKept: true, RecoverableWindow: view.RecoverableWindow, TightenPermissions: view.TightenPermissions}
	for _, change := range view.Changes {
		result.Changes = append(result.Changes, CodexChange{Role: change.Role, Field: change.Field, Action: change.Action, Count: change.Count})
	}
	return result
}
func codexLocal(value configapply.CodexResult) CodexLocalResult {
	return CodexLocalResult{OperationID: value.OperationID, State: value.State, NeedsAttention: value.NeedsAttention, SelectionRevision: value.SelectionRevision, ConfirmationRevision: value.ConfirmationRevision, RemoteOutcomeUnknown: value.RemoteOutcomeUnknown}
}
func codexFailure(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{configclient.ErrNativeUnknown, configapply.ErrCodexUnknown, context.Canceled, context.DeadlineExceeded, ErrClosed, ErrBusy, ErrDenied, configapply.ErrCodexUnsupported, configapply.ErrCodexExpired, configapply.ErrCodexConflict, configapply.ErrCodexCapacity, configapply.ErrCodexAttention, configapply.ErrCodexClosed, configapply.ErrCodexInvalid, configclient.ErrNativeInvalid, configclient.ErrNativeDenied, configclient.ErrNativeUnavailable, configclient.ErrNativeCanceled, configclient.ErrNativeConflict} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ErrInvalid
}
func (client *Client) beginCodex(caller context.Context) (context.Context, func(), error) {
	if caller == nil {
		return nil, nil, ErrInvalid
	}
	deadline := time.Now().Add(2 * time.Minute)
	if parent, ok := caller.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	private, cancel := context.WithDeadline(context.Background(), deadline)
	joined := make(chan struct{})
	stop := context.AfterFunc(caller, func() { cancel(); close(joined) })
	if caller.Err() != nil {
		cancel()
	}
	release := func() {
		cancel()
		if !stop() {
			<-joined
		}
	}
	ctx, done, err := client.begin(private)
	if err != nil {
		release()
		return nil, nil, codexFailure(err)
	}
	return ctx, func() { done(); release() }, nil
}
func codexReview(ctx context.Context, ui CodexInteraction, view func() configapply.SafeCodexPlan, profile string, apply bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(view().ExpiresAt) {
		return configapply.ErrCodexExpired
	}
	if ui.Preview != nil && ui.Preview(ctx, codexView(view(), profile)) != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrDenied
	}
	if !time.Now().Before(view().ExpiresAt) {
		return configapply.ErrCodexExpired
	}
	if !apply {
		return ctx.Err()
	}
	if ui.Approve == nil {
		return ErrDenied
	}
	if !time.Now().Before(view().ExpiresAt) {
		return configapply.ErrCodexExpired
	}
	approval, err := ui.Approve(ctx, codexView(view(), profile), slices.Clone(view().RequiredSlots))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || !approval.Confirmed || !slices.Equal(approval.ApprovedSlots, view().RequiredSlots) {
		return ErrDenied
	}
	if !time.Now().Before(view().ExpiresAt) {
		return configapply.ErrCodexExpired
	}
	return nil
}
func (client *Client) ApplyCodex(caller context.Context, request CodexApplyRequest, ownerSession string, ui CodexInteraction) (result CodexResult, resultError error) {
	ctx, done, err := client.beginCodex(caller)
	if err != nil {
		return result, err
	}
	defer done()
	defer func() { resultError = codexFailure(resultError) }()
	profile, err := configapply.QualifiedCodexNativeProfile()
	if err != nil {
		return result, err
	}
	state := client.private()
	if state.native == nil || request.OwnerID == "" || request.AssetID == "" || request.ProfileID == "" || request.AssetRevision <= 0 || ownerSession == "" || request.Apply && ui.Approve == nil || !filepath.IsAbs(request.Executable) || filepath.Clean(request.Executable) != request.Executable || !filepath.IsAbs(request.Directory) || filepath.Clean(request.Directory) != request.Directory {
		return result, ErrInvalid
	}
	engine, err := configapply.OpenCodex(ctx, state.home, state.stateRoot, state.key)
	if err != nil {
		return result, err
	}
	defer func() { resultError = errors.Join(resultError, engine.Close()) }()
	current, err := engine.CurrentCodexSelection(ctx, state.origin, request.OwnerID, request.ProfileID)
	if err != nil {
		return result, err
	}
	defer current.Destroy()
	revision, err := current.NextSelectionRevision()
	if err != nil {
		return result, err
	}
	confirmation, err := current.NextConfirmationRevision()
	if err != nil {
		return result, err
	}
	flow, err := state.native.OpenConsumer(ctx, request.OwnerID, ownerSession)
	if err != nil {
		return result, err
	}
	defer flow.Close()
	prepared, err := flow.PrepareCodex(ctx, configclient.NativeSelection{AssetID: request.AssetID, AssetRevision: request.AssetRevision, ProfileID: request.ProfileID, SelectionRevision: revision})
	if err != nil {
		return result, err
	}
	defer prepared.Destroy()
	binding := prepared.CopyBinding()
	if binding == nil || binding.ServerOrigin != state.origin || binding.OwnerId != request.OwnerID || binding.AssetId != request.AssetID || binding.AssetRevision != request.AssetRevision || binding.ProfileId != request.ProfileID || binding.SelectionRevision != revision {
		return result, ErrInvalid
	}
	selection, err := configapply.NewCodexSelection(binding, prepared.CopyDeclaration(), prepared.SessionExpiresAt(), current)
	if err != nil {
		return result, err
	}
	defer selection.Destroy()
	observation, err := engine.ObserveCodex(ctx, selection, configapply.CodexLaunch{Executable: request.Executable, Directory: request.Directory, Arguments: []string{"app-server"}})
	if err != nil {
		return result, err
	}
	defer observation.Destroy()
	plan, err := engine.PreviewCodexObserved(ctx, selection, observation)
	if err != nil {
		return result, err
	}
	defer plan.Destroy()
	view := plan.View()
	result.Preview = codexView(view, profile.ID)
	if view.SelectionRevision != revision || view.ConfirmationRevision != confirmation || view.AssetID != request.AssetID || view.AssetRevision != uint64(request.AssetRevision) || view.C0ProfileID != request.ProfileID || !slices.Equal(view.RequiredSlots, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) {
		return result, ErrInvalid
	}
	if err := codexReview(ctx, ui, plan.View, profile.ID, request.Apply); err != nil {
		return result, err
	}
	if !request.Apply {
		return result, ctx.Err()
	}
	admission, err := engine.ConfirmCodexForConsumer(ctx, plan, plan.View().RequiredSlots, true)
	if err != nil {
		return result, err
	}
	defer admission.Destroy()
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	approval, err := configclient.NewCodexNativeApproval(constraints.ConfirmationRevision(), constraints.ApprovedDeadline(), true, true)
	if err != nil {
		return result, err
	}
	defer approval.Destroy()
	authorization, err := flow.AuthorizeCodexAdmitted(ctx, admission, approval)
	if err != nil {
		return codexRemoteFailure(ctx, flow, result, err)
	}
	defer authorization.Destroy()
	material, err := flow.ConsumeCodex(ctx)
	if err != nil {
		return codexRemoteFailure(ctx, flow, result, err)
	}
	defer material.Destroy()
	result.RemoteState = configclient.NativeConsumed
	data := material.CloneBytes()
	selected, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, data)
	clear(data)
	if err != nil {
		return result, err
	}
	defer selected.Destroy()
	candidate, err := engine.MaterializeCodex(ctx, admission, []codexconfig.SelectedSecret{selected})
	if err != nil {
		return result, err
	}
	defer candidate.Destroy()
	local, err := engine.ApplyCodex(ctx, admission, candidate)
	result.Local = codexLocal(local)
	return result, err
}
func codexRemoteFailure(ctx context.Context, flow *configclient.NativeFlow, result CodexResult, err error) (CodexResult, error) {
	if errors.Is(err, configclient.ErrNativeUnknown) {
		result.RemoteUnknown = true
		if ctx.Err() == nil {
			if status, statusErr := flow.StatusCodex(ctx); statusErr == nil {
				result.RemoteState = status.State
			}
		}
	}
	return result, err
}
func (client *Client) StatusCodex(caller context.Context, operationID string) (result CodexLocalResult, resultError error) {
	ctx, done, err := client.beginCodex(caller)
	if err != nil {
		return result, err
	}
	defer done()
	defer func() { resultError = codexFailure(resultError) }()
	state := client.private()
	engine, err := configapply.OpenCodex(ctx, state.home, state.stateRoot, state.key)
	if err != nil {
		return result, err
	}
	defer func() { resultError = errors.Join(resultError, engine.Close()) }()
	local, err := engine.StatusCodex(ctx, operationID)
	return codexLocal(local), err
}
func (client *Client) RecoverCodex(ctx context.Context, operationID string, apply bool, ui CodexInteraction) (CodexResult, error) {
	return client.codexLocalAction(ctx, operationID, apply, ui, 1)
}
func (client *Client) RestoreCodex(ctx context.Context, operationID string, apply bool, ui CodexInteraction) (CodexResult, error) {
	return client.codexLocalAction(ctx, operationID, apply, ui, 2)
}
func (client *Client) AbandonCodex(ctx context.Context, operationID string, apply bool, ui CodexInteraction) (CodexResult, error) {
	return client.codexLocalAction(ctx, operationID, apply, ui, 3)
}
func (client *Client) RetireCodexUnpublished(ctx context.Context, operationID string, apply bool, ui CodexInteraction) (CodexResult, error) {
	return client.codexLocalAction(ctx, operationID, apply, ui, 4)
}

func (client *Client) codexLocalAction(caller context.Context, operationID string, apply bool, ui CodexInteraction, action uint8) (result CodexResult, resultError error) {
	ctx, done, err := client.beginCodex(caller)
	if err != nil {
		return result, err
	}
	defer done()
	defer func() { resultError = codexFailure(resultError) }()
	if apply && ui.Approve == nil {
		return result, ErrInvalid
	}
	state := client.private()
	engine, err := configapply.OpenCodex(ctx, state.home, state.stateRoot, state.key)
	if err != nil {
		return result, err
	}
	defer func() { resultError = errors.Join(resultError, engine.Close()) }()
	var view func() configapply.SafeCodexPlan
	var execute func() (configapply.CodexResult, error)
	switch action {
	case 1:
		plan, err := engine.PreviewCodexRecovery(ctx, operationID)
		if err != nil {
			return result, err
		}
		defer plan.Destroy()
		view = plan.View
		execute = func() (configapply.CodexResult, error) {
			confirmed, err := plan.Confirm(ctx, true)
			if err != nil {
				return configapply.CodexResult{}, err
			}
			defer confirmed.Destroy()
			return engine.RecoverCodex(ctx, confirmed)
		}
	case 2:
		plan, err := engine.PreviewCodexRestore(ctx, operationID)
		if err != nil {
			return result, err
		}
		defer plan.Destroy()
		view = plan.View
		execute = func() (configapply.CodexResult, error) {
			confirmed, err := plan.Confirm(ctx, true)
			if err != nil {
				return configapply.CodexResult{}, err
			}
			defer confirmed.Destroy()
			return engine.RestoreCodex(ctx, confirmed)
		}
	case 3:
		plan, err := engine.PreviewCodexAbandon(ctx, operationID)
		if err != nil {
			return result, err
		}
		defer plan.Destroy()
		view = plan.View
		execute = func() (configapply.CodexResult, error) {
			confirmed, err := plan.Confirm(ctx, true)
			if err != nil {
				return configapply.CodexResult{}, err
			}
			defer confirmed.Destroy()
			return engine.AbandonCodex(ctx, confirmed)
		}
	case 4:
		plan, err := engine.PreviewCodexRetireUnpublished(ctx, operationID)
		if err != nil {
			return result, err
		}
		defer plan.Destroy()
		view = plan.View
		execute = func() (configapply.CodexResult, error) {
			confirmed, err := plan.Confirm(ctx, true)
			if err != nil {
				return configapply.CodexResult{}, err
			}
			defer confirmed.Destroy()
			return engine.RetireCodexUnpublished(ctx, confirmed)
		}
	default:
		return result, ErrInvalid
	}
	profile, _, _, _ := codexconfig.Native0166Manifest()
	result.Preview = codexView(view(), profile)
	if err := codexReview(ctx, ui, view, profile, apply); err != nil {
		return result, err
	}
	if !apply {
		return result, ctx.Err()
	}
	local, err := execute()
	result.Local = codexLocal(local)
	result.RemoteUnknown = local.RemoteOutcomeUnknown
	return result, err
}
