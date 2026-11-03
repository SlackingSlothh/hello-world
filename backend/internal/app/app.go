package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello-world/internal/config"
	persistence "hello-world/internal/database/platform"
	"hello-world/internal/database/target/manageddb"
	targetmigration "hello-world/internal/database/target/migration"
	mysqlcatalog "hello-world/internal/database/target/mysql"
	postgrescatalog "hello-world/internal/database/target/postgres"
	accessapp "hello-world/internal/services/access"
	catalogapp "hello-world/internal/services/catalog"
	connectionapp "hello-world/internal/services/connection"
	"hello-world/internal/services/migration"
	"hello-world/internal/transport/http/handler"
	"hello-world/internal/transport/http/server"
	"hello-world/migrations"
)

func Run() error {
	appConfig, err := config.ReadConfig(os.Getenv)
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

	application, err := server.New(appConfig, server.Dependencies{Users: authStore, Sessions: sessionStore, Access: authStore})
	if err != nil {
		_ = registry.Close()
		return err
	}
	catalogService := catalogapp.Service{Registry: registry, Readers: map[string]catalogapp.ReaderFactory{
		"postgres": func(db *sql.DB) catalogapp.CatalogReader { return postgrescatalog.Reader{DB: db} },
		"mysql":    func(db *sql.DB) catalogapp.CatalogReader { return mysqlcatalog.Reader{DB: db} },
		"mariadb":  func(db *sql.DB) catalogapp.CatalogReader { return mysqlcatalog.Reader{DB: db} },
	}}
	migrationStore := persistence.NewPostgresStore(storeDB)
	migrationConnector := targetmigration.NewSQLTargetConnectorWithResolver(func(ctx context.Context, databaseID string) (*sql.DB, string, error) {
		connection, db, err := registry.ConnectionDB(databaseID)
		if err != nil {
			return nil, "", err
		}
		return db, connection.Driver, nil
	})
	migrationService := migration.NewService(migration.AccessAuthorizer{Reader: authStore}, migrationStore, migrationConnector, nil)
	databaseAPI := handler.New(connections, catalogService, accessapp.Service{Store: authStore, Users: authStore, Groups: authStore, Memberships: authStore}, handler.NewSecurity(application.Authentication, application.Authorization), migrationService)
	if err := application.HandleRoute("/api/v1/", databaseAPI); err != nil {
		_ = registry.Close()
		return err
	}
	application.HTTP.Handler = server.WithCORS(application.HTTP.Handler, os.Getenv("FRONTEND_ORIGINS"))

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
