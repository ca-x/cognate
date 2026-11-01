package nativeconfig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/geminiconfig"
)

type geminiPrivate struct{}

func (geminiPrivate) String() string   { return "[private Gemini configuration]" }
func (geminiPrivate) GoString() string { return "[private Gemini configuration]" }
func (geminiPrivate) Format(writer fmt.State, _ rune) {
	_, _ = io.WriteString(writer, "[private Gemini configuration]")
}
func (geminiPrivate) MarshalJSON() ([]byte, error) {
	return []byte(`"[private Gemini configuration]"`), nil
}
func (geminiPrivate) MarshalText() ([]byte, error) {
	return []byte("[private Gemini configuration]"), nil
}
func (geminiPrivate) LogValue() slog.Value { return slog.StringValue("[private Gemini configuration]") }

type GeminiApplyRequest struct {
	geminiPrivate
	ApplyRequest
	Launch configapply.GeminiLaunch
}
type GeminiApproval struct {
	geminiPrivate
	PlanID        string
	Fields        []configapply.GeminiFieldApproval
	ApprovedSlots []agentconfig.GeminiSecretSlot
	Confirmed     bool
}
type GeminiInteraction struct {
	geminiPrivate
	Preview func(context.Context, configapply.SafeGeminiPlan) error
	Approve func(context.Context, configapply.SafeGeminiPlan) (GeminiApproval, error)
}
type GeminiResult struct {
	geminiPrivate
	Preview       configapply.SafeGeminiPlan
	Local         configapply.GeminiResult
	RemoteState   configclient.NativeGrantStatus
	RemoteUnknown bool
}
type GeminiLocalRequest struct {
	geminiPrivate
	OperationID, OwnerID, ProfileID, Action string
	Launch                                  configapply.GeminiLaunch
	Apply, ReadRemote                       bool
}

func geminiFailure(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{context.Canceled, context.DeadlineExceeded, ErrInvalid, ErrDenied, ErrClosed, ErrBusy, configapply.ErrGeminiUnsupported, configapply.ErrGeminiInvalid, configapply.ErrGeminiConflict, configapply.ErrGeminiExpired, configapply.ErrGeminiUnknown, configapply.ErrGeminiAttention, configapply.ErrGeminiCapacity, configapply.ErrGeminiClosed, configapply.ErrGeminiDenied, configclient.ErrNativeUnknown, configclient.ErrNativeInvalid, configclient.ErrNativeDenied, configclient.ErrNativeUnavailable, configclient.ErrNativeCanceled, configclient.ErrNativeConflict} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ErrInvalid
}
func geminiReview(ctx context.Context, ui GeminiInteraction, view configapply.SafeGeminiPlan, apply bool) (GeminiApproval, error) {
	if err := ctx.Err(); err != nil {
		return GeminiApproval{}, err
	}
	if !time.Now().Before(view.ExpiresAt) {
		return GeminiApproval{}, configapply.ErrGeminiExpired
	}
	if ui.Preview != nil && ui.Preview(ctx, view) != nil {
		if ctx.Err() != nil {
			return GeminiApproval{}, ctx.Err()
		}
		return GeminiApproval{}, ErrDenied
	}
	if !apply {
		return GeminiApproval{}, ctx.Err()
	}
	if ui.Approve == nil {
		return GeminiApproval{}, ErrDenied
	}
	approval, err := ui.Approve(ctx, view)
	if ctx.Err() != nil {
		return GeminiApproval{}, ctx.Err()
	}
	if !time.Now().Before(view.ExpiresAt) {
		return GeminiApproval{}, configapply.ErrGeminiExpired
	}
	if err != nil || !approval.Confirmed || approval.PlanID != view.PlanID {
		return GeminiApproval{}, ErrDenied
	}
	return approval, nil
}

