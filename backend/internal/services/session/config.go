package session

import (
	"errors"
	"time"
)

type Config struct {
	IdleTimeout      time.Duration
	AbsoluteLifetime time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
}

func DefaultConfig() Config {
	return Config{
		IdleTimeout:      30 * time.Minute,
		AbsoluteLifetime: 8 * time.Hour,
		RotationInterval: 15 * time.Minute,
		GracePeriod:      30 * time.Second,
	}
}

func (config Config) validate() error {
	if config.IdleTimeout <= 0 || config.AbsoluteLifetime <= 0 ||
		config.RotationInterval <= 0 || config.GracePeriod <= 0 {
		return errors.New("session durations must be positive")
	}
	if config.IdleTimeout > config.AbsoluteLifetime ||
		config.RotationInterval >= config.AbsoluteLifetime ||
		config.GracePeriod >= config.RotationInterval {
		return errors.New("invalid session duration ordering")
	}
	return nil
}
