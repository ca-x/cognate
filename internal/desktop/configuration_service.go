package desktop

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func configurationLocalFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	for _, safe := range []error{clientkey.ErrInitializationRequired, clientkey.ErrRecoveryRequired, clientkey.ErrUnavailable, clientkey.ErrUnsupported, errConfigurationIdentity, errConfigurationInvalid, errConfigurationUnsupported, nativeconfig.ErrDenied, configapply.ErrPlanExpired, configapply.ErrConflict, configclient.ErrNativeConflict, configclient.ErrNativeUnknown, configapply.ErrOutcomeUnknown, configapply.ErrPending} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return errConfigurationUnavailable
}

func (service *Service) configurationLocal(ctx context.Context, identity configurationIdentity, remote bool) (*nativeconfig.Client, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, nil, errConfigurationUnsupported
	}
	if !filepath.IsAbs(service.Home) || filepath.Clean(service.Home) != service.Home {
		return nil, nil, errConfigurationInvalid
	}
	key, err := clientkey.LoadExisting(ctx, service.Home)
	defer clear(key)
	if err != nil {
		return nil, nil, configurationLocalFailure(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	origin := ""
	var native *configclient.NativeClient
	if remote {
		if identity.private == nil {
			return nil, nil, errConfigurationIdentity
		}
		data := identity.private()
		data.mutex.Lock()
		session := data.session
		data.mutex.Unlock()
		fresh, err := service.ConfigurationIdentity(ctx, identity.Origin, session)
		if err != nil {
			return nil, nil, err
		}
		fresh.clear()
		if fresh.OwnerID != identity.OwnerID {
			return nil, nil, errConfigurationIdentity
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		origin = identity.Origin
		var settings *http.Transport
		if service.HTTP != nil && service.HTTP.Transport != nil {
			var ok bool
			settings, ok = service.HTTP.Transport.(*http.Transport)
			if !ok {
				return nil, nil, errConfigurationInvalid
			}
		}
		native, err = configclient.NewNativeClient(origin, settings)
		if err != nil {
			return nil, nil, errConfigurationInvalid
		}
	}
	client, err := nativeconfig.New(service.Home, filepath.Join(service.Home, ".local", "share", "skillhub"), origin, key, native)
	if err != nil {
		if native != nil {
			native.Close()
		}
		return nil, nil, errConfigurationInvalid
	}
	return client, func() {
		if native != nil {
			native.Close()
		}
	}, nil
}

func configurationResult(result nativeconfig.Result) configurationOutcome {
	return configurationOutcome{Plan: result.Preview, Local: result.Local, RemoteState: result.RemoteState, RemoteUnknown: result.RemoteUnknown}
}

func configurationFinish(ctx context.Context, client *nativeconfig.Client, outcome *configurationOutcome, failure *error) {
	closed := client.Close()
	outcome.CleanupIncomplete = closed != nil
	outcome.LocalUnknown = errors.Is(*failure, configapply.ErrOutcomeUnknown) || errors.Is(closed, configapply.ErrOutcomeUnknown)
	*failure = configurationLocalFailure(ctx, errors.Join(*failure, closed))
	outcome.Failed = *failure != nil
}

func (service *Service) ApplyConfiguration(ctx context.Context, identity configurationIdentity, asset configurationAsset, profile string, apply bool, interaction nativeconfig.Interaction) (outcome configurationOutcome, failure error) {
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if runtime.GOOS != "linux" {
		return outcome, errConfigurationUnsupported
	}
	if !asset.Editable || asset.AgentID != "claude" || asset.Kind != "provider" || asset.SchemaVersion != 1 || !configurationText(asset.ID, 128) || asset.Revision < 1 || !configurationText(profile, 128) || !strings.HasPrefix(identity.Origin, "https://") {
		return outcome, errConfigurationInvalid
	}
	if canonical, err := ValidateOrigin(identity.Origin); err != nil || canonical != identity.Origin {
		return outcome, errConfigurationInvalid
	}
	client, cleanup, err := service.configurationLocal(ctx, identity, true)
	if err != nil {
		return outcome, err
	}
	defer cleanup()
	defer configurationFinish(ctx, client, &outcome, &failure)
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if identity.private == nil {
		return outcome, errConfigurationIdentity
	}
	data := identity.private()
	data.mutex.Lock()
	session := data.session
	data.mutex.Unlock()
	result, err := client.Apply(ctx, nativeconfig.ApplyRequest{OwnerID: identity.OwnerID, AssetID: asset.ID, AssetRevision: asset.Revision, ProfileID: profile, Apply: apply}, session, interaction)
	return configurationResult(result), err
}

func (service *Service) RestoreConfiguration(ctx context.Context, operationID string, apply bool, interaction nativeconfig.Interaction) (outcome configurationOutcome, failure error) {
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if runtime.GOOS != "linux" {
		return outcome, errConfigurationUnsupported
	}
	if !configurationText(operationID, 128) {
		return outcome, errConfigurationInvalid
	}
	client, cleanup, err := service.configurationLocal(ctx, configurationIdentity{}, false)
	if err != nil {
		return outcome, err
	}
	defer cleanup()
	defer configurationFinish(ctx, client, &outcome, &failure)
	result, err := client.Restore(ctx, operationID, apply, interaction)
	return configurationResult(result), err
}

func (service *Service) ConfigurationStatus(ctx context.Context, operationID string) (outcome configurationOutcome, failure error) {
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if runtime.GOOS != "linux" {
		return outcome, errConfigurationUnsupported
	}
	if !configurationText(operationID, 128) {
		return outcome, errConfigurationInvalid
	}
	client, cleanup, err := service.configurationLocal(ctx, configurationIdentity{}, false)
	if err != nil {
		return outcome, err
	}
	defer cleanup()
	defer configurationFinish(ctx, client, &outcome, &failure)
	outcome.Local, failure = client.Status(ctx, operationID)
	return outcome, failure
}

func configurationOptions() []connect.ClientOption {
	return []connect.ClientOption{connect.WithSendMaxBytes(128 << 10), connect.WithReadMaxBytes(1 << 20)}
}

func configurationFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	switch connect.CodeOf(err) {
	case connect.CodeCanceled:
		return context.Canceled
	case connect.CodeDeadlineExceeded:
		return context.DeadlineExceeded
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return errConfigurationIdentity
	case connect.CodeNotFound:
		return errConfigurationNotFound
	case connect.CodeAborted:
		return errConfigurationConflict
	case connect.CodeInvalidArgument:
		return errConfigurationInvalid
	case connect.CodeFailedPrecondition:
		return errConfigurationUnsupported
	case connect.CodeResourceExhausted:
		return errConfigurationLimit
	default:
		return errConfigurationUnavailable
	}
}

func configurationText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func configurationKnown(message proto.Message) bool {
	if message == nil {
		return false
	}
	return configurationKnownMessage(message.ProtoReflect())
}

func configurationKnownMessage(message protoreflect.Message) bool {
	if !message.IsValid() || len(message.GetUnknown()) != 0 {
		return false
	}
	known := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() == protoreflect.MessageKind {
			if field.IsList() {
				list := value.List()
				for index := range list.Len() {
					if !configurationKnownMessage(list.Get(index).Message()) {
						known = false
						break
					}
				}
			} else {
				known = configurationKnownMessage(value.Message())
			}
		}
		return known
	})
	return known
}

