package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/ca-x/cognate/internal/server/ent"
)

func restoreAssetsFixture(test *testing.T, assets, relations int) restoreFixtureData {
	test.Helper()
	fixture := restoreFixture(test, assets, relations)
	var statements []string
	for index := range relations {
		statements = append(statements, fmt.Sprintf("INSERT INTO pack_configuration_attachments(public_id,asset_revision,target_id,ordinal,configuration_asset_id,pack_id) VALUES ('%s',1,'codex.provider.user',0,%d,%d)", rand.Text(), index%2+1, index+1))
	}
	restoreMutation(test, fixture, statements...)
	return fixture
}

func restoreAssetsPool(test *testing.T, fixture restoreFixtureData) *sql.DB {
	test.Helper()
	pool, err := sql.Open("sqlite3", snapshotAddress(fixture.database.Name(), "ro", "query_only(1)", "trusted_schema(0)", "temp_store(MEMORY)", "mmap_size(0)")+"&immutable=1")
	if err != nil {
		test.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	test.Cleanup(func() {
		if err := pool.Close(); err != nil {
			test.Fatal(err)
		}
	})
	return pool
}

func restoreAssetsRebind(test *testing.T, fixture restoreFixtureData, identity int) {
	test.Helper()
	pool := restoreAssetsPool(test, fixture)
	var entry ent.ConfigurationAsset
	var owner string
	err := pool.QueryRowContext(test.Context(), "SELECT a.public_id,a.agent_id,a.kind,a.name,a.schema_version,a.revision,a.user_id,u.user_key FROM configuration_assets a JOIN users u ON u.id=a.user_id WHERE a.id=?", identity).Scan(&entry.PublicID, &entry.AgentID, &entry.Kind, &entry.Name, &entry.SchemaVersion, &entry.Revision, &entry.UserID, &owner)
	if err := errors.Join(err, pool.Close()); err != nil {
		test.Fatal(err)
	}
	aead, err := newConfigurationAEAD(fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	ciphertext := aead.Seal(nil, nil, []byte("restore-private-config-sentinel"), configurationAAD(owner, &entry))
	restoreMutation(test, fixture, fmt.Sprintf("UPDATE configuration_assets SET ciphertext=x'%s' WHERE id=%d", hex.EncodeToString(ciphertext), identity))
}

func restoreAssetsDecision(test *testing.T, fixture restoreFixtureData, limits LocalSnapshotLimits, want error) {
	test.Helper()
	before := restoreFingerprint(test, fixture)
	evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, limits)
	if restoreFingerprint(test, fixture) != before {
		test.Fatal("auxiliary validation modified candidate")
	}
	if _, err := fixture.parent.Stat(); err != nil {
		test.Fatal("borrowed parent closed")
	}
	if _, err := fixture.database.ReadAt(make([]byte, 16), 0); err != nil {
		test.Fatal("borrowed database closed")
	}
	if want == nil {
		if err != nil || !evidence.Valid() {
			test.Fatal("valid auxiliary relationships refused", err)
		}
	} else if !errors.Is(err, want) || evidence != (RestoreDatabaseEvidence{}) {
		test.Fatalf("auxiliary relationship admitted or wrong category: error=%v Valid=%v want=%v", err, evidence.Valid(), want)
	}
}

func TestRestoreAssetsRelationships(test *testing.T) {
	for _, scenario := range []struct {
		name       string
		statements []string
	}{
		{"order-owner", []string{"DELETE FROM configuration_order_entries WHERE order_id=2", "DELETE FROM configuration_orders WHERE id=2", "UPDATE configuration_orders SET user_id=2 WHERE id=1"}},
		{"order-agent", []string{"UPDATE configuration_orders SET agent_id='claude' WHERE id=1"}},
		{"order-kind", []string{"UPDATE configuration_orders SET kind='mcp' WHERE id=1"}},
		{"order-unknown-partition", []string{"INSERT INTO configuration_orders(agent_id,kind,revision,user_id) VALUES('future-agent','provider',1,1)"}},
		{"order-zero-revision", []string{"UPDATE configuration_orders SET revision=0 WHERE id=1"}},
		{"order-negative-revision", []string{"UPDATE configuration_orders SET revision=-1 WHERE id=1"}},
		{"order-fractional-revision", []string{"UPDATE configuration_orders SET revision=1.5 WHERE id=1"}},
		{"order-missing-member", []string{"DELETE FROM configuration_order_entries WHERE asset_id=3"}},
		{"order-extra-member", []string{"DELETE FROM configuration_order_entries WHERE asset_id=2", "INSERT INTO configuration_order_entries(order_id,asset_id,ordinal) VALUES(1,2,2)"}},
		{"order-ordinal-gap", []string{"UPDATE configuration_order_entries SET ordinal=2 WHERE asset_id=3"}},
		{"order-negative-ordinal", []string{"UPDATE configuration_order_entries SET ordinal=-1 WHERE asset_id=1"}},
		{"order-fractional-ordinal", []string{"UPDATE configuration_order_entries SET ordinal=1.5 WHERE asset_id=3"}},
		{"attachment-owner", []string{"UPDATE pack_configuration_attachments SET configuration_asset_id=2 WHERE pack_id=1"}},
		{"attachment-zero-pin", []string{"UPDATE pack_configuration_attachments SET asset_revision=0 WHERE pack_id=1"}},
		{"attachment-negative-pin", []string{"UPDATE pack_configuration_attachments SET asset_revision=-1 WHERE pack_id=1"}},
		{"attachment-public-id", []string{"UPDATE pack_configuration_attachments SET public_id='invalid-key' WHERE pack_id=1"}},
		{"attachment-public-id-alphabet", []string{"UPDATE pack_configuration_attachments SET public_id='AAAAAAAAAAAAAAAAAAAAAAAAA1' WHERE pack_id=1"}},
		{"attachment-target-utf8", []string{"UPDATE pack_configuration_attachments SET target_id=CAST(x'ff' AS TEXT) WHERE pack_id=1"}},
		{"attachment-first-ordinal", []string{"UPDATE pack_configuration_attachments SET ordinal=1 WHERE pack_id=1"}},
		{"attachment-negative-ordinal", []string{"UPDATE pack_configuration_attachments SET ordinal=-1 WHERE pack_id=1"}},
		{"pack-zero-configuration-revision", []string{"UPDATE packs SET configuration_revision=0 WHERE id=1"}},
		{"pack-negative-configuration-revision", []string{"UPDATE packs SET configuration_revision=-1 WHERE id=1"}},
		{"pack-fractional-configuration-revision", []string{"UPDATE packs SET configuration_revision=1.5 WHERE id=1"}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			fixture := restoreAssetsFixture(test, 4, 2)
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
			restoreMutation(test, fixture, scenario.statements...)
			restoreDomainFixtureCheck(test, fixture, "server_metadata WHERE name='credential-key-v1'")
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), ErrRestoreInvalid)
		})
	}
}

