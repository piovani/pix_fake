// Command spi runs the SPI mock (the Bacen instant payment system).
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

	"pixfake/mock/spi/internal/config"
	"pixfake/mock/spi/internal/httpapi"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/postgres"
	"pixfake/mock/spi/internal/spi"
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
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	repo := postgres.NewRepository(pool)
	svc := spi.NewService(repo, cfg.PollWait)
	proc := pix.NewProcessor(svc, repo, pix.DefaultParticipants(), pix.AccountResponder{Accounts: repo}, log)
	proc.Timeout = cfg.Timeout
	svc.SetHandler(proc)
	go proc.RunExpiry(ctx, min(time.Second, cfg.Timeout/2))
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(svc, log, pool.Ping).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// A read waits for messages (long polling), so the write timeout must exceed the wait.
		WriteTimeout: cfg.PollWait + 30*time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("SPI mock listening", "addr", cfg.Addr)
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
