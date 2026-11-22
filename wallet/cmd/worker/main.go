// Command worker is the wallet's second process. It applies what the SPI stream says about the payments the wallet sent,
// and runs the periodic jobs: resend what the SPI did not take, finish interrupted book transfers, report them. It also
// answers the payments and returns other banks send to the wallet and credits them when the SPI settles.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

const everyTick = 5 * time.Second

func main() {
	cfg := config.Load()

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	accounts := ledger.New(pool)
	dict := pix.NewDictClient(cfg.DictBaseURL, cfg.ISPB)
	spi := pix.NewSPIClient(cfg.SPIBaseURL, cfg.ISPB)
	keys := pixkey.New(accounts, dict, pixkey.NewPostgres(pool), cfg.ISPB)
	payments := payment.New(accounts, keys, dict, spi, payment.NewPostgres(pool), cfg.ISPB)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("worker started", "spi", cfg.SPIBaseURL, "ispb", cfg.ISPB)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := payments.Consume(ctx, spi); err != nil {
			slog.Error("consume stopped", "err", err)
			stop()
		}
	}()
	go func() {
		defer wg.Done()
		tick := time.NewTicker(everyTick)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				sweep(ctx, "resend", payments.SendPending)
				sweep(ctx, "finish-book", payments.CompleteBookTransfers)
				sweep(ctx, "report-book", payments.ReportBookTransfers)
				sweep(ctx, "orphans", payments.ReleaseOrphans)
			}
		}
	}()
	wg.Wait()
	slog.Info("worker stopped")
}

func sweep(ctx context.Context, did string, job func(context.Context) (int, error)) {
	switch n, err := job(ctx); {
	case err != nil && ctx.Err() == nil:
		slog.Warn("job failed", "job", did, "err", err)
	case n > 0:
		slog.Info("job done", "job", did, "count", n)
	}
}
