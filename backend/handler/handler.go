package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessapp "hello-world/app/access"
	catalogapp "hello-world/app/catalog"
	connectionapp "hello-world/app/connection"
	"hello-world/authz"
	"hello-world/dbops"
	"hello-world/manageddb"
	"hello-world/migration"
)

const maxRequestBody = 1 << 20

type Handler struct {
	connections connectionapp.Service
	catalog     catalogapp.Service
	access      accessapp.Service
	migrations  *migration.Service
	mux         *http.ServeMux
	security    Security
}

type connectionRequest struct {
	Name   string               `json:"name"`
	Driver string               `json:"driver"`
	DSN    string               `json:"dsn"`
	Pool   manageddb.PoolConfig `json:"pool"`
}

func New(connections connectionapp.Service, catalog catalogapp.Service, access accessapp.Service, security Security, migrations *migration.Service) http.Handler {
	h := &Handler{connections: connections, catalog: catalog, access: access, migrations: migrations, mux: http.NewServeMux(), security: security}
	h.routes()
	return h.recover(h.withTimeout(h.mux))
}

func (h *Handler) routes() {
	user := func(next http.HandlerFunc) http.Handler { return h.security.RequireUser(next) }
	connectionUser := func(action authz.Action, next http.HandlerFunc) http.Handler {
		return h.security.RequireConnectionAccess(action, next)
	}
	admin := func(next http.HandlerFunc) http.Handler {
		return h.security.RequireAdmin(next)
	}
	h.mux.Handle("GET /api/v1/connections", user(h.listConnections))
	h.mux.Handle("POST /api/v1/connections", admin(h.createConnection))
	h.mux.Handle("GET /api/v1/connections/{id}", connectionUser(authz.ReadSchema, h.getConnection))
	h.mux.Handle("PATCH /api/v1/connections/{id}", admin(h.updateConnection))
	h.mux.Handle("DELETE /api/v1/connections/{id}", admin(h.deleteConnection))
	h.mux.Handle("POST /api/v1/connections/{id}/test", connectionUser(authz.ReadSchema, h.testConnection))
	h.mux.Handle("POST /api/v1/connections/{id}/connect", admin(h.connectConnection))
	h.mux.Handle("POST /api/v1/connections/{id}/disconnect", admin(h.disconnectConnection))
	h.mux.Handle("GET /api/v1/access/groups", admin(h.listGroupAccess))
	h.mux.Handle("PUT /api/v1/access/groups/{groupID}/connections/{connectionID}", admin(h.setGroupAccess))
	h.mux.Handle("DELETE /api/v1/access/groups/{groupID}/connections/{connectionID}", admin(h.deleteGroupAccess))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas", connectionUser(authz.ReadSchema, h.schemas))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas/{schema}/tables", connectionUser(authz.ReadSchema, h.tables))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas/{schema}/tables/{table}", connectionUser(authz.ReadSchema, h.table))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas/{schema}/tables/{table}/columns", connectionUser(authz.ReadSchema, h.columns))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas/{schema}/tables/{table}/constraints", connectionUser(authz.ReadSchema, h.constraints))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas/{schema}/tables/{table}/indexes", connectionUser(authz.ReadSchema, h.indexes))
	h.mux.Handle("GET /api/v1/connections/{id}/schemas/{schema}/tables/{table}/rows", connectionUser(authz.ReadData, h.rows))
	h.mux.Handle("POST /api/v1/connections/{id}/query", connectionUser(authz.ReadData, h.selectQuery))
	h.mux.Handle("GET /api/v1/connections/{id}/migrations", connectionUser(authz.ReadMigration, h.listMigrations))
	h.mux.Handle("GET /api/v1/connections/{id}/migrations/{migrationID}", connectionUser(authz.ReadMigration, h.getMigration))
	h.mux.Handle("POST /api/v1/connections/{id}/migrations", connectionUser(authz.CreateMigration, h.createMigration))
	h.mux.Handle("PATCH /api/v1/connections/{id}/migrations/{migrationID}", connectionUser(authz.UpdateMigration, h.updateMigration))
	h.mux.Handle("POST /api/v1/connections/{id}/migrations/{migrationID}/execute", connectionUser(authz.ExecuteMigration, h.executeMigration))
	h.mux.Handle("POST /api/v1/connections/{id}/migrations/explain", connectionUser(authz.ExecuteMigration, h.explainMigration))
	h.mux.Handle("POST /api/v1/connections/{id}/migrations/schema-diff", connectionUser(authz.ReadMigration, h.schemaDiff))
}

type connectionOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

func (h *Handler) listGroupAccess(w http.ResponseWriter, r *http.Request) {
	groups, err := h.access.List(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "access_unavailable", "could not load group access")
		return
	}
	connections := h.connections.List()
	options := make([]connectionOption, 0, len(connections))
	for _, connection := range connections {
		options = append(options, connectionOption{ID: connection.ID, Name: connection.Name, Driver: connection.Driver})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "connections": options})
}

