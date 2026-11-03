package app

import (
	"context"
	"errors"

	persistence "hello-world/internal/database/platform"
	"hello-world/internal/services/authn"
)

func seedInitialAccounts(ctx context.Context, store *persistence.AuthStore, getenv func(string) string) error {
	accounts := []struct {
		id, username, password, groupID, groupName string
		groupIsAdmin                               bool
	}{
		{"bootstrap-admin", getenv("SEED_ADMIN_USERNAME"), getenv("SEED_ADMIN_PASSWORD"), "bootstrap-admins", "Administrators", true},
		{"bootstrap-user", getenv("SEED_USER_USERNAME"), getenv("SEED_USER_PASSWORD"), "bootstrap-users", "Regular users", false},
	}
	for _, account := range accounts {
		if account.username == "" || account.password == "" {
			return errors.New("SEED_ADMIN_USERNAME, SEED_ADMIN_PASSWORD, SEED_USER_USERNAME, and SEED_USER_PASSWORD must be configured")
		}
		hash, err := authn.HashPassword(account.password)
		if err != nil {
			return errors.New("could not hash bootstrap account password")
		}
		encoded, err := hash.Encoded()
		if err != nil {
			return errors.New("could not encode bootstrap account password hash")
		}
		if err := store.SeedAccount(ctx, persistence.SeedAccount{
			ID: account.id, Username: account.username, PasswordHash: encoded,
			GroupIsAdmin: account.groupIsAdmin, GroupID: account.groupID, GroupName: account.groupName,
		}); err != nil {
			return err
		}
	}
	return nil
}
