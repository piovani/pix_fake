// Package config reads the wallet's configuration from the environment. The mocks' URLs live here and nowhere else:
// switching to the real Bacen endpoints means changing these values.
package config

import "os"

type Config struct {
	GRPCAddr    string // where the gRPC API listens
	DatabaseURL string // the wallet's own database
	DictBaseURL string
	SPIBaseURL  string
	ISPB        string // the wallet's ISPB as a Pix participant
}

// Load reads the environment, with the defaults of the local environment started by `make up`.
func Load() Config {
	return Config{
		GRPCAddr:    env("WALLET_GRPC_ADDR", ":9090"),
		DatabaseURL: env("WALLET_DATABASE_URL", "postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable"),
		DictBaseURL: env("DICT_BASE_URL", "http://localhost:8080/api/v2"),
		SPIBaseURL:  env("SPI_BASE_URL", "http://localhost:8081/api/v1"),
		ISPB:        env("WALLET_ISPB", "99000001"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
