package httpapi

import (
	"errors"
	"net/http"

	"hello-world/authz"
)

type AuthorizationHandler struct {
	accessReader authz.AccessReader
}

func NewAuthorizationHandler(accessReader authz.AccessReader) (*AuthorizationHandler, error) {
	if accessReader == nil {
		return nil, errors.New("authorization handler requires an access reader")
	}
	return &AuthorizationHandler{accessReader: accessReader}, nil
}

func (handler *AuthorizationHandler) RequirePermission(action authz.Action, databasePathParameter string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		identity, ok := IdentityFromContext(request.Context())
		if !ok {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}
		var databaseID string
		if databasePathParameter != "" {
			databaseID = request.PathValue(databasePathParameter)
			if databaseID == "" {
				http.Error(writer, "database identifier required", http.StatusBadRequest)
				return
			}
		}
		access, err := handler.accessReader.LoadAccess(request.Context(), identity.UserID)
		if err != nil || access.UserID != identity.UserID {
			http.Error(writer, "authorization unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := authz.Authorize(access, action, databaseID); err != nil {
			http.Error(writer, "access denied", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
