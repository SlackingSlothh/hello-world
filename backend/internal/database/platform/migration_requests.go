package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"hello-world/internal/services/migration"
)

func (ps *PostgresStore) CreateMigrationRequest(ctx context.Context, request migration.MigrationRequest) (migration.MigrationRequest, error) {
	tx, err := ps.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	defer tx.Rollback()

	var currentRevision int64
	var status migration.Status
	var sqlUp, sqlDown string
	err = tx.QueryRowContext(ctx, `SELECT revision, status, sql_content, sql_down
		FROM migrations WHERE id = $1 AND database_id = $2 FOR SHARE`, request.MigrationID, request.DatabaseID).
		Scan(&currentRevision, &status, &sqlUp, &sqlDown)
	if errors.Is(err, sql.ErrNoRows) {
		return migration.MigrationRequest{}, migration.ErrNotFound
	}
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	if currentRevision != request.Revision {
		return migration.MigrationRequest{}, migration.ErrRevisionChanged
	}
	if request.Operation == migration.RunApply && status == migration.StatusApplied {
		return migration.MigrationRequest{}, migration.ErrAlreadyApplied
	}
	request.SQLSnapshot = sqlUp
	if request.Operation == migration.RunRollback {
		if sqlDown == "" {
			return migration.MigrationRequest{}, migration.ErrIrreversible
		}
		request.SQLSnapshot = sqlDown
	}
	request.TargetRunID, err = migrationOrderTarget(ctx, tx, request.DatabaseID, request.MigrationID, request.Operation)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO migration_requests
		(migration_id, database_id, operation, revision, target_run_id, sql_snapshot, requested_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		request.MigrationID, request.DatabaseID, request.Operation, request.Revision,
		request.TargetRunID, request.SQLSnapshot, request.RequestedBy, request.CreatedAt).Scan(&request.ID)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	return commitMigrationRequest(ctx, tx, request.DatabaseID, request.ID)
}

func (ps *PostgresStore) GetMigrationRequest(ctx context.Context, databaseID string, requestID int64) (migration.MigrationRequest, error) {
	request, err := readMigrationRequest(ctx, ps.platformDB, databaseID, requestID, false)
	if err != nil || request.Status == migration.RequestStale {
		return request, err
	}
	err = validateMigrationRequestTarget(ctx, ps.platformDB, request)
	if errors.Is(err, migration.ErrMigrationOrder) {
		request.Status = migration.RequestStale
		return request, nil
	}
	return request, err
}

func (ps *PostgresStore) ApproveMigrationRequest(ctx context.Context, databaseID string, requestID int64, groupID, reviewerID string, approvedAt time.Time) (migration.MigrationRequest, error) {
	tx, err := ps.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	defer tx.Rollback()

	request, err := readMigrationRequest(ctx, tx, databaseID, requestID, true)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	if request.Status == migration.RequestStale {
		return migration.MigrationRequest{}, migration.ErrRevisionChanged
	}
	if err := validateMigrationRequestTarget(ctx, tx, request); err != nil {
		return migration.MigrationRequest{}, err
	}
	const approve = `INSERT INTO migration_request_approvals AS approval (request_id, group_id, reviewer_id, approved_at)
		SELECT $1, g.id, u.id, $5
		FROM access_groups g
		JOIN connection_grants cg ON cg.group_id = g.id AND cg.database_id = $2 AND cg.access_level IN (1, 2)
		JOIN platform_users u ON u.id = g.leader_user_id AND u.active
		WHERE g.id = $3 AND u.id = $4
		ON CONFLICT (request_id, group_id) DO UPDATE SET
			reviewer_id = EXCLUDED.reviewer_id,
			approved_at = CASE WHEN approval.reviewer_id = EXCLUDED.reviewer_id
				THEN approval.approved_at ELSE EXCLUDED.approved_at END`
	result, err := tx.ExecContext(ctx, approve, requestID, databaseID, groupID, reviewerID, approvedAt)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	if count == 0 {
		return migration.MigrationRequest{}, migration.ErrForbidden
	}
	return commitMigrationRequest(ctx, tx, databaseID, requestID)
}

func commitMigrationRequest(ctx context.Context, tx *sql.Tx, databaseID string, requestID int64) (migration.MigrationRequest, error) {
	request, err := readMigrationRequest(ctx, tx, databaseID, requestID, false)
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return migration.MigrationRequest{}, err
	}
	return request, nil
}

type migrationRowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateMigrationRequestTarget(ctx context.Context, db migrationRowReader, request migration.MigrationRequest) error {
	targetRunID, err := migrationOrderTarget(ctx, db, request.DatabaseID, request.MigrationID, request.Operation)
	if err != nil {
		return err
	}
	if targetRunID != nil && (request.TargetRunID == nil || *request.TargetRunID != *targetRunID) {
		return migration.ErrMigrationOrder
	}
	return nil
}

func readMigrationRequest(ctx context.Context, db migrationRowReader, databaseID string, requestID int64, lock bool) (migration.MigrationRequest, error) {
	query := `SELECT r.id, r.migration_id, r.database_id, r.operation, r.revision, r.target_run_id,
		r.sql_snapshot, r.requested_by, r.created_at, m.revision, m.status,
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'group_id', g.id, 'group_name', g.name, 'leader_id', u.id, 'approved_at', a.approved_at
		) ORDER BY g.id)
		FROM connection_grants cg
		JOIN access_groups g ON g.id = cg.group_id
		LEFT JOIN platform_users u ON u.id = g.leader_user_id AND u.active
		LEFT JOIN migration_request_approvals a ON a.request_id = r.id AND a.group_id = g.id AND a.reviewer_id = u.id
		WHERE cg.database_id = r.database_id AND cg.access_level IN (1, 2)), '[]'::jsonb)
		FROM migration_requests r JOIN migrations m ON m.id = r.migration_id
		WHERE r.id = $1 AND r.database_id = $2`
	if lock {
		query += ` FOR UPDATE OF r FOR SHARE OF m`
	}
	var request migration.MigrationRequest
	var currentRevision int64
	var currentStatus migration.Status
	var reviews []byte
	err := db.QueryRowContext(ctx, query, requestID, databaseID).Scan(
		&request.ID, &request.MigrationID, &request.DatabaseID, &request.Operation, &request.Revision, &request.TargetRunID,
		&request.SQLSnapshot, &request.RequestedBy, &request.CreatedAt, &currentRevision, &currentStatus, &reviews)
	if errors.Is(err, sql.ErrNoRows) {
		return migration.MigrationRequest{}, migration.ErrNotFound
	}
	if err != nil {
		return migration.MigrationRequest{}, err
	}
	if err := json.Unmarshal(reviews, &request.Reviews); err != nil {
		return migration.MigrationRequest{}, err
	}
	request.Status = migration.RequestPendingReview
	if currentRevision != request.Revision || (request.Operation == migration.RunApply && currentStatus == migration.StatusApplied) {
		request.Status = migration.RequestStale
		return request, nil
	}
	for _, review := range request.Reviews {
		if review.ApprovedAt == nil {
			return request, nil
		}
	}
	if len(request.Reviews) > 0 {
		request.Status = migration.RequestApproved
	}
	return request, nil
}
