package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func authOrigin(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "?#\\") || strings.ContainsFunc(raw, func(character rune) bool { return unicode.IsSpace(character) || unicode.IsControl(character) }) {
		return "", errAuthRun
	}
	value, err := url.Parse(raw)
	if err != nil || value.Hostname() == "" || value.User != nil || value.Opaque != "" || value.RawPath != "" || value.Path != "" && value.Path != "/" || value.Scheme != "https" && value.Scheme != "http" {
		return "", errAuthRun
	}
	host := strings.ToLower(value.Hostname())
	address, parseErr := netip.ParseAddr(host)
	if parseErr == nil {
		if address.Zone() != "" {
			return "", errAuthRun
		}
	} else {
		for label := range strings.SplitSeq(host, ".") {
			if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return "", errAuthRun
			}
			for _, character := range label {
				if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
					return "", errAuthRun
				}
			}
		}
	}
	if value.Scheme == "http" && (parseErr != nil || !address.IsLoopback()) {
		return "", errAuthRun
	}
	port := value.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", errAuthRun
		}
	} else if strings.HasSuffix(value.Host, ":") {
		return "", errAuthRun
	}
	value.Host = host
	if strings.Contains(host, ":") {
		value.Host = "[" + host + "]"
	}
	if port != "" {
		value.Host = net.JoinHostPort(host, port)
	}
	value.Path = ""
	return value.String(), nil
}

type authTransport struct {
	authRedacted
	origin  string
	private *authPrivate
	http    *http.Client
	owned   *http.Transport
}

func newAuthTransport(origin string, private *authPrivate) *authTransport {
	owned := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, IdleConnTimeout: 5 * time.Second, DisableCompression: true, MaxConnsPerHost: 1, MaxIdleConns: 1, MaxResponseHeaderBytes: 4096}
	client := &authTransport{origin: origin, private: private, owned: owned}
	client.http = &http.Client{Transport: client, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client
}

func (client *authTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme+"://"+request.URL.Host != client.origin || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.RawPath != "" || request.URL.Fragment != "" || request.URL.User != nil || request.Method != http.MethodPost {
		return nil, errAuthRun
	}
	switch request.URL.Path {
	case authv1connect.AuthServiceLoginProcedure, authv1connect.AuthServiceGetSessionProcedure, authv1connect.AuthServiceLogoutProcedure:
	default:
		return nil, errAuthRun
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.owned.RoundTrip(request)
	if err != nil {
		return nil, errAuthRun
	}
	if response == nil || response.Body == nil {
		return nil, errAuthRun
	}
	if response.ContentLength > 4096 || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		_ = response.Body.Close()
		return nil, errAuthRun
	}
	response.Body = &authBody{ReadCloser: response.Body, remaining: 4097}
	return response, nil
}

type authBody struct {
	io.ReadCloser
	remaining int64
}

func (body *authBody) Read(data []byte) (int, error) {
	if body.remaining <= 0 {
		return 0, errAuthRun
	}
	if int64(len(data)) > body.remaining {
		data = data[:body.remaining]
	}
	count, err := body.ReadCloser.Read(data)
	body.remaining -= int64(count)
	if body.remaining == 0 {
		return count, errAuthRun
	}
	return count, err
}

func authContext(caller context.Context) (context.Context, func()) {
	deadline := time.Now().Add(20 * time.Second)
	if earlier, ok := caller.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	joined := make(chan struct{})
	stop := context.AfterFunc(caller, func() { cancel(); close(joined) })
	if caller.Err() != nil {
		cancel()
	}
	return ctx, func() {
		cancel()
		if !stop() {
			<-joined
		}
	}
}

func (client *authTransport) rpc() authv1connect.AuthServiceClient {
	return authv1connect.NewAuthServiceClient(client.http, client.origin, connect.WithCodec(authCodec{private: client.private}), connect.WithAcceptCompression("gzip", nil, nil), connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(4096))
}

