package access

import (
	"context"
	"errors"
)

var ErrMemberNotFound = errors.New("group or user not found")
var ErrInvalidMembership = errors.New("group and user IDs are required")

type GroupMember struct {
	User
	Member bool `json:"member"`
}

type MembershipStore interface {
	ListGroupMembers(context.Context, string) ([]GroupMember, error)
	SetGroupMember(context.Context, string, string, bool) error
}

func (s Service) ListGroupMembers(ctx context.Context, groupID string) ([]GroupMember, error) {
	if groupID == "" {
		return nil, ErrInvalidMembership
	}
	if s.Memberships == nil {
		return nil, errors.New("membership store is unavailable")
	}
	return s.Memberships.ListGroupMembers(ctx, groupID)
}

func (s Service) SetGroupMember(ctx context.Context, groupID, userID string, member bool) error {
	if groupID == "" || userID == "" {
		return ErrInvalidMembership
	}
	if s.Memberships == nil {
		return errors.New("membership store is unavailable")
	}
	return s.Memberships.SetGroupMember(ctx, groupID, userID, member)
}
