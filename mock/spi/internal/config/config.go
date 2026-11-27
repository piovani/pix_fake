// Package config reads the service configuration from environment variables.
package config

import (
	"os"
	"time"
)

type Config struct {
	OTLP        string        // OTEL_EXPORTER_OTLP_ENDPOINT: where traces, metrics and logs go; empty sends nothing
	Addr        string        // SPI_ADDR
	DatabaseURL string        // SPI_DATABASE_URL
	PollWait    time.Duration // SPI_POLL_WAIT: how long a read waits for a message before answering 204
	Timeout     time.Duration // SPI_TIMEOUT: how long a payment waits for the receiving participant
	Retention   time.Duration // SPI_RETENTION: how long read messages are kept before they are deleted
	Control     bool          // SPI_CONTROL: serve the control API under /mock (on unless set to "off")
	DICTURL     string        // SPI_DICT_URL: the DICT mock told of every settled payment (MED); empty tells no one
}

func Load() Config {
	return Config{
		OTLP:        os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		PollWait:    duration("SPI_POLL_WAIT", 5*time.Second),
		Timeout:     duration("SPI_TIMEOUT", 40*time.Second),
		Retention:   duration("SPI_RETENTION", 7*24*time.Hour),
		Control:     os.Getenv("SPI_CONTROL") != "off",
		DICTURL:     os.Getenv("SPI_DICT_URL"),
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