func configurationSession(session string) bool {
	parsed, err := importclient.ReadSession(strings.NewReader(session))
	return err == nil && parsed == session
}

func (identity configurationIdentity) authorize(header http.Header) error {
	if identity.private == nil || !configurationText(identity.OwnerID, 128) {
		return errConfigurationIdentity
	}
	data := identity.private()
	data.mutex.Lock()
	defer data.mutex.Unlock()
	if !configurationSession(data.session) {
		return errConfigurationIdentity
	}
	header.Set("Authorization", "Bearer "+data.session)
	return nil
}

func (service *Service) configurationClient(ctx context.Context, identity configurationIdentity) (configurationv1connect.ConfigurationServiceClient, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	origin, err := ValidateOrigin(identity.Origin)
	if err != nil || origin != identity.Origin {
		return nil, errConfigurationInvalid
	}
	if err := identity.authorize(make(http.Header)); err != nil {
		return nil, err
	}
	return configurationv1connect.NewConfigurationServiceClient(service.client(origin), origin, configurationOptions()...), nil
}

func (service *Service) ConfigurationIdentity(ctx context.Context, origin, session string) (configurationIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return configurationIdentity{}, ctx.Err()
	}
	canonical, err := ValidateOrigin(origin)
	if err != nil {
		return configurationIdentity{}, errConfigurationInvalid
	}
	if !configurationSession(session) {
		return configurationIdentity{}, errConfigurationIdentity
	}
	request := connect.NewRequest(&authv1.GetSessionRequest{})
	request.Header().Set("Authorization", "Bearer "+session)
	client := authv1connect.NewAuthServiceClient(service.client(canonical), canonical, configurationOptions()...)
	response, err := client.GetSession(ctx, request)
	if err := configurationFailure(ctx, err); err != nil {
		return configurationIdentity{}, err
	}
	if !configurationKnown(response.Msg) || !configurationText(response.Msg.UserId, 128) {
		return configurationIdentity{}, errConfigurationInvalid
	}
	data := &configurationIdentityData{session: session}
	return configurationIdentity{Origin: canonical, OwnerID: response.Msg.UserId, private: func() *configurationIdentityData { return data }}, nil
}

