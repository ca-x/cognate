package state

import (
	"context"
	"database/sql"
	"fmt"
)

type legacyProjection struct {
	id   int
	path string
	key  string
}

func migrateLegacyProjections(ctx context.Context, address string) error {
	database, err := sql.Open("sqlite3", address)
	if err != nil {
		return err
	}
	defer database.Close()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(projections)`)
	if err != nil {
		return err
	}
	var hasTable, hasKey bool
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		hasTable = true
		hasKey = hasKey || name == "path_key"
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasTable || hasKey {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE projections ADD COLUMN path_key TEXT`); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id, path FROM projections`)
	if err != nil {
		return err
	}
	var projections []legacyProjection
	seen := make(map[string]struct{})
	for rows.Next() {
		var projection legacyProjection
		if err := rows.Scan(&projection.id, &projection.path); err != nil {
			_ = rows.Close()
			return err
		}
		projection.path, projection.key, err = projectionPath(projection.path)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("resolve legacy projection: %w", err)
		}
		if _, duplicate := seen[projection.key]; duplicate {
			_ = rows.Close()
			return fmt.Errorf("legacy projection paths resolve to the same target")
		}
		seen[projection.key] = struct{}{}
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, projection := range projections {
		if _, err := tx.ExecContext(ctx, `UPDATE projections SET path = ?, path_key = ? WHERE id = ?`, projection.path, projection.key, projection.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS skillhub_projection_path_key ON projections(path_key)`); err != nil {
		return err
	}
	return tx.Commit()
}
