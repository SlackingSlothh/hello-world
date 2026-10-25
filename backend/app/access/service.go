package access

import (
	"context"
	"errors"
	"fmt"

	"hello-world/authz"
)

var (
	ErrNotFound = errors.New("group or connection not found")
	ErrInvalid  = errors.New("invalid group access grant")
)

type Grant struct {
	GroupID    string            `json:"groupId"`
	DatabaseID string            `json:"databaseId"`
	Level      authz.AccessLevel `json:"-"`
	Access     string            `json:"access"`
}

type Group struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	IsAdmin bool    `json:"isAdmin"`
	Grants  []Grant `json:"grants"`
}

type Store interface {
	ListGroupAccess(context.Context) ([]Group, error)
	SetGroupAccess(context.Context, string, string, authz.AccessLevel) error
	DeleteGroupAccess(context.Context, string, string) error
}

type Service struct {
	Store  Store
	Users  UserStore
	Groups GroupStore
}

func (s Service) List(ctx context.Context) ([]Group, error) {
	if s.Store == nil {
		return nil, errors.New("group access store is unavailable")
	}
	groups, err := s.Store.ListGroupAccess(ctx)
	if err != nil {
		return nil, fmt.Errorf("list group access: %w", err)
	}
	for groupIndex := range groups {
		for grantIndex := range groups[groupIndex].Grants {
			switch groups[groupIndex].Grants[grantIndex].Level {
			case authz.ViewOnly:
				groups[groupIndex].Grants[grantIndex].Access = "read"
			case authz.ViewEdit:
				groups[groupIndex].Grants[grantIndex].Access = "write"
			default:
				return nil, errors.New("stored group access has an invalid level")
			}
		}
	}
	return groups, nil
}

func (s Service) Set(ctx context.Context, groupID, databaseID string, level authz.AccessLevel) error {
	if s.Store == nil {
		return errors.New("group access store is unavailable")
	}
	if groupID == "" || databaseID == "" || (level != authz.ViewOnly && level != authz.ViewEdit) {
		return ErrInvalid
	}
	if err := s.Store.SetGroupAccess(ctx, groupID, databaseID, level); err != nil {
		return fmt.Errorf("set group access: %w", err)
	}
	return nil
}

func (s Service) Delete(ctx context.Context, groupID, databaseID string) error {
	if s.Store == nil {
		return errors.New("group access store is unavailable")
	}
	if groupID == "" || databaseID == "" {
		return ErrInvalid
	}
	if err := s.Store.DeleteGroupAccess(ctx, groupID, databaseID); err != nil {
		return fmt.Errorf("delete group access: %w", err)
	}
	return nil
}
