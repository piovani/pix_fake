// Command seed loads the test accounts into the SPI mock database. It is idempotent:
// it can be run as many times as needed (for example, in `make setup`).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/spi/internal/config"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/postgres"
	"pixfake/mock/spi/internal/seed"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	// Ensure the schema exists even if the server has not run yet.
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	res, err := pix.SeedAccounts(ctx, postgres.NewRepository(pool), seed.Accounts())
	if err != nil {
		return err
	}
	for _, id := range res.Created {
		log.Info("created", "account", id)
	}
	for _, id := range res.Existing {
		log.Info("already exists", "account", id)
	}
	log.Info("seed finished", "created", len(res.Created), "already_existing", len(res.Existing))
	return nil
}
