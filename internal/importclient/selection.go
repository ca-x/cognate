package importclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"google.golang.org/protobuf/proto"
)

var ErrSelectionUnknown = errors.New("import outcome unknown; query the operation status; do not resubmit")

type selectionPrivate struct{}

func (selectionPrivate) String() string       { return "[private import selection]" }
func (selectionPrivate) GoString() string     { return "[private import selection]" }
func (selectionPrivate) LogValue() slog.Value { return slog.StringValue("[private import selection]") }
func (selectionPrivate) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private import selection]")
}

type SelectionClient struct {
	selectionPrivate
	state func() *selectionClientState
}

type selectionClientState struct {
	mutex          sync.Mutex
	rpc            importv1connect.ImportServiceClient
	session        string
	ctx            context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	slot           chan struct{}
	statusSlot     chan struct{}
	closed         bool
	previews       map[*SelectionPreview]struct{}
	operations     map[*SelectionOperation]struct{}
	ownedTransport *http.Transport
}

type SelectionPreview struct {
	selectionPrivate
	state func() *selectionPreviewState
}

type selectionPreviewState struct {
	mutex    sync.Mutex
	client   *SelectionClient
	source   importv1.ImportSelectionSource
	hash     string
	options  []*importv1.ImportCandidateOptions
	response *importv1.PreviewImportSelectionResponse
	used     bool
}

type SelectionOperation struct {
	selectionPrivate
	state func() *selectionOperationState
}

type selectionOperationState struct {
	mutex     sync.Mutex
	client    *SelectionClient
	id        string
	source    importv1.ImportSelectionSource
	hash      string
	preview   *importv1.PreviewImportSelectionResponse
	request   *importv1.CommitImportSelectionRequest
	attempted bool
	closed    bool
	receipt   *importv1.ImportReceipt
	cancel    context.CancelFunc
	running   chan struct{}
}

type SelectionChoice struct {
	Repositories, Skills, Configurations []int
	AcknowledgedNotices                  []string
}

type SelectionResult struct {
	OperationID                          string
	Observation                          importv1.ImportCommitObservation
	Phase                                importv1.ImportReceiptPhase
	Revision                             int64
	Repositories, Skills, Configurations int
	SelectionVerified                    bool
}

func NewSelectionClient(server, session string, supplied *http.Client) (*SelectionClient, error) {
	origin, err := parseOrigin(server)
	if err != nil {
		return nil, err
	}
	if !validSession(session) {
		return nil, errSession
	}
	owned := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 4, MaxConnsPerHost: 2, MaxResponseHeaderBytes: 64 << 10, DisableCompression: true}
	var transport http.RoundTripper = owned
	timeout := 120 * time.Second
	if supplied != nil {
		if supplied.Transport != nil {
			transport = supplied.Transport
			owned = nil
		}
		if supplied.Timeout > 0 {
			timeout = min(timeout, supplied.Timeout)
		}
	}
	bounded := &http.Client{Transport: selectionTransport{base: transport, scheme: origin.Scheme, host: origin.Host}, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithCancel(context.Background())
	clientState := &selectionClientState{rpc: importv1connect.NewImportServiceClient(bounded, origin.String(), connect.WithAcceptCompression("gzip", nil, nil), connect.WithCodec(selectionCodec{}), connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(24<<20)), session: session, ctx: ctx, cancel: cancel, slot: make(chan struct{}, 1), statusSlot: make(chan struct{}, 1), previews: make(map[*SelectionPreview]struct{}), operations: make(map[*SelectionOperation]struct{}), ownedTransport: owned}
	return &SelectionClient{state: func() *selectionClientState { return clientState }}, nil
}

type selectionTransport struct {
	base         http.RoundTripper
	scheme, host string
}

func (transport selectionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.scheme || request.URL.Host != transport.host || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return nil, errOrigin
	}
	limit := int64(2 << 20)
	switch request.URL.Path {
	case importv1connect.ImportServicePreviewImportSelectionProcedure, importv1connect.ImportServiceCommitImportSelectionProcedure:
	case importv1connect.ImportServiceGetImportCommitStatusProcedure:
		limit = 128 << 10
	default:
		return nil, errOrigin
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := transport.base.RoundTrip(request)
	if err == nil && response != nil && response.Body != nil {
		if response.ContentLength > limit || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
			_ = response.Body.Close()
			return nil, errPreview
		}
		response.Body = &boundedBody{ReadCloser: response.Body, remaining: limit + 1}
	}
	return response, err
}