func (client *authTransport) login(caller context.Context) error {
	ctx, finish := authContext(caller)
	defer finish()
	material := client.private.material()
	request := connect.NewRequest(&authv1.LoginRequest{Username: string(material.username), Password: string(material.password)})
	defer func() {
		request.Msg.Reset()
		clear(material.username)
		clear(material.password)
		material.username, material.password = nil, nil
	}()
	response, err := client.rpc().Login(ctx, request)
	if err != nil || response == nil || response.Msg == nil {
		return errAuthRun
	}
	defer response.Msg.Reset()
	if !authValidToken(response.Msg.SessionToken) {
		return errAuthRun
	}
	material.token = []byte(response.Msg.SessionToken)
	expiry := time.Unix(response.Msg.ExpiresAtUnix, 0)
	if ctx.Err() != nil || !expiry.After(time.Now()) || expiry.After(time.Now().Add(24*time.Hour+time.Minute)) {
		return errAuthRun
	}
	material.expires = expiry
	return nil
}

func (client *authTransport) identify(caller context.Context) error {
	ctx, finish := authContext(caller)
	defer finish()
	request := connect.NewRequest(&authv1.GetSessionRequest{})
	request.Header().Set("Authorization", "Bearer "+string(client.private.material().token))
	defer request.Header().Del("Authorization")
	response, err := client.rpc().GetSession(ctx, request)
	if err != nil || response == nil || response.Msg == nil {
		return errAuthRun
	}
	defer response.Msg.Reset()
	owner := response.Msg.UserId
	if ctx.Err() != nil || len(owner) == 0 || len(owner) > 512 || !utf8.ValidString(owner) {
		return errAuthRun
	}
	for _, character := range owner {
		if unicode.IsControl(character) || unicode.IsSpace(character) || unicode.Is(unicode.Bidi_Control, character) {
			return errAuthRun
		}
	}
	client.private.material().owner = owner
	return nil
}

func (client *authTransport) logout(caller context.Context) error {
	ctx, finish := authContext(caller)
	defer finish()
	request := connect.NewRequest(&authv1.LogoutRequest{})
	request.Header().Set("Authorization", "Bearer "+string(client.private.material().token))
	defer request.Header().Del("Authorization")
	_, err := client.rpc().Logout(ctx, request)
	if err != nil || ctx.Err() != nil {
		return errAuthCleanup
	}
	return nil
}

func (client *authTransport) close() { client.owned.CloseIdleConnections() }

func authValidToken(value string) bool {
	if len(value) != 87 || value[43] != '.' {
		return false
	}
	for index, character := range value {
		if index != 43 && !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

type authCodec struct{ private *authPrivate }

func (authCodec) Name() string { return "proto" }
func (codec authCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok || proto.Size(message) > 4096 {
		return nil, errAuthRun
	}
	buffer, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return nil, errAuthRun
	}
	codec.private.material().buffers = append(codec.private.material().buffers, buffer)
	return buffer, nil
}
func (codec authCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok || len(data) > 4096 {
		return errAuthRun
	}
	data = bytes.Clone(data)
	codec.private.material().buffers = append(codec.private.material().buffers, data)
	descriptor := message.ProtoReflect().Descriptor()
	seen := map[protowire.Number]bool{}
	for remaining := data; len(remaining) > 0; {
		number, wire, prefix := protowire.ConsumeTag(remaining)
		if prefix <= 0 || prefix != protowire.SizeVarint(uint64(number)<<3|uint64(wire)) || seen[number] {
			return errAuthRun
		}
		seen[number] = true
		field := descriptor.Fields().ByNumber(number)
		if field == nil {
			return errAuthRun
		}
		remaining = remaining[prefix:]
		switch wire {
		case protowire.BytesType:
			text, size := protowire.ConsumeBytes(remaining)
			if field.Kind().String() != "string" || size < 0 || size-len(text) != protowire.SizeVarint(uint64(len(text))) || !utf8.Valid(text) {
				return errAuthRun
			}
			remaining = remaining[size:]
		case protowire.VarintType:
			integer, size := protowire.ConsumeVarint(remaining)
			if field.Kind().String() != "int64" || size <= 0 || integer > 1<<63-1 || size != protowire.SizeVarint(integer) {
				return errAuthRun
			}
			remaining = remaining[size:]
		default:
			return errAuthRun
		}
	}
	if proto.Unmarshal(data, message) != nil {
		return errAuthRun
	}
	return nil
}
