package connection

import (
	"context"

	"hello-world/manageddb"
)

func (s Service) Update(ctx context.Context, id string, change manageddb.ConnectionUpdate) (manageddb.Connection, error) {
	var save func(context.Context, manageddb.Config) error
	if s.Store != nil {
		save = s.Store.Save
	}
	return s.Registry.Update(ctx, id, change, save)
}
