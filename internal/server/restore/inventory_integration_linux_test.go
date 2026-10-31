package restore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestMain(tests *testing.M) {
	if handled, code := state.SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	if handled, code := state.RestoreDatabaseWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	os.Exit(tests.Run())
}

type inventoryFixtureFetcher struct{ archive *repository.ArchiveSnapshot }

func (fetcher inventoryFixtureFetcher) Fetch(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetcher.archive, nil
}

func TestInventoryProductionSnapshotAndRestoreEvidence(test *testing.T) {
	for _, owners := range []int{0, 2} {
		name := "empty"
		if owners != 0 {
			name = "shared-archive-two-owners"
		}
		test.Run(name, func(test *testing.T) {
			source := test.TempDir()
			if err := os.Chmod(source, 0700); err != nil {
				test.Fatal(err)
			}
			key := bytes.Repeat([]byte{115}, 32)
			store, err := state.OpenWithOptions(test.Context(), filepath.Join(source, "server.db"), key, state.Options{MaintenanceManaged: true})
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = store.Close() })
			cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(source, "archives"), library.CacheOptions{MaintenanceManaged: true})
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = cache.Close() })
			var encoded bytes.Buffer
			compressed := gzip.NewWriter(&encoded)
			archiveWriter := tar.NewWriter(compressed)
			content := "---\nname: Inventory\ndescription: Production inventory compatibility\n---\nfixture"
			if err := archiveWriter.WriteHeader(&tar.Header{Name: "source-main/SKILL.md", Mode: 0644, Size: int64(len(content))}); err != nil {
				test.Fatal(err)
			}
			if _, err := io.WriteString(archiveWriter, content); err != nil {
				test.Fatal(err)
			}
			if err := errors.Join(archiveWriter.Close(), compressed.Close()); err != nil {
				test.Fatal(err)
			}
			archive, err := repository.InspectArchive(test.Context(), encoded.Bytes())
			if err != nil {
				test.Fatal(err)
			}
			service, err := library.NewService(store, cache, inventoryFixtureFetcher{archive: archive})
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = service.Close() })
			if err := store.CreateUser(test.Context(), "inventory-unreferenced-owner"); err != nil {
				test.Fatal(err)
			}
			for _, owner := range []string{"inventory-owner-one", "inventory-owner-two"}[:owners] {
				if err := store.CreateUser(test.Context(), owner); err != nil {
					test.Fatal(err)
				}
				entry, err := service.Collect(test.Context(), owner, "org/"+owner, "")
				if err != nil {
					test.Fatal(err)
				}
				entry, err = service.Get(test.Context(), owner, entry.ID)
				if err != nil {
					test.Fatal(err)
				}
				if _, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "inventory pack", Items: []state.PackSelection{{SkillID: entry.Skills[0].ID, Slug: "same"}}}); err != nil {
					test.Fatal(err)
				}
				category, err := store.CreateCategory(test.Context(), owner, "private category")
				if err != nil {
					test.Fatal(err)
				}
				if _, err := store.SetSkillCategories(test.Context(), owner, entry.Skills[0].ID, []string{category.ID}); err != nil {
					test.Fatal(err)
				}
			}
			candidate := test.TempDir()
			if err := os.Chmod(candidate, 0700); err != nil {
				test.Fatal(err)
			}
			output, err := os.OpenFile(filepath.Join(candidate, "server.db"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				test.Fatal(err)
			}
			databaseLimits := state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}
			var inventory bytes.Buffer
			summary, snapshotError := service.WithContentSnapshot(test.Context(), library.ContentSnapshotLimits{Database: databaseLimits, MaxArchives: 1000, MaxInventoryBytes: 8 << 20, MaxContentBytes: 128 << 20}, func(ctx context.Context, view library.ContentSnapshotView) error {
				if _, err := view.CopyDatabase(ctx, output); err != nil {
					return err
				}
				_, err := view.CopyInventory(ctx, &inventory)
				return err
			})
			if err := errors.Join(snapshotError, output.Sync(), output.Close()); err != nil {
				test.Fatal(err)
			}
			if err := errors.Join(service.Close(), cache.Close(), store.Close()); err != nil {
				test.Fatal(err)
			}
			if err := os.Rename(source, source+"-offline"); err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = os.Rename(source+"-offline", source) })
			parent, err := os.Open(candidate)
			if err != nil {
				test.Fatal(err)
			}
			defer parent.Close()
			database, err := os.Open(filepath.Join(candidate, "server.db"))
			if err != nil {
				test.Fatal(err)
			}
			defer database.Close()
			evidence, err := state.InspectRestoreDatabase(test.Context(), parent, database, key, databaseLimits)
			if err != nil || !evidence.Valid() {
				test.Fatal("real candidate database refused", err)
			}
			databaseBytes, databaseHash := evidence.Database()
			if expectedBytes, expectedHash := summary.Database().Database(); databaseBytes != expectedBytes || databaseHash != expectedHash {
				test.Fatal("database and inventory not from one cut")
			}
			inventoryBytes, inventoryHash, recordCount, ownerCount := summary.Inventory()
			cut := summary.Database().SnapshotID()
			closure := summary.ClosureSHA256()
			metadata := backup.SealMetadata{CutID: hex.EncodeToString(cut[:]), Database: backup.SealFileDescription{Bytes: databaseBytes, SHA256: hex.EncodeToString(databaseHash[:])}, Inventory: backup.SealInventoryDescription{Bytes: inventoryBytes, SHA256: hex.EncodeToString(inventoryHash[:]), Records: uint32(recordCount), Owners: uint32(ownerCount)}, Archives: summary.Archives(), ClosureSHA256: hex.EncodeToString(closure[:])}
			referenceBytes, referenceHash, references := evidence.References()
			payload := bytes.Clone(inventory.Bytes())
			limits := inventoryLimits{bytes: inventoryBytes, archives: 1000, contentBytes: summary.ContentBytes()}
			description, err := readInventory(test.Context(), bytes.NewReader(payload), metadata, inventoryReferences{bytes: referenceBytes, digest: referenceHash, records: references}, limits)
			if err != nil || description.contentBytes != summary.ContentBytes() || uint64(len(description.archives)) != uint64(summary.Archives()) || !bytes.Equal(payload, inventory.Bytes()) || ownerCount != uint64(owners) {
				test.Fatal("production inventory incompatible or mutated", err)
			}
			if owners == 2 && (len(description.archives) != 1 || description.archives[0].SHA256 != archive.Hash) {
				test.Fatal("shared archive did not deduplicate")
			}
		})
	}
}