func (client *SelectionClient) start(caller context.Context, observing bool, budget time.Duration) (context.Context, string, func(), error) {
	if err := caller.Err(); err != nil {
		return nil, "", nil, err
	}
	if client == nil || client.state == nil {
		return nil, "", nil, errSelection
	}
	client.state().mutex.Lock()
	defer client.state().mutex.Unlock()
	if client.state().closed || client.state().rpc == nil || client.state().cancel == nil || client.state().session == "" {
		return nil, "", nil, errSelection
	}
	slot := client.state().slot
	if observing {
		slot = client.state().statusSlot
	}
	select {
	case slot <- struct{}{}:
	default:
		return nil, "", nil, connect.NewError(connect.CodeResourceExhausted, errSelection)
	}
	client.state().workers.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	callerDone, clientDone := make(chan struct{}), make(chan struct{})
	stopCaller := context.AfterFunc(caller, func() { cancel(); close(callerDone) })
	stopClient := context.AfterFunc(client.state().ctx, func() { cancel(); close(clientDone) })
	if caller.Err() != nil || client.state().ctx.Err() != nil {
		cancel()
	}
	return ctx, client.state().session, func() {
		cancel()
		if !stopCaller() {
			<-callerDone
		}
		if !stopClient() {
			<-clientDone
		}
		<-slot
		client.state().workers.Done()
	}, nil
}