func TestRestoreAssetsHiddenDomains(test *testing.T) {
	for _, table := range []string{"configuration_orders", "configuration_order_entries", "pack_configuration_attachments"} {
		for _, identity := range []int64{0, -1, -1 << 63} {
			test.Run(fmt.Sprintf("%s/id=%d", table, identity), func(test *testing.T) {
				fixture := restoreAssetsFixture(test, 4, 2)
				restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
				if table == "configuration_orders" {
					restoreMutation(test, fixture, fmt.Sprintf("UPDATE configuration_order_entries SET order_id=%d WHERE order_id=1", identity))
				}
				restoreMutation(test, fixture, fmt.Sprintf("UPDATE %s SET id=%d WHERE id=(SELECT min(id) FROM %s)", table, identity, table))
				restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("%s WHERE id=%d", table, identity))
				restoreAssetsDecision(test, fixture, snapshotTestLimits(), ErrRestoreInvalid)
			})
		}
	}
}

func TestRestoreAssetsReadMetadata(test *testing.T) {
	for _, statement := range []string{
		"UPDATE configuration_assets SET public_id='invalid-asset-key' WHERE id=1",
		"UPDATE configuration_assets SET public_id='AAAAAAAAAAAAAAAAAAAAAAAAA1' WHERE id=1",
		"UPDATE configuration_assets SET revision=0 WHERE id=1",
		"UPDATE configuration_assets SET revision=-1 WHERE id=1",
		"UPDATE configuration_assets SET schema_version=0 WHERE id=1",
		"UPDATE configuration_assets SET schema_version=65536 WHERE id=1",
		"UPDATE configuration_assets SET agent_id=CAST(x'ff' AS TEXT) WHERE id=1",
	} {
		test.Run(statement, func(test *testing.T) {
			fixture := restoreAssetsFixture(test, 4, 2)
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
			restoreMutation(test, fixture, statement)
			if strings.Contains(statement, "agent_id") {
				restoreMutation(test, fixture, "DELETE FROM configuration_order_entries WHERE order_id=1", "DELETE FROM configuration_orders WHERE id=1")
			}
			restoreAssetsRebind(test, fixture, 1)
			restoreDomainFixtureCheck(test, fixture, "configuration_assets WHERE id=1")
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), ErrRestoreInvalid)
		})
	}
}

