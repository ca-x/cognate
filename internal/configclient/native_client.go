package configclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

var (
	ErrNativeInvalid     = errors.New("invalid native configuration message")
	ErrNativeDenied      = errors.New("native configuration authorization denied")
	ErrNativeUnavailable = errors.New("native configuration unavailable")
	ErrNativeCanceled    = errors.New("native configuration canceled")
	ErrNativeUnknown     = errors.New("native materialization outcome unknown; inspect status")
	ErrNativeConflict    = errors.New("native configuration changed")
)

type nativeSecretData struct {
	mutex sync.Mutex
	data  *[]byte
}
type nativeSecret struct{ payload *nativeSecretData }
type NativeMaterial struct{ nativeSecret }

func (value NativeMaterial) Clone() NativeMaterial {
	owned := value.CloneBytes()
	defer clear(owned)
	return NativeMaterial{newNativeSecret(owned)}
}

func newNativeSecret(data []byte) nativeSecret {
	return nativeSecret{&nativeSecretData{data: new(bytes.Clone(data))}}
}
func (value nativeSecret) CloneBytes() []byte {
	if value.payload == nil {
		return nil
	}
	value.payload.mutex.Lock()
	defer value.payload.mutex.Unlock()
	return bytes.Clone(*value.payload.data)
}
func (value nativeSecret) Destroy() {
	if value.payload == nil {
		return
	}
	value.payload.mutex.Lock()
	defer value.payload.mutex.Unlock()
	clear(*value.payload.data)
	*value.payload.data = nil
}
func (nativeSecret) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted native material]")
}
func (nativeSecret) String() string               { return "[redacted native material]" }
func (nativeSecret) GoString() string             { return "[redacted native material]" }
func (nativeSecret) MarshalJSON() ([]byte, error) { return []byte(`"[redacted native material]"`), nil }
func (nativeSecret) MarshalText() ([]byte, error) { return []byte("[redacted native material]"), nil }
func (nativeSecret) LogValue() slog.Value         { return slog.StringValue("[redacted native material]") }

type NativeClient struct {
	origin    string
	http      *http.Client
	transport *http.Transport
	ctx       context.Context
	cancel    context.CancelFunc
	mutex     sync.Mutex
	closed    bool
	workers   sync.WaitGroup
	flows     []*NativeFlow
}

func NewNativeClient(origin string, settings *http.Transport) (*NativeClient, error) {
	parsed, err := url.Parse(origin)
	if err != nil || !validOrigin(origin) || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || parsed.RawPath != "" || parsed.Opaque != "" {
		return nil, ErrNativeInvalid
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, DisableCompression: true, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 16 << 10, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if settings != nil {
		transport.DisableKeepAlives = settings.DisableKeepAlives
		transport.ForceAttemptHTTP2 = settings.ForceAttemptHTTP2
		if settings.MaxConnsPerHost > 0 {
			transport.MaxConnsPerHost = min(settings.MaxConnsPerHost, 8)
		}
		if settings.TLSClientConfig != nil && settings.TLSClientConfig.RootCAs != nil {
			transport.TLSClientConfig.RootCAs = settings.TLSClientConfig.RootCAs.Clone()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &NativeClient{origin: origin, transport: transport, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrNativeDenied }}, ctx: ctx, cancel: cancel}, nil
}

func (client *NativeClient) Close() {
	client.mutex.Lock()
	client.closed = true
	client.cancel()
	client.mutex.Unlock()
	client.workers.Wait()
	client.mutex.Lock()
	defer client.mutex.Unlock()
	for _, flow := range client.flows {
		flow.destroy()
	}
	client.transport.CloseIdleConnections()
}

func (client *NativeClient) operation(caller context.Context) (context.Context, func(), error) {
	client.mutex.Lock()
	if client.closed {
		client.mutex.Unlock()
		return nil, nil, ErrNativeCanceled
	}
	client.workers.Add(1)
	client.mutex.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	callerDone, clientDone := make(chan struct{}), make(chan struct{})
	stopCaller := context.AfterFunc(caller, func() { cancel(); close(callerDone) })
	stopClient := context.AfterFunc(client.ctx, func() { cancel(); close(clientDone) })
	if caller.Err() != nil || client.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() {
		cancel()
		if !stopCaller() {
			<-callerDone
		}
		if !stopClient() {
			<-clientDone
		}
		client.workers.Done()
	}, nil
}

func (client *NativeClient) call(ctx context.Context, method, bearer string, request, response proto.Message, bootstrap bool) error {
	limit := nativeWireLimit
	if bootstrap {
		limit = nativeBootstrapLimit
	}
	data, err := nativeEncode(request, limit)
	if err != nil {
		return err
	}
	defer clear(data)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+"/skillhub.configuration.native.v1.NativeConfigurationService/"+method, io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		return ErrNativeInvalid
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Connect-Timeout-Ms", "20000")
	result, err := client.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ErrNativeCanceled
		}
		return ErrNativeUnavailable
	}
	defer result.Body.Close()
	encoded, readErr := io.ReadAll(io.LimitReader(result.Body, int64(limit)+1))
	defer clear(encoded)
	if ctx.Err() != nil {
		return ErrNativeCanceled
	}
	if readErr != nil || len(encoded) > limit {
		return ErrNativeInvalid
	}
	if result.TLS == nil || len(result.TLS.VerifiedChains) == 0 || result.Header.Get("Cache-Control") != "no-store" || len(result.Header.Values("Set-Cookie")) > 0 || result.Header.Get("Content-Encoding") != "" {
		return ErrNativeInvalid
	}
	if result.StatusCode != http.StatusOK {
		switch result.StatusCode {
		case 401, 403:
			return ErrNativeDenied
		case 409, 412:
			return ErrNativeConflict
		default:
			return ErrNativeUnavailable
		}
	}
	if strings.TrimSpace(result.Header.Get("Content-Type")) != "application/proto" {
		return ErrNativeInvalid
	}
	return nativeDecode(encoded, response, limit)
}
