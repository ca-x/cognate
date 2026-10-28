package publiccatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

type archiveSource func(context.Context, io.Writer) error

func (source archiveSource) WriteSkillArchive(ctx context.Context, _, _, _ string, output io.Writer) error {
	return source(ctx, output)
}

func publicFixture(test *testing.T, source packs.ArchiveSource) (*state.Store, *Service, state.CollectedRepository) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "state.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, _ := repository.ParseReference("org/public", "")
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "Description", Path: "skills/example"}, ContentHash: strings.Repeat("a", 64)}})
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
	service, err := NewService(store, source)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	return store, service, collected
}

func TestPublicDownloadChunksIntegrityAndRevocation(test *testing.T) {
	data := bytes.Repeat([]byte("source"), 40000)
	store, service, collected := publicFixture(test, archiveSource(func(ctx context.Context, output io.Writer) error {
		_, err := output.Write(data)
		return err
	}))
	selected := collected.Skills[0]
	var received bytes.Buffer
	finals := 0
	err := service.Download(test.Context(), selected.ID, selected.ContentHash, 2, func(chunk packs.Chunk) error {
		if chunk.Offset != int64(received.Len()) || len(chunk.Data) > packs.ChunkBytes {
			test.Fatalf("invalid chunk: %+v", chunk)
		}
		if chunk.Final {
			finals++
			digest := sha256.Sum256(received.Bytes())
			if chunk.ContentHash != selected.ContentHash || chunk.SHA256 != hex.EncodeToString(digest[:]) || len(chunk.Data) != 0 {
				test.Fatalf("invalid final: %+v", chunk)
			}
		}
		_, err := received.Write(chunk.Data)
		return err
	})
	if err != nil || finals != 1 || !bytes.Equal(received.Bytes(), data) {
		test.Fatalf("download: %v, finals=%d bytes=%d", err, finals, received.Len())
	}
	chunks := 0
	err = service.Download(test.Context(), selected.ID, selected.ContentHash, 2, func(chunk packs.Chunk) error {
		chunks++
		if chunk.Final {
			test.Fatal("revoked stream sent final")
		}
		_, err := store.SetPublication(test.Context(), "alice", collected.ID, 2, false, false)
		return err
	})
	if !errors.Is(err, state.ErrPublicSkillNotFound) || chunks != 1 {
		test.Fatalf("revocation: %v chunks=%d", err, chunks)
	}
	if _, err := service.GetSkill(test.Context(), selected.ID); !errors.Is(err, state.ErrPublicSkillNotFound) {
		test.Fatalf("revoked detail: %v", err)
	}
}
