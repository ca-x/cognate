package githubproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxURLLength = 4096

var (
	ErrInvalidURL       = errors.New("invalid GitHub forward proxy URL")
	ErrDefaultTransport = errors.New("default HTTP transport is unavailable")
	ErrRequestFailed    = errors.New("GitHub proxy transport request failed")
)

type Transport struct {
	base     *http.Transport
	fallback http.RoundTripper
}

func NewTransport(rawURL string) (*Transport, error) {
	var proxyURL *url.URL
	if rawURL != "" {
		var err error
		proxyURL, err = parseURL(rawURL)
		if err != nil {
			return nil, err
		}
	}
	defaultTransport := http.DefaultTransport
	base, ok := defaultTransport.(*http.Transport)
	if !ok && rawURL == "" && defaultTransport != nil {
		return &Transport{fallback: defaultTransport}, nil
	}
	if !ok || base == nil {
		return nil, ErrDefaultTransport
	}
	owned := base.Clone()
	if proxyURL != nil {
		owned.Proxy = http.ProxyURL(proxyURL)
	}
	return &Transport{base: owned}, nil
}

func (transport *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	upstream := transport.fallback
	if transport.base != nil {
		upstream = transport.base
	}
	response, err := upstream.RoundTrip(request)
	if err == nil {
		return response, nil
	}
	contextError := request.Context().Err()
	if errors.Is(contextError, context.Canceled) || errors.Is(err, context.Canceled) {
		return nil, context.Canceled
	}
	if errors.Is(contextError, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return nil, context.DeadlineExceeded
	}
	networkError, ok := errors.AsType[net.Error](err)
	return nil, requestError{timedOut: ok && networkError.Timeout()}
}

func (transport *Transport) CloseIdleConnections() {
	if transport.base != nil {
		transport.base.CloseIdleConnections()
	}
}

func (*Transport) String() string { return "[redacted GitHub proxy transport]" }

func (*Transport) GoString() string { return "[redacted GitHub proxy transport]" }

type requestError struct {
	timedOut bool
}

func (requestError) Error() string { return ErrRequestFailed.Error() }

func (requestError) Unwrap() error { return ErrRequestFailed }

func (err requestError) Timeout() bool { return err.timedOut }

func (requestError) Temporary() bool { return false }

func parseURL(rawURL string) (*url.URL, error) {
	if len(rawURL) > maxURLLength || !utf8.ValidString(rawURL) || strings.ContainsAny(rawURL, "?#\\") || strings.ContainsFunc(rawURL, func(value rune) bool {
		return unicode.IsControl(value) || unicode.IsSpace(value)
	}) {
		return nil, ErrInvalidURL
	}
	proxyURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrInvalidURL
	}
	switch proxyURL.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, ErrInvalidURL
	}
	if proxyURL.Opaque != "" || proxyURL.Hostname() == "" || proxyURL.RawQuery != "" || proxyURL.ForceQuery || proxyURL.Fragment != "" || proxyURL.RawPath != "" || proxyURL.Path != "" && proxyURL.Path != "/" {
		return nil, ErrInvalidURL
	}
	host := proxyURL.Hostname()
	if strings.HasPrefix(proxyURL.Host, "[") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() {
			return nil, ErrInvalidURL
		}
	} else if strings.ContainsAny(host, ":[]") {
		return nil, ErrInvalidURL
	}
	if port := proxyURL.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, ErrInvalidURL
		}
	} else if strings.HasSuffix(proxyURL.Host, ":") {
		return nil, ErrInvalidURL
	}
	if strings.ContainsFunc(host, func(value rune) bool { return unicode.IsControl(value) || unicode.IsSpace(value) }) {
		return nil, ErrInvalidURL
	}
	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		if !utf8.ValidString(user.Username()) || !utf8.ValidString(password) || strings.ContainsFunc(user.Username(), unicode.IsControl) || strings.ContainsFunc(password, unicode.IsControl) {
			return nil, ErrInvalidURL
		}
	}
	return proxyURL, nil
}
