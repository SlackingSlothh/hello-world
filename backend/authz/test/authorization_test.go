package authz_test

import (
	"errors"
	"hello-world/authz"
	"testing"
)

func TestAuthorizeActionsByAccessLevel(t *testing.T) {
	cases := []struct {
		name     string
		action   authz.Action
		viewOnly bool
		viewEdit bool
	}{
		{"readSchema", authz.ReadSchema, true, true},
		{"readData", authz.ReadData, true, true},
		{"readMigration", authz.ReadMigration, true, true},
		{"createMigration", authz.CreateMigration, false, true},
		{"updateMigration", authz.UpdateMigration, false, true},
		{"executeMigration", authz.ExecuteMigration, false, true},
	}
	for _, testCase := range cases {
		for _, permission := range []struct {
			name    string
			level   authz.AccessLevel
			allowed bool
		}{
			{"noAccess", authz.NoAccess, false},
			{"viewOnly", authz.ViewOnly, testCase.viewOnly},
			{"viewEdit", authz.ViewEdit, testCase.viewEdit},
		} {
			t.Run(testCase.name+"/"+permission.name, func(t *testing.T) {
				access := authz.UserAccess{
					UserID: "u1", Active: true, GroupIDs: []string{"g1"},
					Grants: []authz.GroupGrant{{GroupID: "g1", DatabaseID: "db1", Level: permission.level}},
				}
				err := authz.Authorize(access, testCase.action, "db1")
				if permission.allowed && err != nil {
					t.Fatalf("access denied: %v", err)
				}
				if !permission.allowed && !errors.Is(err, authz.ErrForbidden) {
					t.Fatal("access should be denied")
				}
			})
		}
	}
}

func TestGroupAccessUsesHighestMatchingGrant(t *testing.T) {
	access := authz.UserAccess{
		UserID: "u1", Active: true, GroupIDs: []string{"g1", "g2"},
		Grants: []authz.GroupGrant{
			{GroupID: "g1", DatabaseID: "db1", Level: authz.ViewOnly},
			{GroupID: "g2", DatabaseID: "db1", Level: authz.ViewEdit},
			{GroupID: "g1", DatabaseID: "db2", Level: authz.ViewOnly},
			{GroupID: "otherGroup", DatabaseID: "db2", Level: authz.ViewEdit},
		},
	}
	if authz.HighestDatabaseAccess(access, "db1") != authz.ViewEdit {
		t.Fatal("highest membership grant not selected")
	}
	if authz.HighestDatabaseAccess(access, "db2") != authz.ViewOnly {
		t.Fatal("another group's grant was used")
	}
	if authz.HighestDatabaseAccess(access, "db3") != authz.NoAccess {
		t.Fatal("unknown database granted access")
	}
	access.Active = false
	if !errors.Is(authz.Authorize(access, authz.ReadData, "db1"), authz.ErrForbidden) {
		t.Fatal("inactive user allowed")
	}
}

func TestPlatformAdminPermissions(t *testing.T) {
	admin := authz.UserAccess{UserID: "admin", Active: true, IsPlatformAdmin: true}
	for _, action := range []authz.Action{authz.ManageUsers, authz.ManageGroups, authz.ManageConnections, authz.ManageGrants} {
		t.Run(string(action), func(t *testing.T) {
			if err := authz.Authorize(admin, action, ""); err != nil {
				t.Fatal(err)
			}
			user := admin
			user.IsPlatformAdmin = false
			if !errors.Is(authz.Authorize(user, action, ""), authz.ErrForbidden) {
				t.Fatal("ordinary user managed platform")
			}
		})
	}
	if !errors.Is(authz.Authorize(admin, authz.ReadData, "db1"), authz.ErrForbidden) {
		t.Fatal("admin bypassed database grant")
	}
}
