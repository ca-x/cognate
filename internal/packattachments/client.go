package packattachments

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1/packconfigurationv1connect"
)

var ErrUnavailable = errors.New("pack attachment service unavailable")
var ErrConflict = errors.New("pack or attachment revision changed; read and review again")
var ErrDenied = errors.New("pack attachment session or operation denied")
var ErrClosed = errors.New("pack attachment client or operation closed")
var ErrUnknown = errors.New("pack attachment mutation outcome unknown; never replay; reconcile with Get")
var ErrBusy = errors.New("pack attachment client busy")
var ErrTargetUnsupported = errors.New("pack attachment target unsupported by server")

type Entry struct {
	ID, AssetID, AgentID, TargetID string
	AssetRevision                  int64
	Kind, Schema, Ordinal, State   uint32
}
type Snapshot struct {
	PackID                              string
	PackRevision, ConfigurationRevision int64
	Entries                             []Entry
	Digest                              [32]byte
}
type private struct{}

func (private) String() string   { return "[private pack attachments]" }
func (private) GoString() string { return "[private pack attachments]" }
func (private) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private pack attachments]")
}
func (private) LogValue() slog.Value         { return slog.StringValue("[private pack attachments]") }
func (private) MarshalJSON() ([]byte, error) { return []byte(`"[private pack attachments]"`), nil }

