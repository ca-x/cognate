package packclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/v1/packv1connect"
	"google.golang.org/protobuf/proto"
)

const (
	requestTimeout  = 20 * time.Second
	maxChunkBytes   = 64 << 10
	maxArchiveBytes = 81 << 20
	maxFrames       = 16_384
	maxWireBytes    = 100 << 20
)

var (
	errOrigin   = errors.New("pack server must be an HTTPS origin or an explicit loopback HTTP origin")
	errToken    = errors.New("invalid pack share credential")
	errManifest = errors.New("invalid pack manifest")
	errTransfer = errors.New("invalid pack download framing or integrity")
	errArchive  = errors.New("invalid or unsafe pack skill archive")
	errLimit    = errors.New("pack download exceeds limits")
	errLocal    = errors.New("pack temporary storage unavailable")
)

type Client struct {
	manifest packv1connect.DistributionServiceClient
	download packv1connect.DistributionServiceClient
	token    string
	slots    chan struct{}
}

func (Client) String() string       { return "[redacted pack client]" }
func (Client) GoString() string     { return "[redacted pack client]" }
func (Client) LogValue() slog.Value { return slog.StringValue("[redacted pack client]") }
func (Client) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted pack client]")
}

func New(serverURL, shareToken string, client *http.Client) (*Client, error) {
	origin, err := parseOrigin(serverURL)
	if err != nil {
		return nil, err
	}
	if !validToken(shareToken) {
		return nil, errToken
	}
	bounded := newBoundedHTTPClient(origin, client)
	return &Client{
		manifest: packv1connect.NewDistributionServiceClient(bounded, origin.String(), connect.WithReadMaxBytes(8<<20)),
		download: packv1connect.NewDistributionServiceClient(bounded, origin.String(), connect.WithReadMaxBytes(maxChunkBytes+1024)),
		token:    shareToken, slots: make(chan struct{}, 2),
	}, nil
}

func newBoundedHTTPClient(origin *url.URL, client *http.Client) *http.Client {
	transport, timeout := http.DefaultTransport, requestTimeout
	if client != nil {
		if client.Transport != nil {
			transport = client.Transport
		}
		if client.Timeout > 0 {
			timeout = min(timeout, client.Timeout)
		}
	}
	return &http.Client{
		Transport: originTransport{base: transport, scheme: origin.Scheme, host: origin.Host}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

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
		for _, label := range strings.Split(hostname, ".") {
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

func validToken(raw string) bool {
	encoded, ok := strings.CutPrefix(raw, "shp1.")
	if !ok || len(encoded) != 143 {
		return false
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) != 107 || payload[0] != 1 || base64.RawURLEncoding.EncodeToString(payload) != encoded {
		return false
	}
	for _, letter := range payload[1:27] {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return false
		}
	}
	issued, expires := int64(binary.BigEndian.Uint64(payload[59:67])), int64(binary.BigEndian.Uint64(payload[67:75]))
	return issued > 0 && expires >= 0 && (expires == 0 || expires > issued && expires-issued <= int64(366*24*time.Hour/time.Second))
}

type originTransport struct {
	base   http.RoundTripper
	scheme string
	host   string
}

func (transport originTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.scheme || request.URL.Host != transport.host || request.URL.User != nil || request.URL.RawQuery != "" {
		return nil, errOrigin
	}
	response, err := transport.base.RoundTrip(request)
	if err == nil && response != nil && response.Body != nil {
		response.Body = &boundedBody{ReadCloser: response.Body, remaining: maxWireBytes}
	}
	return response, err
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (body *boundedBody) Read(buffer []byte) (int, error) {
	if body.remaining <= 0 {
		return 0, errLimit
	}
	count, err := body.ReadCloser.Read(buffer[:min(int64(len(buffer)), body.remaining)])
	body.remaining -= int64(count)
	return count, err
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if connect.CodeOf(err) == connect.CodeDeadlineExceeded {
		return context.DeadlineExceeded
	}
	if connect.CodeOf(err) == connect.CodeCanceled {
		return context.Canceled
	}
	return connect.NewError(connect.CodeOf(err), errors.New("pack request failed"))
}

func (client *Client) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil || client.slots == nil {
		return nil, errors.New("pack client is not initialized")
	}
	select {
	case client.slots <- struct{}{}:
		return func() { <-client.slots }, nil
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("pack client is busy"))
	}
}

func (client *Client) Manifest(ctx context.Context) (*packv1.GetManifestResponse, error) {
	release, err := client.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request := connect.NewRequest(&packv1.GetManifestRequest{})
	request.Header().Set("Authorization", "Bearer "+client.token)
	response, err := client.manifest.GetManifest(ctx, request)
	if err != nil {
		return nil, requestError(ctx, err)
	}
	if err := validateManifest(response.Msg); err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (client *Client) Download(ctx context.Context, manifest *packv1.GetManifestResponse, selected *packv1.PackSkill) (snapshot *Snapshot, err error) {
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	member := false
	for _, entry := range manifest.Skills {
		if proto.Equal(entry, selected) {
			member = true
			break
		}
	}
	if !member {
		return nil, errManifest
	}
	release, err := client.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return downloadSnapshot(ctx, selected.ContentHash, func(destination io.Writer) error {
		return client.receive(ctx, destination, selected, manifest.Revision)
	})
}

func downloadSnapshot(ctx context.Context, contentHash string, receive func(io.Writer) error) (snapshot *Snapshot, err error) {
	directory, err := os.MkdirTemp("", "skillhub-pack-*")
	if err != nil {
		return nil, errLocal
	}
	defer func() {
		if snapshot == nil {
			if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
				err = errors.Join(err, errors.New("clean up failed pack download"))
			}
		}
	}()
	filename := filepath.Join(directory, "transfer")
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errLocal
	}
	defer file.Close()
	if err := receive(file); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errLocal
	}
	root := filepath.Join(directory, "skill")
	if err := extractSkill(ctx, file, root, contentHash); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, errLocal
	}
	if err := os.Remove(filename); err != nil {
		return nil, errLocal
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Snapshot{path: root, close: sync.OnceValue(func() error {
		if err := os.RemoveAll(directory); err != nil {
			return errors.New("remove temporary skill snapshot")
		}
		return nil
	})}, nil
}

