package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	packv1 "github.com/ca-x/skillhub/gen/skillhub/pack/v1"
	"github.com/ca-x/skillhub/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/ca-x/skillhub/internal/skill"
	"google.golang.org/protobuf/proto"
)

type representativeDeadlineArchiveSource struct {
	started   chan context.Context
	finished  chan struct{}
	attempted atomic.Int64
	completed atomic.Int64
}

func (source *representativeDeadlineArchiveSource) WriteSkillArchive(ctx context.Context, _, _, _ string, output io.Writer) error {
	source.started <- ctx
	defer close(source.finished)
	data := make([]byte, packs.ChunkBytes)
	for range 768 {
		if err := ctx.Err(); err != nil {
			return err
		}
		source.attempted.Add(1)
		if _, err := output.Write(data); err != nil {
			return err
		}
		source.completed.Add(1)
	}
	return nil
}

func TestRepresentativeDistributionConnectDeadlineUnblocksSocketWrite(test *testing.T) {
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
	reference, err := repository.ParseReference("org/representative-deadline", "")
	if err != nil {
		test.Fatal(err)
	}
	contentHash := strings.Repeat("a", 64)
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "Representative dummy skill", Path: "skills/example"}, ContentHash: contentHash}})
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
	distribution, err := packs.NewService(store, source, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(distribution.Close)
	issued, err := distribution.CreateShare(test.Context(), "alice", created.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	handler := NewWithOptions(authentication, Options{Store: store, Packs: distribution})
	handlerFinished := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(handlerFinished)
		handler.ServeHTTP(writer, request)
	}))
	server.Config.ConnState = func(connection net.Conn, phase http.ConnState) {
		if phase == http.StateNew {
			if socket, ok := connection.(*net.TCPConn); !ok {
				test.Error("fixture did not accept a TCP connection")
			} else if err := socket.SetWriteBuffer(4 << 10); err != nil {
				test.Errorf("limit server socket buffer: %v", err)
			}
		}
	}
	server.Start()
	test.Cleanup(server.Close)
	address, err := net.ResolveTCPAddr("tcp4", server.Listener.Addr().String())
	if err != nil {
		test.Fatal(err)
	}
	connection, err := net.DialTCP("tcp4", nil, address)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetReadBuffer(1024); err != nil {
		test.Fatal(err)
	}
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		test.Fatal(err)
	}
	payload, err := proto.Marshal(&packv1.DownloadSkillRequest{SkillId: detail.Skills[0].ID, ContentHash: contentHash, PackRevision: created.Revision})
	if err != nil {
		test.Fatal(err)
	}
	envelope := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(envelope[1:5], uint32(len(payload)))
	copy(envelope[5:], payload)
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+packv1connect.DistributionServiceDownloadSkillProcedure, bytes.NewReader(envelope))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/connect+proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("Connect-Timeout-Ms", "200")
	request.Header.Set("Authorization", "Bearer "+issued.Token.Reveal())
	if err := request.Write(connection); err != nil {
		test.Fatalf("write Connect request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		test.Fatalf("read streaming response headers: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		test.Fatalf("unexpected HTTP status: %d", response.StatusCode)
	}
	var sourceContext context.Context
	select {
	case sourceContext = <-source.started:
	case <-time.After(2 * time.Second):
		test.Fatal("request did not reach the archive source")
	}
	if deadline, ok := sourceContext.Deadline(); !ok || time.Until(deadline) > time.Second {
		test.Fatal("source context did not inherit the short Connect deadline")
	}
	select {
	case <-sourceContext.Done():
	case <-time.After(2 * time.Second):
		test.Fatal("short Connect deadline did not cancel the source context")
	}
	if sourceContext.Err() != context.DeadlineExceeded {
		test.Fatalf("unexpected source cancellation: %v", sourceContext.Err())
	}
	select {
	case <-source.finished:
	case <-time.After(time.Second):
		test.Fatalf("Connect deadline canceled context but socket write remained blocked: attempted=%d completed=%d", source.attempted.Load(), source.completed.Load())
	}
	select {
	case <-handlerFinished:
	case <-time.After(time.Second):
		test.Fatal("Connect handler did not return after its deadline")
	}
}
