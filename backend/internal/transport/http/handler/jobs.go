package handler

import (
	"net/http"
	"strings"
)

func (h *Handler) rollbackMigration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseMigrationID(w, r)
	if !ok {
		return
	}
	if h.migrations.AsyncEnabled() {
		enqueued, err := h.migrations.EnqueueRollback(r.Context(), principal, r.PathValue("id"), migrationID)
		if err != nil {
			writeMigrationError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, enqueued)
		return
	}
	result, err := h.migrations.RollbackMigration(r.Context(), principal, r.PathValue("id"), migrationID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) forwardMigration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseMigrationID(w, r)
	if !ok {
		return
	}
	if h.migrations.AsyncEnabled() {
		enqueued, err := h.migrations.EnqueueForward(r.Context(), principal, r.PathValue("id"), migrationID)
		if err != nil {
			writeMigrationError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, enqueued)
		return
	}
	result, err := h.migrations.ForwardMigration(r.Context(), principal, r.PathValue("id"), migrationID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	limit, err := intQuery(query.Get("limit"), 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer")
		return
	}
	offset, err := intQuery(query.Get("offset"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer")
		return
	}
	databaseID := r.PathValue("id")
	jobs, err := h.migrations.ListJobsByDatabase(r.Context(), principal, databaseID, limit, offset)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"database_id": databaseID, "jobs": jobs})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "job ID is required")
		return
	}
	job, err := h.migrations.GetJob(r.Context(), principal, jobID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) resumeBatchJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "job ID is required")
		return
	}
	enqueued, err := h.migrations.EnqueueResumeBatch(r.Context(), principal, r.PathValue("id"), jobID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, enqueued)
}

func (h *Handler) revertBatchJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "job ID is required")
		return
	}
	enqueued, err := h.migrations.EnqueueRevertBatch(r.Context(), principal, r.PathValue("id"), jobID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, enqueued)
}
