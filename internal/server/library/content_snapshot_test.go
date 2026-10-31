package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
)

func snapshotContentLimits() ContentSnapshotLimits {
	return ContentSnapshotLimits{Database: contentSnapshotLimits(), MaxArchives: 1000, MaxInventoryBytes: 8 << 20, MaxContentBytes: 128 << 20}
}

func TestContentSnapshotAllOwnersOfflineCopy(test *testing.T) {
	service, store, cache, _ := contentService(test)
	first, err := service.Collect(test.Context(), "owner", "org/first", "")
	if err != nil {
		test.Fatal(err)
	}
	if err := store.CreateUser(test.Context(), "second-owner"); err != nil {
		test.Fatal(err)
	}
	second, err := service.Collect(test.Context(), "second-owner", "org/second", "")
	if err != nil {
		test.Fatal(err)
	}
	first, err = service.Get(test.Context(), "owner", first.ID)
	if err != nil {
		test.Fatal(err)
	}
	second, err = service.Get(test.Context(), "second-owner", second.ID)
	if err != nil {
		test.Fatal(err)
	}
	for _, selection := range []struct{ owner, name, skill string }{{"owner", "first pack", first.Skills[0].ID}, {"owner", "overlap", first.Skills[0].ID}, {"second-owner", "second pack", second.Skills[0].ID}} {
		if _, err := store.CreatePack(test.Context(), selection.owner, state.PackDraft{Name: selection.name, Items: []state.PackSelection{{SkillID: selection.skill, Slug: "same"}}}); err != nil {
			test.Fatal(err)
		}
	}
	category, err := store.CreateCategory(test.Context(), "owner", "Private category")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.SetSkillCategories(test.Context(), "owner", first.Skills[0].ID, []string{category.ID}); err != nil {
		test.Fatal(err)
	}
	if err := cache.Put(test.Context(), archiveHash([]byte("unreferenced-invalid-archive")), []byte("unreferenced-invalid-archive")); err != nil {
		test.Fatal(err)
	}
	var database, inventory, archive bytes.Buffer
	var escaped ContentSnapshotView
	summary, err := service.WithContentSnapshot(test.Context(), snapshotContentLimits(), func(ctx context.Context, view ContentSnapshotView) error {
		escaped = view
		metadata, err := view.Metadata(ctx)
		if err != nil {
			return err
		}
		if metadata.Archives() != 1 {
			test.Fatal("shared content was duplicated or omitted")
		}
		if _, err = view.CopyDatabase(ctx, &database); err != nil {
			return err
		}
		if _, err = view.CopyInventory(ctx, &inventory); err != nil {
			return err
		}
		files, err := view.ArchiveFiles(ctx)
		if err != nil || len(files) != 1 || files[0].Hash() != first.ArchiveHash {
			test.Fatal("all-owner closure missing archive", err)
		}
		_, err = view.CopyArchive(ctx, files[0].Hash(), &archive)
		return err
	})
	if err != nil {
		test.Fatal(err)
	}
	length, digest, records, owners := summary.Inventory()
	if length != int64(inventory.Len()) || digest != sha256.Sum256(inventory.Bytes()) || records != 11 || owners != 2 {
		test.Fatal("complete inventory receipt mismatch")
	}
	if summary.ContentBytes() != int64(database.Len()+inventory.Len()+archive.Len()) || !bytes.Equal(archive.Bytes(), contentSnapshot(test, "initial").Data) {
		test.Fatal("content length or archive changed")
	}
	for _, identity := range []string{first.ID, second.ID} {
		if !strings.Contains(inventory.String(), identity) {
			test.Fatal("inventory omitted an owner repository")
		}
	}
	if _, err := escaped.CopyDatabase(test.Context(), io.Discard); err == nil {
		test.Fatal("expired view accepted")
	}
	if err := service.Close(); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(test.TempDir(), "offline.db")
	if err := os.WriteFile(path, database.Bytes(), 0600); err != nil {
		test.Fatal(err)
	}
	pool, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	for table, expected := range map[string]int{"collected_repositories": 2, "collected_skills": 2, "packs": 3, "pack_items": 3, "category_skills": 1} {
		var count int
		if err := pool.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != expected {
			test.Fatal("offline database differs from full source", table, count, err)
		}
	}
}

