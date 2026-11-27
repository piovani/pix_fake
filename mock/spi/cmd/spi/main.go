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
	"pixfake/mock/spi/internal/control"
	"pixfake/mock/spi/internal/dictfeed"
	"pixfake/mock/spi/internal/fault"
	"pixfake/mock/spi/internal/httpapi"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/postgres"
	"pixfake/mock/spi/internal/spi"
	"pixfake/mock/spi/internal/telemetry"
)

func main() {
	cfg := config.Load()
	log, shutdown, err := telemetry.Setup(context.Background(), "spi-mock", cfg.OTLP)
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
	svc := spi.NewService(repo, cfg.PollWait)
	participants := pix.DefaultParticipants()
	proc := pix.NewProcessor(svc, repo, participants, pix.AccountResponder{Accounts: repo}, log)
	proc.Timeout = cfg.Timeout
	svc.SetHandler(proc)
	go proc.RunExpiry(ctx, min(time.Second, cfg.Timeout/2))
	if cfg.DICTURL != "" {
		go dictfeed.New(cfg.DICTURL, repo, log).Run(ctx, time.Second)
		log.Info("telling the DICT of settled payments", "url", cfg.DICTURL)
	}
	go svc.RunRetention(ctx, time.Hour, cfg.Retention, func(deleted int64, err error) {
		if err != nil {
			log.Error("delete old messages", "error", err)
		} else if deleted > 0 {
			log.Info("deleted old messages", "count", deleted)
		}
	})
	faults := &fault.Set{}
	api := httpapi.New(svc, log, pool.Ping).WithParticipants(participants.Known).WithFaults(faults)
	if cfg.Control {
		api.WithControl(control.New(svc, repo, participants, faults).Handler())
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           telemetry.Handler(api.Handler(), "spi-mock"),
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