func (service *Service) ConfigurationAssets(ctx context.Context, identity configurationIdentity) ([]configurationAsset, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return nil, err
	}
	request := connect.NewRequest(&configurationv1.ListAssetsRequest{})
	if err := identity.authorize(request.Header()); err != nil {
		return nil, err
	}
	response, err := client.ListAssets(ctx, request)
	if err := configurationFailure(ctx, err); err != nil {
		return nil, err
	}
	if !configurationKnown(response.Msg) || len(response.Msg.Assets) > 250 {
		return nil, errConfigurationInvalid
	}
	assets := make([]configurationAsset, 0, len(response.Msg.Assets))
	seen := make(map[string]bool)
	for _, entry := range response.Msg.Assets {
		asset, err := configurationMetadata(entry)
		if err != nil || seen[asset.ID] {
			return nil, errConfigurationInvalid
		}
		seen[asset.ID] = true
		assets = append(assets, asset)
	}
	return assets, nil
}

func (service *Service) ConfigurationDraft(ctx context.Context, identity configurationIdentity, id string) (configurationDraft, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return configurationDraft{}, err
	}
	if !configurationID(id) {
		return configurationDraft{}, errConfigurationInvalid
	}
	get := connect.NewRequest(&configurationv1.GetAssetRequest{Id: id})
	if err := identity.authorize(get.Header()); err != nil {
		return configurationDraft{}, err
	}
	metadata, err := client.GetAsset(ctx, get)
	if err := configurationFailure(ctx, err); err != nil {
		return configurationDraft{}, err
	}
	if !configurationKnown(metadata.Msg) {
		return configurationDraft{}, errConfigurationInvalid
	}
	asset, err := configurationMetadata(metadata.Msg.Asset)
	if err != nil || asset.ID != id {
		return configurationDraft{}, errConfigurationInvalid
	}
	if !asset.Editable {
		return configurationDraft{}, errConfigurationUnsupported
	}
	request := connect.NewRequest(&configurationv1.ReadDraftRequest{Id: id})
	if err := identity.authorize(request.Header()); err != nil {
		return configurationDraft{}, err
	}
	response, err := client.ReadDraft(ctx, request)
	if err := configurationFailure(ctx, err); err != nil {
		return configurationDraft{}, err
	}
	if !configurationKnown(response.Msg) {
		return configurationDraft{}, errConfigurationInvalid
	}
	draft, err := configurationView(response.Msg.View)
	if err != nil || draft.Asset.ID != id {
		return configurationDraft{}, errConfigurationInvalid
	}
	if draft.Asset.Revision != asset.Revision {
		return configurationDraft{}, errConfigurationConflict
	}
	return draft, nil
}

