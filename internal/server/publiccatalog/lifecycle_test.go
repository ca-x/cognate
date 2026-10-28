package publiccatalog

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
)

func TestPublicDownloadCapacityCloseAndCancellation(test *testing.T) {
	started := make(chan context.Context, 2)
	var finished atomic.Int64
	_, service, collected := publicFixture(test, archiveSource(func(ctx context.Context, output io.Writer) error {
		started <- ctx
		defer finished.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}))
	selected := collected.Skills[0]
	errorsSeen := make(chan error, 2)
	var downloads sync.WaitGroup
	for range 2 {
		downloads.Go(func() {
			errorsSeen <- service.Download(test.Context(), selected.ID, selected.ContentHash, 2, func(packs.Chunk) error { return nil })
		})
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			test.Fatal("download did not acquire a slot")
		}
	}
	if err := service.Download(test.Context(), selected.ID, selected.ContentHash, 2, func(packs.Chunk) error { return nil }); !errors.Is(err, ErrBusy) {
		test.Fatalf("global download capacity: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- service.Close() }()
	select {
	case err := <-closed:
		if err != nil || finished.Load() != 2 {
			test.Fatalf("Close did not wait for sources: %v finished=%d", err, finished.Load())
		}
	case <-time.After(time.Second):
		test.Fatal("Close did not cancel and drain active downloads")
	}
	downloads.Wait()
	for range 2 {
		if err := <-errorsSeen; !errors.Is(err, context.Canceled) {
			test.Fatalf("in-flight download not canceled: %v", err)
		}
	}
	if _, err := service.ListSkills(test.Context(), state.PublicSkillQuery{}); !errors.Is(err, ErrClosed) {
		test.Fatalf("closed list: %v", err)
	}
	if err := service.Close(); err != nil {
		test.Fatal(err)
	}
}

func TestPublicRequestDeadlineAndIdempotentDone(test *testing.T) {
	store, _, _ := publicFixture(test, archiveSource(func(context.Context, io.Writer) error { return nil }))
	synctest.Test(test, func(test *testing.T) {
		service, err := NewService(store, archiveSource(func(context.Context, io.Writer) error { return nil }))
		if err != nil {
			test.Fatal(err)
		}
		defer service.Close()
		ctx, done, err := service.BeginRequest(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) != 20*time.Second {
			test.Fatal("public request does not have its 20 second bound")
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			test.Fatalf("deadline did not expire: %v", ctx.Err())
		}
		done()
		done()
		parent, cancel := context.WithTimeout(test.Context(), time.Second)
		defer cancel()
		ctx, done, err = service.BeginRequest(parent)
		if err != nil {
			test.Fatal(err)
		}
		defer done()
		time.Sleep(time.Second)
		synctest.Wait()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			test.Fatalf("caller deadline extended: %v", ctx.Err())
		}
	})
}

func TestPublicDownloadFinalReauthorizationAndRepublish(test *testing.T) {
	for _, mode := range []string{"before-final", "republish", "caller-cancel", "send-error"} {
		test.Run(mode, func(test *testing.T) {
			store, service, collected := publicFixture(test, archiveSource(func(ctx context.Context, output io.Writer) error {
				_, err := io.WriteString(output, strings.Repeat("a", 100))
				return err
			}))
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			selected := collected.Skills[0]
			sendFailure := errors.New("receiver stopped")
			sends := 0
			err := service.Download(ctx, selected.ID, selected.ContentHash, 2, func(chunk packs.Chunk) error {
				sends++
				if chunk.Final {
					test.Fatal("unauthorized/canceled stream sent final")
				}
				switch mode {
				case "caller-cancel":
					cancel()
					return nil
				case "send-error":
					return sendFailure
				default:
					if _, err := store.SetPublication(test.Context(), "alice", collected.ID, 2, false, false); err != nil {
						return err
					}
					if mode == "republish" {
						_, err := store.SetPublication(test.Context(), "alice", collected.ID, 3, true, true)
						return err
					}
					return nil
				}
			})
			expected := map[string]error{"before-final": state.ErrPublicSkillNotFound, "republish": state.ErrPublicationConflict, "caller-cancel": context.Canceled, "send-error": sendFailure}[mode]
			if !errors.Is(err, expected) || sends != 1 {
				test.Fatalf("termination: %v sends=%d", err, sends)
			}
		})
	}
}

func TestPublicDownloadRejectsInvalidIdentityBeforeSource(test *testing.T) {
	var calls atomic.Int64
	_, service, collected := publicFixture(test, archiveSource(func(context.Context, io.Writer) error {
		calls.Add(1)
		return nil
	}))
	selected := collected.Skills[0]
	for _, request := range []struct {
		id       string
		hash     string
		revision int64
	}{
		{"missing", selected.ContentHash, 2},
		{selected.ID, strings.Repeat("c", 64), 2},
		{selected.ID, selected.ContentHash, 1},
		{selected.ID, selected.ContentHash, 0},
	} {
		if err := service.Download(test.Context(), request.id, request.hash, request.revision, func(packs.Chunk) error { return nil }); err == nil {
			test.Fatal("invalid download accepted")
		}
	}
	if err := service.Download(test.Context(), selected.ID, selected.ContentHash, 2, nil); !errors.Is(err, state.ErrPublicCatalogInvalid) {
		test.Fatalf("nil sender: %v", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if err := service.Download(ctx, selected.ID, selected.ContentHash, 2, func(packs.Chunk) error { return nil }); !errors.Is(err, context.Canceled) {
		test.Fatalf("pre-canceled request: %v", err)
	}
	if calls.Load() != 0 {
		test.Fatalf("invalid request reached source: %d", calls.Load())
	}
}

func TestPublicDownloadTotalByteBound(test *testing.T) {
	_, service, collected := publicFixture(test, archiveSource(func(ctx context.Context, output io.Writer) error {
		block := make([]byte, packs.ChunkBytes)
		for remaining := repository.MaxSkillArchiveBytes + 1; remaining > 0; {
			count, err := output.Write(block[:min(len(block), remaining)])
			remaining -= count
			if err != nil {
				return err
			}
		}
		return nil
	}))
	selected := collected.Skills[0]
	var transferred int
	err := service.Download(test.Context(), selected.ID, selected.ContentHash, 2, func(chunk packs.Chunk) error {
		if chunk.Final {
			test.Fatal("oversized archive sent a final")
		}
		transferred += len(chunk.Data)
		return nil
	})
	if !errors.Is(err, repository.ErrArchiveTooLarge) || transferred > repository.MaxSkillArchiveBytes {
		test.Fatalf("total limit: %v transferred=%d", err, transferred)
	}
}
