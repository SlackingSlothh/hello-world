package persistence

import (
	"context"
	"fmt"

	accessapp "hello-world/internal/services/access"
)

func (s *AuthStore) CreateUser(ctx context.Context, user accessapp.User, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO platform_users(id,username,password_hash,active)
		VALUES($1,$2,$3,TRUE) ON CONFLICT(username) DO NOTHING`, user.ID, user.Username, passwordHash)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count created users: %w", err)
	}
	if count == 0 {
		return accessapp.ErrUsernameTaken
	}
	return nil
}

func (s *AuthStore) ListUsers(ctx context.Context) ([]accessapp.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,username,active FROM platform_users ORDER BY username,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]accessapp.User, 0)
	for rows.Next() {
		var user accessapp.User
		if err := rows.Scan(&user.ID, &user.Username, &user.Active); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}
