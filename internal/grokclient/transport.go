package grokclient

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
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const servicePrefix = "/skillhub.configuration.v1.ConfigurationService/"
const authProcedure = "/skillhub.auth.v1.AuthService/GetSession"

func parseOrigin(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "?#\\") || strings.ContainsFunc(raw, func(value rune) bool { return unicode.IsSpace(value) || unicode.IsControl(value) }) {
		return nil, ErrInvalid
	}
	address, err := url.Parse(raw)
	if err != nil || address.Hostname() == "" || address.User != nil || address.Opaque != "" || address.RawPath != "" || address.Path != "" && address.Path != "/" || address.Scheme != "https" && address.Scheme != "http" {
		return nil, ErrInvalid
	}
	host := strings.ToLower(address.Hostname())
	ip, ipErr := netip.ParseAddr(host)
	if ipErr == nil {
		if ip.Zone() != "" {
			return nil, ErrInvalid
		}
	} else {
		for label := range strings.SplitSeq(host, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return nil, ErrInvalid
			}
			for _, letter := range label {
				if !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-') {
					return nil, ErrInvalid
				}
			}
		}
	}
	if address.Scheme == "http" && (ipErr != nil || !ip.IsLoopback()) {
		return nil, ErrInvalid
	}
	if address.Port() != "" {
		port, err := strconv.Atoi(address.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, ErrInvalid
		}
	} else if strings.HasSuffix(address.Host, ":") {
		return nil, ErrInvalid
	}
	address.Host = strings.ToLower(address.Host)
	address.Path = ""
	return address, nil
}
func validSession(value string) bool {
	if len(value) != 87 || value[43] != '.' {
		return false
	}
	for index, letter := range value {
		if index != 43 && !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '_' || letter == '-') {
			return false
		}
	}
	return true
}
func newTransport(origin *url.URL, settings *http.Transport) (*http.Client, *http.Transport, error) {
	configuration := &tls.Config{MinVersion: tls.VersionTLS12}
	if settings != nil && settings.TLSClientConfig != nil {
		value := settings.TLSClientConfig
		if value.InsecureSkipVerify || value.VerifyConnection != nil || value.VerifyPeerCertificate != nil || value.GetClientCertificate != nil || value.GetCertificate != nil {
			return nil, nil, ErrInvalid
		}
		if value.RootCAs != nil {
			configuration.RootCAs = value.RootCAs.Clone()
		}
	}
	owned := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: configuration, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 2, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 4096, DisableCompression: true, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	client := &http.Client{Transport: fixedTransport{base: owned, scheme: origin.Scheme, host: origin.Host}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, owned, nil
}

type fixedTransport struct {
	base         http.RoundTripper
	scheme, host string
}

func (transport fixedTransport) RoundTrip(request *http.Request) (result *http.Response, resultError error) {
	if request == nil || request.URL == nil {
		return nil, ErrInvalid
	}
	request = request.Clone(request.Context())
	if request.Body != nil {
		body := &ownedRequestBody{base: request.Body}
		request.Body = body
		request.GetBody = nil
		defer func() {
			if body.Close() != nil {
				if result != nil && result.Body != nil {
					_ = result.Body.Close()
				}
				result = nil
				resultError = ErrUnavailable
			}
		}()
	}
	if request.Context().Err() != nil {
		return nil, request.Context().Err()
	}
	if request.URL.Scheme != transport.scheme || request.URL.Host != transport.host || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.RawPath != "" || request.Method != http.MethodPost || request.Header.Get("Cookie") != "" || request.Header.Get("Idempotency-Key") != "" {
		return nil, ErrInvalid
	}
	limit := int64(1 << 20)
	switch request.URL.Path {
	case authProcedure:
		limit = 4096
	case servicePrefix + "ListAssets", servicePrefix + "GetAsset", servicePrefix + "ReadDraft", servicePrefix + "CreateAsset", servicePrefix + "UpdateAsset", servicePrefix + "CopyAsset", servicePrefix + "DeleteAsset", servicePrefix + "GetAssetOrder", servicePrefix + "ReplaceAssetOrder":
	default:
		return nil, ErrInvalid
	}
	if request.ContentLength > 128<<10 {
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

type ownedRequestBody struct {
	mutex  sync.Mutex
	base   io.ReadCloser
	closed bool
	err    error
}

func (body *ownedRequestBody) Read(data []byte) (int, error) {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return 0, ErrClosed
	}
	return body.base.Read(data)
}
func (body *ownedRequestBody) Close() error {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if !body.closed {
		body.closed = true
		body.err = body.base.Close()
	}
	return body.err
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

type wireBuffers struct {
	mutex  sync.Mutex
	values [][]byte
}

func (buffers *wireBuffers) clear() {
	buffers.mutex.Lock()
	defer buffers.mutex.Unlock()
	for _, value := range buffers.values {
		clear(value)
	}
	buffers.values = nil
}

type wireCodec struct{ buffers *wireBuffers }

func (wireCodec) Name() string { return "proto" }
func (codec wireCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok || !message.ProtoReflect().IsValid() || proto.Size(message) > 128<<10 {
		return nil, ErrInvalid
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil || len(data) > 128<<10 {
		clear(data)
		return nil, ErrInvalid
	}
	if codec.buffers != nil {
		codec.buffers.mutex.Lock()
		codec.buffers.values = append(codec.buffers.values, data)
		codec.buffers.mutex.Unlock()
	}
	return data, nil
}
func (wireCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	budget := 32768
	if !ok || !message.ProtoReflect().IsValid() || len(data) > 1<<20 || !validWire(data, message.ProtoReflect().Descriptor(), 0, &budget) {
		return ErrInvalid
	}
	candidate := message.ProtoReflect().New().Interface()
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, candidate) != nil {
		return ErrInvalid
	}
	proto.Reset(message)
	proto.Merge(message, candidate)
	return nil
}

func validWire(data []byte, descriptor protoreflect.MessageDescriptor, depth int, budget *int) bool {
	if depth > 16 {
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
		if field == nil || !field.IsList() && seen[field.Number()] > 0 || seen[field.Number()] >= 250 {
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
		switch wire {
		case protowire.BytesType:
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
					count := scalarWire(value, field)
					if count <= 0 {
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
		case protowire.VarintType, protowire.Fixed32Type, protowire.Fixed64Type:
			if expectedWire(field.Kind()) != wire {
				return false
			}
			count := scalarWire(data, field)
			if count <= 0 {
				return false
			}
			data = data[count:]
		default:
			return false
		}
	}
	return true
}
func expectedWire(kind protoreflect.Kind) protowire.Type {
	switch kind {
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return protowire.Fixed32Type
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return protowire.Fixed64Type
	case protoreflect.BoolKind, protoreflect.EnumKind, protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		return protowire.VarintType
	default:
		return -1
	}
}
func scalarWire(data []byte, field protoreflect.FieldDescriptor) int {
	switch expectedWire(field.Kind()) {
	case protowire.Fixed32Type:
		value, count := protowire.ConsumeFixed32(data)
		if count < 0 || field.Kind() == protoreflect.FloatKind && (math.IsNaN(float64(math.Float32frombits(value))) || math.IsInf(float64(math.Float32frombits(value)), 0)) {
			return -1
		}
		return count
	case protowire.Fixed64Type:
		value, count := protowire.ConsumeFixed64(data)
		if count < 0 || field.Kind() == protoreflect.DoubleKind && (math.IsNaN(math.Float64frombits(value)) || math.IsInf(math.Float64frombits(value), 0)) {
			return -1
		}
		return count
	case protowire.VarintType:
		value, count := protowire.ConsumeVarint(data)
		if count <= 0 || count != protowire.SizeVarint(value) {
			return -1
		}
		switch field.Kind() {
		case protoreflect.BoolKind:
			if value > 1 {
				return -1
			}
		case protoreflect.EnumKind:
			if value > math.MaxInt32 && value < 0xffffffff80000000 || field.Enum().Values().ByNumber(protoreflect.EnumNumber(int32(value))) == nil {
				return -1
			}
		case protoreflect.Int32Kind:
			if value > math.MaxInt32 && value < 0xffffffff80000000 {
				return -1
			}
		case protoreflect.Uint32Kind, protoreflect.Sint32Kind:
			if value > math.MaxUint32 {
				return -1
			}
		case protoreflect.Int64Kind, protoreflect.Uint64Kind, protoreflect.Sint64Kind:
		default:
			return -1
		}
		return count
	}
	return -1
}
