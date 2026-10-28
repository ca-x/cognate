package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestAutomaticRefreshFetchesAndRefusesRebasedCandidate(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, _ := repository.ParseReference("org/source", "")
	skills := []repository.ArchivedSkill{{Path: "skills/chosen", Name: "Chosen", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}
	base, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("a", 64), skills)
	if err != nil {
		test.Fatal(err)
	}
	cache, err := OpenArchiveCache(filepath.Join(directory, "cache"))
	if err != nil {
		test.Fatal(err)
	}
	data := fixtureArchive(test)
	digest := sha256.Sum256(data)
	called := 0
	rebase := false
	service, err := NewService(store, cache, fetchArchive(func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		called++
		if rebase {
			_, err := store.RefreshCollectedRepository(test.Context(), "alice", base.ID, state.RepositoryVersion{ArchiveHash: hex.EncodeToString(digest[:]), PublicationRevision: 2}, strings.Repeat("e", 64), skills)
			if err != nil {
				return nil, err
			}
		}
		return &repository.ArchiveSnapshot{Hash: hex.EncodeToString(digest[:]), Data: data, Skills: skills}, nil
	}))
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	ticket := func() state.SourceRefreshTicket {
		batch, err := store.ListSourceRefreshBatch(test.Context(), state.SourceRefreshCursor{}, 1)
		if err != nil {
			test.Fatal(err)
		}
		ticket, err := store.BeginSourceRefresh(test.Context(), batch.Items[0].Target)
		if err != nil {
			test.Fatal(err)
		}
		return ticket
	}
	if _, err := service.RefreshAutomatic(test.Context(), ticket()); err != nil || called != 1 {
		test.Fatalf("automatic fetch failed: %v", err)
	}
	rebase = true
	if _, err := service.RefreshAutomatic(test.Context(), ticket()); !errors.Is(err, state.ErrRepositoryRefreshConflict) || called != 2 {
		test.Fatalf("post-fetch old tuple was rebased: %v", err)
	}
}

func TestAutomaticRefreshPostFetchFencePrecedesCachePersistence(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	skills := []repository.ArchivedSkill{{Path: "skills/chosen", Name: "Chosen", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}
	base, err := store.CollectRepository(test.Context(), "alice", repository.Reference{NameWithOwner: "org/source"}, strings.Repeat("a", 64), skills)
	if err != nil {
		test.Fatal(err)
	}
	cache, err := OpenArchiveCache(filepath.Join(directory, "objects"))
	if err != nil {
		test.Fatal(err)
	}
	data := fixtureArchive(test)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	service, err := NewService(store, cache, fetchArchive(func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		_, err := store.RefreshCollectedRepository(test.Context(), "alice", base.ID, state.RepositoryVersion{ArchiveHash: base.ArchiveHash, PublicationRevision: base.PublicationRevision}, strings.Repeat("e", 64), skills)
		if err != nil {
			return nil, err
		}
		return &repository.ArchiveSnapshot{Hash: hash, Data: data, Skills: skills}, nil
	}))
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	batch, err := store.ListSourceRefreshBatch(test.Context(), state.SourceRefreshCursor{}, 1)
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := store.BeginSourceRefresh(test.Context(), batch.Items[0].Target)
	if err != nil {
		test.Fatal(err)
	}
	result, err := service.RefreshAutomatic(test.Context(), ticket)
	if !errors.Is(err, state.ErrRepositoryRefreshConflict) || !reflect.DeepEqual(result, state.RepositoryRefreshResult{}) {
		test.Fatal("old source candidate was not rejected")
	}
	if err := cache.Verify(test.Context(), hash); err == nil {
		test.Fatal("post-fetch authorization failure persisted candidate into cache")
	}
}

func TestAutomaticRefreshFailureAndPublicationPreserveOldValue(test *testing.T) {
	for _, scenario := range []string{"source-failure", "empty", "published"} {
		test.Run(scenario, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer store.Close()
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			skills := []repository.ArchivedSkill{{Path: "skills/chosen", Name: "Chosen", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}
			base, err := store.CollectRepository(test.Context(), "alice", repository.Reference{NameWithOwner: "org/source"}, strings.Repeat("a", 64), skills)
			if err != nil {
				test.Fatal(err)
			}
			if scenario == "published" {
				if _, err := store.SetPublication(test.Context(), "alice", base.ID, base.PublicationRevision, true, true); err != nil {
					test.Fatal(err)
				}
			}
			before, err := store.CollectedRepository(test.Context(), "alice", base.ID)
			if err != nil {
				test.Fatal(err)
			}
			cache, err := OpenArchiveCache(filepath.Join(directory, "objects"))
			if err != nil {
				test.Fatal(err)
			}
			data := fixtureArchive(test)
			digest := sha256.Sum256(data)
			hash := hex.EncodeToString(digest[:])
			service, err := NewService(store, cache, fetchArchive(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
				switch scenario {
				case "source-failure":
					return nil, repository.ErrArchiveUnavailable
				case "empty":
					return nil, nil
				default:
					return &repository.ArchiveSnapshot{Hash: hash, Data: data, Skills: skills}, nil
				}
			}))
			if err != nil {
				test.Fatal(err)
			}
			defer service.Close()
			batch, err := store.ListSourceRefreshBatch(test.Context(), state.SourceRefreshCursor{}, 1)
			if err != nil {
				test.Fatal(err)
			}
			ticket, err := store.BeginSourceRefresh(test.Context(), batch.Items[0].Target)
			if err != nil {
				test.Fatal(err)
			}
			result, err := service.RefreshAutomatic(test.Context(), ticket)
			if err == nil || !reflect.DeepEqual(result, state.RepositoryRefreshResult{}) {
				test.Fatal("failed source returned a successful receipt")
			}
			if scenario == "published" && !errors.Is(err, state.ErrRepositoryRefreshPublished) {
				test.Fatal("published change did not require explicit unpublish")
			}
			after, err := store.CollectedRepository(test.Context(), "alice", base.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				test.Fatal("failed source changed the old value")
			}
			if err := cache.Verify(test.Context(), hash); err == nil {
				test.Fatal("rejected candidate was cached")
			}
		})
	}
}
