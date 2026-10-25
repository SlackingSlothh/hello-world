package authz

import (
	"context"
	"errors"
)

var ErrForbidden = errors.New("access denied")

type Action string

const (
	ManageUsers       Action = "platform.users.manage"
	ManageGroups      Action = "platform.groups.manage"
	ManageConnections Action = "platform.connections.manage"
	ManageGrants      Action = "platform.grants.manage"
	ReadSchema        Action = "schema.read"
	ReadData          Action = "data.read"
	ReadMigration     Action = "migration.read"
	CreateMigration   Action = "migration.create"
	UpdateMigration   Action = "migration.update"
	ExecuteMigration  Action = "migration.execute"
)

type AccessLevel uint8

const (
	NoAccess AccessLevel = iota
	ViewOnly
	ViewEdit
)

type UserAccess struct {
	UserID          string
	Active          bool
	IsPlatformAdmin bool
	GroupIDs        []string
	Grants          []GroupGrant
}

type GroupGrant struct {
	GroupID    string
	DatabaseID string
	Level      AccessLevel
}

type AccessReader interface {
	LoadAccess(context.Context, string) (UserAccess, error)
}

// mengambil akses database tertinggi dari grup-grup yang dimiliki oleh user
func HighestDatabaseAccess(access UserAccess, databaseID string) AccessLevel {
	if access.UserID == "" || !access.Active || databaseID == "" {
		return NoAccess
	}
	if access.IsPlatformAdmin {
		return ViewEdit
	}
	groups := make(map[string]struct{}, len(access.GroupIDs))
	for _, id := range access.GroupIDs {
		if id != "" {
			groups[id] = struct{}{}
		}
	}
	level := NoAccess
	for _, grant := range access.Grants {
		if grant.DatabaseID != databaseID {
			continue
		}
		if _, member := groups[grant.GroupID]; !member {
			continue
		}
		switch grant.Level {
		case NoAccess, ViewOnly, ViewEdit:
			if grant.Level > level {
				level = grant.Level
			}
		default:
			return NoAccess
		}
	}
	return level
}

func Authorize(access UserAccess, action Action, databaseID string) error {
	if access.UserID == "" || !access.Active {
		return ErrForbidden
	}
	switch action {
	case ManageUsers, ManageGroups, ManageConnections, ManageGrants:
		if access.IsPlatformAdmin && databaseID == "" {
			return nil
		}
	case ReadSchema, ReadData, ReadMigration:
		if access.IsPlatformAdmin {
			return nil
		}
		if HighestDatabaseAccess(access, databaseID) >= ViewOnly {
			return nil
		}
	case CreateMigration, UpdateMigration, ExecuteMigration:
		if HighestDatabaseAccess(access, databaseID) == ViewEdit {
			return nil
		}
	}
	return ErrForbidden
}
