package handler

import (
	"errors"
	"net/http"

	accessapp "hello-world/internal/services/access"
)

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Name      string   `json:"name"`
		MemberIDs []string `json:"memberIds"`
		LeaderID  string   `json:"leaderId"`
	}
	if !decode(w, r, &body) {
		return
	}
	group, err := h.access.CreateGroup(r.Context(), body.Name, body.MemberIDs, body.LeaderID)
	switch {
	case errors.Is(err, accessapp.ErrInvalidGroup):
		writeError(w, http.StatusBadRequest, "invalid_group", "group name must be 1–100 bytes without control characters")
	case errors.Is(err, accessapp.ErrInvalidGroupMembers):
		writeError(w, http.StatusBadRequest, "invalid_group_members", err.Error())
	case errors.Is(err, accessapp.ErrInvalidGroupLeader):
		writeError(w, http.StatusBadRequest, "invalid_group_leader", err.Error())
	case errors.Is(err, accessapp.ErrGroupNameTaken):
		writeError(w, http.StatusConflict, "group_name_taken", "group name already exists")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "groups_unavailable", "could not create group")
	default:
		writeJSON(w, http.StatusCreated, group)
	}
}