func TestContentSnapshotRejectsArchiveMismatch(test *testing.T) {
	for _, fault := range []string{"missing", "corrupt", "skill hash", "skill path", "skill count"} {
		test.Run(fault, func(test *testing.T) {
			service, _, cache, _ := contentService(test)
			entry, err := service.Collect(test.Context(), "owner", "org/first", "")
			if err != nil {
				test.Fatal(err)
			}
			if fault == "missing" {
				err = os.Remove(filepath.Join(cache.directory, entry.ArchiveHash+".tar.gz"))
			}
			if fault == "corrupt" {
				err = os.WriteFile(filepath.Join(cache.directory, entry.ArchiveHash+".tar.gz"), []byte("corrupt"), 0600)
			}
			if strings.HasPrefix(fault, "skill ") {
				pool, openErr := sql.Open("sqlite", "file:"+filepath.Join(filepath.Dir(cache.directory), "server.db"))
				if openErr != nil {
					test.Fatal(openErr)
				}
				switch fault {
				case "skill hash":
					_, err = pool.Exec("UPDATE collected_skills SET content_hash=?", strings.Repeat("f", 64))
				case "skill path":
					_, err = pool.Exec("UPDATE collected_skills SET path='missing'")
				case "skill count":
					_, err = pool.Exec("UPDATE collected_repositories SET skill_count=2")
				}
				if closeErr := pool.Close(); closeErr != nil {
					test.Fatal(closeErr)
				}
			}
			if err != nil {
				test.Fatal(err)
			}
			called := false
			summary, err := service.WithContentSnapshot(test.Context(), snapshotContentLimits(), func(context.Context, ContentSnapshotView) error { called = true; return nil })
			if err == nil || called || summary.ContentBytes() != 0 {
				test.Fatal("invalid content closure exposed candidate")
			}
		})
	}
}

func TestContentSnapshotBudgetAndCallbackFailure(test *testing.T) {
	service, _, _, _ := contentService(test)
	if _, err := service.Collect(test.Context(), "owner", "org/first", ""); err != nil {
		test.Fatal(err)
	}
	limits := snapshotContentLimits()
	summary, err := service.WithContentSnapshot(test.Context(), limits, func(context.Context, ContentSnapshotView) error { return nil })
	if err != nil {
		test.Fatal(err)
	}
	limits.MaxContentBytes = summary.ContentBytes()
	if _, err := service.WithContentSnapshot(test.Context(), limits, func(context.Context, ContentSnapshotView) error { return nil }); err != nil {
		test.Fatal("exact content budget rejected", err)
	}
	limits.MaxContentBytes--
	if summary, err := service.WithContentSnapshot(test.Context(), limits, func(context.Context, ContentSnapshotView) error {
		test.Fatal("overbudget candidate escaped")
		return nil
	}); err == nil || summary.ContentBytes() != 0 {
		test.Fatal("content budget ignored")
	}
	if summary, err := service.WithContentSnapshot(test.Context(), snapshotContentLimits(), func(context.Context, ContentSnapshotView) error { return errors.New("synthetic private error") }); err == nil || summary.ContentBytes() != 0 || strings.Contains(err.Error(), "synthetic private") {
		test.Fatal("callback failure escaped sanitization or returned success")
	}
}

func TestContentSnapshotInventoryDrainAndPagination(test *testing.T) {
	service, store, _, _ := contentService(test)
	for index := range 130 {
		if _, err := service.Collect(test.Context(), "owner", fmt.Sprintf("org/source-%d", index), ""); err != nil {
			test.Fatal(err)
		}
	}
	waiting := &contentInventoryBarrierWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	resume := func() { release.Do(func() { close(waiting.release) }) }
	defer resume()
	copyDone := make(chan error, 1)
	finished := make(chan error, 1)
	callbackReturned := make(chan struct{})
	go func() {
		_, err := service.WithContentSnapshot(test.Context(), snapshotContentLimits(), func(ctx context.Context, view ContentSnapshotView) error {
			metadata, err := view.Metadata(ctx)
			if err != nil {
				return err
			}
			_, _, records, _ := metadata.Inventory()
			if records != 261 {
				return errors.New("reference pages omitted")
			}
			go func() { _, err := view.CopyInventory(ctx, waiting); copyDone <- err }()
			<-waiting.entered
			close(callbackReturned)
			return nil
		})
		finished <- err
	}()
	select {
	case <-callbackReturned:
	case <-time.After(10 * time.Second):
		test.Fatal("inventory copy not entered")
	}
	select {
	case err := <-finished:
		test.Fatal("inventory source invalidated while reader active", err)
	default:
	}
	if err := store.CreateUser(test.Context(), "cut-must-remain-held"); !errors.Is(err, maintenance.ErrQuiescing) {
		test.Fatal("writer crossed inventory drain", err)
	}
	resume()
	if err := <-copyDone; err != nil {
		test.Fatal(err)
	}
	if err := <-finished; err != nil {
		test.Fatal(err)
	}
	if bytes.Count(waiting.output.Bytes(), []byte("\n")) != 261 || !bytes.Contains(waiting.output.Bytes(), []byte("org/source-129")) {
		test.Fatal("inventory mutated or truncated during cleanup")
	}
}