func (client *Client) ApplyGemini(caller context.Context, request GeminiApplyRequest, ownerSession string, ui GeminiInteraction) (result GeminiResult, resultError error) {
	ctx, done, err := client.beginCodex(caller)
	if err != nil {
		return result, err
	}
	defer done()
	defer func() { resultError = geminiFailure(resultError) }()
	if _, err := configapply.QualifiedGeminiNativeProfile(); err != nil {
		return result, err
	}
	state := client.private()
	if state.native == nil || request.OwnerID == "" || request.AssetID == "" || request.ProfileID == "" || request.AssetRevision <= 0 || ownerSession == "" || request.Apply && ui.Approve == nil {
		return result, ErrInvalid
	}
	engine, err := configapply.OpenGemini(ctx, state.home, state.stateRoot, state.key)
	if err != nil {
		return result, err
	}
	defer func() { resultError = errors.Join(resultError, engine.Close()) }()
	deadline, _ := ctx.Deadline()
	observation, err := engine.ObserveGemini(ctx, request.Launch, deadline)
	if err != nil {
		return result, err
	}
	defer observation.Destroy()
	current, err := engine.CurrentGeminiSelection(ctx, state.origin, request.OwnerID, request.ProfileID)
	if err != nil {
		return result, err
	}
	defer current.Destroy()
	if err := engine.CheckGeminiSelection(ctx, current); err != nil {
		return result, err
	}
	if current.Revision() == math.MaxUint64 || current.ConfirmationRevision() == math.MaxUint64 {
		return result, configapply.ErrGeminiCapacity
	}
	flow, err := state.native.OpenConsumer(ctx, request.OwnerID, ownerSession)
	if err != nil {
		return result, err
	}
	defer flow.Close()
	prepared, err := flow.PrepareGemini(ctx, configclient.NativeSelection{AssetID: request.AssetID, AssetRevision: request.AssetRevision, ProfileID: request.ProfileID, SelectionRevision: current.Revision() + 1})
	if err != nil {
		return result, err
	}
	defer prepared.Destroy()
	selection, err := configapply.NewGeminiSelection(prepared.CopyBinding(), prepared.CopyDeclaration(), prepared.SessionExpiresAt(), current)
	if err != nil {
		return result, err
	}
	defer selection.Destroy()
	plan, err := engine.PreviewGeminiObserved(ctx, selection, observation)
	if err != nil {
		return result, err
	}
	defer plan.Destroy()
	result.Preview = plan.View()
	fields, err := geminiReview(ctx, ui, plan.View(), request.Apply)
	if err != nil || !request.Apply {
		return result, err
	}
	if len(fields.ApprovedSlots) != 0 {
		return result, ErrDenied
	}
	final, err := engine.ConfirmGemini(ctx, plan, fields.Fields, nil, false)
	if err != nil {
		return result, err
	}
	defer final.Destroy()
	result.Preview = final.View()
	approval, err := geminiReview(ctx, ui, final.View(), true)
	if err != nil {
		return result, err
	}
	confirmed, err := engine.ConfirmGemini(ctx, final, approval.Fields, approval.ApprovedSlots, approval.Confirmed)
	if err != nil {
		return result, err
	}
	result.Local = configapply.GeminiResult{OperationID: confirmed.View().OperationID, State: configapply.GeminiPreviewed}
	remoteFailure := func(err error) (GeminiResult, error) {
		if errors.Is(err, configclient.ErrNativeUnknown) {
			result.RemoteUnknown = true
			result.Local.RemoteOutcomeUnknown = true
			result.Local.State = configapply.GeminiRemoteOutcomeUnknown
			_ = engine.RecordGeminiRemoteUnknown(ctx, confirmed)
		}
		return result, err
	}
	if err := flow.AuthorizeGemini(ctx, engine, confirmed); err != nil {
		return remoteFailure(err)
	}
	var secrets []geminiconfig.SelectedSecret
	if len(final.View().RequiredSlots) == 1 {
		material, err := flow.ConsumeGemini(ctx, engine, confirmed)
		if err != nil {
			return remoteFailure(err)
		}
		defer material.Destroy()
		body := material.CloneBytes()
		secret, err := geminiconfig.NewSelectedSecret(agentconfig.GeminiAPIKeySecret, body)
		clear(body)
		if err != nil {
			return result, configapply.ErrGeminiUnsupported
		}
		defer secret.Destroy()
		secrets = []geminiconfig.SelectedSecret{secret}
		result.RemoteState = configclient.NativeConsumed
		result.Local.Consumed = true
	}
	if err := engine.MaterializeGemini(ctx, confirmed, secrets); err != nil {
		return result, err
	}
	result.Local, err = engine.ApplyGemini(ctx, confirmed)
	return result, err
}

