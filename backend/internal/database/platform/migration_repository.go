package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"hello-world/internal/services/migration"
)

// PLATFORM DATABASE
// PostgresStore mengimplementasikan interface Store
type PostgresStore struct {
	platformDB *sql.DB
}

func NewPostgresStore(platformDB *sql.DB) *PostgresStore {
	return &PostgresStore{platformDB: platformDB}
}

// CreateMigration menyimpan draf migrasi baru ke tabel migrations di Platform Database
func (ps *PostgresStore) CreateMigration(ctx context.Context, m *migration.Migration) error {
	const query = `
		INSERT INTO migrations (
			database_id, version, title, description, sql_content, sql_down, irreversible_reason, status,
			created_by, updated_by, created_at, updated_at
		) VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, revision
	`
	return ps.platformDB.QueryRowContext(
		ctx, query,
		m.DatabaseID, m.Title, m.Description, m.SQLContent, m.SQLDown, m.IrreversibleReason, string(m.Status),
		m.CreatedBy, m.UpdatedBy, m.CreatedAt, m.UpdatedAt,
	).Scan(&m.ID, &m.Revision)
}

// GetMigrationByID membaca satu baris rancangan migrasi dari tabel migrations
func (ps *PostgresStore) GetMigrationByID(ctx context.Context, migrationID int64) (*migration.Migration, error) {
	const query = `
		SELECT id, database_id, version, title, COALESCE(description, ''), sql_content,
		       sql_down, irreversible_reason, revision, frozen_at,
		       status, created_by, updated_by, created_at, updated_at, applied_at
		FROM migrations
		WHERE id = $1
	`
	var m migration.Migration
	var statusStr string
	var version sql.NullInt64
	var appliedAt sql.NullTime

	err := ps.platformDB.QueryRowContext(ctx, query, migrationID).Scan(
		&m.ID, &m.DatabaseID, &version, &m.Title, &m.Description, &m.SQLContent,
		&m.SQLDown, &m.IrreversibleReason, &m.Revision, &m.FrozenAt,
		&statusStr, &m.CreatedBy, &m.UpdatedBy, &m.CreatedAt, &m.UpdatedAt, &appliedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migration.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	m.Status = migration.Status(statusStr)
	if version.Valid {
		v := version.Int64
		m.Version = &v
	}
	if appliedAt.Valid {
		t := appliedAt.Time
		m.AppliedAt = &t
	}
	return &m, nil
}

// ListMigrationsByDatabase mengambil seluruh daftar migrasi untuk suatu database_id secara berurutan
func (ps *PostgresStore) ListMigrationsByDatabase(ctx context.Context, databaseID string) ([]migration.Migration, error) {
	const query = `
		SELECT id, database_id, version, title, COALESCE(description, ''), sql_content,
		       sql_down, irreversible_reason, revision, frozen_at,
		       status, created_by, updated_by, created_at, updated_at, applied_at
		FROM migrations
		WHERE database_id = $1
		ORDER BY
			CASE WHEN version IS NOT NULL THEN 0 ELSE 1 END ASC,
			version ASC,
			id ASC
	`
	rows, err := ps.platformDB.QueryContext(ctx, query, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []migration.Migration
	for rows.Next() {
		var m migration.Migration
		var statusStr string
		var version sql.NullInt64
		var appliedAt sql.NullTime

		if err := rows.Scan(
			&m.ID, &m.DatabaseID, &version, &m.Title, &m.Description, &m.SQLContent,
			&m.SQLDown, &m.IrreversibleReason, &m.Revision, &m.FrozenAt,
			&statusStr, &m.CreatedBy, &m.UpdatedBy, &m.CreatedAt, &m.UpdatedAt, &appliedAt,
		); err != nil {
			return nil, err
		}
		m.Status = migration.Status(statusStr)
		if version.Valid {
			v := version.Int64
			m.Version = &v
		}
		if appliedAt.Valid {
			t := appliedAt.Time
			m.AppliedAt = &t
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func (ps *PostgresStore) UpdateDraftSQL(ctx context.Context, m *migration.Migration) error {
	const query = `
		UPDATE migrations
		SET title = $1,
		    description = $2,
		    sql_content = $3,
		    sql_down = $4,
		    irreversible_reason = $5,
		    updated_by = $6,
		    updated_at = $7,
		    revision = revision + 1
		WHERE id = $8 AND revision = $9 AND frozen_at IS NULL
		RETURNING revision
	`
	err := ps.platformDB.QueryRowContext(ctx, query, m.Title, m.Description, m.SQLContent,
		m.SQLDown, m.IrreversibleReason, m.UpdatedBy, m.UpdatedAt, m.ID, m.Revision).Scan(&m.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return migration.ErrRevisionChanged
	}
	return err
}

// CreateRun menyisipkan baris percobaan baru pada tabel migration_runs
func (ps *PostgresStore) CreateRun(ctx context.Context, run *migration.MigrationRun) error {
	tx, err := ps.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// The RUNNING row keeps the database reserved after this transaction commits.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migration.AdvisoryLockKey(run.DatabaseID)); err != nil {
		return err
	}
	var running bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM migration_runs WHERE database_id = $1 AND status = 'RUNNING')`,
		run.DatabaseID).Scan(&running); err != nil {
		return err
	}
	if running {
		return migration.ErrMigrationLocked
	}

	var databaseID, sqlUp, sqlDown string
	var status migration.Status
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT database_id, status, revision, sql_content, sql_down FROM migrations WHERE id = $1 FOR UPDATE`,
		run.MigrationID).Scan(&databaseID, &status, &revision, &sqlUp, &sqlDown); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return migration.ErrNotFound
		}
		return err
	}
	if databaseID != run.DatabaseID {
		return migration.ErrDatabaseMismatch
	}
	expectedSQL := sqlUp
	switch run.Operation {
	case migration.RunApply:
		if status == migration.StatusApplied {
			return migration.ErrAlreadyApplied
		}
	case migration.RunRollback, migration.RunForward:
		if status != migration.StatusApplied {
			return migration.ErrMigrationOrder
		}
		if run.Operation == migration.RunRollback {
			if sqlDown == "" {
				return migration.ErrIrreversible
			}
			expectedSQL = sqlDown
		}
	default:
		return fmt.Errorf("unsupported migration operation: %q", run.Operation)
	}
	if run.Revision != revision || run.SQLSnapshot != expectedSQL {
		return migration.ErrRevisionChanged
	}
	if err := validateRunOrder(ctx, tx, run); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_number), 0) + 1 FROM migration_runs WHERE migration_id = $1`,
		run.MigrationID).Scan(&run.AttemptNumber); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE migrations SET frozen_at = $1 WHERE id = $2 AND frozen_at IS NULL`, run.StartedAt, run.MigrationID); err != nil {
		return err
	}

	const query = `
		INSERT INTO migration_runs (
			migration_id, database_id, attempt_number, revision, operation, rollback_target_run_id, sql_snapshot,
			status, executed_by, execution_ms, started_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, $10)
		RETURNING id
	`
	if err := tx.QueryRowContext(
		ctx, query,
		run.MigrationID, run.DatabaseID, run.AttemptNumber, run.Revision, run.Operation, run.RollbackTargetRunID, run.SQLSnapshot,
		string(run.Status), run.ExecutedBy, run.StartedAt,
	).Scan(&run.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// The latest successful operation for each version determines its active state.
func validateRunOrder(ctx context.Context, tx *sql.Tx, run *migration.MigrationRun) error {
	const latestSuccess = `
		SELECT m.id, latest.id
		FROM migrations m
		JOIN LATERAL (
			SELECT id, operation FROM migration_runs
			WHERE migration_id = m.id AND status = 'SUCCEEDED'
			ORDER BY attempt_number DESC LIMIT 1
		) latest ON TRUE
		WHERE m.database_id = $1 AND m.version IS NOT NULL`
	query := latestSuccess + ` AND latest.operation = 'ROLLBACK' ORDER BY m.version ASC LIMIT 1`
	if run.Operation == migration.RunRollback {
		query = latestSuccess + ` AND latest.operation <> 'ROLLBACK' ORDER BY m.version DESC LIMIT 1`
	}
	var migrationID, executionID int64
	err := tx.QueryRowContext(ctx, query, run.DatabaseID).Scan(&migrationID, &executionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if run.Operation == migration.RunApply {
		if err == nil {
			return fmt.Errorf("%w: forward pending on this database", migration.ErrMigrationOrder)
		}
		return nil
	}
	if err != nil || run.MigrationID != migrationID {
		return migration.ErrMigrationOrder
	}
	if run.Operation == migration.RunRollback {
		run.RollbackTargetRunID = &executionID
	}
	return nil
}

// CompleteRunFailed mencatat hasil percobaan yang gagal (setelah Target DB di-rollback)
func (ps *PostgresStore) CompleteRunFailed(ctx context.Context, runID, migrationID int64, errMsg string, execMs int64, finishedAt time.Time) error {
	tx, err := ps.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const updateRun = `
		UPDATE migration_runs
		SET status = 'FAILED',
		    error_message = $1,
		    execution_ms = $2,
		    finished_at = $3
		WHERE id = $4 AND migration_id = $5 AND status = 'RUNNING'
		RETURNING id
	`
	if err := tx.QueryRowContext(ctx, updateRun, errMsg, execMs, finishedAt, runID, migrationID).Scan(&runID); err != nil {
		return fmt.Errorf("finish failed migration run: %w", err)
	}

	const updateMigration = `
		UPDATE migrations
		SET status = 'FAILED',
		    updated_at = $1
		WHERE id = $2 AND status <> 'APPLIED'
	`
	if _, err := tx.ExecContext(ctx, updateMigration, finishedAt, migrationID); err != nil {
		return err
	}

	return tx.Commit()
}

// CompleteRunSucceeded assigns a version on first apply and preserves it on rollback and forward.
func (ps *PostgresStore) CompleteRunSucceeded(ctx context.Context, runID, migrationID int64, databaseID string, execMs int64, finishedAt time.Time) (int64, error) {
	tx, err := ps.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Gunakan hash FNV-1a 64-bit dari string databaseID untuk pg_advisory_xact_lock
	lockKey := migration.AdvisoryLockKey(databaseID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return 0, err
	}

	const updateRun = `
		UPDATE migration_runs
		SET status = 'SUCCEEDED',
		    error_message = NULL,
		    execution_ms = $1,
		    finished_at = $2
		WHERE id = $3 AND migration_id = $4 AND database_id = $5 AND status = 'RUNNING'
		RETURNING operation
	`
	var operation migration.RunOperation
	if err := tx.QueryRowContext(ctx, updateRun, execMs, finishedAt, runID, migrationID, databaseID).Scan(&operation); err != nil {
		return 0, fmt.Errorf("finish successful migration run: %w", err)
	}

	query := `SELECT version FROM migrations WHERE id = $1`
	args := []any{migrationID}
	if operation == migration.RunApply {
		query = `
			UPDATE migrations
			SET version = (SELECT COALESCE(MAX(version), 0) + 1 FROM migrations WHERE database_id = $2),
			    status = 'APPLIED',
			    applied_at = $3,
			    updated_at = $3
			WHERE id = $1
			RETURNING version
		`
		args = append(args, databaseID, finishedAt)
	}
	var version int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&version); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

// ListRunsByMigration mengambil seluruh riwayat percobaan eksekusi untuk suatu migration_id
func (ps *PostgresStore) ListRunsByMigration(ctx context.Context, migrationID int64) ([]migration.MigrationRun, error) {
	const query = `
		SELECT id, migration_id, database_id, attempt_number, revision, operation, rollback_target_run_id, sql_snapshot,
		       status, executed_by, error_message, execution_ms, started_at, finished_at
		FROM migration_runs
		WHERE migration_id = $1
		ORDER BY attempt_number DESC, id DESC
	`
	rows, err := ps.platformDB.QueryContext(ctx, query, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []migration.MigrationRun
	for rows.Next() {
		var r migration.MigrationRun
		var statusStr string
		var errMsg sql.NullString
		var finishedAt sql.NullTime

		if err := rows.Scan(
			&r.ID, &r.MigrationID, &r.DatabaseID, &r.AttemptNumber, &r.Revision, &r.Operation, &r.RollbackTargetRunID, &r.SQLSnapshot,
			&statusStr, &r.ExecutedBy, &errMsg, &r.ExecutionMs, &r.StartedAt, &finishedAt,
		); err != nil {
			return nil, err
		}
		r.Status = migration.RunStatus(statusStr)
		if errMsg.Valid {
			msg := errMsg.String
			r.ErrorMessage = &msg
		}
		if finishedAt.Valid {
			t := finishedAt.Time
			r.FinishedAt = &t
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
