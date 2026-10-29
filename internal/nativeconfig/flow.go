package nativeconfig

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
)

var (
	ErrInvalid = errors.New("invalid native configuration request")
	ErrDenied  = errors.New("native configuration not approved")
	ErrClosed  = errors.New("native configuration client closed")
	ErrBusy    = errors.New("native configuration client busy")
)

type ApplyRequest struct {
	OwnerID, AssetID, ProfileID string
	AssetRevision               int64
	Apply                       bool
}
type Approval struct {
	Confirmed     bool
	ApprovedSlots []agentconfig.ClaudeSecretSlot
}
type Interaction struct {
	Preview func(context.Context, configapply.SafePlan) error
	Approve func(context.Context, configapply.SafePlan, []agentconfig.ClaudeSecretSlot) (Approval, error)
}
type Result struct {
	Preview       configapply.SafePlan
	Local         configapply.SafeResult
	RemoteState   configclient.NativeGrantStatus
	RemoteUnknown bool
}
type Client struct{ private func() *clientState }
type clientState struct {
	mutex                   sync.Mutex
	workers                 sync.WaitGroup
	life                    context.Context
	cancel                  context.CancelFunc
	closed, busy            bool
	home, stateRoot, origin string
	key                     []byte
	native                  *configclient.NativeClient
}

func New(home, stateRoot, origin string, existingKey []byte, native *configclient.NativeClient) (*Client, error) {
	if len(existingKey) != 32 || !filepath.IsAbs(home) || filepath.Clean(home) != home || !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot || home == stateRoot || native != nil && origin == "" {
		return nil, ErrInvalid
	}
	life, cancel := context.WithCancel(context.Background())
	state := &clientState{life: life, cancel: cancel, home: home, stateRoot: stateRoot, origin: origin, key: bytes.Clone(existingKey), native: native}
	return &Client{private: func() *clientState { return state }}, nil
}

func (client *Client) begin(caller context.Context) (context.Context, func(), error) {
	if client == nil || client.private == nil {
		return nil, nil, ErrClosed
	}
	state := client.private()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, nil, ErrClosed
	}
	if err := caller.Err(); err != nil {
		return nil, nil, err
	}
	if state.busy {
		return nil, nil, ErrBusy
	}
	state.busy = true
	state.workers.Add(1)
	ctx, cancel := context.WithCancel(caller)
	joined := make(chan struct{})
	stop := context.AfterFunc(state.life, func() { cancel(); close(joined) })
	return ctx, func() {
		cancel()
		if !stop() {
			<-joined
		}
		state.mutex.Lock()
		state.busy = false
		state.mutex.Unlock()
		state.workers.Done()
	}, nil
}

func (client *Client) Close() error {
	if client == nil || client.private == nil {
		return nil
	}
	state := client.private()
	state.mutex.Lock()
	state.closed = true
	state.cancel()
	state.mutex.Unlock()
	state.workers.Wait()
	state.mutex.Lock()
	clear(state.key)
	state.mutex.Unlock()
	return nil
}

