//go:build linux

package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"hash"
	"math"
	"slices"
	"strconv"
	"strings"
)

type restoreRevokeQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func restoreRevokeProjection(ctx context.Context, query restoreRevokeQuery, post bool, limits LocalSnapshotLimits) ([32]byte, error) {
	if _, err := snapshotSchema(ctx, query); err != nil {
		return [32]byte{}, ErrRestoreUncertain
	}
	rows, err := query.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name")
	if err != nil {
		return [32]byte{}, ErrRestoreInvalid
	}
	var tables []string
	for rows.Next() {
		var table string
		if rows.Scan(&table) != nil {
			_ = rows.Close()
			return [32]byte{}, ErrRestoreUncertain
		}
		if len(tables) >= 128 {
			if rows.Close() != nil {
				return [32]byte{}, ErrRestoreUncertain
			}
			return [32]byte{}, ErrRestoreInvalid
		}
		tables = append(tables, table)
	}
	readError := rows.Err()
	if rows.Close() != nil {
		return [32]byte{}, ErrRestoreUncertain
	}
	if readError != nil {
		return [32]byte{}, ErrRestoreUncertain
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("cognate-restore-protected-v1\x00"))
	var records uint64
	var used int64
	for _, table := range tables {
		if slices.Contains([]string{"native_materialization_grants", "native_apply_preparations", "native_consumer_challenges", "native_consumer_sessions", "sessions"}, table) {
			continue
		}
		if err := restoreRevokeProjectTable(ctx, query, hasher, table, post, limits, &records, &used); err != nil {
			return [32]byte{}, err
		}
	}
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result, ctx.Err()
}

func restoreRevokeQuote(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func restoreRevokeProjectTable(ctx context.Context, query restoreRevokeQuery, hasher hash.Hash, table string, post bool, limits LocalSnapshotLimits, records *uint64, used *int64) error {
	rows, err := query.QueryContext(ctx, "SELECT name,pk FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		return ErrRestoreInvalid
	}
	var columns []string
	keys := make(map[int]string)
	for rows.Next() {
		var name string
		var position int
		if rows.Scan(&name, &position) != nil {
			_ = rows.Close()
			return ErrRestoreUncertain
		}
		if len(columns) >= 128 {
			if rows.Close() != nil {
				return ErrRestoreUncertain
			}
			return ErrRestoreInvalid
		}
		if position > 0 {
			keys[position] = restoreRevokeQuote(name)
		}
		if (table == "pack_shares" || table == "mcp_credentials") && name == "revoked_at" || table == "git_hub_credentials" && slices.Contains([]string{"status", "refresh_attempt", "sync_fetch_id", "collection_cursor"}, name) {
			continue
		}
		columns = append(columns, name)
	}
	readError := rows.Err()
	if rows.Close() != nil {
		return ErrRestoreUncertain
	}
	if readError != nil {
		return ErrRestoreUncertain
	}
	if len(columns) == 0 {
		return ErrRestoreInvalid
	}
	var order []string
	for position := 1; position <= len(keys); position++ {
		if keys[position] == "" {
			return ErrRestoreInvalid
		}
		order = append(order, keys[position])
	}
	if len(order) == 0 {
		if table != "sqlite_sequence" {
			return ErrRestoreInvalid
		}
		order = []string{"name"}
	}
	var selection []string
	for _, column := range columns {
		expression := restoreRevokeQuote(column)
		selection = append(selection, "typeof("+expression+")", "CASE WHEN length(CAST("+expression+" AS BLOB))<="+strconv.FormatInt(limits.MaxDBBytes, 10)+" THEN CASE WHEN typeof("+expression+") IN ('text','blob') THEN CAST("+expression+" AS BLOB) ELSE "+expression+" END END")
	}
	rows, err = query.QueryContext(ctx, "SELECT "+strings.Join(selection, ",")+" FROM "+restoreRevokeQuote(table)+" ORDER BY "+strings.Join(order, ","))
	if err != nil {
		return ErrRestoreInvalid
	}
	checkError := func() error {
		restoreRevokeHashCell(hasher, []byte(table))
		for _, name := range columns {
			restoreRevokeHashCell(hasher, []byte(name))
		}
		for rows.Next() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if *records >= limits.MaxRecords {
				return ErrRestoreBudget
			}
			*records++
			_, _ = hasher.Write([]byte{1})
			values := make([]any, len(selection))
			pointers := make([]any, len(values))
			for index := range pointers {
				pointers[index] = &values[index]
			}
			if rows.Scan(pointers...) != nil {
				return ErrRestoreUncertain
			}
			for index, column := range columns {
				kind, ok := values[index*2].(string)
				if !ok {
					return ErrRestoreInvalid
				}
				value := values[index*2+1]
				if post && table == "mcp_credentials" && column == "revision" {
					revision, ok := value.(int64)
					if !ok || kind != "integer" || revision < 2 {
						return ErrRestoreInvalid
					}
					value = revision - 1
				}
				var encoded []byte
				switch kind {
				case "null":
					if value != nil {
						return ErrRestoreInvalid
					}
				case "integer":
					number, ok := value.(int64)
					if !ok {
						return ErrRestoreInvalid
					}
					encoded = binary.BigEndian.AppendUint64(nil, uint64(number))
				case "real":
					number, ok := value.(float64)
					if !ok {
						return ErrRestoreInvalid
					}
					encoded = binary.BigEndian.AppendUint64(nil, math.Float64bits(number))
				case "text", "blob":
					var ok bool
					encoded, ok = value.([]byte)
					if !ok {
						return ErrRestoreInvalid
					}
				default:
					return ErrRestoreInvalid
				}
				cost := int64(len(encoded) + len(kind) + 16)
				if cost > limits.MaxStagingBytes-*used {
					clear(encoded)
					return ErrRestoreBudget
				}
				*used += cost
				restoreRevokeHashCell(hasher, []byte(kind))
				restoreRevokeHashCell(hasher, encoded)
				clear(encoded)
			}
		}
		if rows.Err() != nil {
			return ErrRestoreUncertain
		}
		_, _ = hasher.Write([]byte{0})
		return nil
	}()
	if rows.Close() != nil {
		return ErrRestoreUncertain
	}
	return checkError
}

func restoreRevokeHashCell(hasher hash.Hash, value []byte) {
	_, _ = hasher.Write(binary.BigEndian.AppendUint64(nil, uint64(len(value))))
	_, _ = hasher.Write(value)
}
