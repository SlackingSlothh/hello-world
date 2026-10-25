package httpapi

import (
	"net/http"
	"strings"
)

func (handler *AuthenticationHandler) RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(writer, request)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		if !handler.checkOrigin(writer, request) {
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *AuthenticationHandler) checkOrigin(writer http.ResponseWriter, request *http.Request) bool {
	origins := request.Header.Values("Origin")
	sites := request.Header.Values("Sec-Fetch-Site")
	if !strings.EqualFold(request.Host, handler.publicHost) || len(origins) > 1 || len(sites) > 1 {
		http.Error(writer, "request origin rejected", http.StatusForbidden)
		return false
	}
	if len(origins) == 1 {
		if origins[0] != handler.publicOrigin {
			http.Error(writer, "request origin rejected", http.StatusForbidden)
			return false
		}
	} else if request.Header.Get("Sec-Fetch-Site") != "same-origin" {
		http.Error(writer, "request origin required", http.StatusForbidden)
		return false
	}
	if err := handler.originGuard.Check(request); err != nil {
		http.Error(writer, "request origin rejected", http.StatusForbidden)
		return false
	}
	return true
}
