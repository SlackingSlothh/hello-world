package handler

import (
	"errors"
	"net/http"

	accessapp "hello-world/app/access"
)

func (h *Handler) listGroupMembers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	members, err := h.access.ListGroupMembers(r.Context(), r.PathValue("groupID"))
	if err != nil {
		writeMembershipError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (h *Handler) setGroupMember(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := h.access.SetGroupMember(r.Context(), r.PathValue("groupID"), r.PathValue("userID"), r.Method == http.MethodPut); err != nil {
		writeMembershipError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeMembershipError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accessapp.ErrMemberNotFound):
		writeError(w, http.StatusNotFound, "not_found", "group or user not found")
	case errors.Is(err, accessapp.ErrInvalidMembership):
		writeError(w, http.StatusBadRequest, "invalid_membership", "group and user IDs are required")
	default:
		writeError(w, http.StatusServiceUnavailable, "memberships_unavailable", "could not manage group members")
	}
}
