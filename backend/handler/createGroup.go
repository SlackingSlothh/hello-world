package handler

import (
	"errors"
	"net/http"

	accessapp "hello-world/app/access"
)

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	group, err := h.access.CreateGroup(r.Context(), body.Name)
	switch {
	case errors.Is(err, accessapp.ErrInvalidGroup):
		writeError(w, http.StatusBadRequest, "invalid_group", "group name must be 1–100 bytes without control characters")
	case errors.Is(err, accessapp.ErrGroupNameTaken):
		writeError(w, http.StatusConflict, "group_name_taken", "group name already exists")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "groups_unavailable", "could not create group")
	default:
		writeJSON(w, http.StatusCreated, group)
	}
}
