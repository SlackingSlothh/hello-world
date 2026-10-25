package httpapi

import (
	"errors"
	"net/http"

	"hello-world/authn"
)

func (handler *AuthenticationHandler) Login(writer http.ResponseWriter, request *http.Request) {
	if !handler.checkRequest(writer, request) || !readAuthForm(writer, request) {
		return
	}
	usernameValues := request.PostForm["username"]
	passwordValues := request.PostForm["password"]
	if len(request.PostForm) != 2 || len(usernameValues) != 1 || len(passwordValues) != 1 ||
		usernameValues[0] == "" || passwordValues[0] == "" {
		http.Error(writer, "invalid login form", http.StatusBadRequest)
		return
	}
	previous, err := handler.readIdentifier(request)
	if err != nil {
		http.Error(writer, "invalid session cookie", http.StatusBadRequest)
		return
	}
	identity, err := handler.authenticator.Authenticate(request.Context(), usernameValues[0], passwordValues[0])
	if errors.Is(err, authn.ErrInvalidCredentials) {
		http.Error(writer, "invalid username or password", http.StatusUnauthorized)
		return
	}
	if err != nil || identity.UserID == "" {
		http.Error(writer, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	issued, err := handler.sessions.StartAfterLogin(request.Context(), identity.UserID, previous)
	if err != nil {
		http.Error(writer, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := handler.cookies.Write(writer, issued); err != nil {
		http.Error(writer, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
