package grokclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"google.golang.org/protobuf/proto"
)

var ErrNotFound = errors.New("Grok asset not found")
var ErrLimit = errors.New("Grok asset client or service capacity exhausted")

type Metadata struct {
	private
	ID, Agent, Name                string
	Kind, Schema                   uint32
	Revision, CreatedAt, UpdatedAt int64
}
type OrderSnapshot struct {
	private
	Revision int64
	Assets   []Metadata
}
type Action uint8

const (
	Create Action = iota + 1
	Update
	Copy
	Delete
	ReplaceOrder
)

type Change struct {
	private
	Action                Action
	ID                    string
	ExpectedRevision      int64
	Name                  string
	ExpectedOrderRevision int64
	OrderIDs              []string
}
type Result struct {
	private
	Metadata *Metadata
	Order    *OrderSnapshot
	Deleted  bool
}
type Client struct {
	private
	state func() *clientState
}
type clientState struct {
	mutex                  sync.Mutex
	closed, busy, blocked  bool
	life                   context.Context
	cancel                 context.CancelFunc
	workers                sync.WaitGroup
	origin, session, owner string
	http                   *http.Client
	transport              *http.Transport
	buffers                wireBuffers
	operations             map[*operationState]struct{}
}
type View struct {
	private
	state func() *viewState
}
type viewState struct {
	mutex           sync.Mutex
	closed          bool
	metadata        Metadata
	draft           *configurationv1.Draft
	present, extras bool
	life            context.Context
	cancel          context.CancelFunc
	workers         sync.WaitGroup
}
type Operation struct {
	private
	state func() *operationState
}
type operationState struct {
	mutex             sync.Mutex
	closed, used      bool
	life              context.Context
	cancel            context.CancelFunc
	workers           sync.WaitGroup
	client            *Client
	change            Change
	input             *Input
	old               *View
	before            OrderSnapshot
	expected          *configurationv1.Draft
	present           bool
	origin, owner, id string
	expires           time.Time
}

func New(origin, session string, settings *http.Transport) (*Client, error) {
	address, err := parseOrigin(origin)
	if err != nil || !validSession(session) {
		return nil, ErrInvalid
	}
	httpClient, transport, err := newTransport(address, settings)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	state := &clientState{life: life, cancel: cancel, http: httpClient, transport: transport, origin: address.String(), session: session, operations: map[*operationState]struct{}{}}
	return &Client{state: func() *clientState { return state }}, nil
}

