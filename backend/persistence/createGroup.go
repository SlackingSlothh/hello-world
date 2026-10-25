package persistence

import (
	"context"
	"fmt"

	accessapp "hello-world/app/access"
)

func (s *AuthStore) CreateGroup(ctx context.Context, group accessapp.Group) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO access_groups(id,name,is_admin)
		VALUES($1,$2,FALSE) ON CONFLICT(name) DO NOTHING`, group.ID, group.Name)
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
	return nil
}
