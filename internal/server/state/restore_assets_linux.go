package state

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"
)

func restoreDatabaseAssets(ctx context.Context, pool *sql.DB, maximum uint64) error {
	if ctx == nil || pool == nil || maximum == 0 || maximum > math.MaxInt64 {
		return ErrRestoreInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, proof := range []struct {
		query string
		args  []any
	}{
		{"SELECT EXISTS(SELECT 1 FROM packs WHERE typeof(configuration_revision)<>'integer' OR configuration_revision<1)", nil},
		{"SELECT EXISTS(SELECT 1 FROM configuration_assets GROUP BY user_id HAVING count(*)>?)", []any{MaxConfigurationAssets}},
	} {
		seen := 0
		err := restoreAssetsRead(ctx, pool, proof.query, proof.args, func(rows *sql.Rows) error {
			seen++
			var invalid bool
			if rows.Scan(&invalid) != nil || invalid || seen != 1 {
				return ErrRestoreInvalid
			}
			return nil
		})
		if err != nil {
			return err
		}
		if seen != 1 {
			return ErrRestoreInvalid
		}
	}
	remaining := maximum
	consume := func() error {
		if remaining == 0 {
			return ErrRestoreBudget
		}
		remaining--
		return nil
	}
	readLimit := func() int64 {
		return int64(min(remaining+1, uint64(math.MaxInt64)))
	}
	orders := "SELECT o.id,o.user_id," + snapshotText("o.agent_id", 64) + "," + snapshotText("o.kind", 64) + ",o.revision,(SELECT count(*) FROM (SELECT 1 FROM configuration_assets a WHERE a.user_id=o.user_id AND a.agent_id=o.agent_id AND a.kind=o.kind LIMIT ?)),(SELECT count(*) FROM (SELECT 1 FROM configuration_order_entries e WHERE e.order_id=o.id LIMIT ?)) FROM configuration_orders o ORDER BY o.id LIMIT ?"
	err := restoreAssetsRead(ctx, pool, orders, []any{MaxConfigurationAssets + 1, MaxConfigurationAssets + 1, readLimit()}, func(rows *sql.Rows) error {
		if err := consume(); err != nil {
			return err
		}
		var identity, owner, revision, assets, entries int64
		var agent, kind string
		if rows.Scan(&identity, &owner, &agent, &kind, &revision, &assets, &entries) != nil || identity < 1 || owner < 1 || revision < 1 || !configurationOrderPartition(agent, ConfigurationKind(kind)) || assets != entries || assets > MaxConfigurationAssets {
			return ErrRestoreInvalid
		}
		return nil
	})
	if err != nil {
		return err
	}
	var lastOrder, orderOrdinal int64
	entries := "SELECT e.id,e.order_id,e.asset_id,e.ordinal,(o.user_id=a.user_id AND o.agent_id=a.agent_id AND o.kind=a.kind) FROM configuration_order_entries e LEFT JOIN configuration_orders o ON o.id=e.order_id LEFT JOIN configuration_assets a ON a.id=e.asset_id ORDER BY e.order_id,e.ordinal,e.id LIMIT ?"
	err = restoreAssetsRead(ctx, pool, entries, []any{readLimit()}, func(rows *sql.Rows) error {
		if err := consume(); err != nil {
			return err
		}
		var identity, order, asset, ordinal int64
		var partition bool
		if rows.Scan(&identity, &order, &asset, &ordinal, &partition) != nil || identity < 1 || order < 1 || asset < 1 || !partition {
			return ErrRestoreInvalid
		}
		if order != lastOrder {
			lastOrder, orderOrdinal = order, 0
		}
		if ordinal != orderOrdinal || orderOrdinal >= MaxConfigurationAssets {
			return ErrRestoreInvalid
		}
		orderOrdinal++
		return nil
	})
	if err != nil {
		return err
	}
	var lastPack, packOrdinal int64
	attachments := "SELECT c.id,c.pack_id,c.configuration_asset_id,c.ordinal," + snapshotText("c.public_id", 26) + ",c.asset_revision,c.target_id,p.user_id,a.user_id," + snapshotText("a.public_id", 26) + ",a.revision,a.schema_version," + snapshotText("a.agent_id", 64) + "," + snapshotText("a.kind", 64) + " FROM pack_configuration_attachments c LEFT JOIN packs p ON p.id=c.pack_id LEFT JOIN configuration_assets a ON a.id=c.configuration_asset_id ORDER BY c.pack_id,c.ordinal,c.id LIMIT ?"
	err = restoreAssetsRead(ctx, pool, attachments, []any{readLimit()}, func(rows *sql.Rows) error {
		if err := consume(); err != nil {
			return err
		}
		var identity, pack, asset, ordinal, pin, owner, assetOwner, revision, schema int64
		var publicID, target, assetPublicID, agent, kind string
		if rows.Scan(&identity, &pack, &asset, &ordinal, &publicID, &pin, &target, &owner, &assetOwner, &assetPublicID, &revision, &schema, &agent, &kind) != nil || identity < 1 || pack < 1 || asset < 1 || owner < 1 || assetOwner != owner || !validPackKey(publicID) || !validPackKey(assetPublicID) || pin < 1 || revision < 1 || schema < 1 || schema > 65535 || !utf8.ValidString(agent) || !utf8.ValidString(kind) || !utf8.ValidString(target) {
			return ErrRestoreInvalid
		}
		if pack != lastPack {
			lastPack, packOrdinal = pack, 0
		}
		if ordinal != packOrdinal || packOrdinal >= MaxPackConfigurationAttachments {
			return ErrRestoreInvalid
		}
		packOrdinal++
		return nil
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}

func restoreAssetsRead(ctx context.Context, pool *sql.DB, query string, arguments []any, consume func(*sql.Rows) error) (returnedError error) {
	rows, err := pool.QueryContext(ctx, query, arguments...)
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return ErrRestoreInvalid
	}
	defer func() {
		readError, closeError := rows.Err(), rows.Close()
		canceled := ctx.Err()
		if closeError != nil || readError != nil && (canceled == nil || !errors.Is(readError, canceled)) {
			returnedError = ErrRestoreUncertain
		} else if canceled != nil {
			returnedError = canceled
		}
	}()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := consume(rows); err != nil {
			return err
		}
	}
	return nil
}
