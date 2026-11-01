package desktop

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/ca-x/cognate/internal/mcpclient"
)

var errMCPReadOnly = errors.New("MCP asset has unknown fields or schema; read only")

type mcpAssetsModel struct{ private func() *mcpAssetsState }

func (*mcpAssetsModel) String() string   { return "[private MCP desktop assets]" }
func (*mcpAssetsModel) GoString() string { return "[private MCP desktop assets]" }
func (*mcpAssetsModel) MarshalJSON() ([]byte, error) {
	return []byte(`"[private MCP desktop assets]"`), nil
}
func (*mcpAssetsModel) LogValue() slog.Value {
	return slog.StringValue("[private MCP desktop assets]")
}

type mcpAssetsState struct {
	mutex        sync.Mutex
	once         sync.Once
	workers      sync.WaitGroup
	client       *mcpclient.Client
	life         context.Context
	cancel       context.CancelFunc
	busy, closed bool
	pending      *mcpclient.Operation
}

type mcpAssetsReview struct {
	Summary, Confirmation string
	Slots                 []string
}

func (model *mcpAssetsModel) begin(caller context.Context) (context.Context, *mcpAssetsState, func(), error) {
	if model == nil || model.private == nil {
		return nil, nil, nil, mcpclient.ErrClosed
	}
	if caller == nil {
		return nil, nil, nil, mcpclient.ErrInvalid
	}
	state := model.private()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, nil, nil, mcpclient.ErrClosed
	}
	if state.busy {
		return nil, nil, nil, mcpclient.ErrBusy
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

func (model *mcpAssetsModel) Close() {
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

func (model *mcpAssetsModel) List(caller context.Context) ([]mcpclient.Metadata, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	return state.client.List(ctx)
}

func (model *mcpAssetsModel) Read(caller context.Context, id string) (mcpclient.EditView, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return mcpclient.EditView{}, err
	}
	defer release()
	view, err := state.client.Read(ctx, id)
	if err != nil {
		if errors.Is(err, mcpclient.ErrInvalid) || errors.Is(err, mcpclient.ErrConflict) {
			metadata, metadataErr := state.client.Get(ctx, id)
			if metadataErr != nil {
				return mcpclient.EditView{}, metadataErr
			}
			return mcpclient.EditView{Metadata: metadata, ReadOnly: true}, nil
		}
		return mcpclient.EditView{}, err
	}
	return view.Editable(ctx)
}

func (model *mcpAssetsModel) Preview(caller context.Context, id string, revision int64, input *mcpclient.Input) (string, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return "", err
	}
	defer release()
	if state.pending != nil {
		state.pending.Close()
		state.pending = nil
	}
	preview, err := state.client.Preview(ctx, id, revision, input)
	if err != nil {
		return "", err
	}
	var summary bytes.Buffer
	if err := input.WriteSummary(ctx, &summary); err != nil {
		return "", err
	}
	if err := preview.WriteSummary(ctx, &summary); err != nil {
		return "", err
	}
	return summary.String(), nil
}

func (model *mcpAssetsModel) Prepare(caller context.Context, change mcpclient.Change, input *mcpclient.Input) (mcpAssetsReview, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return mcpAssetsReview{}, err
	}
	defer release()
	if state.pending != nil {
		state.pending.Close()
		state.pending = nil
	}
	var slots []string
	if input != nil {
		slots, err = input.ChangedSlots(ctx)
		if err != nil {
			return mcpAssetsReview{}, err
		}
	}
	if change.Action != "create" {
		view, err := state.client.Read(ctx, change.ID)
		if err != nil {
			return mcpAssetsReview{}, err
		}
		editable, err := view.Editable(ctx)
		if err != nil {
			return mcpAssetsReview{}, err
		}
		if editable.ReadOnly {
			return mcpAssetsReview{}, errMCPReadOnly
		}
		if editable.Metadata.Revision != change.Revision {
			return mcpAssetsReview{}, mcpclient.ErrConflict
		}
	}
	operation, err := state.client.Prepare(ctx, change, input)
	if err != nil {
		return mcpAssetsReview{}, err
	}
	var summary bytes.Buffer
	if err := operation.WriteSummary(ctx, &summary); err != nil {
		operation.Close()
		return mcpAssetsReview{}, err
	}
	state.pending = operation
	return mcpAssetsReview{Summary: summary.String(), Confirmation: operation.Confirmation(), Slots: append([]string(nil), slots...)}, nil
}

func (model *mcpAssetsModel) CancelReview() error {
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

func (model *mcpAssetsModel) Submit(caller context.Context, confirmation string) (mcpclient.Metadata, error) {
	ctx, state, release, err := model.begin(caller)
	if err != nil {
		return mcpclient.Metadata{}, err
	}
	defer release()
	operation := state.pending
	if operation == nil {
		return mcpclient.Metadata{}, mcpclient.ErrClosed
	}
	if confirmation == "" || confirmation != operation.Confirmation() {
		return mcpclient.Metadata{}, mcpclient.ErrInvalid
	}
	state.pending = nil
	defer operation.Close()
	return operation.Submit(ctx)
}
