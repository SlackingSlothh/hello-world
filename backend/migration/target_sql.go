package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type TargetDialect string

const (
	DialectPostgres TargetDialect = "postgres"
	DialectMySQL    TargetDialect = "mysql"
	DialectMariaDB  TargetDialect = "mariadb"
)

type TargetDBProvider func(ctx context.Context, databaseID string) (*sql.DB, error)
type TargetDBResolver func(ctx context.Context, databaseID string) (*sql.DB, string, error)

type SQLTargetConnector struct {
	resolveTarget TargetDBResolver
}

func NewSQLTargetConnector(provider TargetDBProvider) *SQLTargetConnector {
	if provider == nil {
		return &SQLTargetConnector{}
	}
	return NewSQLTargetConnectorWithResolver(func(ctx context.Context, databaseID string) (*sql.DB, string, error) {
		db, err := provider(ctx, databaseID)
		return db, string(DialectPostgres), err
	})
}

func NewSQLTargetConnectorWithResolver(resolver TargetDBResolver) *SQLTargetConnector {
	return &SQLTargetConnector{resolveTarget: resolver}
}

func (c *SQLTargetConnector) BeginTargetTx(ctx context.Context, databaseID string) (TargetTx, error) {
	if c.resolveTarget == nil {
		return nil, errors.New("provider koneksi database target belum dikonfigurasi")
	}
	targetDB, driver, err := c.resolveTarget(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	dialect, err := targetDialect(driver)
	if err != nil {
		return nil, err
	}
	conn, err := targetDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &sqlTargetTx{conn: conn, tx: tx, dialect: dialect}, nil
}

type sqlTargetTx struct {
	conn      *sql.Conn
	tx        *sql.Tx
	dialect   TargetDialect
	lockName  string
	lockHeld  bool
	closed    bool
	finishErr error
	mu        sync.Mutex
}

func (t *sqlTargetTx) Dialect() TargetDialect { return t.dialect }

func (t *sqlTargetTx) AcquireTargetLock(ctx context.Context, databaseID string) error {
	lockKey := AdvisoryLockKey(databaseID)
	if t.dialect == DialectPostgres {
		_, err := t.tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey)
		return err
	}
	t.lockName = fmt.Sprintf("hello-world-migration-%016x", uint64(lockKey))
	var acquired sql.NullInt64
	if err := t.conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 10)`, t.lockName).Scan(&acquired); err != nil {
		return err
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return ErrMigrationLocked
	}
	t.lockHeld = true
	return nil
}

func (t *sqlTargetTx) ExecSQL(ctx context.Context, sqlQuery string) error {
	if t.dialect == DialectPostgres {
		_, err := t.tx.ExecContext(ctx, sqlQuery)
		return err
	}
	for _, statement := range splitSQLStatementsForDialect(sqlQuery, t.dialect) {
		if _, err := t.tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (t *sqlTargetTx) ExplainSQL(ctx context.Context, sqlQuery string, analyze bool) (string, error) {
	prefix := "EXPLAIN (FORMAT JSON) "
	if analyze {
		prefix = "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "
	}
	if t.dialect == DialectMySQL {
		prefix = "EXPLAIN FORMAT=JSON "
		if analyze {
			prefix = "EXPLAIN ANALYZE FORMAT=TREE "
		}
	} else if t.dialect == DialectMariaDB {
		prefix = "EXPLAIN FORMAT=JSON "
		if analyze {
			prefix = "ANALYZE FORMAT=JSON "
		}
	}
	var rawPlan []byte
	if err := t.tx.QueryRowContext(ctx, prefix+sqlQuery).Scan(&rawPlan); err != nil {
		return "", err
	}
	return string(rawPlan), nil
}

func (t *sqlTargetTx) InspectSchema(ctx context.Context, schemaName string) ([]TableSchema, error) {
	if schemaName == "" && t.dialect == DialectPostgres {
		schemaName = "public"
	}
	if schemaName == "" && (t.dialect == DialectMySQL || t.dialect == DialectMariaDB) {
		if err := t.tx.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&schemaName); err != nil {
			return nil, err
		}
	}
	placeholder := "$1"
	dataTypeExpr := "c.data_type"
	primaryExpr := `EXISTS (
			SELECT 1 FROM information_schema.key_column_usage pk
			WHERE pk.table_schema = c.table_schema AND pk.table_name = c.table_name
			  AND pk.column_name = c.column_name AND pk.constraint_name = 'PRIMARY'
		)`
	if t.dialect == DialectMySQL || t.dialect == DialectMariaDB {
		placeholder = "?"
		dataTypeExpr = "c.column_type"
	}
	query := `
		SELECT
			c.table_name,
			c.column_name,
			` + dataTypeExpr + `,
			(c.is_nullable = 'YES') AS is_nullable,
			c.column_default,
			` + primaryExpr + `
		FROM information_schema.columns c
		JOIN information_schema.tables tbl
		  ON tbl.table_schema = c.table_schema
		 AND tbl.table_name = c.table_name
		WHERE c.table_schema = ` + placeholder + `
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
		var isPrimary bool

		if err := rows.Scan(&tName, &colName, &dataType, &isNullable, &colDefault, &isPrimary); err != nil {
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
			IsPrimary:  isPrimary,
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
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return sql.ErrTxDone
	}
	t.closed = true
	err := t.tx.Commit()
	return errors.Join(err, t.release())
}

func (t *sqlTargetTx) Rollback(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	err := t.tx.Rollback()
	return errors.Join(err, t.release())
}

func (t *sqlTargetTx) release() error {
	if t.lockHeld {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var released sql.NullInt64
		if err := t.conn.QueryRowContext(releaseCtx, `SELECT RELEASE_LOCK(?)`, t.lockName).Scan(&released); err != nil {
			discardErr := t.conn.Raw(func(any) error { return driver.ErrBadConn })
			if !errors.Is(discardErr, driver.ErrBadConn) {
				t.finishErr = errors.Join(err, fmt.Errorf("discard connection holding target lock: %w", discardErr))
			}
		} else if !released.Valid || released.Int64 != 1 {
			discardErr := t.conn.Raw(func(any) error { return driver.ErrBadConn })
			if !errors.Is(discardErr, driver.ErrBadConn) {
				t.finishErr = errors.Join(errors.New("failed to release target migration lock"), fmt.Errorf("discard connection holding target lock: %w", discardErr))
			}
		}
		t.lockHeld = false
	}
	if err := t.conn.Close(); err != nil {
		t.finishErr = errors.Join(t.finishErr, err)
	}
	return t.finishErr
}

func targetDialect(driver string) (TargetDialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql", "pgx":
		return DialectPostgres, nil
	case "mysql":
		return DialectMySQL, nil
	case "mariadb", "maria":
		return DialectMariaDB, nil
	default:
		return "", fmt.Errorf("unsupported migration target driver %q", driver)
	}
}
