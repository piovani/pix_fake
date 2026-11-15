// Package config reads the service configuration from environment variables.
package config

import "os"

type Config struct {
	Addr        string // DICT_ADDR
	DatabaseURL string // DICT_DATABASE_URL
	Control     bool   // DICT_CONTROL: serve the control API under /mock (on unless set to "off")
}

func Load() Config {
	return Config{
		Addr:        getenv("DICT_ADDR", ":8080"),
		DatabaseURL: getenv("DICT_DATABASE_URL", "postgres://dict:dict@localhost:5432/dict?sslmode=disable"),
		Control:     os.Getenv("DICT_CONTROL") != "off",
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
