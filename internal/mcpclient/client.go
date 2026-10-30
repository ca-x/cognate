package mcpclient

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var ErrUnavailable = errors.New("MCP service unavailable or refused; no automatic retry")
var ErrConflict = errors.New("MCP asset revision changed; read and review again")
var ErrDenied = errors.New("MCP session or operation denied")
var ErrBusy = errors.New("MCP client already has an active request")

type Client struct {
	private
	state func() *clientState
}
type clientState struct {
	mutex      sync.Mutex
	closed     bool
	busy       bool
	workers    sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	http       *http.Client
	owned      *http.Transport
	base       string
	session    string
	owner      string
	blocked    bool
	buffers    [][]byte
	operations map[*Operation]struct{}
}

func New(origin, session string, supplied *http.Client) (*Client, error) {
	address, err := parseOrigin(origin)
	if err != nil || !validSession(session) {
		return nil, ErrInvalid
	}
	transport, owned := newTransport(address, supplied)
	ctx, cancel := context.WithCancel(context.Background())
	state := &clientState{ctx: ctx, cancel: cancel, http: transport, owned: owned, base: address.String(), session: session, operations: make(map[*Operation]struct{})}
	return &Client{state: func() *clientState { return state }}, nil
}

func (client *Client) start(caller context.Context) (context.Context, *clientState, func(), error) {
	if caller == nil || client == nil || client.state == nil {
		return nil, nil, nil, ErrClosed
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, safeContext(err)
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
	state.busy = true
	state.workers.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	callerDone, clientDone := make(chan struct{}), make(chan struct{})
	stopCaller := context.AfterFunc(caller, func() { cancel(); close(callerDone) })
	stopClient := context.AfterFunc(state.ctx, func() { cancel(); close(clientDone) })
	if caller.Err() != nil || state.ctx.Err() != nil {
		cancel()
	}
	release := func() {
		cancel()
		if !stopCaller() {
			<-callerDone
		}
		if !stopClient() {
			<-clientDone
		}
		state.mutex.Lock()
		for _, buffer := range state.buffers {
			clear(buffer)
		}
		state.buffers = nil
		state.busy = false
		state.mutex.Unlock()
		state.workers.Done()
	}
	return ctx, state, release, nil
}

func authenticate(ctx context.Context, state *clientState) error {
	rpc := authv1connect.NewAuthServiceClient(state.http, state.base, connect.WithAcceptCompression("gzip", nil, nil), connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(4096))
	request := connect.NewRequest(&authv1.GetSessionRequest{})
	request.Header().Set("Authorization", "Bearer "+state.session)
	response, err := rpc.GetSession(ctx, request)
	if err != nil {
		return requestError(ctx, err)
	}
	if ctx.Err() != nil {
		return safeContext(ctx.Err())
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

func (client *Client) Authenticate(caller context.Context) error {
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return err
	}
	defer release()
	return authenticate(ctx, state)
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return safeContext(ctx.Err())
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return ErrDenied
	case connect.CodeAborted, connect.CodeFailedPrecondition:
		return ErrConflict
	case connect.CodeInvalidArgument, connect.CodeDataLoss, connect.CodeResourceExhausted:
		return ErrInvalid
	default:
		return ErrUnavailable
	}
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
	state.session = ""
	state.owner = ""
	operations := make([]*Operation, 0, len(state.operations))
	for operation := range state.operations {
		operations = append(operations, operation)
	}
	state.mutex.Unlock()
	for _, operation := range operations {
		operation.Close()
	}
	if state.owned != nil {
		state.owned.CloseIdleConnections()
	}
}

type Metadata struct {
	ID        string
	Name      string
	Agent     string
	Schema    uint32
	Revision  int64
	CreatedAt int64
	UpdatedAt int64
}

func (metadata Metadata) Editable() bool { return metadata.Agent == "claude" && metadata.Schema == 1 }

func validID(value string) bool {
	if len(value) != 26 {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z' || character >= '2' && character <= '7') {
			return false
		}
	}
	return true
}

func metadataFrom(value *configurationv1.Asset) (Metadata, error) {
	if value == nil || !validID(value.Id) || value.Kind != configurationv1.AssetKind_ASSET_KIND_MCP || !validText(value.Name, 128) || value.Revision < 1 || value.SchemaVersion < 1 || value.SchemaVersion > 65535 || value.CreatedAtUnix <= 0 || value.UpdatedAtUnix <= 0 || len(value.AgentId) > 64 || !agentIdentifier(value.AgentId) {
		return Metadata{}, ErrInvalid
	}
	return Metadata{ID: value.Id, Name: value.Name, Agent: value.AgentId, Schema: value.SchemaVersion, Revision: value.Revision, CreatedAt: value.CreatedAtUnix, UpdatedAt: value.UpdatedAtUnix}, nil
}

