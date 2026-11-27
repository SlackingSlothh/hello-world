package migration

import (
	"context"
	"database/sql"
	"errors"

	servicemigration "hello-world/internal/services/migration"
)

// Crash recovery can reuse an XID that never reached WAL. The statistics reset
// timestamp also changes after a crash that reinitializes a live postmaster.
const postgresTargetIdentity = `
	CASE
		WHEN NOT pg_catalog.pg_is_in_recovery()
		THEN (pg_catalog.pg_control_system()).system_identifier::text
			|| ':' || (SELECT oid::text FROM pg_catalog.pg_database WHERE datname = pg_catalog.current_database())
			|| ':' || pg_catalog.left(pg_catalog.pg_walfile_name(pg_catalog.pg_current_wal_insert_lsn()), 8)
			|| ':' || EXTRACT(EPOCH FROM pg_catalog.pg_postmaster_start_time())::text
			|| ':' || (SELECT COALESCE(EXTRACT(EPOCH FROM stats_reset)::text, 'none') FROM pg_catalog.pg_stat_bgwriter)
	END`

func (t *sqlTargetTx) RecordTransactionEvidence(ctx context.Context) (servicemigration.TransactionEvidence, error) {
	var evidence servicemigration.TransactionEvidence
	if t.dialect != DialectPostgres {
		return evidence, nil
	}
	var canRecord bool
	err := t.tx.QueryRowContext(ctx, `
		SELECT pg_catalog.current_setting('server_version_num')::int >= 130000
			AND NOT pg_catalog.pg_is_in_recovery()
			AND pg_catalog.has_function_privilege('pg_catalog.pg_control_system()', 'EXECUTE')`,
	).Scan(&canRecord)
	if err != nil || !canRecord {
		return evidence, err
	}
	err = t.tx.QueryRowContext(ctx, `
		WITH target AS MATERIALIZED (SELECT `+postgresTargetIdentity+` AS identity)
		SELECT pg_catalog.pg_current_xact_id()::text, identity FROM target WHERE identity IS NOT NULL`,
	).Scan(&evidence.XactID, &evidence.TargetIdentity)
	if errors.Is(err, sql.ErrNoRows) {
		return evidence, nil
	}
	return evidence, err
}

func (c *SQLTargetConnector) VerifyTransaction(ctx context.Context, databaseID string, evidence servicemigration.TransactionEvidence) (servicemigration.TransactionOutcome, error) {
	if c.resolveTarget == nil {
		return "", errors.New("provider koneksi database target belum dikonfigurasi")
	}
	targetDB, driver, err := c.resolveTarget(ctx, databaseID)
	if err != nil {
		return "", err
	}
	dialect, err := targetDialect(driver)
	if err != nil || dialect != DialectPostgres {
		return "", err
	}
	var status sql.NullString
	err = targetDB.QueryRowContext(ctx, `
		SELECT CASE WHEN (`+postgresTargetIdentity+`) = $1
		            THEN pg_catalog.pg_xact_status($2::pg_catalog.xid8) END`,
		evidence.TargetIdentity, evidence.XactID).Scan(&status)
	return servicemigration.TransactionOutcome(status.String), err
}
