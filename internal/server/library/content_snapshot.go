package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
)

type ContentSnapshotLimits struct {
	Database                           state.LocalSnapshotLimits
	MaxArchives                        uint32
	MaxInventoryBytes, MaxContentBytes int64
}

type ContentSnapshotSummary struct {
	snapshotArchivePrivate
	database                     state.LocalSnapshotMetadata
	inventoryBytes, contentBytes int64
	inventoryHash, closureHash   [32]byte
	records, owners              uint64
	archives                     uint32
}

func (summary ContentSnapshotSummary) Database() state.LocalSnapshotMetadata { return summary.database }
func (summary ContentSnapshotSummary) Inventory() (int64, [32]byte, uint64, uint64) {
	return summary.inventoryBytes, summary.inventoryHash, summary.records, summary.owners
}
func (summary ContentSnapshotSummary) ContentBytes() int64     { return summary.contentBytes }
func (summary ContentSnapshotSummary) Archives() uint32        { return summary.archives }
func (summary ContentSnapshotSummary) ClosureSHA256() [32]byte { return summary.closureHash }

type ContentArchive struct {
	snapshotArchivePrivate
	hash  string
	bytes int64
}

func (archive ContentArchive) Hash() string { return archive.hash }
func (archive ContentArchive) Bytes() int64 { return archive.bytes }

type ContentSnapshotView struct {
	snapshotArchivePrivate
	get func() *contentSnapshotScope
}

type contentSnapshotScope struct {
	live             atomic.Bool
	store            *state.Store
	database         state.LocalSnapshotView
	source           SnapshotArchiveSource
	summary          ContentSnapshotSummary
	inventory        []byte
	inventoryMutex   sync.RWMutex
	inventoryFailure error
	failureMutex     sync.Mutex
	archives         []ContentArchive
}

type contentRepository struct {
	hash   string
	count  int64
	skills map[string]string
}
type contentInventoryArchive struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type contentInventoryHeader struct {
	Format     string                    `json:"format"`
	Version    uint32                    `json:"version"`
	SnapshotID string                    `json:"snapshot_id"`
	References uint64                    `json:"references"`
	Archives   []contentInventoryArchive `json:"archives"`
}

func (service *Service) WithContentSnapshot(ctx context.Context, limits ContentSnapshotLimits, consume func(context.Context, ContentSnapshotView) error) (summary ContentSnapshotSummary, returnedError error) {
	if ctx == nil || service == nil || consume == nil || limits.MaxArchives == 0 || limits.MaxArchives > 100000 || limits.MaxInventoryBytes <= 0 || limits.MaxInventoryBytes > 64<<20 || limits.MaxContentBytes <= 0 || limits.MaxContentBytes > 1<<40 || limits.Database.MaxRecords > 1000000 || limits.Database.OperationTimeout <= 0 {
		return summary, ErrSnapshotArchiveInvalid
	}
	ctx, done, err := service.beginOperation(ctx, limits.Database.OperationTimeout)
	if err != nil {
		return summary, err
	}
	defer done()
	if err := service.reserveSlot(); err != nil {
		return summary, err
	}
	defer func() { <-service.slots }()
	err = service.cache.WithSnapshotSource(ctx, service.store, func(ctx context.Context, source SnapshotArchiveSource) error {
		database, err := service.store.WithLocalSnapshot(ctx, limits.Database, func(ctx context.Context, view state.LocalSnapshotView) (returnedError error) {
			scope, err := service.buildContentSnapshot(ctx, view, source, limits)
			if err != nil {
				return err
			}
			scope.live.Store(true)
			defer func() {
				scope.live.Store(false)
				scope.inventoryMutex.Lock()
				clear(scope.inventory)
				scope.failureMutex.Lock()
				returnedError = snapshotArchiveMerge(returnedError, scope.inventoryFailure)
				scope.failureMutex.Unlock()
				scope.inventoryMutex.Unlock()
			}()
			if err := consume(ctx, ContentSnapshotView{get: func() *contentSnapshotScope { return scope }}); err != nil {
				return err
			}
			summary = scope.summary
			return nil
		})
		if err != nil {
			return err
		}
		summary.database = database.Metadata()
		return nil
	})
	if err != nil {
		return ContentSnapshotSummary{}, snapshotArchiveError(ctx, err)
	}
	return summary, nil
}

