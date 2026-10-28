package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"google.golang.org/protobuf/proto"
)

type catalogLifecycleFixture struct {
	service *publiccatalog.Service
	handler http.Handler
	skill   state.PublicSkill
	source  *representativeDeadlineArchiveSource
}

func newCatalogLifecycleFixture(test *testing.T) catalogLifecycleFixture {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, _ := repository.ParseReference("org/lifecycle", "")
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Lifecycle", Description: "Bounded transport", Path: "skills/example"}, ContentHash: strings.Repeat("a", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.SetPublication(test.Context(), "alice", collected.ID, 1, true, true); err != nil {
		test.Fatal(err)
	}
	source := &representativeDeadlineArchiveSource{started: make(chan context.Context, 1), finished: make(chan struct{})}
	service, err := publiccatalog.NewService(store, source)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	selected, err := service.GetSkill(test.Context(), collected.Skills[0].ID)
	if err != nil {
		test.Fatal(err)
	}
	return catalogLifecycleFixture{service: service, handler: NewWithOptions(authentication, Options{Store: store, PublicCatalog: service}), skill: selected, source: source}
}

func TestCatalogCloseUnblocksPartialRequestBody(test *testing.T) {
	fixture := newCatalogLifecycleFixture(test)
	ready := make(chan struct{}, 2)
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(finished)
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
	if _, err := fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/connect+proto\r\nConnect-Protocol-Version: 1\r\nConnect-Timeout-Ms: 4000\r\nContent-Length: 100\r\n\r\n", catalogv1connect.PublicCatalogServiceDownloadSkillProcedure, server.Listener.Addr()); err != nil {
		test.Fatal(err)
	}
	for range 2 {
		select {
		case <-ready:
		case <-time.After(time.Second):
			test.Fatal("request did not bind middleware and RPC transport deadlines")
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- fixture.service.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(time.Second):
		test.Fatal("Close did not interrupt incomplete body read")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		test.Fatal("handler still blocked after Close")
	}
	select {
	case <-fixture.source.started:
		test.Fatal("incomplete body reached archive source")
	default:
	}
}

func TestCatalogProtocolCancelPreservesKeepAlive(test *testing.T) {
	for _, mode := range []string{"no-rpc-timeout", "future-rpc-timeout"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCatalogLifecycleFixture(test)
			_, protocol := catalogv1connect.NewPublicCatalogServiceHandler(&publicCatalogHandler{service: fixture.service}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(1<<20), connect.WithInterceptors(distributionDeadlines{}))
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
			payload, err := proto.Marshal(&catalogv1.GetSkillRequest{SkillId: fixture.skill.ID})
			if err != nil {
				test.Fatal(err)
			}
			reader := bufio.NewReader(connection)
			for attempt := range 2 {
				request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+catalogv1connect.PublicCatalogServiceGetSkillProcedure, bytes.NewReader(payload))
				if err != nil {
					test.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/proto")
				request.Header.Set("Connect-Protocol-Version", "1")
				if mode == "future-rpc-timeout" {
					request.Header.Set("Connect-Timeout-Ms", "1000")
				}
				if err := request.Write(connection); err != nil {
					test.Fatalf("keep-alive write %d: %v", attempt, err)
				}
				response, err := http.ReadResponse(reader, request)
				if err != nil {
					test.Fatalf("keep-alive read %d: %v", attempt, err)
				}
				data, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil || response.StatusCode != http.StatusOK || response.Close {
					test.Fatalf("normal request lost keep-alive: status=%d close=%t %v", response.StatusCode, response.Close, readErr)
				}
				var detail catalogv1.GetSkillResponse
				if err := proto.Unmarshal(data, &detail); err != nil || detail.Skill.GetId() != fixture.skill.ID {
					test.Fatalf("wrong public detail: %v", err)
				}
			}
		})
	}
}