func agentIdentifier(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
			return false
		}
	}
	return true
}

func rpcFor(state *clientState) mcpv1connect.MCPConfigurationServiceClient {
	return mcpv1connect.NewMCPConfigurationServiceClient(state.http, state.base, connect.WithAcceptCompression("gzip", nil, nil), connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(2<<20), connect.WithCodec(mcpCodec{state: state}))
}

func (state *clientState) authorized[Message any](message *Message) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+state.session)
	return request
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
	response, err := rpcFor(state).ListAssets(ctx, state.authorized(&mcpv1.ListAssetsRequest{}))
	if err != nil {
		return nil, requestError(ctx, err)
	}
	if ctx.Err() != nil {
		return nil, safeContext(ctx.Err())
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
		result = append(result, metadata)
	}
	return result, nil
}

func getMetadata(ctx context.Context, state *clientState, id string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrInvalid
	}
	response, err := rpcFor(state).GetAsset(ctx, state.authorized(&mcpv1.GetAssetRequest{Id: id}))
	if err != nil {
		return Metadata{}, requestError(ctx, err)
	}
	if ctx.Err() != nil {
		return Metadata{}, safeContext(ctx.Err())
	}
	if response == nil || response.Msg == nil {
		return Metadata{}, ErrInvalid
	}
	metadata, err := metadataFrom(response.Msg.Asset)
	if err != nil || metadata.ID != id {
		return Metadata{}, ErrInvalid
	}
	return metadata, nil
}

