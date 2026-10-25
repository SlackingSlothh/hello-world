package httpapi

import "net/http"

func (handler *AuthenticationHandler) Logout(writer http.ResponseWriter, request *http.Request) {
	if !handler.checkRequest(writer, request) || !readAuthForm(writer, request) {
		return
	}
	if len(request.PostForm) != 0 {
		http.Error(writer, "logout form must be empty", http.StatusBadRequest)
		return
	}
	identifier, err := handler.readIdentifier(request)
	if err != nil {
		http.Error(writer, "invalid session cookie", http.StatusBadRequest)
		return
	}
	if err := handler.sessions.Logout(request.Context(), identifier); err != nil {
		http.Error(writer, "logout unavailable", http.StatusServiceUnavailable)
		return
	}
	handler.cookies.Clear(writer)
	writer.WriteHeader(http.StatusNoContent)
}
