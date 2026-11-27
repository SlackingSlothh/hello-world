package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	servicemigration "hello-world/internal/services/migration"
)

var (
	validIdentPattern  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_$]*$`)
	placeholderPattern = regexp.MustCompile(`\$(\d+)`)
	updateTargetRe     = regexp.MustCompile(`(?i)\bUPDATE\s+(?:ONLY\s+)?("(?:[^"]|"")+"|[a-zA-Z_][a-zA-Z0-9_$]*)(?:\s*\.\s*("(?:[^"]|"")+"|[a-zA-Z_][a-zA-Z0-9_$]*))?`)
	deleteTargetRe     = regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+(?:ONLY\s+)?("(?:[^"]|"")+"|[a-zA-Z_][a-zA-Z0-9_$]*)(?:\s*\.\s*("(?:[^"]|"")+"|[a-zA-Z_][a-zA-Z0-9_$]*))?`)
	insertTargetRe     = regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+("(?:[^"]|"")+"|[a-zA-Z_][a-zA-Z0-9_$]*)(?:\s*\.\s*("(?:[^"]|"")+"|[a-zA-Z_][a-zA-Z0-9_$]*))?`)
)

func (c *SQLTargetConnector) resolvePostgresDB(ctx context.Context, databaseID string) (*sql.DB, error) {
	if c.resolveTarget == nil {
		return nil, errors.New("target database connector is not configured")
	}
	db, driverName, err := c.resolveTarget(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	dialect, err := targetDialect(driverName)
	if err != nil {
		return nil, err
	}
	if dialect != DialectPostgres {
		return nil, fmt.Errorf("batch checkpoint execution requires postgres target, got %q", dialect)
	}
	return db, nil
}

func (c *SQLTargetConnector) EnsureCheckpointSchema(ctx context.Context, databaseID string) error {
	db, err := c.resolvePostgresDB(ctx, databaseID)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS _platform_internal`); err != nil {
		return fmt.Errorf("create internal checkpoint schema: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _platform_internal.migration_checkpoints (
			execution_id UUID NOT NULL,
			batch_number INTEGER NOT NULL,
			direction TEXT NOT NULL CHECK (direction IN ('up', 'down')),
			cursor_start TEXT NOT NULL,
			cursor_end TEXT NOT NULL,
			rows_affected BIGINT NOT NULL DEFAULT 0,
			recovery_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			committed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (execution_id, batch_number, direction)
		)
	`)
	if err != nil {
		return fmt.Errorf("create internal checkpoint table: %w", err)
	}
	return nil
}

func (c *SQLTargetConnector) ResolveTargetScopeMax(ctx context.Context, databaseID, sqlQuery, cursorCol string) (string, error) {
	if err := validateIdentifier(cursorCol); err != nil {
		return "", err
	}
	schema, table, err := ExtractTargetTable(sqlQuery)
	if err != nil {
		return "", err
	}
	db, err := c.resolvePostgresDB(ctx, databaseID)
	if err != nil {
		return "", err
	}
	qualified := qualifyTable(schema, table)
	quotedCol := servicemigration.QuoteIdent(cursorCol)
	var maxVal sql.NullString
	query := fmt.Sprintf(`SELECT MAX(%s)::text FROM %s`, quotedCol, qualified)
	if err := db.QueryRowContext(ctx, query).Scan(&maxVal); err != nil {
		return "", fmt.Errorf("resolve target scope max: %w", err)
	}
	if !maxVal.Valid {
		return "", nil
	}
	return maxVal.String, nil
}

func (c *SQLTargetConnector) ExecuteBatchStep(ctx context.Context, databaseID string, req servicemigration.BatchStepRequest) (servicemigration.BatchStepResult, error) {
	if err := validateIdentifier(req.CursorColumn); err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	if req.BatchSize <= 0 {
		return servicemigration.BatchStepResult{}, fmt.Errorf("%w: batch_size must be positive", servicemigration.ErrInvalidSQL)
	}
	schema, table, err := ExtractTargetTable(req.SQLQuery)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	db, err := c.resolvePostgresDB(ctx, databaseID)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	existing, found, err := findCheckpointTx(ctx, tx, req.ExecutionID, req.BatchNumber, servicemigration.DirectionUp)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	if found {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return servicemigration.BatchStepResult{}, err
		}
		committed = true
		return servicemigration.BatchStepResult{
			Checkpoint:       existing,
			AlreadyCommitted: true,
		}, nil
	}

	if strings.TrimSpace(req.TargetScopeMax) == "" {
		_ = tx.Rollback()
		committed = true
		return servicemigration.BatchStepResult{Done: true}, nil
	}

	qualified := qualifyTable(schema, table)
	quotedCol := servicemigration.QuoteIdent(req.CursorColumn)
	var sliceCount int64
	var minCursor, maxCursor sql.NullString
	if req.LastCursor == "" {
		sliceQuery := fmt.Sprintf(`
			WITH slice AS (
				SELECT %s AS c FROM %s
				WHERE %s <= $1
				ORDER BY %s ASC
				LIMIT $2
			)
			SELECT COUNT(*), MIN(c)::text, MAX(c)::text FROM slice
		`, quotedCol, qualified, quotedCol, quotedCol)
		if err := tx.QueryRowContext(ctx, sliceQuery, CursorArg(req.TargetScopeMax), req.BatchSize).Scan(&sliceCount, &minCursor, &maxCursor); err != nil {
			return servicemigration.BatchStepResult{}, fmt.Errorf("select first batch slice: %w", err)
		}
	} else {
		sliceQuery := fmt.Sprintf(`
			WITH slice AS (
				SELECT %s AS c FROM %s
				WHERE %s > $1 AND %s <= $2
				ORDER BY %s ASC
				LIMIT $3
			)
			SELECT COUNT(*), MIN(c)::text, MAX(c)::text FROM slice
		`, quotedCol, qualified, quotedCol, quotedCol, quotedCol)
		if err := tx.QueryRowContext(ctx, sliceQuery, CursorArg(req.LastCursor), CursorArg(req.TargetScopeMax), req.BatchSize).Scan(&sliceCount, &minCursor, &maxCursor); err != nil {
			return servicemigration.BatchStepResult{}, fmt.Errorf("select next batch slice: %w", err)
		}
	}

	if sliceCount == 0 || !minCursor.Valid || !maxCursor.Valid {
		_ = tx.Rollback()
		committed = true
		return servicemigration.BatchStepResult{Done: true}, nil
	}

	args := buildBatchQueryArgs(req.SQLQuery, req.LastCursor, minCursor.String, maxCursor.String, req.TargetScopeMax, req.BatchSize)
	res, err := tx.ExecContext(ctx, req.SQLQuery, args...)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	rowsAffected, _ := res.RowsAffected()

	metaPayload, _ := json.Marshal(map[string]any{
		"schema":        schema,
		"table":         table,
		"cursor_column": req.CursorColumn,
		"cursor_start":  minCursor.String,
		"cursor_end":    maxCursor.String,
		"batch_size":    req.BatchSize,
	})
	now := time.Now().UTC()
	cp := servicemigration.BatchCheckpoint{
		ExecutionID:      req.ExecutionID,
		BatchNumber:      req.BatchNumber,
		Direction:        servicemigration.DirectionUp,
		CursorStart:      minCursor.String,
		CursorEnd:        maxCursor.String,
		RowsAffected:     rowsAffected,
		RecoveryMetadata: metaPayload,
		CommittedAt:      now,
	}
	if err := insertCheckpointTx(ctx, tx, cp); err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	committed = true
	return servicemigration.BatchStepResult{Checkpoint: cp}, nil
}

func (c *SQLTargetConnector) RevertBatchStep(ctx context.Context, databaseID string, req servicemigration.RevertStepRequest) (servicemigration.BatchStepResult, error) {
	db, err := c.resolvePostgresDB(ctx, databaseID)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	existing, found, err := findCheckpointTx(ctx, tx, req.ExecutionID, req.Checkpoint.BatchNumber, servicemigration.DirectionDown)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	if found {
		_ = tx.Rollback()
		committed = true
		return servicemigration.BatchStepResult{
			Checkpoint:       existing,
			AlreadyCommitted: true,
		}, nil
	}

	args := buildRevertQueryArgs(req.SQLDown, req.Checkpoint.CursorStart, req.Checkpoint.CursorEnd, req.BatchSize)
	res, err := tx.ExecContext(ctx, req.SQLDown, args...)
	if err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	rowsAffected, _ := res.RowsAffected()

	meta := req.Checkpoint.RecoveryMetadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	cp := servicemigration.BatchCheckpoint{
		ExecutionID:      req.ExecutionID,
		BatchNumber:      req.Checkpoint.BatchNumber,
		Direction:        servicemigration.DirectionDown,
		CursorStart:      req.Checkpoint.CursorStart,
		CursorEnd:        req.Checkpoint.CursorEnd,
		RowsAffected:     rowsAffected,
		RecoveryMetadata: meta,
		CommittedAt:      time.Now().UTC(),
	}
	if err := insertCheckpointTx(ctx, tx, cp); err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return servicemigration.BatchStepResult{}, err
	}
	committed = true
	return servicemigration.BatchStepResult{Checkpoint: cp}, nil
}

func (c *SQLTargetConnector) ListCheckpoints(ctx context.Context, databaseID, executionID string, direction servicemigration.CheckpointDirection) ([]servicemigration.BatchCheckpoint, error) {
	db, err := c.resolvePostgresDB(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	order := "ASC"
	if direction == servicemigration.DirectionUp {
		order = "ASC"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT execution_id, batch_number, direction, cursor_start, cursor_end, rows_affected, recovery_metadata, committed_at
		FROM _platform_internal.migration_checkpoints
		WHERE execution_id = $1 AND direction = $2
		ORDER BY batch_number `+order, executionID, string(direction))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "3F000" || pgErr.Code == "42P01") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := make([]servicemigration.BatchCheckpoint, 0)
	for rows.Next() {
		var cp servicemigration.BatchCheckpoint
		var dir string
		if err := rows.Scan(
			&cp.ExecutionID, &cp.BatchNumber, &dir, &cp.CursorStart, &cp.CursorEnd,
			&cp.RowsAffected, &cp.RecoveryMetadata, &cp.CommittedAt,
		); err != nil {
			return nil, err
		}
		cp.Direction = servicemigration.CheckpointDirection(dir)
		out = append(out, cp)
	}
	return out, rows.Err()
}

