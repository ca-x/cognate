package state

import (
	"context"
	"errors"
	"time"

	"github.com/ca-x/skillhub/internal/server/ent"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const catalogTransactionAttempts = 4

func (store *Store) catalogTransaction(ctx context.Context, operation func(*ent.Tx) error) error {
	var err error
	for attempt := range catalogTransactionAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Millisecond << (attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err = func() error {
			tx, err := store.state.client.Tx(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err := operation(tx); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		sqliteError, ok := errors.AsType[*sqlite.Error](err)
		if !ok || sqliteError.Code()&0xff != sqlite3.SQLITE_BUSY {
			return err
		}
	}
	return err
}
