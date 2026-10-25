package persistence

import (
	"context"
	"fmt"

	accessapp "hello-world/app/access"
)

func (s *AuthStore) CreateUser(ctx context.Context, user accessapp.User, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO platform_users(id,username,password_hash,active,is_platform_admin)
		VALUES($1,$2,$3,TRUE,FALSE) ON CONFLICT(username) DO NOTHING`, user.ID, user.Username, passwordHash)
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
