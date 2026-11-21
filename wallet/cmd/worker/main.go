// Command worker is the wallet's second process, started next to the API. It reads the SPI output stream (long polling,
// following PI-Pull-Next) and applies what comes back for the payments the wallet sent: settled or rejected, which moves
// the reserved money. It also sends again the payments the SPI has not accepted yet, such as the ones made while it was
// unreachable.
//
// Not handled yet, and skipped with a warning: payments received (pacs.008), returns (pacs.004) and the receipts of reports
// (camt.025).
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
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

const sendPendingEvery = 5 * time.Second

func main() {
	cfg := config.Load()

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to the database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	spi := pix.NewSPIClient(cfg.SPIBaseURL, cfg.ISPB)
	payments := payment.New(ledger.New(pool), pix.NewDictClient(cfg.DictBaseURL, cfg.ISPB), spi, payment.NewPostgres(pool), cfg.ISPB)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("worker started", "spi", cfg.SPIBaseURL, "ispb", cfg.ISPB)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := payments.Consume(ctx, spi); err != nil {
			slog.Error("reading the SPI stream", "err", err)
			stop()
		}
	}()
	go func() {
		defer wg.Done()
		tick := time.NewTicker(sendPendingEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if n, err := payments.SendPending(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("sending pending payments", "err", err)
				} else if n > 0 {
					slog.Info("sent pending payments", "count", n)
				}
			}
		}
	}()
	wg.Wait()
	slog.Info("worker stopped")
}
