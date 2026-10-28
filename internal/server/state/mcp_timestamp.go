package state

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const mcpTimestampBatchSize = 128

func parseMCPTimestamp(value string) (time.Time, error) {
	if value == "" || len(value) > 256 {
		return time.Time{}, ErrMCPUnavailable
	}
	wall, monotonic, hasMonotonic := strings.Cut(value, " m=")
	if hasMonotonic {
		seconds, err := strconv.ParseFloat(monotonic, 64)
		if err != nil || math.IsInf(seconds, 0) || math.IsNaN(seconds) || !strings.HasPrefix(monotonic, "+") && !strings.HasPrefix(monotonic, "-") {
			return time.Time{}, ErrMCPUnavailable
		}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, wall); err == nil {
			return parsed.UTC(), nil
		}
	}
	fields := strings.Fields(wall)
	if len(fields) != 4 {
		return time.Time{}, ErrMCPUnavailable
	}
	parsed, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", strings.Join(fields[:3], " "))
	if err != nil {
		return time.Time{}, ErrMCPUnavailable
	}
	return parsed.UTC(), nil
}

type mcpTimestampRow struct {
	id     int64
	values []sql.NullString
}

type mcpTimestampLayout struct {
	selectQuery string
	updateQuery string
	nullable    []bool
}

func normalizeMCPTimestampBatch(ctx context.Context, database *sql.DB, layout mcpTimestampLayout, cursor int64) (int64, int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return cursor, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, layout.selectQuery, cursor, mcpTimestampBatchSize)
	if err != nil {
		return cursor, 0, err
	}
	entries := make([]mcpTimestampRow, 0, mcpTimestampBatchSize)
	for rows.Next() {
		entry := mcpTimestampRow{values: make([]sql.NullString, len(layout.nullable))}
		destinations := []any{&entry.id}
		for index := range entry.values {
			destinations = append(destinations, &entry.values[index])
		}
		if err := rows.Scan(destinations...); err != nil {
			_ = rows.Close()
			return cursor, 0, err
		}
		entries = append(entries, entry)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return cursor, 0, err
	}
	for _, entry := range entries {
		parameters := make([]any, len(entry.values)+1)
		unchanged := true
		for index, value := range entry.values {
			if !value.Valid {
				if !layout.nullable[index] {
					return cursor, 0, ErrMCPUnavailable
				}
				continue
			}
			parsed, err := parseMCPTimestamp(value.String)
			if err != nil {
				return cursor, 0, err
			}
			parameters[index] = parsed
			unchanged = unchanged && value.String == parsed.String()
		}
		parameters[len(entry.values)] = entry.id
		if !unchanged {
			_, err := tx.ExecContext(ctx, layout.updateQuery, parameters...)
			if err != nil {
				return cursor, 0, err
			}
		}
		cursor = entry.id
	}
	if err := tx.Commit(); err != nil {
		return cursor, 0, err
	}
	return cursor, len(entries), nil
}

func normalizeMCPTimestamps(ctx context.Context, address string) error {
	database, err := sql.Open("sqlite3", address)
	if err != nil {
		return ErrMCPUnavailable
	}
	defer database.Close()
	layouts := []mcpTimestampLayout{
		{
			selectQuery: `SELECT id, substr(CAST(created_at AS TEXT), 1, 257),
				substr(CAST(expires_at AS TEXT), 1, 257), substr(CAST(revoked_at AS TEXT), 1, 257)
				FROM mcp_credentials WHERE id > ? ORDER BY id LIMIT ?`,
			updateQuery: `UPDATE mcp_credentials SET created_at=?, expires_at=?, revoked_at=? WHERE id=?`,
			nullable:    []bool{false, false, true},
		},
		{
			selectQuery: `SELECT id, substr(CAST(created_at AS TEXT), 1, 257)
				FROM mcp_audits WHERE id > ? ORDER BY id LIMIT ?`,
			updateQuery: `UPDATE mcp_audits SET created_at=? WHERE id=?`,
			nullable:    []bool{false},
		},
	}
	for _, layout := range layouts {
		var cursor int64
		for {
			next, count, err := normalizeMCPTimestampBatch(ctx, database, layout, cursor)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrMCPUnavailable
			}
			if count < mcpTimestampBatchSize {
				break
			}
			cursor = next
		}
	}
	return nil
}
