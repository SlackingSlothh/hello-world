package handler

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	accessapp "hello-world/app/access"
)

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "query parameters are not allowed")
		return
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		writeError(w, 415, "invalid_content_type", "use application/x-www-form-urlencoded")
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/x-www-form-urlencoded" || (parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8")) {
		writeError(w, 415, "invalid_content_type", "use application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	defer r.Body.Close()
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, 413, "request_too_large", "form is too large")
		} else {
			writeError(w, 400, "invalid_request", "invalid form")
		}
		return
	}
	if len(r.PostForm) != 2 || len(r.PostForm["username"]) != 1 || len(r.PostForm["password"]) != 1 {
		writeError(w, 400, "invalid_request", "provide username and password once each")
		return
	}
	user, err := h.access.CreateUser(r.Context(), r.PostForm.Get("username"), r.PostForm.Get("password"))
	switch {
	case errors.Is(err, accessapp.ErrInvalidUser):
		writeError(w, 400, "invalid_user", "username must be 1–100 bytes without whitespace or control characters; password must be 1–1024 bytes")
	case errors.Is(err, accessapp.ErrUsernameTaken):
		writeError(w, 409, "username_taken", "username already exists")
	case err != nil:
		writeError(w, 503, "users_unavailable", "could not create user")
	default:
		writeJSON(w, http.StatusCreated, user)
	}
}
