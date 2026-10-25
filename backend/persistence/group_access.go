package persistence

import (
	"context"
	"database/sql"
	"fmt"

	accessapp "hello-world/app/access"
	"hello-world/authz"
)

func (s *AuthStore) ListGroupAccess(ctx context.Context) ([]accessapp.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.name,g.is_admin,cg.database_id,cg.access_level
		FROM access_groups g LEFT JOIN connection_grants cg ON cg.group_id=g.id
		ORDER BY g.name,g.id,cg.database_id`)
	if err != nil {
		return nil, fmt.Errorf("query group access: %w", err)
	}
	defer rows.Close()

	groups := make([]accessapp.Group, 0)
	indices := make(map[string]int)
	for rows.Next() {
		var id, name string
		var isAdmin bool
		var databaseID sql.NullString
		var level sql.NullInt64
		if err := rows.Scan(&id, &name, &isAdmin, &databaseID, &level); err != nil {
			return nil, fmt.Errorf("scan group access: %w", err)
		}
		index, exists := indices[id]
		if !exists {
			index = len(groups)
			indices[id] = index
			groups = append(groups, accessapp.Group{ID: id, Name: name, IsAdmin: isAdmin, Grants: []accessapp.Grant{}})
		}
		if databaseID.Valid {
			if authz.AccessLevel(level.Int64) == authz.NoAccess {
				continue
			}
			groups[index].Grants = append(groups[index].Grants, accessapp.Grant{
				GroupID: id, DatabaseID: databaseID.String, Level: authz.AccessLevel(level.Int64),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read group access: %w", err)
	}
	return groups, nil
}

func (s *AuthStore) SetGroupAccess(ctx context.Context, groupID, databaseID string, level authz.AccessLevel) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group access update: %w", err)
	}
	defer tx.Rollback()
	if err := validateGroupAndConnection(ctx, tx, groupID, databaseID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO connection_grants(group_id,database_id,access_level)
		VALUES($1,$2,$3) ON CONFLICT(group_id,database_id) DO UPDATE SET access_level=EXCLUDED.access_level`, groupID, databaseID, level)
	if err != nil {
		return fmt.Errorf("upsert group access: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit group access update: %w", err)
	}
	return nil
}

func (s *AuthStore) DeleteGroupAccess(ctx context.Context, groupID, databaseID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group access removal: %w", err)
	}
	defer tx.Rollback()
	if err := validateGroupAndConnection(ctx, tx, groupID, databaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM connection_grants WHERE group_id=$1 AND database_id=$2`, groupID, databaseID); err != nil {
		return fmt.Errorf("delete group access: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit group access removal: %w", err)
	}
	return nil
}

func validateGroupAndConnection(ctx context.Context, tx *sql.Tx, groupID, databaseID string) error {
	var groupExists, databaseExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM access_groups WHERE id=$1),
		EXISTS(SELECT 1 FROM managed_db_connections WHERE id=$2)`, groupID, databaseID).Scan(&groupExists, &databaseExists); err != nil {
		return fmt.Errorf("validate group access targets: %w", err)
	}
	if !groupExists || !databaseExists {
		return accessapp.ErrNotFound
	}
	return nil
}
