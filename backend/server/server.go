package server

import (
	"errors"
	"net/http"
	"time"

	"hello-world/authn"
	"hello-world/authz"
	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/users"
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

func New(config Config, dependencies Dependencies) (*Application, error) {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(config.FrontendDirectory)))
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
