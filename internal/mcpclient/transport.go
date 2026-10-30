package mcpclient

import (
	"crypto/tls"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const servicePrefix = "/skillhub.configuration.mcp.v1.MCPConfigurationService/"

func parseOrigin(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "?#\\") || strings.ContainsFunc(raw, func(character rune) bool { return unicode.IsSpace(character) || unicode.IsControl(character) }) {
		return nil, ErrInvalid
	}
	value, err := url.Parse(raw)
	if err != nil || value.Hostname() == "" || value.User != nil || value.Opaque != "" || value.RawPath != "" || value.Path != "" && value.Path != "/" || value.Scheme != "https" && value.Scheme != "http" {
		return nil, ErrInvalid
	}
	host := strings.ToLower(value.Hostname())
	address, addressErr := netip.ParseAddr(host)
	if addressErr == nil {
		if address.Zone() != "" {
			return nil, ErrInvalid
		}
	} else {
		for label := range strings.SplitSeq(host, ".") {
			if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return nil, ErrInvalid
			}
			for _, character := range label {
				if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
					return nil, ErrInvalid
				}
			}
		}
	}
	if value.Scheme == "http" && host != "localhost" && (addressErr != nil || !address.IsLoopback()) {
		return nil, ErrInvalid
	}
	if port := value.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, ErrInvalid
		}
	} else if strings.HasSuffix(value.Host, ":") {
		return nil, ErrInvalid
	}
	value.Path = ""
	return value, nil
}

func validSession(value string) bool {
	if len(value) != 87 || value[43] != '.' {
		return false
	}
	for index, character := range value {
		if index == 43 {
			continue
		}
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func newTransport(origin *url.URL, supplied *http.Client) (*http.Client, *http.Transport) {
	owned := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 2, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 64 << 10, DisableCompression: true, ForceAttemptHTTP2: true}
	var base http.RoundTripper = owned
	timeout := 20 * time.Second
	if supplied != nil {
		if supplied.Transport != nil {
			base = supplied.Transport
			owned = nil
		}
		if supplied.Timeout > 0 {
			timeout = min(timeout, supplied.Timeout)
		}
	}
	return &http.Client{Transport: fixedTransport{base: base, scheme: origin.Scheme, host: origin.Host}, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, owned
}

type fixedTransport struct {
	base         http.RoundTripper
	scheme, host string
}

func (transport fixedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.scheme || request.URL.Host != transport.host || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.RawPath != "" || request.Method != http.MethodPost {
		return nil, ErrInvalid
	}
	limit := int64(2 << 20)
	switch request.URL.Path {
	case "/skillhub.auth.v1.AuthService/GetSession":
		limit = 4096
	case servicePrefix + "ListAssets", servicePrefix + "GetAsset", servicePrefix + "ReadDraft", servicePrefix + "CreateAsset", servicePrefix + "UpdateAsset", servicePrefix + "CopyAsset", servicePrefix + "DeleteAsset", servicePrefix + "Preview":
	default:
		return nil, ErrInvalid
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, ErrUnavailable
	}
	if response == nil || response.Body == nil {
		return nil, ErrInvalid
	}
	if response.ContentLength > limit || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		_ = response.Body.Close()
		return nil, ErrInvalid
	}
	response.Body = &limitedBody{ReadCloser: response.Body, remaining: limit + 1}
	return response, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (body *limitedBody) Read(data []byte) (int, error) {
	if body.remaining <= 0 {
		return 0, ErrInvalid
	}
	if int64(len(data)) > body.remaining {
		data = data[:body.remaining]
	}
	count, err := body.ReadCloser.Read(data)
	body.remaining -= int64(count)
	if body.remaining == 0 {
		return count, ErrInvalid
	}
	return count, err
}

type mcpCodec struct{ state *clientState }

func (mcpCodec) Name() string { return "proto" }
func (codec mcpCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok || proto.Size(message) > 2<<20 {
		return nil, ErrInvalid
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil || len(data) > 2<<20 {
		clear(data)
		return nil, ErrInvalid
	}
	if codec.state != nil {
		codec.state.mutex.Lock()
		codec.state.buffers = append(codec.state.buffers, data)
		codec.state.mutex.Unlock()
	}
	return data, nil
}
func (mcpCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	budget := 2 << 20
	if !ok || len(data) > 2<<20 || !validWire(data, message.ProtoReflect().Descriptor(), 0, &budget) {
		return ErrInvalid
	}
	if (proto.UnmarshalOptions{RecursionLimit: 24}).Unmarshal(data, message) != nil {
		return ErrInvalid
	}
	return nil
}

func validWire(data []byte, descriptor protoreflect.MessageDescriptor, depth int, budget *int) bool {
	if depth > 24 {
		return false
	}
	seen := map[protoreflect.FieldNumber]int{}
	oneofs := map[protoreflect.FullName]bool{}
	for len(data) > 0 {
		*budget--
		if *budget < 0 {
			return false
		}
		number, wire, prefix := protowire.ConsumeTag(data)
		if prefix <= 0 || prefix != protowire.SizeVarint(uint64(number)<<3|uint64(wire)) {
			return false
		}
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(number))
		if field == nil || !field.IsList() && seen[field.Number()] > 0 || seen[field.Number()] >= 1024 {
			return false
		}
		seen[field.Number()]++
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.FullName()] {
				return false
			}
			oneofs[oneof.FullName()] = true
		}
		data = data[prefix:]
		if wire == protowire.BytesType {
			value, size := protowire.ConsumeBytes(data)
			if size < 0 || size-len(value) != protowire.SizeVarint(uint64(len(value))) {
				return false
			}
			switch field.Kind() {
			case protoreflect.MessageKind:
				if !validWire(value, field.Message(), depth+1, budget) {
					return false
				}
			case protoreflect.StringKind:
				if !utf8.Valid(value) {
					return false
				}
			case protoreflect.BytesKind:
			default:
				if !field.IsList() || !field.IsPacked() {
					return false
				}
				for len(value) > 0 {
					encoded, count := protowire.ConsumeVarint(value)
					if !validVarint(field, encoded, count) {
						return false
					}
					value = value[count:]
					*budget--
					if *budget < 0 {
						return false
					}
				}
			}
			data = data[size:]
		} else {
			if wire != protowire.VarintType || field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.StringKind || field.Kind() == protoreflect.BytesKind {
				return false
			}
			value, count := protowire.ConsumeVarint(data)
			if !validVarint(field, value, count) {
				return false
			}
			data = data[count:]
		}
	}
	return true
}

func validVarint(field protoreflect.FieldDescriptor, value uint64, count int) bool {
	if count <= 0 || count != protowire.SizeVarint(value) {
		return false
	}
	switch field.Kind() {
	case protoreflect.BoolKind:
		return value <= 1
	case protoreflect.EnumKind:
		return value <= math.MaxInt32 && field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) != nil
	case protoreflect.Uint32Kind:
		return value <= math.MaxUint32
	case protoreflect.Int64Kind:
		return value <= math.MaxInt64
	default:
		return false
	}
}