type Client struct {
	private
	state func() *clientState
}
type clientState struct {
	mutex                  sync.Mutex
	closed, busy, blocked  bool
	workers                sync.WaitGroup
	life                   context.Context
	cancel                 context.CancelFunc
	http                   *http.Client
	transport              *http.Transport
	origin, session, owner string
}
type Operation struct {
	private
	state func() *operationState
}
type operationState struct {
	mutex               sync.Mutex
	client              *Client
	closed, used        bool
	before              Snapshot
	refs                []Reference
	owner, confirmation string
	expires             time.Time
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
	state := &clientState{life: life, cancel: cancel, http: httpClient, transport: transport, origin: address.String(), session: session}
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	state.session = ""
	state.owner = ""
	state.mutex.Unlock()
	state.transport.CloseIdleConnections()
}
func (state *clientState) authorized[Message any](message *Message) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+state.session)
	return request
}
func knownFailure(err error) error {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return ErrDenied
	case connect.CodeAborted:
		return ErrConflict
	case connect.CodeFailedPrecondition:
		return ErrTargetUnsupported
	case connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeResourceExhausted, connect.CodeUnimplemented:
		return ErrInvalid
	default:
		return nil
	}
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
	rpc := authv1connect.NewAuthServiceClient(state.http, state.origin, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(4096), connect.WithCodec(attachmentCodec{}))
	response, err := rpc.GetSession(ctx, state.authorized(&authv1.GetSessionRequest{}))
	if err != nil {
		return readFailure(ctx, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if response == nil || response.Msg == nil || !validTarget(response.Msg.UserId) && !validID(response.Msg.UserId) {
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
func rpcFor(state *clientState) packconfigurationv1connect.PackConfigurationServiceClient {
	return packconfigurationv1connect.NewPackConfigurationServiceClient(state.http, state.origin, connect.WithReadMaxBytes(64<<10), connect.WithSendMaxBytes(64<<10), connect.WithCodec(attachmentCodec{}))
}
func snapshotFrom(value *packv1.AttachmentSnapshot, pack string) (Snapshot, error) {
	if value == nil || value.PackId != pack || !validID(pack) || value.PackRevision < 1 || value.ConfigurationRevision < 1 || len(value.SnapshotDigest) != 32 || len(value.Entries) > 16 {
		return Snapshot{}, ErrInvalid
	}
	result := Snapshot{PackID: pack, PackRevision: value.PackRevision, ConfigurationRevision: value.ConfigurationRevision, Entries: make([]Entry, 0, len(value.Entries))}
	copy(result.Digest[:], value.SnapshotDigest)
	seenIDs, seenRefs := map[string]bool{}, map[string]bool{}
	for index, entry := range value.Entries {
		if entry == nil || !validID(entry.AttachmentId) || !validID(entry.AssetId) || entry.AssetRevision < 1 || !validTarget(entry.TargetId) || !validTarget(entry.AgentId) || len(entry.AgentId) > 64 || entry.Kind < 1 || entry.Kind > 2 || entry.SchemaVersion < 1 || entry.SchemaVersion > 65535 || entry.Ordinal != uint32(index) || entry.State < 1 || entry.State > 3 || seenIDs[entry.AttachmentId] || seenRefs[entry.AssetId+"/"+entry.TargetId] {
			return Snapshot{}, ErrInvalid
		}
		if entry.State == packv1.AttachmentState_ATTACHMENT_STATE_CURRENT && (entry.Kind != 1 || entry.SchemaVersion != 1 || entry.TargetId != entry.AgentId+".provider.user") {
			return Snapshot{}, ErrInvalid
		}
		seenIDs[entry.AttachmentId] = true
		seenRefs[entry.AssetId+"/"+entry.TargetId] = true
		result.Entries = append(result.Entries, Entry{ID: entry.AttachmentId, AssetID: entry.AssetId, AgentID: entry.AgentId, TargetID: entry.TargetId, AssetRevision: entry.AssetRevision, Kind: uint32(entry.Kind), Schema: entry.SchemaVersion, Ordinal: entry.Ordinal, State: uint32(entry.State)})
	}
	return result, nil
}
func get(ctx context.Context, state *clientState, pack string) (Snapshot, error) {
	response, err := rpcFor(state).GetAttachments(ctx, state.authorized(&packv1.GetAttachmentsRequest{PackId: pack}))
	if err != nil {
		return Snapshot{}, readFailure(ctx, err)
	}
	if ctx.Err() != nil {
		return Snapshot{}, ctx.Err()
	}
	if response == nil || response.Msg == nil {
		return Snapshot{}, ErrInvalid
	}
	return snapshotFrom(response.Msg.Snapshot, pack)
}
func (client *Client) Get(caller context.Context, pack string) (Snapshot, error) {
	if !validID(pack) {
		return Snapshot{}, ErrInvalid
	}
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return Snapshot{}, err
	}
	return get(ctx, state, pack)
}
func (client *Client) Prepare(caller context.Context, pack string, packRevision, configurationRevision int64, refs []Reference) (*Operation, error) {
	if !validID(pack) || packRevision < 1 || configurationRevision < 1 || packRevision == math.MaxInt64 || configurationRevision == math.MaxInt64 || !validReferences(refs) {
		return nil, ErrInvalid
	}
	owned := slices.Clone(refs)
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
	before, err := get(ctx, state, pack)
	if err != nil {
		return nil, err
	}
	if before.PackRevision != packRevision || before.ConfigurationRevision != configurationRevision {
		return nil, ErrConflict
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrUnavailable
	}
	state.mutex.Lock()
	owner := state.owner
	state.mutex.Unlock()
	transcript, err := json.Marshal(struct {
		Before Snapshot
		New    []Reference
		Owner  string
		Nonce  []byte
	}{before, owned, owner, nonce})
	if err != nil {
		return nil, ErrInvalid
	}
	digest := sha256.Sum256(transcript)
	clear(transcript)
	clear(nonce)
	operation := &operationState{client: client, before: before, refs: owned, owner: owner, expires: time.Now().Add(time.Minute), confirmation: "REPLACE " + pack + " " + hex.EncodeToString(digest[:16])}
	return &Operation{state: func() *operationState { return operation }}, nil
}
func (operation *Operation) Close() {
	if operation == nil || operation.state == nil {
		return
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.closed = true
	state.refs = nil
	state.before = Snapshot{}
	state.owner = ""
	state.confirmation = ""
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
	return state.confirmation
}
func writeSummary(ctx context.Context, writer io.Writer, text string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(text) > 64<<10 {
		return ErrInvalid
	}
	var count int
	var err error
	if bounded, ok := writer.(interface {
		WriteContext(context.Context, []byte) (int, error)
	}); ok {
		count, err = bounded.WriteContext(ctx, []byte(text))
	} else if buffer, ok := writer.(*bytes.Buffer); ok {
		count, err = buffer.WriteString(text)
	} else {
		return ErrInvalid
	}
	if err != nil || count != len(text) {
		return ErrInvalid
	}
	return ctx.Err()
}
func snapshotText(snapshot Snapshot) string {
	var text strings.Builder
	fmt.Fprintf(&text, "PACK %q packRevision=%d configurationRevision=%d oldOrderedCount=%d digest=%x\n", snapshot.PackID, snapshot.PackRevision, snapshot.ConfigurationRevision, len(snapshot.Entries), snapshot.Digest)
	for _, entry := range snapshot.Entries {
		fmt.Fprintf(&text, "OLD ordinal=%d attachment=%q asset=%q assetRevision=%d agent=%q kind=%d schema=%d target=%q state=%d NativeUnavailable\n", entry.Ordinal, entry.ID, entry.AssetID, entry.AssetRevision, entry.AgentID, entry.Kind, entry.Schema, entry.TargetID, entry.State)
	}
	return text.String()
}
func WriteSnapshot(ctx context.Context, writer io.Writer, snapshot Snapshot) error {
	return writeSummary(ctx, writer, snapshotText(snapshot)+"Metadata snapshot only. All targets NativeUnavailable; no native application.\n")
}
func WriteReviewIdentity(ctx context.Context, writer io.Writer, origin string) error {
	address, err := parseOrigin(origin)
	if err != nil {
		return err
	}
	return writeSummary(ctx, writer, "Owner metadata replacement at "+address.String()+"; current authenticated session; no native application.\n")
}
func (operation *Operation) WriteSummary(ctx context.Context, writer io.Writer) error {
	if operation == nil || operation.state == nil {
		return ErrClosed
	}
	state := operation.state()
	state.mutex.Lock()
	if state.closed || state.used {
		state.mutex.Unlock()
		return ErrClosed
	}
	text := snapshotText(state.before) + "Metadata/CAS preview only; new references NOT server-validated (no preview RPC).\n"
	for index, ref := range state.refs {
		text += fmt.Sprintf("NEW ordinal=%d asset=%q expectedAssetRevision=%d target=%q NativeUnavailable\n", index, ref.AssetID, ref.ExpectedAssetRevision, ref.TargetID)
	}
	text += fmt.Sprintf("REPLACE ENTIRE ORDERED SET count=%d (zero clears). Nothing applied.\n", len(state.refs))
	state.mutex.Unlock()
	return writeSummary(ctx, writer, text)
}
func (operation *Operation) Submit(caller context.Context) (Snapshot, error) {
	if operation == nil || operation.state == nil {
		return Snapshot{}, ErrClosed
	}
	plan := operation.state()
	ctx, state, release, err := plan.client.start(caller)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	state.mutex.Lock()
	blocked := state.blocked
	state.mutex.Unlock()
	if blocked {
		return Snapshot{}, ErrUnknown
	}
	plan.mutex.Lock()
	if plan.closed || plan.used || !time.Now().Before(plan.expires) {
		plan.mutex.Unlock()
		return Snapshot{}, ErrClosed
	}
	plan.used = true
	before := plan.before
	refs := slices.Clone(plan.refs)
	owner := plan.owner
	plan.mutex.Unlock()
	if err := authenticate(ctx, state); err != nil {
		return Snapshot{}, err
	}
	state.mutex.Lock()
	sameOwner := state.owner == owner
	state.mutex.Unlock()
	if !sameOwner {
		return Snapshot{}, ErrDenied
	}
	current, err := get(ctx, state, before.PackID)
	if err != nil {
		return Snapshot{}, err
	}
	if !reflect.DeepEqual(current, before) {
		return Snapshot{}, ErrConflict
	}
	wire := make([]*packv1.AttachmentReference, 0, len(refs))
	for _, ref := range refs {
		wire = append(wire, &packv1.AttachmentReference{AssetId: ref.AssetID, ExpectedAssetRevision: ref.ExpectedAssetRevision, TargetId: ref.TargetID})
	}
	response, err := rpcFor(state).ReplaceAttachments(ctx, state.authorized(&packv1.ReplaceAttachmentsRequest{PackId: before.PackID, ExpectedPackRevision: before.PackRevision, ExpectedConfigurationRevision: before.ConfigurationRevision, References: wire}))
	if err != nil {
		if known := knownFailure(err); known != nil && connect.IsWireError(err) && ctx.Err() == nil {
			return Snapshot{}, known
		}
		state.mutex.Lock()
		state.blocked = true
		state.mutex.Unlock()
		return Snapshot{}, ErrUnknown
	}
	var result Snapshot
	if response != nil && response.Msg != nil {
		result, err = snapshotFrom(response.Msg.Snapshot, before.PackID)
	} else {
		err = ErrInvalid
	}
	valid := err == nil && ctx.Err() == nil && result.PackRevision == before.PackRevision+1 && result.ConfigurationRevision == before.ConfigurationRevision+1 && len(result.Entries) == len(refs)
	if valid {
		for index, ref := range refs {
			entry := result.Entries[index]
			if entry.AssetID != ref.AssetID || entry.AssetRevision != ref.ExpectedAssetRevision || entry.TargetID != ref.TargetID || entry.State != 1 {
				valid = false
				break
			}
		}
	}
	if !valid {
		state.mutex.Lock()
		state.blocked = true
		state.mutex.Unlock()
		return Snapshot{}, ErrUnknown
	}
	return result, nil
}