func TestRestoreAssetsReadableStates(test *testing.T) {
	for _, scenario := range []string{"empty-order", "legacy-no-order", "stale-old-pin", "stale-future-pin", "unsupported-schema", "unsupported-schema-max", "unsupported-agent", "unsupported-target", "empty-target", "long-unsupported-target", "max-revisions"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := restoreAssetsFixture(test, 4, 2)
			switch scenario {
			case "empty-order":
				restoreMutation(test, fixture, "INSERT INTO configuration_orders(agent_id,kind,revision,user_id) VALUES('claude','provider',7,1)")
			case "legacy-no-order":
				restoreMutation(test, fixture, "DELETE FROM configuration_order_entries", "DELETE FROM configuration_orders")
			case "stale-old-pin":
				restoreMutation(test, fixture, "UPDATE configuration_assets SET revision=2 WHERE id=1")
				restoreAssetsRebind(test, fixture, 1)
			case "stale-future-pin":
				restoreMutation(test, fixture, "UPDATE pack_configuration_attachments SET asset_revision=2 WHERE pack_id=1")
			case "unsupported-schema":
				restoreMutation(test, fixture, "UPDATE configuration_assets SET schema_version=2 WHERE id=1")
				restoreAssetsRebind(test, fixture, 1)
			case "unsupported-schema-max":
				restoreMutation(test, fixture, "UPDATE configuration_assets SET schema_version=65535 WHERE id=1")
				restoreAssetsRebind(test, fixture, 1)
			case "unsupported-agent":
				restoreMutation(test, fixture, "UPDATE configuration_assets SET agent_id='future-agent' WHERE id=1", "DELETE FROM configuration_order_entries WHERE order_id=1", "DELETE FROM configuration_orders WHERE id=1")
				restoreAssetsRebind(test, fixture, 1)
			case "unsupported-target":
				restoreMutation(test, fixture, "UPDATE pack_configuration_attachments SET target_id='future.target' WHERE pack_id=1")
			case "empty-target":
				restoreMutation(test, fixture, "UPDATE pack_configuration_attachments SET target_id='' WHERE pack_id=1")
			case "long-unsupported-target":
				restoreMutation(test, fixture, "UPDATE pack_configuration_attachments SET target_id='"+strings.Repeat("future", 100)+"' WHERE pack_id=1")
			case "max-revisions":
				restoreMutation(test, fixture, "UPDATE configuration_orders SET revision=9223372036854775807", "UPDATE packs SET configuration_revision=9223372036854775807", "UPDATE pack_configuration_attachments SET asset_revision=9223372036854775807")
			}
			restoreAssetsProducerRead(test, fixture, scenario)
			restoreDomainFixtureCheck(test, fixture, "pack_configuration_attachments WHERE id=1")
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
		})
	}
}