func (client *Client) Get(caller context.Context, id string) (Metadata, error) {
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

type View struct {
	private
	state func() *viewState
}
type viewState struct {
	metadata  Metadata
	draft     draft
	refs      map[string]string
	presence  map[string]bool
	readonly  bool
	readiness mcpv1.Readiness
}

func (view *View) Metadata() Metadata {
	if view == nil || view.state == nil {
		return Metadata{}
	}
	return view.state().metadata
}
func (view *View) WriteSummary(ctx context.Context, writer io.Writer) error {
	if view == nil || view.state == nil || writer == nil {
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return safeContext(ctx.Err())
	}
	state := view.state()
	if _, err := fmt.Fprintf(writer, "MCP asset %q revision=%d target=%q schema=%d readonly=%t readiness=%d\n", state.metadata.ID, state.metadata.Revision, state.metadata.Agent, state.metadata.Schema, state.readonly, state.readiness); err != nil {
		return ErrInvalid
	}
	for _, ref := range orderedRefs(state.draft) {
		if _, err := fmt.Fprintf(writer, "SLOT %q present=%t\n", ref, state.presence[ref]); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

func orderedRefs(value draft) []string {
	result := []string{}
	if value.Document == nil {
		return result
	}
	for _, server := range value.Document.Servers {
		if server.Stdio != nil {
			if server.Stdio.Args != nil {
				for _, arg := range server.Stdio.Args.Values {
					result = append(result, arg.SlotRef)
				}
			}
			if server.Stdio.Env != nil {
				for _, entry := range server.Stdio.Env.Values {
					result = append(result, entry.SlotRef)
				}
			}
		}
		for _, remote := range []*remote{server.HTTP, server.SSE} {
			if remote != nil && remote.Headers != nil {
				for _, entry := range remote.Headers.Values {
					result = append(result, entry.SlotRef)
				}
			}
		}
	}
	return result
}

func readView(value *mcpv1.View) (*View, error) {
	if value == nil {
		return nil, ErrInvalid
	}
	metadata, err := metadataFrom(value.Asset)
	if err != nil || !metadata.Editable() || value.Draft == nil || value.Draft.Name != metadata.Name || value.Draft.AgentId != metadata.Agent {
		return nil, ErrInvalid
	}
	encoded, err := protojson.Marshal(value.Draft)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(encoded)
	var declaration draft
	if strictJSON(encoded, &declaration) != nil {
		return nil, ErrInvalid
	}
	refs, err := validateDraft(&declaration)
	if err != nil || len(refs) != len(value.Secrets) {
		return nil, ErrInvalid
	}
	presence := map[string]bool{}
	readiness := mcpv1.Readiness_READINESS_COMPLETE
	for _, status := range value.Secrets {
		if status == nil || status.Present == nil || refs[status.SlotId] == "" {
			return nil, ErrInvalid
		}
		if _, seen := presence[status.SlotId]; seen {
			return nil, ErrInvalid
		}
		presence[status.SlotId] = *status.Present
		if !*status.Present {
			readiness = mcpv1.Readiness_READINESS_NEEDS_CREDENTIAL
		}
	}
	if value.Readiness != readiness {
		return nil, ErrInvalid
	}
	state := &viewState{metadata: metadata, draft: declaration, refs: refs, presence: presence, readiness: readiness, readonly: value.HasPreservedExtras}
	return &View{state: func() *viewState { return state }}, nil
}

func readDraft(ctx context.Context, state *clientState, id string) (*View, error) {
	if !validID(id) {
		return nil, ErrInvalid
	}
	response, err := rpcFor(state).ReadDraft(ctx, state.authorized(&mcpv1.ReadDraftRequest{Id: id}))
	if err != nil {
		return nil, requestError(ctx, err)
	}
	if ctx.Err() != nil {
		return nil, safeContext(ctx.Err())
	}
	if response == nil || response.Msg == nil {
		return nil, ErrInvalid
	}
	view, err := readView(response.Msg.View)
	if err != nil || view.Metadata().ID != id {
		return nil, ErrInvalid
	}
	return view, nil
}

func (client *Client) Read(caller context.Context, id string) (*View, error) {
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return nil, err
	}
	metadata, err := getMetadata(ctx, state, id)
	if err != nil {
		return nil, err
	}
	if !metadata.Editable() {
		data := &viewState{metadata: metadata, readonly: true}
		return &View{state: func() *viewState { return data }}, nil
	}
	return readDraft(ctx, state, id)
}

func cloneInput(input *Input) (*Input, error) {
	if input == nil || input.state == nil {
		return nil, ErrInvalid
	}
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, ErrClosed
	}
	data, err := json.Marshal(state.draft)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(data)
	copyState := &inputState{}
	if strictJSON(data, &copyState.draft) != nil {
		return nil, ErrInvalid
	}
	copyState.refs, err = validateDraft(&copyState.draft)
	if err != nil {
		return nil, err
	}
	copyState.mutations = make([]mutation, len(state.mutations))
	for index, value := range state.mutations {
		copyState.mutations[index] = value
		copyState.mutations[index].value = bytes.Clone(value.value)
	}
	return &Input{state: func() *inputState { return copyState }}, nil
}

func wireInput(input *Input) (*mcpv1.Draft, []*mcpv1.SecretMutation, error) {
	state := input.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return nil, nil, ErrClosed
	}
	data, err := json.Marshal(state.draft)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	defer clear(data)
	declaration := &mcpv1.Draft{}
	if protojson.Unmarshal(data, declaration) != nil {
		return nil, nil, ErrInvalid
	}
	values := make([]*mcpv1.SecretMutation, 0, len(state.mutations))
	for _, value := range state.mutations {
		entry := &mcpv1.SecretMutation{SlotId: value.SlotID}
		switch {
		case value.Keep != nil:
			entry.Action = &mcpv1.SecretMutation_Keep{Keep: &mcpv1.Keep{}}
		case value.Replace != nil:
			entry.Action = &mcpv1.SecretMutation_Replace{Replace: append([]byte{}, value.value...)}
		case value.Clear != nil:
			entry.Action = &mcpv1.SecretMutation_Clear{Clear: &mcpv1.Clear{Confirmed: new(true)}}
		default:
			clearWire(values)
			return nil, nil, ErrInvalid
		}
		values = append(values, entry)
	}
	return declaration, values, nil
}

func clearWire(values []*mcpv1.SecretMutation) {
	for _, value := range values {
		if value != nil {
			clear(value.GetReplace())
			value.Action = nil
		}
	}
	clear(values)
}

type Preview struct {
	private
	state func() *previewState
}
type previewState struct{ view *mcpv1.PreviewResponse }