func (client *Client) Apply(ctx context.Context, request ApplyRequest, ownerSession string, ui Interaction) (Result, error) {
	ctx, done, err := client.begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer done()
	state := client.private()
	if state.native == nil || request.OwnerID == "" || request.AssetID == "" || request.ProfileID == "" || request.AssetRevision <= 0 || ownerSession == "" || request.Apply && ui.Approve == nil {
		return Result{}, ErrInvalid
	}
	var current configapply.SelectionState
	err = client.withEngine(ctx, func(engine *configapply.Engine) error {
		var readErr error
		current, readErr = engine.CurrentSelection(ctx, state.origin, request.OwnerID, request.ProfileID)
		return readErr
	})
	if err != nil {
		return Result{}, err
	}
	next, err := nextSelection(current)
	if err != nil {
		return Result{}, err
	}
	selectionRevision, confirmationRevision, policyRevision := next.SelectionRevision, next.ConfirmationRevision, next.PolicyRevision
	device := next.DeviceID
	flow, err := state.native.OpenConsumer(ctx, request.OwnerID, ownerSession)
	if err != nil {
		return Result{}, err
	}
	defer flow.Close()
	prepared, err := flow.Prepare(ctx, configclient.NativeSelection{AssetID: request.AssetID, AssetRevision: request.AssetRevision, ProfileID: request.ProfileID, SelectionRevision: selectionRevision})
	if err != nil {
		return Result{}, err
	}
	if prepared.Origin != state.origin || prepared.OwnerID != request.OwnerID || prepared.ProfileID != request.ProfileID || prepared.AssetID != request.AssetID || prepared.AssetRevision != request.AssetRevision || prepared.SelectionRevision != selectionRevision || prepared.SchemaVersion != 1 || len(prepared.RequiredSlots) > 1 {
		return Result{}, ErrInvalid
	}
	input := configapply.SelectionInput{
		Selected: true, Origin: prepared.Origin, Owner: prepared.OwnerID, Profile: prepared.ProfileID,
		DeviceID: device, AssetID: prepared.AssetID, SchemaVersion: prepared.SchemaVersion, AssetRevision: uint64(prepared.AssetRevision),
		Adapter: prepared.Adapter, RequiredCapabilities: prepared.RequiredCapabilities, Patch: prepared.Patch,
		SelectionRevision: selectionRevision, ConfirmationRevision: confirmationRevision, PolicyRevision: policyRevision,
		OperationID: hex.EncodeToString(prepared.OperationID[:]), PreparationID: prepared.PreparationID,
		PreparationPreparedAt: prepared.PreparedAt, PreparationExpiresAt: prepared.PreparationExpiresAt,
		NativeSessionExpiresAt: new(prepared.NativeSessionExpiresAt), Scope: "user",
	}
	selection, err := configapply.NewSelection(input)
	if err != nil {
		return Result{}, err
	}
	var plan *configapply.Plan
	err = client.withEngine(ctx, func(engine *configapply.Engine) error {
		var previewErr error
		plan, previewErr = engine.Preview(ctx, selection)
		return previewErr
	})
	if err != nil {
		return Result{}, err
	}
	result := Result{Preview: plan.View()}
	if !slices.Equal(result.Preview.Slots, prepared.RequiredSlots) {
		return Result{}, ErrInvalid
	}
	if err := preview(ctx, plan, ui); err != nil {
		return result, err
	}
	if !request.Apply {
		return result, ctx.Err()
	}
	if err := approve(ctx, plan, ui); err != nil {
		return result, err
	}
	confirmed := plan.Confirm()
	required, err := flow.Authorize(ctx, configclient.NativeApproval{
		PreparationID: prepared.PreparationID, OperationID: prepared.OperationID, Commitment: confirmed.Commitment(),
		IntentExpiresAt: confirmed.ExpiresAt(), Slots: slices.Clone(prepared.RequiredSlots), ConfirmationRevision: confirmationRevision,
	})
	if err != nil {
		return remoteFailure(ctx, flow, result, err)
	}
	if required != (len(prepared.RequiredSlots) == 1) {
		return remoteFailure(ctx, flow, result, configclient.ErrNativeUnknown)
	}
	material := make(map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial)
	defer func() {
		for _, secret := range material {
			secret.Destroy()
		}
	}()
	if required {
		remote, consumeErr := flow.Consume(ctx)
		if consumeErr != nil {
			return remoteFailure(ctx, flow, result, consumeErr)
		}
		defer remote.Destroy()
		data := remote.CloneBytes()
		secret, materialErr := configapply.NewSecretMaterial(data)
		clear(data)
		if materialErr != nil {
			return result, materialErr
		}
		material[prepared.RequiredSlots[0]] = secret
		result.RemoteState = configclient.NativeConsumed
	}
	err = client.withEngine(ctx, func(engine *configapply.Engine) error {
		var applyErr error
		result.Local, applyErr = engine.Apply(ctx, confirmed, material)
		return applyErr
	})
	return result, err
}

