package backup

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
)

type operation struct {
	target             *S3Target
	ctx                context.Context
	cancel             context.CancelFunc
	transport          *http.Transport
	mutex              sync.Mutex
	bodies             []*boundedBody
	bad                bool
	requestCloseFailed bool
	dispatched         bool
	requests, maximum  int
	size               int64
	key, prefix        string
	mode               string
}
type boundedBody struct {
	body        io.ReadCloser
	operation   *operation
	limit, read int64
	exact       bool
	mutex       sync.Mutex
	closed      bool
	closeError  error
}

type requestBody struct {
	body       io.ReadCloser
	operation  *operation
	mutex      sync.Mutex
	closed     bool
	closeError error
	done       chan struct{}
}

func (body *requestBody) Read(buffer []byte) (int, error) {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	if err := body.operation.ctx.Err(); err != nil {
		return 0, err
	}
	return body.body.Read(buffer)
}
func (body *requestBody) Close() error {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return body.closeError
	}
	body.closed = true
	body.closeError = body.body.Close()
	if body.closeError != nil {
		body.operation.mutex.Lock()
		body.operation.bad = true
		body.operation.requestCloseFailed = true
		body.operation.mutex.Unlock()
	}
	close(body.done)
	return body.closeError
}

func (target *S3Target) begin(ctx context.Context, mode string, maximum int) (*operation, error) {
	if ctx == nil || target == nil || target.done == nil {
		return nil, ErrInvalid
	}
	target.mutex.Lock()
	defer target.mutex.Unlock()
	if target.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(target.active) >= 2 {
		return nil, ErrBusy
	}
	linked, cancel := context.WithTimeout(ctx, 120*time.Second)
	operation := &operation{target: target, ctx: linked, cancel: cancel, mode: mode, maximum: maximum}
	operation.transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: target.roots}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 32 << 10, MaxConnsPerHost: 1, DisableKeepAlives: true, DisableCompression: true, ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10}
	target.active[operation] = struct{}{}
	return operation, nil
}
func (operation *operation) failed() {
	operation.mutex.Lock()
	operation.bad = true
	operation.mutex.Unlock()
}
func (operation *operation) healthy() bool {
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	return !operation.bad && operation.ctx.Err() == nil
}
func (operation *operation) end() {
	operation.cancel()
	operation.mutex.Lock()
	bodies := append([]*boundedBody(nil), operation.bodies...)
	operation.mutex.Unlock()
	operation.mutex.Lock()
	closeFailed := operation.requestCloseFailed
	operation.mutex.Unlock()
	for _, body := range bodies {
		if body.Close() != nil {
			closeFailed = true
		}
	}
	operation.transport.CloseIdleConnections()
	target := operation.target
	target.mutex.Lock()
	target.closeFailed = target.closeFailed || closeFailed
	delete(target.active, operation)
	if target.closed && len(target.active) == 0 {
		target.finishClose()
	}
	target.mutex.Unlock()
}
func (target *S3Target) finishClose() {
	if target.options.read != nil {
		data := target.options.read()
		private := data.credentials.read()
		clear(private.access)
		clear(private.secret)
		clear(private.token)
		target.options = S3Options{}
	}
	select {
	case <-target.done:
	default:
		close(target.done)
	}
}
func (target *S3Target) Close() error {
	if target == nil || target.done == nil {
		return ErrInvalid
	}
	target.mutex.Lock()
	target.closed = true
	for operation := range target.active {
		operation.cancel()
	}
	if len(target.active) == 0 {
		target.finishClose()
	}
	done := target.done
	target.mutex.Unlock()
	<-done
	target.mutex.Lock()
	failed := target.closeFailed
	target.mutex.Unlock()
	if failed {
		return ErrUnavailable
	}
	return nil
}

func (operation *operation) client(allowed ...string) *s3.Client {
	options := operation.target.options.read()
	private := options.credentials.read()
	return s3.New(s3.Options{BaseEndpoint: new(options.endpoint), Region: options.region, UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(string(private.access), string(private.secret), string(private.token)), HTTPClient: &http.Client{Transport: operation, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrUnsupported }}, RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired, APIOptions: []func(*middleware.Stack) error{func(stack *middleware.Stack) error {
		return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("backup-allowed-operation", func(ctx context.Context, input middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
			name := awsmiddleware.GetOperationName(ctx)
			for _, candidate := range allowed {
				if candidate == name {
					return next.HandleInitialize(ctx, input)
				}
			}
			return middleware.InitializeOutput{}, middleware.Metadata{}, ErrUnsupported
		}), middleware.After)
	}}})
}