func (c *SQLTargetConnector) ExecNormalTx(ctx context.Context, databaseID, sqlQuery string) (int64, error) {
	tx, err := c.BeginTargetTx(ctx, databaseID)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := servicemigration.ValidateTransactionalSQLForDialect(sqlQuery, tx.Dialect()); err != nil {
		return 0, err
	}
	if err := tx.AcquireTargetLock(ctx, databaseID); err != nil {
		return 0, err
	}
	var rowsAffected int64
	if sqlTx, ok := tx.(*sqlTargetTx); ok {
		rowsAffected, err = sqlTx.execSQLWithRows(ctx, sqlQuery)
	} else {
		err = tx.ExecSQL(ctx, sqlQuery)
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	committed = true
	return rowsAffected, nil
}

func (t *sqlTargetTx) execSQLWithRows(ctx context.Context, sqlQuery string) (int64, error) {
	if t.dialect == DialectPostgres {
		res, err := t.tx.ExecContext(ctx, sqlQuery)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return n, nil
	}
	var total int64
	for _, statement := range servicemigration.SplitSQLStatementsForDialect(sqlQuery, t.dialect) {
		res, err := t.tx.ExecContext(ctx, statement)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func findCheckpointTx(ctx context.Context, tx *sql.Tx, executionID string, batchNumber int, direction servicemigration.CheckpointDirection) (servicemigration.BatchCheckpoint, bool, error) {
	var cp servicemigration.BatchCheckpoint
	var dir string
	err := tx.QueryRowContext(ctx, `
		SELECT execution_id, batch_number, direction, cursor_start, cursor_end, rows_affected, recovery_metadata, committed_at
		FROM _platform_internal.migration_checkpoints
		WHERE execution_id = $1 AND batch_number = $2 AND direction = $3
	`, executionID, batchNumber, string(direction)).Scan(
		&cp.ExecutionID, &cp.BatchNumber, &dir, &cp.CursorStart, &cp.CursorEnd,
		&cp.RowsAffected, &cp.RecoveryMetadata, &cp.CommittedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return servicemigration.BatchCheckpoint{}, false, nil
	}
	if err != nil {
		return servicemigration.BatchCheckpoint{}, false, err
	}
	cp.Direction = servicemigration.CheckpointDirection(dir)
	return cp, true, nil
}

func insertCheckpointTx(ctx context.Context, tx *sql.Tx, cp servicemigration.BatchCheckpoint) error {
	meta := cp.RecoveryMetadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO _platform_internal.migration_checkpoints (
			execution_id, batch_number, direction, cursor_start, cursor_end, rows_affected, recovery_metadata, committed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, cp.ExecutionID, cp.BatchNumber, string(cp.Direction), cp.CursorStart, cp.CursorEnd, cp.RowsAffected, meta, cp.CommittedAt)
	return err
}

func buildBatchQueryArgs(sqlQuery, lastCursor, cursorStart, cursorEnd, targetScopeMax string, batchSize int) []any {
	n := CountSQLPlaceholders(sqlQuery)
	switch {
	case n >= 3:
		prev := CursorBefore(cursorStart)
		if lastCursor != "" {
			prev = CursorArg(lastCursor)
		}
		return []any{prev, CursorArg(targetScopeMax), batchSize}
	case n == 2:
		return []any{CursorArg(cursorStart), CursorArg(cursorEnd)}
	case n == 1:
		return []any{CursorArg(cursorEnd)}
	default:
		return nil
	}
}

func buildRevertQueryArgs(sqlDown, cursorStart, cursorEnd string, batchSize int) []any {
	n := CountSQLPlaceholders(sqlDown)
	switch {
	case n >= 3:
		return []any{CursorBefore(cursorStart), CursorArg(cursorEnd), batchSize}
	case n == 2:
		return []any{CursorArg(cursorStart), CursorArg(cursorEnd)}
	case n == 1:
		return []any{CursorArg(cursorEnd)}
	default:
		return nil
	}
}

func CountSQLPlaceholders(sqlQuery string) int {
	matches := placeholderPattern.FindAllStringSubmatch(stripSingleQuoted(sqlQuery), -1)
	maxIdx := 0
	for _, m := range matches {
		if v, err := strconv.Atoi(m[1]); err == nil && v > maxIdx {
			maxIdx = v
		}
	}
	return maxIdx
}

func ExtractTargetTable(sqlQuery string) (string, string, error) {
	cleaned := stripSingleQuoted(sqlQuery)
	for _, re := range []*regexp.Regexp{updateTargetRe, deleteTargetRe, insertTargetRe} {
		if m := re.FindStringSubmatch(cleaned); len(m) >= 3 {
			first := unquoteIdent(m[1])
			second := unquoteIdent(m[2])
			if second != "" {
				return first, second, nil
			}
			return "", first, nil
		}
	}
	return "", "", fmt.Errorf("%w: could not determine target table from batch SQL", servicemigration.ErrInvalidSQL)
}

func CursorArg(raw string) any {
	if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		return v
	}
	return raw
}

func CursorBefore(raw string) any {
	if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		return v - 1
	}
	return ""
}

func ExtractSQLState(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	var stateErr interface{ SQLState() string }
	if errors.As(err, &stateErr) {
		return stateErr.SQLState()
	}
	return ""
}

func validateIdentifier(name string) error {
	trimmed := strings.TrimSpace(name)
	if !validIdentPattern.MatchString(trimmed) {
		return fmt.Errorf("%w: invalid batch cursor column %q", servicemigration.ErrInvalidSQL, name)
	}
	return nil
}

func qualifyTable(schema, table string) string {
	if schema != "" {
		return servicemigration.QuoteIdent(schema) + "." + servicemigration.QuoteIdent(table)
	}
	return servicemigration.QuoteIdent(table)
}

func unquoteIdent(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return strings.ReplaceAll(raw[1:len(raw)-1], `""`, `"`)
	}
	return raw
}

func stripSingleQuoted(sqlText string) string {
	var b strings.Builder
	b.Grow(len(sqlText))
	inSingle := false
	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}
