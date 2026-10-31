//go:build linux

package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(tests *testing.M) {
	if handled, code := SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	os.Exit(tests.Run())
}

func TestLocalSnapshotExactBudgets(test *testing.T) {
	store, _ := snapshotTestStore(test)
	snapshotSeedRelations(test, store, 1)
	summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
	if err != nil {
		test.Fatal(err)
	}
	databaseBytes, _ := summary.Metadata().Database()
	referenceBytes, _, _ := summary.Metadata().References()
	for _, kind := range []string{"database", "references", "records", "staging"} {
		for _, subtract := range []int64{0, 1} {
			test.Run(fmt.Sprintf("%s-minus-%d", kind, subtract), func(test *testing.T) {
				limits := snapshotTestLimits()
				limits.MaxDBBytes = databaseBytes
				limits.MaxInventoryBytes = referenceBytes
				limits.MaxRecords = 6
				limits.MaxStagingBytes = max(databaseBytes, 4096) + referenceBytes + 65536
				switch kind {
				case "database":
					limits.MaxDBBytes -= subtract
				case "references":
					limits.MaxInventoryBytes -= subtract
				case "records":
					limits.MaxRecords -= uint64(subtract)
				case "staging":
					limits.MaxStagingBytes -= subtract
				}
				result, err := store.WithLocalSnapshot(test.Context(), limits, func(context.Context, LocalSnapshotView) error { return nil })
				if subtract == 0 {
					if err != nil || result.get == nil {
						test.Fatal("exact budget refused", err)
					}
				} else if !errors.Is(err, ErrLocalSnapshotBudget) || result.get != nil {
					test.Fatal("below budget admitted")
				}
			})
		}
	}
}

func TestLocalSnapshotPhysicalReplacementAndPeerClose(test *testing.T) {
	for _, mode := range []string{"peer-close", "renamed-parent", "replaced-main", "hardlink"} {
		test.Run(mode, func(test *testing.T) {
			store, path := snapshotTestStore(test)
			if mode == "peer-close" {
				peer, err := OpenWithOptions(test.Context(), path, bytes.Repeat([]byte{91}, 32), Options{MaintenanceManaged: true})
				if err != nil {
					test.Fatal(err)
				}
				result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
					if err := peer.Close(); err != nil {
						return err
					}
					_, err := view.CopyDatabase(ctx, io.Discard)
					return err
				})
				if err != nil || result.get == nil {
					test.Fatal("independent peer close invalidated pinned cut", err)
				}
				return
			}
			switch mode {
			case "renamed-parent":
				old := filepath.Dir(path)
				if err := os.Rename(old, old+"-moved"); err != nil {
					test.Fatal(err)
				}
				defer os.Rename(old+"-moved", old)
			case "replaced-main":
				if err := os.Rename(path, path+"-old"); err != nil {
					test.Fatal(err)
				}
				defer os.Rename(path+"-old", path)
				if err := os.WriteFile(path, bytes.Repeat([]byte{0}, 4096), 0600); err != nil {
					test.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+"-alias"); err != nil {
					test.Fatal(err)
				}
				defer os.Remove(path + "-alias")
			}
			called := false
			result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil })
			if err == nil || called || result.get != nil {
				test.Fatal("physical alias/replacement admitted")
			}
			if errors.Is(err, ErrLocalSnapshotUncertain) {
				test.Fatal("known refusal incorrectly poisoned lifecycle")
			}
		})
	}
}

func snapshotTestLimits() LocalSnapshotLimits {
	return LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxRecords: 100000, MaxStagingBytes: 64 << 20, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, DrainTimeout: time.Second, OperationTimeout: 10 * time.Second}
}

func snapshotTestStore(test *testing.T) (*Store, string) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "source.sqlite")
	store, err := OpenWithOptions(test.Context(), path, bytes.Repeat([]byte{91}, 32), Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	if err = store.CreateUser(test.Context(), "snapshot-private-owner"); err != nil {
		test.Fatal(err)
	}
	return store, path
}

func TestLocalSnapshotActualCallback(test *testing.T) {
	store, _ := snapshotTestStore(test)
	called := false
	summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error { called = true; return nil })
	if err != nil || !called || summary.get == nil {
		test.Fatal("real managed snapshot did not reach the scoped consumer", err)
	}
}

func TestLocalSnapshotMetadataIdentityAndCounts(test *testing.T) {
	store, _ := snapshotTestStore(test)
	snapshotSeedRelations(test, store, 1)
	var previous [16]byte
	for range 2 {
		summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
		if err != nil {
			test.Fatal(err)
		}
		metadata := summary.Metadata()
		identity := metadata.SnapshotID()
		format, version, closure := metadata.CandidateFormat()
		if format != "cognate-local-sqlite-candidate" || version != 1 || closure != "not-included" || metadata.SQLiteVersion() == "" {
			test.Fatal("candidate identity overstated or missing")
		}
		if identity == ([16]byte{}) || identity == previous {
			test.Fatal("actual cut identity missing or reused")
		}
		previous = identity
		repositories, skills, packs, items := metadata.ReferenceCounts()
		if repositories != 1 || skills != 1 || packs != 1 || items != 1 {
			test.Fatal("trusted classification counts not available")
		}
	}
}