func (client *Client) LocalGemini(caller context.Context, request GeminiLocalRequest, ui GeminiInteraction) (result GeminiResult, resultError error) {
	ctx, done, err := client.beginCodex(caller)
	if err != nil {
		return result, err
	}
	defer done()
	defer func() { resultError = geminiFailure(resultError) }()
	if _, err := configapply.QualifiedGeminiNativeProfile(); err != nil {
		return result, err
	}
	if request.OperationID == "" || request.OwnerID == "" || request.ProfileID == "" || request.Action != "status" && request.ReadRemote || request.Action == "status" && request.Apply {
		return result, ErrInvalid
	}
	state := client.private()
	engine, err := configapply.OpenGemini(ctx, state.home, state.stateRoot, state.key)
	if err != nil {
		return result, err
	}
	defer func() { resultError = errors.Join(resultError, engine.Close()) }()
	result.Local, err = engine.StatusGemini(ctx, request.OperationID, state.origin, request.OwnerID, request.ProfileID)
	if err != nil {
		return result, err
	}
	if request.Action == "status" {
		if request.ReadRemote {
			if state.native == nil {
				return result, ErrInvalid
			}
			ticket, err := engine.GeminiStatusTicket(ctx, request.OperationID, state.origin, request.OwnerID, request.ProfileID)
			if err != nil {
				return result, err
			}
			defer ticket.Destroy()
			status, err := state.native.StatusGeminiTicket(ctx, ticket, request.OwnerID)
			if err != nil {
				return result, err
			}
			result.RemoteState = status.State
			remote := uint32(0)
			switch status.State {
			case configclient.NativeNotIssued:
				remote = 1
			case configclient.NativeIssued:
				remote = 2
			case configclient.NativeConsumed:
				remote = 3
			case configclient.NativeExpired:
				remote = 4
			case configclient.NativeRetired:
				remote = 5
			default:
				return result, ErrInvalid
			}
			if err := engine.RecordGeminiStatus(ctx, request.OperationID, state.origin, request.OwnerID, request.ProfileID, remote); err != nil {
				return result, err
			}
			result.Local, err = engine.StatusGemini(ctx, request.OperationID, state.origin, request.OwnerID, request.ProfileID)
		}
		result.RemoteUnknown = result.Local.RemoteOutcomeUnknown
		return result, err
	}
	deadline, _ := ctx.Deadline()
	observed, err := engine.ObserveGemini(ctx, request.Launch, deadline)
	if err != nil {
		return result, err
	}
	defer observed.Destroy()
	plan, err := engine.PreviewGeminiRecovery(ctx, request.OperationID, state.origin, request.OwnerID, request.ProfileID, request.Action, observed)
	if err != nil {
		return result, err
	}
	defer plan.Destroy()
	result.Preview = plan.View()
	approval, err := geminiReview(ctx, ui, plan.View(), request.Apply)
	if err != nil || !request.Apply {
		return result, err
	}
	if len(approval.Fields) != 0 {
		return result, ErrDenied
	}
	if err := engine.ConfirmGeminiRecovery(ctx, plan, approval.PlanID, approval.ApprovedSlots, approval.Confirmed); err != nil {
		return result, err
	}
	result.Local, err = engine.ExecuteGeminiRecovery(ctx, plan)
	result.RemoteUnknown = result.Local.RemoteOutcomeUnknown
	return result, err
}