func TestCatalogBlockedSocketDeadlineCloseAndDisconnect(test *testing.T) {
	for _, reason := range []string{"deadline", "close", "disconnect"} {
		test.Run(reason, func(test *testing.T) {
			fixture := newCatalogLifecycleFixture(test)
			finished := make(chan struct{})
			writeDeadlines := make(chan time.Time, 4)
			socketWriteReturned := make(chan error, 1)
			releaseSocketErrors := make(chan struct{})
			releaseErrors := sync.OnceFunc(func() { close(releaseSocketErrors) })
			if reason == "deadline" {
				fixture.source.writeReturned = make(chan error, 1)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				defer close(finished)
				if reason == "deadline" {
					writer = representativeDeadlineWatchWriter{ResponseWriter: writer, deadlines: writeDeadlines}
				}
				fixture.handler.ServeHTTP(writer, request)
			}))
			server.Config.ConnState = func(connection net.Conn, phase http.ConnState) {
				if reason == "deadline" {
					connection = connection.(*representativeDeadlineConnection).Conn
				}
				if socket, ok := connection.(*net.TCPConn); ok && phase == http.StateNew {
					_ = socket.SetWriteBuffer(4 << 10)
				}
			}
			if reason == "deadline" {
				server.Listener = representativeDeadlineListener{Listener: server.Listener, writeReturned: socketWriteReturned, releaseErrors: releaseSocketErrors}
			}
			server.Start()
			test.Cleanup(server.Close)
			test.Cleanup(releaseErrors)
			address, err := net.ResolveTCPAddr("tcp4", server.Listener.Addr().String())
			if err != nil {
				test.Fatal(err)
			}
			connection, err := net.DialTCP("tcp4", nil, address)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = connection.Close() })
			if err := errors.Join(connection.SetReadBuffer(1024), connection.SetDeadline(time.Now().Add(5*time.Second))); err != nil {
				test.Fatal(err)
			}
			payload, err := proto.Marshal(&catalogv1.DownloadSkillRequest{SkillId: fixture.skill.ID, ContentHash: fixture.skill.ContentHash, PublicationRevision: fixture.skill.PublicationRevision})
			if err != nil {
				test.Fatal(err)
			}
			envelope := make([]byte, 5+len(payload))
			binary.BigEndian.PutUint32(envelope[1:5], uint32(len(payload)))
			copy(envelope[5:], payload)
			request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+catalogv1connect.PublicCatalogServiceDownloadSkillProcedure, bytes.NewReader(envelope))
			if err != nil {
				test.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/connect+proto")
			request.Header.Set("Connect-Protocol-Version", "1")
			if reason == "deadline" {
				request.Header.Set("Connect-Timeout-Ms", "200")
			}
			if err := request.Write(connection); err != nil {
				test.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), request)
			if err != nil || response.StatusCode != http.StatusOK {
				test.Fatalf("stream response headers: %v", err)
			}
			var sourceContext context.Context
			select {
			case sourceContext = <-fixture.source.started:
			case <-time.After(time.Second):
				test.Fatal("request did not reach source")
			}
			if reason == "deadline" {
				stopRelease := context.AfterFunc(sourceContext, releaseErrors)
				test.Cleanup(func() { stopRelease() })
				if deadline, ok := sourceContext.Deadline(); !ok || time.Until(deadline) > 200*time.Millisecond {
					test.Fatal("source context did not inherit the 200ms Connect deadline")
				}
				writeUnblocked := time.After(time.Second)
				select {
				case err := <-socketWriteReturned:
					if socketError, ok := errors.AsType[net.Error](err); !ok || !socketError.Timeout() {
						test.Fatalf("socket write did not return a timeout: %v", err)
					}
				case <-writeUnblocked:
					test.Fatalf("Connect deadline did not unblock socket write: attempted=%d completed=%d", fixture.source.attempted.Load(), fixture.source.completed.Load())
				}
				select {
				case err := <-fixture.source.writeReturned:
					if err == nil {
						test.Fatal("archive write returned without an error")
					}
				case <-writeUnblocked:
					test.Fatal("socket write returned but archive write remained blocked")
				}
				var connectWriteDeadline time.Time
				for range 2 {
					select {
					case connectWriteDeadline = <-writeDeadlines:
					default:
						test.Fatal("transport did not bind both lifetime and Connect write deadlines")
					}
				}
				if sourceDeadline, _ := sourceContext.Deadline(); !sourceDeadline.Equal(connectWriteDeadline) {
					test.Fatalf("source deadline %v differs from transport Connect deadline %v", sourceDeadline, connectWriteDeadline)
				}
			}
			closed := make(chan error, 1)
			if reason == "close" {
				go func() { closed <- fixture.service.Close() }()
			} else if reason == "disconnect" {
				_ = connection.Close()
			}
			select {
			case <-sourceContext.Done():
			case <-time.After(2 * time.Second):
				test.Fatal("source context was not canceled")
			}
			if reason == "deadline" && sourceContext.Err() != context.DeadlineExceeded {
				test.Fatalf("short Connect deadline not inherited: %v", sourceContext.Err())
			}
			select {
			case <-fixture.source.finished:
			case <-time.After(time.Second):
				test.Fatalf("socket write remained blocked: attempted=%d completed=%d", fixture.source.attempted.Load(), fixture.source.completed.Load())
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				test.Fatal("handler did not drain")
			}
			if reason == "close" {
				select {
				case err := <-closed:
					if err != nil {
						test.Fatal(err)
					}
				case <-time.After(time.Second):
					test.Fatal("Close did not wait and finish")
				}
			}
			_ = response.Body.Close()
		})
	}
}