func (client *Client) start(caller context.Context) (context.Context, *clientState, func(), error) {
	if client == nil || client.state == nil || caller == nil {
		return nil, nil, nil, ErrClosed
	}
	state := client.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, nil, nil, ErrClosed
	}
	if state.busy {
		return nil, nil, nil, ErrBusy
	}
	if caller.Err() != nil {
		return nil, nil, nil, caller.Err()
	}
	deadline := time.Now().Add(20 * time.Second)
	if earlier, ok := caller.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	callerDone, lifeDone := make(chan struct{}), make(chan struct{})
	stopCaller := context.AfterFunc(caller, func() { cancel(); close(callerDone) })
	stopLife := context.AfterFunc(state.life, func() { cancel(); close(lifeDone) })
	if caller.Err() != nil || state.life.Err() != nil {
		cancel()
	}
	state.busy = true
	state.workers.Add(1)
	release := func() {
		cancel()
		if !stopCaller() {
			<-callerDone
		}
		if !stopLife() {
			<-lifeDone
		}
		state.buffers.clear()
		state.mutex.Lock()
		state.busy = false
		state.mutex.Unlock()
		state.workers.Done()
	}
	return ctx, state, release, nil
}
func (client *Client) Close() {
	if client == nil || client.state == nil {
		return
	}
	state := client.state()
	state.mutex.Lock()
	state.closed = true
	state.cancel()
	state.mutex.Unlock()
	state.workers.Wait()
	state.mutex.Lock()
	operations := make([]*operationState, 0, len(state.operations))
	for operation := range state.operations {
		operations = append(operations, operation)
	}
	state.session = ""
	state.owner = ""
	state.mutex.Unlock()
	for _, operation := range operations {
		(&Operation{state: func() *operationState { return operation }}).Close()
	}
	state.buffers.clear()
	state.transport.CloseIdleConnections()
}
func (state *clientState) authorized[Message any](message *Message) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+state.session)
	return request
}
func rpcFor(state *clientState) configurationv1connect.ConfigurationServiceClient {
	return configurationv1connect.NewConfigurationServiceClient(state.http, state.origin, connect.WithReadMaxBytes(1<<20), connect.WithSendMaxBytes(128<<10), connect.WithCodec(wireCodec{buffers: &state.buffers}))
}
func knownFailure(err error) error {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return ErrDenied
	case connect.CodeAborted:
		return ErrConflict
	case connect.CodeNotFound:
		return ErrNotFound
	case connect.CodeResourceExhausted:
		return ErrLimit
	case connect.CodeInvalidArgument:
		return ErrInvalid
	case connect.CodeUnimplemented:
		return ErrUnsupported
	case connect.CodeFailedPrecondition:
		if failure, ok := errors.AsType[*connect.Error](err); ok {
			if len(failure.Details()) == 0 {
				return ErrUnsupported
			}
			if len(failure.Details()) == 1 {
				detail := failure.Details()[0]
				data := detail.Bytes()
				value := new(packv1.AttachmentFailure)
				if len(data) <= 64 && detail.Type() == string(value.ProtoReflect().Descriptor().FullName()) && (wireCodec{}).Unmarshal(data, value) == nil && value.Reason == packv1.AttachmentFailureReason_ATTACHMENT_FAILURE_REASON_ASSET_IN_USE {
					return ErrInUse
				}
			}
		}
	}
	return nil
}
func readFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if known := knownFailure(err); known != nil {
		return known
	}
	return ErrUnavailable
}
func authenticate(ctx context.Context, state *clientState) error {
	rpc := authv1connect.NewAuthServiceClient(state.http, state.origin, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(4096), connect.WithCodec(wireCodec{buffers: &state.buffers}))
	response, err := rpc.GetSession(ctx, state.authorized(&authv1.GetSessionRequest{}))
	if err != nil {
		return readFailure(ctx, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if response == nil || response.Msg == nil || !validText(response.Msg.UserId, 128) {
		return ErrInvalid
	}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.owner != "" && state.owner != response.Msg.UserId {
		return ErrDenied
	}
	state.owner = response.Msg.UserId
	return nil
}
func metadataFrom(value *configurationv1.Asset) (Metadata, error) {
	if value == nil || !validID(value.Id) || !validText(value.AgentId, 64) || !validText(value.Name, 128) || value.Kind < 1 || value.Kind > 5 || value.SchemaVersion < 1 || value.SchemaVersion > 65535 || value.Revision < 1 || value.CreatedAtUnix <= 0 || value.UpdatedAtUnix <= 0 {
		return Metadata{}, ErrInvalid
	}
	for index, letter := range value.AgentId {
		if !(letter >= 'a' && letter <= 'z' || index > 0 && (letter >= '0' && letter <= '9' || letter == '-')) {
			return Metadata{}, ErrInvalid
		}
	}
	return Metadata{ID: value.Id, Agent: value.AgentId, Name: value.Name, Kind: uint32(value.Kind), Schema: value.SchemaVersion, Revision: value.Revision, CreatedAt: value.CreatedAtUnix, UpdatedAt: value.UpdatedAtUnix}, nil
}
func grokMetadata(metadata Metadata) bool { return metadata.Agent == "grokbuild" && metadata.Kind == 1 }
func getMetadata(ctx context.Context, state *clientState, id string) (Metadata, error) {
	response, err := rpcFor(state).GetAsset(ctx, state.authorized(&configurationv1.GetAssetRequest{Id: id}))
	if err != nil {
		return Metadata{}, readFailure(ctx, err)
	}
	if response == nil || response.Msg == nil {
		return Metadata{}, ErrInvalid
	}
	metadata, err := metadataFrom(response.Msg.Asset)
	if err != nil || metadata.ID != id {
		return Metadata{}, ErrInvalid
	}
	if !grokMetadata(metadata) {
		return Metadata{}, ErrUnsupported
	}
	if ctx.Err() != nil {
		return Metadata{}, ctx.Err()
	}
	return metadata, nil
}
func (client *Client) List(caller context.Context) ([]Metadata, error) {
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return nil, err
	}
	response, err := rpcFor(state).ListAssets(ctx, state.authorized(&configurationv1.ListAssetsRequest{}))
	if err != nil {
		return nil, readFailure(ctx, err)
	}
	if response == nil || response.Msg == nil || len(response.Msg.Assets) > 250 {
		return nil, ErrInvalid
	}
	result := make([]Metadata, 0, len(response.Msg.Assets))
	seen := map[string]bool{}
	for _, asset := range response.Msg.Assets {
		metadata, err := metadataFrom(asset)
		if err != nil || seen[metadata.ID] {
			return nil, ErrInvalid
		}
		seen[metadata.ID] = true
		if grokMetadata(metadata) {
			result = append(result, metadata)
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, nil
}
func (client *Client) Get(caller context.Context, id string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrInvalid
	}
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return Metadata{}, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return Metadata{}, err
	}
	return getMetadata(ctx, state, id)
}
func viewFrom(value *configurationv1.DraftView) (*View, error) {
	if value == nil {
		return nil, ErrInvalid
	}
	metadata, err := metadataFrom(value.Asset)
	if err != nil || !grokMetadata(metadata) || metadata.Schema != 1 || validateDraft(value.Draft) != nil || value.Draft.Name != metadata.Name || len(value.Secrets) != 1 || value.Secrets[0] == nil || value.Secrets[0].SlotId != keySlot {
		return nil, ErrInvalid
	}
	present := value.Secrets[0].Present
	if value.Draft.GetGrokbuildProvider().Mode == configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN && present {
		return nil, ErrInvalid
	}
	if value.CredentialReadiness != readiness(value.Draft, present) {
		return nil, ErrInvalid
	}
	state := &viewState{metadata: metadata, draft: proto.Clone(value.Draft).(*configurationv1.Draft), present: present, extras: value.HasPreservedExtras}
	state.life, state.cancel = context.WithCancel(context.Background())
	return &View{state: func() *viewState { return state }}, nil
}
func readiness(draft *configurationv1.Draft, present bool) configurationv1.CredentialReadiness {
	provider := draft.GetGrokbuildProvider()
	if provider.Mode == configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL && !present && provider.Model.EnvKey == nil {
		return configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
	}
	return configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
}
func readView(ctx context.Context, state *clientState, id string) (*View, error) {
	response, err := rpcFor(state).ReadDraft(ctx, state.authorized(&configurationv1.ReadDraftRequest{Id: id}))
	if err != nil {
		return nil, readFailure(ctx, err)
	}
	if response == nil || response.Msg == nil {
		return nil, ErrInvalid
	}
	view, err := viewFrom(response.Msg.View)
	if err != nil {
		return nil, err
	}
	if view.state().metadata.ID != id {
		view.Close()
		return nil, ErrInvalid
	}
	if ctx.Err() != nil {
		view.Close()
		return nil, ctx.Err()
	}
	return view, nil
}
func (client *Client) Read(caller context.Context, id string) (*View, error) {
	if !validID(id) {
		return nil, ErrInvalid
	}
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return nil, err
	}
	return readView(ctx, state, id)
}
func (view *View) Close() {
	if view == nil || view.state == nil {
		return
	}
	state := view.state()
	state.mutex.Lock()
	state.closed = true
	if state.cancel != nil {
		state.cancel()
	}
	state.mutex.Unlock()
	state.workers.Wait()
	state.mutex.Lock()
	state.draft = nil
	state.mutex.Unlock()
}
func (view *View) Metadata() Metadata {
	if view == nil || view.state == nil {
		return Metadata{}
	}
	state := view.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return Metadata{}
	}
	return state.metadata
}
func orderFrom(value *configurationv1.AssetOrderSnapshot) (OrderSnapshot, error) {
	if value == nil || value.AgentId != "grokbuild" || value.Kind != configurationv1.AssetKind_ASSET_KIND_PROVIDER || value.Revision < 0 || len(value.Assets) > 250 || value.Revision == 0 && len(value.Assets) > 0 {
		return OrderSnapshot{}, ErrInvalid
	}
	result := OrderSnapshot{Revision: value.Revision, Assets: make([]Metadata, 0, len(value.Assets))}
	seen := map[string]bool{}
	for _, asset := range value.Assets {
		metadata, err := metadataFrom(asset)
		if err != nil || !grokMetadata(metadata) || seen[metadata.ID] {
			return OrderSnapshot{}, ErrInvalid
		}
		seen[metadata.ID] = true
		result.Assets = append(result.Assets, metadata)
	}
	return result, nil
}
func getOrder(ctx context.Context, state *clientState) (OrderSnapshot, error) {
	response, err := rpcFor(state).GetAssetOrder(ctx, state.authorized(&configurationv1.GetAssetOrderRequest{AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER}))
	if err != nil {
		return OrderSnapshot{}, readFailure(ctx, err)
	}
	if response == nil || response.Msg == nil {
		return OrderSnapshot{}, ErrInvalid
	}
	result, err := orderFrom(response.Msg.Snapshot)
	if err != nil {
		return OrderSnapshot{}, err
	}
	if ctx.Err() != nil {
		return OrderSnapshot{}, ctx.Err()
	}
	return result, nil
}
func (client *Client) GetOrder(caller context.Context) (OrderSnapshot, error) {
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return OrderSnapshot{}, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return OrderSnapshot{}, err
	}
	return getOrder(ctx, state)
}
func validChange(change Change, input *Input) bool {
	if change.Action < Create || change.Action > ReplaceOrder {
		return false
	}
	if change.Action == ReplaceOrder {
		return input == nil && change.ID == "" && change.Name == "" && change.ExpectedRevision == 0 && change.ExpectedOrderRevision >= 0 && validIDs(change.OrderIDs)
	}
	if change.ExpectedOrderRevision != 0 || change.OrderIDs != nil {
		return false
	}
	if change.Action == Create {
		return input != nil && change.ID == "" && change.Name == "" && change.ExpectedRevision == 0
	}
	if !validID(change.ID) || change.ExpectedRevision < 1 {
		return false
	}
	if change.Action == Copy {
		return input == nil && validText(strings.TrimSpace(change.Name), 128)
	}
	if change.Name != "" {
		return false
	}
	if change.Action == Update {
		return input != nil && change.ExpectedRevision < math.MaxInt64
	}
	return input == nil
}
func orderIDs(snapshot OrderSnapshot) []string {
	result := make([]string, 0, len(snapshot.Assets))
	for _, asset := range snapshot.Assets {
		result = append(result, asset.ID)
	}
	return result
}
func (client *Client) Prepare(caller context.Context, change Change, input *Input) (*Operation, error) {
	if !validChange(change, input) {
		return nil, ErrInvalid
	}
	change.OrderIDs = slices.Clone(change.OrderIDs)
	var owned *Input
	var err error
	if input != nil {
		owned, err = cloneInput(input)
		if err != nil {
			return nil, err
		}
	}
	retained := false
	defer func() {
		if !retained && owned != nil {
			owned.Close()
		}
	}()
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	state.mutex.Lock()
	blocked := state.blocked
	full := len(state.operations) >= 8
	state.mutex.Unlock()
	if blocked {
		return nil, ErrUnknown
	}
	if full {
		return nil, ErrLimit
	}
	if err := authenticate(ctx, state); err != nil {
		return nil, err
	}
	operation := &operationState{client: client, change: change, input: owned, origin: state.origin, owner: state.owner, id: rand.Text(), expires: time.Now().Add(time.Minute)}
	if err := operation.prepare(ctx, state); err != nil {
		if operation.old != nil {
			operation.old.Close()
		}
		return nil, err
	}
	if ctx.Err() != nil {
		if operation.old != nil {
			operation.old.Close()
		}
		return nil, ctx.Err()
	}
	operation.life, operation.cancel = context.WithCancel(context.Background())
	state.mutex.Lock()
	if state.closed {
		state.mutex.Unlock()
		operation.cancel()
		if operation.old != nil {
			operation.old.Close()
		}
		return nil, ErrClosed
	}
	state.operations[operation] = struct{}{}
	state.mutex.Unlock()
	retained = true
	return &Operation{state: func() *operationState { return operation }}, nil
}
func (operation *operationState) prepare(ctx context.Context, state *clientState) error {
	change := operation.change
	if change.Action == ReplaceOrder {
		before, err := getOrder(ctx, state)
		if err != nil {
			return err
		}
		if before.Revision != change.ExpectedOrderRevision || len(before.Assets) != len(change.OrderIDs) {
			return ErrConflict
		}
		membership := map[string]bool{}
		for _, asset := range before.Assets {
			membership[asset.ID] = true
		}
		for _, id := range change.OrderIDs {
			if !membership[id] {
				return ErrConflict
			}
		}
		if before.Revision == math.MaxInt64 && !slices.Equal(orderIDs(before), change.OrderIDs) {
			return ErrInvalid
		}
		operation.before = before
		return nil
	}
	if change.Action == Delete {
		metadata, err := getMetadata(ctx, state, change.ID)
		if err != nil {
			return err
		}
		if metadata.Revision != change.ExpectedRevision {
			return ErrConflict
		}
		viewData := &viewState{metadata: metadata}
		operation.old = &View{state: func() *viewState { return viewData }}
		return nil
	}
	if change.Action != Create {
		view, err := readView(ctx, state, change.ID)
		if err != nil {
			return err
		}
		operation.old = view
		if view.state().metadata.Revision != change.ExpectedRevision {
			return ErrConflict
		}
		if view.state().extras {
			return ErrUnsupported
		}
	}
	if change.Action == Copy {
		operation.expected = proto.Clone(operation.old.state().draft).(*configurationv1.Draft)
		operation.expected.Name = strings.TrimSpace(change.Name)
		return nil
	}
	input := operation.input.state()
	if change.Action == Create && input.action == KeepKey {
		return ErrInvalid
	}
	present := operation.old != nil && operation.old.state().present
	switch input.action {
	case ReplaceKey:
		present = true
	case ClearKey:
		present = false
	}
	if input.draft.GetGrokbuildProvider().Mode == configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN && present {
		return ErrInvalid
	}
	operation.expected = proto.Clone(input.draft).(*configurationv1.Draft)
	operation.expected.Name = strings.TrimSpace(input.draft.Name)
	operation.present = present
	return nil
}
func (operation *Operation) Close() {
	if operation == nil || operation.state == nil {
		return
	}
	state := operation.state()
	state.mutex.Lock()
	state.closed = true
	if state.cancel != nil {
		state.cancel()
	}
	state.mutex.Unlock()
	state.workers.Wait()
	state.mutex.Lock()
	if state.input != nil {
		state.input.Close()
	}
	if state.old != nil {
		state.old.Close()
	}
	state.expected = nil
	state.mutex.Unlock()
	client := state.client.state()
	client.mutex.Lock()
	delete(client.operations, state)
	client.mutex.Unlock()
}
func (operation *Operation) ReviewID() string {
	if operation == nil || operation.state == nil {
		return ""
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return ""
	}
	return state.id
}
func actionLabel(action Action) string {
	return []string{"", "create", "update", "copy", "delete", "order-replace"}[action]
}
func (operation *Operation) Confirmation() string {
	if operation == nil || operation.state == nil {
		return ""
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return ""
	}
	return "grok " + actionLabel(state.change.Action) + " " + state.id
}
func (operation *Operation) KeyAction() KeyAction {
	if operation == nil || operation.state == nil {
		return NoKeyMutation
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed || state.input == nil {
		return NoKeyMutation
	}
	return state.input.state().action
}

func (operation *Operation) Submit(caller context.Context) (Result, error) {
	if operation == nil || operation.state == nil || caller == nil {
		return Result{}, ErrInvalid
	}
	op := operation.state()
	op.mutex.Lock()
	if op.closed || op.used {
		op.mutex.Unlock()
		return Result{}, ErrClosed
	}
	op.used = true
	if !time.Now().Before(op.expires) {
		if op.input != nil {
			op.input.Close()
		}
		op.mutex.Unlock()
		return Result{}, ErrExpired
	}
	op.workers.Add(1)
	op.mutex.Unlock()
	defer func() {
		if op.input != nil {
			op.input.Close()
		}
		op.workers.Done()
	}()
	ctx, state, release, err := op.client.start(caller)
	if err != nil {
		return Result{}, err
	}
	defer release()
	ctx, cancel := context.WithDeadline(ctx, op.expires)
	defer cancel()
	done := make(chan struct{})
	stop := context.AfterFunc(op.life, func() { cancel(); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
	}()
	if op.life.Err() != nil {
		cancel()
	}
	state.mutex.Lock()
	blocked := state.blocked
	state.mutex.Unlock()
	if blocked {
		return Result{}, ErrUnknown
	}
	if err := authenticate(ctx, state); err != nil {
		if !time.Now().Before(op.expires) {
			return Result{}, ErrExpired
		}
		return Result{}, err
	}
	if state.owner != op.owner {
		return Result{}, ErrDenied
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if !time.Now().Before(op.expires) {
		return Result{}, ErrExpired
	}
	result, err := op.publish(ctx, state)
	if err == nil && ctx.Err() == nil {
		return result, nil
	}
	if ctx.Err() == nil {
		if known := knownFailure(err); known != nil {
			return Result{}, known
		}
	}
	state.mutex.Lock()
	state.blocked = true
	state.mutex.Unlock()
	return Result{}, ErrUnknown
}
func (operation *operationState) publish(ctx context.Context, state *clientState) (Result, error) {
	rpc := rpcFor(state)
	change := operation.change
	if change.Action == Delete {
		response, err := rpc.DeleteAsset(ctx, state.authorized(&configurationv1.DeleteAssetRequest{Id: change.ID, ExpectedRevision: change.ExpectedRevision}))
		if err != nil {
			return Result{}, err
		}
		if response == nil || response.Msg == nil {
			return Result{}, ErrInvalid
		}
		metadata := operation.old.state().metadata
		return Result{Metadata: &metadata, Deleted: true}, nil
	}
	if change.Action == ReplaceOrder {
		response, err := rpc.ReplaceAssetOrder(ctx, state.authorized(&configurationv1.ReplaceAssetOrderRequest{AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, ExpectedRevision: change.ExpectedOrderRevision, AssetIds: slices.Clone(change.OrderIDs)}))
		if err != nil {
			return Result{}, err
		}
		if response == nil || response.Msg == nil {
			return Result{}, ErrInvalid
		}
		order, err := orderFrom(response.Msg.Snapshot)
		if err != nil {
			return Result{}, ErrInvalid
		}
		expected := operation.before.Revision
		if !slices.Equal(orderIDs(operation.before), change.OrderIDs) {
			expected++
		}
		if order.Revision != expected || !slices.Equal(orderIDs(order), change.OrderIDs) {
			return Result{}, ErrInvalid
		}
		return Result{Order: &order}, nil
	}
	var responseView *configurationv1.DraftView
	var err error
	switch change.Action {
	case Copy:
		response, callErr := rpc.CopyAsset(ctx, state.authorized(&configurationv1.CopyAssetRequest{SourceId: change.ID, ExpectedSourceRevision: change.ExpectedRevision, Name: change.Name}))
		err = callErr
		if response != nil && response.Msg != nil {
			responseView = response.Msg.View
		}
	case Create, Update:
		input := operation.input.state()
		var mutations []*configurationv1.SecretMutation
		if input.action != NoKeyMutation {
			mutation := &configurationv1.SecretMutation{SlotId: keySlot}
			switch input.action {
			case KeepKey:
				mutation.Action = &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}
			case ClearKey:
				mutation.Action = &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}
			case ReplaceKey:
				mutation.Action = &configurationv1.SecretMutation_Replace{Replace: string(input.key)}
			}
			mutations = []*configurationv1.SecretMutation{mutation}
			defer func() { mutation.Action = nil }()
		}
		draft := proto.Clone(input.draft).(*configurationv1.Draft)
		if change.Action == Create {
			response, callErr := rpc.CreateAsset(ctx, state.authorized(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: mutations}))
			err = callErr
			if response != nil && response.Msg != nil {
				responseView = response.Msg.View
			}
		} else {
			response, callErr := rpc.UpdateAsset(ctx, state.authorized(&configurationv1.UpdateAssetRequest{Id: change.ID, ExpectedRevision: change.ExpectedRevision, Draft: draft, Secrets: mutations}))
			err = callErr
			if response != nil && response.Msg != nil {
				responseView = response.Msg.View
			}
		}
	}
	if err != nil {
		return Result{}, err
	}
	view, err := viewFrom(responseView)
	if err != nil {
		return Result{}, ErrInvalid
	}
	defer view.Close()
	value := view.state()
	revision := int64(1)
	if change.Action == Update {
		revision = change.ExpectedRevision + 1
	}
	if value.extras || value.metadata.Revision != revision || !proto.Equal(value.draft, operation.expected) || value.present != operation.present || change.Action == Update && value.metadata.ID != change.ID || change.Action == Copy && value.metadata.ID == change.ID {
		return Result{}, ErrInvalid
	}
	metadata := value.metadata
	return Result{Metadata: &metadata}, nil
}

