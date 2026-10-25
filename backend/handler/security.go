package handler

import (
	"context"
	"net/http"

	"hello-world/authz"
	"hello-world/httpapi"
	"hello-world/manageddb"
)

// Security connects the database API routes to the application's authn/authz
// boundaries. A nil implementation is deliberately fail-closed.
type Security interface {
	RequireUser(http.Handler) http.Handler
	RequireAdmin(http.Handler) http.Handler
	RequireConnectionAccess(authz.Action, http.Handler) http.Handler
	VisibleConnections(context.Context, []manageddb.Connection) ([]manageddb.Connection, error)
}

type authSecurity struct {
	authentication *httpapi.AuthenticationHandler
	authorization  *httpapi.AuthorizationHandler
}

func NewSecurity(authentication *httpapi.AuthenticationHandler, authorization *httpapi.AuthorizationHandler) Security {
	if authentication == nil || authorization == nil {
		return unavailableSecurity{}
	}
	return authSecurity{authentication: authentication, authorization: authorization}
}

func (s authSecurity) RequireUser(next http.Handler) http.Handler {
	return s.authentication.RequireSession(next)
}
func (s authSecurity) RequireAdmin(next http.Handler) http.Handler {
	return s.authentication.RequireSession(s.authorization.RequirePermission(authz.ManageConnections, "", next))
}
func (s authSecurity) RequireConnectionAccess(action authz.Action, next http.Handler) http.Handler {
	return s.authentication.RequireSession(s.authorization.RequirePermission(action, "id", next))
}
func (s authSecurity) VisibleConnections(ctx context.Context, items []manageddb.Connection) ([]manageddb.Connection, error) {
	identity, ok := httpapi.IdentityFromContext(ctx)
	if !ok {
		return nil, authz.ErrForbidden
	}
	visible := make([]manageddb.Connection, 0, len(items))
	for _, item := range items {
		allowed, err := s.authorization.CanAccess(ctx, identity.UserID, authz.ReadSchema, item.ID)
		if err != nil {
			return nil, err
		}
		if allowed {
			visible = append(visible, item)
		}
	}
	return visible, nil
}

type unavailableSecurity struct{}

func (unavailableSecurity) fail(w http.ResponseWriter) {
	http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
}
func (s unavailableSecurity) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { s.fail(w) })
}
func (s unavailableSecurity) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { s.fail(w) })
}
func (s unavailableSecurity) RequireConnectionAccess(_ authz.Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { s.fail(w) })
}
func (unavailableSecurity) VisibleConnections(_ context.Context, _ []manageddb.Connection) ([]manageddb.Connection, error) {
	return nil, authz.ErrForbidden
}
