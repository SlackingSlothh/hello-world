package httpapi

import (
	"context"
	"errors"
	"net/http"

	"hello-world/authn"
	"hello-world/session"
)

type identityContextKey struct{}

func IdentityFromContext(ctx context.Context) (authn.Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(authn.Identity)
	return identity, ok && identity.UserID != ""
}

func (handler *AuthenticationHandler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		identifier, err := handler.cookies.Read(request)
		if err != nil {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}
		validated, err := handler.sessions.Validate(request.Context(), identifier)
		if errors.Is(err, session.ErrInvalid) {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}
		if err != nil || validated.UserID == "" {
			http.Error(writer, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		if validated.Replacement != nil {
			if err := handler.cookies.Write(writer, *validated.Replacement); err != nil {
				http.Error(writer, "authentication unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		identity := authn.Identity{UserID: validated.UserID}
		ctx := context.WithValue(request.Context(), identityContextKey{}, identity)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}
