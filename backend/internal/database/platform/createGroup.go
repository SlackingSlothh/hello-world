package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	accessapp "hello-world/internal/services/access"
)

func (s *AuthStore) CreateGroup(ctx context.Context, group accessapp.Group, memberIDs []string) error {
	if group.LeaderID == nil {
		return accessapp.ErrInvalidGroupLeader
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO access_groups(id,name,is_admin,leader_user_id)
		VALUES($1,$2,FALSE,$3) ON CONFLICT(name) DO NOTHING`, group.ID, group.Name, *group.LeaderID)
	if err != nil {
		return fmt.Errorf("create group: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count created groups: %w", err)
	}
	if count == 0 {
		return accessapp.ErrGroupNameTaken
	}
	// Lock members in ID order so concurrent group creation with overlapping members can rotate sessions safely.
	result, err = tx.ExecContext(ctx, `INSERT INTO user_group_memberships(user_id,group_id)
		SELECT id,$1 FROM platform_users WHERE id=ANY($2::text[]) ORDER BY id FOR NO KEY UPDATE`, group.ID, memberIDs)
	if err != nil {
		return fmt.Errorf("create group memberships: %w", err)
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if err := validateGroupLeader(ctx, tx, group.ID, *group.LeaderID); err != nil {
		return err
	}
	if count != int64(len(memberIDs)) {
		return accessapp.ErrInvalidGroupMembers
	}
	if _, err := tx.ExecContext(ctx, `UPDATE managed_sessions SET rotation_required=TRUE
		WHERE user_id=ANY($1::text[]) AND revoked_at IS NULL`, memberIDs); err != nil {
		return fmt.Errorf("rotate new group members' sessions: %w", err)
	}
	return tx.Commit()
}

func validateGroupLeader(ctx context.Context, tx *sql.Tx, groupID, leaderID string) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT u.active FROM user_group_memberships m
		JOIN platform_users u ON u.id=m.user_id WHERE m.group_id=$1 AND m.user_id=$2 FOR SHARE OF u`,
		groupID, leaderID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return accessapp.ErrInvalidGroupLeader
	}
	if err != nil {
		return err
	}
	if !active {
		return accessapp.ErrInvalidGroupLeader
	}
	return nil
}
