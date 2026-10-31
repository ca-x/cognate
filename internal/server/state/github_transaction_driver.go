package state

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type githubTransactionContextKey struct{}

type githubTransactionDriver struct {
	*entsql.Driver
	admission *entsql.Driver
}

func newGitHubTransactionDriver(address *url.URL) (*githubTransactionDriver, error) {
	regular, err := entsql.Open(dialect.SQLite, address.String())
	if err != nil {
		return nil, err
	}
	lockAddress := address.Clone()
	parameters := lockAddress.Query()
	for index, pragma := range parameters["_pragma"] {
		if strings.HasPrefix(pragma, "busy_timeout(") {
			parameters["_pragma"][index] = "busy_timeout(0)"
		}
	}
	parameters.Set("_txlock", "immediate")
	lockAddress.RawQuery = parameters.Encode()
	admission, err := entsql.Open(dialect.SQLite, lockAddress.String())
	if err != nil {
		return nil, errors.Join(err, regular.Close())
	}
	return &githubTransactionDriver{Driver: regular, admission: admission}, nil
}

func (driver *githubTransactionDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	if required, _ := ctx.Value(githubTransactionContextKey{}).(bool); !required {
		return driver.Driver.Tx(ctx)
	}
	budget := time.NewTimer(10 * time.Second)
	defer budget.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		transaction, err := driver.admission.Tx(ctx)
		if err == nil {
			return transaction, nil
		}
		failure, sqliteError := errors.AsType[*sqlite.Error](err)
		if !sqliteError || failure.Code() != sqlite3.SQLITE_BUSY {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-budget.C:
			return nil, err
		case <-poll.C:
		}
	}
}

func (driver *githubTransactionDriver) Close() error {
	return errors.Join(driver.admission.Close(), driver.Driver.Close())
}