func (service *Service) SaveConfiguration(ctx context.Context, identity configurationIdentity, edit configurationEdit) (configurationDraft, error) {
	defer edit.clear()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return configurationDraft{}, err
	}
	if edit.private == nil {
		return configurationDraft{}, errConfigurationInvalid
	}
	data := edit.private()
	draft, mutations, err := configurationInput(data)
	if err != nil {
		return configurationDraft{}, err
	}
	defer func() {
		for _, mutation := range mutations {
			mutation.Action = nil
		}
		draft.Payload = nil
		draft.Name = ""
	}()
	var view *configurationv1.DraftView
	if data.Asset.ID == "" {
		request := connect.NewRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: mutations})
		if err := identity.authorize(request.Header()); err != nil {
			return configurationDraft{}, err
		}
		response, err := client.CreateAsset(ctx, request)
		if err := configurationFailure(ctx, err); err != nil {
			return configurationDraft{}, err
		}
		if !configurationKnown(response.Msg) {
			return configurationDraft{}, errConfigurationInvalid
		}
		view = response.Msg.View
	} else {
		request := connect.NewRequest(&configurationv1.UpdateAssetRequest{Id: data.Asset.ID, ExpectedRevision: data.Asset.Revision, Draft: draft, Secrets: mutations})
		if err := identity.authorize(request.Header()); err != nil {
			return configurationDraft{}, err
		}
		response, err := client.UpdateAsset(ctx, request)
		if err := configurationFailure(ctx, err); err != nil {
			return configurationDraft{}, err
		}
		if !configurationKnown(response.Msg) {
			return configurationDraft{}, errConfigurationInvalid
		}
		view = response.Msg.View
	}
	result, err := configurationView(view)
	if err != nil {
		return configurationDraft{}, err
	}
	if result.Asset.Name != data.Name || result.Asset.Revision != data.Asset.Revision+1 || data.Asset.ID != "" && result.Asset.ID != data.Asset.ID {
		return configurationDraft{}, errConfigurationInvalid
	}
	return result, nil
}

func (service *Service) CopyConfiguration(ctx context.Context, identity configurationIdentity, asset configurationAsset, name string) (configurationDraft, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return configurationDraft{}, err
	}
	if !asset.Editable || asset.AgentID != "claude" || asset.Kind != "provider" || asset.SchemaVersion != 1 {
		return configurationDraft{}, errConfigurationUnsupported
	}
	if !configurationID(asset.ID) || asset.Revision < 1 || !configurationText(name, 128) || name != strings.TrimSpace(name) {
		return configurationDraft{}, errConfigurationInvalid
	}
	request := connect.NewRequest(&configurationv1.CopyAssetRequest{SourceId: asset.ID, ExpectedSourceRevision: asset.Revision, Name: name})
	if err := identity.authorize(request.Header()); err != nil {
		return configurationDraft{}, err
	}
	response, err := client.CopyAsset(ctx, request)
	if err := configurationFailure(ctx, err); err != nil {
		return configurationDraft{}, err
	}
	if !configurationKnown(response.Msg) {
		return configurationDraft{}, errConfigurationInvalid
	}
	draft, err := configurationView(response.Msg.View)
	if err != nil {
		return configurationDraft{}, err
	}
	provider := draft.private().Provider
	if draft.Asset.ID == asset.ID || draft.Asset.Revision != 1 || draft.Asset.Name != name || provider.APIKeyPresent || provider.AuthTokenPresent {
		return configurationDraft{}, errConfigurationInvalid
	}
	return draft, nil
}

func (service *Service) DeleteConfiguration(ctx context.Context, identity configurationIdentity, asset configurationAsset) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := service.configurationClient(ctx, identity)
	if err != nil {
		return err
	}
	if !configurationID(asset.ID) || asset.Revision < 1 {
		return errConfigurationInvalid
	}
	request := connect.NewRequest(&configurationv1.DeleteAssetRequest{Id: asset.ID, ExpectedRevision: asset.Revision})
	if err := identity.authorize(request.Header()); err != nil {
		return err
	}
	response, err := client.DeleteAsset(ctx, request)
	if err := configurationFailure(ctx, err); err != nil {
		return err
	}
	if !configurationKnown(response.Msg) {
		return errConfigurationInvalid
	}
	return nil
}

