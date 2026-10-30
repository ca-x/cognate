package state_test

import (
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/importreceipt"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestImportReceiptSchemaMigrationAndReopen(test *testing.T) {
	path := filepath.Join(privateDirectory(test), "imports.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.ExecContext(test.Context(), `CREATE TABLE users (id integer PRIMARY KEY AUTOINCREMENT, user_key text NOT NULL UNIQUE); INSERT INTO users (user_key) VALUES ('legacy-owner')`); err != nil {
		test.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{37}, 32)
	store, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	var ownerID int
	if err := database.QueryRowContext(test.Context(), `SELECT id FROM users WHERE user_key='legacy-owner'`).Scan(&ownerID); err != nil {
		test.Fatal("migration lost existing user")
	}
	if _, err := database.ExecContext(test.Context(), `PRAGMA foreign_keys = ON`); err != nil {
		test.Fatal(err)
	}
	insert := func(operation, ticket []byte, userID int) error {
		_, err := database.ExecContext(test.Context(), `INSERT INTO import_receipts (user_id, operation_id, ticket_id, request_digest, origin_digest, revision, phase, execution_id, created_at, updated_at, ciphertext, charge) VALUES (?, ?, ?, ?, ?, 1, 'reserved', ?, ?, ?, ?, 131072)`, userID, operation, ticket, bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 16), time.Now(), time.Now(), []byte("synthetic ciphertext"))
		return err
	}
	operation, ticket := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)
	if err := insert(operation, ticket, ownerID); err != nil {
		test.Fatalf("receipt migration unavailable: %v", err)
	}
	if err := insert(operation, bytes.Repeat([]byte{6}, 16), ownerID); err == nil {
		test.Fatal("duplicate owner operation accepted")
	}
	if err := insert(bytes.Repeat([]byte{7}, 16), ticket, ownerID); err == nil {
		test.Fatal("duplicate owner ticket accepted")
	}
	if err := insert(bytes.Repeat([]byte{8}, 16), bytes.Repeat([]byte{9}, 16), ownerID+100); err == nil {
		test.Fatal("orphan receipt accepted")
	}
	other, err := database.ExecContext(test.Context(), `INSERT INTO users (user_key) VALUES ('other-owner')`)
	if err != nil {
		test.Fatal(err)
	}
	otherID, err := other.LastInsertId()
	if err != nil || insert(operation, ticket, int(otherID)) != nil {
		test.Fatal("receipt uniqueness is not scoped to owner")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err := database.QueryRowContext(test.Context(), `SELECT count(*) FROM import_receipts`).Scan(&count); err != nil || count != 2 {
		test.Fatal("receipt not retained across reopen")
	}
	if _, err := database.ExecContext(test.Context(), `DELETE FROM users WHERE id=?`, ownerID); err != nil {
		test.Fatal(err)
	}
	if err := database.QueryRowContext(test.Context(), `SELECT count(*) FROM import_receipts WHERE user_id=?`, ownerID).Scan(&count); err != nil || count != 0 {
		test.Fatal("owner deletion did not cascade to receipt")
	}
	if err := database.QueryRowContext(test.Context(), `SELECT count(*) FROM import_receipts WHERE user_id=?`, otherID).Scan(&count); err != nil || count != 1 {
		test.Fatal("owner deletion removed another owner's receipt")
	}
}

func TestImportReceiptSchemaValidationAndPrivacy(test *testing.T) {
	path := filepath.Join(privateDirectory(test), "validation.db")
	client, err := ent.Open("sqlite3", path+"?_fk=1")
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	if err := client.Schema.Create(test.Context()); err != nil {
		test.Fatal(err)
	}
	owner, err := client.User.Create().SetUserKey("synthetic-owner").Save(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	build := func() *ent.ImportReceiptCreate {
		return client.ImportReceipt.Create().SetUserID(owner.ID).
			SetOperationID(bytes.Repeat([]byte{1}, 16)).SetTicketID(bytes.Repeat([]byte{2}, 16)).
			SetRequestDigest(bytes.Repeat([]byte{3}, 32)).SetOriginDigest(bytes.Repeat([]byte{4}, 32)).
			SetPhase(importreceipt.PhaseReserved).SetExecutionID(bytes.Repeat([]byte{5}, 16)).
			SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).SetCiphertext([]byte("synthetic-private-receipt")).SetCharge(131072)
	}
	for _, mutate := range []func(*ent.ImportReceiptCreate){
		func(builder *ent.ImportReceiptCreate) { builder.SetOperationID(make([]byte, 15)) },
		func(builder *ent.ImportReceiptCreate) { builder.SetOperationID(make([]byte, 17)) },
		func(builder *ent.ImportReceiptCreate) { builder.SetTicketID(make([]byte, 15)) },
		func(builder *ent.ImportReceiptCreate) { builder.SetRequestDigest(make([]byte, 31)) },
		func(builder *ent.ImportReceiptCreate) { builder.SetOriginDigest(make([]byte, 33)) },
		func(builder *ent.ImportReceiptCreate) { builder.SetExecutionID(make([]byte, 17)) },
		func(builder *ent.ImportReceiptCreate) { builder.SetRevision(0) },
		func(builder *ent.ImportReceiptCreate) { builder.SetPhase("unknown") },
		func(builder *ent.ImportReceiptCreate) { builder.SetCiphertext(nil) },
		func(builder *ent.ImportReceiptCreate) { builder.SetCharge(131071) },
		func(builder *ent.ImportReceiptCreate) { builder.SetCharge(131073) },
	} {
		builder := build()
		mutate(builder)
		if _, err := builder.Save(test.Context()); err == nil {
			test.Fatal("invalid receipt field accepted")
		}
	}
	receipt, err := build().Save(test.Context())
	if err != nil || receipt.Revision != 1 {
		test.Fatal("valid receipt rejected or revision default missing")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || strings.Contains(string(encoded), "ciphertext") || strings.Contains(string(encoded), "ticket_id") || strings.Contains(string(encoded), "execution_id") || strings.Contains(fmt.Sprint(receipt), "synthetic-private-receipt") {
		test.Fatal("sensitive receipt field exposed")
	}
	if count, err := client.ImportReceipt.Query().Count(test.Context()); err != nil || count != 1 {
		test.Fatal("invalid receipts were written")
	}
}
