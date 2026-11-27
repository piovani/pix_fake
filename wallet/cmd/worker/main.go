// Command worker is the wallet's second process. It applies what the SPI stream says about the payments the wallet sent,
// and runs the periodic jobs: resend what the SPI did not take, finish interrupted book transfers, report them, and
// reconcile the wallet's keys with the DICT every 6 hours. It also answers the payments and returns other banks send to
// the wallet and credits them when the SPI settles. For the MED, it follows the infraction reports and refunds the
// DICT has against the wallet: blocks the money reported, and returns it when a refund asks.
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
	"pixfake/wallet/internal/med"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
	"pixfake/wallet/internal/platform/telemetry"
)

const (
	everyTick = 5 * time.Second
	// A reconciliation that finds a difference asks the DICT for a CID file per key type, and the DICT allows 40 a day
	// (CIDS_FILES_WRITE): every 6 hours, with 5 key types, stays at 20 even when a difference persists.
	keysCheckEvery = 6 * time.Hour
)

func main() {
	cfg := config.Load()
	log, shutdown, err := telemetry.Setup(context.Background(), "wallet-worker", cfg.OTLP)
	if err != nil {
		slog.Error("telemetry", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(log)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			slog.Warn("telemetry flush", "err", err)
		}
	}()

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
	fraud := med.New(dict, accounts, payments, med.NewPostgres(pool))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("worker started", "spi", cfg.SPIBaseURL, "ispb", cfg.ISPB)
	var wg sync.WaitGroup
	wg.Add(3)
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
				sweep(ctx, "med-reports", fraud.SyncReports)
				sweep(ctx, "med-refunds", fraud.SyncRefunds)
				sweep(ctx, "med-returns", fraud.ProcessRefunds)
			}
		}
	}()
	go func() {
		defer wg.Done()
		tick := time.NewTicker(keysCheckEvery)
		defer tick.Stop()
		for {
			checkKeys(ctx, keys)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	wg.Wait()
	slog.Info("worker stopped")
}

// checkKeys logs only how many keys disagree, by kind: the keys are CPFs, emails and phones, kept out of the logs.
// `cmd/keys check` lists them.
func checkKeys(ctx context.Context, keys *pixkey.Service) {
	findings, err := keys.Check(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("job failed", "job", "keys-check", "err", err)
		}
		return
	}
	if len(findings) == 0 {
		return
	}
	count := map[pixkey.Divergence]int{}
	for _, f := range findings {
		count[f.Kind]++
	}
	slog.WarnContext(ctx, "keys of the wallet disagree with the DICT, see cmd/keys check", "count", len(findings),
		"missing_in_dict", count[pixkey.MissingInDict], "data_differs", count[pixkey.DataDiffers], "only_in_dict", count[pixkey.OnlyInDict])
}

func sweep(ctx context.Context, did string, job func(context.Context) (int, error)) {
	switch n, err := job(ctx); {
	case err != nil && ctx.Err() == nil:
		slog.Warn("job failed", "job", did, "err", err)
	case n > 0:
		slog.Info("job done", "job", did, "count", n)
	}
}