func restoreAssetsProducerRead(test *testing.T, fixture restoreFixtureData, scenario string) {
	test.Helper()
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", restoreAssetsPool(test, fixture))))
	transaction, err := client.Tx(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer func() {
		if err := transaction.Rollback(); err != nil {
			test.Fatal(err)
		}
	}()
	entry, err := transaction.Pack.Get(test.Context(), 1)
	if err != nil {
		test.Fatal(err)
	}
	snapshot, err := packConfigurationSnapshotTx(test.Context(), transaction, entry)
	if err != nil || len(snapshot.Entries) != 1 {
		test.Fatal("producer attachment reader rejected positive fixture", err)
	}
	want := PackConfigurationCurrent
	if strings.HasPrefix(scenario, "stale-") || scenario == "max-revisions" {
		want = PackConfigurationStaleAsset
	} else if strings.Contains(scenario, "unsupported") || scenario == "empty-target" {
		want = PackConfigurationUnsupported
	}
	if snapshot.Entries[0].State != want {
		test.Fatal("producer state differs", snapshot.Entries[0].State, want)
	}
	if scenario == "empty-order" || scenario == "legacy-no-order" {
		agent, revision, count := "claude", int64(7), 0
		if scenario == "legacy-no-order" {
			agent, revision, count = "codex", 0, 2
		}
		_, order, err := configurationOrderData(test.Context(), transaction.Client(), "snapshot-private-owner", agent, ConfigurationKindProvider, "")
		if err != nil || order.Revision != revision || len(order.Assets) != count {
			test.Fatal("producer order reader rejected retained revision/fallback", err)
		}
	}
}

func restoreAssetsOwnerRow(test *testing.T, fixture restoreFixtureData, ordinal int) []string {
	test.Helper()
	entry := ent.ConfigurationAsset{PublicID: rand.Text(), AgentID: "codex", Kind: "provider", Name: fmt.Sprintf("auxiliary-boundary-%d", ordinal), SchemaVersion: 1, Revision: 1, UserID: 1}
	aead, err := newConfigurationAEAD(fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	ciphertext := aead.Seal(nil, nil, []byte("restore-private-config-sentinel"), configurationAAD("snapshot-private-owner", &entry))
	return []string{
		fmt.Sprintf("INSERT INTO configuration_assets(public_id,agent_id,kind,name,schema_version,revision,created_at,updated_at,ciphertext,user_id) SELECT '%s','codex','provider','%s',1,1,created_at,updated_at,x'%s',1 FROM configuration_assets WHERE id=1", entry.PublicID, entry.Name, hex.EncodeToString(ciphertext)),
		fmt.Sprintf("INSERT INTO configuration_order_entries(order_id,asset_id,ordinal) VALUES(1,(SELECT max(id) FROM configuration_assets),%d)", ordinal),
	}
}

func TestRestoreAssetsBusinessBoundaries(test *testing.T) {
	test.Run("250-owner-assets", func(test *testing.T) {
		fixture := restoreAssetsFixture(test, 2, 2)
		var statements []string
		for ordinal := 1; ordinal < MaxConfigurationAssets; ordinal++ {
			statements = append(statements, restoreAssetsOwnerRow(test, fixture, ordinal)...)
		}
		restoreMutation(test, fixture, statements...)
		restoreDomainFixtureCheck(test, fixture, "configuration_orders WHERE id=1")
		restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
		restoreMutation(test, fixture, restoreAssetsOwnerRow(test, fixture, MaxConfigurationAssets)...)
		restoreDomainFixtureCheck(test, fixture, "configuration_orders WHERE id=1")
		restoreAssetsDecision(test, fixture, snapshotTestLimits(), ErrRestoreInvalid)
	})
	test.Run("16-pack-attachments", func(test *testing.T) {
		fixture := restoreAssetsFixture(test, 4, 2)
		statements := []string{"DELETE FROM pack_configuration_attachments WHERE pack_id=1"}
		for ordinal := range MaxPackConfigurationAttachments {
			statements = append(statements, fmt.Sprintf("INSERT INTO pack_configuration_attachments(public_id,asset_revision,target_id,ordinal,configuration_asset_id,pack_id) VALUES('%s',1,'future-%d',%d,1,1)", rand.Text(), ordinal, ordinal))
		}
		restoreMutation(test, fixture, statements...)
		restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
		restoreMutation(test, fixture, fmt.Sprintf("INSERT INTO pack_configuration_attachments(public_id,asset_revision,target_id,ordinal,configuration_asset_id,pack_id) VALUES('%s',1,'future-16',16,1,1)", rand.Text()))
		restoreDomainFixtureCheck(test, fixture, "packs WHERE id=1")
		restoreAssetsDecision(test, fixture, snapshotTestLimits(), ErrRestoreInvalid)
	})
}

func TestRestoreAssetsBudgetMonotonicity(test *testing.T) {
	fixture := restoreAssetsFixture(test, 4, 2)
	for _, maximum := range []uint64{1000000, 1000001, math.MaxInt64 - 1, math.MaxInt64} {
		test.Run(fmt.Sprint(maximum), func(test *testing.T) {
			limits := snapshotTestLimits()
			limits.MaxRecords = maximum
			if !limits.valid() {
				test.Fatal("existing T1 budget contract rejected positive control")
			}
			restoreAssetsDecision(test, fixture, limits, nil)
			test.Log("unchanged small candidate admitted with valid T1 budget", maximum)
		})
	}
	pool := restoreAssetsPool(test, fixture)
	for _, maximum := range []uint64{0, math.MaxInt64 + 1, math.MaxUint64} {
		test.Run(fmt.Sprint(maximum), func(test *testing.T) {
			limits := snapshotTestLimits()
			limits.MaxRecords = maximum
			if limits.valid() {
				test.Fatal("invalid T1 budget accepted")
			}
			restoreAssetsDecision(test, fixture, limits, ErrRestoreInvalid)
			if err := restoreDatabaseAssets(test.Context(), pool, maximum); err != ErrRestoreInvalid {
				test.Fatal("invalid helper limit accepted", err)
			}
		})
	}
}

func TestRestoreAssetsSaturatedEmptyStreams(test *testing.T) {
	fixture := restoreAssetsFixture(test, 4, 2)
	restoreMutation(test, fixture, "DELETE FROM configuration_order_entries", "DELETE FROM configuration_orders", "DELETE FROM pack_configuration_attachments")
	limits := snapshotTestLimits()
	limits.MaxRecords = math.MaxInt64
	restoreAssetsDecision(test, fixture, limits, nil)
}

func TestRestoreAssetsRecordBudget(test *testing.T) {
	fixture := restoreAssetsFixture(test, 20, 2)
	limits := snapshotTestLimits()
	limits.MaxRecords = 24
	restoreAssetsDecision(test, fixture, limits, nil)
	limits.MaxRecords--
	restoreAssetsDecision(test, fixture, limits, ErrRestoreBudget)
	pool := restoreAssetsPool(test, fixture)
	for _, maximum := range []uint64{0, math.MaxInt64 + 1, math.MaxUint64} {
		if err := restoreDatabaseAssets(test.Context(), pool, maximum); err != ErrRestoreInvalid {
			test.Fatal("invalid helper limit accepted", err)
		}
	}
	if err := restoreDatabaseAssets(nil, pool, 24); err != ErrRestoreInvalid {
		test.Fatal("nil context accepted", err)
	}
	if err := restoreDatabaseAssets(test.Context(), nil, 24); err != ErrRestoreInvalid {
		test.Fatal("nil pool accepted", err)
	}
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("auxiliary-private-cancellation-cause"))
	for _, maximum := range []uint64{24, 1000001, math.MaxInt64} {
		if err := restoreDatabaseAssets(ctx, pool, maximum); err != context.Canceled {
			test.Fatal("bare cancellation lost", maximum, err)
		}
	}
	if err := pool.PingContext(test.Context()); err != nil {
		test.Fatal("borrowed pool closed", err)
	}
}

type restoreAssetsFaultDriver struct{}

func (restoreAssetsFaultDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unused driver open")
}

type restoreAssetsFaultConnector struct {
	query func(string, []driver.NamedValue) (driver.Rows, error)
}

func (connector restoreAssetsFaultConnector) Connect(context.Context) (driver.Conn, error) {
	return restoreAssetsFaultConnection{query: connector.query}, nil
}

func (restoreAssetsFaultConnector) Driver() driver.Driver { return restoreAssetsFaultDriver{} }

type restoreAssetsFaultConnection struct {
	query func(string, []driver.NamedValue) (driver.Rows, error)
}

func (restoreAssetsFaultConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unused prepare")
}
func (restoreAssetsFaultConnection) Close() error { return nil }
func (restoreAssetsFaultConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("unused transaction")
}
func (restoreAssetsFaultConnection) Ping(context.Context) error { return nil }
func (connection restoreAssetsFaultConnection) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	return connection.query(query, arguments)
}

