package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/protobuf/proto"
)

type representativeDistributionFixture struct {
	service *packs.Service
	handler http.Handler
	pack    state.Pack
	token   packs.ShareToken
	source  *representativeDeadlineArchiveSource
}

func newRepresentativeDistributionFixture(test *testing.T) representativeDistributionFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := make([]byte, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, err := repository.ParseReference("org/representative-lifecycle", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "Representative dummy skill", Path: "skills/example"}, ContentHash: strings.Repeat("a", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil || len(detail.Skills) != 1 {
		test.Fatalf("prepare collected skill: %v", err)
	}
	created, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Example", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	source := &representativeDeadlineArchiveSource{started: make(chan context.Context, 1), finished: make(chan struct{})}
	service, err := packs.NewService(store, source, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(service.Close)
	issued, err := service.CreateShare(test.Context(), "alice", created.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	return representativeDistributionFixture{service: service, handler: NewWithOptions(authentication, Options{Store: store, Packs: service}), pack: created, token: issued.Token, source: source}
}

type representativeReadWatchWriter struct {
	http.ResponseWriter
	ready chan<- struct{}
}

func (writer representativeReadWatchWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer representativeReadWatchWriter) SetReadDeadline(deadline time.Time) error {
	err := http.NewResponseController(writer.ResponseWriter).SetReadDeadline(deadline)
	if err == nil && deadline.After(time.Now()) {
		select {
		case writer.ready <- struct{}{}:
		default:
		}
	}
	return err
}

func TestRepresentativeDistributionCloseUnblocksPartialRequestBody(test *testing.T) {
	fixture := newRepresentativeDistributionFixture(test)
	ready := make(chan struct{}, 2)
	handlerFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(handlerFinished)
		fixture.handler.ServeHTTP(representativeReadWatchWriter{ResponseWriter: writer, ready: ready}, request)
	}))
	test.Cleanup(server.Close)
	connection, err := net.DialTimeout("tcp4", server.Listener.Addr().String(), time.Second)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		test.Fatal(err)
	}
	if _, err := fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/connect+proto\r\nConnect-Protocol-Version: 1\r\nConnect-Timeout-Ms: 4000\r\nContent-Length: 100\r\nAuthorization: Bearer %s\r\n\r\n", packv1connect.DistributionServiceDownloadSkillProcedure, server.Listener.Addr(), fixture.token.Reveal()); err != nil {
		test.Fatalf("write partial request headers: %v", err)
	}
	for range 2 {
		select {
		case <-ready:
		case <-time.After(time.Second):
			test.Fatal("partial request did not bind both middleware and effective RPC deadlines")
		}
	}
	closed := make(chan struct{})
	go func() { fixture.service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		test.Fatal("Close did not interrupt the partial request body read")
	}
	select {
	case <-handlerFinished:
	case <-time.After(time.Second):
		test.Fatal("HTTP handler stayed blocked after Close")
	}
	select {
	case <-fixture.source.started:
		test.Fatal("partial request unexpectedly reached archive extraction")
	default:
	}
}

func TestRepresentativeDistributionNormalProtocolCancelPreservesKeepAlive(test *testing.T) {
	for _, mode := range []string{"no-rpc-timeout", "future-rpc-timeout"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newRepresentativeDistributionFixture(test)
			_, protocol := packv1connect.NewDistributionServiceHandler(&distributionHandler{service: fixture.service}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(8<<20), connect.WithInterceptors(distributionDeadlines{}))
			server := httptest.NewServer(distributionRequests(fixture.service, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				protocol.ServeHTTP(writer, request)
				time.Sleep(20 * time.Millisecond)
			})))
			test.Cleanup(server.Close)
			connection, err := net.DialTimeout("tcp4", server.Listener.Addr().String(), time.Second)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = connection.Close() })
			if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				test.Fatal(err)
			}
			reader := bufio.NewReader(connection)
			for attempt := range 2 {
				request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+packv1connect.DistributionServiceGetManifestProcedure, nil)
				if err != nil {
					test.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/proto")
				request.Header.Set("Connect-Protocol-Version", "1")
				if mode == "future-rpc-timeout" {
					request.Header.Set("Connect-Timeout-Ms", "1000")
				}
				request.Header.Set("Authorization", "Bearer "+fixture.token.Reveal())
				if err := request.Write(connection); err != nil {
					test.Fatalf("write normal request %d on the same connection: %v", attempt+1, err)
				}
				response, err := http.ReadResponse(reader, request)
				if err != nil {
					test.Fatalf("read normal response %d before its deadline: %v", attempt+1, err)
				}
				data, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil || response.StatusCode != http.StatusOK || response.Close {
					test.Fatalf("normal response %d lost keep-alive or was truncated: status=%d close=%t read=%v", attempt+1, response.StatusCode, response.Close, readErr)
				}
				var manifest packv1.GetManifestResponse
				if err := proto.Unmarshal(data, &manifest); err != nil || manifest.PackId != fixture.pack.ID || len(manifest.Skills) != 1 {
					test.Fatalf("normal response %d was not the expected manifest: %v", attempt+1, err)
				}
			}
		})
	}
}

