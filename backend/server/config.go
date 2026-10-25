package server

import (
	"encoding/base64"
	"errors"
	"strconv"
)

type Config struct {
	Address                string
	PublicOrigin           string
	AllowInsecureLocalHTTP bool
	SessionEncryptionKey   []byte
}

func ReadConfig(getenv func(string) string) (Config, error) {
	config := Config{
		Address:      getenv("HTTP_ADDR"),
		PublicOrigin: getenv("PUBLIC_ORIGIN"),
	}
	if config.Address == "" {
		config.Address = ":8080"
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
	return config, nil
}