func selectionReadFile(ctx context.Context, source importv1.ImportSelectionSource, path, expected string) ([]byte, string, error) {
	limit := 1 << 20
	if source == importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24 {
		limit = 16 << 20
	} else if source != importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON {
		return nil, "", errSelection
	}
	if expected != "" && !validHash(expected) {
		return nil, "", errHash
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if part == ".." {
			return nil, "", errSelection
		}
	}
	file, err := openExport(path)
	if err != nil {
		return nil, "", errSelection
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > int64(limit) {
		return nil, "", errSelection
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) == 0 || len(data) > limit {
		clear(data)
		return nil, "", errSelection
	}
	if err := ctx.Err(); err != nil {
		clear(data)
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	if expected != "" && hash != expected {
		clear(data)
		return nil, "", errHash
	}
	return data, hash, nil
}

func (client *SelectionClient) PreviewFile(caller context.Context, source importv1.ImportSelectionSource, path, expected string, options []*importv1.ImportCandidateOptions) (*SelectionPreview, error) {
	budget := 5 * time.Second
	if source == importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24 {
		budget = 15 * time.Second
	}
	ctx, session, done, err := client.start(caller, false, budget)
	if err != nil {
		return nil, err
	}
	defer done()
	if len(options) > 160 {
		return nil, errSelection
	}
	request := &importv1.PreviewImportSelectionRequest{Source: source}
	for _, option := range options {
		if option == nil || !selectionKnown(option.ProtoReflect(), 0) || !validHash(option.CandidateId) {
			return nil, errSelection
		}
		request.Options = append(request.Options, proto.Clone(option).(*importv1.ImportCandidateOptions))
	}
	data, hash, err := selectionReadFile(ctx, source, path, expected)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	request.SourceData = data
	wire := connect.NewRequest(request)
	wire.Header().Set("Authorization", "Bearer "+session)
	response, err := client.state().rpc.PreviewImportSelection(ctx, wire)
	if err != nil {
		return nil, selectionRequestError(caller, ctx, err)
	}
	if !selectionPreviewValid(response.Msg, time.Now()) {
		return nil, errPreview
	}
	previewState := &selectionPreviewState{client: client, source: source, hash: hash, options: request.Options, response: proto.Clone(response.Msg).(*importv1.PreviewImportSelectionResponse)}
	preview := &SelectionPreview{state: func() *selectionPreviewState { return previewState }}
	client.state().mutex.Lock()
	defer client.state().mutex.Unlock()
	if client.state().closed || len(client.state().previews) >= 16 {
		clear(preview.state().response.Ticket)
		return nil, errSelection
	}
	client.state().previews[preview] = struct{}{}
	return preview, nil
}

func (preview *SelectionPreview) SHA256() string {
	if preview == nil || preview.state == nil {
		return ""
	}
	preview.state().mutex.Lock()
	defer preview.state().mutex.Unlock()
	return preview.state().hash
}
func (preview *SelectionPreview) View() *importv1.PreviewImportSelectionResponse {
	if preview == nil || preview.state == nil {
		return nil
	}
	preview.state().mutex.Lock()
	defer preview.state().mutex.Unlock()
	if preview.state().response == nil {
		return nil
	}
	view := proto.Clone(preview.state().response).(*importv1.PreviewImportSelectionResponse)
	clear(view.Ticket)
	view.Ticket = nil
	return view
}
func (preview *SelectionPreview) Close() {
	if preview == nil || preview.state == nil {
		return
	}
	preview.state().mutex.Lock()
	if preview.state().response != nil {
		clear(preview.state().response.Ticket)
		preview.state().response = nil
	}
	preview.state().options = nil
	preview.state().hash = ""
	preview.state().used = true
	preview.state().mutex.Unlock()
	if preview.state().client != nil {
		preview.state().client.state().mutex.Lock()
		delete(preview.state().client.state().previews, preview)
		preview.state().client.state().mutex.Unlock()
	}
}

func (client *SelectionClient) BeginSelection(preview *SelectionPreview, choice SelectionChoice) (*SelectionOperation, error) {
	_, _, done, err := client.start(context.Background(), false, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer done()
	if preview == nil || preview.state == nil {
		return nil, errSelection
	}
	preview.state().mutex.Lock()
	defer preview.state().mutex.Unlock()
	if preview.state().client != client || preview.state().used || preview.state().response == nil || preview.state().response.ExpiresMs <= time.Now().UnixMilli() {
		return nil, errSelection
	}
	request, err := selectionRequest(preview.state().response, choice)
	if err != nil {
		return nil, err
	}
	operationID := make([]byte, 16)
	if _, err := rand.Read(operationID); err != nil {
		return nil, errSelection
	}
	request.OperationId = operationID
	request.Source = preview.state().source
	request.Ticket = bytes.Clone(preview.state().response.Ticket)
	for _, option := range preview.state().options {
		request.Options = append(request.Options, proto.Clone(option).(*importv1.ImportCandidateOptions))
	}
	operationState := &selectionOperationState{client: client, id: hex.EncodeToString(operationID), source: preview.state().source, hash: preview.state().hash, preview: proto.Clone(preview.state().response).(*importv1.PreviewImportSelectionResponse), request: request}
	operation := &SelectionOperation{state: func() *selectionOperationState { return operationState }}
	client.state().mutex.Lock()
	defer client.state().mutex.Unlock()
	if client.state().closed || len(client.state().operations) >= 32 {
		clear(request.Ticket)
		clear(operation.state().preview.Ticket)
		return nil, errSelection
	}
	client.state().operations[operation] = struct{}{}
	preview.state().used = true
	return operation, nil
}

func (operation *SelectionOperation) ID() string {
	if operation == nil || operation.state == nil {
		return ""
	}
	return operation.state().id
}
func (operation *SelectionOperation) CommitFile(caller context.Context, path string) (SelectionResult, error) {
	if operation == nil || operation.state == nil || operation.state().client == nil {
		return SelectionResult{}, errSelection
	}
	result := SelectionResult{OperationID: operation.state().id, Observation: importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_UNKNOWN}
	ctx, session, done, err := operation.state().client.start(caller, false, 120*time.Second)
	if err != nil {
		return result, err
	}
	defer done()
	operation.state().mutex.Lock()
	if operation.state().closed || operation.state().attempted || operation.state().preview.ExpiresMs <= time.Now().UnixMilli() {
		operation.state().mutex.Unlock()
		return result, errSelection
	}
	request := proto.Clone(operation.state().request).(*importv1.CommitImportSelectionRequest)
	preview := proto.Clone(operation.state().preview).(*importv1.PreviewImportSelectionResponse)
	clear(preview.Ticket)
	preview.Ticket = nil
	source, hash := operation.state().source, operation.state().hash
	ctx, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	operation.state().cancel = cancel
	operation.state().running = finished
	operation.state().mutex.Unlock()
	defer func() {
		cancel()
		operation.state().mutex.Lock()
		operation.state().cancel = nil
		operation.state().running = nil
		operation.state().mutex.Unlock()
		close(finished)
	}()
	defer clear(request.Ticket)
	data, _, err := selectionReadFile(ctx, source, path, hash)
	if err != nil {
		return result, err
	}
	defer clear(data)
	request.SourceData = data
	wire := connect.NewRequest(request)
	wire.Header().Set("Authorization", "Bearer "+session)
	operation.state().mutex.Lock()
	if operation.state().closed || operation.state().attempted || preview.ExpiresMs <= time.Now().UnixMilli() {
		operation.state().mutex.Unlock()
		return result, errSelection
	}
	operation.state().attempted = true
	clear(operation.state().request.Ticket)
	operation.state().mutex.Unlock()
	response, err := operation.state().client.state().rpc.CommitImportSelection(ctx, wire)
	if err != nil {
		return result, errors.Join(ErrSelectionUnknown, selectionRequestError(caller, ctx, err))
	}
	if !selectionReceiptValid(response.Msg.GetReceipt(), request, preview) {
		return result, ErrSelectionUnknown
	}
	operation.state().mutex.Lock()
	if !selectionReceiptProgress(operation.state().receipt, response.Msg.Receipt) {
		operation.state().mutex.Unlock()
		return result, ErrSelectionUnknown
	}
	if !operation.state().closed {
		operation.state().receipt = proto.Clone(response.Msg.Receipt).(*importv1.ImportReceipt)
	}
	operation.state().mutex.Unlock()
	result = selectionResult(response.Msg.Receipt)
	result.SelectionVerified = true
	if result.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED {
		return result, ErrIncomplete
	}
	return result, nil
}
func (operation *SelectionOperation) Close() {
	if operation == nil || operation.state == nil {
		return
	}
	operation.state().mutex.Lock()
	cancel, running := operation.state().cancel, operation.state().running
	if operation.state().request != nil {
		clear(operation.state().request.Ticket)
		operation.state().request = nil
	}
	if operation.state().preview != nil {
		clear(operation.state().preview.Ticket)
		operation.state().preview = nil
	}
	operation.state().closed = true
	operation.state().mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	if running != nil {
		<-running
	}
	if operation.state().client != nil {
		operation.state().client.state().mutex.Lock()
		delete(operation.state().client.state().operations, operation)
		operation.state().client.state().mutex.Unlock()
	}
}
func (client *SelectionClient) Status(caller context.Context, id string) (SelectionResult, error) {
	result := SelectionResult{OperationID: id, Observation: importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_UNKNOWN}
	operationID, err := hex.DecodeString(id)
	if err != nil || len(operationID) != 16 || strings.ToLower(id) != id || bytes.Equal(operationID, make([]byte, 16)) {
		return result, errSelection
	}
	ctx, session, done, err := client.start(caller, true, 5*time.Second)
	if err != nil {
		return result, err
	}
	defer done()
	request := connect.NewRequest(&importv1.GetImportCommitStatusRequest{OperationId: operationID})
	request.Header().Set("Authorization", "Bearer "+session)
	response, err := client.state().rpc.GetImportCommitStatus(ctx, request)
	if err != nil {
		return result, selectionRequestError(caller, ctx, err)
	}
	if !selectionStatusValid(response.Msg, operationID) {
		return result, errPreview
	}
	client.state().mutex.Lock()
	var live *SelectionOperation
	for operation := range client.state().operations {
		if operation.state().id == id {
			live = operation
			break
		}
	}
	client.state().mutex.Unlock()
	verified := false
	if live != nil {
		live.state().mutex.Lock()
		defer live.state().mutex.Unlock()
		if !live.state().closed {
			if response.Msg.Receipt == nil {
				if live.state().receipt != nil {
					return result, errPreview
				}
			} else {
				if !selectionReceiptValid(response.Msg.Receipt, live.state().request, live.state().preview) || !selectionReceiptProgress(live.state().receipt, response.Msg.Receipt) {
					return result, errPreview
				}
				live.state().receipt = proto.Clone(response.Msg.Receipt).(*importv1.ImportReceipt)
			}
			verified = true
		}
	}
	if response.Msg.Receipt != nil {
		result = selectionResult(response.Msg.Receipt)
	}
	result.Observation = response.Msg.Observation
	result.SelectionVerified = verified
	return result, nil
}
func (client *SelectionClient) Close() {
	if client == nil || client.state == nil {
		return
	}
	client.state().mutex.Lock()
	client.state().closed = true
	if client.state().cancel != nil {
		client.state().cancel()
	}
	client.state().mutex.Unlock()
	client.state().workers.Wait()
	if client.state().ownedTransport != nil {
		client.state().ownedTransport.CloseIdleConnections()
	}
	client.state().mutex.Lock()
	previews := slices.Collect(maps.Keys(client.state().previews))
	operations := slices.Collect(maps.Keys(client.state().operations))
	client.state().session = ""
	client.state().mutex.Unlock()
	for _, preview := range previews {
		preview.Close()
	}
	for _, operation := range operations {
		operation.Close()
	}
}

func selectionRequestError(caller, request context.Context, err error) error {
	if caller.Err() != nil {
		return requestError(caller, err)
	}
	return requestError(request, err)
}
