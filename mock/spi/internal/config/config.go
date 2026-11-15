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
	Timeout     time.Duration // SPI_TIMEOUT: how long a payment waits for the receiving participant
}

func Load() Config {
	return Config{
		PollWait:    duration("SPI_POLL_WAIT", 5*time.Second),
		Timeout:     duration("SPI_TIMEOUT", 40*time.Second),
		Addr:        getenv("SPI_ADDR", ":8081"),
		DatabaseURL: getenv("SPI_DATABASE_URL", "postgres://spi:spi@localhost:5433/spi?sslmode=disable"),
	}
}

// duration reads a duration such as "40s" or "500ms"; a missing or invalid value gives the fallback.
func duration(key string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(key))
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
