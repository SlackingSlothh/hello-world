package server

import (
	"errors"
	"net/http"
	"time"

	"hello-world/internal/config"
	"hello-world/internal/services/authn"
	"hello-world/internal/services/authz"
	"hello-world/internal/services/session"
	"hello-world/internal/services/users"
	"hello-world/internal/transport/http/httpapi"
)

type Dependencies struct {
	Users    users.Reader
	Sessions session.Store
	Access   authz.AccessReader
}

type Application struct {
	HTTP           *http.Server
	Authentication *httpapi.AuthenticationHandler
	Authorization  *httpapi.AuthorizationHandler
	mux            *http.ServeMux
}

func New(config config.Config, dependencies Dependencies) (*Application, error) {
	mux := http.NewServeMux()
	application := &Application{
		mux: mux,
		HTTP: &http.Server{
			Addr: config.Address, Handler: mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
	if dependencies.Users == nil && dependencies.Sessions == nil && dependencies.Access == nil {
		unavailable := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Cache-Control", "no-store")
			http.Error(writer, "authentication unavailable", http.StatusServiceUnavailable)
		})
		mux.Handle("/auth/login", unavailable)
		mux.Handle("/auth/logout", unavailable)
		mux.Handle("GET /auth/session", unavailable)
		mux.Handle("GET /auth/admin-session", unavailable)
		return application, nil
	}
	if dependencies.Users == nil || dependencies.Sessions == nil || dependencies.Access == nil {
		return nil, errors.New("server requires user, session, and access adapters together")
	}
	cipher, err := session.NewIdentifierCipher(config.SessionEncryptionKey)
	if err != nil {
		return nil, err
	}
	sessions, err := session.NewService(dependencies.Sessions, session.DefaultConfig(), cipher, nil)
	if err != nil {
		return nil, err
	}
	authenticator, err := authn.NewAuthenticator(dependencies.Users)
	if err != nil {
		return nil, err
	}
	application.Authentication, err = httpapi.NewAuthenticationHandler(
		authenticator, sessions,
		session.Cookies{AllowInsecureLocalHTTP: config.AllowInsecureLocalHTTP},
		config.PublicOrigin,
	)
	if err != nil {
		return nil, err
	}
	application.Authorization, err = httpapi.NewAuthorizationHandler(dependencies.Access)
	if err != nil {
		return nil, err
	}
	sessionStatus := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("GET /auth/session", application.Authentication.RequireSession(sessionStatus))
	mux.Handle("GET /auth/admin-session", application.Authentication.RequireSession(
		application.Authorization.RequirePermission(authz.ManageConnections, "", sessionStatus),
	))
	mux.Handle("/auth/", application.Authentication.Routes())
	application.HTTP.Handler = application.Authentication.RequireSameOrigin(mux)
	return application, nil
}

func (application *Application) HandleProtected(pattern string, action authz.Action, databasePathParameter string, handler http.Handler) error {
	if application.Authentication == nil || application.Authorization == nil {
		return errors.New("protected routes require authentication and authorization adapters")
	}
	application.mux.Handle(pattern, application.Authentication.RequireSession(
		application.Authorization.RequirePermission(action, databasePathParameter, handler),
	))
	return nil
}

// HandleRoute mounts an already secured API subtree. Authentication and
// authorization must be applied by the route handler before calling this.
func (application *Application) HandleRoute(pattern string, handler http.Handler) error {
	if handler == nil {
		return errors.New("route handler is required")
	}
	application.mux.Handle(pattern, handler)
	return nil
}
