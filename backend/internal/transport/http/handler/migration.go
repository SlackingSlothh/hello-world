package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"hello-world/internal/services/migration"
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
	writeJSON(w, http.StatusOK, map[string]any{
		"database_id": databaseID, "migrations": items,
		"can_submit": h.migrations.CanExecuteMigration(r.Context(), principal, databaseID),
	})
}

func (h *Handler) listMigrationHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	databaseID := r.PathValue("id")
	history, err := h.migrations.ListMigrationHistory(r.Context(), principal, databaseID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"database_id": databaseID, "runs": history})
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
		Title              string `json:"title"`
		Description        string `json:"description"`
		SQLContent         string `json:"sql_content"`
		SQLDown            string `json:"sql_down"`
		IrreversibleReason string `json:"irreversible_reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	created, err := h.migrations.CreateDraft(r.Context(), principal, migration.CreateDraftInput{
		DatabaseID:         r.PathValue("id"),
		Title:              body.Title,
		Description:        body.Description,
		SQLContent:         body.SQLContent,
		SQLDown:            body.SQLDown,
		IrreversibleReason: body.IrreversibleReason,
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
		Title              string `json:"title"`
		Description        string `json:"description"`
		SQLContent         string `json:"sql_content"`
		SQLDown            string `json:"sql_down"`
		IrreversibleReason string `json:"irreversible_reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	updated, err := h.migrations.UpdateDraft(r.Context(), principal, migration.UpdateDraftInput{
		DatabaseID:         r.PathValue("id"),
		MigrationID:        migrationID,
		Title:              body.Title,
		Description:        body.Description,
		SQLContent:         body.SQLContent,
		SQLDown:            body.SQLDown,
		IrreversibleReason: body.IrreversibleReason,
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

func (h *Handler) submitMigrationRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseMigrationID(w, r)
	if !ok {
		return
	}
	var body struct {
		Operation migration.RunOperation `json:"operation"`
		Revision  int64                  `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	request, err := h.migrations.SubmitMigrationRequest(r.Context(), principal, r.PathValue("id"), migrationID, body.Revision, body.Operation)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	h.writeMigrationRequest(w, r, http.StatusCreated, principal, request)
}

func (h *Handler) getMigrationRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	requestID, ok := parsePositivePathID(w, r, "requestID", "request ID")
	if !ok {
		return
	}
	request, err := h.migrations.GetMigrationRequest(r.Context(), principal, r.PathValue("id"), requestID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	h.writeMigrationRequest(w, r, http.StatusOK, principal, request)
}

func (h *Handler) approveMigrationRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	requestID, ok := parsePositivePathID(w, r, "requestID", "request ID")
	if !ok {
		return
	}
	request, err := h.migrations.ApproveMigrationRequest(r.Context(), principal, r.PathValue("id"), requestID, r.PathValue("groupID"))
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	h.writeMigrationRequest(w, r, http.StatusOK, principal, request)
}

func (h *Handler) writeMigrationRequest(w http.ResponseWriter, r *http.Request, status int, principal migration.Principal, request migration.MigrationRequest) {
	canExecute := request.Status == migration.RequestApproved && request.RequestedBy == principal.UserID &&
		h.migrations.CanExecuteMigration(r.Context(), principal, request.DatabaseID)
	writeJSON(w, status, struct {
		migration.MigrationRequest
		ViewerID   string `json:"viewer_id"`
		CanExecute bool   `json:"can_execute"`
	}{MigrationRequest: request, ViewerID: principal.UserID, CanExecute: canExecute})
}

func (h *Handler) enqueueMigrationRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	requestID, ok := parsePositivePathID(w, r, "requestID", "request ID")
	if !ok {
		return
	}
	result, err := h.migrations.EnqueueMigrationRequest(r.Context(), principal, r.PathValue("id"), requestID)
	if err != nil {
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (h *Handler) reconcileMigrationRun(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.migrationPrincipal(w, r)
	if !ok {
		return
	}
	runID, err := strconv.ParseInt(r.PathValue("runID"), 10, 64)
	if err != nil || runID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "execution ID must be a positive integer")
		return
	}
	result, err := h.migrations.ReconcileRun(r.Context(), principal, r.PathValue("id"), runID)
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
	return parsePositivePathID(w, r, "migrationID", "migration ID")
}

func parsePositivePathID(w http.ResponseWriter, r *http.Request, pathKey, label string) (int64, bool) {
	raw := strings.TrimSpace(r.PathValue(pathKey))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", label+" must be a positive integer")
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
	case errors.Is(err, migration.ErrInvalidMigrationRequest):
		writeError(w, http.StatusBadRequest, "invalid_request", "provide an apply, rollback, or forward operation and a positive revision")
	case errors.Is(err, migration.ErrNotApproved):
		writeError(w, http.StatusConflict, "migration_not_approved", "all required group leaders must approve before execution")
	case errors.Is(err, migration.ErrInvalidJobState):
		writeError(w, http.StatusConflict, "migration_invalid_job_state", "the current job state does not allow this operation")
	case errors.Is(err, migration.ErrMigrationLocked):
		writeError(w, http.StatusConflict, "migration_locked", "a migration is running or awaiting outcome verification for this connection")
	case errors.Is(err, migration.ErrOutcomeUnconfirmed):
		writeError(w, http.StatusConflict, "migration_outcome_unconfirmed", "verify the previous execution outcome before trying again")
	case errors.Is(err, migration.ErrMigrationOrder):
		writeError(w, http.StatusConflict, "migration_out_of_order", "the requested operation is out of order or its target has changed")
	case errors.Is(err, migration.ErrIrreversible):
		writeError(w, http.StatusConflict, "migration_irreversible", "this migration does not have SQL down")
	case errors.Is(err, migration.ErrAlreadyApplied):
		writeError(w, http.StatusConflict, "migration_already_applied", "an applied migration cannot be changed or executed again")
	case errors.Is(err, migration.ErrDefinitionFrozen):
		writeError(w, http.StatusConflict, "migration_definition_frozen", "create a new migration to change a definition that has already been attempted")
	case errors.Is(err, migration.ErrRevisionChanged):
		writeError(w, http.StatusConflict, "migration_revision_changed", "reload the migration before trying again")
	case errors.Is(err, migration.ErrInvalidDefinition):
		writeError(w, http.StatusUnprocessableEntity, "invalid_migration_definition", "provide either SQL down or an irreversible reason")
	case errors.Is(err, migration.ErrInvalidSQL):
		writeError(w, http.StatusUnprocessableEntity, "invalid_migration_sql", "migration SQL is invalid or not transaction-safe")
	case errors.Is(err, migration.ErrExecutionFailed):
		writeError(w, http.StatusUnprocessableEntity, "migration_execution_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "migration_failed", "migration operation failed")
	}
}
