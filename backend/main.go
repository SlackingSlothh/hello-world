package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	accessapp "hello-world/app/access"
	catalogapp "hello-world/app/catalog"
	connectionapp "hello-world/app/connection"
	"hello-world/authn"
	"hello-world/dbops"
	mysqlcatalog "hello-world/dialect/mysql"
	postgrescatalog "hello-world/dialect/postgres"
	"hello-world/handler"
	"hello-world/manageddb"
	"hello-world/migrations"
	"hello-world/persistence"
	"hello-world/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	config, err := server.ReadConfig(os.Getenv)
	if err != nil {
		return err
	}
	storeDSN := os.Getenv("DATABASE_STORE_DSN")
	if storeDSN == "" {
		return errors.New("DATABASE_STORE_DSN must be configured")
	}
	storeDB, err := sql.Open("pgx", storeDSN)
	if err != nil {
		return errors.New("could not open application database")
	}
	defer storeDB.Close()
	storeDB.SetMaxOpenConns(5)
	storeDB.SetMaxIdleConns(2)
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startupCancel()
	if err := storeDB.PingContext(startupCtx); err != nil {
		return errors.New("could not connect to application database")
	}
	if err := migrations.Apply(startupCtx, storeDB); err != nil {
		return err
	}

	connectionStore, err := persistence.NewConnectionStore(storeDB, os.Getenv("DATABASE_ENCRYPTION_KEY"))
	if err != nil {
		return err
	}
	sessionStore, err := persistence.NewSessionStore(storeDB)
	if err != nil {
		return err
	}
	authStore, err := persistence.NewAuthStore(storeDB)
	if err != nil {
		return err
	}
	if err := seedInitialAccounts(startupCtx, authStore, os.Getenv); err != nil {
		return err
	}
	registry := manageddb.NewRegistry()
	connections := connectionapp.Service{Registry: registry, Store: connectionStore}
	if err := connections.Restore(startupCtx); err != nil {
		_ = registry.Close()
		return errors.New("could not restore saved database connections")
	}

	application, err := server.New(config, server.Dependencies{Users: authStore, Sessions: sessionStore, Access: authStore})
	if err != nil {
		_ = registry.Close()
		return err
	}
	catalogService := catalogapp.Service{Registry: registry, Readers: map[string]catalogapp.ReaderFactory{
		"postgres": func(db *sql.DB) dbops.CatalogReader { return postgrescatalog.Reader{DB: db} },
		"mysql":    func(db *sql.DB) dbops.CatalogReader { return mysqlcatalog.Reader{DB: db} },
		"mariadb":  func(db *sql.DB) dbops.CatalogReader { return mysqlcatalog.Reader{DB: db} },
	}}
	databaseAPI := handler.New(connections, catalogService, accessapp.Service{Store: authStore}, handler.NewSecurity(application.Authentication, application.Authorization))
	if err := application.HandleRoute("/api/v1/", databaseAPI); err != nil {
		_ = registry.Close()
		return err
	}
	application.HTTP.Handler = withCORS(application.HTTP.Handler, os.Getenv("FRONTEND_ORIGINS"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- application.HTTP.ListenAndServe() }()
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		_ = registry.Close()
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.HTTP.Shutdown(shutdownCtx); err != nil {
			_ = application.HTTP.Close()
			_ = registry.Close()
			return err
		}
		return registry.Close()
	}
}

func seedInitialAccounts(ctx context.Context, store *persistence.AuthStore, getenv func(string) string) error {
	accounts := []struct {
		id, username, password, groupID, groupName string
		admin                                      bool
	}{
		{"bootstrap-admin", getenv("SEED_ADMIN_USERNAME"), getenv("SEED_ADMIN_PASSWORD"), "bootstrap-admins", "Administrators", true},
		{"bootstrap-user", getenv("SEED_USER_USERNAME"), getenv("SEED_USER_PASSWORD"), "bootstrap-users", "Regular users", false},
	}
	for _, account := range accounts {
		if account.username == "" || account.password == "" {
			return errors.New("SEED_ADMIN_USERNAME, SEED_ADMIN_PASSWORD, SEED_USER_USERNAME, and SEED_USER_PASSWORD must be configured")
		}
		hash, err := authn.HashPassword(account.password)
		if err != nil {
			return errors.New("could not hash bootstrap account password")
		}
		encoded, err := hash.Encoded()
		if err != nil {
			return errors.New("could not encode bootstrap account password hash")
		}
		if err := store.SeedAccount(ctx, persistence.SeedAccount{
			ID: account.id, Username: account.username, PasswordHash: encoded,
			PlatformAdmin: account.admin, GroupID: account.groupID, GroupName: account.groupName,
		}); err != nil {
			return err
		}
	}
	return nil
}

func withCORS(next http.Handler, configuredOrigins string) http.Handler {
	allowed := make(map[string]struct{})
	for _, origin := range strings.Split(configuredOrigins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		_, ok := allowed[origin]
		if origin != "" {
			w.Header().Add("Vary", "Origin")
		}
		if origin != "" && ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if !ok {
				http.Error(w, "origin is not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, HX-Request, HX-Trigger, HX-Trigger-Name, HX-Target, HX-Current-URL, HX-Boosted")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