func (service *Service) buildContentSnapshot(ctx context.Context, view state.LocalSnapshotView, source SnapshotArchiveSource, limits ContentSnapshotLimits) (*contentSnapshotScope, error) {
	metadata, err := view.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	databaseBytes, databaseHash := metadata.Database()
	if databaseBytes > limits.MaxContentBytes {
		return nil, state.ErrLocalSnapshotBudget
	}
	repositories := make(map[int64]*contentRepository)
	byHash := make(map[string][]*contentRepository)
	owners := make(map[int64]struct{})
	var cursor state.LocalSnapshotCursor
	var recordCount uint64
	for {
		page, err := view.NextReferences(ctx, cursor, 128)
		if err != nil {
			return nil, err
		}
		for _, reference := range page.Rows() {
			recordCount++
			switch reference.Kind() {
			case "repository":
				owner, _, _, hash, count, _ := reference.Repository()
				identity, _ := reference.Identity()
				if _, exists := byHash[hash]; !exists && uint32(len(byHash)) >= limits.MaxArchives {
					return nil, state.ErrLocalSnapshotBudget
				}
				entry := &contentRepository{hash: hash, count: count, skills: make(map[string]string)}
				repositories[identity] = entry
				byHash[hash] = append(byHash[hash], entry)
				owners[owner] = struct{}{}
			case "skill":
				repositoryID, path, hash := reference.Skill()
				entry := repositories[repositoryID]
				if entry == nil {
					return nil, ErrSnapshotArchiveInvalid
				}
				if _, exists := entry.skills[path]; exists {
					return nil, ErrSnapshotArchiveInvalid
				}
				entry.skills[path] = hash
			case "pack":
				owner, _, _ := reference.Pack()
				owners[owner] = struct{}{}
			}
		}
		if page.Done() {
			break
		}
		cursor = page.Cursor()
	}
	_, _, expectedRecords := metadata.References()
	if recordCount != expectedRecords {
		return nil, ErrSnapshotArchiveInvalid
	}
	scope := &contentSnapshotScope{store: service.store, database: view, source: source}
	header := contentInventoryHeader{Format: "cognate-content-inventory", Version: 1, References: recordCount, Archives: make([]contentInventoryArchive, 0, len(byHash))}
	id := metadata.SnapshotID()
	header.SnapshotID = hex.EncodeToString(id[:])
	closure := sha256.New()
	_, _ = closure.Write([]byte("cognate/f4/content-closure/v1\x00"))
	_, _ = closure.Write(databaseHash[:])
	_, referencesHash, _ := metadata.References()
	_, _ = closure.Write(referencesHash[:])
	contentBytes := databaseBytes
	for _, hash := range slices.Sorted(maps.Keys(byHash)) {
		copied, err := inspectContentSnapshotArchive(ctx, source, hash, byHash[hash])
		if err != nil {
			return nil, err
		}
		if copied.Bytes() > limits.MaxContentBytes-contentBytes {
			return nil, state.ErrLocalSnapshotBudget
		}
		contentBytes += copied.Bytes()
		scope.archives = append(scope.archives, ContentArchive{hash: hash, bytes: copied.Bytes()})
		header.Archives = append(header.Archives, contentInventoryArchive{SHA256: hash, Bytes: copied.Bytes()})
		digest := copied.SHA256()
		_, _ = closure.Write(digest[:])
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(copied.Bytes()))
		_, _ = closure.Write(length[:])
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return nil, ErrSnapshotArchiveInvalid
	}
	referencesBytes, _, _ := metadata.References()
	if int64(len(encoded)+1) > limits.MaxInventoryBytes || referencesBytes > limits.MaxInventoryBytes-int64(len(encoded)+1) {
		return nil, state.ErrLocalSnapshotBudget
	}
	var inventory bytes.Buffer
	inventory.Grow(len(encoded) + 1 + int(referencesBytes))
	inventory.Write(encoded)
	inventory.WriteByte('\n')
	if _, err := view.CopyReferences(ctx, &inventory); err != nil {
		clear(inventory.Bytes())
		return nil, err
	}
	if int64(inventory.Len()) > limits.MaxContentBytes-contentBytes {
		clear(inventory.Bytes())
		return nil, state.ErrLocalSnapshotBudget
	}
	scope.inventory = inventory.Bytes()
	scope.summary = ContentSnapshotSummary{database: metadata, inventoryBytes: int64(inventory.Len()), inventoryHash: sha256.Sum256(scope.inventory), contentBytes: contentBytes + int64(inventory.Len()), records: recordCount + 1, owners: uint64(len(owners)), archives: uint32(len(scope.archives))}
	copy(scope.summary.closureHash[:], closure.Sum(nil))
	return scope, nil
}

