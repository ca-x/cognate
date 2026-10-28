package state

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ca-x/skillhub/internal/filelock"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/state/ent"
	"github.com/ca-x/skillhub/internal/state/ent/installedskill"
	"github.com/ca-x/skillhub/internal/state/ent/operation"
	_ "github.com/lib-x/entsqlite"
)

type Installation struct {
	Slug                string
	ContentHash         string
	Generation          string
	SubscriptionManaged bool
	Projections         []Projection
}

type Projection struct {
	Path     string
	Kind     string
	Identity string
}

type Store struct {
	client *ent.Client
}

func Open(ctx context.Context, path string) (*Store, error) {
	absolute, err := ResolveDatabasePath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve state database: %w", err)
	}
	lock, err := filelock.Acquire(ctx, absolute+".schema.lock")
	if err != nil {
		return nil, fmt.Errorf("lock state schema: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := checkDatabaseLinks(absolute); err != nil {
		return nil, fmt.Errorf("inspect state database: %w", err)
	}
	parameters := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(10000)", "synchronous(NORMAL)"} {
		parameters.Add("_pragma", pragma)
	}
	filePath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	address := url.URL{Scheme: "file", Path: filePath, RawQuery: parameters.Encode()}
	if err := migrateLegacyProjections(ctx, address.String()); err != nil {
		return nil, fmt.Errorf("migrate installation state: %w", err)
	}
	client, err := ent.Open("sqlite3", address.String())
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	if err := client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize state database: %w", err)
	}
	if err := ensureOperationClaims(ctx, client); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	return &Store{client: client}, nil
}

func ResolveDatabasePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return databasePath(absolute)
}

func databasePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		return filepath.EvalSymlinks(path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func (store *Store) Close() error {
	return store.client.Close()
}

func (store *Store) Create(ctx context.Context, record Installation) error {
	if err := ValidateSlug(record.Slug); err != nil {
		return err
	}
	if record.Generation != "" || record.SubscriptionManaged {
		return fmt.Errorf("direct installation records cannot claim a generation")
	}
	paths := make([]string, 0, len(record.Projections))
	keys := make([]string, 0, len(record.Projections))
	for _, projection := range record.Projections {
		if projection.Identity != "" {
			return fmt.Errorf("direct installation records cannot claim object identity")
		}
		path, key, err := projectionPath(projection.Path)
		if err != nil {
			return err
		}
		paths = append(paths, path)
		keys = append(keys, key)
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start installation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	if err := checkInstalledConflicts(ctx, tx, record.Slug, keys); err != nil {
		return err
	}
	pending, err := tx.Operation.Query().Where(operation.StatusNotIn(operation.StatusCompleted, operation.StatusCancelled)).WithTargets().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect pending operations: %w", err)
	}
	for _, entry := range pending {
		if sameSlug(entry.Slug, record.Slug) {
			return fmt.Errorf("installation slug is reserved by a pending operation")
		}
		var reserved []string
		for _, target := range entry.Edges.Targets {
			targetKeys, err := targetReservationKeys(target.Path, target.StageName)
			if err != nil {
				return err
			}
			reserved = append(reserved, targetKeys...)
		}
		if err := validateTargetReservations(reserved, keys); err != nil {
			return err
		}
	}
	installed, err := tx.InstalledSkill.Create().SetSlug(record.Slug).SetContentHash(record.ContentHash).Save(ctx)
	if err != nil {
		return fmt.Errorf("record installation: %w", err)
	}
	for index, projection := range record.Projections {
		if err := tx.Projection.Create().SetPath(paths[index]).SetPathKey(keys[index]).SetKind(projection.Kind).SetSkill(installed).Exec(ctx); err != nil {
			return fmt.Errorf("record projection: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit installation: %w", err)
	}
	return nil
}

func ValidateSlug(slug string) error {
	return skill.ValidateSlug(slug)
}

func sameSlug(first, second string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func checkInstalledConflicts(ctx context.Context, tx *ent.Tx, slug string, keys []string) error {
	entries, err := tx.InstalledSkill.Query().WithProjections().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect existing installations: %w", err)
	}
	var reserved []string
	for _, entry := range entries {
		if sameSlug(entry.Slug, slug) {
			return fmt.Errorf("installation slug is already recorded")
		}
		for _, projection := range entry.Edges.Projections {
			reserved = append(reserved, projection.PathKey)
		}
	}
	backupPaths, err := recordedBackupPaths(ctx, tx)
	if err != nil {
		return err
	}
	reserved = append(reserved, backupPaths...)
	return validateTargetReservations(reserved, keys)
}

func projectionPath(path string) (string, string, error) {
	if err := validateProjectionPath(path); err != nil {
		return "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", "", fmt.Errorf("resolve projection parent: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("projection parent must be an existing directory")
	}
	canonicalPath := filepath.Join(parent, filepath.Base(path))
	return canonicalPath, projectionPathKey(canonicalPath), nil
}

func validateProjectionPath(path string) error {
	if strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path {
		return fmt.Errorf("projection path must be absolute, clean and not a filesystem root")
	}
	return nil
}

func projectionPathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func (store *Store) Get(ctx context.Context, slug string) (Installation, error) {
	installed, err := store.client.InstalledSkill.Query().Where(installedskill.SlugEQ(slug)).WithProjections().Only(ctx)
	if err != nil {
		return Installation{}, fmt.Errorf("read installation: %w", err)
	}
	return installationRecord(installed), nil
}

func installationRecord(installed *ent.InstalledSkill) Installation {
	record := Installation{Slug: installed.Slug, ContentHash: installed.ContentHash, Generation: installed.Generation, SubscriptionManaged: installed.SubscriptionManaged}
	for _, projection := range installed.Edges.Projections {
		record.Projections = append(record.Projections, Projection{Path: projection.Path, Kind: projection.Kind, Identity: projection.Identity})
	}
	slices.SortFunc(record.Projections, func(first, second Projection) int { return strings.Compare(first.Path, second.Path) })
	return record
}
