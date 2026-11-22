// Command seed applies the migrations and loads the wallet's test accounts and their Pix keys. It is idempotent.
package main

import (
	"context"
	"log/slog"
	"os"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
	"pixfake/wallet/internal/seed"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := seed.Run(ctx, ledger.New(pool), pixkey.NewPostgres(pool)); err != nil {
		slog.Error("seed", "err", err)
		os.Exit(1)
	}
	slog.Info("seed done")
}
