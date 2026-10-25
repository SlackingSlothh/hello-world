package connection

import (
	"context"
	"errors"
	"time"

	"hello-world/manageddb"
)

type ConfigStore interface {
	Save(context.Context, manageddb.Config) error
	LoadAll(context.Context) ([]manageddb.Config, error)
	Delete(context.Context, string) error
}

type Service struct {
	Registry *manageddb.Registry
	Store    ConfigStore
}

func (s Service) Create(ctx context.Context, c manageddb.Config) (manageddb.Connection, error) {
	created, err := s.Registry.Add(ctx, c)
	if err != nil {
		return manageddb.Connection{}, err
	}
	if s.Store == nil {
		return created, nil
	}
	stored, err := s.Registry.ConfigForStorage(created.ID)
	if err == nil {
		err = s.Store.Save(ctx, stored)
	}
	if err != nil {
		rollbackErr := s.Registry.Remove(created.ID)
		return manageddb.Connection{}, errors.Join(err, rollbackErr)
	}
	return created, nil
}
func (s Service) List() []manageddb.Connection                { return s.Registry.List() }
func (s Service) Get(id string) (manageddb.Connection, error) { return s.Registry.Get(id) }
func (s Service) Test(ctx context.Context, id string) (time.Duration, error) {
	return s.Registry.Test(ctx, id)
}
func (s Service) Connect(ctx context.Context, id string) (manageddb.Connection, error) {
	return s.Registry.Connect(ctx, id)
}
func (s Service) Disconnect(id string) error {
	return s.Registry.Disconnect(id)
}
func (s Service) Remove(ctx context.Context, id string) error {
	old, err := s.Registry.ConfigForStorage(id)
	if err != nil {
		return err
	}
	if s.Store != nil {
		if err := s.Store.Delete(ctx, id); err != nil {
			return err
		}
	}
	if err := s.Registry.Remove(id); err != nil {
		if s.Store != nil {
			return errors.Join(err, s.Store.Save(ctx, old))
		}
		return err
	}
	return nil
}
func (s Service) UpdatePool(ctx context.Context, id string, p manageddb.PoolConfig) (manageddb.Connection, error) {
	old, err := s.Registry.ConfigForStorage(id)
	if err != nil {
		return manageddb.Connection{}, err
	}
	updated := old
	updated.Pool = p
	if s.Store != nil {
		if err := s.Store.Save(ctx, updated); err != nil {
			return manageddb.Connection{}, err
		}
	}
	view, err := s.Registry.UpdatePool(id, p)
	if err != nil && s.Store != nil {
		return manageddb.Connection{}, errors.Join(err, s.Store.Save(ctx, old))
	}
	return view, err
}
func (s Service) Replace(ctx context.Context, id string, c manageddb.Config) (manageddb.Connection, error) {
	old, err := s.Registry.ConfigForStorage(id)
	if err != nil {
		return manageddb.Connection{}, err
	}
	c.ID = id
	if s.Store != nil {
		if err := s.Store.Save(ctx, c); err != nil {
			return manageddb.Connection{}, err
		}
	}
	view, err := s.Registry.Replace(ctx, id, c)
	if err != nil && s.Store != nil {
		return manageddb.Connection{}, errors.Join(err, s.Store.Save(ctx, old))
	}
	return view, err
}

// Restore reloads saved connections into the runtime registry without pinging
// target databases. The storage database itself must be available at startup.
func (s Service) Restore(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	configs, err := s.Store.LoadAll(ctx)
	if err != nil {
		return err
	}
	var restoreErrs []error
	for _, config := range configs {
		if _, err := s.Registry.Restore(config); err != nil {
			restoreErrs = append(restoreErrs, errors.New("restore managed database connection "+config.ID))
		}
	}
	return errors.Join(restoreErrs...)
}