func inspectContentSnapshotArchive(ctx context.Context, source SnapshotArchiveSource, hash string, expected []*contentRepository) (ArchiveCopySummary, error) {
	var contents bytes.Buffer
	defer func() { clear(contents.Bytes()) }()
	copied, err := source.CopyVerified(ctx, hash, &contents)
	if err != nil {
		return ArchiveCopySummary{}, err
	}
	inspected, err := repository.InspectArchive(ctx, contents.Bytes())
	if err != nil || inspected.Hash != hash {
		return ArchiveCopySummary{}, ErrSnapshotArchiveInvalid
	}
	for _, expected := range expected {
		if int64(len(inspected.Skills)) != expected.count || len(expected.skills) != len(inspected.Skills) {
			return ArchiveCopySummary{}, ErrSnapshotArchiveInvalid
		}
		for _, skill := range inspected.Skills {
			if expected.skills[skill.Path] != skill.ContentHash {
				return ArchiveCopySummary{}, ErrSnapshotArchiveInvalid
			}
		}
	}
	return copied, nil
}

func (view ContentSnapshotView) enter(ctx context.Context) (*contentSnapshotScope, error) {
	if ctx == nil || view.get == nil {
		return nil, ErrSnapshotArchiveInvalid
	}
	scope := view.get()
	if !scope.live.Load() {
		return nil, ErrSnapshotArchiveInvalid
	}
	if err := scope.store.ValidateLocalSnapshotContext(ctx); err != nil {
		return nil, err
	}
	return scope, nil
}

func (view ContentSnapshotView) Metadata(ctx context.Context) (ContentSnapshotSummary, error) {
	scope, err := view.enter(ctx)
	if err != nil {
		return ContentSnapshotSummary{}, err
	}
	return scope.summary, nil
}
func (view ContentSnapshotView) ArchiveFiles(ctx context.Context) ([]ContentArchive, error) {
	scope, err := view.enter(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Clone(scope.archives), nil
}
func (view ContentSnapshotView) CopyDatabase(ctx context.Context, output io.Writer) (int64, error) {
	scope, err := view.enter(ctx)
	if err != nil {
		return 0, err
	}
	return scope.database.CopyDatabase(ctx, output)
}
func (view ContentSnapshotView) CopyInventory(ctx context.Context, output io.Writer) (int64, error) {
	scope, err := view.enter(ctx)
	if err != nil {
		return 0, err
	}
	if output == nil {
		return 0, ErrSnapshotArchiveInvalid
	}
	scope.inventoryMutex.RLock()
	defer scope.inventoryMutex.RUnlock()
	if !scope.live.Load() {
		return 0, ErrSnapshotArchiveInvalid
	}
	count, err := io.Copy(output, cacheContextReader{ctx: ctx, reader: bytes.NewReader(scope.inventory)})
	err = snapshotArchiveMerge(err, scope.store.ValidateLocalSnapshotContext(ctx))
	if err != nil {
		err = snapshotArchiveError(ctx, err)
		scope.failureMutex.Lock()
		scope.inventoryFailure = snapshotArchiveMerge(scope.inventoryFailure, err)
		scope.failureMutex.Unlock()
	}
	return count, err
}
func (view ContentSnapshotView) CopyArchive(ctx context.Context, hash string, output io.Writer) (ArchiveCopySummary, error) {
	scope, err := view.enter(ctx)
	if err != nil {
		return ArchiveCopySummary{}, err
	}
	for _, archive := range scope.archives {
		if archive.hash == hash {
			return scope.source.CopyVerified(ctx, hash, output)
		}
	}
	return ArchiveCopySummary{}, ErrSnapshotArchiveInvalid
}