type contentInventoryBarrierWriter struct {
	entered, release chan struct{}
	once             sync.Once
	output           bytes.Buffer
}

func (writer *contentInventoryBarrierWriter) Write(data []byte) (int, error) {
	writer.once.Do(func() { close(writer.entered); <-writer.release })
	return writer.output.Write(data)
}

func TestContentSnapshotIgnoredInventoryFailureRefused(test *testing.T) {
	service, _, _, _ := contentService(test)
	summary, err := service.WithContentSnapshot(test.Context(), snapshotContentLimits(), func(ctx context.Context, view ContentSnapshotView) error {
		if _, err := view.CopyInventory(ctx, snapshotShortWriter{}); err == nil {
			test.Fatal("short inventory write accepted")
		}
		return nil
	})
	if err == nil || summary.ContentBytes() != 0 {
		test.Fatal("ignored inventory failure became successful candidate")
	}
}

func TestContentSnapshotInventoryOriginalLife(test *testing.T) {
	for _, withoutCancel := range []bool{false, true} {
		for _, cancelDuringWrite := range []bool{false, true} {
			test.Run(fmt.Sprintf("withoutCancel=%t/cancelDuringWrite=%t", withoutCancel, cancelDuringWrite), func(test *testing.T) {
				service, store, _, _ := contentService(test)
				parent, cancel := context.WithCancel(test.Context())
				defer cancel()
				var copyError error
				summary, outerError := service.WithContentSnapshot(parent, snapshotContentLimits(), func(cut context.Context, view ContentSnapshotView) error {
					copyContext := cut
					if withoutCancel {
						copyContext = context.WithoutCancel(cut)
					}
					writer := contentInventoryResultWriter{}
					if cancelDuringWrite {
						writer.cancel = cancel
					}
					count, err := view.CopyInventory(copyContext, writer)
					copyError = err
					if cancelDuringWrite {
						if store.ValidateLocalSnapshotContext(context.WithoutCancel(cut)) == nil {
							test.Fatal("destination did not cancel the actual cut")
						}
					} else if err != nil || count <= 0 {
						test.Fatal("live inventory copy failed", err)
					}
					return err
				})
				if cancelDuringWrite {
					if copyError == nil || outerError == nil || summary.ContentBytes() != 0 {
						test.Fatal("canceled original life returned success", copyError, outerError)
					}
				} else if outerError != nil || summary.ContentBytes() <= 0 {
					test.Fatal("live content scope failed", outerError)
				}
			})
		}
	}
}

func TestContentSnapshotInventoryFailurePrecedence(test *testing.T) {
	for _, critical := range []error{state.ErrLocalSnapshotCleanup, state.ErrLocalSnapshotUncertain} {
		for _, ordinaryLater := range []bool{false, true} {
			test.Run(fmt.Sprintf("critical=%s/ordinaryLater=%t", critical, ordinaryLater), func(test *testing.T) {
				service, _, _, _ := contentService(test)
				summary, err := service.WithContentSnapshot(test.Context(), snapshotContentLimits(), func(cut context.Context, view ContentSnapshotView) error {
					if _, err := view.CopyInventory(cut, contentInventoryResultWriter{failure: critical}); !errors.Is(err, critical) {
						test.Fatal("first inventory error lost its cleanup category", err)
					}
					if ordinaryLater {
						if _, err := view.CopyInventory(cut, snapshotShortWriter{}); err == nil {
							test.Fatal("later short write succeeded")
						}
					}
					return nil
				})
				if !errors.Is(err, critical) || summary.ContentBytes() != 0 {
					test.Fatal("later failure downgraded cleanup or returned a candidate", err)
				}
			})
		}
	}
}

type contentInventoryResultWriter struct {
	cancel  context.CancelFunc
	failure error
}

func (writer contentInventoryResultWriter) Write(payload []byte) (int, error) {
	if writer.cancel != nil {
		writer.cancel()
	}
	if writer.failure != nil {
		return 0, writer.failure
	}
	return len(payload), nil
}
