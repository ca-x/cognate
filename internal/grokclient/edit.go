package grokclient

import (
	"bytes"
	"context"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
)

type Mode string

const (
	NativeLogin Mode = "native-login"
	CustomModel Mode = "custom-model"
)

type Draft struct {
	private
	Name  string
	Mode  Mode
	Model *Model
}

type Model struct {
	private
	Profile, UpstreamModel, Endpoint, Name, APIBackend string
	ContextWindow                                      int64
	EnvKey                                             *string
}

type EditView struct {
	private
	Metadata                              Metadata
	Draft                                 Draft
	KeyPresent, ReadOnly, NeedsCredential bool
}

func NewInput(ctx context.Context, draft Draft, action KeyAction, key []byte) (*Input, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if action > ClearKey || action == ReplaceKey && !validKey(key) || action != ReplaceKey && len(key) != 0 {
		return nil, ErrInvalid
	}
	provider := &configurationv1.GrokBuildProvider{}
	switch draft.Mode {
	case NativeLogin:
		if draft.Model != nil || action == ReplaceKey {
			return nil, ErrInvalid
		}
		provider.Mode = configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN
	case CustomModel:
		if draft.Model == nil {
			return nil, ErrInvalid
		}
		provider.Mode = configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL
		model := draft.Model
		provider.Model = &configurationv1.GrokBuildModel{Profile: model.Profile, UpstreamModel: model.UpstreamModel, Endpoint: model.Endpoint, Name: model.Name, ApiBackend: model.APIBackend, ContextWindow: model.ContextWindow}
		if model.EnvKey != nil {
			provider.Model.EnvKey = new(*model.EnvKey)
		}
	default:
		return nil, ErrInvalid
	}
	value := &configurationv1.Draft{Name: draft.Name, Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: provider}}
	if err := validateDraft(value); err != nil {
		return nil, err
	}
	owned := &inputState{draft: value, action: action, key: bytes.Clone(key)}
	if err := ctx.Err(); err != nil {
		clear(owned.key)
		return nil, err
	}
	return &Input{state: func() *inputState { return owned }}, nil
}

func (view *View) Editable(ctx context.Context) (EditView, error) {
	if ctx == nil {
		return EditView{}, ErrInvalid
	}
	if view == nil || view.state == nil {
		return EditView{}, ErrClosed
	}
	state := view.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return EditView{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	if state.draft == nil {
		return EditView{}, ErrUnsupported
	}
	provider := state.draft.GetGrokbuildProvider()
	draft := Draft{Name: state.draft.Name}
	switch provider.Mode {
	case configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN:
		draft.Mode = NativeLogin
	case configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL:
		draft.Mode = CustomModel
		model := provider.Model
		draft.Model = &Model{Profile: model.Profile, UpstreamModel: model.UpstreamModel, Endpoint: model.Endpoint, Name: model.Name, APIBackend: model.ApiBackend, ContextWindow: model.ContextWindow}
		if model.EnvKey != nil {
			draft.Model.EnvKey = new(*model.EnvKey)
		}
	default:
		return EditView{}, ErrUnsupported
	}
	return EditView{Metadata: state.metadata, Draft: draft, KeyPresent: state.present, ReadOnly: state.extras, NeedsCredential: readiness(state.draft, state.present) == configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL}, nil
}
