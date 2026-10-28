package packs

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/ca-x/skillhub/internal/skill"
)

type testSource struct{ data []byte }

func (source testSource) WriteSkillArchive(ctx context.Context, _, _, _ string, output io.Writer) error {
	_, err := output.Write(source.data)
	return err
}

func fixture(test *testing.T) (*state.Store, *Service, state.Pack, []byte) {
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
	draft := state.PackDraft{Name: "Example"}
	for _, name := range []string{"org/first", "org/second"} {
		reference, _ := repository.ParseReference(name, "")
		collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Shared", Description: "Shared skill", Path: "skills/shared"}, ContentHash: strings.Repeat("a", 64)}})
		if err != nil {
			test.Fatal(err)
		}
		detail, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
		if err != nil {
			test.Fatal(err)
		}
		draft.Items = append(draft.Items, state.PackSelection{SkillID: detail.Skills[0].ID})
	}
	pack, err := store.CreatePack(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	data := make([]byte, 150<<10)
	_, _ = rand.Read(data)
	service, err := NewService(store, testSource{data}, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(service.Close)
	return store, service, pack, data
}

func TestSignedShareManifestAndStreaming(test *testing.T) {
	store, service, pack, data := fixture(test)
	issued, err := service.CreateShare(test.Context(), "alice", pack.ID, new(time.Now().Add(time.Hour)))
	if err != nil {
		test.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if strings.Contains(fmt.Sprintf(format, issued.Token), issued.Token.Reveal()) || strings.Contains(fmt.Sprintf(format, service), issued.Token.Reveal()) {
			test.Fatal("credential leaked through formatting")
		}
	}
	manifest, err := service.Manifest(test.Context(), issued.Token.Reveal())
	if err != nil || len(manifest.Skills) != 1 || manifest.Revision != 1 || len(manifest.Hash) != 64 {
		test.Fatalf("manifest: %+v, %v", manifest, err)
	}
	again, err := service.Manifest(test.Context(), issued.Token.Reveal())
	if err != nil || manifest.Hash != again.Hash {
		test.Fatalf("unstable manifest: %v", err)
	}
	var output bytes.Buffer
	var last Chunk
	err = service.Download(test.Context(), issued.Token.Reveal(), manifest.Skills[0].SkillID, manifest.Skills[0].ContentHash, 1, func(chunk Chunk) error {
		if chunk.Offset != int64(output.Len()) || len(chunk.Data) > ChunkBytes {
			test.Fatal("invalid chunk offset or size")
		}
		_, _ = output.Write(chunk.Data)
		last = chunk
		return nil
	})
	digest := sha256.Sum256(data)
	if err != nil || !bytes.Equal(output.Bytes(), data) || !last.Final || last.SHA256 != hex.EncodeToString(digest[:]) || last.Offset != int64(len(data)) {
		test.Fatalf("stream integrity: %v", err)
	}
	if err := store.RevokePackShare(test.Context(), "alice", pack.ID, issued.Info.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Manifest(test.Context(), issued.Token.Reveal()); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("revoked token: %v", err)
	}
}

func TestShareForgeryExpiryAndStreamRevocation(test *testing.T) {
	store, service, pack, _ := fixture(test)
	issued, err := service.CreateShare(test.Context(), "alice", pack.ID, new(time.Now().Add(time.Hour)))
	if err != nil {
		test.Fatal(err)
	}
	raw := issued.Token.Reveal()
	for _, invalid := range []string{"", raw + "=", "shp1." + strings.Repeat("a", 143), raw[:20] + "!" + raw[21:]} {
		if _, err := service.Manifest(test.Context(), invalid); !errors.Is(err, state.ErrPackShareUnavailable) {
			test.Fatalf("invalid token: %v", err)
		}
	}
	chunks := 0
	err = service.Download(test.Context(), raw, pack.Items[0].SkillID, pack.Items[0].ContentHash, 1, func(chunk Chunk) error {
		chunks++
		return store.RevokePackShare(test.Context(), "alice", pack.ID, issued.Info.ID)
	})
	if !errors.Is(err, state.ErrPackShareUnavailable) || chunks != 1 {
		test.Fatalf("revocation did not stop next chunk: chunks=%d, err=%v", chunks, err)
	}
	unrevoked, err := service.CreateShare(test.Context(), "alice", pack.ID, new(time.Now().Add(time.Hour)))
	if err != nil {
		test.Fatal(err)
	}
	service.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := service.Manifest(test.Context(), unrevoked.Token.Reveal()); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("expiry: %v", err)
	}
}

func TestPackServiceCloseCancelsAndWaitsForRequests(test *testing.T) {
	_, service, _, _ := fixture(test)
	ctx, done, err := service.BeginRequest(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { service.Close(); close(finished) }()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		test.Fatal("close did not cancel admitted request")
	}
	select {
	case <-finished:
		test.Fatal("close returned before request cleanup")
	default:
	}
	done()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		test.Fatal("close did not finish after request cleanup")
	}
	if _, _, err := service.BeginRequest(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatalf("request admitted after close: %v", err)
	}
}

func TestShareReconstructionAndKeyIsolation(test *testing.T) {
	store, service, pack, data := fixture(test)
	if _, err := NewService(store, testSource{data}, bytes.Repeat([]byte{1}, 32)); err == nil {
		test.Fatal("mismatched database key accepted")
	}
	issued, err := service.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	before, err := service.Manifest(test.Context(), issued.Token.Reveal())
	if err != nil {
		test.Fatal(err)
	}
	service.Close()
	restarted, err := NewService(store, testSource{data}, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer restarted.Close()
	after, err := restarted.Manifest(test.Context(), issued.Token.Reveal())
	if err != nil || after.Hash != before.Hash {
		test.Fatalf("share changed across service reconstruction: %v", err)
	}
	if err := store.RevokePackShare(test.Context(), "alice", pack.ID, issued.Info.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := restarted.Manifest(test.Context(), issued.Token.Reveal()); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("persistent revocation not enforced: %v", err)
	}
}
