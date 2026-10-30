package packattachments

import (
	"crypto/tls"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
)

const servicePrefix = "/skillhub.pack.configuration.v1.PackConfigurationService/"

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

func newTransport(origin *url.URL, settings *http.Transport) (*http.Client, *http.Transport, error) {
	configuration := &tls.Config{MinVersion: tls.VersionTLS12}
	if settings != nil && settings.TLSClientConfig != nil {
		supplied := settings.TLSClientConfig
		if supplied.InsecureSkipVerify || supplied.VerifyPeerCertificate != nil || supplied.VerifyConnection != nil {
			return nil, nil, ErrInvalid
		}
		if supplied.RootCAs != nil {
			configuration.RootCAs = supplied.RootCAs.Clone()
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

func (transport fixedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.scheme || request.URL.Host != transport.host || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.RawPath != "" || request.Method != http.MethodPost {
		return nil, ErrInvalid
	}
	limit := int64(64 << 10)
	switch request.URL.Path {
	case "/skillhub.auth.v1.AuthService/GetSession":
		limit = 4096
	case servicePrefix + "GetAttachments", servicePrefix + "ReplaceAttachments":
	default:
		return nil, ErrInvalid
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Del("Idempotency-Key")
	request.Header.Del("X-Idempotency-Key")
	request.GetBody = nil
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

type attachmentCodec struct{}

func (attachmentCodec) Name() string { return "proto" }
func (attachmentCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok || !message.ProtoReflect().IsValid() || proto.Size(message) > 64<<10 {
		return nil, ErrInvalid
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil || len(data) > 64<<10 {
		clear(data)
		return nil, ErrInvalid
	}
	return data, nil
}
func (attachmentCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	budget := 64 << 10
	if !ok || !message.ProtoReflect().IsValid() || len(data) > 64<<10 || !validWire(data, message.ProtoReflect().Descriptor(), 0, &budget) {
		return ErrInvalid
	}
	candidate := message.ProtoReflect().New().Interface()
	if (proto.UnmarshalOptions{RecursionLimit: 24}).Unmarshal(data, candidate) != nil {
		return ErrInvalid
	}
	proto.Reset(message)
	proto.Merge(message, candidate)
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
