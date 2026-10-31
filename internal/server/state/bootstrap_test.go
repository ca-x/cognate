package state

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/cognate/internal/server/ent/servermetadata"
)

func bootstrapStore(test *testing.T) (*Store, string, []byte) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	key := bytes.Repeat([]byte{71}, 32)
	store, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	return store, path, key
}

func TestBootstrapAdministratorAtomicRollbackAndRestart(test *testing.T) {
	store, path, key := bootstrapStore(test)
	if _, err := store.BootstrapPasswordAdministrator(test.Context(), "admin", nil); !errors.Is(err, ErrBootstrapRequired) {
		test.Fatal("empty instance did not request password")
	}
	pool, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.ExecContext(test.Context(), "CREATE TRIGGER reject_bootstrap BEFORE INSERT ON server_metadata WHEN NEW.name='bootstrap-admin-v1' BEGIN SELECT RAISE(ABORT, 'synthetic bootstrap failure'); END"); err != nil {
		test.Fatal(err)
	}
	hash := bytes.Repeat([]byte{81}, 64)
	if _, err := store.BootstrapPasswordAdministrator(test.Context(), "admin", hash); err == nil {
		test.Fatal("injected binding failure succeeded")
	}
	for _, table := range []string{"users", "local_accounts"} {
		var count int
		if err := pool.QueryRowContext(test.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			test.Fatalf("failed bootstrap left partial %s: %v", table, err)
		}
	}
	if _, err := pool.ExecContext(test.Context(), "DROP TRIGGER reject_bootstrap"); err != nil {
		test.Fatal(err)
	}
	user, err := store.BootstrapPasswordAdministrator(test.Context(), "admin", hash)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	if actual, err := reopened.BootstrapPasswordAdministrator(test.Context(), "admin", nil); err != nil || actual != user {
		test.Fatal("binding did not survive database restart")
	}
	account, err := reopened.PasswordAccount(test.Context(), "admin")
	if err != nil || !bytes.Equal(hash, account.PasswordHash) {
		test.Fatal("password hash did not survive restart")
	}
	if _, err := reopened.state.client.ServerMetadata.Delete().Where(servermetadata.NameEQ(bootstrapMetadataName)).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := reopened.BootstrapPasswordAdministrator(test.Context(), "admin", hash); err == nil {
		test.Fatal("missing binding silently adopted an existing account")
	}
}

func TestBootstrapAdministratorRejectsCorruptedBinding(test *testing.T) {
	store, path, _ := bootstrapStore(test)
	if _, err := store.BootstrapPasswordAdministrator(test.Context(), "admin", bytes.Repeat([]byte{81}, 64)); err != nil {
		test.Fatal(err)
	}
	pool, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.ExecContext(test.Context(), "UPDATE server_metadata SET key_proof=zeroblob(64) WHERE name='bootstrap-admin-v1'"); err != nil {
		test.Fatal(err)
	}
	if _, err := store.BootstrapPasswordAdministrator(test.Context(), "admin", nil); err == nil {
		test.Fatal("corrupt binding accepted")
	}
}