func writeSummary(ctx context.Context, writer io.Writer, text string) error {
	if ctx == nil || writer == nil || len(text) > 64<<10 {
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var count int
	var err error
	switch output := writer.(type) {
	case interface {
		WriteContext(context.Context, []byte) (int, error)
	}:
		count, err = output.WriteContext(ctx, []byte(text))
	case *bytes.Buffer:
		count, err = output.WriteString(text)
	default:
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || count != len(text) {
		return ErrUnavailable
	}
	return nil
}
func summaryContext(caller, life context.Context, expires time.Time) (context.Context, func()) {
	deadline := min(time.Now().Add(20*time.Second).UnixNano(), expires.UnixNano())
	if earlier, ok := caller.Deadline(); ok {
		deadline = min(deadline, earlier.UnixNano())
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, deadline))
	callerDone, lifeDone := make(chan struct{}), make(chan struct{})
	stopCaller := context.AfterFunc(caller, func() { cancel(); close(callerDone) })
	stopLife := context.AfterFunc(life, func() { cancel(); close(lifeDone) })
	if caller.Err() != nil || life.Err() != nil {
		cancel()
	}
	return ctx, func() {
		cancel()
		if !stopCaller() {
			<-callerDone
		}
		if !stopLife() {
			<-lifeDone
		}
	}
}
func quoted(value string) string { return strconv.QuoteToASCII(value) }
func describeDraft(output *bytes.Buffer, label string, draft *configurationv1.Draft, present bool) {
	if draft == nil {
		return
	}
	provider := draft.GetGrokbuildProvider()
	ready := "complete"
	if readiness(draft, present) == configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL {
		ready = "needs-credential"
	}
	fmt.Fprintf(output, "%s name=%s mode=%s key-present=%t readiness=%s\n", label, quoted(draft.Name), provider.Mode.String(), present, ready)
	if model := provider.Model; model != nil {
		env := "<absent>"
		if model.EnvKey != nil {
			env = quoted(*model.EnvKey)
		}
		fmt.Fprintf(output, "profile=%s upstreamModel=%s endpoint=%s provider-name=%s apiBackend=%s contextWindow=%d envKey=%s\n", quoted(model.Profile), quoted(model.UpstreamModel), quoted(model.Endpoint), quoted(model.Name), quoted(model.ApiBackend), model.ContextWindow, env)
	}
}
func (view *View) WriteSummary(ctx context.Context, writer io.Writer) error {
	if ctx == nil {
		return ErrInvalid
	}
	if view == nil || view.state == nil {
		return ErrClosed
	}
	state := view.state()
	state.mutex.Lock()
	if state.closed {
		state.mutex.Unlock()
		return ErrClosed
	}
	state.workers.Add(1)
	var output bytes.Buffer
	fmt.Fprintf(&output, "GROK ASSET id=%s revision=%d extras=%t native-applied=false\n", state.metadata.ID, state.metadata.Revision, state.extras)
	describeDraft(&output, "CURRENT", state.draft, state.present)
	state.mutex.Unlock()
	defer state.workers.Done()
	ctx, release := summaryContext(ctx, state.life, time.Now().Add(20*time.Second))
	defer release()
	return writeSummary(ctx, writer, output.String())
}
func (operation *Operation) WriteSummary(ctx context.Context, writer io.Writer) error {
	if ctx == nil {
		return ErrInvalid
	}
	if operation == nil || operation.state == nil {
		return ErrClosed
	}
	state := operation.state()
	state.mutex.Lock()
	if state.closed || state.used {
		state.mutex.Unlock()
		return ErrClosed
	}
	if !time.Now().Before(state.expires) {
		state.mutex.Unlock()
		return ErrExpired
	}
	state.workers.Add(1)
	var output bytes.Buffer
	fmt.Fprintf(&output, "GROK REVIEW BEGIN action=%s review-id=%s\norigin=%s owner=%s expires=%s\n", actionLabel(state.change.Action), state.id, quoted(state.origin), quoted(state.owner), state.expires.UTC().Format(time.RFC3339Nano))
	if state.old != nil {
		old := state.old.state()
		fmt.Fprintf(&output, "source-id=%s expected-revision=%d source-name=%s schema=%d\n", old.metadata.ID, state.change.ExpectedRevision, quoted(old.metadata.Name), old.metadata.Schema)
		describeDraft(&output, "BEFORE", old.draft, old.present)
	}
	if state.input != nil {
		input := state.input.state()
		label := []string{"keep (omitted)", "keep", "replace", "clear"}[input.action]
		if input.action == NoKeyMutation && state.change.Action == Create {
			label = "none (absent)"
		}
		fmt.Fprintf(&output, "key-action=%s (no key value/hash/length shown)\nrequested-name=%s expected-metadata-name=%s\n", label, quoted(input.draft.Name), quoted(state.expected.Name))
	}
	describeDraft(&output, "AFTER", state.expected, state.present)
	if state.change.Action == ReplaceOrder {
		fmt.Fprintf(&output, "order expected-revision=%d\n", state.change.ExpectedOrderRevision)
		fmt.Fprintf(&output, "BEFORE IDs=%s\nAFTER IDs=%s\n", strings.Join(orderIDs(state.before), ","), strings.Join(state.change.OrderIDs, ","))
		fmt.Fprintln(&output, "Order approves membership/order only, not asset contents.")
	}
	fmt.Fprintln(&output, "New draft has NOT been server-validated; no mutation submitted; no native application. Readiness is predicted until acknowledged. Review ID is local, not a server receipt/idempotency key.")
	fmt.Fprintf(&output, "GROK REVIEW END action=%s review-id=%s\n", actionLabel(state.change.Action), state.id)
	state.mutex.Unlock()
	defer state.workers.Done()
	ctx, release := summaryContext(ctx, state.life, state.expires)
	defer release()
	return writeSummary(ctx, writer, output.String())
}
