package importer

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/plugin"
	_ "github.com/lib-x/entsqlite"
)

//go:embed ccswitch.mjs
var ccSwitchModule string

const maxRepositories = 1000

type Repository struct {
	Owner   string `json:"owner"`
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Enabled bool   `json:"enabled"`
}

type RepositoryPreview struct {
	Found        bool
	Repositories []Repository
}

func PreviewCCSwitchRepos(ctx context.Context, path string) (RepositoryPreview, error) {
	var input []Repository
	found, err := readCCSwitch(ctx, path, func(tx *sql.Tx) error {
		var readError error
		input, readError = readCCSwitchRepositoryRows(ctx, tx)
		return readError
	})
	if err != nil || !found {
		return RepositoryPreview{}, err
	}
	return transformCCSwitchRepositories(ctx, input)
}

type ccSwitchReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readCCSwitchRepositoryRows(ctx context.Context, database ccSwitchReader) ([]Repository, error) {
	var kind string
	if err := database.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name = 'skill_repos'`).Scan(&kind); err != nil || kind != "table" {
		return nil, databaseError(ctx)
	}
	rows, err := database.QueryContext(ctx, `SELECT substr(owner,1,257), substr(name,1,257), substr(branch,1,257), length(CAST(owner AS BLOB)), length(CAST(name AS BLOB)), length(CAST(branch AS BLOB)), enabled FROM skill_repos ORDER BY owner, name LIMIT 1001`)
	if err != nil {
		return nil, databaseError(ctx)
	}
	defer rows.Close()
	input := make([]Repository, 0)
	for rows.Next() {
		var item Repository
		var ownerBytes, nameBytes, branchBytes, enabled int64
		if err := rows.Scan(&item.Owner, &item.Name, &item.Branch, &ownerBytes, &nameBytes, &branchBytes, &enabled); err != nil {
			return nil, databaseError(ctx)
		}
		if len(input) >= maxRepositories || ownerBytes < 1 || ownerBytes > 256 || nameBytes < 1 || nameBytes > 256 || branchBytes < 0 || branchBytes > 256 || (enabled != 0 && enabled != 1) || !utf8.ValidString(item.Owner) || !utf8.ValidString(item.Name) || !utf8.ValidString(item.Branch) {
			return nil, fmt.Errorf("invalid cc-switch repository record")
		}
		if _, err := repository.GitHubURL(item.Owner + "/" + item.Name); err != nil {
			return nil, fmt.Errorf("invalid cc-switch repository record")
		}
		item.Enabled = enabled == 1
		input = append(input, item)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError(ctx)
	}
	if err := rows.Close(); err != nil {
		return nil, databaseError(ctx)
	}
	return input, nil
}

func transformCCSwitchRepositories(ctx context.Context, input []Repository) (RepositoryPreview, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return RepositoryPreview{}, fmt.Errorf("encode cc-switch repositories")
	}
	var results []Repository
	if err := plugin.RunJSON(ctx, ccSwitchModule, "listRepos", encoded, &results); err != nil {
		return RepositoryPreview{}, err
	}
	if len(results) != len(input) {
		return RepositoryPreview{}, fmt.Errorf("invalid cc-switch plugin result")
	}
	for index, item := range results {
		expectedBranch := input[index].Branch
		if strings.EqualFold(expectedBranch, "HEAD") {
			expectedBranch = ""
		}
		if item.Owner != input[index].Owner || item.Name != input[index].Name || item.Branch != expectedBranch || item.Enabled != input[index].Enabled {
			return RepositoryPreview{}, fmt.Errorf("invalid cc-switch plugin result")
		}
	}
	return RepositoryPreview{Found: true, Repositories: results}, nil
}

func readCCSwitch(ctx context.Context, path string, read func(*sql.Tx) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for _, component := range strings.FieldsFunc(path, func(character rune) bool { return character == '/' || character == '\\' }) {
		if component == ".." {
			return false, fmt.Errorf("cc-switch database path cannot contain parent traversal")
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("invalid cc-switch database path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("cc-switch database is unavailable or not a regular file")
	}
	if err := validateDatabaseTrust(path, info); err != nil {
		return false, err
	}
	if err := validateSidecars(path); err != nil {
		return false, err
	}
	filePath := filepath.ToSlash(path)
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	address := url.URL{Scheme: "file", Path: filePath}
	parameters := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(OFF)", "busy_timeout(1000)"}}
	address.RawQuery = parameters.Encode()
	database, err := sql.Open("sqlite3", address.String())
	if err != nil {
		return false, fmt.Errorf("open cc-switch database")
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, databaseError(ctx)
	}
	defer tx.Rollback()
	if err := read(tx); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, databaseError(ctx)
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return false, fmt.Errorf("cc-switch database changed during preview")
	}
	if err := validateDatabaseTrust(path, current); err != nil {
		return false, err
	}
	if err := validateSidecars(path); err != nil {
		return false, err
	}
	return true, nil
}

func validateSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecar := path + suffix
		info, err := os.Lstat(sidecar)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("cc-switch database sidecar is not trusted")
		}
		if err := validateDatabaseTrust(sidecar, info); err != nil {
			return fmt.Errorf("cc-switch database sidecar is not trusted")
		}
	}
	return nil
}

func databaseError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("cannot read cc-switch repository data")
}