func configurationID(id string) bool {
	return len(id) == 26 && !strings.ContainsFunc(id, func(letter rune) bool { return !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') })
}

func configurationMetadata(entry *configurationv1.Asset) (configurationAsset, error) {
	if entry == nil || !configurationKnown(entry) || !configurationID(entry.Id) || entry.Revision < 1 || entry.SchemaVersion < 1 || entry.SchemaVersion > 65535 || entry.CreatedAtUnix <= 0 || entry.UpdatedAtUnix <= 0 || !configurationText(entry.Name, 128) || entry.Name != strings.TrimSpace(entry.Name) || len(entry.AgentId) == 0 || len(entry.AgentId) > 64 || entry.AgentId[0] < 'a' || entry.AgentId[0] > 'z' || strings.ContainsFunc(entry.AgentId, func(letter rune) bool {
		return !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-')
	}) {
		return configurationAsset{}, errConfigurationInvalid
	}
	var kind string
	switch entry.Kind {
	case configurationv1.AssetKind_ASSET_KIND_PROVIDER:
		kind = "provider"
	case configurationv1.AssetKind_ASSET_KIND_MCP:
		kind = "mcp"
	case configurationv1.AssetKind_ASSET_KIND_PROMPT:
		kind = "prompt"
	case configurationv1.AssetKind_ASSET_KIND_AGENT_SETTINGS:
		kind = "agent-settings"
	case configurationv1.AssetKind_ASSET_KIND_WORKSPACE:
		kind = "workspace"
	default:
		return configurationAsset{}, errConfigurationInvalid
	}
	return configurationAsset{ID: entry.Id, AgentID: entry.AgentId, Kind: kind, Name: entry.Name, SchemaVersion: entry.SchemaVersion, Revision: entry.Revision, Editable: entry.AgentId == "claude" && kind == "provider" && entry.SchemaVersion == 1}, nil
}

func configurationProviderValid(provider agentconfig.ClaudeProviderDesired) bool {
	switch provider.AuthMode {
	case agentconfig.ClaudeNativeLogin, agentconfig.ClaudeAPIKeyAuth, agentconfig.ClaudeAuthTokenAuth:
	default:
		return false
	}
	if provider.Endpoint != nil {
		value := *provider.Endpoint
		endpoint, err := url.Parse(value)
		if !configurationText(value, 2048) || err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.Hostname() == "" || endpoint.User != nil || strings.ContainsAny(value, "?#") || provider.AuthMode == agentconfig.ClaudeNativeLogin {
			return false
		}
	}
	for _, value := range []*string{provider.Models.Default, provider.Models.Haiku, provider.Models.Sonnet, provider.Models.Opus} {
		if value != nil && !configurationText(*value, 256) {
			return false
		}
	}
	return true
}

func configurationView(view *configurationv1.DraftView) (configurationDraft, error) {
	if view == nil || !configurationKnown(view) {
		return configurationDraft{}, errConfigurationInvalid
	}
	asset, err := configurationMetadata(view.Asset)
	if err != nil || !asset.Editable || view.Draft == nil || view.Draft.Name != asset.Name {
		return configurationDraft{}, errConfigurationInvalid
	}
	wire := view.Draft.GetClaudeProvider()
	if wire == nil || wire.Models == nil || len(view.Secrets) != 2 {
		return configurationDraft{}, errConfigurationInvalid
	}
	var mode agentconfig.ClaudeAuthMode
	switch wire.AuthMode {
	case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN:
		mode = agentconfig.ClaudeNativeLogin
	case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY:
		mode = agentconfig.ClaudeAPIKeyAuth
	case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN:
		mode = agentconfig.ClaudeAuthTokenAuth
	default:
		return configurationDraft{}, errConfigurationInvalid
	}
	provider := agentconfig.ClaudeProviderDesired{AuthMode: mode, Endpoint: wire.Endpoint, Models: agentconfig.ClaudeModelsDesired{Default: wire.Models.DefaultModel, Haiku: wire.Models.Haiku, Sonnet: wire.Models.Sonnet, Opus: wire.Models.Opus}}
	seen := make(map[string]bool)
	for _, status := range view.Secrets {
		if status == nil || seen[status.SlotId] {
			return configurationDraft{}, errConfigurationInvalid
		}
		seen[status.SlotId] = true
		switch status.SlotId {
		case string(agentconfig.ClaudeAPIKeySecret):
			provider.APIKeyPresent = status.Present
		case string(agentconfig.ClaudeAuthTokenSecret):
			provider.AuthTokenPresent = status.Present
		default:
			return configurationDraft{}, errConfigurationInvalid
		}
	}
	if !configurationProviderValid(provider) {
		return configurationDraft{}, errConfigurationInvalid
	}
	needs := mode == agentconfig.ClaudeAPIKeyAuth && !provider.APIKeyPresent || mode == agentconfig.ClaudeAuthTokenAuth && !provider.AuthTokenPresent
	readiness := configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
	if needs {
		readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
	}
	if view.CredentialReadiness != readiness {
		return configurationDraft{}, errConfigurationInvalid
	}
	data := &configurationDraftData{Provider: cloneConfigurationProvider(provider), NeedsCredential: needs, PreservedExtras: view.HasPreservedExtras}
	return configurationDraft{Asset: asset, private: func() *configurationDraftData { return data }}, nil
}

func configurationInput(data *configurationEditData) (*configurationv1.Draft, []*configurationv1.SecretMutation, error) {
	if !configurationText(data.Name, 128) || data.Name != strings.TrimSpace(data.Name) || !configurationProviderValid(data.Provider) {
		return nil, nil, errConfigurationInvalid
	}
	if data.Asset.ID != "" && (!data.Asset.Editable || data.Asset.AgentID != "claude" || data.Asset.Kind != "provider" || data.Asset.SchemaVersion != 1) {
		return nil, nil, errConfigurationUnsupported
	}
	if data.Asset.ID != "" && (!configurationID(data.Asset.ID) || data.Asset.Revision < 1) || data.Asset.ID == "" && data.Asset.Revision != 0 {
		return nil, nil, errConfigurationInvalid
	}
	mode := configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN
	if data.Provider.AuthMode == agentconfig.ClaudeAPIKeyAuth {
		mode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY
	}
	if data.Provider.AuthMode == agentconfig.ClaudeAuthTokenAuth {
		mode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN
	}
	wire := &configurationv1.ClaudeProvider{AuthMode: mode, Endpoint: data.Provider.Endpoint, Models: &configurationv1.ClaudeModels{DefaultModel: data.Provider.Models.Default, Haiku: data.Provider.Models.Haiku, Sonnet: data.Provider.Models.Sonnet, Opus: data.Provider.Models.Opus}}
	mutations := make([]*configurationv1.SecretMutation, 0, 2)
	for index, secret := range data.Secrets {
		slot := string(agentconfig.ClaudeAPIKeySecret)
		if index == 1 {
			slot = string(agentconfig.ClaudeAuthTokenSecret)
		}
		mutation := &configurationv1.SecretMutation{SlotId: slot}
		switch secret.Action {
		case configurationKeep:
			if data.Asset.ID == "" || len(secret.Replacement) != 0 {
				return nil, nil, errConfigurationInvalid
			}
			mutation.Action = &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}
		case configurationClear:
			if len(secret.Replacement) != 0 {
				return nil, nil, errConfigurationInvalid
			}
			mutation.Action = &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}
		case configurationReplace:
			if len(secret.Replacement) == 0 || len(secret.Replacement) > 8192 || !utf8.Valid(secret.Replacement) || strings.ContainsAny(string(secret.Replacement), "\x00\r\n") {
				return nil, nil, errConfigurationInvalid
			}
			mutation.Action = &configurationv1.SecretMutation_Replace{Replace: string(secret.Replacement)}
		default:
			return nil, nil, errConfigurationInvalid
		}
		mutations = append(mutations, mutation)
	}
	return &configurationv1.Draft{Name: data.Name, Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: wire}}, mutations, nil
}