type restoreAssetsFaultRows struct {
	values     []driver.Value
	readError  error
	closeError error
	cancel     context.CancelFunc
	closes     *atomic.Int32
	read       bool
	onClose    func()
}

func (rows *restoreAssetsFaultRows) Columns() []string {
	return make([]string, len(rows.values))
}

func (rows *restoreAssetsFaultRows) Close() error {
	rows.closes.Add(1)
	if rows.onClose != nil {
		rows.onClose()
	}
	return rows.closeError
}

func (rows *restoreAssetsFaultRows) Next(output []driver.Value) error {
	if rows.cancel != nil {
		rows.cancel()
	}
	if rows.readError != nil {
		return rows.readError
	}
	if rows.read {
		return io.EOF
	}
	rows.read = true
	copy(output, rows.values)
	return nil
}

func TestRestoreAssetsSQLLimitBoundaries(test *testing.T) {
	for _, scenario := range []struct {
		name    string
		maximum uint64
		consume bool
		limits  []int64
		want    error
	}{
		{"empty-minimum", 1, false, []int64{2, 2, 2}, nil},
		{"empty-above-million", 1000001, false, []int64{1000002, 1000002, 1000002}, nil},
		{"empty-before-saturation", math.MaxInt64 - 1, false, []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64}, nil},
		{"empty-saturated", math.MaxInt64, false, []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64}, nil},
		{"consume-saturated", math.MaxInt64, true, []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64 - 1}, nil},
		{"consume-exact", 3, true, []int64{4, 3, 2}, nil},
		{"consume-attachment-extra", 2, true, []int64{3, 2, 1}, ErrRestoreBudget},
		{"consume-entry-extra", 1, true, []int64{2, 1}, ErrRestoreBudget},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			var closes atomic.Int32
			calls := 0
			publicID := rand.Text()
			query := func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
				rows := &restoreAssetsFaultRows{values: []driver.Value{false}, closes: &closes}
				if strings.HasPrefix(query, "SELECT EXISTS") {
					return rows, nil
				}
				if calls >= len(scenario.limits) || len(arguments) == 0 {
					return nil, errors.New("unexpected SQL limit query")
				}
				limit, ok := arguments[len(arguments)-1].Value.(int64)
				if !ok || limit < 1 || limit != scenario.limits[calls] {
					test.Errorf("stream %d SQL LIMIT binding=%v want positive int64 %d", calls, arguments[len(arguments)-1].Value, scenario.limits[calls])
				}
				calls++
				switch {
				case strings.Contains(query, "FROM configuration_orders o"):
					rows.values = []driver.Value{int64(1), int64(1), "codex", "provider", int64(1), int64(1), int64(1)}
				case strings.Contains(query, "FROM configuration_order_entries e LEFT"):
					rows.values = []driver.Value{int64(1), int64(1), int64(1), int64(0), true}
				case strings.Contains(query, "FROM pack_configuration_attachments c LEFT"):
					rows.values = []driver.Value{int64(1), int64(1), int64(1), int64(0), publicID, int64(1), "future-target", int64(1), int64(1), publicID, int64(1), int64(65535), "future-agent", "future-kind"}
				default:
					return nil, errors.New("unexpected SQL stream")
				}
				rows.read = !scenario.consume
				return rows, nil
			}
			pool := sql.OpenDB(restoreAssetsFaultConnector{query: query})
			defer pool.Close()
			pool.SetMaxOpenConns(1)
			if got := restoreDatabaseAssets(test.Context(), pool, scenario.maximum); got != scenario.want {
				test.Fatal("SQL stream budget category changed", got, scenario.want)
			}
			if calls != len(scenario.limits) || int(closes.Load()) != calls+2 {
				test.Fatal("SQL streams or owned rows incomplete", calls, closes.Load())
			}
			if err := pool.PingContext(test.Context()); err != nil {
				test.Fatal("borrowed pool closed", err)
			}
		})
	}
}

