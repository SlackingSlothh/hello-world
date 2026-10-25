package httpapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"hello-world/authn"
	"hello-world/session"
)

const maxAuthBodyBytes int64 = 8 * 1024

type Authenticator interface {
	Authenticate(context.Context, string, string) (authn.Identity, error)
}

type Sessions interface {
	StartAfterLogin(context.Context, string, session.Identifier) (session.Issued, error)
	Logout(context.Context, session.Identifier) error
	Validate(context.Context, session.Identifier) (session.Validated, error)
}

type AuthenticationHandler struct {
	authenticator Authenticator
	sessions      Sessions
	cookies       session.Cookies
	publicOrigin  string
	publicHost    string
	originGuard   *http.CrossOriginProtection
}

func NewAuthenticationHandler(authenticator Authenticator, sessions Sessions, cookies session.Cookies, publicOrigin string) (*AuthenticationHandler, error) {
	if authenticator == nil || sessions == nil {
		return nil, errors.New("auth handler requires authentication and session services")
	}
	origin, err := url.Parse(publicOrigin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Opaque != "" ||
		(origin.Scheme != "https" && origin.Scheme != "http") ||
		(origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" ||
		origin.ForceQuery || origin.Fragment != "" || origin.RawFragment != "" {
		return nil, errors.New("public origin must be an HTTP or HTTPS origin without credentials, query, or fragment")
	}
	if (origin.Scheme == "http") != cookies.AllowInsecureLocalHTTP {
		return nil, errors.New("cookie security must match the configured public origin")
	}
	return &AuthenticationHandler{
		authenticator: authenticator, sessions: sessions, cookies: cookies,
		publicOrigin: origin.Scheme + "://" + origin.Host, publicHost: origin.Host,
		originGuard: http.NewCrossOriginProtection(),
	}, nil
}

func (handler *AuthenticationHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", handler.Login)
	mux.HandleFunc("/auth/logout", handler.Logout)
	return mux
}

func (handler *AuthenticationHandler) checkRequest(writer http.ResponseWriter, request *http.Request) bool {
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		http.Error(writer, "query parameters are not accepted", http.StatusBadRequest)
		return false
	}
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

func readAuthForm(writer http.ResponseWriter, request *http.Request) bool {
	contentTypes := request.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		http.Error(writer, "form-urlencoded content type required", http.StatusUnsupportedMediaType)
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/x-www-form-urlencoded" ||
		(parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8")) {
		http.Error(writer, "form-urlencoded content type required", http.StatusUnsupportedMediaType)
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxAuthBodyBytes)
	if err := request.ParseForm(); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			http.Error(writer, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(writer, "invalid form encoding", http.StatusBadRequest)
		}
		return false
	}
	return true
}

func (handler *AuthenticationHandler) readIdentifier(request *http.Request) (session.Identifier, error) {
	for _, cookie := range request.Cookies() {
		if cookie.Name == session.CookieName {
			return handler.cookies.Read(request)
		}
	}
	return session.Identifier{}, nil
}
