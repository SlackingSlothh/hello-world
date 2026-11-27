package config_test

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"hello-world/internal/config"
)

func TestReadConfig(t *testing.T) {
	defaults, err := config.ReadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Address != ":8080" || defaults.AllowInsecureLocalHTTP || len(defaults.SessionEncryptionKey) != 0 || defaults.PublicOrigin != "" ||
		defaults.OTELExporter != "none" || defaults.OTELOTLPEndpoint != "otel-collector:4317" || defaults.OTELServiceName != "datadock-api" ||
		defaults.WorkerConcurrency != 2 || defaults.WorkerMetricsAddr != ":9091" ||
		defaults.LockLeaseDuration != 15*time.Second || defaults.LockHeartbeatInterval != 5*time.Second {
		t.Fatal("unexpected defaults")
	}
	key := bytes.Repeat([]byte{7}, 32)
	environment := map[string]string{
		"HTTP_ADDR":                   "127.0.0.1:9000",
		"PUBLIC_ORIGIN":               "http://localhost:9000",
		"ALLOW_INSECURE_LOCAL_HTTP":   "true",
		"SESSION_ENCRYPTION_KEY":      base64.StdEncoding.EncodeToString(key),
		"OTEL_EXPORTER":               "otlp",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "localhost:4317",
		"OTEL_SERVICE_NAME":           "datadock-worker",
		"WORKER_CONCURRENCY":          "4",
		"WORKER_METRICS_ADDR":         ":9099",
		"LOCK_LEASE_SECONDS":          "30",
		"LOCK_HEARTBEAT_SECONDS":      "10",
	}
	loaded, err := config.ReadConfig(func(name string) string { return environment[name] })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Address != environment["HTTP_ADDR"] || loaded.PublicOrigin != environment["PUBLIC_ORIGIN"] || !loaded.AllowInsecureLocalHTTP || !bytes.Equal(loaded.SessionEncryptionKey, key) ||
		loaded.OTELExporter != "otlp" || loaded.OTELOTLPEndpoint != "localhost:4317" || loaded.OTELServiceName != "datadock-worker" ||
		loaded.WorkerConcurrency != 4 || loaded.WorkerMetricsAddr != ":9099" ||
		loaded.LockLeaseDuration != 30*time.Second || loaded.LockHeartbeatInterval != 10*time.Second {
		t.Fatal("environment config not loaded")
	}
	for name, environment := range map[string]map[string]string{
		"invalid bool":            {"ALLOW_INSECURE_LOCAL_HTTP": "sometimes"},
		"invalid base64":          {"SESSION_ENCRYPTION_KEY": "not-a-key"},
		"short key":               {"SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 31))},
		"long key":                {"SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 33))},
		"invalid otel exporter":   {"OTEL_EXPORTER": "jaeger"},
		"zero worker concurrency": {"WORKER_CONCURRENCY": "0"},
		"bad worker concurrency":  {"WORKER_CONCURRENCY": "many"},
		"zero lease":              {"LOCK_LEASE_SECONDS": "0"},
		"zero heartbeat":          {"LOCK_HEARTBEAT_SECONDS": "0"},
		"heartbeat ge lease":      {"LOCK_LEASE_SECONDS": "10", "LOCK_HEARTBEAT_SECONDS": "10"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.ReadConfig(func(key string) string { return environment[key] }); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
