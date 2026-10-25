package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

type PrincipalExtractor func(ctx context.Context) (Principal, bool)

type Handler struct {
	service          *Service
	extractPrincipal PrincipalExtractor
}

func NewHandler(service *Service, extractPrincipal PrincipalExtractor) *Handler {
	return &Handler{
		service:          service,
		extractPrincipal: extractPrincipal,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /databases/{dbID}/migrations", h.HandleListMigrations)
	mux.HandleFunc("GET /databases/{dbID}/migrations/{migrationID}", h.HandleGetMigrationDetail)
	mux.HandleFunc("POST /databases/{dbID}/migrations", h.HandleCreateDraft)
	mux.HandleFunc("POST /databases/{dbID}/migrations/{migrationID}/update", h.HandleUpdateDraft)
	mux.HandleFunc("POST /databases/{dbID}/migrations/{migrationID}/execute", h.HandleExecuteMigration)
	mux.HandleFunc("POST /databases/{dbID}/migrations/explain", h.HandleExplainQuery)
	mux.HandleFunc("POST /databases/{dbID}/migrations/schema-diff", h.HandleSchemaDiff)
}

func (h *Handler) HandleListMigrations(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	items, err := h.service.ListMigrations(r.Context(), principal, dbID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	if isHTMX(r) {
		h.renderMigrationListFragment(w, dbID, items)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"database_id": dbID,
		"migrations":  items,
	})
}

func (h *Handler) HandleGetMigrationDetail(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}
	migrationID, err := parsePathInt64(r, "migrationID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	detail, err := h.service.GetMigrationDetail(r.Context(), principal, dbID, migrationID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) HandleCreateDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	var payload struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		SQLContent  string `json:"sql_content"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			h.writeBadRequest(w, "format JSON tidak valid")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			h.writeBadRequest(w, "gagal membaca form")
			return
		}
		payload.Title = r.FormValue("title")
		payload.Description = r.FormValue("description")
		payload.SQLContent = r.FormValue("sql_content")
	}

	created, err := h.service.CreateDraft(r.Context(), principal, CreateDraftInput{
		DatabaseID:  dbID,
		Title:       payload.Title,
		Description: payload.Description,
		SQLContent:  payload.SQLContent,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) HandleUpdateDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}
	migrationID, err := parsePathInt64(r, "migrationID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	var payload struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		SQLContent  string `json:"sql_content"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			h.writeBadRequest(w, "format JSON tidak valid")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			h.writeBadRequest(w, "gagal membaca form")
			return
		}
		payload.Title = r.FormValue("title")
		payload.Description = r.FormValue("description")
		payload.SQLContent = r.FormValue("sql_content")
	}

	updated, err := h.service.UpdateDraft(r.Context(), principal, UpdateDraftInput{
		DatabaseID:  dbID,
		MigrationID: migrationID,
		Title:       payload.Title,
		Description: payload.Description,
		SQLContent:  payload.SQLContent,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) HandleExecuteMigration(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}
	migrationID, err := parsePathInt64(r, "migrationID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	res, err := h.service.ExecuteMigration(r.Context(), principal, dbID, migrationID)
	if err != nil {
		if errors.Is(err, ErrExecutionFailed) && res != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":     err.Error(),
				"migration": res.Migration,
				"run":       res.Run,
			})
			return
		}
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) HandleExplainQuery(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	var payload struct {
		SQLContent string `json:"sql_content"`
		Analyze    bool   `json:"analyze"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			h.writeBadRequest(w, "format JSON tidak valid")
			return
		}
	} else {
		_ = r.ParseForm()
		payload.SQLContent = r.FormValue("sql_content")
		payload.Analyze = r.FormValue("analyze") == "true" || r.FormValue("analyze") == "1"
	}

	plan, err := h.service.ExplainQuery(r.Context(), principal, dbID, payload.SQLContent, payload.Analyze)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h *Handler) HandleSchemaDiff(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.getPrincipal(r)
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	dbID, err := parsePathString(r, "dbID")
	if err != nil {
		h.writeBadRequest(w, err.Error())
		return
	}

	var payload struct {
		SchemaName    string        `json:"schema_name"`
		DesiredTables []TableSchema `json:"desired_tables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.writeBadRequest(w, "format JSON desired_tables tidak valid")
		return
	}

	diff, err := h.service.GenerateSchemaDiff(r.Context(), principal, dbID, payload.SchemaName, payload.DesiredTables)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

var migrationListTpl = template.Must(template.New("migration_list").Parse(`
<div class="migration-history" data-database-id="{{.DatabaseID}}">
  <table>
    <thead>
      <tr>
        <th>Versi</th>
        <th>Judul</th>
        <th>Status</th>
        <th>Diperbarui</th>
      </tr>
    </thead>
    <tbody>
      {{range .Items}}
      <tr data-migration-id="{{.ID}}">
        <td>{{if .Version}}v{{.Version}}{{else}}-{{end}}</td>
        <td>{{.Title}}</td>
        <td><span class="badge status-{{.Status}}">{{.Status}}</span></td>
        <td>{{.UpdatedAt.Format "2006-01-02 15:04:05"}}</td>
      </tr>
      {{else}}
      <tr><td colspan="4">Belum ada riwayat migrasi untuk basis data ini.</td></tr>
      {{end}}
    </tbody>
  </table>
</div>
`))

func (h *Handler) renderMigrationListFragment(w http.ResponseWriter, dbID string, items []Migration) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = migrationListTpl.Execute(w, map[string]any{
		"DatabaseID": dbID,
		"Items":      items,
	})
}

func (h *Handler) getPrincipal(r *http.Request) (Principal, bool) {
	if h.extractPrincipal == nil {
		return Principal{}, false
	}
	p, ok := h.extractPrincipal(r.Context())
	if !ok || strings.TrimSpace(p.UserID) == "" {
		return Principal{}, false
	}
	return p, true
}

func (h *Handler) writeBadRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrDatabaseMismatch):
		status = http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrMigrationLocked):
		status = http.StatusConflict
	case errors.Is(err, ErrAlreadyApplied), errors.Is(err, ErrInvalidSQL), errors.Is(err, ErrExecutionFailed):
		status = http.StatusUnprocessableEntity
	}

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		escaped := template.HTMLEscapeString(err.Error())
		_, _ = fmt.Fprintf(w, `<div class="alert alert-error" role="alert">%s</div>`, escaped)
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func isHTMX(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

func parsePathString(r *http.Request, param string) (string, error) {
	raw := strings.TrimSpace(r.PathValue(param))
	if raw == "" {
		return "", fmt.Errorf("parameter %s tidak boleh kosong", param)
	}
	return raw, nil
}

func parsePathInt64(r *http.Request, param string) (int64, error) {
	raw := strings.TrimSpace(r.PathValue(param))
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("parameter %s tidak valid", param)
	}
	return val, nil
}