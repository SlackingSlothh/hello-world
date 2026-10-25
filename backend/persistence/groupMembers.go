package persistence

import (
	"context"
	"database/sql"
	"errors"

	accessapp "hello-world/app/access"
)

func (s *AuthStore) ListGroupMembers(ctx context.Context, groupID string) ([]accessapp.GroupMember, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM access_groups WHERE id=$1)`, groupID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, accessapp.ErrMemberNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.username,u.active,m.user_id IS NOT NULL
 FROM platform_users u LEFT JOIN user_group_memberships m ON m.user_id=u.id AND m.group_id=$1
 ORDER BY u.username,u.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]accessapp.GroupMember, 0)
	for rows.Next() {
		var member accessapp.GroupMember
		if err := rows.Scan(&member.ID, &member.Username, &member.Active, &member.Member); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *AuthStore) SetGroupMember(ctx context.Context, groupID, userID string, member bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM access_groups WHERE id=$1 FOR UPDATE`, groupID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return accessapp.ErrMemberNotFound
		}
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM platform_users WHERE id=$1 FOR UPDATE`, userID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return accessapp.ErrMemberNotFound
		}
		return err
	}
	var result sql.Result
	if member {
		result, err = tx.ExecContext(ctx, `INSERT INTO user_group_memberships(user_id,group_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, groupID)
	} else {
		result, err = tx.ExecContext(ctx, `DELETE FROM user_group_memberships WHERE user_id=$1 AND group_id=$2`, userID, groupID)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		if member {
			_, err = tx.ExecContext(ctx, `UPDATE managed_sessions SET rotation_required=TRUE WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE managed_sessions SET revoked_at=CURRENT_TIMESTAMP WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