func (h *Handler) setGroupAccess(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Access string `json:"access"`
	}
	if !decode(w, r, &body) {
		return
	}
	var level authz.AccessLevel
	switch body.Access {
	case "read":
		level = authz.ViewOnly
	case "write":
		level = authz.ViewEdit
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "access must be 'read' or 'write'")
		return
	}
	if err := h.access.Set(r.Context(), r.PathValue("groupID"), r.PathValue("connectionID"), level); err != nil {
		writeAccessError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteGroupAccess(w http.ResponseWriter, r *http.Request) {
	if err := h.access.Delete(r.Context(), r.PathValue("groupID"), r.PathValue("connectionID")); err != nil {
		writeAccessError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accessapp.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", "group access request is invalid")
	case errors.Is(err, accessapp.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "group or database connection not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "access_unavailable", "could not update group access")
	}
}

func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	items, err := h.security.VisibleConnections(r.Context(), h.connections.List())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "authorization_unavailable", "could not load database permissions")
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (h *Handler) createConnection(w http.ResponseWriter, r *http.Request) {
	var request connectionRequest
	if !decode(w, r, &request) {
		return
	}
	out, err := h.connections.Create(r.Context(), manageddb.Config{Name: request.Name, Driver: request.Driver, DSN: request.DSN, Pool: request.Pool})
	if err != nil {
		writeError(w, 400, "connection_failed", "could not create or verify database connection")
		return
	}
	writeJSON(w, 201, out)
}
func (h *Handler) getConnection(w http.ResponseWriter, r *http.Request) {
	out, err := h.connections.Get(r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *Handler) updateConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pool   *manageddb.PoolConfig `json:"pool"`
		Name   string                `json:"name"`
		Driver string                `json:"driver"`
		DSN    string                `json:"dsn"`
	}
	if !decode(w, r, &body) {
		return
	}
	var out manageddb.Connection
	var err error
	if body.DSN != "" {
		out, err = h.connections.Replace(r.Context(), r.PathValue("id"), manageddb.Config{Name: body.Name, Driver: body.Driver, DSN: body.DSN, Pool: valuePool(body.Pool)})
	} else if body.Pool != nil {
		out, err = h.connections.UpdatePool(r.Context(), r.PathValue("id"), *body.Pool)
	} else {
		writeError(w, 400, "invalid_request", "provide pool settings or a complete replacement connection")
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func valuePool(p *manageddb.PoolConfig) manageddb.PoolConfig {
	if p == nil {
		return manageddb.PoolConfig{}
	}
	return *p
}
func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.connections.Remove(r.Context(), r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) testConnection(w http.ResponseWriter, r *http.Request) {
	started, err := h.connections.Test(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 502, "connection_failed", "database ping failed")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "latencyMs": float64(started.Microseconds()) / 1000})
}
func (h *Handler) connectConnection(w http.ResponseWriter, r *http.Request) {
	out, err := h.connections.Connect(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *Handler) disconnectConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.connections.Disconnect(r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) schemas(w http.ResponseWriter, r *http.Request) {
	out, e := h.catalog.Schemas(r.Context(), r.PathValue("id"))
	respond(w, out, e)
}
func (h *Handler) tables(w http.ResponseWriter, r *http.Request) {
	out, e := h.catalog.Tables(r.Context(), r.PathValue("id"), r.PathValue("schema"))
	respond(w, out, e)
}
func (h *Handler) table(w http.ResponseWriter, r *http.Request) {
	out, e := h.catalog.Tables(r.Context(), r.PathValue("id"), r.PathValue("schema"))
	if e != nil {
		writeServiceError(w, e)
		return
	}
	for _, item := range out {
		if item.Name == r.PathValue("table") {
			writeJSON(w, 200, item)
			return
		}
	}
	writeError(w, 404, "not_found", "table not found")
}
func (h *Handler) columns(w http.ResponseWriter, r *http.Request) {
	out, e := h.catalog.Columns(r.Context(), r.PathValue("id"), r.PathValue("schema"), r.PathValue("table"))
	respond(w, out, e)
}
func (h *Handler) constraints(w http.ResponseWriter, r *http.Request) {
	out, e := h.catalog.Constraints(r.Context(), r.PathValue("id"), r.PathValue("schema"), r.PathValue("table"))
	respond(w, out, e)
}
func (h *Handler) indexes(w http.ResponseWriter, r *http.Request) {
	out, e := h.catalog.Indexes(r.Context(), r.PathValue("id"), r.PathValue("schema"), r.PathValue("table"))
	respond(w, out, e)
}
func (h *Handler) rows(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := intQuery(query.Get("limit"), catalogapp.DefaultLimit)
	if err != nil {
		writeError(w, 400, "invalid_request", "limit must be an integer")
		return
	}
	offset, err := intQuery(query.Get("offset"), 0)
	if err != nil {
		writeError(w, 400, "invalid_request", "offset must be an integer")
		return
	}
	p := dbops.PageRequest{Limit: limit, Offset: offset, Columns: query["column"], Sort: query.Get("sort"), Direction: strings.ToLower(query.Get("direction"))}
	for key, values := range query {
		if strings.HasPrefix(key, "filter.") && len(values) > 0 {
			if p.Filters == nil {
				p.Filters = make(map[string]string)
			}
			p.Filters[strings.TrimPrefix(key, "filter.")] = values[len(values)-1]
		}
	}
	out, e := h.catalog.Rows(r.Context(), r.PathValue("id"), r.PathValue("schema"), r.PathValue("table"), p)
	respond(w, out, e)
}
func (h *Handler) selectQuery(w http.ResponseWriter, r *http.Request) {
	var req dbops.QueryRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, e := h.catalog.Select(ctx, r.PathValue("id"), req)
	respond(w, out, e)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid_json", "request body is invalid")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func writeServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, manageddb.ErrNotFound) {
		writeError(w, 404, "not_found", "connection not found")
		return
	}
	if errors.Is(err, dbops.ErrObjectNotFound) {
		writeError(w, 404, "not_found", "database object not found")
		return
	}
	if errors.Is(err, manageddb.ErrAlreadyExists) {
		writeError(w, 409, "already_exists", "connection ID already exists")
		return
	}
	if errors.Is(err, dbops.ErrUnsafeQuery) || errors.Is(err, dbops.ErrInvalidRequest) {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	writeError(w, 500, "database_operation_failed", "database operation failed")
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func intQuery(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}
func (h *Handler) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (h *Handler) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
