package config_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"hello-world/internal/config"
)

func TestReadConfig(t *testing.T) {
	defaults, err := config.ReadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Address != ":8080" || defaults.AllowInsecureLocalHTTP || len(defaults.SessionEncryptionKey) != 0 || defaults.PublicOrigin != "" {
		t.Fatal("unexpected defaults")
	}
	key := bytes.Repeat([]byte{7}, 32)
	environment := map[string]string{"HTTP_ADDR": "127.0.0.1:9000", "PUBLIC_ORIGIN": "http://localhost:9000", "ALLOW_INSECURE_LOCAL_HTTP": "true", "SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(key)}
	loaded, err := config.ReadConfig(func(name string) string { return environment[name] })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Address != environment["HTTP_ADDR"] || loaded.PublicOrigin != environment["PUBLIC_ORIGIN"] || !loaded.AllowInsecureLocalHTTP || !bytes.Equal(loaded.SessionEncryptionKey, key) {
		t.Fatal("environment config not loaded")
	}
	for name, environment := range map[string]map[string]string{
		"invalid bool":   {"ALLOW_INSECURE_LOCAL_HTTP": "sometimes"},
		"invalid base64": {"SESSION_ENCRYPTION_KEY": "not-a-key"},
		"short key":      {"SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 31))},
		"long key":       {"SESSION_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 33))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.ReadConfig(func(key string) string { return environment[key] }); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
