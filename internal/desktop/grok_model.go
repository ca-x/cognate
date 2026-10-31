package desktop

import (
	"bytes"
	"context"
	"log/slog"
	"sync"

	"github.com/ca-x/cognate/internal/grokclient"
)

type grokAssetsModel struct{ private func() *grokAssetsState }

func (*grokAssetsModel) String() string   { return "[private Grok desktop assets]" }
func (*grokAssetsModel) GoString() string { return "[private Grok desktop assets]" }
func (*grokAssetsModel) MarshalJSON() ([]byte, error) {
	return []byte(`"[private Grok desktop assets]"`), nil
}
func (*grokAssetsModel) LogValue() slog.Value {
	return slog.StringValue("[private Grok desktop assets]")
}

type grokAssetsState struct {
	mutex        sync.Mutex
	once         sync.Once
	workers      sync.WaitGroup
	client       *grokclient.Client
	life         context.Context
	cancel       context.CancelFunc
	busy, closed bool
	pending      *grokclient.Operation
}

type grokAssetsReview struct {
	Summary, Confirmation string
}

func (model *grokAssetsModel) begin(caller context.Context) (context.Context, *grokAssetsState, func(), error) {
	if model == nil || model.private == nil {
		return nil, nil, nil, grokclient.ErrClosed
	}
	if caller == nil {
		return nil, nil, nil, grokclient.ErrInvalid
	}
	state := model.private()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, nil, nil, grokclient.ErrClosed
	}
	if state.busy {
		return nil, nil, nil, grokclient.ErrBusy
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithCancel(caller)
	stop := context.AfterFunc(state.life, cancel)
	state.busy = true
	state.workers.Add(1)
	return ctx, state, func() {
		stop()
		cancel()
		state.mutex.Lock()
		state.busy = false
		state.mutex.Unlock()
		state.workers.Done()
	}, nil
}

func (model *grokAssetsModel) Close() {
	if model == nil || model.private == nil {
		return
	}
	state := model.private()
	state.once.Do(func() {
		state.mutex.Lock()
		state.closed = true
		state.cancel()
		state.mutex.Unlock()
		state.client.Close()
		state.workers.Wait()
		if state.pending != nil {
			state.pending.Close()
			state.pending = nil
		}
	})
}

func (model *grokAssetsModel) List(caller context.Context) ([]grokclient.Metadata, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	return state.client.List(ctx)
}

func (model *grokAssetsModel) Read(caller context.Context, id string) (string, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return "", err
	}
	defer release()
	view, err := state.client.Read(ctx, id)
	if err != nil {
		return "", err
	}
	defer view.Close()
	var summary bytes.Buffer
	if err := view.WriteSummary(ctx, &summary); err != nil {
		return "", err
	}
	return summary.String(), nil
}

func (model *grokAssetsModel) Editable(caller context.Context, id string) (grokclient.EditView, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return grokclient.EditView{}, err
	}
	defer release()
	view, err := state.client.Read(ctx, id)
	if err != nil {
		return grokclient.EditView{}, err
	}
	defer view.Close()
	return view.Editable(ctx)
}

func (model *grokAssetsModel) PrepareInput(caller context.Context, change grokclient.Change, draft grokclient.Draft, action grokclient.KeyAction, key []byte) (grokAssetsReview, error) {
	input, err := grokclient.NewInput(caller, draft, action, key)
	if err != nil {
		return grokAssetsReview{}, err
	}
	defer input.Close()
	return model.prepare(caller, change, input)
}

func (model *grokAssetsModel) Order(caller context.Context) (grokclient.OrderSnapshot, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return grokclient.OrderSnapshot{}, err
	}
	defer release()
	return state.client.GetOrder(ctx)
}

func (model *grokAssetsModel) Prepare(caller context.Context, change grokclient.Change) (grokAssetsReview, error) {
	return model.prepare(caller, change, nil)
}

func (model *grokAssetsModel) prepare(caller context.Context, change grokclient.Change, input *grokclient.Input) (grokAssetsReview, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return grokAssetsReview{}, err
	}
	defer release()
	if state.pending != nil {
		state.pending.Close()
		state.pending = nil
	}
	if input == nil && change.Action != grokclient.Copy && change.Action != grokclient.Delete && change.Action != grokclient.ReplaceOrder {
		return grokAssetsReview{}, grokclient.ErrUnsupported
	}
	operation, err := state.client.Prepare(ctx, change, input)
	if err != nil {
		return grokAssetsReview{}, err
	}
	var summary bytes.Buffer
	if err := operation.WriteSummary(ctx, &summary); err != nil {
		operation.Close()
		return grokAssetsReview{}, err
	}
	state.pending = operation
	return grokAssetsReview{Summary: summary.String(), Confirmation: operation.Confirmation()}, nil
}

func (model *grokAssetsModel) CancelReview() error {
	_, state, release, err := model.begin(context.Background())
	if err != nil {
		return err
	}
	defer release()
	if state.pending != nil {
		state.pending.Close()
		state.pending = nil
	}
	return nil
}

func (model *grokAssetsModel) Submit(caller context.Context, confirmation string) (grokclient.Result, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return grokclient.Result{}, err
	}
	defer release()
	operation := state.pending
	if operation == nil {
		return grokclient.Result{}, grokclient.ErrClosed
	}
	if confirmation == "" || confirmation != operation.Confirmation() {
		return grokclient.Result{}, grokclient.ErrInvalid
	}
	state.pending = nil
	defer operation.Close()
	return operation.Submit(ctx)
}