func (preview *Preview) WriteSummary(ctx context.Context, writer io.Writer) error {
	if preview == nil || preview.state == nil || writer == nil {
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return safeContext(ctx.Err())
	}
	value := preview.state().view
	if _, err := fmt.Fprintf(writer, "LOGICAL PREVIEW can_apply=false applied=false readiness=%d warnings=%v\n", value.Readiness, value.Warnings); err != nil {
		return ErrInvalid
	}
	for _, operation := range value.Operations {
		if _, err := fmt.Fprintf(writer, "SERVER %q native=%q action=%d managed-proof=true\n", operation.ServerId, operation.NativeName, operation.Action); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

func previewDraft(ctx context.Context, state *clientState, id string, revision int64, input *Input) (*Preview, error) {
	declaration, values, err := wireInput(input)
	if err != nil {
		return nil, err
	}
	defer clearWire(values)
	request := &mcpv1.PreviewRequest{Id: id, ExpectedRevision: revision, Draft: declaration, Secrets: values}
	response, err := rpcFor(state).Preview(ctx, state.authorized(request))
	if err != nil {
		return nil, requestError(ctx, err)
	}
	if ctx.Err() != nil {
		return nil, safeContext(ctx.Err())
	}
	if response == nil || response.Msg == nil {
		return nil, ErrInvalid
	}
	value := response.Msg
	if value.CanApply || value.Applied || value.Readiness != mcpv1.Readiness_READINESS_COMPLETE && value.Readiness != mcpv1.Readiness_READINESS_NEEDS_CREDENTIAL || len(value.Operations) != len(declaration.Document.Servers) {
		return nil, ErrInvalid
	}
	for index, server := range declaration.Document.Servers {
		operation := value.Operations[index]
		if operation == nil || operation.ServerId != server.Id || operation.NativeName != server.Name || !operation.RequiresManagedProof {
			return nil, ErrInvalid
		}
		if *server.Enabled {
			if operation.Action != mcpv1.Action_ACTION_UPSERT_MANAGED || !proto.Equal(operation.Desired, server) {
				return nil, ErrInvalid
			}
		} else if operation.Action != mcpv1.Action_ACTION_DISABLE_MANAGED || operation.Desired != nil {
			return nil, ErrInvalid
		}
	}
	seen := map[mcpv1.Warning]bool{}
	for _, warning := range value.Warnings {
		if warning != mcpv1.Warning_WARNING_INSECURE_HTTP && warning != mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED || seen[warning] {
			return nil, ErrInvalid
		}
		seen[warning] = true
	}
	if !seen[mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED] {
		return nil, ErrInvalid
	}
	data := &previewState{view: value}
	return &Preview{state: func() *previewState { return data }}, nil
}

func (client *Client) Preview(caller context.Context, id string, revision int64, input *Input) (*Preview, error) {
	snapshot, err := cloneInput(input)
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return nil, err
	}
	if id == "" {
		if revision != 0 || snapshot.ValidateUpdate(nil) != nil {
			return nil, ErrInvalid
		}
	} else {
		view, err := readDraft(ctx, state, id)
		if err != nil {
			return nil, err
		}
		if view.Metadata().Revision != revision {
			return nil, ErrConflict
		}
		if view.state().readonly {
			return nil, ErrInvalid
		}
		if validateMutations(snapshot.state().mutations, snapshot.state().refs, view.state().refs, true) != nil {
			return nil, ErrInvalid
		}
	}
	return previewDraft(ctx, state, id, revision, snapshot)
}

type Change struct {
	Action   string
	ID       string
	Revision int64
	Name     string
}
type Operation struct {
	private
	state func() *operationState
}
type operationState struct {
	mutex         sync.Mutex
	closed, used  bool
	change        Change
	client        *Client
	input         *Input
	metadata      Metadata
	preview       *Preview
	origin        string
	presence      map[string]bool
	copyReview    *Input
	previousOrder []string
}

func (client *Client) Prepare(caller context.Context, change Change, input *Input) (*Operation, error) {
	if change.Action != "create" && change.Action != "update" && change.Action != "copy" && change.Action != "delete" {
		return nil, ErrInvalid
	}
	if change.Action == "create" {
		if change.ID != "" || change.Revision != 0 || change.Name != "" {
			return nil, ErrInvalid
		}
	} else if !validID(change.ID) || change.Revision < 1 {
		return nil, ErrInvalid
	}
	if change.Action == "copy" {
		if !validText(change.Name, 128) {
			return nil, ErrInvalid
		}
	} else if change.Name != "" {
		return nil, ErrInvalid
	}
	var snapshot *Input
	var copyReview *Input
	if change.Action == "create" || change.Action == "update" {
		var err error
		snapshot, err = cloneInput(input)
		if err != nil {
			return nil, err
		}
	} else if input != nil {
		return nil, ErrInvalid
	}
	owned := false
	defer func() {
		if !owned {
			if snapshot != nil {
				snapshot.Close()
			}
			if copyReview != nil {
				copyReview.Close()
			}
		}
	}()
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return nil, err
	}
	defer release()
	state.mutex.Lock()
	blocked := state.blocked
	state.mutex.Unlock()
	if blocked {
		return nil, ErrUnknown
	}
	if err := authenticate(ctx, state); err != nil {
		return nil, err
	}
	prepared := &operationState{change: change, client: client, input: snapshot, origin: state.base, presence: map[string]bool{}}
	if change.Action != "create" {
		metadata, err := getMetadata(ctx, state, change.ID)
		if err != nil {
			return nil, err
		}
		if metadata.Revision != change.Revision {
			return nil, ErrConflict
		}
		prepared.metadata = metadata
		if change.Action == "update" || change.Action == "copy" {
			if !metadata.Editable() {
				return nil, ErrInvalid
			}
			view, err := readDraft(ctx, state, change.ID)
			if err != nil {
				return nil, err
			}
			if view.Metadata().Revision != change.Revision {
				return nil, ErrConflict
			}
			if view.state().readonly {
				return nil, ErrInvalid
			}
			prepared.presence = view.state().presence
			prepared.previousOrder = orderedRefs(view.state().draft)
			if change.Action == "copy" {
				reviewState := &inputState{draft: view.state().draft, refs: view.state().refs}
				copyReview = &Input{state: func() *inputState { return reviewState }}
				prepared.copyReview = copyReview
			}
			if snapshot != nil && validateMutations(snapshot.state().mutations, snapshot.state().refs, view.state().refs, true) != nil {
				return nil, ErrInvalid
			}
		}
	} else if snapshot.ValidateUpdate(nil) != nil {
		return nil, ErrInvalid
	}
	if snapshot != nil {
		prepared.preview, err = previewDraft(ctx, state, change.ID, change.Revision, snapshot)
		if err != nil {
			return nil, err
		}
	}
	operation := &Operation{state: func() *operationState { return prepared }}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed || ctx.Err() != nil {
		return nil, ErrClosed
	}
	state.operations[operation] = struct{}{}
	owned = true
	return operation, nil
}

