// Command worker is the wallet's second process, started next to the API. It will consume the SPI output stream
// (long polling, following PI-Pull-Next), answer what the SPI asks of the wallet, and handle payment timeouts and
// retries. For now it only starts and waits.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"pixfake/wallet/internal/platform/config"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("worker started", "spi", cfg.SPIBaseURL, "ispb", cfg.ISPB)
	<-ctx.Done()
	slog.Info("worker stopped")
}
