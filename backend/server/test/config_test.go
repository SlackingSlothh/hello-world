package server_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"hello-world/server"
)

func TestReadConfig(t *testing.T) {
	defaults, err := server.ReadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Address != ":8080" || defaults.FrontendDirectory != "frontend/dist" || defaults.AllowInsecureLocalHTTP || len(defaults.SessionEncryptionKey) != 0 || defaults.PublicOrigin != "" {
		t.Fatal("unexpected defaults")
	}
	key := bytes.Repeat([]byte{7}, 32)
	environment := map[string]string{"HTTP_ADDR": "127.0.0.1:9000", "FRONTEND_DIR": "../frontend/dist", "PUBLIC_ORIGIN": "http://localhost:9000", "ALLOW_INSECURE_LOCAL_HTTP": "true", "SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(key)}
	config, err := server.ReadConfig(func(name string) string { return environment[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.Address != environment["HTTP_ADDR"] || config.FrontendDirectory != environment["FRONTEND_DIR"] || config.PublicOrigin != environment["PUBLIC_ORIGIN"] || !config.AllowInsecureLocalHTTP || !bytes.Equal(config.SessionEncryptionKey, key) {
		t.Fatal("environment config not loaded")
	}
	for name, environment := range map[string]map[string]string{
		"invalid bool":   {"ALLOW_INSECURE_LOCAL_HTTP": "sometimes"},
		"invalid base64": {"SESSION_ENCRYPTION_KEY": "not-a-key"},
		"short key":      {"SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 31))},
		"long key":       {"SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 33))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := server.ReadConfig(func(key string) string { return environment[key] }); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
