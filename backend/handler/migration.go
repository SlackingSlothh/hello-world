package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"hello-world/migration"
)

func (h *Handler) listMigrations(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	databaseID := r.PathValue("id")
	items, err := h.migrations.ListMigrations(r.Context(), principal, databaseID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"database_id": databaseID, "migrations": items})
}

func (h *Handler) getMigration(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseMigrationID(w, r)
	if !ok {
		return
	}
	detail, err := h.migrations.GetMigrationDetail(r.Context(), principal, r.PathValue("id"), migrationID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) createMigration(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		SQLContent  string `json:"sql_content"`
	}
	if !decode(w, r, &body) {
		return
	}
	created, err := h.migrations.CreateDraft(r.Context(), principal, migration.CreateDraftInput{
		DatabaseID:  r.PathValue("id"),
		Title:       body.Title,
		Description: body.Description,
		SQLContent:  body.SQLContent,
	})
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) updateMigration(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseMigrationID(w, r)
	if !ok {
		return
	}
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		SQLContent  string `json:"sql_content"`
	}
	if !decode(w, r, &body) {
		return
	}
	updated, err := h.migrations.UpdateDraft(r.Context(), principal, migration.UpdateDraftInput{
		DatabaseID:  r.PathValue("id"),
		MigrationID: migrationID,
		Title:       body.Title,
		Description: body.Description,
		SQLContent:  body.SQLContent,
	})
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) executeMigration(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseMigrationID(w, r)
	if !ok {
		return
	}
	result, err := h.migrations.ExecuteMigration(r.Context(), principal, r.PathValue("id"), migrationID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) explainMigration(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	var body struct {
		SQLContent string `json:"sql_content"`
		Analyze    bool   `json:"analyze"`
	}
	if !decode(w, r, &body) {
		return
	}
	plan, err := h.migrations.ExplainQuery(r.Context(), principal, r.PathValue("id"), body.SQLContent, body.Analyze)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h *Handler) schemaDiff(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaName    string                  `json:"schema_name"`
		DesiredTables []migration.TableSchema `json:"desired_tables"`
	}
	if !decode(w, r, &body) {
		return
	}
	diff, err := h.migrations.GenerateSchemaDiff(r.Context(), principal, r.PathValue("id"), body.SchemaName, body.DesiredTables)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (h *Handler) migrationPrincipal(w http.ResponseWriter, r *http.Request) (migration.Principal, bool) {
	if h.migrations == nil {
		writeError(w, http.StatusServiceUnavailable, "migration_unavailable", "migration service is unavailable")
		return migration.Principal{}, false
	}
	userID, ok := h.security.UserID(r.Context())
	if !ok || strings.TrimSpace(userID) == "" {
		writeError(w, http.StatusUnauthorized, "authentication_required", "authentication is required")
		return migration.Principal{}, false
	}
	return migration.Principal{UserID: userID}, true
}

func parseMigrationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.PathValue("migrationID"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "migration ID must be a positive integer")
		return 0, false
	}
	return id, true
}

func writeMigrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, migration.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "authentication_required", "authentication is required")
	case errors.Is(err, migration.ErrForbidden), errors.Is(err, migration.ErrDatabaseMismatch):
		writeError(w, http.StatusForbidden, "access_denied", "access to this migration is denied")
	case errors.Is(err, migration.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "migration not found")
	case errors.Is(err, migration.ErrMigrationLocked):
		writeError(w, http.StatusConflict, "migration_locked", "another migration is executing for this connection")
	case errors.Is(err, migration.ErrAlreadyApplied):
		writeError(w, http.StatusConflict, "migration_already_applied", "an applied migration cannot be changed or executed again")
	case errors.Is(err, migration.ErrInvalidSQL):
		writeError(w, http.StatusUnprocessableEntity, "invalid_migration_sql", "migration SQL is invalid or not transaction-safe")
	case errors.Is(err, migration.ErrExecutionFailed):
		writeError(w, http.StatusUnprocessableEntity, "migration_execution_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "migration_failed", "migration operation failed")
	}
}
