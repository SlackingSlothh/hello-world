package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello-world/internal/config"
	persistence "hello-world/internal/database/platform"
	"hello-world/internal/database/target/manageddb"
	targetmigration "hello-world/internal/database/target/migration"
	"hello-world/internal/observability"
	connectionapp "hello-world/internal/services/connection"
	"hello-world/internal/services/migration"
	"hello-world/migrations"
)

func RunWorker() error {
	appConfig, err := config.ReadConfig(os.Getenv)
	if err != nil {
		return err
	}
	serviceName := appConfig.OTELServiceName
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		serviceName = "datadock-worker"
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startupCancel()

	telemetry, err := observability.InitTelemetry(startupCtx, observability.TelemetryConfig{
		Exporter:     appConfig.OTELExporter,
		OTLPEndpoint: appConfig.OTELOTLPEndpoint,
		ServiceName:  serviceName,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	logger := observability.NewLogger(observability.LoggerOptions{
		Writer:       os.Stdout,
		OTelProvider: telemetry.LoggerProvider,
		ServiceName:  serviceName,
	})
	metrics := observability.NewMetrics()

	storeDSN := os.Getenv("DATABASE_STORE_DSN")
	if storeDSN == "" {
		return errors.New("DATABASE_STORE_DSN must be configured")
	}
	storeDB, err := sql.Open("pgx", storeDSN)
	if err != nil {
		return errors.New("could not open application database")
	}
	defer storeDB.Close()
	storeDB.SetMaxOpenConns(10)
	storeDB.SetMaxIdleConns(4)
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
	authStore, err := persistence.NewAuthStore(storeDB)
	if err != nil {
		return err
	}
	registry := manageddb.NewRegistry()
	defer registry.Close()
	connections := connectionapp.Service{Registry: registry, Store: connectionStore}
	if err := connections.Restore(startupCtx); err != nil {
		return errors.New("could not restore saved database connections")
	}

	_ = metrics.RegisterDBPoolCollector(func() []observability.PoolStatsSnapshot {
		snaps := []observability.PoolStatsSnapshot{
			{PoolType: "platform", Database: "platform", Stats: storeDB.Stats()},
		}
		for _, conn := range registry.List() {
			if conn.Connected {
				snaps = append(snaps, observability.PoolStatsSnapshot{
					PoolType: "target",
					Database: conn.Name,
					Stats:    conn.Stats,
				})
			}
		}
		return snaps
	})

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", metrics.Handler())
	metricsMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	metricsServer := &http.Server{
		Addr:              appConfig.WorkerMetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	migrationStore := persistence.NewPostgresStore(storeDB)
	jobStore := persistence.NewJobStore(storeDB)
	lockStore := persistence.NewLockStore(storeDB)
	migrationConnector := targetmigration.NewSQLTargetConnectorWithResolver(func(ctx context.Context, databaseID string) (*sql.DB, string, error) {
		connection, db, err := registry.ConnectionDB(databaseID)
		if errors.Is(err, manageddb.ErrNotFound) {
			if saved, loadErr := connectionStore.LoadAll(ctx); loadErr == nil {
				for _, cfg := range saved {
					if cfg.ID == databaseID {
						_, _ = registry.Restore(cfg)
						break
					}
				}
			}
			connection, db, err = registry.ConnectionDB(databaseID)
		}
		if err != nil {
			return nil, "", err
		}
		return db, connection.Driver, nil
	})
	migrationAudit := migration.SlogAuditLogger{Logger: logger}
	migrationService := migration.NewService(migration.AccessAuthorizer{Reader: authStore}, migrationStore, migrationConnector, migrationAudit)
	migrationService.ConfigureAsync(jobStore, lockStore, migration.AllowAllApprovals{}, metrics)

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "worker"
	}
	pool := migration.NewWorkerPool(migrationService, migrationConnector, migration.WorkerConfig{
		WorkerIDPrefix:    hostname,
		Concurrency:       appConfig.WorkerConcurrency,
		LeaseDuration:     appConfig.LockLeaseDuration,
		HeartbeatInterval: appConfig.LockHeartbeatInterval,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.LogAttrs(context.Background(), slog.LevelError, "worker.metrics_server_error",
				slog.String("event", "worker.metrics_server_error"),
				slog.String("error", err.Error()),
			)
		}
	}()

	logger.LogAttrs(ctx, slog.LevelInfo, "worker.started",
		slog.String("event", "worker.started"),
		slog.Int("concurrency", appConfig.WorkerConcurrency),
		slog.String("metrics_addr", appConfig.WorkerMetricsAddr),
		slog.String("status", "running"),
	)

	pool.Run(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = metricsServer.Shutdown(shutdownCtx)
	return nil
}
