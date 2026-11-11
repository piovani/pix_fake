// Comando seed popula o banco do DICT mock com chaves de teste. É idempotente:
// pode ser executado quantas vezes for preciso (por exemplo, em `make setup`).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/dict/internal/config"
	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/postgres"
	"pixfake/mock/dict/internal/seed"
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
	// Garante o schema mesmo que o servidor ainda não tenha rodado.
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	res, err := dict.NewService(postgres.NewRepository(pool)).Seed(ctx, seed.Entries())
	if err != nil {
		return err
	}
	for _, key := range res.Created {
		log.Info("created", "key", key)
	}
	for _, key := range res.Existing {
		log.Info("already exists", "key", key)
	}
	log.Info("seed finished", "created", len(res.Created), "already_existing", len(res.Existing))
	return nil
}
