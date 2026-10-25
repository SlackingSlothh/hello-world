package access

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidGroup = errors.New("invalid group name")
var ErrGroupNameTaken = errors.New("group name already exists")

type GroupStore interface {
	CreateGroup(context.Context, Group) error
}

func (s Service) CreateGroup(ctx context.Context, name string) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return Group{}, ErrInvalidGroup
	}
	if s.Groups == nil {
		return Group{}, errors.New("group store is unavailable")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Group{}, err
	}
	group := Group{ID: hex.EncodeToString(id[:]), Name: name, Grants: []Grant{}}
	if err := s.Groups.CreateGroup(ctx, group); err != nil {
		return Group{}, err
	}
	return group, nil
}
