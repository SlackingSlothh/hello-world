package config

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address                string
	PublicOrigin           string
	AllowInsecureLocalHTTP bool
	SessionEncryptionKey   []byte
	OTELExporter           string
	OTELOTLPEndpoint       string
	OTELServiceName        string
	WorkerConcurrency      int
	WorkerMetricsAddr      string
	LockLeaseDuration      time.Duration
	LockHeartbeatInterval  time.Duration
}

func ReadConfig(getenv func(string) string) (Config, error) {
	config := Config{
		Address:               getenv("HTTP_ADDR"),
		PublicOrigin:          getenv("PUBLIC_ORIGIN"),
		OTELExporter:          strings.ToLower(strings.TrimSpace(getenv("OTEL_EXPORTER"))),
		OTELOTLPEndpoint:      strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		OTELServiceName:       strings.TrimSpace(getenv("OTEL_SERVICE_NAME")),
		WorkerConcurrency:     2,
		WorkerMetricsAddr:     strings.TrimSpace(getenv("WORKER_METRICS_ADDR")),
		LockLeaseDuration:     15 * time.Second,
		LockHeartbeatInterval: 5 * time.Second,
	}
	if config.Address == "" {
		config.Address = ":8080"
	}
	if config.OTELExporter == "" {
		config.OTELExporter = "none"
	}
	switch config.OTELExporter {
	case "none", "console", "otlp":
	default:
		return Config{}, errors.New("OTEL_EXPORTER must be one of none, console, or otlp")
	}
	if config.OTELOTLPEndpoint == "" {
		config.OTELOTLPEndpoint = "otel-collector:4317"
	}
	if config.OTELServiceName == "" {
		config.OTELServiceName = "datadock-api"
	}
	if config.WorkerMetricsAddr == "" {
		config.WorkerMetricsAddr = ":9091"
	}
	if value := getenv("ALLOW_INSECURE_LOCAL_HTTP"); value != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, errors.New("ALLOW_INSECURE_LOCAL_HTTP must be a boolean")
		}
		config.AllowInsecureLocalHTTP = allow
	}
	if value := getenv("SESSION_ENCRYPTION_KEY"); value != "" {
		key, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || len(key) != 32 {
			return Config{}, errors.New("SESSION_ENCRYPTION_KEY must encode 32 bytes using standard base64")
		}
		config.SessionEncryptionKey = key
	}
	if value := strings.TrimSpace(getenv("WORKER_CONCURRENCY")); value != "" {
		concurrency, err := strconv.Atoi(value)
		if err != nil || concurrency < 1 {
			return Config{}, errors.New("WORKER_CONCURRENCY must be a positive integer")
		}
		config.WorkerConcurrency = concurrency
	}
	if value := strings.TrimSpace(getenv("LOCK_LEASE_SECONDS")); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 {
			return Config{}, errors.New("LOCK_LEASE_SECONDS must be a positive integer")
		}
		config.LockLeaseDuration = time.Duration(seconds) * time.Second
	}
	if value := strings.TrimSpace(getenv("LOCK_HEARTBEAT_SECONDS")); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 {
			return Config{}, errors.New("LOCK_HEARTBEAT_SECONDS must be a positive integer")
		}
		config.LockHeartbeatInterval = time.Duration(seconds) * time.Second
	}
	if config.LockHeartbeatInterval >= config.LockLeaseDuration {
		return Config{}, errors.New("LOCK_HEARTBEAT_SECONDS must be less than LOCK_LEASE_SECONDS")
	}
	return config, nil
}
