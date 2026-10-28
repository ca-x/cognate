package state

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureCatalogIntegrity(ctx context.Context, address string) error {
	database, err := sql.Open("sqlite3", address)
	if err != nil {
		return fmt.Errorf("open catalog integrity database: %w", err)
	}
	defer database.Close()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("start catalog integrity transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var invalid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM git_hub_memberships AS membership
		LEFT JOIN git_hub_lists AS list ON list.id = membership.list_id
		LEFT JOIN git_hub_repositories AS repository ON repository.id = membership.repository_id
		WHERE list.id IS NULL OR repository.id IS NULL OR list.user_id != repository.user_id OR list.viewer_id != repository.viewer_id
	)`).Scan(&invalid); err != nil {
		return fmt.Errorf("inspect catalog membership ownership: %w", err)
	}
	if invalid {
		return fmt.Errorf("catalog contains cross-account or orphaned memberships")
	}
	statements := []string{
		`DROP TRIGGER IF EXISTS skillhub_membership_owner_insert`,
		`DROP TRIGGER IF EXISTS skillhub_membership_owner_update`,
		`DROP TRIGGER IF EXISTS skillhub_list_owner_immutable`,
		`DROP TRIGGER IF EXISTS skillhub_repository_owner_immutable`,
		`CREATE TRIGGER skillhub_membership_owner_insert BEFORE INSERT ON git_hub_memberships
		WHEN NOT EXISTS (SELECT 1 FROM git_hub_lists AS list JOIN git_hub_repositories AS repository
		ON list.user_id = repository.user_id AND list.viewer_id = repository.viewer_id
		WHERE list.id = NEW.list_id AND repository.id = NEW.repository_id)
		BEGIN SELECT RAISE(ABORT, 'membership owner mismatch'); END`,
		`CREATE TRIGGER skillhub_membership_owner_update BEFORE UPDATE OF list_id, repository_id ON git_hub_memberships
		WHEN NOT EXISTS (SELECT 1 FROM git_hub_lists AS list JOIN git_hub_repositories AS repository
		ON list.user_id = repository.user_id AND list.viewer_id = repository.viewer_id
		WHERE list.id = NEW.list_id AND repository.id = NEW.repository_id)
		BEGIN SELECT RAISE(ABORT, 'membership owner mismatch'); END`,
		`CREATE TRIGGER skillhub_list_owner_immutable BEFORE UPDATE OF user_id, viewer_id ON git_hub_lists
		WHEN NEW.user_id != OLD.user_id OR NEW.viewer_id != OLD.viewer_id
		BEGIN SELECT RAISE(ABORT, 'list owner is immutable'); END`,
		`CREATE TRIGGER skillhub_repository_owner_immutable BEFORE UPDATE OF user_id, viewer_id ON git_hub_repositories
		WHEN NEW.user_id != OLD.user_id OR NEW.viewer_id != OLD.viewer_id
		BEGIN SELECT RAISE(ABORT, 'repository owner is immutable'); END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("install catalog integrity constraints: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog integrity constraints: %w", err)
	}
	return nil
}
