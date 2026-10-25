package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
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
func (ps *PostgresStore) CreateMigration(ctx context.Context, m *Migration) error {
	const query = `
		INSERT INTO migrations (
			database_id, version, title, description, sql_content, status,
			created_by, updated_by, created_at, updated_at
		) VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`
	return ps.platformDB.QueryRowContext(
		ctx, query,
		m.DatabaseID, m.Title, m.Description, m.SQLContent, string(m.Status),
		m.CreatedBy, m.UpdatedBy, m.CreatedAt, m.UpdatedAt,
	).Scan(&m.ID)
}

// GetMigrationByID membaca satu baris rancangan migrasi dari tabel migrations
func (ps *PostgresStore) GetMigrationByID(ctx context.Context, migrationID int64) (*Migration, error) {
	const query = `
		SELECT id, database_id, version, title, COALESCE(description, ''), sql_content,
		       status, created_by, updated_by, created_at, updated_at, applied_at
		FROM migrations
		WHERE id = $1
	`
	var m Migration
	var statusStr string
	var version sql.NullInt64
	var appliedAt sql.NullTime

	err := ps.platformDB.QueryRowContext(ctx, query, migrationID).Scan(
		&m.ID, &m.DatabaseID, &version, &m.Title, &m.Description, &m.SQLContent,
		&statusStr, &m.CreatedBy, &m.UpdatedBy, &m.CreatedAt, &m.UpdatedAt, &appliedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	m.Status = Status(statusStr)
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
func (ps *PostgresStore) ListMigrationsByDatabase(ctx context.Context, databaseID string) ([]Migration, error) {
	const query = `
		SELECT id, database_id, version, title, COALESCE(description, ''), sql_content,
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

	var result []Migration
	for rows.Next() {
		var m Migration
		var statusStr string
		var version sql.NullInt64
		var appliedAt sql.NullTime

		if err := rows.Scan(
			&m.ID, &m.DatabaseID, &version, &m.Title, &m.Description, &m.SQLContent,
			&statusStr, &m.CreatedBy, &m.UpdatedBy, &m.CreatedAt, &m.UpdatedAt, &appliedAt,
		); err != nil {
			return nil, err
		}
		m.Status = Status(statusStr)
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

// UpdateDraftSQL memperbarui isi SQL pada migrasi yang belum berstatus APPLIED (memang masih berupa draft atau gagal dieksekusi)
func (ps *PostgresStore) UpdateDraftSQL(ctx context.Context, migrationID int64, title, description, sqlContent, updatedBy string, updatedAt time.Time) error {
	const query = `
		UPDATE migrations
		SET title = $1,
		    description = $2,
		    sql_content = $3,
		    updated_by = $4,
		    updated_at = $5
		WHERE id = $6 AND status <> 'APPLIED'
	`
	res, err := ps.platformDB.ExecContext(ctx, query, title, description, sqlContent, updatedBy, updatedAt, migrationID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrAlreadyApplied
	}
	return nil
}

// CreateRun menyisipkan baris percobaan baru pada tabel migration_runs
func (ps *PostgresStore) CreateRun(ctx context.Context, run *MigrationRun) error {
	const query = `
		INSERT INTO migration_runs (
			migration_id, database_id, attempt_number, sql_snapshot,
			status, executed_by, execution_ms, started_at
		) VALUES ($1, $2, $3, $4, $5, $6, 0, $7)
		RETURNING id
	`
	return ps.platformDB.QueryRowContext(
		ctx, query,
		run.MigrationID, run.DatabaseID, run.AttemptNumber, run.SQLSnapshot,
		string(run.Status), run.ExecutedBy, run.StartedAt,
	).Scan(&run.ID)
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
		WHERE id = $4
	`
	if _, err := tx.ExecContext(ctx, updateRun, errMsg, execMs, finishedAt, runID); err != nil {
		return err
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

// CompleteRunSucceeded menetapkan nomor versi berikutnya 
func (ps *PostgresStore) CompleteRunSucceeded(ctx context.Context, runID, migrationID int64, databaseID string, execMs int64, finishedAt time.Time) (int64, error) {
	tx, err := ps.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Gunakan hash FNV-1a 64-bit dari string databaseID untuk pg_advisory_xact_lock
	lockKey := AdvisoryLockKey(databaseID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return 0, err
	}

	var nextVersion int64
	const nextVerQuery = `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM migrations
		WHERE database_id = $1 AND status = 'APPLIED'
	`
	if err := tx.QueryRowContext(ctx, nextVerQuery, databaseID).Scan(&nextVersion); err != nil {
		return 0, err
	}

	const updateMigration = `
		UPDATE migrations
		SET version = $1,
		    status = 'APPLIED',
		    applied_at = $2,
		    updated_at = $2
		WHERE id = $3
	`
	if _, err := tx.ExecContext(ctx, updateMigration, nextVersion, finishedAt, migrationID); err != nil {
		return 0, err
	}

	const updateRun = `
		UPDATE migration_runs
		SET status = 'SUCCEEDED',
		    error_message = NULL,
		    execution_ms = $1,
		    finished_at = $2
		WHERE id = $3
	`
	if _, err := tx.ExecContext(ctx, updateRun, execMs, finishedAt, runID); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return nextVersion, nil
}

// ListRunsByMigration mengambil seluruh riwayat percobaan eksekusi untuk suatu migration_id
func (ps *PostgresStore) ListRunsByMigration(ctx context.Context, migrationID int64) ([]MigrationRun, error) {
	const query = `
		SELECT id, migration_id, database_id, attempt_number, sql_snapshot,
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

	var runs []MigrationRun
	for rows.Next() {
		var r MigrationRun
		var statusStr string
		var errMsg sql.NullString
		var finishedAt sql.NullTime

		if err := rows.Scan(
			&r.ID, &r.MigrationID, &r.DatabaseID, &r.AttemptNumber, &r.SQLSnapshot,
			&statusStr, &r.ExecutedBy, &errMsg, &r.ExecutionMs, &r.StartedAt, &finishedAt,
		); err != nil {
			return nil, err
		}
		r.Status = RunStatus(statusStr)
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

// TARGET DATABASE
type TargetDBProvider func(ctx context.Context, databaseID string) (*sql.DB, error)

type SQLTargetConnector struct {
	getTargetDB TargetDBProvider
}

func NewSQLTargetConnector(provider TargetDBProvider) *SQLTargetConnector {
	return &SQLTargetConnector{getTargetDB: provider}
}

func (c *SQLTargetConnector) BeginTargetTx(ctx context.Context, databaseID string) (TargetTx, error) {
	if c.getTargetDB == nil {
		return nil, errors.New("provider koneksi database target belum dikonfigurasi")
	}
	targetDB, err := c.getTargetDB(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	tx, err := targetDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlTargetTx{tx: tx}, nil
}

type sqlTargetTx struct {
	tx *sql.Tx
}

func (t *sqlTargetTx) AcquireTargetLock(ctx context.Context, databaseID string) error {
	lockKey := AdvisoryLockKey(databaseID)
	_, err := t.tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey)
	return err
}

func (t *sqlTargetTx) ExecSQL(ctx context.Context, sqlQuery string) error {
	_, err := t.tx.ExecContext(ctx, sqlQuery)
	return err
}

func (t *sqlTargetTx) ExplainSQL(ctx context.Context, sqlQuery string, analyze bool) (string, error) {
	prefix := "EXPLAIN (FORMAT JSON) "
	if analyze {
		prefix = "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "
	}
	var jsonPlan string
	if err := t.tx.QueryRowContext(ctx, prefix+sqlQuery).Scan(&jsonPlan); err != nil {
		return "", err
	}
	return jsonPlan, nil
}

func (t *sqlTargetTx) InspectSchema(ctx context.Context, schemaName string) ([]TableSchema, error) {
	const query = `
		SELECT
			c.table_name,
			c.column_name,
			c.data_type,
			(c.is_nullable = 'YES') AS is_nullable,
			c.column_default
		FROM information_schema.columns c
		JOIN information_schema.tables tbl
		  ON tbl.table_schema = c.table_schema
		 AND tbl.table_name = c.table_name
		WHERE c.table_schema = $1
		  AND tbl.table_type = 'BASE TABLE'
		ORDER BY c.table_name ASC, c.ordinal_position ASC
	`
	rows, err := t.tx.QueryContext(ctx, query, schemaName)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca information_schema: %w", err)
	}
	defer rows.Close()

	tableIndex := make(map[string]int)
	var tables []TableSchema

	for rows.Next() {
		var tName, colName, dataType string
		var isNullable bool
		var colDefault sql.NullString

		if err := rows.Scan(&tName, &colName, &dataType, &isNullable, &colDefault); err != nil {
			return nil, err
		}

		idx, exists := tableIndex[tName]
		if !exists {
			idx = len(tables)
			tableIndex[tName] = idx
			tables = append(tables, TableSchema{TableName: tName})
		}

		col := ColumnSchema{
			Name:       colName,
			DataType:   dataType,
			IsNullable: isNullable,
		}
		if colDefault.Valid {
			def := colDefault.String
			col.DefaultVal = &def
		}
		tables[idx].Columns = append(tables[idx].Columns, col)
	}
	return tables, rows.Err()
}

func (t *sqlTargetTx) Commit(ctx context.Context) error {
	return t.tx.Commit()
}

func (t *sqlTargetTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback()
}