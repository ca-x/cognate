package importclient

import (
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const requestTimeout = 20 * time.Second
const maxResponseBytes = 1 << 20

var errOrigin = errors.New("import server must be an HTTPS origin or an explicit loopback HTTP origin")

func parseOrigin(raw string) (*url.URL, error) {
	if len(raw) > 2048 || strings.ContainsAny(raw, "?#\\") || strings.ContainsFunc(raw, unicode.IsSpace) || strings.ContainsFunc(raw, unicode.IsControl) {
		return nil, errOrigin
	}
	origin, err := url.Parse(raw)
	if err != nil || origin.Hostname() == "" || origin.User != nil || origin.Opaque != "" || origin.RawPath != "" || origin.Path != "" && origin.Path != "/" || origin.Scheme != "https" && origin.Scheme != "http" {
		return nil, errOrigin
	}
	hostname := strings.ToLower(origin.Hostname())
	address, addressErr := netip.ParseAddr(hostname)
	if addressErr != nil {
		for label := range strings.SplitSeq(hostname, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return nil, errOrigin
			}
			for _, letter := range label {
				if !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-') {
					return nil, errOrigin
				}
			}
		}
	} else if address.Zone() != "" {
		return nil, errOrigin
	}
	if origin.Scheme == "http" && hostname != "localhost" && (addressErr != nil || !address.IsLoopback()) {
		return nil, errOrigin
	}
	if port := origin.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errOrigin
		}
	} else if strings.HasSuffix(origin.Host, ":") {
		return nil, errOrigin
	}
	origin.Path = ""
	return origin, nil
}

func boundedHTTPClient(origin *url.URL, supplied *http.Client) *http.Client {
	transport, timeout := http.DefaultTransport, requestTimeout
	if supplied != nil {
		if supplied.Transport != nil {
			transport = supplied.Transport
		}
		if supplied.Timeout > 0 {
			timeout = min(timeout, supplied.Timeout)
		}
	}
	return &http.Client{
		Transport: originTransport{base: transport, scheme: origin.Scheme, host: origin.Host}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type originTransport struct {
	base         http.RoundTripper
	scheme, host string
}

func (transport originTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.scheme || request.URL.Host != transport.host || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return nil, errOrigin
	}
	response, err := transport.base.RoundTrip(request)
	if err == nil && response != nil && response.Body != nil {
		response.Body = &boundedBody{ReadCloser: response.Body, remaining: maxResponseBytes + 1}
	}
	return response, err
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (body *boundedBody) Read(buffer []byte) (int, error) {
	if body.remaining <= 0 {
		return 0, errors.New("import response exceeds limits")
	}
	count, err := body.ReadCloser.Read(buffer[:min(int64(len(buffer)), body.remaining)])
	body.remaining -= int64(count)
	return count, err
}