func (operation *Operation) WriteSummary(ctx context.Context, writer io.Writer) error {
	if operation == nil || operation.state == nil || writer == nil {
		return ErrInvalid
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed || state.used {
		return ErrClosed
	}
	if ctx.Err() != nil {
		return safeContext(ctx.Err())
	}
	if _, err := fmt.Fprintf(writer, "ORIGIN %q\nOWNER current authenticated session\nACTION %s ASSET %q REVISION %d NAME %q\nServer asset only; no native application. Copy never includes credential values.\n", state.origin, state.change.Action, state.change.ID, state.change.Revision, state.change.Name); err != nil {
		return ErrInvalid
	}
	if state.input != nil {
		if state.input.WriteSummary(ctx, writer) != nil {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, ref := range append(append([]string{}, state.previousOrder...), orderedRefs(state.input.state().draft)...) {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if _, err := fmt.Fprintf(writer, "\nPRESENCE %q %t", ref, state.presence[ref]); err != nil {
				return ErrInvalid
			}
		}
	}
	if state.copyReview != nil {
		if state.copyReview.WriteSummary(ctx, writer) != nil {
			return ErrInvalid
		}
		for _, ref := range state.previousOrder {
			if _, err := fmt.Fprintf(writer, "\nCOPY SOURCE SLOT %q present=%t DESTINATION present=false", ref, state.presence[ref]); err != nil {
				return ErrInvalid
			}
		}
	}
	if state.preview != nil {
		return state.preview.WriteSummary(ctx, writer)
	}
	if _, err := fmt.Fprintf(writer, "METADATA name=%q target=%q schema=%d\n", state.metadata.Name, state.metadata.Agent, state.metadata.Schema); err != nil {
		return ErrInvalid
	}
	return nil
}

func (operation *Operation) Confirmation() string {
	if operation == nil || operation.state == nil {
		return ""
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed || state.used {
		return ""
	}
	if state.change.Action == "create" {
		return "mcp create"
	}
	return fmt.Sprintf("mcp %s %s %d", state.change.Action, state.change.ID, state.change.Revision)
}

func (operation *Operation) Close() {
	if operation == nil || operation.state == nil {
		return
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	if state.input != nil {
		state.input.Close()
		state.input = nil
	}
	state.preview = nil
	state.presence = nil
	state.metadata = Metadata{}
	if state.copyReview != nil {
		state.copyReview.Close()
		state.copyReview = nil
	}
	state.previousOrder = nil
	clientState := state.client.state()
	clientState.mutex.Lock()
	delete(clientState.operations, operation)
	clientState.mutex.Unlock()
}

func (operation *Operation) Submit(caller context.Context) (Metadata, error) {
	if operation == nil || operation.state == nil {
		return Metadata{}, ErrInvalid
	}
	data := operation.state()
	data.mutex.Lock()
	defer data.mutex.Unlock()
	if data.closed {
		return Metadata{}, ErrClosed
	}
	if data.used {
		return Metadata{}, ErrUnknown
	}
	data.used = true
	ctx, state, release, err := data.client.start(caller)
	if err != nil {
		return Metadata{}, err
	}
	defer release()
	state.mutex.Lock()
	blocked := state.blocked
	state.mutex.Unlock()
	if blocked {
		return Metadata{}, ErrUnknown
	}
	if err := authenticate(ctx, state); err != nil {
		return Metadata{}, err
	}
	var declaration *mcpv1.Draft
	var values []*mcpv1.SecretMutation
	if data.input != nil {
		declaration, values, err = wireInput(data.input)
		if err != nil {
			return Metadata{}, err
		}
		defer clearWire(values)
	}
	var view *mcpv1.View
	rpc := rpcFor(state)
	switch data.change.Action {
	case "create":
		response, callErr := rpc.CreateAsset(ctx, state.authorized(&mcpv1.CreateAssetRequest{Draft: declaration, Secrets: values}))
		err = callErr
		if response != nil && response.Msg != nil {
			view = response.Msg.View
		}
	case "update":
		response, callErr := rpc.UpdateAsset(ctx, state.authorized(&mcpv1.UpdateAssetRequest{Id: data.change.ID, ExpectedRevision: data.change.Revision, Draft: declaration, Secrets: values}))
		err = callErr
		if response != nil && response.Msg != nil {
			view = response.Msg.View
		}
	case "copy":
		response, callErr := rpc.CopyAsset(ctx, state.authorized(&mcpv1.CopyAssetRequest{Id: data.change.ID, ExpectedSourceRevision: data.change.Revision, Name: data.change.Name}))
		err = callErr
		if response != nil && response.Msg != nil {
			view = response.Msg.View
		}
	case "delete":
		response, callErr := rpc.DeleteAsset(ctx, state.authorized(&mcpv1.DeleteAssetRequest{Id: data.change.ID, ExpectedRevision: data.change.Revision}))
		err = callErr
		if callErr == nil && response != nil && response.Msg != nil && ctx.Err() == nil {
			return data.metadata, nil
		}
	}
	if err != nil {
		classified := requestError(ctx, err)
		code := connect.CodeOf(err)
		if ctx.Err() == nil && (code == connect.CodeAborted || code == connect.CodeFailedPrecondition || code == connect.CodeUnauthenticated || code == connect.CodePermissionDenied || code == connect.CodeInvalidArgument) {
			return Metadata{}, classified
		}
		state.mutex.Lock()
		state.blocked = true
		state.mutex.Unlock()
		return Metadata{}, ErrUnknown
	}
	result, validateErr := readView(view)
	if ctx.Err() != nil || validateErr != nil {
		state.mutex.Lock()
		state.blocked = true
		state.mutex.Unlock()
		return Metadata{}, ErrUnknown
	}
	metadata := result.Metadata()
	valid := true
	switch data.change.Action {
	case "create":
		valid = metadata.Revision == 1
	case "update":
		valid = metadata.ID == data.change.ID && metadata.Revision == data.change.Revision+1 && data.change.Revision < 9223372036854775807
	case "copy":
		valid = metadata.ID != data.change.ID && metadata.Revision == 1 && metadata.Name == data.change.Name
		for _, present := range result.state().presence {
			if present {
				valid = false
			}
		}
	default:
		valid = false
	}
	if declaration != nil {
		valid = valid && metadata.Name == declaration.Name && proto.Equal(view.Draft, declaration)
	}
	if data.copyReview != nil {
		expected, values, copyErr := wireInput(data.copyReview)
		clearWire(values)
		if copyErr != nil {
			valid = false
		} else {
			expected.Name = data.change.Name
			valid = valid && proto.Equal(view.Draft, expected)
		}
	}
	if !valid {
		state.mutex.Lock()
		state.blocked = true
		state.mutex.Unlock()
		return Metadata{}, ErrUnknown
	}
	return metadata, nil
}
