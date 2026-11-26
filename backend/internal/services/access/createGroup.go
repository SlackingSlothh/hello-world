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
var ErrInvalidGroupMembers = errors.New("group member IDs must be unique and refer to existing users")
var ErrInvalidGroupLeader = errors.New("leader must be an active member of the group")

type GroupStore interface {
	CreateGroup(context.Context, Group, []string) error
}

func (s Service) CreateGroup(ctx context.Context, name string, memberIDs []string, leaderID string) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return Group{}, ErrInvalidGroup
	}
	members := make(map[string]bool, len(memberIDs))
	for _, id := range memberIDs {
		if strings.TrimSpace(id) == "" || members[id] {
			return Group{}, ErrInvalidGroupMembers
		}
		members[id] = true
	}
	if !members[leaderID] {
		return Group{}, ErrInvalidGroupLeader
	}
	if s.Groups == nil {
		return Group{}, errors.New("group store is unavailable")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Group{}, err
	}
	group := Group{ID: hex.EncodeToString(id[:]), Name: name, LeaderID: &leaderID, Grants: []Grant{}}
	if err := s.Groups.CreateGroup(ctx, group, memberIDs); err != nil {
		return Group{}, err
	}
	return group, nil
}
