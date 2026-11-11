// Package config lê a configuração do serviço a partir de variáveis de ambiente.
package config

import "os"

type Config struct {
	Addr        string // DICT_ADDR
	DatabaseURL string // DICT_DATABASE_URL
}

func Load() Config {
	return Config{
		Addr:        getenv("DICT_ADDR", ":8080"),
		DatabaseURL: getenv("DICT_DATABASE_URL", "postgres://dict:dict@localhost:5432/dict?sslmode=disable"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