func TestRepresentativeDistributionHTTP2BackpressureCancellation(test *testing.T) {
	for _, reason := range []string{"deadline", "close"} {
		test.Run(reason, func(test *testing.T) {
			fixture := newRepresentativeDistributionFixture(test)
			server := httptest.NewUnstartedServer(fixture.handler)
			server.EnableHTTP2 = true
			server.StartTLS()
			test.Cleanup(server.Close)
			configuration := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			configuration.NextProtos = []string{"h2"}
			connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp4", server.Listener.Addr().String(), configuration)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = connection.Close() })
			if connection.ConnectionState().NegotiatedProtocol != "h2" {
				test.Fatal("fixture did not negotiate HTTP/2")
			}
			if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				test.Fatal(err)
			}
			if _, err := io.WriteString(connection, http2.ClientPreface); err != nil {
				test.Fatal(err)
			}
			framer := http2.NewFramer(connection, connection)
			if err := framer.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 1024}); err != nil {
				test.Fatal(err)
			}
			payload, err := proto.Marshal(&packv1.DownloadSkillRequest{SkillId: fixture.pack.Items[0].SkillID, ContentHash: fixture.pack.Items[0].ContentHash, PackRevision: fixture.pack.Revision})
			if err != nil {
				test.Fatal(err)
			}
			envelope := make([]byte, 5+len(payload))
			binary.BigEndian.PutUint32(envelope[1:5], uint32(len(payload)))
			copy(envelope[5:], payload)
			var headers bytes.Buffer
			encoder := hpack.NewEncoder(&headers)
			fields := []hpack.HeaderField{
				{Name: ":method", Value: http.MethodPost},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: server.Listener.Addr().String()},
				{Name: ":path", Value: packv1connect.DistributionServiceDownloadSkillProcedure},
				{Name: "content-type", Value: "application/connect+proto"},
				{Name: "content-length", Value: strconv.Itoa(len(envelope))},
				{Name: "connect-protocol-version", Value: "1"},
				{Name: "authorization", Value: "Bearer " + fixture.token.Reveal(), Sensitive: true},
			}
			if reason == "deadline" {
				fields = append(fields, hpack.HeaderField{Name: "connect-timeout-ms", Value: "200"})
			}
			for _, field := range fields {
				if err := encoder.WriteField(field); err != nil {
					test.Fatal("encode HTTP/2 request headers")
				}
			}
			if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: headers.Bytes()}); err != nil {
				test.Fatal(err)
			}
			if err := framer.WriteData(1, true, envelope); err != nil {
				test.Fatal(err)
			}
			for {
				frame, err := framer.ReadFrame()
				if err != nil {
					test.Fatalf("read initial HTTP/2 stream: %v", err)
				}
				if settings, ok := frame.(*http2.SettingsFrame); ok && !settings.IsAck() {
					if err := framer.WriteSettingsAck(); err != nil {
						test.Fatal(err)
					}
				}
				if data, ok := frame.(*http2.DataFrame); ok && len(data.Data()) != 0 {
					break
				}
			}
			var sourceContext context.Context
			select {
			case sourceContext = <-fixture.source.started:
			case <-time.After(time.Second):
				test.Fatal("HTTP/2 request did not reach archive source")
			}
			select {
			case <-fixture.source.finished:
				test.Fatal("source unexpectedly finished with its HTTP/2 flow-control window exhausted")
			default:
			}
			closed := make(chan struct{})
			if reason == "close" {
				go func() { fixture.service.Close(); close(closed) }()
			}
			select {
			case <-sourceContext.Done():
			case <-time.After(time.Second):
				test.Fatal("HTTP/2 source context was not canceled")
			}
			select {
			case <-fixture.source.finished:
			case <-time.After(time.Second):
				test.Fatalf("HTTP/2 flow-controlled write stayed blocked after %s: attempted=%d completed=%d", reason, fixture.source.attempted.Load(), fixture.source.completed.Load())
			}
			if reason == "close" {
				select {
				case <-closed:
				case <-time.After(time.Second):
					test.Fatal("Close stayed blocked after HTTP/2 source cancellation")
				}
			}
			test.Logf("HTTP/2 %s unblocked: attempted=%d completed=%d", reason, fixture.source.attempted.Load(), fixture.source.completed.Load())
		})
	}
}