func (client *Client) Restore(ctx context.Context, operationID string, apply bool, ui Interaction) (Result, error) {
	ctx, done, err := client.begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer done()
	if apply && ui.Approve == nil {
		return Result{}, ErrInvalid
	}
	var plan *configapply.Plan
	err = client.withEngine(ctx, func(engine *configapply.Engine) error {
		var previewErr error
		plan, previewErr = engine.PreviewRestore(ctx, operationID)
		return previewErr
	})
	if err != nil {
		return Result{}, err
	}
	result := Result{Preview: plan.View()}
	if err := preview(ctx, plan, ui); err != nil {
		return result, err
	}
	if !apply {
		return result, ctx.Err()
	}
	if err := approve(ctx, plan, ui); err != nil {
		return result, err
	}
	err = client.withEngine(ctx, func(engine *configapply.Engine) error {
		var restoreErr error
		result.Local, restoreErr = engine.Restore(ctx, plan.Confirm())
		if restoreErr != nil {
			return restoreErr
		}
		result.Local, restoreErr = engine.Status(ctx, plan.OperationID())
		return restoreErr
	})
	return result, err
}

func (client *Client) Status(ctx context.Context, operationID string) (configapply.SafeResult, error) {
	ctx, done, err := client.begin(ctx)
	if err != nil {
		return configapply.SafeResult{}, err
	}
	defer done()
	var result configapply.SafeResult
	err = client.withEngine(ctx, func(engine *configapply.Engine) error {
		var statusErr error
		result, statusErr = engine.Status(ctx, operationID)
		return statusErr
	})
	if err != nil {
		return configapply.SafeResult{}, err
	}
	return result, nil
}

func (client *Client) withEngine(ctx context.Context, action func(*configapply.Engine) error) error {
	state := client.private()
	engine, err := configapply.Open(ctx, state.home, state.stateRoot, state.key)
	if err != nil {
		return err
	}
	operationErr := action(engine)
	closeErr := engine.Close()
	return errors.Join(operationErr, closeErr, ctx.Err())
}

func nextSelection(current configapply.SelectionState) (configapply.SelectionState, error) {
	if !current.Exists {
		return configapply.SelectionState{Exists: true, SelectionRevision: 1, ConfirmationRevision: 1, DeviceID: uuid.New().String()}, nil
	}
	if current.SelectionRevision == math.MaxUint64 || current.ConfirmationRevision == math.MaxUint64 {
		return configapply.SelectionState{}, ErrInvalid
	}
	current.SelectionRevision++
	current.ConfirmationRevision++
	current.AutoApply = false
	return current, nil
}

func preview(ctx context.Context, plan *configapply.Plan, ui Interaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(plan.ExpiresAt()) {
		return configapply.ErrPlanExpired
	}
	if ui.Preview != nil {
		if err := ui.Preview(ctx, plan.View()); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrDenied
		}
	}
	return ctx.Err()
}

func approve(ctx context.Context, plan *configapply.Plan, ui Interaction) error {
	if ui.Approve == nil {
		return ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(plan.ExpiresAt()) {
		return configapply.ErrPlanExpired
	}
	approval, err := ui.Approve(ctx, plan.View(), slices.Clone(plan.View().Slots))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || !approval.Confirmed || !slices.Equal(approval.ApprovedSlots, plan.View().Slots) {
		return ErrDenied
	}
	if !time.Now().Before(plan.ExpiresAt()) {
		return configapply.ErrPlanExpired
	}
	return nil
}

func remoteFailure(ctx context.Context, flow *configclient.NativeFlow, result Result, err error) (Result, error) {
	if errors.Is(err, configclient.ErrNativeUnknown) {
		result.RemoteUnknown = true
		if ctx.Err() == nil {
			if status, statusErr := flow.Status(ctx); statusErr == nil {
				result.RemoteState = status.State
			}
		}
	}
	return result, err
}

func (Client) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private native configuration client]")
}
func (Client) String() string       { return "[private native configuration client]" }
func (Client) LogValue() slog.Value { return slog.StringValue("[private native configuration client]") }
func (Client) MarshalJSON() ([]byte, error) {
	return []byte(`"[private native configuration client]"`), nil
}
