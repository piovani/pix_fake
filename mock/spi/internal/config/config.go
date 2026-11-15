// Package config reads the service configuration from environment variables.
package config

import (
	"os"
	"time"
)

type Config struct {
	Addr        string        // SPI_ADDR
	DatabaseURL string        // SPI_DATABASE_URL
	PollWait    time.Duration // SPI_POLL_WAIT: how long a read waits for a message before answering 204
}

func Load() Config {
	wait, err := time.ParseDuration(getenv("SPI_POLL_WAIT", "5s"))
	if err != nil || wait < 0 {
		wait = 5 * time.Second
	}
	return Config{
		Addr:        getenv("SPI_ADDR", ":8081"),
		DatabaseURL: getenv("SPI_DATABASE_URL", "postgres://spi:spi@localhost:5433/spi?sslmode=disable"),
		PollWait:    wait,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