func (client *Client) receive(ctx context.Context, destination io.Writer, selected *packv1.PackSkill, revision int64) error {
	request := connect.NewRequest(&packv1.DownloadSkillRequest{SkillId: selected.SkillId, ContentHash: selected.ContentHash, PackRevision: revision})
	request.Header().Set("Authorization", "Bearer "+client.token)
	stream, err := client.download.DownloadSkill(ctx, request)
	if err != nil {
		return requestError(ctx, err)
	}
	return receiveSkill(ctx, destination, stream, selected.ContentHash)
}

func receiveSkill(ctx context.Context, destination io.Writer, stream *connect.ServerStreamForClient[packv1.DownloadSkillResponse], contentHash string) error {
	defer stream.Close()
	digest := sha256.New()
	var offset int64
	final := false
	for count := 0; stream.Receive(); count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame := stream.Msg()
		if final || frame.Offset != offset {
			return errTransfer
		}
		if count >= maxFrames {
			return errLimit
		}
		if frame.Final {
			if offset == 0 || len(frame.Data) != 0 || frame.ContentHash != contentHash || frame.TransferSha256 != hex.EncodeToString(digest.Sum(nil)) {
				return errTransfer
			}
			final = true
			continue
		}
		if len(frame.Data) == 0 || len(frame.Data) > maxChunkBytes || frame.TransferSha256 != "" || frame.ContentHash != "" {
			return errTransfer
		}
		if int64(len(frame.Data)) > maxArchiveBytes-offset {
			return errLimit
		}
		written, err := destination.Write(frame.Data)
		if err != nil || written != len(frame.Data) {
			return errLocal
		}
		_, _ = digest.Write(frame.Data)
		offset += int64(written)
	}
	if err := stream.Err(); err != nil {
		return requestError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !final {
		return errTransfer
	}
	return nil
}

type Snapshot struct {
	path  string
	close func() error
}

func (snapshot *Snapshot) Path() string {
	if snapshot == nil {
		return ""
	}
	return snapshot.path
}

func (snapshot *Snapshot) Close() error {
	if snapshot == nil || snapshot.close == nil {
		return nil
	}
	return snapshot.close()
}
