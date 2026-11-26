// Command dict runs the DICT mock (the Bacen Pix key directory).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/mock/dict/internal/config"
	"pixfake/mock/dict/internal/control"
	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/fault"
	"pixfake/mock/dict/internal/httpapi"
	"pixfake/mock/dict/internal/postgres"
	"pixfake/mock/dict/internal/seed"
	"pixfake/mock/dict/internal/telemetry"
)

func main() {
	cfg := config.Load()
	log, shutdown, err := telemetry.Setup(context.Background(), "dict-mock", cfg.OTLP)
	if err != nil {
		slog.Error("telemetry", "error", err)
		os.Exit(1)
	}
	err = run(cfg, log)
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(flushCtx)
	if err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
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
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	repo := postgres.NewRepository(pool)
	svc := dict.NewService(repo)
	faults := &fault.Set{}
	api := httpapi.New(svc, log, pool.Ping).WithFaults(faults)
	if cfg.Control {
		reseed := func(ctx context.Context) error { _, err := svc.Seed(ctx, seed.Entries()); return err }
		api.WithControl(control.New(repo, reseed, faults).WithTransactions(svc).Handler())
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           telemetry.Handler(api.Handler(), "dict-mock"),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("DICT mock listening", "addr", cfg.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
