package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"hello-world/internal/services/authz"
)

type AuthorizationHandler struct {
	accessReader authz.AccessReader
	logger       *slog.Logger
}

func (handler *AuthorizationHandler) SetLogger(logger *slog.Logger) {
	handler.logger = logger
}

func (handler *AuthorizationHandler) CanAccess(ctx context.Context, userID string, action authz.Action, databaseID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	access, err := handler.accessReader.LoadAccess(ctx, userID)
	if err != nil {
		return false, err
	}
	if access.UserID != userID {
		return false, errors.New("access reader returned a mismatched user")
	}
	return authz.Authorize(access, action, databaseID) == nil, nil
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
			if handler.logger != nil {
				handler.logger.LogAttrs(request.Context(), slog.LevelWarn, "authz.denied",
					slog.String("event", "authz.denied"),
					slog.String("actor", identity.UserID),
					slog.String("database", databaseID),
					slog.String("action", string(action)),
					slog.String("status", "denied"),
				)
			}
			http.Error(writer, "access denied", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
