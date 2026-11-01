package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func revokeRestoreCredentialsTx(ctx context.Context, tx *sql.Tx, revokedAt time.Time) error {
	if ctx == nil || tx == nil || revokedAt.IsZero() {
		return ErrRestoreInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	foreignKeys, err := restoreCredentialsScalar(ctx, tx, "PRAGMA foreign_keys")
	if err != nil {
		return err
	}
	if foreignKeys != 1 {
		return ErrRestoreInvalid
	}
	invalid, err := restoreCredentialsScalar(ctx, tx, "SELECT count(*) FROM mcp_credentials WHERE typeof(revision)<>'integer' OR revision<1 OR revision>=9223372036854775807")
	if err != nil {
		return err
	}
	if invalid != 0 {
		return ErrRestoreInvalid
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{query: "DELETE FROM native_materialization_grants"},
		{query: "DELETE FROM native_apply_preparations"},
		{query: "DELETE FROM native_consumer_challenges"},
		{query: "DELETE FROM native_consumer_sessions"},
		{query: "DELETE FROM sessions"},
		{query: "UPDATE pack_shares SET revoked_at=?", args: []any{revokedAt.UTC()}},
		{query: "UPDATE mcp_credentials SET revoked_at=?, revision=revision+1", args: []any{revokedAt.UTC()}},
		{query: "UPDATE git_hub_credentials SET status='needs_reauthorization', refresh_attempt='', sync_fetch_id='', collection_cursor=0"},
	} {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return restoreCredentialsFailure(ctx, err)
		}
	}
	return verifyRestoreCredentialsTx(ctx, tx, revokedAt)
}

func verifyRestoreCredentialsTx(ctx context.Context, tx *sql.Tx, revokedAt time.Time) error {
	if ctx == nil || tx == nil || revokedAt.IsZero() {
		return ErrRestoreInvalid
	}
	for _, check := range []struct {
		query string
		args  []any
	}{
		{query: "SELECT (SELECT count(*) FROM native_materialization_grants)+(SELECT count(*) FROM native_apply_preparations)+(SELECT count(*) FROM native_consumer_challenges)+(SELECT count(*) FROM native_consumer_sessions)+(SELECT count(*) FROM sessions)"},
		{query: "SELECT count(*) FROM pack_shares WHERE revoked_at IS NOT ?", args: []any{revokedAt.UTC()}},
		{query: "SELECT count(*) FROM mcp_credentials WHERE revoked_at IS NOT ? OR typeof(revision)<>'integer' OR revision<2", args: []any{revokedAt.UTC()}},
		{query: "SELECT count(*) FROM git_hub_credentials WHERE status IS NOT 'needs_reauthorization' OR refresh_attempt IS NOT '' OR sync_fetch_id IS NOT '' OR collection_cursor IS NOT 0"},
		{query: "SELECT count(*) FROM pragma_foreign_key_check"},
	} {
		invalid, err := restoreCredentialsScalar(ctx, tx, check.query, check.args...)
		if err != nil {
			return err
		}
		if invalid != 0 {
			return ErrRestoreInvalid
		}
	}
	return ctx.Err()
}

func restoreCredentialsScalar(ctx context.Context, tx *sql.Tx, query string, arguments ...any) (int64, error) {
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return 0, restoreCredentialsFailure(ctx, err)
	}
	var value int64
	var readError error
	if !rows.Next() {
		readError = rows.Err()
		if readError == nil {
			readError = ErrRestoreInvalid
		}
	} else {
		readError = rows.Scan(&value)
	}
	if rows.Close() != nil {
		return 0, ErrRestoreUncertain
	}
	if readError != nil {
		if readError == ErrRestoreInvalid {
			return 0, readError
		}
		return 0, ErrRestoreUncertain
	}
	if err := rows.Err(); err != nil {
		return 0, ErrRestoreUncertain
	}
	return value, ctx.Err()
}

func restoreCredentialsFailure(ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil && errors.Is(err, canceled) {
		return canceled
	}
	return ErrRestoreUncertain
}
