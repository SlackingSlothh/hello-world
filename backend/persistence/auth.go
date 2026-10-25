package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"hello-world/authz"
	"hello-world/users"
)

// AuthStore reads login identities and database grants from the application
// PostgreSQL database. Password hashes are written by the auth provisioning
// flow; this package never accepts or stores plaintext passwords.
type AuthStore struct{ db *sql.DB }

type SeedAccount struct {
	ID            string
	Username      string
	PasswordHash  string
	PlatformAdmin bool
	GroupID       string
	GroupName     string
}

func NewAuthStore(db *sql.DB) (*AuthStore, error) {
	if db == nil {
		return nil, errors.New("auth store database is required")
	}
	return &AuthStore{db: db}, nil
}

// SeedAccount inserts a bootstrap account, its dedicated group, and their
// membership. Existing account passwords and active flags are preserved so
// restarts do not overwrite credential changes or disabled accounts.
func (s *AuthStore) SeedAccount(ctx context.Context, seed SeedAccount) error {
	if seed.ID == "" || seed.Username == "" || seed.PasswordHash == "" || seed.GroupID == "" || seed.GroupName == "" {
		return errors.New("seed account fields are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account seed: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_users(id,username,password_hash,is_platform_admin,active)
		VALUES($1,$2,$3,$4,TRUE) ON CONFLICT(id) DO UPDATE SET is_platform_admin=EXCLUDED.is_platform_admin
		WHERE platform_users.username=EXCLUDED.username`, seed.ID, seed.Username, seed.PasswordHash, seed.PlatformAdmin)
	if err != nil {
		return fmt.Errorf("seed account %s: %w", seed.ID, err)
	}
	var username string
	if err := tx.QueryRowContext(ctx, `SELECT username FROM platform_users WHERE id=$1`, seed.ID).Scan(&username); err != nil {
		return fmt.Errorf("verify seeded account %s: %w", seed.ID, err)
	}
	if username != seed.Username {
		return fmt.Errorf("seed account ID %s is already assigned to another username", seed.ID)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO access_groups(id,name,is_admin) VALUES($1,$2,$3)
		ON CONFLICT(id) DO UPDATE SET is_admin=EXCLUDED.is_admin
		WHERE access_groups.name=EXCLUDED.name`, seed.GroupID, seed.GroupName, seed.PlatformAdmin)
	if err != nil {
		return fmt.Errorf("seed account group %s: %w", seed.GroupID, err)
	}
	var groupName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM access_groups WHERE id=$1`, seed.GroupID).Scan(&groupName); err != nil {
		return fmt.Errorf("verify seeded group %s: %w", seed.GroupID, err)
	}
	if groupName != seed.GroupName {
		return fmt.Errorf("seed group ID %s is already assigned to another group", seed.GroupID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_group_memberships(user_id,group_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, seed.ID, seed.GroupID); err != nil {
		return fmt.Errorf("assign seeded account %s to group: %w", seed.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account seed %s: %w", seed.ID, err)
	}
	return nil
}

func (s *AuthStore) FindByUsername(ctx context.Context, username string) (users.User, error) {
	var u users.User
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash,is_platform_admin,active FROM platform_users WHERE username=$1`, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return users.User{}, users.ErrNotFound
	}
	return u, err
}

func (s *AuthStore) LoadAccess(ctx context.Context, userID string) (authz.UserAccess, error) {
	var access authz.UserAccess
	err := s.db.QueryRowContext(ctx, `SELECT id,active FROM platform_users WHERE id=$1`, userID).Scan(&access.UserID, &access.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.UserAccess{}, users.ErrNotFound
	}
	if err != nil {
		return authz.UserAccess{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.is_admin,cg.database_id,cg.access_level FROM user_group_memberships ug
		JOIN access_groups g ON g.id=ug.group_id LEFT JOIN connection_grants cg ON cg.group_id=g.id WHERE ug.user_id=$1 ORDER BY g.id,cg.database_id`, userID)
	if err != nil {
		return authz.UserAccess{}, err
	}
	defer rows.Close()
	groupSet := map[string]struct{}{}
	for rows.Next() {
		var groupID string
		var adminGroup bool
		var databaseID sql.NullString
		var level sql.NullInt64
		if err := rows.Scan(&groupID, &adminGroup, &databaseID, &level); err != nil {
			return authz.UserAccess{}, err
		}
		if adminGroup {
			access.IsPlatformAdmin = true
		}
		if _, ok := groupSet[groupID]; !ok {
			groupSet[groupID] = struct{}{}
			access.GroupIDs = append(access.GroupIDs, groupID)
		}
		if databaseID.Valid && level.Valid {
			access.Grants = append(access.Grants, authz.GroupGrant{GroupID: groupID, DatabaseID: databaseID.String, Level: authz.AccessLevel(level.Int64)})
		}
	}
	if err := rows.Err(); err != nil {
		return authz.UserAccess{}, err
	}
	return access, nil
}