func (operation *operation) RoundTrip(request *http.Request) (*http.Response, error) {
	options := operation.target.options.read()
	if operation.ctx.Err() != nil {
		return nil, operation.ctx.Err()
	}
	if request.URL.Scheme != "https" || request.URL.Scheme+"://"+request.URL.Host != options.endpoint {
		return nil, ErrInvalid
	}
	if operation.key != "" && request.URL.Path != "/"+options.bucket+"/"+operation.key {
		return nil, ErrInvalid
	}
	if operation.mode == "list" && (request.Method != http.MethodGet || request.URL.Query().Get("prefix") != operation.prefix || request.URL.Query().Get("list-type") != "2" || request.URL.Query().Get("max-keys") != "50") {
		return nil, ErrInvalid
	}
	operation.mutex.Lock()
	if operation.requests >= operation.maximum {
		operation.mutex.Unlock()
		return nil, ErrUnavailable
	}
	operation.requests++
	operation.dispatched = true
	operation.mutex.Unlock()
	ownedRequest := request.Clone(operation.ctx)
	var outgoing *requestBody
	if request.Body != nil && request.Body != http.NoBody {
		outgoing = &requestBody{body: request.Body, operation: operation, done: make(chan struct{})}
		ownedRequest.Body = outgoing
	}
	response, err := operation.transport.RoundTrip(ownedRequest)
	if outgoing != nil {
		<-outgoing.done
	}
	if err != nil {
		return nil, err
	}
	body := &boundedBody{body: response.Body, operation: operation}
	operation.mutex.Lock()
	operation.bodies = append(operation.bodies, body)
	operation.mutex.Unlock()
	limit := int64(64 << 10)
	exact := false
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		switch {
		case operation.mode == "list":
			limit = 256 << 10
		case request.Method == http.MethodHead:
			_, dateError := http.ParseTime(response.Header.Get("Last-Modified"))
			if response.ContentLength != operation.size || dateError != nil {
				body.Close()
				operation.failed()
				return nil, ErrUnavailable
			}
			limit = 0
		case request.Method == http.MethodGet:
			limit = operation.size
			expectedRange := "bytes 0-" + strconv.FormatInt(operation.size-1, 10) + "/" + strconv.FormatInt(operation.size, 10)
			if request.Header.Get("Range") == "bytes=0-0" {
				limit = 1
				expectedRange = "bytes 0-0/" + strconv.FormatInt(operation.size, 10)
			}
			if response.StatusCode != 206 || response.ContentLength != limit || response.Header.Get("Content-Range") != expectedRange {
				body.Close()
				operation.failed()
				return nil, ErrUnavailable
			}
			exact = true
		default:
			limit = 64 << 10
		}
	}
	if response.Header.Get("Content-Encoding") != "" {
		body.Close()
		operation.failed()
		return nil, ErrUnavailable
	}
	body.limit, body.exact = limit, exact
	response.Body = body
	return response, nil
}
func (body *boundedBody) Read(buffer []byte) (int, error) {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	return body.readLocked(buffer)
}
func (body *boundedBody) readLocked(buffer []byte) (int, error) {
	if err := body.operation.ctx.Err(); err != nil {
		body.operation.failed()
		return 0, err
	}
	maximum := body.limit - body.read + 1
	if maximum <= 0 {
		body.operation.failed()
		return 0, ErrUnavailable
	}
	count, err := body.body.Read(buffer[:min(int64(len(buffer)), maximum)])
	body.read += int64(count)
	if body.read > body.limit {
		body.operation.failed()
		return count, ErrUnavailable
	}
	if err != nil && !errors.Is(err, io.EOF) {
		body.operation.failed()
	}
	return count, err
}
func (body *boundedBody) Close() error {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return body.closeError
	}
	if body.exact {
		if body.read != body.limit {
			body.operation.failed()
		} else {
			var overflow [1]byte
			count, err := body.readLocked(overflow[:])
			if count != 0 || !errors.Is(err, io.EOF) {
				body.operation.failed()
			}
		}
	}
	body.closed = true
	body.closeError = body.body.Close()
	if body.closeError != nil {
		body.operation.failed()
	}
	return body.closeError
}

func publicError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	return ErrUnavailable
}
