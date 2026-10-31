package backup

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestCaptureSnapshotEmptyInstance(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("managed SQLite worker requires Linux")
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "empty.db"), bytes.Repeat([]byte{117}, 32), state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "archives"), library.CacheOptions{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer cache.Close()
	service, err := library.NewService(store, cache, captureFetcher{})
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	engine, _, _ := sealFixture(test)
	captured, err := engine.CaptureSnapshot(test.Context(), service, captureOptions())
	if err != nil {
		test.Fatal("empty installation cannot be captured", err)
	}
	defer captured.Close()
	metadata, err := captured.Metadata(test.Context())
	if err != nil || metadata.Archives != 0 || metadata.Inventory.Records != 1 || metadata.Inventory.Owners != 0 {
		test.Fatal("empty installation metadata is not complete", err)
	}
	var references []ObjectRef
	var files []*os.File
	if err = captured.WithFiles(test.Context(), func(ctx context.Context, ref ObjectRef, input *os.File) error {
		output, err := os.CreateTemp(test.TempDir(), "cipher-*")
		if err != nil {
			return err
		}
		test.Cleanup(func() { _ = output.Close() })
		if _, err = io.Copy(output, input); err != nil {
			return err
		}
		if _, err = output.Seek(0, io.SeekStart); err != nil {
			return err
		}
		references, files = append(references, ref), append(files, output)
		return ctx.Err()
	}); err != nil {
		test.Fatal(err)
	}
	if len(files) != 6 || references[5].read().kind != Complete {
		test.Fatal("empty installation did not produce the complete protocol")
	}
	session, err := engine.BeginOpen(test.Context(), files[0], files[5], Attempt{read: func() attemptData { return references[0].read().attempt }})
	if err != nil {
		test.Fatal(err)
	}
	defer session.Close()
	for _, ordinal := range []int{4, 3, 1, 2} {
		if err := session.AcceptFile(test.Context(), files[ordinal]); err != nil {
			test.Fatal(err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer bundle.Close()
	var inventory bytes.Buffer
	if _, err = bundle.CopyFile(test.Context(), "inventory", "inventory", &inventory); err != nil {
		test.Fatal(err)
	}
	var header struct {
		Format     string     `json:"format"`
		References uint64     `json:"references"`
		Archives   []struct{} `json:"archives"`
	}
	if err = json.Unmarshal(bytes.TrimSpace(inventory.Bytes()), &header); err != nil || header.Format != "cognate-content-inventory" || header.References != 0 || header.Archives == nil || len(header.Archives) != 0 {
		test.Fatal("empty inventory is not an authenticated, explicit closure", err)
	}
}