type restoreAssetsCloseContext struct {
	context.Context
	closed *atomic.Bool
	cancel context.CancelFunc
}

func (ctx restoreAssetsCloseContext) Err() error {
	if ctx.closed.Load() {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestRestoreAssetsSQLFailureOwnership(test *testing.T) {
	for _, mode := range []string{"eof-close", "invalid-and-close", "budget-and-close", "read-error", "query-error", "cancel-next", "observed-close-before-cancel"} {
		test.Run(mode, func(test *testing.T) {
			base, cancel := context.WithCancel(test.Context())
			defer cancel()
			var ctx context.Context = base
			var closed atomic.Bool
			if mode == "observed-close-before-cancel" {
				ctx = restoreAssetsCloseContext{Context: base, closed: &closed, cancel: cancel}
			}
			var closes atomic.Int32
			query := func(query string, _ []driver.NamedValue) (driver.Rows, error) {
				if mode == "query-error" {
					return nil, errors.New("auxiliary-private-SQL-path")
				}
				rows := &restoreAssetsFaultRows{values: []driver.Value{false}, closes: &closes}
				if strings.Contains(query, "FROM configuration_orders o") {
					rows.values = []driver.Value{int64(1), int64(1), "codex", "provider", int64(1), int64(0), int64(0)}
					switch mode {
					case "eof-close":
						rows.closeError = syscall.EIO
					case "invalid-and-close":
						rows.values[0] = int64(0)
						rows.closeError = syscall.EIO
					case "read-error":
						rows.readError = errors.New("auxiliary-private-row-error")
					case "cancel-next":
						rows.cancel = cancel
					case "observed-close-before-cancel":
						rows.closeError = syscall.EIO
						rows.onClose = func() { closed.Store(true) }
					}
				} else if strings.Contains(query, "FROM configuration_order_entries e LEFT") {
					rows.values = []driver.Value{int64(1), int64(1), int64(1), int64(0), true}
					rows.closeError = syscall.EIO
				}
				return rows, nil
			}
			pool := sql.OpenDB(restoreAssetsFaultConnector{query: query})
			defer pool.Close()
			pool.SetMaxOpenConns(1)
			maximum := uint64(100)
			if mode == "budget-and-close" {
				maximum = 1
			}
			got := restoreDatabaseAssets(ctx, pool, maximum)
			want := ErrRestoreUncertain
			if mode == "query-error" {
				want = ErrRestoreInvalid
			} else if mode == "cancel-next" {
				if got != context.Canceled {
					test.Fatal("in-flight cancellation lost", got)
				}
				want = nil
			}
			if want != nil && got != want {
				test.Fatal("SQL lifecycle category lost", got, want)
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", got, got, got), "auxiliary-private") {
				test.Fatal("SQL/cause details leaked")
			}
			if mode != "query-error" && closes.Load() < 3 {
				test.Fatal("owned SQL rows did not close")
			}
			if err := pool.PingContext(test.Context()); err != nil {
				test.Fatal("helper closed borrowed pool", err)
			}
		})
	}
}

func TestRestoreAssetsCompleteStreams(test *testing.T) {
	for _, scenario := range []string{"entry-tail", "attachment-tail", "empty-order-tail"} {
		test.Run(scenario, func(test *testing.T) {
			assets, relations := 4, 2
			if scenario == "entry-tail" {
				assets = 260
			} else if scenario == "attachment-tail" {
				relations = 130
			}
			fixture := restoreAssetsFixture(test, assets, relations)
			if scenario == "empty-order-tail" {
				for index := range 130 {
					restoreMutation(test, fixture, fmt.Sprintf("INSERT INTO users(user_key) VALUES('restore-auxiliary-owner-%d')", index), "INSERT INTO configuration_orders(user_id,agent_id,kind,revision) VALUES((SELECT max(id) FROM users),'codex','provider',1)")
				}
			}
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), nil)
			switch scenario {
			case "entry-tail":
				restoreMutation(test, fixture, "UPDATE configuration_order_entries SET ordinal=ordinal+1 WHERE id=(SELECT max(id) FROM configuration_order_entries)")
			case "attachment-tail":
				restoreMutation(test, fixture, "UPDATE pack_configuration_attachments SET configuration_asset_id=1 WHERE id=(SELECT max(id) FROM pack_configuration_attachments)")
			case "empty-order-tail":
				restoreMutation(test, fixture, "UPDATE configuration_orders SET revision=0 WHERE id=(SELECT max(id) FROM configuration_orders)")
			}
			restoreDomainFixtureCheck(test, fixture, "server_metadata WHERE name='credential-key-v1'")
			restoreAssetsDecision(test, fixture, snapshotTestLimits(), ErrRestoreInvalid)
		})
	}
}
